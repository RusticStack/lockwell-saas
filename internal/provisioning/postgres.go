package provisioning

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/RusticStack/lockwell-saas/internal/billing"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Postgres struct{ Pool *pgxpool.Pool }

func (p Postgres) EntitledPlan(ctx context.Context, accountID string, now time.Time) (string, error) {
	var plan string
	err := p.Pool.QueryRow(ctx, `SELECT plan_code FROM hosted_subscriptions WHERE account_id=$1 AND entitlement_status IN ('active','grace') AND (entitlement_until IS NULL OR entitlement_until>$2) ORDER BY updated_at DESC LIMIT 1`, accountID, now).Scan(&plan)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errors.New("account has no current hosted entitlement")
	}
	return plan, err
}

func (p Postgres) UpsertCell(ctx context.Context, r Reservation, capacity int) error {
	if r.CellID == "" || r.Region == "" || r.PublicEndpoint == "" || r.AdminEndpoint == "" || r.AdminSecretRef == "" || capacity <= 0 {
		return errors.New("complete cell configuration and positive capacity are required")
	}
	_, err := p.Pool.Exec(ctx, `INSERT INTO hosting_cells(id,region,public_endpoint,admin_endpoint,admin_secret_ref,status,tenant_capacity) VALUES($1,$2,$3,$4,$5,'ready',$6) ON CONFLICT(id) DO UPDATE SET region=EXCLUDED.region,public_endpoint=EXCLUDED.public_endpoint,admin_endpoint=EXCLUDED.admin_endpoint,admin_secret_ref=EXCLUDED.admin_secret_ref,tenant_capacity=EXCLUDED.tenant_capacity,updated_at=now()`, r.CellID, r.Region, r.PublicEndpoint, r.AdminEndpoint, r.AdminSecretRef, capacity)
	return err
}

