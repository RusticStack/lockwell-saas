package metering

import (
	"context"
	"errors"
	"testing"
	"time"
)

type workerRepo struct {
	export     Export
	claimed    bool
	sent       bool
	deadLetter bool
	retryAt    time.Time
}

func (*workerRepo) AppendRollup(context.Context, Rollup, MeterConfig) (Export, bool, error) {
	return Export{}, false, errors.New("unused")
}
func (r *workerRepo) ClaimNextExport(context.Context, time.Time) (Export, bool, error) {
	if r.claimed {
		return Export{}, false, nil
	}
	r.claimed = true
	return r.export, true, nil
}
func (r *workerRepo) MarkExportSent(context.Context, string, time.Time) error {
	r.sent = true
	return nil
}
func (r *workerRepo) MarkExportFailed(_ context.Context, _ string, retryAt time.Time, _ string, deadLetter bool) error {
	r.retryAt, r.deadLetter = retryAt, deadLetter
	return nil
}
func (*workerRepo) ClaimNextReconciliation(context.Context, time.Time) (Export, bool, error) {
	return Export{}, false, nil
}
func (*workerRepo) MarkReconciled(context.Context, string, int64, time.Time) error { return nil }
func (*workerRepo) MarkReconciliationPending(context.Context, string, time.Time, string) error {
	return nil
}

type workerProvider struct {
	err     error
	summary int64
}

func (p workerProvider) SendMeterEvent(context.Context, Export) error { return p.err }
func (p workerProvider) ReadMeterSummary(context.Context, Export) (int64, error) {
	return p.summary, p.err
}

func TestWorkerMarksSuccessfulExportSent(t *testing.T) {
	repo := &workerRepo{export: Export{ID: "exp_1", Attempts: 1}}
	processed, err := (Worker{Repo: repo, Provider: workerProvider{}}).RunOnce(context.Background())
	if err != nil || !processed || !repo.sent {
		t.Fatalf("processed=%v sent=%v err=%v", processed, repo.sent, err)
	}
}

func TestWorkerDeadLettersAtBoundedAttempt(t *testing.T) {
	repo := &workerRepo{export: Export{ID: "exp_1", Attempts: 8}}
	processed, err := (Worker{Repo: repo, Provider: workerProvider{err: errors.New("provider unavailable")}, MaxAttempts: 8}).RunOnce(context.Background())
	if !processed || err == nil || !repo.deadLetter {
		t.Fatalf("processed=%v deadLetter=%v err=%v", processed, repo.deadLetter, err)
	}
}

func TestWorkerReconcilesMatchingAggregate(t *testing.T) {
	repo := &workerRepo{export: Export{ID: "exp_1", ExpectedAggregate: 42}}
	repo.claimed = false
	// Reconciliation uses its own fake below because the export queue helper is intentionally separate.
	reconcileRepo := &reconcileWorkerRepo{export: repo.export}
	processed, err := (Worker{Repo: reconcileRepo, Provider: workerProvider{summary: 42}}).ReconcileOnce(context.Background())
	if err != nil || !processed || !reconcileRepo.reconciled {
		t.Fatalf("processed=%v reconciled=%v err=%v", processed, reconcileRepo.reconciled, err)
	}
}

type reconcileWorkerRepo struct {
	export     Export
	reconciled bool
}

func (*reconcileWorkerRepo) AppendRollup(context.Context, Rollup, MeterConfig) (Export, bool, error) {
	return Export{}, false, errors.New("unused")
}
func (*reconcileWorkerRepo) ClaimNextExport(context.Context, time.Time) (Export, bool, error) {
	return Export{}, false, nil
}
func (*reconcileWorkerRepo) MarkExportSent(context.Context, string, time.Time) error { return nil }
func (*reconcileWorkerRepo) MarkExportFailed(context.Context, string, time.Time, string, bool) error {
	return nil
}
func (r *reconcileWorkerRepo) ClaimNextReconciliation(context.Context, time.Time) (Export, bool, error) {
	return r.export, true, nil
}
func (r *reconcileWorkerRepo) MarkReconciled(context.Context, string, int64, time.Time) error {
	r.reconciled = true
	return nil
}
func (*reconcileWorkerRepo) MarkReconciliationPending(context.Context, string, time.Time, string) error {
	return nil
}
