// Package metrics keeps in-memory per-route traffic counters and a rolling
// per-minute series for the dashboard. Counters reset on server restart.
package metrics

import (
	"sync"
	"time"
)

const seriesMinutes = 60

type Counters struct {
	Requests int64 `json:"requests"`
	BytesIn  int64 `json:"bytes_in"`
	BytesOut int64 `json:"bytes_out"`
}

type Sample struct {
	Unix     int64 `json:"t"`
	Requests int64 `json:"requests"`
	BytesIn  int64 `json:"bytes_in"`
	BytesOut int64 `json:"bytes_out"`
}

type Snapshot struct {
	Total  Counters            `json:"total"`
	Routes map[string]Counters `json:"routes"`
	Series []Sample            `json:"series"`
}

type Registry struct {
	mu       sync.Mutex
	total    Counters
	perRoute map[string]*Counters
	slots    [seriesMinutes]Sample
	now      func() time.Time
}

func New() *Registry {
	return &Registry{perRoute: map[string]*Counters{}, now: time.Now}
}

// NewWithClock is used by tests to control sample bucketing.
func NewWithClock(now func() time.Time) *Registry {
	registry := New()
	registry.now = now
	return registry
}

// ObserveHTTP records one proxied HTTP request.
func (r *Registry) ObserveHTTP(routeID string, requestBytes, responseBytes int64) {
	r.observe(routeID, 1, requestBytes, responseBytes)
}

// ObserveStream records one finished TCP connection or UDP session.
func (r *Registry) ObserveStream(routeID string, bytesIn, bytesOut int64) {
	r.observe(routeID, 1, bytesIn, bytesOut)
}

func (r *Registry) observe(routeID string, requests, bytesIn, bytesOut int64) {
	minute := r.now().Unix() / 60
	r.mu.Lock()
	defer r.mu.Unlock()
	counters, ok := r.perRoute[routeID]
	if !ok {
		counters = &Counters{}
		r.perRoute[routeID] = counters
	}
	counters.Requests += requests
	counters.BytesIn += bytesIn
	counters.BytesOut += bytesOut
	r.total.Requests += requests
	r.total.BytesIn += bytesIn
	r.total.BytesOut += bytesOut
	slot := &r.slots[minute%seriesMinutes]
	if slot.Unix != minute*60 {
		*slot = Sample{Unix: minute * 60}
	}
	slot.Requests += requests
	slot.BytesIn += bytesIn
	slot.BytesOut += bytesOut
}

// Forget drops the counters of a deleted route.
func (r *Registry) Forget(routeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.perRoute, routeID)
}

// Snapshot returns totals, per-route counters, and the per-minute series for
// the last hour ordered oldest to newest. Missing minutes are zero-filled.
func (r *Registry) Snapshot() Snapshot {
	minute := r.now().Unix() / 60
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := Snapshot{
		Total:  r.total,
		Routes: make(map[string]Counters, len(r.perRoute)),
		Series: make([]Sample, 0, seriesMinutes),
	}
	for id, counters := range r.perRoute {
		snapshot.Routes[id] = *counters
	}
	for offset := seriesMinutes - 1; offset >= 0; offset-- {
		target := minute - int64(offset)
		slot := r.slots[target%seriesMinutes]
		if slot.Unix == target*60 {
			snapshot.Series = append(snapshot.Series, slot)
		} else {
			snapshot.Series = append(snapshot.Series, Sample{Unix: target * 60})
		}
	}
	return snapshot
}
