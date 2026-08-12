package billing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFinancialProviderRetrievesFullInvoiceAndRefund(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Stripe-Version") != "2026-06-30" {
			t.Fatalf("Stripe-Version=%q", r.Header.Get("Stripe-Version"))
		}
		switch r.URL.Path {
		case "/v1/invoices/in_1":
			_, _ = w.Write([]byte(`{"id":"in_1","customer":"cus_1","currency":"EUR","status":"paid","created":1700000000,"subtotal":1000,"total":1230,"amount_paid":1230,"amount_remaining":0,"metadata":{"account_id":"acc_1"},"parent":{"subscription_details":{"subscription":"sub_1"}},"automatic_tax":{"enabled":true,"status":"complete"},"customer_tax_exempt":"none","customer_address":{"country":"PT","state":"Lisboa","postal_code":"1000-001"},"total_taxes":[{"amount":230,"taxable_amount":1000,"inclusive":false,"tax_rate":"txr_1","taxability_reason":"standard_rated"}]}`))
		case "/v1/invoices/in_1/lines":
			_, _ = w.Write([]byte(`{"data":[{"id":"il_1","description":"Starter","currency":"eur","amount":1000,"quantity":1,"period":{"start":1700000000,"end":1702592000},"pricing":{"price_details":{"price":"price_1"}}}],"has_more":false}`))
		case "/v1/refunds/re_1":
			_, _ = w.Write([]byte(`{"id":"re_1","charge":"ch_1","payment_intent":"pi_1","currency":"eur","status":"succeeded","reason":"requested_by_customer","amount":1230,"created":1700000100,"metadata":{"account_id":"acc_1"}}`))
		case "/v1/charges/ch_1":
			_, _ = w.Write([]byte(`{"id":"ch_1","customer":"cus_1","invoice":"in_1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := FinancialProvider{Stripe: StripeClient{APIKey: "sk_test", APIVersion: "2026-06-30", BaseURL: server.URL, HTTPClient: server.Client()}}
	invoice, err := p.RetrieveInvoice(context.Background(), "in_1")
	if err != nil {
		t.Fatal(err)
	}
	if invoice.AccountID != "acc_1" || invoice.SubscriptionID != "sub_1" || invoice.Tax != 230 || len(invoice.Lines) != 1 || invoice.Lines[0].PriceID != "price_1" || !invoice.TaxEvidence.AutomaticTaxEnabled || invoice.TaxEvidence.CustomerCountry != "PT" || len(invoice.TaxEvidence.Amounts) != 1 {
		t.Fatalf("invoice=%#v", invoice)
	}
	refund, err := p.RetrieveRefund(context.Background(), "re_1")
	if err != nil {
		t.Fatal(err)
	}
	if refund.AccountID != "acc_1" || refund.CustomerID != "cus_1" || refund.InvoiceID != "in_1" || refund.Amount != 1230 {
		t.Fatalf("refund=%#v", refund)
	}
}

func TestFinancialProviderRejectsInvalidResourcePrefix(t *testing.T) {
	p := FinancialProvider{}
	if _, err := p.RetrieveInvoice(context.Background(), "re_wrong"); err == nil {
		t.Fatal("expected invoice prefix denial")
	}
	if _, err := p.RetrieveRefund(context.Background(), "in_wrong"); err == nil {
		t.Fatal("expected refund prefix denial")
	}
}
