package financial

import (
	"context"
	"errors"
	"testing"
	"time"
)

type testRepo struct {
	claimed ClaimedEvent
	applied string
	retried bool
	dead    bool
}

func (r *testRepo) ClaimNextFinancialEvent(context.Context, time.Time) (ClaimedEvent, bool, error) {
	return r.claimed, true, nil
}
func (r *testRepo) ApplyInvoice(_ context.Context, _ string, _ Invoice, _ time.Time) error {
	r.applied = "invoice"
	return nil
}
func (r *testRepo) ApplyRefund(_ context.Context, _ string, _ Refund, _ time.Time) error {
	r.applied = "refund"
	return nil
}
func (r *testRepo) RetryFinancialEvent(_ context.Context, _ string, _ time.Time, _ string, dead bool) error {
	r.retried = true
	r.dead = dead
	return nil
}

type testProvider struct {
	invoice Invoice
	refund  Refund
	err     error
}

func (p testProvider) RetrieveInvoice(context.Context, string) (Invoice, error) {
	return p.invoice, p.err
}
func (p testProvider) RetrieveRefund(context.Context, string) (Refund, error) { return p.refund, p.err }

func TestWorkerAppliesAuthoritativeInvoice(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	repo := &testRepo{claimed: ClaimedEvent{OutboxID: "job", Attempts: 1, Event: Event{Type: "invoice.paid", ResourceID: "in_1"}}}
	provider := testProvider{invoice: Invoice{ID: "in_1", AccountID: "a", CustomerID: "cus", Currency: "eur", Status: "paid", CreatedAt: now, Lines: []InvoiceLine{{ID: "il_1", Currency: "eur", Quantity: 1}}}}
	ok, err := (Worker{Repo: repo, Provider: provider, Now: func() time.Time { return now }}).RunOnce(context.Background())
	if err != nil || !ok || repo.applied != "invoice" {
		t.Fatalf("ok=%v err=%v applied=%q", ok, err, repo.applied)
	}
}
func TestWorkerRetriesAndDeadLettersInvalidRefund(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	repo := &testRepo{claimed: ClaimedEvent{OutboxID: "job", Attempts: 8, Event: Event{Type: "refund.updated", ResourceID: "re_1"}}}
	_, err := (Worker{Repo: repo, Provider: testProvider{refund: Refund{}}, Now: func() time.Time { return now }}).RunOnce(context.Background())
	if !errors.Is(err, ErrInvalidFinancialRecord) || !repo.retried || !repo.dead {
		t.Fatalf("err=%v retried=%v dead=%v", err, repo.retried, repo.dead)
	}
}

func TestWorkerRejectsIncompleteOrInconsistentAutomaticTaxEvidence(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	base := Invoice{ID: "in_1", CustomerID: "cus", Currency: "eur", Status: "paid", CreatedAt: now, Subtotal: 1000, Tax: 230, Total: 1230, Lines: []InvoiceLine{{ID: "il_1", Currency: "eur", Quantity: 1}}}
	for name, evidence := range map[string]InvoiceTaxEvidence{
		"incomplete": {AutomaticTaxEnabled: true, AutomaticTaxStatus: "requires_location_inputs", CustomerTaxExempt: "none", Amounts: []TaxAmount{{Amount: 230, TaxableAmount: 1000}}},
		"mismatch":   {AutomaticTaxEnabled: true, AutomaticTaxStatus: "complete", CustomerTaxExempt: "none", Amounts: []TaxAmount{{Amount: 100, TaxableAmount: 1000}}},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &testRepo{claimed: ClaimedEvent{OutboxID: "job", Attempts: 1, Event: Event{Type: "invoice.paid", ResourceID: "in_1"}}}
			invoice := base
			invoice.TaxEvidence = evidence
			_, err := (Worker{Repo: repo, Provider: testProvider{invoice: invoice}, Now: func() time.Time { return now }}).RunOnce(context.Background())
			if !errors.Is(err, ErrInvalidFinancialRecord) || !repo.retried || repo.applied != "" {
				t.Fatalf("err=%v retried=%v applied=%q", err, repo.retried, repo.applied)
			}
		})
	}
}
