package domain

import "testing"

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
	for _, port := range []int{-1, 65536} {
		r := validRoute()
		r.Protocol = ProtocolTCP
		r.Domain = ""
		r.PublicPort = port
		if err := r.Validate(); err == nil {
			t.Fatalf("TCP public port %d accepted", port)
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
