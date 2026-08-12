package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/RusticStack/lockwell-saas/internal/accounts"
	"github.com/RusticStack/lockwell-saas/internal/billing"
	"github.com/RusticStack/lockwell-saas/internal/entitlements"
	"github.com/RusticStack/lockwell-saas/internal/metering"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Postgres struct {
	Pool *pgxpool.Pool
}

func (p Postgres) CreateAccount(ctx context.Context, email, passwordHash, termsVersion string, acceptedAt time.Time) (accounts.Account, error) {
	id, err := randomUUID()
	if err != nil {
		return accounts.Account{}, err
	}
	var account accounts.Account
	err = p.Pool.QueryRow(ctx, `
		INSERT INTO customer_accounts (id, email, password_hash, terms_version, terms_accepted_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text, email::text, email_verified_at IS NOT NULL, COALESCE(stripe_customer_id, '')`, id, email, passwordHash, termsVersion, acceptedAt).Scan(&account.ID, &account.Email, &account.EmailVerified, &account.StripeCustomerID)
	if err != nil {
		if pgErr := new(pgconn.PgError); errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return accounts.Account{}, accounts.ErrEmailExists
		}
		return accounts.Account{}, err
	}
	return account, nil
}

func (p Postgres) FindAccountByEmail(ctx context.Context, email string) (accounts.Account, string, error) {
	var account accounts.Account
	var passwordHash string
	err := p.Pool.QueryRow(ctx, `SELECT id::text, email::text, email_verified_at IS NOT NULL, password_hash, COALESCE(stripe_customer_id, '') FROM customer_accounts WHERE email = $1`, email).
		Scan(&account.ID, &account.Email, &account.EmailVerified, &passwordHash, &account.StripeCustomerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return accounts.Account{}, "", accounts.ErrInvalidCredentials
	}
	return account, passwordHash, err
}

func (p Postgres) CreateSession(ctx context.Context, accountID string, tokenHash [32]byte, expiresAt time.Time) error {
	_, err := p.Pool.Exec(ctx, `INSERT INTO customer_sessions (token_hash, account_id, expires_at) VALUES ($1, $2, $3)`, tokenHash[:], accountID, expiresAt)
	return err
}

func (p Postgres) AccountBySession(ctx context.Context, tokenHash [32]byte, now time.Time) (accounts.Account, error) {
	var account accounts.Account
	err := p.Pool.QueryRow(ctx, `
		SELECT a.id::text, a.email::text, a.email_verified_at IS NOT NULL, COALESCE(a.stripe_customer_id, '')
		FROM customer_sessions s JOIN customer_accounts a ON a.id = s.account_id
		WHERE s.token_hash = $1 AND s.expires_at > $2`, tokenHash[:], now).Scan(&account.ID, &account.Email, &account.EmailVerified, &account.StripeCustomerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return accounts.Account{}, accounts.ErrInvalidCredentials
	}
	return account, err
}

func (p Postgres) BindStripeCustomer(ctx context.Context, accountID, customerID string) error {
	command, err := p.Pool.Exec(ctx, `UPDATE customer_accounts SET stripe_customer_id = $2, updated_at = now() WHERE id = $1 AND (stripe_customer_id IS NULL OR stripe_customer_id = $2)`, accountID, customerID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("account has a different Stripe customer")
	}
	return nil
}

