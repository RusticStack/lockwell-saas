package metering

import (
	"testing"
	"time"
)

func TestIdentifierIsStableAcrossRetriesAndDistinctAcrossRevisions(t *testing.T) {
	start := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)
	end := start.Add(time.Hour)
	one, err := NewRollup("acct", "cus", Operations, start, end, 42, "rev-1", []byte("evidence"))
	if err != nil {
		t.Fatal(err)
	}
	two := one
	two.ID = "different-local-id"
	if Identifier(one) != Identifier(two) {
		t.Fatal("retry identity changed with local row ID")
	}
	two.SourceRevision = "rev-2"
	if Identifier(one) == Identifier(two) {
		t.Fatal("different source revision reused Stripe identifier")
	}
}

func TestNewRollupRejectsNegativeAndUnknownMetric(t *testing.T) {
	end := time.Now().UTC().Add(-time.Minute)
	start := end.Add(-time.Hour)
	if _, err := NewRollup("acct", "cus", Operations, start, end, -1, "rev", nil); err == nil {
		t.Fatal("negative usage accepted")
	}
	if _, err := NewRollup("acct", "cus", Metric("browser_supplied"), start, end, 1, "rev", nil); err == nil {
		t.Fatal("unknown metric accepted")
	}
}
