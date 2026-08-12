package provisioning

import (
	"context"
	"errors"
	"time"
)

type EnforcementJob struct {
	OutboxID, AccountID, Status string
	Attempts                    int
	Reservation                 Reservation
	AccessKeyID                 string
	HasProvision                bool
}
type EnforcementRepository interface {
	ClaimNextEnforcement(context.Context, time.Time) (EnforcementJob, bool, error)
	CompleteEnforcement(context.Context, string, string, string, time.Time) error
	RetryEnforcement(context.Context, string, time.Time, string, bool) error
}
type CellSuspender interface {
	Suspend(context.Context, Reservation, string, string) error
}
type EnforcementWorker struct {
	Repo  EnforcementRepository
	Vault SecretVault
	Cells CellSuspender
	Now   func() time.Time
}

func (w EnforcementWorker) RunOnce(ctx context.Context) (bool, error) {
	now := time.Now().UTC()
	if w.Now != nil {
		now = w.Now().UTC()
	}
	job, ok, err := w.Repo.ClaimNextEnforcement(ctx, now)
	if err != nil || !ok {
		return ok, err
	}
	switch job.Status {
	case "active", "grace":
		return true, w.Repo.CompleteEnforcement(ctx, job.OutboxID, "", "ready for customer credential request", now)
	case "suspended", "canceled":
		if !job.HasProvision {
			return true, w.Repo.CompleteEnforcement(ctx, job.OutboxID, "", "no provisioned cell to suspend", now)
		}
		adminToken, err := w.Vault.Get(ctx, job.Reservation.AdminSecretRef)
		if err == nil {
			err = w.Cells.Suspend(ctx, job.Reservation, string(adminToken), job.AccessKeyID)
		}
		if err != nil {
			retryErr := w.Repo.RetryEnforcement(ctx, job.OutboxID, now.Add(5*time.Minute), err.Error(), job.Attempts >= 8)
			return true, errors.Join(err, retryErr)
		}
		return true, w.Repo.CompleteEnforcement(ctx, job.OutboxID, "suspended", "serving credential revoked", now)
	default:
		err := errors.New("unsupported entitlement enforcement state")
		retryErr := w.Repo.RetryEnforcement(ctx, job.OutboxID, now.Add(time.Hour), err.Error(), job.Attempts >= 8)
		return true, errors.Join(err, retryErr)
	}
}
