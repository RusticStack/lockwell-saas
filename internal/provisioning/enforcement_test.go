package provisioning

import (
	"context"
	"errors"
	"testing"
	"time"
)

type enforcementRepo struct {
	job             EnforcementJob
	ok              bool
	completedStatus string
	retried, dead   bool
}

func (r *enforcementRepo) ClaimNextEnforcement(context.Context, time.Time) (EnforcementJob, bool, error) {
	return r.job, r.ok, nil
}
func (r *enforcementRepo) CompleteEnforcement(_ context.Context, _, status, _ string, _ time.Time) error {
	r.completedStatus = status
	return nil
}
func (r *enforcementRepo) RetryEnforcement(_ context.Context, _ string, _ time.Time, _ string, dead bool) error {
	r.retried = true
	r.dead = dead
	return nil
}

type suspenderStub struct {
	calls int
	err   error
}

func (s *suspenderStub) Suspend(context.Context, Reservation, string, string) error {
	s.calls++
	return s.err
}
func TestEnforcementSuspendsProvisionedTenantAfterReadback(t *testing.T) {
	repo := &enforcementRepo{ok: true, job: EnforcementJob{OutboxID: "o", Status: "suspended", HasProvision: true, AccessKeyID: "AKIA", Reservation: Reservation{AdminSecretRef: "cell"}}}
	vault := &memoryVault{values: map[string][]byte{"cell": []byte("admin")}}
	cells := &suspenderStub{}
	processed, err := (EnforcementWorker{Repo: repo, Vault: vault, Cells: cells}).RunOnce(context.Background())
	if err != nil || !processed || cells.calls != 1 || repo.completedStatus != "suspended" {
		t.Fatalf("processed=%v err=%v calls=%d status=%q", processed, err, cells.calls, repo.completedStatus)
	}
}
func TestEnforcementRetriesAndDeadLettersFailedSuspension(t *testing.T) {
	repo := &enforcementRepo{ok: true, job: EnforcementJob{OutboxID: "o", Status: "canceled", Attempts: 8, HasProvision: true, AccessKeyID: "AKIA", Reservation: Reservation{AdminSecretRef: "cell"}}}
	vault := &memoryVault{values: map[string][]byte{"cell": []byte("admin")}}
	cells := &suspenderStub{err: errors.New("readback failed")}
	if _, err := (EnforcementWorker{Repo: repo, Vault: vault, Cells: cells}).RunOnce(context.Background()); err == nil || !repo.retried || !repo.dead {
		t.Fatalf("err=%v retried=%v dead=%v", err, repo.retried, repo.dead)
	}
}
func TestEnforcementCompletesActiveWithoutProvisioning(t *testing.T) {
	repo := &enforcementRepo{ok: true, job: EnforcementJob{OutboxID: "o", Status: "active"}}
	processed, err := (EnforcementWorker{Repo: repo}).RunOnce(context.Background())
	if err != nil || !processed || repo.completedStatus != "" {
		t.Fatalf("processed=%v err=%v status=%q", processed, err, repo.completedStatus)
	}
}
