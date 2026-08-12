package operations

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
)

type Snapshot struct {
	Accounts                int64
	ActiveEntitlements      int64
	GraceEntitlements       int64
	SuspendedEntitlements   int64
	ReadyProvisions         int64
	FailedProvisions        int64
	PendingOutboxJobs       int64
	ClaimedOutboxJobs       int64
	DeadLetterOutboxJobs    int64
	PendingMeterExports     int64
	DeadLetterMeterExports  int64
	UnprocessedStripeEvents int64
	ReconciledInvoices      int64
	ReconciledRefunds       int64
}

type Repository interface {
	OperationalSnapshot(context.Context) (Snapshot, error)
}

type MetricsHandler struct {
	Repo  Repository
	Token string
}

func (h MetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !authorized(r, h.Token) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	snapshot, err := h.Repo.OperationalSnapshot(r.Context())
	if err != nil {
		http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	metrics := []struct {
		name  string
		help  string
		value int64
	}{
		{"lockwell_saas_accounts", "Registered customer accounts.", snapshot.Accounts},
		{"lockwell_saas_entitlements_active", "Active hosted entitlements.", snapshot.ActiveEntitlements},
		{"lockwell_saas_entitlements_grace", "Hosted entitlements in payment grace.", snapshot.GraceEntitlements},
		{"lockwell_saas_entitlements_suspended", "Suspended hosted entitlements.", snapshot.SuspendedEntitlements},
		{"lockwell_saas_provisions_ready", "Ready tenant provisions.", snapshot.ReadyProvisions},
		{"lockwell_saas_provisions_failed", "Failed tenant provisions.", snapshot.FailedProvisions},
		{"lockwell_saas_outbox_pending", "Unclaimed control-plane outbox jobs.", snapshot.PendingOutboxJobs},
		{"lockwell_saas_outbox_claimed", "Currently claimed control-plane outbox jobs.", snapshot.ClaimedOutboxJobs},
		{"lockwell_saas_outbox_dead_letter", "Dead-lettered control-plane outbox jobs.", snapshot.DeadLetterOutboxJobs},
		{"lockwell_saas_meter_exports_pending", "Pending Stripe meter exports.", snapshot.PendingMeterExports},
		{"lockwell_saas_meter_exports_dead_letter", "Dead-lettered Stripe meter exports.", snapshot.DeadLetterMeterExports},
		{"lockwell_saas_stripe_events_unprocessed", "Verified Stripe events not fully processed.", snapshot.UnprocessedStripeEvents},
		{"lockwell_saas_invoices_reconciled", "Authoritative invoice projections.", snapshot.ReconciledInvoices},
		{"lockwell_saas_refunds_reconciled", "Authoritative refund projections.", snapshot.ReconciledRefunds},
	}
	for _, metric := range metrics {
		_, _ = fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", metric.name, metric.help, metric.name, metric.name, metric.value)
	}
}

func authorized(r *http.Request, expected string) bool {
	scheme, token, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || expected == "" {
		return false
	}
	want := sha256.Sum256([]byte(expected))
	got := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}
