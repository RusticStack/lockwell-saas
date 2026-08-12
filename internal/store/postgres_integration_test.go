package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/RusticStack/lockwell-saas/internal/metering"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresMeterExportAndReconciliationLifecycle(t *testing.T) {
	databaseURL := os.Getenv("LOCKWELL_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("LOCKWELL_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := Postgres{Pool: pool}
	accountID, err := randomUUID()
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO customer_accounts (id,email,password_hash,terms_version,terms_accepted_at,stripe_customer_id) VALUES ($1,$2,'test-hash','test',now(),'cus_test')`, accountID, accountID+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM stripe_meter_exports WHERE usage_rollup_id IN (SELECT id FROM usage_rollups WHERE account_id=$1)`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM usage_rollups WHERE account_id=$1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM customer_accounts WHERE id=$1`, accountID)
	})

	end := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)
	rollup, err := metering.NewRollup(accountID, "cus_test", metering.Operations, end.Add(-time.Hour), end, 42, "rev-1", []byte("source evidence"))
	if err != nil {
		t.Fatal(err)
	}
	export, created, err := repo.AppendRollup(ctx, rollup, metering.MeterConfig{EventName: "lockwell_operations_v1", MeterID: "mtr_ops"})
	if err != nil || !created || export.Identifier == "" {
		t.Fatalf("append export=%#v created=%v err=%v", export, created, err)
	}
	claimed, ok, err := repo.ClaimNextExport(ctx, time.Now().UTC())
	if err != nil || !ok || claimed.ID != export.ID || claimed.Attempts != 1 {
		t.Fatalf("claim export=%#v ok=%v err=%v", claimed, ok, err)
	}
	sentAt := time.Now().UTC().Add(-2 * time.Minute)
	if err := repo.MarkExportSent(ctx, export.ID, sentAt); err != nil {
		t.Fatal(err)
	}
	reconciliation, ok, err := repo.ClaimNextReconciliation(ctx, time.Now().UTC())
	if err != nil || !ok || reconciliation.ExpectedAggregate != 42 {
		t.Fatalf("reconciliation=%#v ok=%v err=%v", reconciliation, ok, err)
	}
	if err := repo.MarkReconciled(ctx, export.ID, 42, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}
