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
	"github.com/RusticStack/lockwell-saas/internal/store"
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
	mux.Handle("POST /webhooks/stripe", billing.WebhookHandler{Secret: cfg.StripeWebhookSecret, ExpectedAPIVersion: cfg.StripeAPIVersion, Recorder: repo})
	mux.HandleFunc("POST /v1/accounts/signup", accountHTTP.Signup)
	mux.HandleFunc("POST /v1/accounts/login", accountHTTP.Login)
	mux.HandleFunc("POST /v1/billing/checkout", billingHTTP.Checkout)
	mux.HandleFunc("POST /v1/billing/portal", billingHTTP.Portal)

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
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
