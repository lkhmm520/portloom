package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/store"
)

func TestDurablePublicationGateControlsNativeAndCompatibilityGateway(t *testing.T) {
	ctx := context.Background()
	probe, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	start := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	opts := store.Options{PortRangeStart: start, PortRangeEnd: 65535}
	path := filepath.Join(t.TempDir(), "state.db")
	open := func() *store.Store {
		t.Helper()
		s, err := store.Open(path, opts)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	s := open()
	if err := s.CreateEnrollmentToken(ctx, "enroll", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	a, _, err := s.ConsumeEnrollmentToken(ctx, "enroll", "gateway-agent")
	if err != nil {
		t.Fatal(err)
	}
	const app = "app.example.test"
	const unrelated = "other.example.test"
	var revision int64
	var observations []domain.RouteObservation
	create := func(host, prefix, body string) {
		t.Helper()
		r, err := s.CreateRoute(ctx, domain.Route{ClientID: a.ID, Name: host, Protocol: domain.ProtocolHTTPS,
			Domain: host, PathPrefix: prefix, LocalHost: "localhost", LocalPort: 8080, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", r.RemotePort))
		if err != nil {
			t.Fatal(err)
		}
		upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, body) }))
		_ = upstream.Listener.Close()
		upstream.Listener = listener
		upstream.Start()
		t.Cleanup(upstream.Close)
		revision = r.DesiredRevision
		observations = append(observations, domain.RouteObservation{RouteID: r.ID, ObservedRevision: revision, TunnelStatus: "up"})
	}
	create(app, "", "root")
	create(app, "/private", "private")
	create(unrelated, "", "unrelated")
	if err := s.Heartbeat(ctx, a.ID, revision, observations); err != nil {
		t.Fatal(err)
	}
	h := New(s, WithHTTPSRedirect(443))
	native := Edge{Scheme: "https", Default: true, Port: 443}
	legacy := Edge{}
	plain := Edge{Scheme: "http", Default: true, Port: 80}
	assert := func(host, path string, edge Edge, status int, body string) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "http://gateway"+path, nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r.WithContext(WithEdge(r.Context(), edge)))
		if w.Code != status || (body != "" && w.Body.String() != body) || (status == 404 && w.Header().Get("Location") != "") {
			t.Fatalf("%+v %s%s: got %d %q (location %q), want %d %q", edge, host, path, w.Code, w.Body.String(), w.Header().Get("Location"), status, body)
		}
	}
	assert("APP.Example.Test.:443", "/private/x", native, 200, "private")
	assert(app, "/", legacy, 200, "root")
	assert(app, "/private", plain, 308, "")
	policy := store.PublicationPolicy{Phase: store.PublicationBlocked, OperationID: "cutover", ClientID: a.ID}
	transition := func(expected store.PublicationGate) {
		t.Helper()
		if err := s.TransitionPublicationGates(ctx, "console.example.test", []store.PublicationGateExpectation{{Host: app, Expected: expected}}, policy); err != nil {
			t.Fatal(err)
		}
	}
	transition(store.PublicationGate{})
	assert(app, "/private", native, 404, "")
	assert(app, "/private", legacy, 404, "")
	assert(app, "/", native, 404, "")
	assert(app, "/private", plain, 404, "")
	if enabled, err := s.HTTPSDomainEnabled(ctx, app); err != nil || enabled {
		t.Fatalf("certificate authorization for blocked host = %v, %v", enabled, err)
	}
	if enabled, err := s.HTTPSDomainEnabled(ctx, unrelated); err != nil || !enabled {
		t.Fatalf("certificate authorization for unrelated host = %v, %v", enabled, err)
	}
	r := httptest.NewRequest(http.MethodGet, "http://"+app+"/private", nil)
	if h.ServeIfMatch(httptest.NewRecorder(), r) {
		t.Fatal("ServeIfMatch claimed a blocked route")
	}
	assert(unrelated, "/", native, 200, "unrelated")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	h = New(s, WithHTTPSRedirect(443))
	assert(app, "/private", native, 404, "")
	expected := store.PublicationGate{PublicationPolicy: policy, Generation: 1}
	policy.Phase = store.PublicationFrozen
	// Reopening the gate cannot override a down tunnel.
	observations[1].TunnelStatus = "down"
	if err := s.Heartbeat(ctx, a.ID, revision, observations); err != nil {
		t.Fatal(err)
	}
	transition(expected)
	assert(app, "/private", native, 200, "root")
	observations[1].TunnelStatus = "up"
	if err := s.Heartbeat(ctx, a.ID, revision, observations); err != nil {
		t.Fatal(err)
	}
	assert(app, "/private", native, 200, "private")
	assert(app, "/private", plain, 308, "")
	expected = store.PublicationGate{PublicationPolicy: policy, Generation: 2}
	policy.Phase = store.PublicationBlocked
	transition(expected)
	assert(app, "/private", native, 404, "")
	assert(unrelated, "/", native, 200, "unrelated")
}
