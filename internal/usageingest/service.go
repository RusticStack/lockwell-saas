package usageingest

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/RusticStack/lockwell-saas/internal/metering"
)

type Window struct {
	CellID          string    `json:"cell_id"`
	TenantID        string    `json:"tenant_id"`
	WindowStart     time.Time `json:"window_start"`
	WindowEnd       time.Time `json:"window_end"`
	SourceRevision  string    `json:"source_revision"`
	StorageMiBHours int64     `json:"storage_mib_hours"`
	Operations      int64     `json:"operations"`
	EgressMiB       int64     `json:"egress_mib"`
	SourceDigest    string    `json:"source_digest"`
}

type Repository interface {
	AppendUsageWindow(context.Context, Window, [32]byte, map[metering.Metric]metering.MeterConfig) (bool, error)
}

type Service struct {
	Repo   Repository
	Meters map[metering.Metric]metering.MeterConfig
	Now    func() time.Time
}

func (s Service) Record(ctx context.Context, in Window) (bool, error) {
	in.CellID = strings.TrimSpace(in.CellID)
	in.TenantID = strings.TrimSpace(in.TenantID)
	in.SourceRevision = strings.TrimSpace(in.SourceRevision)
	in.SourceDigest = strings.ToLower(strings.TrimSpace(in.SourceDigest))
	in.WindowStart = in.WindowStart.UTC()
	in.WindowEnd = in.WindowEnd.UTC()
	if in.CellID == "" || in.TenantID == "" || in.SourceRevision == "" {
		return false, errors.New("cell, tenant, and source revision are required")
	}
	if !in.WindowEnd.After(in.WindowStart) || in.WindowStart.Unix()%60 != 0 || in.WindowEnd.Unix()%60 != 0 {
		return false, errors.New("usage window must be minute-aligned with end after start")
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	if in.WindowEnd.After(now.Add(5*time.Minute)) || in.StorageMiBHours < 0 || in.Operations < 0 || in.EgressMiB < 0 {
		return false, errors.New("usage window values are invalid")
	}
	for _, metric := range []metering.Metric{metering.StorageMiBHours, metering.Operations, metering.EgressMiB} {
		meter := s.Meters[metric]
		if meter.EventName == "" || meter.MeterID == "" {
			return false, errors.New("all usage metrics require approved Stripe event and meter IDs")
		}
	}
	digest := Digest(in)
	provided, err := hex.DecodeString(in.SourceDigest)
	if err != nil || len(provided) != sha256.Size || !equalDigest(provided, digest[:]) {
		return false, errors.New("source digest does not match canonical usage evidence")
	}
	return s.Repo.AppendUsageWindow(ctx, in, digest, s.Meters)
}

func Digest(in Window) [32]byte {
	evidence, _ := json.Marshal(struct {
		CellID          string `json:"cell_id"`
		TenantID        string `json:"tenant_id"`
		WindowStart     int64  `json:"window_start_unix"`
		WindowEnd       int64  `json:"window_end_unix"`
		SourceRevision  string `json:"source_revision"`
		StorageMiBHours int64  `json:"storage_mib_hours"`
		Operations      int64  `json:"operations"`
		EgressMiB       int64  `json:"egress_mib"`
	}{in.CellID, in.TenantID, in.WindowStart.UTC().Unix(), in.WindowEnd.UTC().Unix(), in.SourceRevision, in.StorageMiBHours, in.Operations, in.EgressMiB})
	return sha256.Sum256(evidence)
}

func equalDigest(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}
