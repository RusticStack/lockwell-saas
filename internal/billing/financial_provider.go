package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RusticStack/lockwell-saas/internal/financial"
)

type FinancialProvider struct{ Stripe StripeClient }

func (p FinancialProvider) RetrieveInvoice(ctx context.Context, id string) (financial.Invoice, error) {
	if !strings.HasPrefix(id, "in_") {
		return financial.Invoice{}, financial.ErrInvalidFinancialRecord
	}
	var value stripeInvoice
	if err := p.get(ctx, "/v1/invoices/"+url.PathEscape(id), nil, &value); err != nil {
		return financial.Invoice{}, err
	}
	lines, err := p.invoiceLines(ctx, id)
	if err != nil {
		return financial.Invoice{}, err
	}
	tax := int64(0)
	taxAmounts := make([]financial.TaxAmount, 0, len(value.TotalTaxes))
	for _, item := range value.TotalTaxes {
		tax += item.Amount
		taxAmounts = append(taxAmounts, financial.TaxAmount{Amount: item.Amount, TaxableAmount: item.TaxableAmount, Inclusive: item.Inclusive, TaxRateID: item.TaxRate, TaxabilityReason: item.TaxabilityReason})
	}
	evidence := financial.InvoiceTaxEvidence{AutomaticTaxEnabled: value.AutomaticTax.Enabled, AutomaticTaxStatus: value.AutomaticTax.Status, CustomerTaxExempt: value.CustomerTaxExempt, CustomerCountry: strings.ToUpper(value.CustomerAddress.Country), CustomerState: value.CustomerAddress.State, CustomerPostalCode: value.CustomerAddress.PostalCode, Amounts: taxAmounts}
	return financial.Invoice{ID: value.ID, AccountID: value.Metadata["account_id"], CustomerID: value.Customer, SubscriptionID: value.subscriptionID(), Currency: strings.ToLower(value.Currency), Status: value.Status, HostedURL: value.HostedInvoiceURL, PDF: value.InvoicePDF, Subtotal: value.Subtotal, Tax: tax, Total: value.Total, AmountPaid: value.AmountPaid, AmountRemaining: value.AmountRemaining, CreatedAt: time.Unix(value.Created, 0).UTC(), Lines: lines, TaxEvidence: evidence}, nil
}

func (p FinancialProvider) RetrieveRefund(ctx context.Context, id string) (financial.Refund, error) {
	if !strings.HasPrefix(id, "re_") {
		return financial.Refund{}, financial.ErrInvalidFinancialRecord
	}
	var refund struct {
		ID            string            `json:"id"`
		Charge        string            `json:"charge"`
		PaymentIntent string            `json:"payment_intent"`
		Currency      string            `json:"currency"`
		Status        string            `json:"status"`
		Reason        string            `json:"reason"`
		Amount        int64             `json:"amount"`
		Created       int64             `json:"created"`
		Metadata      map[string]string `json:"metadata"`
	}
	if err := p.get(ctx, "/v1/refunds/"+url.PathEscape(id), nil, &refund); err != nil {
		return financial.Refund{}, err
	}
	if refund.Charge == "" {
		return financial.Refund{}, errors.New("Stripe refund has no charge")
	}
	var charge struct {
		ID       string `json:"id"`
		Customer string `json:"customer"`
		Invoice  string `json:"invoice"`
	}
	if err := p.get(ctx, "/v1/charges/"+url.PathEscape(refund.Charge), nil, &charge); err != nil {
		return financial.Refund{}, err
	}
	return financial.Refund{ID: refund.ID, AccountID: refund.Metadata["account_id"], CustomerID: charge.Customer, InvoiceID: charge.Invoice, ChargeID: charge.ID, PaymentIntentID: refund.PaymentIntent, Currency: strings.ToLower(refund.Currency), Status: refund.Status, Reason: refund.Reason, Amount: refund.Amount, CreatedAt: time.Unix(refund.Created, 0).UTC()}, nil
}

