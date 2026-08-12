package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/RusticStack/lockwell-saas/internal/metering"
)

type MeterProvider struct {
	Stripe StripeClient
}

func (p MeterProvider) ReadMeterSummary(ctx context.Context, export metering.Export) (int64, error) {
	if export.MeterID == "" || export.StripeCustomerID == "" || !export.WindowEnd.After(export.WindowStart) {
		return 0, errors.New("invalid meter summary request")
	}
	base := strings.TrimRight(p.Stripe.BaseURL, "/")
	if base == "" {
		base = "https://api.stripe.com"
	}
	query := url.Values{
		"customer":   {export.StripeCustomerID},
		"start_time": {strconv.FormatInt(export.WindowStart.Unix(), 10)},
		"end_time":   {strconv.FormatInt(export.WindowEnd.Unix(), 10)},
		"limit":      {"100"},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/billing/meters/"+url.PathEscape(export.MeterID)+"/event_summaries?"+query.Encode(), nil)
	if err != nil {
		return 0, err
	}
	request.SetBasicAuth(p.Stripe.APIKey, "")
	request.Header.Set("Stripe-Version", p.Stripe.APIVersion)
	client := p.Stripe.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, fmt.Errorf("Stripe API returned status %d", response.StatusCode)
	}
	var result struct {
		Data []struct {
			AggregatedValue int64 `json:"aggregated_value"`
		} `json:"data"`
		HasMore bool `json:"has_more"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}
	if result.HasMore {
		return 0, errors.New("Stripe meter summary exceeded bounded page")
	}
	var total int64
	for _, item := range result.Data {
		if item.AggregatedValue < 0 || total > int64(^uint64(0)>>1)-item.AggregatedValue {
			return 0, errors.New("invalid Stripe aggregate")
		}
		total += item.AggregatedValue
	}
	return total, nil
}

func (p MeterProvider) SendMeterEvent(ctx context.Context, export metering.Export) error {
	if export.EventName == "" || export.Identifier == "" || export.StripeCustomerID == "" || export.Value < 0 {
		return errors.New("invalid meter export")
	}
	values := url.Values{
		"event_name":                  {export.EventName},
		"identifier":                  {export.Identifier},
		"timestamp":                   {strconv.FormatInt(export.WindowEnd.Unix(), 10)},
		"payload[stripe_customer_id]": {export.StripeCustomerID},
		"payload[value]":              {strconv.FormatInt(export.Value, 10)},
	}
	var response struct {
		Identifier string `json:"identifier"`
		EventName  string `json:"event_name"`
	}
	if err := p.Stripe.postForm(ctx, "/v1/billing/meter_events", values, export.Identifier, &response); err != nil {
		return err
	}
	if response.Identifier != export.Identifier || response.EventName != export.EventName {
		return errors.New("Stripe meter response failed identity checks")
	}
	return nil
}
