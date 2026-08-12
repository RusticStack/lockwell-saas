package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/RusticStack/lockwell-saas/internal/billing"
	"github.com/RusticStack/lockwell-saas/internal/entitlements"
	"github.com/RusticStack/lockwell-saas/internal/financial"
	"github.com/RusticStack/lockwell-saas/internal/metering"
	"github.com/RusticStack/lockwell-saas/internal/usageingest"
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

func TestPostgresUsageWindowIsPlacementBoundAtomicAndReplaySafe(t *testing.T) {
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
	provisionID, err := randomUUID()
	if err != nil {
		t.Fatal(err)
	}
	cellID := "usage-cell-" + accountID
	tenantID := "usage-tenant-" + accountID
	if _, err = pool.Exec(ctx, `INSERT INTO customer_accounts(id,email,password_hash,terms_version,terms_accepted_at,stripe_customer_id) VALUES($1,$2,'hash','v1',now(),$3)`, accountID, accountID+"@example.test", "cus_"+accountID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO hosting_cells(id,region,public_endpoint,admin_endpoint,admin_secret_ref,status,tenant_capacity) VALUES($1,'fr-par','https://cell.example','https://admin.example','secret-ref','ready',10)`, cellID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO tenant_provisions(id,account_id,cell_id,tenant_id,bucket_name,status,plan_code,quota_bytes) VALUES($1,$2,$3,$4,'default','ready','starter',1048576)`, provisionID, accountID, cellID, tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM stripe_meter_exports WHERE usage_rollup_id IN (SELECT id FROM usage_rollups WHERE account_id=$1)`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM usage_rollups WHERE account_id=$1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM usage_windows WHERE account_id=$1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM tenant_provisions WHERE account_id=$1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM hosting_cells WHERE id=$1`, cellID)
		_, _ = pool.Exec(ctx, `DELETE FROM customer_accounts WHERE id=$1`, accountID)
	})
	end := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)
	window := usageingest.Window{CellID: cellID, TenantID: tenantID, WindowStart: end.Add(-time.Hour), WindowEnd: end, SourceRevision: "edge-ledger-7", StorageMiBHours: 2048, Operations: 51, EgressMiB: 9}
	digest := usageingest.Digest(window)
	window.SourceDigest = hex.EncodeToString(digest[:])
	meters := map[metering.Metric]metering.MeterConfig{
		metering.StorageMiBHours: {EventName: "storage", MeterID: "mtr_storage"},
		metering.Operations:      {EventName: "operations", MeterID: "mtr_operations"},
		metering.EgressMiB:       {EventName: "egress", MeterID: "mtr_egress"},
	}
	created, err := repo.AppendUsageWindow(ctx, window, digest, meters)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	var windows, rollups, exports int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM usage_windows WHERE account_id=$1`, accountID).Scan(&windows); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM usage_rollups WHERE account_id=$1`, accountID).Scan(&rollups); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM stripe_meter_exports WHERE usage_rollup_id IN (SELECT id FROM usage_rollups WHERE account_id=$1)`, accountID).Scan(&exports); err != nil {
		t.Fatal(err)
	}
	if windows != 1 || rollups != 3 || exports != 3 {
		t.Fatalf("windows=%d rollups=%d exports=%d", windows, rollups, exports)
	}
	created, err = repo.AppendUsageWindow(ctx, window, digest, meters)
	if err != nil || created {
		t.Fatalf("replay created=%v err=%v", created, err)
	}
	tampered := digest
	tampered[0] ^= 0xff
	if _, err = repo.AppendUsageWindow(ctx, window, tampered, meters); err == nil {
		t.Fatal("expected changed evidence replay denial")
	}
	unbound := window
	unbound.TenantID = "other-tenant"
	unboundDigest := usageingest.Digest(unbound)
	if _, err = repo.AppendUsageWindow(ctx, unbound, unboundDigest, meters); err == nil {
		t.Fatal("expected unbound tenant denial")
	}
}

func TestPostgresFinancialReconciliationBindsCustomerAndPersistsLines(t *testing.T) {
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
	invoiceID := "in_" + accountID
	eventID := "evt_invoice_" + accountID
	_, err = pool.Exec(ctx, `INSERT INTO customer_accounts(id,email,password_hash,terms_version,terms_accepted_at,stripe_customer_id)VALUES($1,$2,'hash','test',now(),$3)`, accountID, accountID+"@example.test", customerID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO hosted_subscriptions(stripe_subscription_id,account_id,stripe_customer_id,plan_code,stripe_price_id,stripe_status,entitlement_status,last_stripe_event_created,last_stripe_event_priority,last_stripe_event_id)VALUES($1,$2,$3,'starter','price_starter','active','active',now(),1,'seed')`, subscriptionID, accountID, customerID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM hosted_refunds WHERE account_id=$1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM hosted_invoice_lines WHERE stripe_invoice_id=$1`, invoiceID)
		_, _ = pool.Exec(ctx, `DELETE FROM hosted_invoices WHERE account_id=$1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM control_plane_outbox WHERE aggregate_id=$1`, eventID)
		_, _ = pool.Exec(ctx, `DELETE FROM stripe_event_inbox WHERE event_id=$1`, eventID)
		_, _ = pool.Exec(ctx, `DELETE FROM hosted_subscriptions WHERE account_id=$1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM customer_accounts WHERE id=$1`, accountID)
	})
	payload := []byte(fmt.Sprintf(`{"id":%q,"type":"invoice.finalized","api_version":"test","created":1700000000,"data":{"object":{"id":%q}}}`, eventID, invoiceID))
	digest := sha256.Sum256(payload)
	outboxID, _ := randomUUID()
	if _, err = repo.RecordStripeEvent(ctx, billing.StripeEvent{ID: eventID, Type: "invoice.finalized", APIVersion: "test", Created: 1700000000}, payload, digest, outboxID); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := repo.ClaimNextFinancialEvent(ctx, time.Now().UTC())
	if err != nil || !ok || claimed.Event.ResourceID != invoiceID {
		t.Fatalf("claim=%#v ok=%v err=%v", claimed, ok, err)
	}
	now := time.Now().UTC()
	invoice := financial.Invoice{ID: invoiceID, AccountID: accountID, CustomerID: customerID, SubscriptionID: subscriptionID, Currency: "eur", Status: "paid", Subtotal: 1000, Tax: 230, Total: 1230, AmountPaid: 1230, CreatedAt: now, Lines: []financial.InvoiceLine{{ID: "il_" + accountID, PriceID: "price_starter", Description: "Starter", Currency: "eur", Amount: 1000, Quantity: 1}}, TaxEvidence: financial.InvoiceTaxEvidence{AutomaticTaxEnabled: true, AutomaticTaxStatus: "complete", CustomerTaxExempt: "none", CustomerCountry: "PT", Amounts: []financial.TaxAmount{{Amount: 230, TaxableAmount: 1000, TaxRateID: "txr_test", TaxabilityReason: "standard_rated"}}}}
	if err = repo.ApplyInvoice(ctx, claimed.OutboxID, invoice, now); err != nil {
		t.Fatal(err)
	}
	var total, tax int64
	var lineCount, taxCount int
	var automaticTaxStatus, country string
	if err = pool.QueryRow(ctx, `SELECT total,tax,automatic_tax_status,customer_country,(SELECT count(*) FROM hosted_invoice_lines WHERE stripe_invoice_id=$1),(SELECT count(*) FROM hosted_invoice_tax_amounts WHERE stripe_invoice_id=$1) FROM hosted_invoices WHERE stripe_invoice_id=$1`, invoiceID).Scan(&total, &tax, &automaticTaxStatus, &country, &lineCount, &taxCount); err != nil || total != 1230 || tax != 230 || automaticTaxStatus != "complete" || country != "PT" || lineCount != 1 || taxCount != 1 {
		t.Fatalf("total=%d tax=%d automatic=%q country=%q lines=%d taxes=%d err=%v", total, tax, automaticTaxStatus, country, lineCount, taxCount, err)
	}
}

func TestPostgresCustomerStatusIsAccountScoped(t *testing.T) {
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
	otherAccountID, _ := randomUUID()
	invoiceID := "in_status_" + accountID
	otherInvoiceID := "in_status_" + otherAccountID
	for _, id := range []string{accountID, otherAccountID} {
		if _, err = pool.Exec(ctx, `INSERT INTO customer_accounts(id,email,password_hash,terms_version,terms_accepted_at,stripe_customer_id)VALUES($1,$2,'hash','test',now(),$3)`, id, id+"@example.test", "cus_"+id); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM hosted_invoices WHERE account_id IN ($1,$2)`, accountID, otherAccountID)
		_, _ = pool.Exec(ctx, `DELETE FROM usage_rollups WHERE account_id IN ($1,$2)`, accountID, otherAccountID)
		_, _ = pool.Exec(ctx, `DELETE FROM hosted_subscriptions WHERE account_id IN ($1,$2)`, accountID, otherAccountID)
		_, _ = pool.Exec(ctx, `DELETE FROM customer_accounts WHERE id IN ($1,$2)`, accountID, otherAccountID)
	})
	now := time.Now().UTC().Truncate(time.Second)
	if _, err = pool.Exec(ctx, `INSERT INTO hosted_subscriptions(stripe_subscription_id,account_id,stripe_customer_id,plan_code,stripe_price_id,stripe_status,entitlement_status,last_stripe_event_created,last_stripe_event_priority,last_stripe_event_id)VALUES($1,$2,$3,'team','price_team','active','active',now(),1,'seed')`, "sub_status_"+accountID, accountID, "cus_"+accountID); err != nil {
		t.Fatal(err)
	}
	sourceDigest := sha256.Sum256([]byte("status"))
	if _, err = pool.Exec(ctx, `INSERT INTO usage_rollups(id,account_id,metric,window_start,window_end,value,source_revision,source_digest)VALUES($1,$2,'operations',$3,$4,42,'rev',$5)`, mustRandomUUID(t), accountID, now.Add(-time.Hour), now, sourceDigest[:]); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ invoice, account string }{{invoiceID, accountID}, {otherInvoiceID, otherAccountID}} {
		if _, err = pool.Exec(ctx, `INSERT INTO hosted_invoices(stripe_invoice_id,account_id,stripe_customer_id,currency,status,subtotal,tax,total,amount_paid,amount_remaining,stripe_created_at,reconciled_at)VALUES($1,$2,$3,'eur','paid',100,0,100,100,0,$4,$4)`, item.invoice, item.account, "cus_"+item.account, now); err != nil {
			t.Fatal(err)
		}
	}
	status, err := repo.CustomerStatus(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Subscription == nil || status.Subscription.PlanCode != "team" || len(status.Usage) != 1 || status.Usage[0].Value != 42 || len(status.Invoices) != 1 || status.Invoices[0].ID != invoiceID {
		t.Fatalf("status=%#v", status)
	}
	operations, err := repo.OperationalSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if operations.Accounts < 2 || operations.ActiveEntitlements < 1 || operations.ReconciledInvoices < 2 {
		t.Fatalf("operations=%#v", operations)
	}
}

func mustRandomUUID(t *testing.T) string {
	t.Helper()
	id, err := randomUUID()
	if err != nil {
		t.Fatal(err)
	}
	return id
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
