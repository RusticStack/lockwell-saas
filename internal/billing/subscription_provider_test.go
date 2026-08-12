package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubscriptionProviderRetrievesBoundIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/subscriptions/sub_1" || r.Header.Get("Stripe-Version") != "2026-06-30" {
			t.Fatalf("request = %s version=%q", r.URL.Path, r.Header.Get("Stripe-Version"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "sub_1", "customer": "cus_1", "status": "active", "cancel_at_period_end": false,
			"metadata": map[string]string{"account_id": "acct_1", "plan_code": "starter"},
			"items":    map[string]any{"data": []any{map[string]any{"current_period_end": 1_800_000_000, "price": map[string]string{"id": "price_starter"}}}},
		})
	}))
	defer server.Close()
	provider := SubscriptionProvider{Stripe: StripeClient{APIKey: "sk_test", APIVersion: "2026-06-30", BaseURL: server.URL, HTTPClient: server.Client()}}
	subscription, err := provider.RetrieveSubscription(context.Background(), "sub_1")
	if err != nil || subscription.AccountID != "acct_1" || subscription.PriceID != "price_starter" || subscription.CurrentPeriodEnd.Unix() != 1_800_000_000 {
		t.Fatalf("subscription=%#v err=%v", subscription, err)
	}
}