func (p Postgres) Reserve(ctx context.Context, accountID, planCode string, quotaBytes int64, now time.Time) (Reservation, bool, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Reservation{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var r Reservation
	err = tx.QueryRow(ctx, `SELECT p.id::text,p.account_id::text,p.cell_id,c.region,c.public_endpoint,c.admin_endpoint,c.admin_secret_ref,p.tenant_id,p.bucket_name,p.status,p.plan_code,p.quota_bytes FROM tenant_provisions p JOIN hosting_cells c ON c.id=p.cell_id WHERE p.account_id=$1 FOR UPDATE`, accountID).Scan(&r.ID, &r.AccountID, &r.CellID, &r.Region, &r.PublicEndpoint, &r.AdminEndpoint, &r.AdminSecretRef, &r.TenantID, &r.BucketName, &r.Status, &r.PlanCode, &r.QuotaBytes)
	if err == nil {
		if r.PlanCode != planCode || r.QuotaBytes != quotaBytes {
			return Reservation{}, false, errors.New("existing tenant reservation has a different plan")
		}
		return r, r.Status == "ready", tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, false, err
	}
	if planCode == "" || quotaBytes <= 0 {
		return Reservation{}, false, errors.New("plan code and positive quota are required")
	}
	err = tx.QueryRow(ctx, `SELECT c.id,c.region,c.public_endpoint,c.admin_endpoint,c.admin_secret_ref FROM hosting_cells c WHERE c.status='ready' AND (SELECT count(*) FROM tenant_provisions p WHERE p.cell_id=c.id AND p.status IN ('reserved','ready','suspended')) < c.tenant_capacity ORDER BY (SELECT count(*) FROM tenant_provisions p WHERE p.cell_id=c.id),c.id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&r.CellID, &r.Region, &r.PublicEndpoint, &r.AdminEndpoint, &r.AdminSecretRef)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, false, ErrNoCapacity
	}
	if err != nil {
		return Reservation{}, false, err
	}
	r.ID, err = billing.RandomID()
	if err != nil {
		return Reservation{}, false, err
	}
	r.AccountID = accountID
	r.TenantID = "acct_" + accountID
	r.BucketName = "data"
	r.Status = "reserved"
	r.PlanCode = planCode
	r.QuotaBytes = quotaBytes
	_, err = tx.Exec(ctx, `INSERT INTO tenant_provisions(id,account_id,cell_id,tenant_id,bucket_name,status,plan_code,quota_bytes,created_at,updated_at) VALUES($1,$2,$3,$4,$5,'reserved',$6,$7,$8,$8)`, r.ID, r.AccountID, r.CellID, r.TenantID, r.BucketName, r.PlanCode, r.QuotaBytes, now)
	if err != nil {
		return Reservation{}, false, err
	}
	return r, false, tx.Commit(ctx)
}

func (p Postgres) Complete(ctx context.Context, id, accessKeyID, secretRef string, now time.Time) error {
	c, err := p.Pool.Exec(ctx, `UPDATE tenant_provisions SET access_key_id=$2,credential_secret_ref=$3,status='ready',last_error=NULL,updated_at=$4 WHERE id=$1 AND status IN ('reserved','failed','suspended')`, id, accessKeyID, secretRef, now)
	if err != nil {
		return err
	}
	if c.RowsAffected() != 1 {
		return ErrNotReady
	}
	return nil
}
func (p Postgres) Fail(ctx context.Context, id, message string, now time.Time) error {
	_, err := p.Pool.Exec(ctx, `UPDATE tenant_provisions SET status='failed',last_error=$2,updated_at=$3 WHERE id=$1 AND status='reserved'`, id, message, now)
	return err
}

func (p Postgres) CreateRedemption(ctx context.Context, provisionID string, hash [32]byte, expiresAt, now time.Time) error {
	id, err := billing.RandomID()
	if err != nil {
		return err
	}
	_, err = p.Pool.Exec(ctx, `INSERT INTO credential_redemptions(id,provision_id,token_hash,expires_at,created_at) VALUES($1,$2,$3,$4,$5)`, id, provisionID, hash[:], expiresAt, now)
	return err
}

func (p Postgres) ClaimRedemption(ctx context.Context, accountID string, hash [32]byte, now, claimExpires time.Time) (string, Reservation, string, string, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", Reservation{}, "", "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id, accessKey, secretRef string
	var r Reservation
	err = tx.QueryRow(ctx, `SELECT d.id::text,p.id::text,p.account_id::text,p.cell_id,c.region,c.public_endpoint,c.admin_endpoint,c.admin_secret_ref,p.tenant_id,p.bucket_name,p.status,p.plan_code,p.quota_bytes,p.access_key_id,p.credential_secret_ref FROM credential_redemptions d JOIN tenant_provisions p ON p.id=d.provision_id JOIN hosting_cells c ON c.id=p.cell_id WHERE d.token_hash=$1 AND p.account_id=$2 AND p.status='ready' AND d.redeemed_at IS NULL AND d.expires_at>$3 AND (d.claim_expires_at IS NULL OR d.claim_expires_at<$3) FOR UPDATE`, hash[:], accountID, now).Scan(&id, &r.ID, &r.AccountID, &r.CellID, &r.Region, &r.PublicEndpoint, &r.AdminEndpoint, &r.AdminSecretRef, &r.TenantID, &r.BucketName, &r.Status, &r.PlanCode, &r.QuotaBytes, &accessKey, &secretRef)
	if err != nil {
		return "", Reservation{}, "", "", ErrInvalidRedemption
	}
	if _, err = tx.Exec(ctx, `UPDATE credential_redemptions SET claimed_at=$2,claim_expires_at=$3 WHERE id=$1`, id, now, claimExpires); err != nil {
		return "", Reservation{}, "", "", err
	}
	return id, r, accessKey, secretRef, tx.Commit(ctx)
}
func (p Postgres) CompleteRedemption(ctx context.Context, id string, now time.Time) error {
	c, err := p.Pool.Exec(ctx, `UPDATE credential_redemptions SET redeemed_at=$2,claim_expires_at=NULL WHERE id=$1 AND claimed_at IS NOT NULL AND redeemed_at IS NULL`, id, now)
	if err != nil {
		return err
	}
	if c.RowsAffected() != 1 {
		return ErrInvalidRedemption
	}
	return nil
}
func (p Postgres) ReleaseRedemption(ctx context.Context, id string) error {
	c, err := p.Pool.Exec(ctx, `UPDATE credential_redemptions SET claimed_at=NULL,claim_expires_at=NULL WHERE id=$1 AND redeemed_at IS NULL`, id)
	if err != nil {
		return err
	}
	if c.RowsAffected() != 1 {
		return fmt.Errorf("release redemption: %w", ErrInvalidRedemption)
	}
	return nil
}

func (p Postgres) ClaimNextEnforcement(ctx context.Context, now time.Time) (EnforcementJob, bool, error) {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EnforcementJob{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var job EnforcementJob
	err = tx.QueryRow(ctx, `SELECT o.id::text,o.attempts+1,s.account_id::text,s.entitlement_status,p.id IS NOT NULL,COALESCE(p.id::text,''),COALESCE(p.cell_id,''),COALESCE(c.region,''),COALESCE(c.public_endpoint,''),COALESCE(c.admin_endpoint,''),COALESCE(c.admin_secret_ref,''),COALESCE(p.tenant_id,''),COALESCE(p.bucket_name,''),COALESCE(p.status,''),COALESCE(p.plan_code,''),COALESCE(p.quota_bytes,0),COALESCE(p.access_key_id,'') FROM control_plane_outbox o JOIN hosted_subscriptions s ON s.stripe_subscription_id=o.aggregate_id LEFT JOIN tenant_provisions p ON p.account_id=s.account_id LEFT JOIN hosting_cells c ON c.id=p.cell_id WHERE o.topic='entitlement.changed' AND o.completed_at IS NULL AND o.dead_lettered_at IS NULL AND o.available_at<=$1 AND (o.claimed_at IS NULL OR o.claimed_at<$2) ORDER BY o.available_at,o.created_at FOR UPDATE OF o SKIP LOCKED LIMIT 1`, now, now.Add(-5*time.Minute)).Scan(&job.OutboxID, &job.Attempts, &job.AccountID, &job.Status, &job.HasProvision, &job.Reservation.ID, &job.Reservation.CellID, &job.Reservation.Region, &job.Reservation.PublicEndpoint, &job.Reservation.AdminEndpoint, &job.Reservation.AdminSecretRef, &job.Reservation.TenantID, &job.Reservation.BucketName, &job.Reservation.Status, &job.Reservation.PlanCode, &job.Reservation.QuotaBytes, &job.AccessKeyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return EnforcementJob{}, false, nil
	}
	if err != nil {
		return EnforcementJob{}, false, err
	}
	job.Reservation.AccountID = job.AccountID
	if _, err = tx.Exec(ctx, `UPDATE control_plane_outbox SET claimed_at=$2,attempts=$3 WHERE id=$1`, job.OutboxID, now, job.Attempts); err != nil {
		return EnforcementJob{}, false, err
	}
	return job, true, tx.Commit(ctx)
}
func (p Postgres) CompleteEnforcement(ctx context.Context, outboxID, provisionStatus, message string, now time.Time) error {
	tx, err := p.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if provisionStatus != "" {
		if _, err = tx.Exec(ctx, `UPDATE tenant_provisions p SET status=$2,updated_at=$3,last_error=NULL FROM control_plane_outbox o JOIN hosted_subscriptions s ON s.stripe_subscription_id=o.aggregate_id WHERE o.id=$1 AND p.account_id=s.account_id`, outboxID, provisionStatus, now); err != nil {
			return err
		}
		if provisionStatus == "suspended" {
			if _, err = tx.Exec(ctx, `UPDATE credential_redemptions d SET redeemed_at=$2,claim_expires_at=NULL FROM tenant_provisions p JOIN control_plane_outbox o ON o.id=$1 JOIN hosted_subscriptions s ON s.stripe_subscription_id=o.aggregate_id WHERE d.provision_id=p.id AND p.account_id=s.account_id AND d.redeemed_at IS NULL`, outboxID, now); err != nil {
				return err
			}
		}
	}
	_, err = tx.Exec(ctx, `UPDATE control_plane_outbox SET completed_at=$2,claimed_at=NULL,last_error=NULL WHERE id=$1 AND completed_at IS NULL`, outboxID, now)
	if err != nil {
		return err
	}
	_ = message
	return tx.Commit(ctx)
}
func (p Postgres) RetryEnforcement(ctx context.Context, outboxID string, retryAt time.Time, message string, dead bool) error {
	var deadAt any
	if dead {
		deadAt = time.Now().UTC()
	}
	_, err := p.Pool.Exec(ctx, `UPDATE control_plane_outbox SET available_at=$2,claimed_at=NULL,last_error=$3,dead_lettered_at=$4 WHERE id=$1 AND completed_at IS NULL`, outboxID, retryAt, message, deadAt)
	return err
}
