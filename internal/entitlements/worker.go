package entitlements

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Worker struct {
	Repo          Repository
	Provider      Provider
	AllowedPrices map[string]string
	GracePeriod   time.Duration
	Now           func() time.Time
}

func (w Worker) RunOnce(ctx context.Context) (bool, error) {
	now := time.Now().UTC()
	if w.Now != nil {
		now = w.Now().UTC()
	}
	claimed, ok, err := w.Repo.ClaimNextStripeEvent(ctx, now)
	if err != nil || !ok {
		return ok, err
	}
	claimed.Event.Priority = eventPriority(claimed.Event.Type)
	if claimed.Event.SubscriptionID == "" {
		return true, w.Repo.RetryStripeEvent(ctx, claimed.OutboxID, now.Add(time.Hour), "event has no subscription identity", claimed.Attempts >= 8)
	}
	subscription, err := w.Provider.RetrieveSubscription(ctx, claimed.Event.SubscriptionID)
	if err != nil {
		if retryErr := w.Repo.RetryStripeEvent(ctx, claimed.OutboxID, now.Add(5*time.Minute), err.Error(), claimed.Attempts >= 8); retryErr != nil {
			return true, errors.Join(err, retryErr)
		}
		return true, err
	}
	if err := w.validate(subscription); err != nil {
		if retryErr := w.Repo.RetryStripeEvent(ctx, claimed.OutboxID, now.Add(time.Hour), err.Error(), claimed.Attempts >= 8); retryErr != nil {
			return true, errors.Join(err, retryErr)
		}
		return true, err
	}
	projection := w.project(now, claimed.Event, subscription)
	_, err = w.Repo.ApplySubscriptionProjection(ctx, claimed.OutboxID, projection)
	return true, err
}

func (w Worker) ExpireGraceOnce(ctx context.Context) (bool, error) {
	now := time.Now().UTC()
	if w.Now != nil {
		now = w.Now().UTC()
	}
	_, changed, err := w.Repo.SuspendExpiredGrace(ctx, now)
	return changed, err
}

func eventPriority(eventType string) int {
	switch eventType {
	case "customer.subscription.deleted":
		return 50
	case "invoice.payment_failed":
		return 40
	case "invoice.paid":
		return 30
	case "customer.subscription.updated":
		return 20
	default:
		return 10
	}
}

func (w Worker) validate(subscription Subscription) error {
	if subscription.ID == "" || subscription.AccountID == "" || subscription.CustomerID == "" || subscription.PlanCode == "" || subscription.PriceID == "" || subscription.CurrentPeriodEnd.IsZero() {
		return ErrInvalidSubscription
	}
	if expected := w.AllowedPrices[subscription.PlanCode]; expected == "" || expected != subscription.PriceID {
		return fmt.Errorf("%w: plan/Price mismatch", ErrInvalidSubscription)
	}
	return nil
}

func (w Worker) project(now time.Time, event Event, subscription Subscription) Projection {
	projection := Projection{Subscription: subscription, Event: event, EntitlementStatus: Pending}
	switch event.Type {
	case "invoice.paid":
		if subscription.Status == "active" || subscription.Status == "trialing" {
			projection.EntitlementStatus = Active
			end := subscription.CurrentPeriodEnd
			projection.EntitlementUntil = &end
		}
	case "invoice.payment_failed":
		projection.EntitlementStatus = Grace
		grace := now.Add(w.gracePeriod())
		projection.GraceUntil = &grace
	case "customer.subscription.deleted":
		projection.EntitlementStatus = Canceled
	case "customer.subscription.updated", "customer.subscription.created", "checkout.session.completed":
		if subscription.Status == "canceled" || subscription.Status == "unpaid" {
			projection.EntitlementStatus = Suspended
		}
	}
	return projection
}

func (w Worker) gracePeriod() time.Duration {
	if w.GracePeriod > 0 {
		return w.GracePeriod
	}
	return 7 * 24 * time.Hour
}
