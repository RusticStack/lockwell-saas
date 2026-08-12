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
	c, err := p.Pool.Exec(ctx, `UPDATE tenant_provisions SET access_key_id=$2,credential_secret_ref=$3,status='ready',last_error=NULL,updated_at=$4 WHERE id=$1 AND status IN ('reserved','failed')`, id, accessKeyID, secretRef, now)
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
