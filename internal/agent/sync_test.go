package agent

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"testing"
)

type fakeServerClient struct {
	states    []DesiredState
	fetched   []int64
	reported  []ObservedState
	fetchErr  error
	reportErr error
}

func (c *fakeServerClient) FetchDesired(_ context.Context, observed int64) (DesiredState, error) {
	c.fetched = append(c.fetched, observed)
	if c.fetchErr != nil {
		return DesiredState{}, c.fetchErr
	}
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

type timeoutOnlyNetError struct{}

func (timeoutOnlyNetError) Error() string   { return "TLS handshake timeout" }
func (timeoutOnlyNetError) Timeout() bool   { return true }
func (timeoutOnlyNetError) Temporary() bool { return true }

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

func TestSyncOnceReconcilesCachedDesiredOnTemporaryFetchFailure(t *testing.T) {
	c := &fakeServerClient{states: []DesiredState{{Revision: 7}}}
	r := &fakeStateReconciler{}
	s := NewSyncer(c, r)
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.fetchErr = &url.Error{Op: "Get", URL: "https://portloom.example/api/v1/agent/desired", Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}}
	if err := s.SyncOnce(context.Background()); err == nil {
		t.Fatal("expected fetch error")
	}
	if len(r.calls) != 2 || r.calls[1].Revision != 7 {
		t.Fatalf("reconcile calls=%#v", r.calls)
	}
}

func TestSyncOnceDoesNotUseCachedDesiredOnPermanentFetchFailure(t *testing.T) {
	c := &fakeServerClient{states: []DesiredState{{Revision: 7}}}
	r := &fakeStateReconciler{}
	s := NewSyncer(c, r)
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.fetchErr = &HTTPStatusError{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized"}
	if err := s.SyncOnce(context.Background()); err == nil {
		t.Fatal("expected fetch error")
	}
	if len(r.calls) != 1 {
		t.Fatalf("reconcile calls=%#v", r.calls)
	}
}

func TestSyncOncePermanentFailureInvalidatesCachedDesired(t *testing.T) {
	c := &fakeServerClient{states: []DesiredState{{Revision: 7}}}
	r := &fakeStateReconciler{}
	s := NewSyncer(c, r)
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.fetchErr = &HTTPStatusError{StatusCode: http.StatusForbidden, Status: "403 Forbidden"}
	if err := s.SyncOnce(context.Background()); err == nil {
		t.Fatal("expected permanent fetch error")
	}
	c.fetchErr = &HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable"}
	if err := s.SyncOnce(context.Background()); err == nil {
		t.Fatal("expected temporary fetch error")
	}
	if len(r.calls) != 1 {
		t.Fatalf("invalidated desired state was reconciled: %#v", r.calls)
	}
}

func TestRecoverableFetchErrorClassification(t *testing.T) {
	certificateError := x509.UnknownAuthorityError{Cert: &x509.Certificate{}}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "connection refused", err: &url.Error{Op: "Get", URL: "https://portloom.example", Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}}, want: true},
		{name: "connection reset", err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, want: true},
		{name: "client timeout", err: context.DeadlineExceeded, want: true},
		{name: "wrapped TLS handshake timeout", err: &url.Error{Op: "Get", URL: "https://portloom.example", Err: timeoutOnlyNetError{}}, want: true},
		{name: "truncated response", err: io.ErrUnexpectedEOF, want: true},
		{name: "request timeout", err: &HTTPStatusError{StatusCode: http.StatusRequestTimeout, Status: "408 Request Timeout"}, want: true},
		{name: "too early", err: &HTTPStatusError{StatusCode: http.StatusTooEarly, Status: "425 Too Early"}, want: true},
		{name: "too many requests", err: &HTTPStatusError{StatusCode: http.StatusTooManyRequests, Status: "429 Too Many Requests"}, want: true},
		{name: "status 500", err: &HTTPStatusError{StatusCode: 500, Status: "500 Internal Server Error"}, want: true},
		{name: "status 599", err: &HTTPStatusError{StatusCode: 599, Status: "599 Nonstandard Server Error"}, want: true},
		{name: "unauthorized", err: &HTTPStatusError{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized"}, want: false},
		{name: "forbidden", err: &HTTPStatusError{StatusCode: http.StatusForbidden, Status: "403 Forbidden"}, want: false},
		{name: "status 499", err: &HTTPStatusError{StatusCode: 499, Status: "499 Client Error"}, want: false},
		{name: "status 600", err: &HTTPStatusError{StatusCode: 600, Status: "600 Nonstandard Error"}, want: false},
		{name: "parent canceled", err: context.Canceled, want: false},
		{name: "certificate validation", err: &url.Error{Op: "Get", URL: "https://portloom.example", Err: certificateError}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := recoverableFetchError(tc.err); got != tc.want {
				t.Fatalf("recoverableFetchError(%T)=%v want %v", tc.err, got, tc.want)
			}
		})
	}
}
