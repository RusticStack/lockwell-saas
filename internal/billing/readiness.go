package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

var RequiredWebhookEvents = []string{
	"checkout.session.completed",
	"customer.subscription.created",
	"customer.subscription.deleted",
	"customer.subscription.updated",
	"invoice.finalization_failed",
	"invoice.finalized",
	"invoice.marked_uncollectible",
	"invoice.paid",
	"invoice.payment_action_required",
	"invoice.payment_failed",
	"invoice.voided",
	"refund.created",
	"refund.failed",
	"refund.updated",
}

type ReadinessConfig struct {
	APIKey          string
	APIVersion      string
	BaseURL         string
	PriceIDs        map[string]string
	MeterIDs        map[string]string
	MeterEventNames map[string]string
	PortalConfigID  string
	WebhookID       string
	WebhookURL      string
}

type ReadinessReport struct {
	Ready          bool              `json:"ready"`
	Mode           string            `json:"mode"`
	APIVersion     string            `json:"api_version"`
	Prices         map[string]string `json:"prices"`
	Meters         map[string]string `json:"meters"`
	PortalConfigID string            `json:"portal_config_id"`
	WebhookID      string            `json:"webhook_id"`
}

func (c ReadinessConfig) Validate() error {
	if !strings.HasPrefix(c.APIKey, "sk_test_") {
		return errors.New("Stripe readiness requires a test-mode secret key")
	}
	if strings.TrimSpace(c.APIVersion) == "" {
		return errors.New("Stripe API version is required")
	}
	if err := validateNamedIDs("price", c.PriceIDs); err != nil {
		return err
	}
	if err := validateNamedIDs("meter", c.MeterIDs); err != nil {
		return err
	}
	for _, plan := range []string{"starter", "team", "compliance"} {
		if c.PriceIDs[plan] == "" {
			return fmt.Errorf("Stripe price ID for %s is required", plan)
		}
	}
	for _, metric := range []string{"storage", "operations", "egress"} {
		if c.MeterIDs[metric] == "" || strings.TrimSpace(c.MeterEventNames[metric]) == "" {
			return fmt.Errorf("Stripe meter ID and event name for %s are required", metric)
		}
	}
	if c.PortalConfigID == "" || c.WebhookID == "" {
		return errors.New("Stripe portal configuration and webhook endpoint IDs are required")
	}
	parsed, err := url.Parse(c.WebhookURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("Stripe webhook URL must be an exact HTTPS URL without credentials, query, or fragment")
	}
	return nil
}

func validateNamedIDs(kind string, values map[string]string) error {
	seen := map[string]struct{}{}
	for name, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("Stripe %s IDs must be distinct", kind)
		}
		seen[value] = struct{}{}
		values[name] = value
	}
	return nil
}

func VerifyStripeReadiness(ctx context.Context, cfg ReadinessConfig, client *http.Client) (ReadinessReport, error) {
	if err := cfg.Validate(); err != nil {
		return ReadinessReport{}, err
	}
	if client == nil {
		return ReadinessReport{}, errors.New("bounded HTTP client is required")
	}
	report := ReadinessReport{Mode: "test", APIVersion: cfg.APIVersion, Prices: map[string]string{}, Meters: map[string]string{}, PortalConfigID: cfg.PortalConfigID, WebhookID: cfg.WebhookID}
	for plan, id := range cfg.PriceIDs {
		var price struct {
			ID               string `json:"id"`
			Active, Livemode bool
			Currency, Type   string
			Recurring        *struct {
				Interval      string `json:"interval"`
				IntervalCount int    `json:"interval_count"`
			} `json:"recurring"`
		}
		if err := stripeReadinessGET(ctx, cfg, client, "/v1/prices/"+url.PathEscape(id), &price); err != nil {
			return report, fmt.Errorf("price %s: %w", plan, err)
		}
		if price.ID != id || !price.Active || price.Livemode || price.Currency != "eur" || price.Type != "recurring" || price.Recurring == nil || price.Recurring.Interval != "month" || price.Recurring.IntervalCount != 1 {
			return report, fmt.Errorf("price %s failed active monthly EUR test-mode checks", plan)
		}
		report.Prices[plan] = id
	}
	for metric, id := range cfg.MeterIDs {
		var meter struct {
			ID                 string `json:"id"`
			EventName          string `json:"event_name"`
			Status             string `json:"status"`
			Livemode           bool   `json:"livemode"`
			DefaultAggregation struct {
				Formula string `json:"formula"`
			} `json:"default_aggregation"`
			CustomerMapping struct {
				Type            string `json:"type"`
				EventPayloadKey string `json:"event_payload_key"`
			} `json:"customer_mapping"`
			ValueSettings struct {
				EventPayloadKey string `json:"event_payload_key"`
			} `json:"value_settings"`
		}
		if err := stripeReadinessGET(ctx, cfg, client, "/v1/billing/meters/"+url.PathEscape(id), &meter); err != nil {
			return report, fmt.Errorf("meter %s: %w", metric, err)
		}
		if meter.ID != id || meter.Livemode || meter.Status != "active" || meter.EventName != cfg.MeterEventNames[metric] || meter.DefaultAggregation.Formula != "sum" || meter.CustomerMapping.Type != "by_id" || meter.CustomerMapping.EventPayloadKey != "stripe_customer_id" || meter.ValueSettings.EventPayloadKey != "value" {
			return report, fmt.Errorf("meter %s failed identity or aggregation checks", metric)
		}
		report.Meters[metric] = id
	}
	var portal struct {
		ID               string
		Active, Livemode bool
		IsDefault        bool `json:"is_default"`
	}
	if err := stripeReadinessGET(ctx, cfg, client, "/v1/billing_portal/configurations/"+url.PathEscape(cfg.PortalConfigID), &portal); err != nil {
		return report, fmt.Errorf("portal configuration: %w", err)
	}
	if portal.ID != cfg.PortalConfigID || !portal.Active || portal.Livemode {
		return report, errors.New("portal configuration is not active in test mode")
	}
	var webhook struct {
		ID            string   `json:"id"`
		URL           string   `json:"url"`
		Status        string   `json:"status"`
		APIVersion    string   `json:"api_version"`
		Livemode      bool     `json:"livemode"`
		EnabledEvents []string `json:"enabled_events"`
	}
	if err := stripeReadinessGET(ctx, cfg, client, "/v1/webhook_endpoints/"+url.PathEscape(cfg.WebhookID), &webhook); err != nil {
		return report, fmt.Errorf("webhook endpoint: %w", err)
	}
	if webhook.ID != cfg.WebhookID || webhook.Livemode || webhook.Status != "enabled" || webhook.URL != cfg.WebhookURL || webhook.APIVersion != cfg.APIVersion {
		return report, errors.New("webhook endpoint failed identity, mode, URL, status, or API-version checks")
	}
	events := append([]string(nil), webhook.EnabledEvents...)
	sort.Strings(events)
	for _, required := range RequiredWebhookEvents {
		if !contains(events, required) && !contains(events, "*") {
			return report, fmt.Errorf("webhook endpoint is missing event %s", required)
		}
	}
	report.Ready = true
	return report, nil
}

func stripeReadinessGET(ctx context.Context, cfg ReadinessConfig, client *http.Client, path string, target any) error {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://api.stripe.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(cfg.APIKey, "")
	req.Header.Set("Stripe-Version", cfg.APIVersion)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Stripe API returned status %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode Stripe response: %w", err)
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
