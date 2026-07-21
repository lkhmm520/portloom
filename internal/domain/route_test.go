package domain

import (
	"testing"
	"time"
)

func validRoute() Route {
	return Route{Name: "MoviePilot", Protocol: ProtocolHTTP, Domain: "MP.look4i.COM.", LocalHost: "127.0.0.1", LocalPort: 3333, TunnelGroup: "web", Enabled: true}
}

func TestRouteValidateNormalizesHTTPDomain(t *testing.T) {
	r := validRoute()
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if r.Domain != "mp.look4i.com" {
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

func TestRouteValidateAllowsExtraPublicPortForWebRoutes(t *testing.T) {
	r := validRoute()
	r.PublicPort = 24443
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if r.PublicPort != 24443 {
		t.Fatalf("public port=%d", r.PublicPort)
	}
}

func TestRouteValidateNormalizesDefaultWebPortToZero(t *testing.T) {
	r := validRoute()
	r.PublicPort = 80
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if r.PublicPort != 0 {
		t.Fatalf("default HTTP port was not normalized, got %d", r.PublicPort)
	}
	r = validRoute()
	r.Protocol = ProtocolHTTPS
	r.PublicPort = 443
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if r.PublicPort != 0 {
		t.Fatalf("default HTTPS port was not normalized, got %d", r.PublicPort)
	}
}

func TestRouteValidatePathPrefix(t *testing.T) {
	valid := map[string]string{
		"":          "",
		"/":         "",
		"/jellyfin": "/jellyfin",
		"/media/tv": "/media/tv",
		"/app/":     "/app",
	}
	for input, want := range valid {
		r := validRoute()
		r.PathPrefix = input
		if err := r.Validate(); err != nil {
			t.Fatalf("path prefix %q rejected: %v", input, err)
		}
		if r.PathPrefix != want {
			t.Fatalf("path prefix %q normalized to %q, want %q", input, r.PathPrefix, want)
		}
	}
	for _, input := range []string{"jellyfin", "//x", "/a//b", "/../etc", "/a/..", "/a b"} {
		r := validRoute()
		r.PathPrefix = input
		if err := r.Validate(); err == nil {
			t.Fatalf("path prefix %q accepted", input)
		}
	}
}

func TestRouteValidateStripPathRequiresPrefix(t *testing.T) {
	r := validRoute()
	r.StripPath = true
	if err := r.Validate(); err == nil {
		t.Fatal("expected error")
	}
	r.PathPrefix = "/app"
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestRouteValidateUDPRequiresPublicPort(t *testing.T) {
	r := validRoute()
	r.Protocol = ProtocolUDP
	r.Domain = ""
	r.TunnelGroup = ""
	if err := r.Validate(); err == nil {
		t.Fatal("expected error")
	}
	r.PublicPort = 24553
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if r.TunnelGroup != "udp" {
		t.Fatalf("tunnel group=%q", r.TunnelGroup)
	}
}

func TestRouteMatchesPath(t *testing.T) {
	route := Route{PathPrefix: "/app"}
	for path, want := range map[string]bool{
		"/app": true, "/app/x": true, "/application": false, "/": false,
	} {
		if got := route.MatchesPath(path); got != want {
			t.Fatalf("MatchesPath(%q)=%v want %v", path, got, want)
		}
	}
	root := Route{}
	if !root.MatchesPath("/anything") {
		t.Fatal("empty prefix must match all paths")
	}
}

func TestEffectivePublicPort(t *testing.T) {
	if got := (Route{Protocol: ProtocolHTTP}).EffectivePublicPort(); got != 80 {
		t.Fatalf("http default=%d", got)
	}
	if got := (Route{Protocol: ProtocolHTTPS}).EffectivePublicPort(); got != 443 {
		t.Fatalf("https default=%d", got)
	}
	if got := (Route{Protocol: ProtocolHTTPS, PublicPort: 8443}).EffectivePublicPort(); got != 8443 {
		t.Fatalf("https extra=%d", got)
	}
	if got := (Route{Protocol: ProtocolTCP, PublicPort: 25000}).EffectivePublicPort(); got != 25000 {
		t.Fatalf("tcp=%d", got)
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
	if got := NormalizeHost("MP.look4i.COM:443"); got != "mp.look4i.com" {
		t.Fatalf("got %q", got)
	}
}
