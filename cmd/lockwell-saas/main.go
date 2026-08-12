package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/RusticStack/lockwell-saas/internal/accounts"
	"github.com/RusticStack/lockwell-saas/internal/billing"
	"github.com/RusticStack/lockwell-saas/internal/config"
	"github.com/RusticStack/lockwell-saas/internal/customer"
	"github.com/RusticStack/lockwell-saas/internal/entitlements"
	"github.com/RusticStack/lockwell-saas/internal/financial"
	"github.com/RusticStack/lockwell-saas/internal/httpapi"
	"github.com/RusticStack/lockwell-saas/internal/metering"
	"github.com/RusticStack/lockwell-saas/internal/operations"
	"github.com/RusticStack/lockwell-saas/internal/provisioning"
	"github.com/RusticStack/lockwell-saas/internal/store"
	"github.com/RusticStack/lockwell-saas/internal/usageingest"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	repo := store.Postgres{Pool: pool}
	accountService := accounts.Service{Repo: repo, TermsVersion: cfg.TermsVersion}
	if cfg.EmailEnabled {
		accountService.Mailer = accounts.ScalewayMailer{ProjectID: cfg.ScalewayProjectID, Region: "fr-par", AuthToken: cfg.ScalewayAuthToken, FromEmail: cfg.EmailFrom, FromName: cfg.EmailFromName, VerifyURL: cfg.EmailVerificationURL}
	}
	accountHTTP := accounts.HTTPHandler{Service: accountService}
	billingHTTP := billing.HTTPHandler{Service: billing.Service{
		Accounts: accountService,
		Repo:     repo,
		Stripe:   billing.StripeClient{APIKey: cfg.StripeAPIKey, APIVersion: cfg.StripeAPIVersion},
		PriceIDs: map[string]string{
			"starter":    cfg.StripeStarterPrice,
			"team":       cfg.StripeTeamPrice,
			"compliance": cfg.StripeCompliancePrice,
		},
		SuccessURL: cfg.CheckoutSuccessURL,
		CancelURL:  cfg.CheckoutCancelURL,
		PortalURL:  cfg.PortalReturnURL,
	}}
	meterWorker := metering.Worker{Repo: repo, Provider: billing.MeterProvider{Stripe: billing.StripeClient{APIKey: cfg.StripeAPIKey, APIVersion: cfg.StripeAPIVersion}}}
	go runMeterWorker(ctx, logger, meterWorker)
	entitlementWorker := entitlements.Worker{
		Repo: repo, Provider: billing.SubscriptionProvider{Stripe: billing.StripeClient{APIKey: cfg.StripeAPIKey, APIVersion: cfg.StripeAPIVersion}},
		AllowedPrices: map[string]string{"starter": cfg.StripeStarterPrice, "team": cfg.StripeTeamPrice, "compliance": cfg.StripeCompliancePrice},
	}
	go runEntitlementWorker(ctx, logger, entitlementWorker)
	go runFinancialWorker(ctx, logger, financial.Worker{Repo: repo, Provider: billing.FinancialProvider{Stripe: billing.StripeClient{APIKey: cfg.StripeAPIKey, APIVersion: cfg.StripeAPIVersion}}})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		probeCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := repo.Ping(probeCtx); err != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("GET /metrics", operations.MetricsHandler{Repo: repo, Token: cfg.MetricsToken})
	usageService := usageingest.Service{Repo: repo, Meters: map[metering.Metric]metering.MeterConfig{
		metering.StorageMiBHours: {EventName: cfg.StripeStorageEventName, MeterID: cfg.StripeStorageMeterID},
		metering.Operations:      {EventName: cfg.StripeOperationsEventName, MeterID: cfg.StripeOperationsMeterID},
		metering.EgressMiB:       {EventName: cfg.StripeEgressEventName, MeterID: cfg.StripeEgressMeterID},
	}}
	mux.HandleFunc("POST /internal/v1/usage-windows", usageingest.HTTPHandler{Service: usageService, Token: cfg.UsageIngestToken}.Record)
	mux.Handle("POST /webhooks/stripe", billing.WebhookHandler{Secret: cfg.StripeWebhookSecret, ExpectedAPIVersion: cfg.StripeAPIVersion, Recorder: repo})
	mux.HandleFunc("POST /v1/accounts/signup", accountHTTP.Signup)
	mux.HandleFunc("POST /v1/accounts/login", accountHTTP.Login)
	mux.HandleFunc("POST /v1/accounts/verification/request", accountHTTP.RequestVerification)
	mux.HandleFunc("POST /v1/accounts/verification/confirm", accountHTTP.VerifyEmail)
	mux.HandleFunc("GET /v1/customer/status", customer.HTTPHandler{Accounts: accountService, Repo: repo}.Status)
	mux.HandleFunc("POST /v1/billing/checkout", billingHTTP.Checkout)
	mux.HandleFunc("POST /v1/billing/portal", billingHTTP.Portal)
	if cfg.ProvisioningEnabled {
		provisionRepo := provisioning.Postgres{Pool: pool}
		if err := provisionRepo.UpsertCell(ctx, provisioning.Reservation{CellID: cfg.CellID, Region: cfg.ScalewayRegion, PublicEndpoint: cfg.CellPublicEndpoint, AdminEndpoint: cfg.CellAdminEndpoint, AdminSecretRef: cfg.CellAdminSecretRef}, cfg.CellCapacity); err != nil {
			logger.Error("configure hosted cell", "error", err)
			os.Exit(1)
		}
		vault := provisioning.ScalewayVault{ProjectID: cfg.ScalewayProjectID, Region: cfg.ScalewayRegion, AuthToken: cfg.ScalewayAuthToken}
		provisionHTTP := provisioning.HTTPHandler{Accounts: accountService, Service: provisioning.Service{Repo: provisionRepo, Cells: provisioning.LockwellCell{}, Vault: vault, PlanQuotas: map[string]int64{"starter": cfg.StarterQuotaBytes, "team": cfg.TeamQuotaBytes, "compliance": cfg.ComplianceQuotaBytes}}}
		go runEnforcementWorker(ctx, logger, provisioning.EnforcementWorker{Repo: provisionRepo, Vault: vault, Cells: provisioning.LockwellCell{}})
		mux.HandleFunc("POST /v1/provisioning/credentials", provisionHTTP.RequestCredential)
		mux.HandleFunc("POST /v1/provisioning/redeem", provisionHTTP.RedeemCredential)
	}

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           httpapi.CustomerCORS(cfg.CustomerOrigin, mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	logger.Info("hosted control plane listening", "addr", cfg.ListenAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serve", "error", err)
		os.Exit(1)
	}
}

