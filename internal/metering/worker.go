package metering

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Provider interface {
	SendMeterEvent(context.Context, Export) error
	ReadMeterSummary(context.Context, Export) (int64, error)
}

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
	export, ok, err := w.Repo.ClaimNextExport(ctx, now)
	if err != nil || !ok {
		return ok, err
	}
	if err := w.Provider.SendMeterEvent(ctx, export); err != nil {
		maxAttempts := w.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 8
		}
		deadLetter := export.Attempts >= maxAttempts
		retryAt := now.Add(backoff(export.Attempts))
		if markErr := w.Repo.MarkExportFailed(ctx, export.ID, retryAt, err.Error(), deadLetter); markErr != nil {
			return true, errors.Join(err, markErr)
		}
		return true, fmt.Errorf("send meter event: %w", err)
	}
	if err := w.Repo.MarkExportSent(ctx, export.ID, now); err != nil {
		return true, err
	}
	return true, nil
}

func (w Worker) ReconcileOnce(ctx context.Context) (bool, error) {
	now := time.Now().UTC()
	if w.Now != nil {
		now = w.Now().UTC()
	}
	export, ok, err := w.Repo.ClaimNextReconciliation(ctx, now)
	if err != nil || !ok {
		return ok, err
	}
	aggregated, err := w.Provider.ReadMeterSummary(ctx, export)
	if err != nil {
		if markErr := w.Repo.MarkReconciliationPending(ctx, export.ID, now.Add(5*time.Minute), err.Error()); markErr != nil {
			return true, errors.Join(err, markErr)
		}
		return true, err
	}
	if aggregated != export.ExpectedAggregate {
		message := fmt.Sprintf("Stripe aggregate %d does not match internal aggregate %d", aggregated, export.ExpectedAggregate)
		if err := w.Repo.MarkReconciliationPending(ctx, export.ID, now.Add(5*time.Minute), message); err != nil {
			return true, err
		}
		return true, errors.New(message)
	}
	return true, w.Repo.MarkReconciled(ctx, export.ID, aggregated, now)
}

func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<uint(attempt-1)) * time.Minute
}
