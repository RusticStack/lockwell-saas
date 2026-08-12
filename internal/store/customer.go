package store

import (
	"context"
	"errors"

	"github.com/RusticStack/lockwell-saas/internal/customer"
	"github.com/jackc/pgx/v5"
)

func (p Postgres) CustomerStatus(ctx context.Context, accountID string) (customer.Status, error) {
	status := customer.Status{Usage: []customer.Usage{}, Invoices: []customer.Invoice{}, Refunds: []customer.Refund{}}
	var subscription customer.Subscription
	err := p.Pool.QueryRow(ctx, `SELECT plan_code,stripe_status,entitlement_status,entitlement_until,grace_until,cancel_at_period_end FROM hosted_subscriptions WHERE account_id=$1 ORDER BY updated_at DESC LIMIT 1`, accountID).Scan(
		&subscription.PlanCode, &subscription.StripeStatus, &subscription.EntitlementStatus, &subscription.EntitlementUntil, &subscription.GraceUntil, &subscription.CancelAtPeriodEnd,
	)
	if err == nil {
		status.Subscription = &subscription
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return customer.Status{}, err
	}

	var provision customer.Provision
	err = p.Pool.QueryRow(ctx, `SELECT p.status,c.region,c.public_endpoint,p.tenant_id,p.bucket_name,COALESCE(p.access_key_id,'') FROM tenant_provisions p JOIN hosting_cells c ON c.id=p.cell_id WHERE p.account_id=$1`, accountID).Scan(
		&provision.Status, &provision.Region, &provision.Endpoint, &provision.TenantID, &provision.Bucket, &provision.AccessKeyID,
	)
	if err == nil {
		status.Provision = &provision
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return customer.Status{}, err
	}

	rows, err := p.Pool.Query(ctx, `SELECT DISTINCT ON (metric) metric,value,window_start,window_end FROM usage_rollups WHERE account_id=$1 ORDER BY metric,window_end DESC`, accountID)
	if err != nil {
		return customer.Status{}, err
	}
	for rows.Next() {
		var value customer.Usage
		if err = rows.Scan(&value.Metric, &value.Value, &value.WindowStart, &value.WindowEnd); err != nil {
			rows.Close()
			return customer.Status{}, err
		}
		status.Usage = append(status.Usage, value)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return customer.Status{}, err
	}
	rows.Close()

	rows, err = p.Pool.Query(ctx, `SELECT stripe_invoice_id,currency,status,total,amount_paid,amount_remaining,COALESCE(hosted_invoice_url,''),COALESCE(invoice_pdf,''),stripe_created_at FROM hosted_invoices WHERE account_id=$1 ORDER BY stripe_created_at DESC LIMIT 10`, accountID)
	if err != nil {
		return customer.Status{}, err
	}
	for rows.Next() {
		var value customer.Invoice
		if err = rows.Scan(&value.ID, &value.Currency, &value.Status, &value.Total, &value.AmountPaid, &value.AmountRemaining, &value.HostedURL, &value.PDFURL, &value.CreatedAt); err != nil {
			rows.Close()
			return customer.Status{}, err
		}
		status.Invoices = append(status.Invoices, value)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return customer.Status{}, err
	}
	rows.Close()

	rows, err = p.Pool.Query(ctx, `SELECT stripe_refund_id,COALESCE(stripe_invoice_id,''),currency,amount,status,stripe_created_at FROM hosted_refunds WHERE account_id=$1 ORDER BY stripe_created_at DESC LIMIT 10`, accountID)
	if err != nil {
		return customer.Status{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var value customer.Refund
		if err = rows.Scan(&value.ID, &value.InvoiceID, &value.Currency, &value.Amount, &value.Status, &value.CreatedAt); err != nil {
			return customer.Status{}, err
		}
		status.Refunds = append(status.Refunds, value)
	}
	if err = rows.Err(); err != nil {
		return customer.Status{}, err
	}
	return status, nil
}
