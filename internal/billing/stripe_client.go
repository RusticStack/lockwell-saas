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
)

type StripeClient struct {
	APIKey     string
	APIVersion string
	BaseURL    string
	HTTPClient *http.Client
}

type CheckoutRequest struct {
	CustomerID     string
	CustomerEmail  string
	PriceID        string
	AccountID      string
	PlanCode       string
	SuccessURL     string
	CancelURL      string
	IdempotencyKey string
}

type CheckoutSession struct {
	ID         string `json:"id"`
	URL        string `json:"url"`
	CustomerID string `json:"customer"`
}

type PortalSession struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

func (c StripeClient) CreateCustomer(ctx context.Context, email, accountID, idempotencyKey string) (string, error) {
	values := url.Values{"email": {email}, "metadata[lockwell_account_id]": {accountID}}
	var response struct {
		ID string `json:"id"`
	}
	if err := c.postForm(ctx, "/v1/customers", values, idempotencyKey, &response); err != nil {
		return "", err
	}
	if response.ID == "" {
		return "", errors.New("Stripe customer response missing ID")
	}
	return response.ID, nil
}

func (c StripeClient) CreateCheckoutSession(ctx context.Context, request CheckoutRequest) (CheckoutSession, error) {
	values := url.Values{
		"mode":                                    {"subscription"},
		"customer":                                {request.CustomerID},
		"line_items[0][price]":                    {request.PriceID},
		"line_items[0][quantity]":                 {"1"},
		"success_url":                             {request.SuccessURL},
		"cancel_url":                              {request.CancelURL},
		"automatic_tax[enabled]":                  {"true"},
		"tax_id_collection[enabled]":              {"true"},
		"billing_address_collection":              {"required"},
		"customer_update[address]":                {"auto"},
		"customer_update[name]":                   {"auto"},
		"client_reference_id":                     {request.AccountID},
		"metadata[lockwell_account_id]":           {request.AccountID},
		"metadata[lockwell_plan_code]":            {request.PlanCode},
		"subscription_data[metadata][account_id]": {request.AccountID},
		"subscription_data[metadata][plan_code]":  {request.PlanCode},
	}
	var response CheckoutSession
	if err := c.postForm(ctx, "/v1/checkout/sessions", values, request.IdempotencyKey, &response); err != nil {
		return CheckoutSession{}, err
	}
	if response.ID == "" || response.URL == "" || response.CustomerID != request.CustomerID {
		return CheckoutSession{}, errors.New("Stripe checkout response failed identity checks")
	}
	return response, nil
}

func (c StripeClient) CreatePortalSession(ctx context.Context, customerID, returnURL, idempotencyKey string) (PortalSession, error) {
	values := url.Values{"customer": {customerID}, "return_url": {returnURL}}
	var response PortalSession
	if err := c.postForm(ctx, "/v1/billing_portal/sessions", values, idempotencyKey, &response); err != nil {
		return PortalSession{}, err
	}
	if response.ID == "" || response.URL == "" {
		return PortalSession{}, errors.New("Stripe portal response missing fields")
	}
	return response, nil
}

func (c StripeClient) postForm(ctx context.Context, path string, values url.Values, idempotencyKey string, target any) error {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "https://api.stripe.com"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	request.SetBasicAuth(c.APIKey, "")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.Header.Set("Stripe-Version", c.APIVersion)
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Stripe API returned status %d", response.StatusCode)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode Stripe response: %w", err)
	}
	return nil
}
