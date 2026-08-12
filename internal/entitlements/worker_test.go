package entitlements

import (
	"context"
	"errors"
	"testing"
	"time"
)

type workerRepo struct {
	claimed    ClaimedEvent
	projection Projection
	retried    bool
	dead       bool
}

func (r *workerRepo) ClaimNextStripeEvent(context.Context, time.Time) (ClaimedEvent, bool, error) {
	return r.claimed, true, nil
}
func (r *workerRepo) ApplySubscriptionProjection(_ context.Context, _ string, projection Projection) (bool, error) {
	r.projection = projection
	return true, nil
}
func (r *workerRepo) RetryStripeEvent(_ context.Context, _ string, _ time.Time, _ string, dead bool) error {
	r.retried, r.dead = true, dead
	return nil
}
func (*workerRepo) SuspendExpiredGrace(context.Context, time.Time) (string, bool, error) {
	return "", false, nil
}

type workerProvider struct {
	subscription Subscription
	err          error
}

func (p workerProvider) RetrieveSubscription(context.Context, string) (Subscription, error) {
	return p.subscription, p.err
}

func TestInvoicePaidGrantsOnlyActiveBoundSubscription(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	repo := &workerRepo{claimed: ClaimedEvent{OutboxID: "job", Attempts: 1, Event: Event{ID: "evt", Type: "invoice.paid", CreatedAt: now, SubscriptionID: "sub"}}}
	provider := workerProvider{subscription: Subscription{ID: "sub", AccountID: "acct", CustomerID: "cus", PlanCode: "starter", PriceID: "price_starter", Status: "active", CurrentPeriodEnd: now.Add(30 * 24 * time.Hour)}}
	worker := Worker{Repo: repo, Provider: provider, AllowedPrices: map[string]string{"starter": "price_starter"}, Now: func() time.Time { return now }}
	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed || repo.projection.EntitlementStatus != Active || repo.projection.EntitlementUntil == nil {
		t.Fatalf("processed=%v projection=%#v err=%v", processed, repo.projection, err)
	}
}

func TestPaymentFailureStartsBoundedGrace(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	repo := &workerRepo{claimed: ClaimedEvent{OutboxID: "job", Attempts: 1, Event: Event{ID: "evt", Type: "invoice.payment_failed", CreatedAt: now, SubscriptionID: "sub"}}}
	provider := workerProvider{subscription: Subscription{ID: "sub", AccountID: "acct", CustomerID: "cus", PlanCode: "team", PriceID: "price_team", Status: "past_due", CurrentPeriodEnd: now.Add(24 * time.Hour)}}
	worker := Worker{Repo: repo, Provider: provider, AllowedPrices: map[string]string{"team": "price_team"}, GracePeriod: 72 * time.Hour, Now: func() time.Time { return now }}
	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.projection.EntitlementStatus != Grace || repo.projection.GraceUntil == nil || !repo.projection.GraceUntil.Equal(now.Add(72*time.Hour)) {
		t.Fatalf("projection=%#v", repo.projection)
	}
}

func TestPriceSubstitutionIsRetriedThenDeadLettered(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	repo := &workerRepo{claimed: ClaimedEvent{OutboxID: "job", Attempts: 8, Event: Event{ID: "evt", Type: "invoice.paid", CreatedAt: now, SubscriptionID: "sub"}}}
	provider := workerProvider{subscription: Subscription{ID: "sub", AccountID: "acct", CustomerID: "cus", PlanCode: "starter", PriceID: "price_attacker", Status: "active", CurrentPeriodEnd: now.Add(time.Hour)}}
	worker := Worker{Repo: repo, Provider: provider, AllowedPrices: map[string]string{"starter": "price_starter"}, Now: func() time.Time { return now }}
	_, err := worker.RunOnce(context.Background())
	if !errors.Is(err, ErrInvalidSubscription) || !repo.retried || !repo.dead {
		t.Fatalf("retried=%v dead=%v err=%v", repo.retried, repo.dead, err)
	}
}