type stripeInvoice struct {
	ID               string            `json:"id"`
	Customer         string            `json:"customer"`
	Currency         string            `json:"currency"`
	Status           string            `json:"status"`
	HostedInvoiceURL string            `json:"hosted_invoice_url"`
	InvoicePDF       string            `json:"invoice_pdf"`
	Created          int64             `json:"created"`
	Subtotal         int64             `json:"subtotal"`
	Total            int64             `json:"total"`
	AmountPaid       int64             `json:"amount_paid"`
	AmountRemaining  int64             `json:"amount_remaining"`
	Metadata         map[string]string `json:"metadata"`
	Subscription     string            `json:"subscription"`
	Parent           struct {
		SubscriptionDetails struct {
			Subscription string `json:"subscription"`
		} `json:"subscription_details"`
	} `json:"parent"`
	TotalTaxes []struct {
		Amount           int64  `json:"amount"`
		TaxableAmount    int64  `json:"taxable_amount"`
		Inclusive        bool   `json:"inclusive"`
		TaxRate          string `json:"tax_rate"`
		TaxabilityReason string `json:"taxability_reason"`
	} `json:"total_taxes"`
	AutomaticTax struct {
		Enabled bool   `json:"enabled"`
		Status  string `json:"status"`
	} `json:"automatic_tax"`
	CustomerTaxExempt string `json:"customer_tax_exempt"`
	CustomerAddress   struct {
		Country    string `json:"country"`
		State      string `json:"state"`
		PostalCode string `json:"postal_code"`
	} `json:"customer_address"`
}

func (v stripeInvoice) subscriptionID() string {
	if v.Subscription != "" {
		return v.Subscription
	}
	return v.Parent.SubscriptionDetails.Subscription
}

func (p FinancialProvider) invoiceLines(ctx context.Context, invoiceID string) ([]financial.InvoiceLine, error) {
	var result []financial.InvoiceLine
	cursor := ""
	for page := 0; page < 100; page++ {
		query := url.Values{"limit": {"100"}}
		if cursor != "" {
			query.Set("starting_after", cursor)
		}
		var list struct {
			Data []struct {
				ID          string `json:"id"`
				Description string `json:"description"`
				Currency    string `json:"currency"`
				Amount      int64  `json:"amount"`
				Quantity    int64  `json:"quantity"`
				Period      struct {
					Start int64 `json:"start"`
					End   int64 `json:"end"`
				} `json:"period"`
				Pricing struct {
					PriceDetails struct {
						Price string `json:"price"`
					} `json:"price_details"`
				} `json:"pricing"`
			} `json:"data"`
			HasMore bool `json:"has_more"`
		}
		if err := p.get(ctx, "/v1/invoices/"+url.PathEscape(invoiceID)+"/lines", query, &list); err != nil {
			return nil, err
		}
		for _, line := range list.Data {
			result = append(result, financial.InvoiceLine{ID: line.ID, PriceID: line.Pricing.PriceDetails.Price, Description: line.Description, Currency: strings.ToLower(line.Currency), Amount: line.Amount, Quantity: line.Quantity, PeriodStart: unixOrZero(line.Period.Start), PeriodEnd: unixOrZero(line.Period.End)})
		}
		if !list.HasMore {
			return result, nil
		}
		if len(list.Data) == 0 {
			return nil, errors.New("Stripe invoice line pagination made no progress")
		}
		cursor = list.Data[len(list.Data)-1].ID
	}
	return nil, errors.New("Stripe invoice line pagination exceeded 100 pages")
}

func (p FinancialProvider) get(ctx context.Context, path string, query url.Values, target any) error {
	base := strings.TrimRight(p.Stripe.BaseURL, "/")
	if base == "" {
		base = "https://api.stripe.com"
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(p.Stripe.APIKey, "")
	req.Header.Set("Stripe-Version", p.Stripe.APIVersion)
	client := p.Stripe.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Stripe API returned status %d", resp.StatusCode)
	}
	if err = json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode Stripe response: %w", err)
	}
	return nil
}
func unixOrZero(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}
