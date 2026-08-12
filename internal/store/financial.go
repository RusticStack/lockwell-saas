package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/RusticStack/lockwell-saas/internal/financial"
	"github.com/jackc/pgx/v5"
)

func (p Postgres) ClaimNextFinancialEvent(ctx context.Context, now time.Time) (financial.ClaimedEvent, bool, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return financial.ClaimedEvent{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var claimed financial.ClaimedEvent
	var payload []byte
	err = tx.QueryRow(ctx, `SELECT o.id::text,o.attempts+1,i.event_id,i.event_type,i.stripe_created_at,i.payload_json::text FROM control_plane_outbox o JOIN stripe_event_inbox i ON i.event_id=o.aggregate_id WHERE o.topic='billing.reconcile' AND o.completed_at IS NULL AND o.dead_lettered_at IS NULL AND o.available_at<=$1 AND (o.claimed_at IS NULL OR o.claimed_at<$2) ORDER BY o.available_at,o.created_at FOR UPDATE OF o SKIP LOCKED LIMIT 1`, now, now.Add(-5*time.Minute)).Scan(&claimed.OutboxID, &claimed.Attempts, &claimed.Event.ID, &claimed.Event.Type, &claimed.Event.CreatedAt, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return financial.ClaimedEvent{}, false, nil
	}
	if err != nil {
		return financial.ClaimedEvent{}, false, err
	}
	var envelope struct {
		Data struct {
			Object struct {
				ID string `json:"id"`
			} `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &envelope) == nil {
		claimed.Event.ResourceID = envelope.Data.Object.ID
	}
	if _, err = tx.Exec(ctx, `UPDATE control_plane_outbox SET claimed_at=$2,attempts=$3 WHERE id=$1`, claimed.OutboxID, now, claimed.Attempts); err != nil {
		return financial.ClaimedEvent{}, false, err
	}
	return claimed, true, tx.Commit(ctx)
}

func (p Postgres) ApplyInvoice(ctx context.Context, outboxID string, invoice financial.Invoice, now time.Time) error {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	accountID, err := financialAccount(ctx, tx, invoice.AccountID, invoice.CustomerID, invoice.SubscriptionID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO hosted_invoices(stripe_invoice_id,account_id,stripe_customer_id,stripe_subscription_id,currency,status,subtotal,tax,total,amount_paid,amount_remaining,hosted_invoice_url,invoice_pdf,stripe_created_at,reconciled_at) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,$11,NULLIF($12,''),NULLIF($13,''),$14,$15) ON CONFLICT(stripe_invoice_id) DO UPDATE SET account_id=EXCLUDED.account_id,stripe_customer_id=EXCLUDED.stripe_customer_id,stripe_subscription_id=EXCLUDED.stripe_subscription_id,currency=EXCLUDED.currency,status=EXCLUDED.status,subtotal=EXCLUDED.subtotal,tax=EXCLUDED.tax,total=EXCLUDED.total,amount_paid=EXCLUDED.amount_paid,amount_remaining=EXCLUDED.amount_remaining,hosted_invoice_url=EXCLUDED.hosted_invoice_url,invoice_pdf=EXCLUDED.invoice_pdf,stripe_created_at=EXCLUDED.stripe_created_at,reconciled_at=EXCLUDED.reconciled_at`, invoice.ID, accountID, invoice.CustomerID, invoice.SubscriptionID, invoice.Currency, invoice.Status, invoice.Subtotal, invoice.Tax, invoice.Total, invoice.AmountPaid, invoice.AmountRemaining, invoice.HostedURL, invoice.PDF, invoice.CreatedAt, now)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM hosted_invoice_lines WHERE stripe_invoice_id=$1`, invoice.ID); err != nil {
		return err
	}
	for _, line := range invoice.Lines {
		var start, end any
		if !line.PeriodStart.IsZero() {
			start = line.PeriodStart
		}
		if !line.PeriodEnd.IsZero() {
			end = line.PeriodEnd
		}
		if _, err = tx.Exec(ctx, `INSERT INTO hosted_invoice_lines(stripe_line_id,stripe_invoice_id,stripe_price_id,description,currency,amount,quantity,period_start,period_end) VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9)`, line.ID, invoice.ID, line.PriceID, line.Description, line.Currency, line.Amount, line.Quantity, start, end); err != nil {
			return err
		}
	}
	if err = completeFinancial(ctx, tx, outboxID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p Postgres) ApplyRefund(ctx context.Context, outboxID string, refund financial.Refund, now time.Time) error {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	accountID, err := financialAccount(ctx, tx, refund.AccountID, refund.CustomerID, "")
	if err != nil {
		return err
	}
	if refund.InvoiceID != "" {
		var invoiceAccount string
		if err = tx.QueryRow(ctx, `SELECT account_id::text FROM hosted_invoices WHERE stripe_invoice_id=$1`, refund.InvoiceID).Scan(&invoiceAccount); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && invoiceAccount != accountID {
			return errors.New("refund invoice does not match customer account")
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO hosted_refunds(stripe_refund_id,account_id,stripe_customer_id,stripe_invoice_id,stripe_charge_id,stripe_payment_intent_id,currency,amount,status,reason,stripe_created_at,reconciled_at) VALUES($1,$2,$3,NULLIF($4,''),$5,NULLIF($6,''),$7,$8,$9,NULLIF($10,''),$11,$12) ON CONFLICT(stripe_refund_id) DO UPDATE SET account_id=EXCLUDED.account_id,stripe_customer_id=EXCLUDED.stripe_customer_id,stripe_invoice_id=EXCLUDED.stripe_invoice_id,stripe_charge_id=EXCLUDED.stripe_charge_id,stripe_payment_intent_id=EXCLUDED.stripe_payment_intent_id,currency=EXCLUDED.currency,amount=EXCLUDED.amount,status=EXCLUDED.status,reason=EXCLUDED.reason,stripe_created_at=EXCLUDED.stripe_created_at,reconciled_at=EXCLUDED.reconciled_at`, refund.ID, accountID, refund.CustomerID, refund.InvoiceID, refund.ChargeID, refund.PaymentIntentID, refund.Currency, refund.Amount, refund.Status, refund.Reason, refund.CreatedAt, now)
	if err != nil {
		return err
	}
	if err = completeFinancial(ctx, tx, outboxID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func financialAccount(ctx context.Context, tx pgx.Tx, claimedID, customerID, subscriptionID string) (string, error) {
	var accountID string
	err := tx.QueryRow(ctx, `SELECT id::text FROM customer_accounts WHERE stripe_customer_id=$1 FOR UPDATE`, customerID).Scan(&accountID)
	if err != nil {
		return "", err
	}
	if claimedID != "" && claimedID != accountID {
		return "", errors.New("Stripe financial metadata does not match customer binding")
	}
	if subscriptionID != "" {
		var subscriptionAccount, subscriptionCustomer string
		err = tx.QueryRow(ctx, `SELECT account_id::text,stripe_customer_id FROM hosted_subscriptions WHERE stripe_subscription_id=$1`, subscriptionID).Scan(&subscriptionAccount, &subscriptionCustomer)
		if err != nil {
			return "", err
		}
		if subscriptionAccount != accountID || subscriptionCustomer != customerID {
			return "", errors.New("Stripe subscription does not match financial customer")
		}
	}
	return accountID, nil
}

func completeFinancial(ctx context.Context, tx pgx.Tx, outboxID string, now time.Time) error {
	var eventID string
	if err := tx.QueryRow(ctx, `UPDATE control_plane_outbox SET completed_at=$2,claimed_at=NULL,last_error=NULL WHERE id=$1 AND completed_at IS NULL RETURNING aggregate_id`, outboxID, now).Scan(&eventID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE stripe_event_inbox i SET processed_at=$2,processing_error=NULL WHERE i.event_id=$1 AND NOT EXISTS(SELECT 1 FROM control_plane_outbox o WHERE o.aggregate_id=i.event_id AND o.completed_at IS NULL AND o.dead_lettered_at IS NULL)`, eventID, now)
	return err
}

func (p Postgres) RetryFinancialEvent(ctx context.Context, outboxID string, retryAt time.Time, message string, dead bool) error {
	var deadAt any
	if dead {
		deadAt = time.Now().UTC()
	}
	_, err := p.Pool.Exec(ctx, `UPDATE control_plane_outbox SET available_at=$2,claimed_at=NULL,last_error=$3,dead_lettered_at=$4 WHERE id=$1 AND completed_at IS NULL`, outboxID, retryAt, message, deadAt)
	return err
}
