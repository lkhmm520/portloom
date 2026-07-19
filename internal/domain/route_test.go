package domain

import (
	"testing"
	"time"
)

func validRoute() Route {
	return Route{Name: "MoviePilot", Protocol: ProtocolHTTP, Domain: "MP.961121.XYZ.", LocalHost: "127.0.0.1", LocalPort: 3333, TunnelGroup: "web", Enabled: true}
}

func TestRouteValidateNormalizesHTTPDomain(t *testing.T) {
	r := validRoute()
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if r.Domain != "mp.961121.xyz" {
		t.Fatalf("domain=%q", r.Domain)
	}
}

func TestRouteValidateRejectsMissingHTTPDomain(t *testing.T) {
	r := validRoute()
	r.Domain = ""
	if err := r.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestRouteValidateRejectsNonPublicHTTPDomain(t *testing.T) {
	for _, candidate := range []string{
		"console.example.com:443",
		"127.0.0.1",
		"127.1",
		"2130706433",
		"localhost",
		"service.123",
	} {
		t.Run(candidate, func(t *testing.T) {
			r := validRoute()
			r.Domain = candidate
			if err := r.Validate(); err == nil {
				t.Fatalf("HTTP route domain %q was accepted", candidate)
			}
		})
	}
}

func TestRouteValidateRejectsUnsafeTarget(t *testing.T) {
	r := validRoute()
	r.LocalHost = "127.0.0.1;rm -rf /"
	if err := r.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestRouteValidateTCPDoesNotRequireDomain(t *testing.T) {
	r := validRoute()
	r.Protocol = ProtocolTCP
	r.Domain = ""
	r.PublicPort = 24443
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestRouteValidateRejectsDomainForTCP(t *testing.T) {
	r := validRoute()
	r.Protocol = ProtocolTCP
	r.PublicPort = 24443
	if err := r.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestRouteValidateRejectsInvalidTCPPublicPort(t *testing.T) {
	for _, port := range []int{0, -1, 65536} {
		r := validRoute()
		r.Protocol = ProtocolTCP
		r.Domain = ""
		r.PublicPort = port
		if err := r.Validate(); err == nil {
			t.Fatalf("TCP public port %d accepted", port)
		}
	}
}

func TestPublicationReadyRequiresFreshAgentHeartbeat(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	route := Route{
		Enabled: true, DesiredRevision: 3, ObservedRevision: 3, TunnelStatus: "up",
		AgentLastSeenAt: now.Add(-30 * time.Second),
	}
	if !route.PublicationReadyAt(now) {
		t.Fatal("fresh converged route was not ready")
	}
	route.AgentLastSeenAt = now.Add(-2 * time.Minute)
	if route.PublicationReadyAt(now) {
		t.Fatal("stale Agent heartbeat left route published")
	}
	route.AgentLastSeenAt = time.Time{}
	if route.PublicationReadyAt(now) {
		t.Fatal("missing Agent heartbeat left route published")
	}
	route.AgentLastSeenAt = now
	for _, status := range []string{"UP", " up ", "connected"} {
		route.TunnelStatus = status
		if route.PublicationReadyAt(now) {
			t.Fatalf("non-canonical tunnel status %q was published", status)
		}
	}
}

func TestRouteValidateRejectsPublicPortForHTTP(t *testing.T) {
	r := validRoute()
	r.PublicPort = 24443
	if err := r.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestRouteValidateRejectsInvalidPorts(t *testing.T) {
	for _, p := range []int{0, 65536} {
		r := validRoute()
		r.LocalPort = p
		if err := r.Validate(); err == nil {
			t.Fatalf("port %d accepted", p)
		}
	}
}

func TestNormalizeHostDropsPortAndCase(t *testing.T) {
	if got := NormalizeHost("MP.961121.XYZ:443"); got != "mp.961121.xyz" {
		t.Fatalf("got %q", got)
	}
}
