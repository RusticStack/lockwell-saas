package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/RusticStack/lockwell-saas/internal/billing"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Postgres struct {
	Pool *pgxpool.Pool
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
