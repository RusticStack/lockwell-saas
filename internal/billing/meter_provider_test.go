package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RusticStack/lockwell-saas/internal/metering"
)

func TestMeterProviderSendsCanonicalIntegerEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing/meter_events" || r.Header.Get("Idempotency-Key") != "lw_identifier" {
			t.Fatalf("path/idempotency = %s/%q", r.URL.Path, r.Header.Get("Idempotency-Key"))
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("payload[stripe_customer_id]") != "cus_1" || r.Form.Get("payload[value]") != "42" || r.Form.Get("timestamp") != "1700000000" {
			t.Fatalf("form = %#v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"identifier": "lw_identifier", "event_name": "lockwell_operations_v1"})
	}))
	defer server.Close()
	provider := MeterProvider{Stripe: StripeClient{APIKey: "sk_test", APIVersion: "2026-06-30", BaseURL: server.URL, HTTPClient: server.Client()}}
	err := provider.SendMeterEvent(context.Background(), metering.Export{EventName: "lockwell_operations_v1", Identifier: "lw_identifier", StripeCustomerID: "cus_1", Value: 42, WindowEnd: time.Unix(1_700_000_000, 0)})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMeterProviderReadsBoundedSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing/meters/mtr_1/event_summaries" || r.URL.Query().Get("customer") != "cus_1" {
			t.Fatalf("path/query = %s / %s", r.URL.Path, r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"aggregated_value": 20}, {"aggregated_value": 22}}, "has_more": false})
	}))
	defer server.Close()
	provider := MeterProvider{Stripe: StripeClient{APIKey: "sk_test", APIVersion: "2026-06-30", BaseURL: server.URL, HTTPClient: server.Client()}}
	start := time.Unix(1_700_000_000, 0).UTC().Truncate(time.Minute)
	total, err := provider.ReadMeterSummary(context.Background(), metering.Export{MeterID: "mtr_1", StripeCustomerID: "cus_1", WindowStart: start, WindowEnd: start.Add(time.Hour)})
	if err != nil || total != 42 {
		t.Fatalf("total = %d, err = %v", total, err)
	}
}
