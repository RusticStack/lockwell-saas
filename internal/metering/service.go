package metering

import (
	"context"
	"errors"
	"time"
)

type Service struct {
	Repo   Repository
	Meters map[Metric]MeterConfig
}

func (s Service) Record(ctx context.Context, accountID, customerID string, metric Metric, start, end time.Time, value int64, revision string, evidence []byte) (Export, bool, error) {
	meter := s.Meters[metric]
	if meter.EventName == "" || meter.MeterID == "" {
		return Export{}, false, errors.New("metric is not mapped to an approved Stripe event")
	}
	rollup, err := NewRollup(accountID, customerID, metric, start, end, value, revision, evidence)
	if err != nil {
		return Export{}, false, err
	}
	return s.Repo.AppendRollup(ctx, rollup, meter)
}
