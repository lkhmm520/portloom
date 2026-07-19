package store

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
)

func TestEnrollmentTokenIsOneTimeAndCreatesAuthenticatableAgent(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"), Options{PortRangeStart: 31000, PortRangeEnd: 31002})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.CreateEnrollmentToken(ctx, "enroll-secret", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	agent, apiToken, err := s.ConsumeEnrollmentToken(ctx, "enroll-secret", "nas-one")
	if err != nil {
		t.Fatal(err)
	}
	if agent.ID == "" || agent.Name != "nas-one" || apiToken == "" {
		t.Fatalf("unexpected enrollment result: agent=%+v token=%q", agent, apiToken)
	}

	authenticated, err := s.AuthenticateAgent(ctx, apiToken)
	if err != nil {
		t.Fatal(err)
	}
	if authenticated.ID != agent.ID {
		t.Fatalf("authenticated agent %q, want %q", authenticated.ID, agent.ID)
	}
	if _, _, err := s.ConsumeEnrollmentToken(ctx, "enroll-secret", "nas-two"); !errors.Is(err, ErrInvalidEnrollmentToken) {
		t.Fatalf("second consume error = %v, want ErrInvalidEnrollmentToken", err)
	}
}

func TestEnrollmentTokenExpiryUsesUnixNanoInsteadOfTextOrdering(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"), Options{PortRangeStart: 31010, PortRangeEnd: 31012})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now().UTC()

	_, err = s.db.ExecContext(ctx, `INSERT INTO enrollment_tokens
		(token_hash, expires_at, expires_at_unix_nano, created_at) VALUES (?, ?, ?, ?)`,
		hashToken("expired-by-integer"), "9999-12-31T23:59:59.999999999Z", now.Add(-time.Second).UnixNano(), formatTime(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ConsumeEnrollmentToken(ctx, "expired-by-integer", "nas-expired"); !errors.Is(err, ErrInvalidEnrollmentToken) {
		t.Fatalf("expired token error = %v, want ErrInvalidEnrollmentToken", err)
	}

	_, err = s.db.ExecContext(ctx, `INSERT INTO enrollment_tokens
		(token_hash, expires_at, expires_at_unix_nano, created_at) VALUES (?, ?, ?, ?)`,
		hashToken("valid-by-integer"), "1970-01-01T00:00:00Z", now.Add(time.Hour).UnixNano(), formatTime(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ConsumeEnrollmentToken(ctx, "valid-by-integer", "nas-valid"); err != nil {
		t.Fatalf("valid integer expiry rejected because of text value: %v", err)
	}
}

func TestOpenMigratesLegacyEnrollmentExpiryToUnixNano(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Exec(`CREATE TABLE enrollment_tokens (
		token_hash TEXT PRIMARY KEY, expires_at TEXT NOT NULL, used_at TEXT, created_at TEXT NOT NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Nanosecond)
	_, err = legacy.Exec(`INSERT INTO enrollment_tokens(token_hash, expires_at, created_at) VALUES (?, ?, ?)`,
		hashToken("legacy-token"), formatTime(expires), formatTime(time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path, Options{PortRangeStart: 31020, PortRangeEnd: 31022})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var migrated int64
	if err := s.db.QueryRowContext(ctx, `SELECT expires_at_unix_nano FROM enrollment_tokens WHERE token_hash = ?`, hashToken("legacy-token")).Scan(&migrated); err != nil {
		t.Fatal(err)
	}
	if migrated != expires.UnixNano() {
		t.Fatalf("migrated expiry = %d, want %d", migrated, expires.UnixNano())
	}
	if _, _, err := s.ConsumeEnrollmentToken(ctx, "legacy-token", "legacy-agent"); err != nil {
		t.Fatalf("consume migrated token: %v", err)
	}
}

func TestRouteLifecycleAllocatesPortsAndAdvancesRevision(t *testing.T) {
	ctx := context.Background()
	s, agent := enrolledStore(t, 31000, 31002)

	created, err := s.CreateRoute(ctx, domain.Route{
		ClientID: agent.ID, Name: "dashboard", Protocol: domain.ProtocolHTTP,
		Domain: "Nas.Example.com", LocalHost: "127.0.0.1", LocalPort: 8080,
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.RemotePort != 31000 || created.DesiredRevision != 1 || created.Domain != "nas.example.com" {
		t.Fatalf("unexpected created route: %+v", created)
	}

	second, err := s.CreateRoute(ctx, domain.Route{
		ClientID: agent.ID, Name: "ssh", Protocol: domain.ProtocolTCP, PublicPort: 45000,
		LocalHost: "localhost", LocalPort: 22, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.RemotePort != 31001 || second.DesiredRevision != 2 {
		t.Fatalf("unexpected second route: %+v", second)
	}

	created.Name = "dashboard-v2"
	created.Enabled = false
	updated, err := s.UpdateRoute(ctx, created.ID, created)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RemotePort != 31000 || updated.DesiredRevision != 3 || updated.Name != "dashboard-v2" {
		t.Fatalf("unexpected updated route: %+v", updated)
	}

	if err := s.DeleteRoute(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	state, routes, err := s.SyncAgent(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.DesiredRevision != 4 || len(routes) != 1 || routes[0].ID != created.ID {
		t.Fatalf("unexpected sync: agent=%+v routes=%+v", state, routes)
	}
}

func TestHTTPDomainEnabledUsesExactEnabledHTTPRoute(t *testing.T) {
	ctx := context.Background()
	s, agent := enrolledStore(t, 31100, 31102)
	for _, route := range []domain.Route{
		{ClientID: agent.ID, Name: "enabled", Protocol: domain.ProtocolHTTP, Domain: "app.example.com", LocalHost: "localhost", LocalPort: 8080, Enabled: true},
		{ClientID: agent.ID, Name: "disabled", Protocol: domain.ProtocolHTTP, Domain: "off.example.com", LocalHost: "localhost", LocalPort: 8081, Enabled: false},
	} {
		if _, err := s.CreateRoute(ctx, route); err != nil {
			t.Fatal(err)
		}
	}
	for domainName, want := range map[string]bool{"app.example.com": true, "off.example.com": false, "missing.example.com": false} {
		got, err := s.HTTPDomainEnabled(ctx, domainName)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("HTTPDomainEnabled(%q)=%v want %v", domainName, got, want)
		}
	}
}

func TestRoutePortAllocationSkipsPortUsedByLoopbackService(t *testing.T) {
	listener, occupiedPort := listenWithFreeSuccessor(t)
	t.Cleanup(func() { _ = listener.Close() })

	ctx := context.Background()
	s, agent := enrolledStore(t, occupiedPort, occupiedPort+1)
	route, err := s.CreateRoute(ctx, domain.Route{
		ClientID: agent.ID, Name: "app", Protocol: domain.ProtocolTCP, PublicPort: 45000,
		LocalHost: "127.0.0.1", LocalPort: 8080, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if route.RemotePort != occupiedPort+1 {
		t.Fatalf("allocated port = %d, want %d; %d is used by a loopback service",
			route.RemotePort, occupiedPort+1, occupiedPort)
	}
}

func listenWithFreeSuccessor(t *testing.T) (net.Listener, int) {
	t.Helper()
	for attempt := 0; attempt < 100; attempt++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		_, portValue, err := net.SplitHostPort(listener.Addr().String())
		if err != nil {
			_ = listener.Close()
			t.Fatal(err)
		}
		port, err := strconv.Atoi(portValue)
		if err != nil {
			_ = listener.Close()
			t.Fatal(err)
		}
		if port < 65535 {
			probe, probeErr := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port+1)))
			if probeErr == nil {
				_ = probe.Close()
				return listener, port
			}
		}
		_ = listener.Close()
	}
	t.Fatal("could not reserve a loopback port with a free successor")
	return nil, 0
}

func enrolledStore(t *testing.T, start, end int) (*Store, domain.Agent) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"), Options{PortRangeStart: start, PortRangeEnd: end})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	secret := "enroll-" + t.Name()
	if err := s.CreateEnrollmentToken(ctx, secret, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	agent, _, err := s.ConsumeEnrollmentToken(ctx, secret, "test-agent")
	if err != nil {
		t.Fatal(err)
	}
	return s, agent
}

func TestHeartbeatUpdatesAgentAndRouteObservations(t *testing.T) {
	ctx := context.Background()
	s, agent := enrolledStore(t, 32000, 32001)
	route, err := s.CreateRoute(ctx, domain.Route{
		ClientID: agent.ID, Name: "app", Protocol: domain.ProtocolHTTP,
		Domain: "app.example.com", LocalHost: "localhost", LocalPort: 8080, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Heartbeat(ctx, agent.ID, route.DesiredRevision, []domain.RouteObservation{{
		RouteID: route.ID, ObservedRevision: route.DesiredRevision,
		LocalStatus: "healthy", TunnelStatus: "connected",
	}}); err != nil {
		t.Fatal(err)
	}
	state, routes, err := s.SyncAgent(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastSeenAt.IsZero() || state.ObservedRevision != route.DesiredRevision {
		t.Fatalf("heartbeat did not update agent: %+v", state)
	}
	if routes[0].ObservedRevision != route.DesiredRevision || routes[0].TunnelStatus != "connected" {
		t.Fatalf("heartbeat did not update route: %+v", routes[0])
	}
}

func TestHeartbeatRejectsRevisionOutsideAgentDesiredRange(t *testing.T) {
	tests := []struct {
		name     string
		revision int64
	}{
		{name: "negative", revision: -1},
		{name: "ahead of desired", revision: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s, agent := enrolledStore(t, 32100, 32101)
			if _, err := s.CreateRoute(ctx, domain.Route{
				ClientID: agent.ID, Name: "app", Protocol: domain.ProtocolTCP, PublicPort: 45000,
				LocalHost: "localhost", LocalPort: 8080, Enabled: true,
			}); err != nil {
				t.Fatal(err)
			}

			err := s.Heartbeat(ctx, agent.ID, tt.revision, nil)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Heartbeat revision %d error = %v, want ErrInvalid", tt.revision, err)
			}
			state, _, err := s.SyncAgent(ctx, agent.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state.ObservedRevision != 0 || !state.LastSeenAt.IsZero() {
				t.Fatalf("rejected heartbeat changed agent state: %+v", state)
			}
		})
	}
}

func TestHeartbeatRejectsRouteObservationOutsideAgentDesiredRange(t *testing.T) {
	tests := []struct {
		name     string
		revision int64
	}{
		{name: "negative", revision: -1},
		{name: "ahead of desired", revision: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s, agent := enrolledStore(t, 32110, 32111)
			route, err := s.CreateRoute(ctx, domain.Route{
				ClientID: agent.ID, Name: "app", Protocol: domain.ProtocolTCP, PublicPort: 45000,
				LocalHost: "localhost", LocalPort: 8080, Enabled: true,
			})
			if err != nil {
				t.Fatal(err)
			}

			err = s.Heartbeat(ctx, agent.ID, 1, []domain.RouteObservation{{
				RouteID: route.ID, ObservedRevision: tt.revision,
				LocalStatus: "healthy", TunnelStatus: "up",
			}})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("route observation revision %d error = %v, want ErrInvalid", tt.revision, err)
			}
			state, routes, err := s.SyncAgent(ctx, agent.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state.ObservedRevision != 0 || !state.LastSeenAt.IsZero() || routes[0].ObservedRevision != 0 || routes[0].TunnelStatus != "" {
				t.Fatalf("rejected route observation changed state: agent=%+v route=%+v", state, routes[0])
			}
		})
	}
}

func TestHeartbeatStaleRouteObservationCannotOverwriteNewerStatus(t *testing.T) {
	ctx := context.Background()
	s, agent := enrolledStore(t, 32200, 32201)
	route, err := s.CreateRoute(ctx, domain.Route{
		ClientID: agent.ID, Name: "app", Protocol: domain.ProtocolHTTP,
		Domain: "app.example.com", LocalHost: "localhost", LocalPort: 8080, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.CreateRoute(ctx, domain.Route{
		ClientID: agent.ID, Name: "second", Protocol: domain.ProtocolTCP, PublicPort: 45000,
		LocalHost: "localhost", LocalPort: 8081, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Heartbeat(ctx, agent.ID, 2, []domain.RouteObservation{{
		RouteID: route.ID, ObservedRevision: 2,
		LocalStatus: "down", TunnelStatus: "down", LastError: "new status",
	}}); err != nil {
		t.Fatal(err)
	}

	if err := s.Heartbeat(ctx, agent.ID, 1, []domain.RouteObservation{{
		RouteID: route.ID, ObservedRevision: 1,
		LocalStatus: "healthy", TunnelStatus: "up", LastError: "stale status",
	}}); err != nil {
		t.Fatal(err)
	}
	state, routes, err := s.SyncAgent(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ObservedRevision != 2 {
		t.Fatalf("agent observed revision = %d, want 2", state.ObservedRevision)
	}
	got := routes[0]
	if got.ObservedRevision != 2 || got.LocalStatus != "down" || got.TunnelStatus != "down" || got.LastError != "new status" {
		t.Fatalf("stale heartbeat overwrote route state: %+v", got)
	}
}

func TestHeartbeatDoesNotUpdateRouteBeforeItsDesiredRevision(t *testing.T) {
	ctx := context.Background()
	s, agent := enrolledStore(t, 32300, 32301)
	route, err := s.CreateRoute(ctx, domain.Route{
		ClientID: agent.ID, Name: "app", Protocol: domain.ProtocolTCP, PublicPort: 45000,
		LocalHost: "localhost", LocalPort: 8080, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	route.Name = "app-v2"
	route, err = s.UpdateRoute(ctx, route.ID, route)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Heartbeat(ctx, agent.ID, 2, []domain.RouteObservation{{
		RouteID: route.ID, ObservedRevision: 1,
		LocalStatus: "healthy", TunnelStatus: "up",
	}}); err != nil {
		t.Fatal(err)
	}
	_, routes, err := s.SyncAgent(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := routes[0]
	if got.ObservedRevision != 0 || got.LocalStatus != "" || got.TunnelStatus != "" {
		t.Fatalf("route converged before desired revision %d was reported: %+v", got.DesiredRevision, got)
	}
}

func TestHeartbeatCannotUpdateRouteOwnedByAnotherAgent(t *testing.T) {
	ctx := context.Background()
	s, first := enrolledStore(t, 32400, 32403)
	second := enrollAdditionalAgent(t, s, "second-agent")
	if _, err := s.CreateRoute(ctx, domain.Route{
		ClientID: first.ID, Name: "first", Protocol: domain.ProtocolTCP, PublicPort: 45000,
		LocalHost: "localhost", LocalPort: 8080, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	secondRoute, err := s.CreateRoute(ctx, domain.Route{
		ClientID: second.ID, Name: "second", Protocol: domain.ProtocolTCP, PublicPort: 45001,
		LocalHost: "localhost", LocalPort: 8081, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Heartbeat(ctx, second.ID, 1, []domain.RouteObservation{{
		RouteID: secondRoute.ID, ObservedRevision: 1,
		LocalStatus: "down", TunnelStatus: "down", LastError: "owner status",
	}}); err != nil {
		t.Fatal(err)
	}

	if err := s.Heartbeat(ctx, first.ID, 1, []domain.RouteObservation{{
		RouteID: secondRoute.ID, ObservedRevision: 1,
		LocalStatus: "healthy", TunnelStatus: "up", LastError: "wrong agent status",
	}}); err != nil {
		t.Fatal(err)
	}
	_, routes, err := s.SyncAgent(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := routes[0]
	if got.LocalStatus != "down" || got.TunnelStatus != "down" || got.LastError != "owner status" {
		t.Fatalf("another agent overwrote route state: %+v", got)
	}
}

func enrollAdditionalAgent(t *testing.T, s *Store, name string) domain.Agent {
	t.Helper()
	ctx := context.Background()
	secret := "enroll-" + name + "-" + t.Name()
	if err := s.CreateEnrollmentToken(ctx, secret, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	agent, _, err := s.ConsumeEnrollmentToken(ctx, secret, name)
	if err != nil {
		t.Fatal(err)
	}
	return agent
}

func TestEnrollmentClaimIsIdempotentForSameRequestOnly(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "claims.db"), Options{PortRangeStart: 32000, PortRangeEnd: 32010})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateEnrollmentToken(ctx, "one-time", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	requestID, agentToken := strings.Repeat("a", 64), strings.Repeat("b", 64)
	first, err := s.ClaimEnrollmentToken(ctx, "one-time", "nas", requestID, agentToken)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.ClaimEnrollmentToken(ctx, "one-time", "nas", requestID, agentToken)
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID != first.ID {
		t.Fatalf("retry agent=%q want=%q", retry.ID, first.ID)
	}
	if authenticated, err := s.AuthenticateAgent(ctx, agentToken); err != nil || authenticated.ID != first.ID {
		t.Fatalf("authenticate claimed agent: %#v %v", authenticated, err)
	}
	if _, err := s.ClaimEnrollmentToken(ctx, "one-time", "nas", strings.Repeat("c", 64), agentToken); !errors.Is(err, ErrInvalidEnrollmentToken) {
		t.Fatalf("different request reused token: %v", err)
	}
	if _, err := s.ClaimEnrollmentToken(ctx, "one-time", "nas", requestID, strings.Repeat("d", 64)); !errors.Is(err, ErrInvalidEnrollmentToken) {
		t.Fatalf("same request accepted a different Agent token: %v", err)
	}
}
