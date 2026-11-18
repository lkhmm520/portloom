package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
)

const testManagementHost = "console.example.test"

func gateStore(t *testing.T) (*Store, string, Options) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gates.db")
	opts := Options{PortRangeStart: 31070, PortRangeEnd: 31120}
	s, err := Open(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path, opts
}

func gateAgent(t *testing.T, s *Store, name string) domain.Agent {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateEnrollmentToken(ctx, name, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	a, _, err := s.ConsumeEnrollmentToken(ctx, name, name)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func gateRoute(t *testing.T, s *Store, owner, host, path string, enabled bool) domain.Route {
	t.Helper()
	r, err := s.CreateRoute(context.Background(), domain.Route{ClientID: owner, Name: host, Protocol: domain.ProtocolHTTPS,
		Domain: host, PathPrefix: path, LocalHost: "localhost", LocalPort: 8080, Enabled: enabled})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPublicationGateGroupDurableCAS(t *testing.T) {
	ctx := context.Background()
	s, dbPath, opts := gateStore(t)
	a := gateAgent(t, s, "gate-owner")
	gateRoute(t, s, a.ID, "a.example.test", "", true)
	gateRoute(t, s, a.ID, "a.example.test", "/disabled", false)
	gateRoute(t, s, a.ID, "b.example.test", "", true)
	group := []PublicationGateExpectation{{Host: " A.Example.Test. "}, {Host: "b.example.test"}}
	blocked := PublicationPolicy{Phase: PublicationBlocked, OperationID: "cutover-1", ClientID: a.ID}
	transition := func(group []PublicationGateExpectation, policy PublicationPolicy) error {
		return s.TransitionPublicationGates(ctx, testManagementHost, group, policy)
	}
	read := func(host string) PublicationGate {
		t.Helper()
		got, err := s.GetPublicationGate(ctx, host)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if err := transition(group, blocked); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = Open(dbPath, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	want := PublicationGate{PublicationPolicy: blocked, Generation: 1}
	if got := read(group[0].Host); got != want || read(group[1].Host) != want {
		t.Fatalf("reopened gate = %+v, want %+v for both hosts", got, want)
	}
	if err := transition([]PublicationGateExpectation{group[1], group[0]}, blocked); err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	if err := transition(group[:1], blocked); !errors.Is(err, ErrConflict) {
		t.Fatalf("subset retry = %v, want conflict", err)
	}
	fresh := []PublicationGateExpectation{{Host: group[0].Host, Expected: want}, {Host: group[1].Host, Expected: want}}
	serving := blocked
	serving.Phase = PublicationFrozen
	foreign := serving
	foreign.OperationID = "another-op"
	if err := transition(fresh, foreign); !errors.Is(err, ErrConflict) {
		t.Fatalf("takeover = %v, want conflict", err)
	}
	if err := transition(fresh[1:], serving); err != nil {
		t.Fatal(err)
	}
	if err := transition(fresh, serving); !errors.Is(err, ErrConflict) {
		t.Fatalf("mixed fresh/stale transition = %v, want conflict", err)
	}
	if got := read(group[0].Host); got != want {
		t.Fatalf("failed group mutated first gate: %+v", got)
	}
	release := blocked
	release.Phase = PublicationOpen
	if err := transition(fresh[:1], release); err != nil {
		t.Fatal(err)
	}
	if err := transition(fresh[:1], release); err != nil {
		t.Fatalf("release retry: %v", err)
	}
	if got := read(group[0].Host); got != (PublicationGate{Generation: 2}) {
		t.Fatalf("released gate = %+v, want open tombstone", got)
	}
	if err := transition(group[:1], blocked); !errors.Is(err, ErrConflict) {
		t.Fatalf("ABA replay = %v, want conflict", err)
	}
}

func TestPublicationGateFreezesRouteCRUD(t *testing.T) {
	ctx := context.Background()
	s, _, _ := gateStore(t)
	a := gateAgent(t, s, "freeze-owner")
	r := gateRoute(t, s, a.ID, "frozen.example.test", "", true)
	disabled := gateRoute(t, s, a.ID, r.Domain, "/disabled", false)
	other := gateRoute(t, s, a.ID, "other.example.test", "", true)
	policy := PublicationPolicy{Phase: PublicationFrozen, OperationID: "freeze", ClientID: a.ID}
	if err := s.TransitionPublicationGates(ctx, testManagementHost, []PublicationGateExpectation{{Host: r.Domain}}, policy); err != nil {
		t.Fatal(err)
	}
	create := r
	create.Domain, create.PathPrefix, create.Protocol = " FROZEN.Example.Test. ", "/new", domain.ProtocolHTTP
	if _, err := s.CreateRoute(ctx, create); !errors.Is(err, ErrConflict) {
		t.Fatalf("create on frozen host: %v", err)
	}
	moveOut := r
	moveOut.Domain, moveOut.Protocol, moveOut.PublicPort = "", domain.ProtocolTCP, 55555
	if _, err := s.UpdateRoute(ctx, r.ID, moveOut); !errors.Is(err, ErrConflict) {
		t.Fatalf("convert frozen route into stream: %v", err)
	}
	moveIn := other
	moveIn.Domain, moveIn.PathPrefix, moveIn.PublicPort = r.Domain, "/new", 8443
	if _, err := s.UpdateRoute(ctx, other.ID, moveIn); !errors.Is(err, ErrConflict) {
		t.Fatalf("move onto frozen host: %v", err)
	}
	if err := s.DeleteRoute(ctx, disabled.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete disabled frozen route: %v", err)
	}
	agent, routes, err := s.SyncAgent(ctx, a.ID)
	if err != nil || agent.DesiredRevision != other.DesiredRevision || len(routes) != 3 {
		t.Fatalf("rejected CRUD mutated desired state: %+v, %+v, %v", agent, routes, err)
	}
	if err := s.Heartbeat(ctx, a.ID, agent.DesiredRevision, []domain.RouteObservation{{RouteID: r.ID, ObservedRevision: agent.DesiredRevision, TunnelStatus: "up"}}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetRoute(ctx, r.ID); err != nil || !got.PublicationReady() {
		t.Fatalf("frozen serving route lost heartbeat/publication: %+v, %v", got, err)
	}
	if err := s.DeleteRoute(ctx, other.ID); err != nil {
		t.Fatalf("unrelated delete: %v", err)
	}
	for _, protocol := range []domain.Protocol{domain.ProtocolTCP, domain.ProtocolUDP} {
		moveOut.Protocol = protocol
		stream, err := s.CreateRoute(ctx, moveOut)
		if err != nil {
			t.Fatalf("unrelated stream create: %v", err)
		}
		if err := s.DeleteRoute(ctx, stream.ID); err != nil {
			t.Fatalf("unrelated stream delete: %v", err)
		}
	}
	gate, err := s.GetPublicationGate(ctx, r.Domain)
	if err != nil {
		t.Fatal(err)
	}
	policy.Phase = PublicationOpen
	if err := s.TransitionPublicationGates(ctx, testManagementHost, []PublicationGateExpectation{{Host: r.Domain, Expected: gate}}, policy); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRoute(ctx, disabled.ID); err != nil {
		t.Fatalf("release did not restore CRUD: %v", err)
	}
}

func TestPublicationGateRejectsUnsupportedOrAmbiguousGroups(t *testing.T) {
	ctx := context.Background()
	s, _, _ := gateStore(t)
	a := gateAgent(t, s, "supported-owner")
	b := gateAgent(t, s, "other-owner")
	good := gateRoute(t, s, a.ID, "a-good.example.test", "", true)
	mixed := gateRoute(t, s, a.ID, "mixed.example.test", "", true)
	gateRoute(t, s, b.ID, mixed.Domain, "/disabled", false)
	gateRoute(t, s, a.ID, testManagementHost, "/business", true)
	for _, protocol := range []domain.Protocol{domain.ProtocolHTTP, domain.ProtocolHTTPS} {
		r := good
		r.Domain, r.Protocol, r.Enabled = string(protocol)+"-unsupported.example.test", protocol, false
		if protocol == domain.ProtocolHTTPS {
			r.PublicPort = 8443
		}
		if _, err := s.CreateRoute(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	policy := PublicationPolicy{Phase: PublicationBlocked, OperationID: "rejected", ClientID: a.ID}
	for _, tc := range []struct {
		host string
		want error
	}{
		{mixed.Domain, ErrConflict}, {"missing.example.test", ErrConflict},
		{"http-unsupported.example.test", ErrConflict}, {"https-unsupported.example.test", ErrConflict},
		{" CONSOLE.Example.Test. ", ErrInvalid}, {" A-GOOD.Example.Test. ", ErrInvalid},
		{"*.example.test", ErrInvalid}, {"host.example.test:443", ErrInvalid},
	} {
		t.Run(tc.host, func(t *testing.T) {
			group := []PublicationGateExpectation{{Host: good.Domain}, {Host: tc.host}}
			if err := s.TransitionPublicationGates(ctx, testManagementHost, group, policy); !errors.Is(err, tc.want) {
				t.Fatalf("transition = %v, want %v", err, tc.want)
			}
			if got, err := s.GetPublicationGate(ctx, good.Domain); err != nil || got != (PublicationGate{}) {
				t.Fatalf("invalid group changed eligible host: %+v, %v", got, err)
			}
		})
	}
}

func TestPublicationGateRollsBackAfterSecondHostWriteFails(t *testing.T) {
	ctx := context.Background()
	s, dbPath, opts := gateStore(t)
	a := gateAgent(t, s, "rollback-owner")
	x := gateRoute(t, s, a.ID, "a.example.test", "", true)
	y := gateRoute(t, s, a.ID, "b.example.test", "", true)
	reader, err := Open(dbPath, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_second BEFORE INSERT ON publication_gates
		WHEN new.host = 'b.example.test' BEGIN SELECT RAISE(ABORT, 'injected failure'); END;`); err != nil {
		t.Fatal(err)
	}
	policy := PublicationPolicy{Phase: PublicationBlocked, OperationID: "rollback", ClientID: a.ID}
	if err := s.TransitionPublicationGates(ctx, testManagementHost, []PublicationGateExpectation{{Host: x.Domain}, {Host: y.Domain}}, policy); err == nil {
		t.Fatal("expected injected write failure")
	}
	for _, host := range []string{x.Domain, y.Domain} {
		if got, err := reader.GetPublicationGate(ctx, host); err != nil || got != (PublicationGate{}) {
			t.Fatalf("independent reader saw a partial group: %+v, %v", got, err)
		}
	}
}

func TestPublicationGateSerializesWithCompetingRouteCreate(t *testing.T) {
	ctx := context.Background()
	s, dbPath, opts := gateStore(t)
	a := gateAgent(t, s, "concurrent-owner")
	r := gateRoute(t, s, a.ID, "race.example.test", "", true)
	writer, err := Open(dbPath, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	group := []PublicationGateExpectation{{Host: r.Domain}}
	policy := PublicationPolicy{Phase: PublicationBlocked, OperationID: "race", ClientID: a.ID}
	conflicting := r
	conflicting.Protocol = domain.ProtocolHTTP // if this commits first, the group is ineligible
	start := make(chan struct{})
	gateDone := make(chan error, 1)
	createDone := make(chan error, 1)
	go func() {
		<-start
		gateDone <- s.TransitionPublicationGates(ctx, testManagementHost, group, policy)
	}()
	go func() {
		<-start
		_, err := writer.CreateRoute(ctx, conflicting)
		createDone <- err
	}()
	close(start)
	gateErr, createErr := <-gateDone, <-createDone
	switch {
	case gateErr == nil && createErr != nil:
		if _, err := writer.CreateRoute(ctx, conflicting); !errors.Is(err, ErrConflict) {
			t.Fatalf("subsequent create should see committed freeze: %v", err)
		}
	case gateErr != nil && createErr == nil:
		if err := s.TransitionPublicationGates(ctx, testManagementHost, group, policy); !errors.Is(err, ErrConflict) {
			t.Fatalf("subsequent gate should see committed HTTP route: %v", err)
		}
	default:
		t.Fatalf("expected exactly one winner, gate=%v create=%v", gateErr, createErr)
	}
}
