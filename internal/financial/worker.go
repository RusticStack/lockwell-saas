package financial

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Worker struct {
	Repo        Repository
	Provider    Provider
	Now         func() time.Time
	MaxAttempts int
}

func (w Worker) RunOnce(ctx context.Context) (bool, error) {
	now := time.Now().UTC()
	if w.Now != nil {
		now = w.Now().UTC()
	}
	claimed, ok, err := w.Repo.ClaimNextFinancialEvent(ctx, now)
	if err != nil || !ok {
		return ok, err
	}
	if claimed.Event.ResourceID == "" {
		return true, w.retry(ctx, claimed, now, errors.New("event has no financial resource identity"))
	}
	var applyErr error
	switch claimed.Event.Type {
	case "invoice.finalized", "invoice.paid", "invoice.voided", "invoice.marked_uncollectible":
		var invoice Invoice
		invoice, err = w.Provider.RetrieveInvoice(ctx, claimed.Event.ResourceID)
		if err == nil {
			err = validateInvoice(invoice)
		}
		if err == nil {
			applyErr = w.Repo.ApplyInvoice(ctx, claimed.OutboxID, invoice, now)
		}
	case "refund.created", "refund.updated", "refund.failed":
		var refund Refund
		refund, err = w.Provider.RetrieveRefund(ctx, claimed.Event.ResourceID)
		if err == nil {
			err = validateRefund(refund)
		}
		if err == nil {
			applyErr = w.Repo.ApplyRefund(ctx, claimed.OutboxID, refund, now)
		}
	default:
		err = fmt.Errorf("unsupported financial event %q", claimed.Event.Type)
	}
	if err != nil {
		return true, w.retry(ctx, claimed, now, err)
	}
	return true, applyErr
}

func (w Worker) retry(ctx context.Context, claimed ClaimedEvent, now time.Time, cause error) error {
	dead := claimed.Attempts >= w.maxAttempts()
	retryAt := now.Add(5 * time.Minute)
	if dead {
		retryAt = now.Add(24 * time.Hour)
	}
	if retryErr := w.Repo.RetryFinancialEvent(ctx, claimed.OutboxID, retryAt, cause.Error(), dead); retryErr != nil {
		return errors.Join(cause, retryErr)
	}
	return cause
}

func (w Worker) maxAttempts() int {
	if w.MaxAttempts > 0 {
		return w.MaxAttempts
	}
	return 8
}

func validateInvoice(v Invoice) error {
	if v.ID == "" || v.CustomerID == "" || len(v.Currency) != 3 || v.Status == "" || v.CreatedAt.IsZero() || v.Subtotal < 0 || v.Tax < 0 || v.Total < 0 || v.AmountPaid < 0 || v.AmountRemaining < 0 || v.Tax > v.Total {
		return ErrInvalidFinancialRecord
	}
	if v.TaxEvidence.AutomaticTaxEnabled && v.TaxEvidence.AutomaticTaxStatus != "complete" {
		return ErrInvalidFinancialRecord
	}
	if v.TaxEvidence.CustomerTaxExempt != "" && v.TaxEvidence.CustomerTaxExempt != "none" && v.TaxEvidence.CustomerTaxExempt != "exempt" && v.TaxEvidence.CustomerTaxExempt != "reverse" {
		return ErrInvalidFinancialRecord
	}
	if v.TaxEvidence.CustomerCountry != "" && len(v.TaxEvidence.CustomerCountry) != 2 {
		return ErrInvalidFinancialRecord
	}
	taxTotal := int64(0)
	for _, amount := range v.TaxEvidence.Amounts {
		if amount.Amount < 0 || amount.TaxableAmount < 0 {
			return ErrInvalidFinancialRecord
		}
		taxTotal += amount.Amount
	}
	if taxTotal != v.Tax {
		return ErrInvalidFinancialRecord
	}
	seen := map[string]bool{}
	for _, line := range v.Lines {
		if line.ID == "" || seen[line.ID] || len(line.Currency) != 3 || line.Quantity < 0 {
			return ErrInvalidFinancialRecord
		}
		seen[line.ID] = true
	}
	return nil
}

func validateRefund(v Refund) error {
	if v.ID == "" || v.CustomerID == "" || v.ChargeID == "" || len(v.Currency) != 3 || v.Status == "" || v.Amount <= 0 || v.CreatedAt.IsZero() {
		return ErrInvalidFinancialRecord
	}
	return nil
}