func runFinancialWorker(ctx context.Context, logger *slog.Logger, worker financial.Worker) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		processed, err := worker.RunOnce(ctx)
		if err != nil {
			logger.Warn("Stripe financial reconciliation pending", "error", err)
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runEnforcementWorker(ctx context.Context, logger *slog.Logger, worker provisioning.EnforcementWorker) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		processed, err := worker.RunOnce(ctx)
		if err != nil {
			logger.Warn("cell entitlement enforcement pending", "error", err)
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runEntitlementWorker(ctx context.Context, logger *slog.Logger, worker entitlements.Worker) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		processed, err := worker.RunOnce(ctx)
		if err != nil {
			logger.Warn("Stripe entitlement event pending", "error", err)
		}
		if processed {
			continue
		}
		expired, err := worker.ExpireGraceOnce(ctx)
		if err != nil {
			logger.Warn("entitlement grace expiry failed", "error", err)
		}
		if expired {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runMeterWorker(ctx context.Context, logger *slog.Logger, worker metering.Worker) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		processed, err := worker.RunOnce(ctx)
		if err != nil {
			logger.Warn("meter export failed", "error", err)
		}
		if processed {
			continue
		}
		reconciled, err := worker.ReconcileOnce(ctx)
		if err != nil {
			logger.Warn("meter reconciliation pending", "error", err)
		}
		if reconciled {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
