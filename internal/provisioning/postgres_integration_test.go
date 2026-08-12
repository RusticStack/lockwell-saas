package provisioning

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/RusticStack/lockwell-saas/internal/billing"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresProvisionAndExactlyOnceRedemption(t *testing.T) {
	databaseURL := os.Getenv("LOCKWELL_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("LOCKWELL_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	accountID, err := billing.RandomID()
	if err != nil {
		t.Fatal(err)
	}
	cellID := "cell-" + accountID
	subscriptionID := "sub_" + accountID
	_, err = pool.Exec(ctx, `INSERT INTO customer_accounts(id,email,password_hash,terms_version,terms_accepted_at) VALUES($1,$2,'test','test',now())`, accountID, accountID+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO hosting_cells(id,region,public_endpoint,admin_endpoint,admin_secret_ref,status,tenant_capacity) VALUES($1,'fr-par','https://s3.example.test','https://admin.example.test','vault://cell','ready',1)`, cellID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO hosted_subscriptions(stripe_subscription_id,account_id,stripe_customer_id,plan_code,stripe_price_id,stripe_status,entitlement_status,entitlement_until,last_stripe_event_created,last_stripe_event_priority,last_stripe_event_id) VALUES($1,$2,'cus_test','starter','price_test','active','active',$3,now(),1,'evt_test')`, subscriptionID, accountID, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM control_plane_outbox WHERE aggregate_id=$1`, subscriptionID)
		_, _ = pool.Exec(ctx, `DELETE FROM credential_redemptions WHERE provision_id IN (SELECT id FROM tenant_provisions WHERE account_id=$1)`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM tenant_provisions WHERE account_id=$1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM hosted_subscriptions WHERE account_id=$1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM hosting_cells WHERE id=$1`, cellID)
		_, _ = pool.Exec(ctx, `DELETE FROM customer_accounts WHERE id=$1`, accountID)
	})
	repo := Postgres{Pool: pool}
	vault := &memoryVault{values: map[string][]byte{"vault://cell": []byte("admin")}}
	cells := &fakeCells{}
	now := time.Now().UTC().Truncate(time.Second)
	service := Service{Repo: repo, Cells: cells, Vault: vault, Now: func() time.Time { return now }, PlanQuotas: map[string]int64{"starter": 1 << 30}}
	token, err := service.Provision(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := service.Redeem(ctx, accountID, token)
	if err != nil {
		t.Fatal(err)
	}
	if credential.SecretKey != "secret-once" || credential.TenantID != "acct_"+accountID {
		t.Fatalf("credential=%#v", credential)
	}
	if _, err = service.Redeem(ctx, accountID, token); !errors.Is(err, ErrInvalidRedemption) {
		t.Fatalf("replay err=%v", err)
	}
	var storedSecret string
	if err = pool.QueryRow(ctx, `SELECT COALESCE(credential_secret_ref,'') FROM tenant_provisions WHERE account_id=$1`, accountID).Scan(&storedSecret); err != nil {
		t.Fatal(err)
	}
	if storedSecret == "secret-once" || storedSecret == "" {
		t.Fatalf("stored credential value=%q", storedSecret)
	}
	pendingToken, err := service.Provision(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	outboxID, err := billing.RandomID()
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `UPDATE hosted_subscriptions SET entitlement_status='suspended' WHERE stripe_subscription_id=$1`, subscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO control_plane_outbox(id,topic,aggregate_id,idempotency_key,payload_json) VALUES($1,'entitlement.changed',$2,$3,'{}')`, outboxID, subscriptionID, "enforce-"+outboxID)
	if err != nil {
		t.Fatal(err)
	}
	suspender := &suspenderStub{}
	processed, err := (EnforcementWorker{Repo: repo, Vault: vault, Cells: suspender, Now: func() time.Time { return now }}).RunOnce(ctx)
	if err != nil || !processed || suspender.calls != 1 {
		t.Fatalf("processed=%v calls=%d err=%v", processed, suspender.calls, err)
	}
	var provisionStatus string
	var completedAt *time.Time
	if err = pool.QueryRow(ctx, `SELECT p.status,o.completed_at FROM tenant_provisions p JOIN control_plane_outbox o ON o.id=$2 WHERE p.account_id=$1`, accountID, outboxID).Scan(&provisionStatus, &completedAt); err != nil {
		t.Fatal(err)
	}
	if provisionStatus != "suspended" || completedAt == nil {
		t.Fatalf("status=%q completed=%v", provisionStatus, completedAt)
	}
	if _, err = service.Redeem(ctx, accountID, pendingToken); !errors.Is(err, ErrInvalidRedemption) {
		t.Fatalf("pre-suspension token survived: %v", err)
	}
}
