package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerifyStripeReadinessAcceptsExactTestInventory(t *testing.T) {
	events := append([]string(nil), RequiredWebhookEvents...)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, _, ok := r.BasicAuth(); !ok || user != "sk_test_secret" || r.Header.Get("Stripe-Version") != "2026-06-30" {
			t.Fatal("missing Stripe authentication or version")
		}
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var response any
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/prices/"):
			response = map[string]any{"id": id, "active": true, "livemode": false, "currency": "eur", "type": "recurring", "recurring": map[string]any{"interval": "month", "interval_count": 1}}
		case strings.HasPrefix(r.URL.Path, "/v1/billing/meters/"):
			metric := strings.TrimPrefix(id, "mtr_")
			response = map[string]any{"id": id, "event_name": "lockwell_" + metric, "status": "active", "livemode": false, "default_aggregation": map[string]string{"formula": "sum"}, "customer_mapping": map[string]string{"type": "by_id", "event_payload_key": "stripe_customer_id"}, "value_settings": map[string]string{"event_payload_key": "value"}}
		case strings.HasPrefix(r.URL.Path, "/v1/billing_portal/configurations/"):
			response = map[string]any{"id": id, "active": true, "livemode": false}
		case strings.HasPrefix(r.URL.Path, "/v1/webhook_endpoints/"):
			response = map[string]any{"id": id, "url": "https://saas.example.test/webhooks/stripe", "status": "enabled", "api_version": "2026-06-30", "livemode": false, "enabled_events": events}
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	cfg := testReadinessConfig()
	cfg.BaseURL = server.URL
	report, err := VerifyStripeReadiness(context.Background(), cfg, server.Client())
	if err != nil || !report.Ready || report.Mode != "test" {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
}

func TestVerifyStripeReadinessFailsClosed(t *testing.T) {
	cfg := testReadinessConfig()
	cfg.APIKey = "sk_live_forbidden"
	if _, err := VerifyStripeReadiness(context.Background(), cfg, http.DefaultClient); err == nil || !strings.Contains(err.Error(), "test-mode") {
		t.Fatalf("expected live-key denial, got %v", err)
	}
	cfg = testReadinessConfig()
	cfg.MeterIDs["egress"] = cfg.MeterIDs["storage"]
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "distinct") {
		t.Fatalf("expected duplicate-meter denial, got %v", err)
	}
}

func TestVerifyStripeReadinessRejectsLiveInventory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "active": true, "livemode": true, "currency": "eur", "type": "recurring", "recurring": map[string]any{"interval": "month", "interval_count": 1}})
	}))
	defer server.Close()
	cfg := testReadinessConfig()
	cfg.BaseURL = server.URL
	if _, err := VerifyStripeReadiness(context.Background(), cfg, server.Client()); err == nil || !strings.Contains(err.Error(), "test-mode") {
		t.Fatalf("expected live-inventory denial, got %v", err)
	}
}

func testReadinessConfig() ReadinessConfig {
	return ReadinessConfig{
		APIKey: "sk_test_secret", APIVersion: "2026-06-30",
		PriceIDs:        map[string]string{"starter": "price_starter", "team": "price_team", "compliance": "price_compliance"},
		MeterIDs:        map[string]string{"storage": "mtr_storage", "operations": "mtr_operations", "egress": "mtr_egress"},
		MeterEventNames: map[string]string{"storage": "lockwell_storage", "operations": "lockwell_operations", "egress": "lockwell_egress"},
		PortalConfigID:  "bpc_test", WebhookID: "we_test", WebhookURL: "https://saas.example.test/webhooks/stripe",
	}
}
