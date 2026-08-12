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
	_, err = pool.Exec(ctx, `INSERT INTO customer_accounts(id,email,password_hash,terms_version,terms_accepted_at) VALUES($1,$2,'test','test',now()); INSERT INTO hosting_cells(id,region,public_endpoint,admin_endpoint,admin_secret_ref,status,tenant_capacity) VALUES($3,'fr-par','https://s3.example.test','https://admin.example.test','vault://cell','ready',1)`, accountID, accountID+"@example.test", cellID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM credential_redemptions WHERE provision_id IN (SELECT id FROM tenant_provisions WHERE account_id=$1); DELETE FROM tenant_provisions WHERE account_id=$1; DELETE FROM hosting_cells WHERE id=$2; DELETE FROM customer_accounts WHERE id=$1`, accountID, cellID)
	})
	repo := Postgres{Pool: pool}
	vault := &memoryVault{values: map[string][]byte{"vault://cell": []byte("admin")}}
	cells := &fakeCells{}
	now := time.Now().UTC().Truncate(time.Second)
	service := Service{Repo: repo, Cells: cells, Vault: vault, Now: func() time.Time { return now }}
	token, err := service.Provision(ctx, accountID, "starter")
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
}
