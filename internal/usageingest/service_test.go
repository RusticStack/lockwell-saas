package usageingest

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/RusticStack/lockwell-saas/internal/metering"
)

type recordingRepo struct {
	window Window
	digest [32]byte
	calls  int
}

func (r *recordingRepo) AppendUsageWindow(_ context.Context, window Window, digest [32]byte, _ map[metering.Metric]metering.MeterConfig) (bool, error) {
	r.calls++
	r.window, r.digest = window, digest
	return true, nil
}

func testWindow() Window {
	end := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	w := Window{CellID: "cell-a", TenantID: "tenant-a", WindowStart: end.Add(-time.Hour), WindowEnd: end, SourceRevision: "edge-ledger-42", StorageMiBHours: 512, Operations: 41, EgressMiB: 7}
	digest := Digest(w)
	w.SourceDigest = hex.EncodeToString(digest[:])
	return w
}

func testService(repo Repository) Service {
	return Service{Repo: repo, Now: func() time.Time { return time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC) }, Meters: map[metering.Metric]metering.MeterConfig{
		metering.StorageMiBHours: {EventName: "storage", MeterID: "mtr_storage"},
		metering.Operations:      {EventName: "operations", MeterID: "mtr_operations"},
		metering.EgressMiB:       {EventName: "egress", MeterID: "mtr_egress"},
	}}
}

func TestRecordValidatesCanonicalEvidenceBeforeRepository(t *testing.T) {
	repo := &recordingRepo{}
	created, err := testService(repo).Record(context.Background(), testWindow())
	if err != nil || !created || repo.calls != 1 || repo.window.TenantID != "tenant-a" {
		t.Fatalf("created=%v calls=%d window=%#v err=%v", created, repo.calls, repo.window, err)
	}
}

func TestRecordRejectsTamperingAndBadWindows(t *testing.T) {
	for name, mutate := range map[string]func(*Window){
		"digest":    func(w *Window) { w.Operations++ },
		"future":    func(w *Window) { w.WindowEnd = time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC) },
		"negative":  func(w *Window) { w.EgressMiB = -1 },
		"unaligned": func(w *Window) { w.WindowStart = w.WindowStart.Add(time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			repo := &recordingRepo{}
			window := testWindow()
			mutate(&window)
			if _, err := testService(repo).Record(context.Background(), window); err == nil || repo.calls != 0 {
				t.Fatalf("err=%v calls=%d", err, repo.calls)
			}
		})
	}
}
