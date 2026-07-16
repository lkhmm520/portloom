package agent

import (
	"context"
	"errors"
	"testing"
)

type fakeServerClient struct {
	states    []DesiredState
	fetched   []int64
	reported  []ObservedState
	reportErr error
}

func (c *fakeServerClient) FetchDesired(_ context.Context, observed int64) (DesiredState, error) {
	c.fetched = append(c.fetched, observed)
	if len(c.states) == 0 {
		return DesiredState{}, errors.New("no state")
	}
	s := c.states[0]
	c.states = c.states[1:]
	return s, nil
}
func (c *fakeServerClient) ReportObserved(_ context.Context, s ObservedState) error {
	c.reported = append(c.reported, s)
	return c.reportErr
}

type fakeStateReconciler struct{ calls []DesiredState }

func (r *fakeStateReconciler) Reconcile(_ context.Context, s DesiredState) ObservedState {
	r.calls = append(r.calls, s)
	return ObservedState{Revision: s.Revision}
}
func TestSyncOnceCarriesSuccessfullyReportedObservedRevision(t *testing.T) {
	c := &fakeServerClient{states: []DesiredState{{Revision: 3}, {Revision: 5}}}
	s := NewSyncer(c, &fakeStateReconciler{})
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.fetched) != 2 || c.fetched[0] != 0 || c.fetched[1] != 3 {
		t.Fatalf("fetched=%#v", c.fetched)
	}
	if len(c.reported) != 2 || s.ObservedRevision() != 5 {
		t.Fatalf("reported=%#v revision=%d", c.reported, s.ObservedRevision())
	}
}
func TestSyncOnceDoesNotAdvanceRevisionWhenReportFails(t *testing.T) {
	c := &fakeServerClient{states: []DesiredState{{Revision: 4}}, reportErr: errors.New("offline")}
	s := NewSyncer(c, &fakeStateReconciler{})
	if err := s.SyncOnce(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if s.ObservedRevision() != 0 {
		t.Fatalf("revision=%d", s.ObservedRevision())
	}
}
