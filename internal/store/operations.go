package store

import (
	"context"

	"github.com/RusticStack/lockwell-saas/internal/operations"
)

func (p Postgres) OperationalSnapshot(ctx context.Context) (operations.Snapshot, error) {
	var snapshot operations.Snapshot
	err := p.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM customer_accounts),
		(SELECT count(*) FROM hosted_subscriptions WHERE entitlement_status='active'),
		(SELECT count(*) FROM hosted_subscriptions WHERE entitlement_status='grace'),
		(SELECT count(*) FROM hosted_subscriptions WHERE entitlement_status='suspended'),
		(SELECT count(*) FROM tenant_provisions WHERE status='ready'),
		(SELECT count(*) FROM tenant_provisions WHERE status='failed'),
		(SELECT count(*) FROM control_plane_outbox WHERE completed_at IS NULL AND dead_lettered_at IS NULL AND claimed_at IS NULL),
		(SELECT count(*) FROM control_plane_outbox WHERE completed_at IS NULL AND dead_lettered_at IS NULL AND claimed_at IS NOT NULL),
		(SELECT count(*) FROM control_plane_outbox WHERE dead_lettered_at IS NOT NULL),
		(SELECT count(*) FROM stripe_meter_exports WHERE status='pending'),
		(SELECT count(*) FROM stripe_meter_exports WHERE status='dead_letter'),
		(SELECT count(*) FROM stripe_event_inbox WHERE processed_at IS NULL),
		(SELECT count(*) FROM hosted_invoices),
		(SELECT count(*) FROM hosted_refunds)`).Scan(
		&snapshot.Accounts,
		&snapshot.ActiveEntitlements,
		&snapshot.GraceEntitlements,
		&snapshot.SuspendedEntitlements,
		&snapshot.ReadyProvisions,
		&snapshot.FailedProvisions,
		&snapshot.PendingOutboxJobs,
		&snapshot.ClaimedOutboxJobs,
		&snapshot.DeadLetterOutboxJobs,
		&snapshot.PendingMeterExports,
		&snapshot.DeadLetterMeterExports,
		&snapshot.UnprocessedStripeEvents,
		&snapshot.ReconciledInvoices,
		&snapshot.ReconciledRefunds,
	)
	return snapshot, err
}
