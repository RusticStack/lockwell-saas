package financial

import (
	"context"
	"errors"
	"time"
)

type Event struct {
	ID, Type, ResourceID string
	CreatedAt            time.Time
}

type ClaimedEvent struct {
	OutboxID string
	Attempts int
	Event    Event
}

type Invoice struct {
	ID, AccountID, CustomerID, SubscriptionID, Currency, Status, HostedURL, PDF string
	Subtotal, Tax, Total, AmountPaid, AmountRemaining                           int64
	CreatedAt                                                                   time.Time
	Lines                                                                       []InvoiceLine
}

type InvoiceLine struct {
	ID, PriceID, Description, Currency string
	Amount, Quantity                   int64
	PeriodStart, PeriodEnd             time.Time
}

type Refund struct {
	ID, AccountID, CustomerID, InvoiceID, ChargeID, PaymentIntentID, Currency, Status, Reason string
	Amount                                                                                    int64
	CreatedAt                                                                                 time.Time
}

type Repository interface {
	ClaimNextFinancialEvent(context.Context, time.Time) (ClaimedEvent, bool, error)
	ApplyInvoice(context.Context, string, Invoice, time.Time) error
	ApplyRefund(context.Context, string, Refund, time.Time) error
	RetryFinancialEvent(context.Context, string, time.Time, string, bool) error
}

type Provider interface {
	RetrieveInvoice(context.Context, string) (Invoice, error)
	RetrieveRefund(context.Context, string) (Refund, error)
}

var ErrInvalidFinancialRecord = errors.New("invalid Stripe financial record")
