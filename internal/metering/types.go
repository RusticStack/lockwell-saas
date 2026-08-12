package metering

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
)

type Metric string

const (
	StorageMiBHours Metric = "storage_mib_hours"
	Operations      Metric = "operations"
	EgressMiB       Metric = "egress_mib"
)

type Rollup struct {
	ID               string
	AccountID        string
	StripeCustomerID string
	Metric           Metric
	WindowStart      time.Time
	WindowEnd        time.Time
	Value            int64
	SourceRevision   string
	SourceDigest     [32]byte
}

type Export struct {
	ID                string
	RollupID          string
	StripeCustomerID  string
	EventName         string
	MeterID           string
	Identifier        string
	WindowStart       time.Time
	WindowEnd         time.Time
	Value             int64
	ExpectedAggregate int64
	Attempts          int
}

type Repository interface {
	AppendRollup(context.Context, Rollup, MeterConfig) (Export, bool, error)
	ClaimNextExport(context.Context, time.Time) (Export, bool, error)
	MarkExportSent(context.Context, string, time.Time) error
	MarkExportFailed(context.Context, string, time.Time, string, bool) error
	ClaimNextReconciliation(context.Context, time.Time) (Export, bool, error)
	MarkReconciled(context.Context, string, int64, time.Time) error
	MarkReconciliationPending(context.Context, string, time.Time, string) error
}

type MeterConfig struct {
	EventName string
	MeterID   string
}

func NewRollup(accountID, customerID string, metric Metric, start, end time.Time, value int64, revision string, sourceEvidence []byte) (Rollup, error) {
	if accountID == "" || customerID == "" || revision == "" || !validMetric(metric) {
		return Rollup{}, errors.New("invalid usage identity")
	}
	start = start.UTC()
	end = end.UTC()
	if !end.After(start) || start.Unix()%60 != 0 || end.Unix()%60 != 0 || value < 0 || end.After(time.Now().UTC().Add(5*time.Minute)) {
		return Rollup{}, errors.New("invalid usage window or value")
	}
	id, err := randomID()
	if err != nil {
		return Rollup{}, err
	}
	return Rollup{ID: id, AccountID: accountID, StripeCustomerID: customerID, Metric: metric, WindowStart: start, WindowEnd: end, Value: value, SourceRevision: revision, SourceDigest: sha256.Sum256(sourceEvidence)}, nil
}

func Identifier(rollup Rollup) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%s", rollup.AccountID, rollup.Metric, rollup.WindowStart.Unix(), rollup.WindowEnd.Unix(), rollup.SourceRevision)))
	return fmt.Sprintf("lw_%x", digest[:16])
}

func validMetric(metric Metric) bool {
	return metric == StorageMiBHours || metric == Operations || metric == EgressMiB
}
