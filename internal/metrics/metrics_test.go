package metrics

import (
	"testing"
	"time"
)

func TestRegistryAccumulatesTotalsAndPerRouteCounters(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 30, 0, time.UTC)
	registry := NewWithClock(func() time.Time { return now })
	registry.ObserveHTTP("route-a", 100, 200)
	registry.ObserveHTTP("route-a", 1, 2)
	registry.ObserveStream("route-b", 10, 20)

	snapshot := registry.Snapshot()
	if snapshot.Total.Requests != 3 || snapshot.Total.BytesIn != 111 || snapshot.Total.BytesOut != 222 {
		t.Fatalf("total=%+v", snapshot.Total)
	}
	if got := snapshot.Routes["route-a"]; got.Requests != 2 || got.BytesIn != 101 || got.BytesOut != 202 {
		t.Fatalf("route-a=%+v", got)
	}
	if got := snapshot.Routes["route-b"]; got.Requests != 1 || got.BytesIn != 10 || got.BytesOut != 20 {
		t.Fatalf("route-b=%+v", got)
	}
}

func TestRegistrySeriesBucketsByMinuteAndZeroFills(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 30, 0, time.UTC)
	registry := NewWithClock(func() time.Time { return now })
	registry.ObserveHTTP("route-a", 5, 5)
	now = now.Add(2 * time.Minute)
	registry.ObserveHTTP("route-a", 7, 7)

	snapshot := registry.Snapshot()
	if len(snapshot.Series) != 60 {
		t.Fatalf("series length=%d", len(snapshot.Series))
	}
	last := snapshot.Series[len(snapshot.Series)-1]
	if last.BytesIn != 7 || last.Requests != 1 {
		t.Fatalf("latest sample=%+v", last)
	}
	previous := snapshot.Series[len(snapshot.Series)-2]
	if previous.BytesIn != 0 || previous.Requests != 0 {
		t.Fatalf("gap sample was not zero-filled: %+v", previous)
	}
	older := snapshot.Series[len(snapshot.Series)-3]
	if older.BytesIn != 5 {
		t.Fatalf("older sample=%+v", older)
	}
}

func TestRegistrySlotReuseAfterWrapAround(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	registry := NewWithClock(func() time.Time { return now })
	registry.ObserveHTTP("route-a", 1, 1)
	// Sixty minutes later the same ring slot is reused and must reset.
	now = now.Add(60 * time.Minute)
	registry.ObserveHTTP("route-a", 2, 2)
	snapshot := registry.Snapshot()
	last := snapshot.Series[len(snapshot.Series)-1]
	if last.BytesIn != 2 || last.Requests != 1 {
		t.Fatalf("wrapped slot=%+v", last)
	}
	for _, sample := range snapshot.Series[:len(snapshot.Series)-1] {
		if sample.BytesIn != 0 {
			t.Fatalf("stale slot leaked into series: %+v", sample)
		}
	}
}

func TestRegistryForgetDropsRoute(t *testing.T) {
	registry := New()
	registry.ObserveHTTP("route-a", 1, 1)
	registry.Forget("route-a")
	if _, ok := registry.Snapshot().Routes["route-a"]; ok {
		t.Fatal("forgotten route still present")
	}
}
