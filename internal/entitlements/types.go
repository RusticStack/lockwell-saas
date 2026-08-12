package entitlements

import (
	"context"
	"errors"
	"time"
)

type Event struct {
	ID             string
	Type           string
	CreatedAt      time.Time
	Priority       int
	SubscriptionID string
}

type Subscription struct {
	ID                string
	AccountID         string
	CustomerID        string
	PlanCode          string
	PriceID           string
	Status            string
	CurrentPeriodEnd  time.Time
	CancelAtPeriodEnd bool
}

type State string

const (
	Pending   State = "pending"
	Active    State = "active"
	Grace     State = "grace"
	Suspended State = "suspended"
	Canceled  State = "canceled"
)

type Projection struct {
	Subscription
	EntitlementStatus State
	EntitlementUntil  *time.Time
	GraceUntil        *time.Time
	Event             Event
}

type ClaimedEvent struct {
	OutboxID string
	Attempts int
	Event    Event
}

type Repository interface {
	ClaimNextStripeEvent(context.Context, time.Time) (ClaimedEvent, bool, error)
	ApplySubscriptionProjection(context.Context, string, Projection) (bool, error)
	RetryStripeEvent(context.Context, string, time.Time, string, bool) error
	SuspendExpiredGrace(context.Context, time.Time) (string, bool, error)
}

type Provider interface {
	RetrieveSubscription(context.Context, string) (Subscription, error)
}

var ErrInvalidSubscription = errors.New("invalid subscription identity")
