package customer

import (
	"context"
	"time"
)

type Subscription struct {
	PlanCode          string     `json:"plan_code"`
	StripeStatus      string     `json:"billing_status"`
	EntitlementStatus string     `json:"entitlement_status"`
	EntitlementUntil  *time.Time `json:"entitlement_until,omitempty"`
	GraceUntil        *time.Time `json:"grace_until,omitempty"`
	CancelAtPeriodEnd bool       `json:"cancel_at_period_end"`
}

type Provision struct {
	Status      string `json:"status"`
	Region      string `json:"region"`
	Endpoint    string `json:"endpoint"`
	TenantID    string `json:"tenant_id"`
	Bucket      string `json:"bucket"`
	AccessKeyID string `json:"access_key_id,omitempty"`
}

type Usage struct {
	Metric      string    `json:"metric"`
	Value       int64     `json:"value"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
}

type Invoice struct {
	ID              string    `json:"id"`
	Currency        string    `json:"currency"`
	Status          string    `json:"status"`
	Total           int64     `json:"total"`
	AmountPaid      int64     `json:"amount_paid"`
	AmountRemaining int64     `json:"amount_remaining"`
	HostedURL       string    `json:"hosted_url,omitempty"`
	PDFURL          string    `json:"pdf_url,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type Refund struct {
	ID        string    `json:"id"`
	InvoiceID string    `json:"invoice_id,omitempty"`
	Currency  string    `json:"currency"`
	Amount    int64     `json:"amount"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type Status struct {
	Subscription *Subscription `json:"subscription"`
	Provision    *Provision    `json:"provision"`
	Usage        []Usage       `json:"usage"`
	Invoices     []Invoice     `json:"invoices"`
	Refunds      []Refund      `json:"refunds"`
}

type Repository interface {
	CustomerStatus(context.Context, string) (Status, error)
}
