package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/RusticStack/lockwell-saas/internal/billing"
	"github.com/RusticStack/lockwell-saas/internal/entitlements"
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
	t.Cleanup(pool.Close)
	repo := Postgres{Pool: pool}
	accountID, err := randomUUID()
	if err != nil {
		t.Fatal(err)
	}
	customerID := "cus_" + accountID
	_, err = pool.Exec(ctx, `INSERT INTO customer_accounts (id,email,password_hash,terms_version,terms_accepted_at,stripe_customer_id) VALUES ($1,$2,'test-hash','test',now(),$3)`, accountID, accountID+"@example.test", customerID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM stripe_meter_exports WHERE usage_rollup_id IN (SELECT id FROM usage_rollups WHERE account_id=$1)`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM usage_rollups WHERE account_id=$1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM customer_accounts WHERE id=$1`, accountID)
	})

	end := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)
	rollup, err := metering.NewRollup(accountID, customerID, metering.Operations, end.Add(-time.Hour), end, 42, "rev-1", []byte("source evidence"))
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

func TestPostgresStripeEventToEntitlementProjection(t *testing.T) {
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
	repo := Postgres{Pool: pool}
	accountID, _ := randomUUID()
	customerID := "cus_" + accountID
	subscriptionID := "sub_" + accountID
	_, err = pool.Exec(ctx, `INSERT INTO customer_accounts (id,email,password_hash,terms_version,terms_accepted_at,stripe_customer_id) VALUES ($1,$2,'test-hash','test',now(),$3)`, accountID, accountID+"@example.test", customerID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM hosted_subscriptions WHERE account_id=$1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM control_plane_outbox WHERE aggregate_id LIKE $1`, "evt_%"+accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM stripe_event_inbox WHERE event_id LIKE $1`, "evt_%"+accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM customer_accounts WHERE id=$1`, accountID)
	})
	eventID := "evt_paid_" + accountID
	payload := []byte(fmt.Sprintf(`{"id":%q,"type":"invoice.paid","api_version":"test","created":1700000000,"data":{"object":{"subscription":%q}}}`, eventID, subscriptionID))
	digest := sha256.Sum256(payload)
	outboxID, _ := randomUUID()
	created, err := repo.RecordStripeEvent(ctx, billing.StripeEvent{ID: eventID, Type: "invoice.paid", APIVersion: "test", Created: 1_700_000_000}, payload, digest, outboxID)
	if err != nil || !created {
		t.Fatalf("record created=%v err=%v", created, err)
	}
	claimed, ok, err := repo.ClaimNextStripeEvent(ctx, time.Now().UTC())
	if err != nil || !ok || claimed.Event.SubscriptionID != subscriptionID {
		t.Fatalf("claimed=%#v ok=%v err=%v", claimed, ok, err)
	}
	periodEnd := time.Now().UTC().Add(30 * 24 * time.Hour)
	projection := entitlements.Projection{Subscription: entitlements.Subscription{ID: subscriptionID, AccountID: accountID, CustomerID: customerID, PlanCode: "starter", PriceID: "price_starter", Status: "active", CurrentPeriodEnd: periodEnd}, EntitlementStatus: entitlements.Active, EntitlementUntil: &periodEnd, Event: claimed.Event}
	mutated, err := repo.ApplySubscriptionProjection(ctx, claimed.OutboxID, projection)
	if err != nil || !mutated {
		t.Fatalf("apply mutated=%v err=%v", mutated, err)
	}
	var state string
	if err := pool.QueryRow(ctx, `SELECT entitlement_status FROM hosted_subscriptions WHERE stripe_subscription_id=$1`, subscriptionID).Scan(&state); err != nil || state != "active" {
		t.Fatalf("state=%q err=%v", state, err)
	}
	staleID := "evt_failed_" + accountID
	stalePayload := []byte(fmt.Sprintf(`{"id":%q,"type":"invoice.payment_failed","api_version":"test","created":1699999999,"data":{"object":{"subscription":%q}}}`, staleID, subscriptionID))
	staleDigest := sha256.Sum256(stalePayload)
	staleOutbox, _ := randomUUID()
	if _, err := repo.RecordStripeEvent(ctx, billing.StripeEvent{ID: staleID, Type: "invoice.payment_failed", APIVersion: "test", Created: 1_699_999_999}, stalePayload, staleDigest, staleOutbox); err != nil {
		t.Fatal(err)
	}
	staleClaim, ok, err := repo.ClaimNextStripeEvent(ctx, time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("stale claim ok=%v err=%v", ok, err)
	}
	graceUntil := time.Now().UTC().Add(7 * 24 * time.Hour)
	staleProjection := projection
	staleProjection.Event = staleClaim.Event
	staleProjection.Event.Priority = 40
	staleProjection.EntitlementStatus = entitlements.Grace
	staleProjection.GraceUntil = &graceUntil
	mutated, err = repo.ApplySubscriptionProjection(ctx, staleClaim.OutboxID, staleProjection)
	if err != nil || mutated {
		t.Fatalf("stale projection mutated=%v err=%v", mutated, err)
	}
	if err := pool.QueryRow(ctx, `SELECT entitlement_status FROM hosted_subscriptions WHERE stripe_subscription_id=$1`, subscriptionID).Scan(&state); err != nil || state != "active" {
		t.Fatalf("state after stale event=%q err=%v", state, err)
	}
}