func (p Postgres) CreateEmailVerification(ctx context.Context, accountID string, hash [32]byte, expiresAt, now time.Time) error {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `DELETE FROM email_verifications WHERE account_id=$1 AND consumed_at IS NULL`, accountID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO email_verifications(token_hash,account_id,expires_at,created_at) VALUES($1,$2,$3,$4)`, hash[:], accountID, expiresAt, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p Postgres) ConsumeEmailVerification(ctx context.Context, hash [32]byte, now time.Time) (accounts.Account, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return accounts.Account{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var account accounts.Account
	err = tx.QueryRow(ctx, `SELECT a.id::text,a.email::text,a.email_verified_at IS NOT NULL,COALESCE(a.stripe_customer_id,'') FROM email_verifications v JOIN customer_accounts a ON a.id=v.account_id WHERE v.token_hash=$1 AND v.consumed_at IS NULL AND v.expires_at>$2 FOR UPDATE OF v,a`, hash[:], now).Scan(&account.ID, &account.Email, &account.EmailVerified, &account.StripeCustomerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return accounts.Account{}, accounts.ErrInvalidCredentials
	}
	if err != nil {
		return accounts.Account{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE email_verifications SET consumed_at=$2 WHERE token_hash=$1`, hash[:], now); err != nil {
		return accounts.Account{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE customer_accounts SET email_verified_at=COALESCE(email_verified_at,$2),updated_at=$2 WHERE id=$1`, account.ID, now); err != nil {
		return accounts.Account{}, err
	}
	account.EmailVerified = true
	return account, tx.Commit(ctx)
}

func (p Postgres) RecordCheckoutSession(ctx context.Context, accountID, planCode string, session billing.CheckoutSession, idempotencyKey string) error {
	id, err := randomUUID()
	if err != nil {
		return err
	}
	_, err = p.Pool.Exec(ctx, `
		INSERT INTO checkout_sessions
			(id, account_id, plan_code, stripe_checkout_session_id, stripe_customer_id, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (idempotency_key) DO NOTHING`, id, accountID, planCode, session.ID, session.CustomerID, idempotencyKey)
	return err
}

func randomUUID() (string, error) {
	return billing.RandomID()
}

func (p Postgres) AppendRollup(ctx context.Context, rollup metering.Rollup, meter metering.MeterConfig) (metering.Export, bool, error) {
	if meter.EventName == "" || meter.MeterID == "" {
		return metering.Export{}, false, errors.New("meter event name and ID are required")
	}
	exportID, err := randomUUID()
	if err != nil {
		return metering.Export{}, false, err
	}
	identifier := metering.Identifier(rollup)
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return metering.Export{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
		INSERT INTO usage_rollups
			(id, account_id, metric, window_start, window_end, value, source_revision, source_digest)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (account_id, metric, window_start, window_end, source_revision) DO NOTHING`,
		rollup.ID, rollup.AccountID, rollup.Metric, rollup.WindowStart, rollup.WindowEnd, rollup.Value, rollup.SourceRevision, rollup.SourceDigest[:])
	if err != nil {
		return metering.Export{}, false, err
	}
	created := command.RowsAffected() == 1
	if created {
		_, err = tx.Exec(ctx, `
			INSERT INTO stripe_meter_exports
				(id, usage_rollup_id, stripe_customer_id, meter_event_name, stripe_meter_id, stripe_identifier)
			VALUES ($1, $2, $3, $4, $5, $6)`, exportID, rollup.ID, rollup.StripeCustomerID, meter.EventName, meter.MeterID, identifier)
		if err != nil {
			return metering.Export{}, false, err
		}
	} else {
		var existingDigest []byte
		err = tx.QueryRow(ctx, `
			SELECT r.id::text, r.source_digest, e.id::text, e.stripe_identifier,
			       e.stripe_customer_id, e.meter_event_name, e.stripe_meter_id, r.window_end, r.value
			FROM usage_rollups r JOIN stripe_meter_exports e ON e.usage_rollup_id = r.id
			WHERE r.account_id=$1 AND r.metric=$2 AND r.window_start=$3 AND r.window_end=$4 AND r.source_revision=$5`,
			rollup.AccountID, rollup.Metric, rollup.WindowStart, rollup.WindowEnd, rollup.SourceRevision).
			Scan(&rollup.ID, &existingDigest, &exportID, &identifier, &rollup.StripeCustomerID, &meter.EventName, &meter.MeterID, &rollup.WindowEnd, &rollup.Value)
		if err != nil {
			return metering.Export{}, false, err
		}
		if !bytes.Equal(existingDigest, rollup.SourceDigest[:]) {
			return metering.Export{}, false, errors.New("usage revision replayed with different evidence")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return metering.Export{}, false, err
	}
	return metering.Export{ID: exportID, RollupID: rollup.ID, StripeCustomerID: rollup.StripeCustomerID, EventName: meter.EventName, MeterID: meter.MeterID, Identifier: identifier, WindowStart: rollup.WindowStart, WindowEnd: rollup.WindowEnd, Value: rollup.Value}, created, nil
}

func (p Postgres) ClaimNextExport(ctx context.Context, now time.Time) (metering.Export, bool, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return metering.Export{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var export metering.Export
	err = tx.QueryRow(ctx, `
		SELECT e.id::text, e.usage_rollup_id::text, e.stripe_customer_id, e.meter_event_name,
		       e.stripe_meter_id, e.stripe_identifier, r.window_start, r.window_end, r.value, e.attempts + 1
		FROM stripe_meter_exports e JOIN usage_rollups r ON r.id = e.usage_rollup_id
		WHERE (e.status = 'pending' AND e.available_at <= $1)
		   OR (e.status = 'sending' AND e.claimed_at < $2)
		ORDER BY e.available_at, e.created_at
		FOR UPDATE OF e SKIP LOCKED LIMIT 1`, now, now.Add(-5*time.Minute)).Scan(&export.ID, &export.RollupID, &export.StripeCustomerID, &export.EventName, &export.MeterID, &export.Identifier, &export.WindowStart, &export.WindowEnd, &export.Value, &export.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return metering.Export{}, false, nil
	}
	if err != nil {
		return metering.Export{}, false, err
	}
	_, err = tx.Exec(ctx, `UPDATE stripe_meter_exports SET status='sending', attempts=$2, claimed_at=$3 WHERE id=$1`, export.ID, export.Attempts, now)
	if err != nil {
		return metering.Export{}, false, err
	}
	return export, true, tx.Commit(ctx)
}

func (p Postgres) MarkExportSent(ctx context.Context, exportID string, sentAt time.Time) error {
	command, err := p.Pool.Exec(ctx, `UPDATE stripe_meter_exports SET status='sent', sent_at=$2, available_at=$3, last_error=NULL WHERE id=$1 AND status='sending'`, exportID, sentAt, sentAt.Add(time.Minute))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("meter export is not claimed")
	}
	return nil
}

func (p Postgres) MarkExportFailed(ctx context.Context, exportID string, retryAt time.Time, message string, deadLetter bool) error {
	status := "pending"
	if deadLetter {
		status = "dead_letter"
	}
	command, err := p.Pool.Exec(ctx, `UPDATE stripe_meter_exports SET status=$2, available_at=$3, last_error=$4 WHERE id=$1 AND status='sending'`, exportID, status, retryAt, message)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("meter export is not claimed")
	}
	return nil
}

func (p Postgres) ClaimNextReconciliation(ctx context.Context, now time.Time) (metering.Export, bool, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return metering.Export{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var export metering.Export
	err = tx.QueryRow(ctx, `
		SELECT e.id::text, e.usage_rollup_id::text, e.stripe_customer_id, e.meter_event_name,
		       e.stripe_meter_id, e.stripe_identifier, r.window_start, r.window_end, r.value, e.attempts,
		       (SELECT COALESCE(SUM(r2.value), 0)
		          FROM usage_rollups r2 JOIN stripe_meter_exports e2 ON e2.usage_rollup_id=r2.id
		         WHERE e2.stripe_customer_id=e.stripe_customer_id AND e2.stripe_meter_id=e.stripe_meter_id
		           AND r2.window_start=r.window_start AND r2.window_end=r.window_end
		           AND e2.status IN ('sent','reconciling','reconciled'))
		FROM stripe_meter_exports e JOIN usage_rollups r ON r.id=e.usage_rollup_id
		WHERE (e.status='sent' AND e.available_at <= $1)
		   OR (e.status='reconciling' AND e.claimed_at < $2)
		ORDER BY e.available_at, e.created_at
		FOR UPDATE OF e SKIP LOCKED LIMIT 1`, now, now.Add(-5*time.Minute)).Scan(&export.ID, &export.RollupID, &export.StripeCustomerID, &export.EventName, &export.MeterID, &export.Identifier, &export.WindowStart, &export.WindowEnd, &export.Value, &export.Attempts, &export.ExpectedAggregate)
	if errors.Is(err, pgx.ErrNoRows) {
		return metering.Export{}, false, nil
	}
	if err != nil {
		return metering.Export{}, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE stripe_meter_exports SET status='reconciling', claimed_at=$2 WHERE id=$1`, export.ID, now); err != nil {
		return metering.Export{}, false, err
	}
	return export, true, tx.Commit(ctx)
}

func (p Postgres) MarkReconciled(ctx context.Context, exportID string, aggregated int64, at time.Time) error {
	command, err := p.Pool.Exec(ctx, `UPDATE stripe_meter_exports SET status='reconciled', stripe_aggregated_value=$2, reconciled_at=$3, last_error=NULL WHERE id=$1 AND status='reconciling'`, exportID, aggregated, at)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("meter reconciliation is not claimed")
	}
	return nil
}

func (p Postgres) MarkReconciliationPending(ctx context.Context, exportID string, retryAt time.Time, message string) error {
	command, err := p.Pool.Exec(ctx, `UPDATE stripe_meter_exports SET status='sent', available_at=$2, last_error=$3 WHERE id=$1 AND status='reconciling'`, exportID, retryAt, message)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("meter reconciliation is not claimed")
	}
	return nil
}

func (p Postgres) RecordStripeEvent(ctx context.Context, event billing.StripeEvent, payload []byte, digest [32]byte, outboxID string) (bool, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
		INSERT INTO stripe_event_inbox
			(event_id, event_type, api_version, payload_sha256, payload_json, stripe_created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (event_id) DO NOTHING`, event.ID, event.Type, event.APIVersion, digest[:], json.RawMessage(payload), time.Unix(event.Created, 0).UTC())
	if err != nil {
		return false, err
	}
	if command.RowsAffected() == 0 {
		var existing []byte
		if err := tx.QueryRow(ctx, `SELECT payload_sha256 FROM stripe_event_inbox WHERE event_id = $1`, event.ID).Scan(&existing); err != nil {
			return false, err
		}
		if !bytes.Equal(existing, digest[:]) {
			return false, billing.ErrConflictingReplay
		}
		return false, tx.Commit(ctx)
	}
	topic := stripeEventTopic(event.Type)
	if topic == "" {
		return true, tx.Commit(ctx)
	}
	jobPayload, err := json.Marshal(map[string]string{"stripe_event_id": event.ID, "event_type": event.Type})
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO control_plane_outbox
			(id, topic, aggregate_id, idempotency_key, payload_json)
			VALUES ($1, $2, $3, $4, $5)`, outboxID, topic, event.ID, "stripe-event:"+event.ID, jobPayload)
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (p Postgres) ClaimNextStripeEvent(ctx context.Context, now time.Time) (entitlements.ClaimedEvent, bool, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return entitlements.ClaimedEvent{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var claimed entitlements.ClaimedEvent
	var payload []byte
	err = tx.QueryRow(ctx, `
		SELECT o.id::text, o.attempts + 1, i.event_id, i.event_type, i.stripe_created_at, i.payload_json::text
		FROM control_plane_outbox o
		JOIN stripe_event_inbox i ON i.event_id=o.aggregate_id
		WHERE o.topic='stripe.event.received' AND o.completed_at IS NULL AND o.dead_lettered_at IS NULL
		  AND o.available_at <= $1 AND (o.claimed_at IS NULL OR o.claimed_at < $2)
		ORDER BY o.available_at, o.created_at
		FOR UPDATE OF o SKIP LOCKED LIMIT 1`, now, now.Add(-5*time.Minute)).
		Scan(&claimed.OutboxID, &claimed.Attempts, &claimed.Event.ID, &claimed.Event.Type, &claimed.Event.CreatedAt, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return entitlements.ClaimedEvent{}, false, nil
	}
	if err != nil {
		return entitlements.ClaimedEvent{}, false, err
	}
	claimed.Event.SubscriptionID = subscriptionID(claimed.Event.Type, payload)
	if _, err := tx.Exec(ctx, `UPDATE control_plane_outbox SET claimed_at=$2, attempts=$3 WHERE id=$1`, claimed.OutboxID, now, claimed.Attempts); err != nil {
		return entitlements.ClaimedEvent{}, false, err
	}
	return claimed, true, tx.Commit(ctx)
}

func subscriptionID(eventType string, payload []byte) string {
	var envelope struct {
		Data struct {
			Object struct {
				ID           string `json:"id"`
				Subscription string `json:"subscription"`
				Parent       struct {
					SubscriptionDetails struct {
						Subscription string `json:"subscription"`
					} `json:"subscription_details"`
				} `json:"parent"`
			} `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return ""
	}
	switch eventType {
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted":
		return envelope.Data.Object.ID
	case "checkout.session.completed":
		return envelope.Data.Object.Subscription
	case "invoice.paid", "invoice.payment_failed":
		if envelope.Data.Object.Subscription != "" {
			return envelope.Data.Object.Subscription
		}
		return envelope.Data.Object.Parent.SubscriptionDetails.Subscription
	default:
		return ""
	}
}

func (p Postgres) ApplySubscriptionProjection(ctx context.Context, outboxID string, projection entitlements.Projection) (bool, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var boundCustomer string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(stripe_customer_id,'') FROM customer_accounts WHERE id=$1 FOR UPDATE`, projection.AccountID).Scan(&boundCustomer); err != nil {
		return false, err
	}
	if boundCustomer != projection.CustomerID {
		return false, errors.New("Stripe customer does not match account binding")
	}
	var lastCreated time.Time
	var lastPriority int
	err = tx.QueryRow(ctx, `SELECT last_stripe_event_created,last_stripe_event_priority FROM hosted_subscriptions WHERE stripe_subscription_id=$1 FOR UPDATE`, projection.ID).Scan(&lastCreated, &lastPriority)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	mutated := errors.Is(err, pgx.ErrNoRows) || projection.Event.CreatedAt.After(lastCreated) || (projection.Event.CreatedAt.Equal(lastCreated) && projection.Event.Priority > lastPriority)
	if mutated {
		_, err = tx.Exec(ctx, `
			INSERT INTO hosted_subscriptions
				(stripe_subscription_id,account_id,stripe_customer_id,plan_code,stripe_price_id,stripe_status,
				 entitlement_status,entitlement_until,grace_until,cancel_at_period_end,last_stripe_event_created,last_stripe_event_priority,last_stripe_event_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT (stripe_subscription_id) DO UPDATE SET
				plan_code=EXCLUDED.plan_code, stripe_price_id=EXCLUDED.stripe_price_id, stripe_status=EXCLUDED.stripe_status,
				entitlement_status=CASE WHEN EXCLUDED.entitlement_status='pending' THEN hosted_subscriptions.entitlement_status ELSE EXCLUDED.entitlement_status END,
				entitlement_until=CASE WHEN EXCLUDED.entitlement_status='pending' THEN hosted_subscriptions.entitlement_until ELSE EXCLUDED.entitlement_until END,
				grace_until=CASE WHEN EXCLUDED.entitlement_status='pending' THEN hosted_subscriptions.grace_until ELSE EXCLUDED.grace_until END,
				cancel_at_period_end=EXCLUDED.cancel_at_period_end,last_stripe_event_created=EXCLUDED.last_stripe_event_created,
				last_stripe_event_priority=EXCLUDED.last_stripe_event_priority,
				last_stripe_event_id=EXCLUDED.last_stripe_event_id,updated_at=now()`,
			projection.ID, projection.AccountID, projection.CustomerID, projection.PlanCode, projection.PriceID, projection.Status,
			projection.EntitlementStatus, projection.EntitlementUntil, projection.GraceUntil, projection.CancelAtPeriodEnd,
			projection.Event.CreatedAt, projection.Event.Priority, projection.Event.ID)
		if err != nil {
			return false, err
		}
		jobID, err := randomUUID()
		if err != nil {
			return false, err
		}
		jobPayload, _ := json.Marshal(map[string]string{"subscription_id": projection.ID, "account_id": projection.AccountID, "entitlement_status": string(projection.EntitlementStatus)})
		if _, err := tx.Exec(ctx, `INSERT INTO control_plane_outbox (id,topic,aggregate_id,idempotency_key,payload_json) VALUES ($1,'entitlement.changed',$2,$3,$4) ON CONFLICT (idempotency_key) DO NOTHING`, jobID, projection.ID, "entitlement:"+projection.Event.ID, jobPayload); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE control_plane_outbox SET completed_at=now(),claimed_at=NULL,last_error=NULL WHERE id=$1`, outboxID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE stripe_event_inbox SET processed_at=now(),processing_error=NULL WHERE event_id=$1`, projection.Event.ID); err != nil {
		return false, err
	}
	return mutated, tx.Commit(ctx)
}

func (p Postgres) RetryStripeEvent(ctx context.Context, outboxID string, retryAt time.Time, message string, deadLetter bool) error {
	var deadLetteredAt any
	if deadLetter {
		deadLetteredAt = time.Now().UTC()
	}
	_, err := p.Pool.Exec(ctx, `UPDATE control_plane_outbox SET available_at=$2,claimed_at=NULL,last_error=$3,dead_lettered_at=$4 WHERE id=$1 AND completed_at IS NULL`, outboxID, retryAt, message, deadLetteredAt)
	return err
}

func (p Postgres) SuspendExpiredGrace(ctx context.Context, now time.Time) (string, bool, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var subscriptionID string
	var accountID string
	err = tx.QueryRow(ctx, `SELECT stripe_subscription_id,account_id::text FROM hosted_subscriptions WHERE entitlement_status='grace' AND grace_until <= $1 ORDER BY grace_until FOR UPDATE SKIP LOCKED LIMIT 1`, now).Scan(&subscriptionID, &accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE hosted_subscriptions SET entitlement_status='suspended',updated_at=now() WHERE stripe_subscription_id=$1`, subscriptionID); err != nil {
		return "", false, err
	}
	jobID, err := randomUUID()
	if err != nil {
		return "", false, err
	}
	payload, _ := json.Marshal(map[string]string{"subscription_id": subscriptionID, "account_id": accountID, "entitlement_status": "suspended"})
	if _, err := tx.Exec(ctx, `INSERT INTO control_plane_outbox (id,topic,aggregate_id,idempotency_key,payload_json) VALUES ($1,'entitlement.changed',$2,$3,$4)`, jobID, subscriptionID, "entitlement-grace-expired:"+subscriptionID+":"+now.Format(time.RFC3339), payload); err != nil {
		return "", false, err
	}
	return subscriptionID, true, tx.Commit(ctx)
}

func stripeEventTopic(eventType string) string {
	switch eventType {
	case "checkout.session.completed", "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted", "invoice.paid", "invoice.payment_failed":
		return "stripe.event.received"
	case "invoice.finalization_failed", "invoice.payment_action_required":
		return "billing.alert"
	default:
		return ""
	}
}

func (p Postgres) Ping(ctx context.Context) error {
	if p.Pool == nil {
		return errors.New("database pool is nil")
	}
	return p.Pool.Ping(ctx)
}
