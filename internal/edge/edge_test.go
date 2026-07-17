package edge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type routeSource struct {
	enabled map[string]bool
	err     error
}

func (s routeSource) HTTPDomainEnabled(_ context.Context, host string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.enabled[host], nil
}

func TestRouterSendsManagementHostToControlAndRouteHostToGateway(t *testing.T) {
	control := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	gateway := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	handler, err := NewRouter("console.example.com", control, gateway)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		host string
		want int
	}{
		{host: "console.example.com", want: http.StatusNoContent},
		{host: "CONSOLE.EXAMPLE.COM:443", want: http.StatusNoContent},
		{host: "app.example.com", want: http.StatusAccepted},
	} {
		t.Run(test.host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "https://"+test.host+"/", nil)
			req.Host = test.host
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != test.want {
				t.Fatalf("status=%d want=%d", res.Code, test.want)
			}
		})
	}
}

func TestHTTPRedirectOnlyAllowsManagementAndEnabledRouteHosts(t *testing.T) {
	handler, err := NewHTTPRedirectHandler("console.example.com", ":443", routeSource{enabled: map[string]bool{"app.example.com": true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		host string
		want int
	}{
		{host: "console.example.com", want: http.StatusPermanentRedirect},
		{host: "app.example.com", want: http.StatusPermanentRedirect},
		{host: "unknown.example.com", want: http.StatusNotFound},
	} {
		t.Run(test.host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+test.host+"/path?q=1", nil)
			req.Host = test.host
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != test.want {
				t.Fatalf("status=%d want=%d", res.Code, test.want)
			}
			if test.want == http.StatusPermanentRedirect && res.Header().Get("Location") != "https://"+test.host+"/path?q=1" {
				t.Fatalf("location=%q", res.Header().Get("Location"))
			}
		})
	}
}

func TestHTTPRedirectUsesConfiguredHTTPSPort(t *testing.T) {
	handler, err := NewHTTPRedirectHandler("console.example.com", ":8443", routeSource{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://console.example.com:8080/deep?q=1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "https://console.example.com:8443/deep?q=1" {
		t.Fatalf("Location = %q", got)
	}
}

func TestHTTPRedirectReturnsServiceUnavailableWhenRouteLookupFails(t *testing.T) {
	handler, err := NewHTTPRedirectHandler("console.example.com", ":443", routeSource{err: errors.New("database unavailable")})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)
	req.Host = "app.example.com"
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", res.Code)
	}
}

func TestCertificateManagerRejectsMalformedPublicHost(t *testing.T) {
	for _, publicHost := range []string{"bad host.example.com", "console.example.com:443", "console..example.com", "127.0.0.1", "127.1", "2130706433", "localhost", "2001:db8::1"} {
		if _, err := NewCertificateManager(t.TempDir(), publicHost, routeSource{}); err == nil {
			t.Fatalf("malformed public host %q accepted", publicHost)
		}
	}
}

func TestCertificateManagerHostPolicyRejectsMalformedCandidate(t *testing.T) {
	manager, err := NewCertificateManager(t.TempDir(), "console.example.com", routeSource{enabled: map[string]bool{"app.example.com": true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"app.example.com:443", "app..example.com", "bad host.example.com", strings.Repeat("a", 64) + ".example.com"} {
		if err := manager.HostPolicy(context.Background(), host); err == nil {
			t.Fatalf("malformed certificate candidate %q accepted", host)
		}
	}
}

func TestCertificateManagerOnlyAuthorizesManagementAndEnabledRouteHosts(t *testing.T) {
	manager, err := NewCertificateManager(t.TempDir(), "console.example.com", routeSource{enabled: map[string]bool{"app.example.com": true}})
	if err != nil {
		t.Fatal(err)
	}
	for host, wantAllowed := range map[string]bool{
		"console.example.com": true,
		"APP.EXAMPLE.COM.":    true,
		"unknown.example.com": false,
	} {
		err := manager.HostPolicy(context.Background(), host)
		if (err == nil) != wantAllowed {
			t.Fatalf("host=%q allowed=%t err=%v", host, err == nil, err)
		}
	}
}
