package api

import (
	"testing"

	"github.com/lkhmm520/portloom/internal/sysinfo"
)

func TestAgentSystemStoreRejectsLowerRevisionAfterNewerReport(t *testing.T) {
	store := NewAgentSystemStore()
	store.Record("agent-a", 2, sysinfo.Stats{RSSBytes: 200})
	before := store.Snapshot()["agent-a"]

	store.Record("agent-a", 1, sysinfo.Stats{RSSBytes: 100})
	after := store.Snapshot()["agent-a"]
	if after.RSSBytes != 200 {
		t.Fatalf("lower revision replaced RSS: got %d want 200", after.RSSBytes)
	}
	if !after.ReportedAt.Equal(before.ReportedAt) {
		t.Fatalf("lower revision refreshed ReportedAt: before=%s after=%s", before.ReportedAt, after.ReportedAt)
	}
}
