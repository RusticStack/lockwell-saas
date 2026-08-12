package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateCheckoutSessionBindsTaxCustomerPlanAndIdempotency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/checkout/sessions" || r.Header.Get("Idempotency-Key") != "checkout:acct:starter:req" {
			t.Fatalf("request path/idempotency = %s / %q", r.URL.Path, r.Header.Get("Idempotency-Key"))
		}
		if got := r.Header.Get("Stripe-Version"); got != "2026-06-30" {
			t.Fatalf("Stripe-Version = %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		checks := map[string]string{
			"mode":                         "subscription",
			"customer":                     "cus_1",
			"line_items[0][price]":         "price_starter",
			"automatic_tax[enabled]":       "true",
			"tax_id_collection[enabled]":   "true",
			"client_reference_id":          "acct",
			"metadata[lockwell_plan_code]": "starter",
		}
		for key, want := range checks {
			if got := r.Form.Get(key); got != want {
				t.Fatalf("%s = %q, want %q", key, got, want)
			}
		}
		_ = json.NewEncoder(w).Encode(CheckoutSession{ID: "cs_1", URL: "https://checkout.stripe.test/cs_1", CustomerID: "cus_1"})
	}))
	defer server.Close()
	client := StripeClient{APIKey: "sk_test", APIVersion: "2026-06-30", BaseURL: server.URL, HTTPClient: server.Client()}
	result, err := client.CreateCheckoutSession(context.Background(), CheckoutRequest{CustomerID: "cus_1", PriceID: "price_starter", AccountID: "acct", PlanCode: "starter", SuccessURL: "https://app.test/success", CancelURL: "https://app.test/cancel", IdempotencyKey: "checkout:acct:starter:req"})
	if err != nil || result.ID != "cs_1" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}
