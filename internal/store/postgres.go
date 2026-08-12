package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/RusticStack/lockwell-saas/internal/accounts"
	"github.com/RusticStack/lockwell-saas/internal/billing"
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
	jobPayload, err := json.Marshal(map[string]string{"stripe_event_id": event.ID, "event_type": event.Type})
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO control_plane_outbox
			(id, topic, aggregate_id, idempotency_key, payload_json)
		VALUES ($1, 'stripe.event.received', $2, $3, $4)`, outboxID, event.ID, "stripe-event:"+event.ID, jobPayload)
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (p Postgres) Ping(ctx context.Context) error {
	if p.Pool == nil {
		return errors.New("database pool is nil")
	}
	return p.Pool.Ping(ctx)
}
