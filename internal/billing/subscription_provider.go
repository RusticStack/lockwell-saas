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

	"github.com/RusticStack/lockwell-saas/internal/entitlements"
)

type SubscriptionProvider struct {
	Stripe StripeClient
}

func (p SubscriptionProvider) RetrieveSubscription(ctx context.Context, subscriptionID string) (entitlements.Subscription, error) {
	if subscriptionID == "" {
		return entitlements.Subscription{}, entitlements.ErrInvalidSubscription
	}
	base := strings.TrimRight(p.Stripe.BaseURL, "/")
	if base == "" {
		base = "https://api.stripe.com"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/subscriptions/"+url.PathEscape(subscriptionID), nil)
	if err != nil {
		return entitlements.Subscription{}, err
	}
	request.SetBasicAuth(p.Stripe.APIKey, "")
	request.Header.Set("Stripe-Version", p.Stripe.APIVersion)
	client := p.Stripe.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return entitlements.Subscription{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return entitlements.Subscription{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return entitlements.Subscription{}, fmt.Errorf("Stripe API returned status %d", response.StatusCode)
	}
	var value struct {
		ID                string            `json:"id"`
		Customer          string            `json:"customer"`
		Status            string            `json:"status"`
		CurrentPeriodEnd  int64             `json:"current_period_end"`
		CancelAtPeriodEnd bool              `json:"cancel_at_period_end"`
		Metadata          map[string]string `json:"metadata"`
		Items             struct {
			Data []struct {
				CurrentPeriodEnd int64 `json:"current_period_end"`
				Price            struct {
					ID string `json:"id"`
				} `json:"price"`
			} `json:"data"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &value); err != nil {
		return entitlements.Subscription{}, err
	}
	if len(value.Items.Data) != 1 {
		return entitlements.Subscription{}, errors.New("subscription must contain exactly one approved Price")
	}
	periodEnd := value.CurrentPeriodEnd
	if periodEnd == 0 {
		periodEnd = value.Items.Data[0].CurrentPeriodEnd
	}
	return entitlements.Subscription{
		ID: value.ID, AccountID: value.Metadata["account_id"], CustomerID: value.Customer,
		PlanCode: value.Metadata["plan_code"], PriceID: value.Items.Data[0].Price.ID, Status: value.Status,
		CurrentPeriodEnd: time.Unix(periodEnd, 0).UTC(), CancelAtPeriodEnd: value.CancelAtPeriodEnd,
	}, nil
}
