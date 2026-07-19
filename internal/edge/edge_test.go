package edge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/gateway"
)

type routeSource struct {
	enabled map[string]bool
	err     error
}

func (s routeSource) HTTPSDomainEnabled(_ context.Context, host string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.enabled[host], nil
}

type fakeRouteList struct {
	routes []domain.Route
	err    error
}

func (s fakeRouteList) ListRoutes(context.Context) ([]domain.Route, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.routes, nil
}

func readyRoute(protocol domain.Protocol, host, prefix string) domain.Route {
	return domain.Route{
		ID: "route-" + host + prefix, ClientID: "client-1", Name: host,
		Protocol: protocol, Domain: host, PathPrefix: prefix,
		LocalHost: "127.0.0.1", LocalPort: 65534, RemotePort: 1,
		Enabled: true, TunnelStatus: "up", DesiredRevision: 1, ObservedRevision: 1,
		AgentLastSeenAt: time.Now().UTC(),
	}
}

func emptyGateway() *gateway.Handler { return gateway.New(fakeRouteList{}) }

func TestRouterSendsManagementHostToControlAndRouteHostToGateway(t *testing.T) {
	control := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler, err := NewRouter("console.example.com", control, emptyGateway(), ":443")
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		host string
		want int
	}{
		{host: "console.example.com", want: http.StatusNoContent},
		{host: "CONSOLE.EXAMPLE.COM:443", want: http.StatusNoContent},
		// Unknown route hosts land in the gateway, which returns 404.
		{host: "app.example.com", want: http.StatusNotFound},
	} {
		t.Run(test.host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "https://"+test.host+"/", nil)
			req.Host = test.host
			res := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			handler.ServeHTTP(res, req)
			if res.Code != test.want {
				t.Fatalf("status=%d want=%d", res.Code, test.want)
			}
		})
	}
}

func TestRouterPathPrefixRouteMayShareManagementDomain(t *testing.T) {
	control := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	route := readyRoute(domain.ProtocolHTTPS, "console.example.com", "/app")
	handler, err := NewRouter("console.example.com", control, gateway.New(fakeRouteList{routes: []domain.Route{route}}), ":443")
	if err != nil {
		t.Fatal(err)
	}
	// The path-prefix route matches and the gateway attempts to proxy; with no
	// live tunnel the upstream dial fails with 502, proving dispatch happened.
	req := httptest.NewRequest(http.MethodGet, "https://console.example.com/app/library", nil)
	res := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("path-prefix status=%d want=502", res.Code)
	}
	// Everything else on the management domain still reaches control.
	req = httptest.NewRequest(http.MethodGet, "https://console.example.com/api/v1/system", nil)
	res = &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("management status=%d want=204", res.Code)
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	readDeadline  time.Time
	writeDeadline time.Time
}

func (r *deadlineRecorder) SetReadDeadline(deadline time.Time) error {
	r.readDeadline = deadline
	return nil
}

func (r *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	r.writeDeadline = deadline
	return nil
}

func TestRouterBoundsPublicManagementRequests(t *testing.T) {
	recorder := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	control := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if recorder.readDeadline.IsZero() || recorder.writeDeadline.IsZero() {
			t.Fatal("management deadlines were not installed before dispatch")
		}
		if remaining := time.Until(recorder.readDeadline); remaining <= 0 || remaining > managementRequestTimeout {
			t.Fatalf("read deadline remaining = %v", remaining)
		}
		_, err := io.Copy(io.Discard, r.Body)
		var maxErr *http.MaxBytesError
		if !errors.As(err, &maxErr) || maxErr.Limit != managementMaxBodyBytes {
			t.Fatalf("oversized body error = %v", err)
		}
		http.Error(w, "too large", http.StatusRequestEntityTooLarge)
	})
	router, err := NewRouter("console.example.com", control, emptyGateway(), ":443")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://console.example.com/api/v1/routes", strings.NewReader(strings.Repeat("x", managementMaxBodyBytes+1)))
	req.ContentLength = -1
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestRouterRejectsDeclaredOversizedManagementBodyBeforeControl(t *testing.T) {
	called := false
	router, err := NewRouter("console.example.com", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }), emptyGateway(), ":443")
	if err != nil {
		t.Fatal(err)
	}
	recorder := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodPost, "https://console.example.com/api/v1/routes", strings.NewReader("small fixture"))
	req.ContentLength = managementMaxBodyBytes + 1
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusRequestEntityTooLarge || called {
		t.Fatalf("status=%d control_called=%t", recorder.Code, called)
	}
}

func TestRouterManagementDeadlinesWorkOnHTTP1AndHTTP2(t *testing.T) {
	router, err := NewRouter("console.example.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), emptyGateway(), ":443")
	if err != nil {
		t.Fatal(err)
	}
	for _, enableHTTP2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "http1", true: "http2"}[enableHTTP2], func(t *testing.T) {
			server := httptest.NewUnstartedServer(router)
			server.EnableHTTP2 = enableHTTP2
			server.StartTLS()
			defer server.Close()
			req, err := http.NewRequest(http.MethodGet, server.URL+"/healthz", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = "console.example.com"
			resp, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status=%d protocol=%s", resp.StatusCode, resp.Proto)
			}
			if enableHTTP2 && resp.ProtoMajor != 2 {
				t.Fatalf("expected HTTP/2, got %s", resp.Proto)
			}
		})
	}
}

func TestRouterFailsClosedWithoutManagementDeadlineSupport(t *testing.T) {
	called := false
	router, err := NewRouter("console.example.com", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }), emptyGateway(), ":443")
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://console.example.com/", nil)
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusServiceUnavailable || called {
		t.Fatalf("status=%d control_called=%t", recorder.Code, called)
	}
}

func redirectingGateway(routes ...domain.Route) *gateway.Handler {
	return gateway.New(fakeRouteList{routes: routes}, gateway.WithHTTPSRedirect(443))
}

func TestHTTPHandlerRedirectsManagementAndHTTPSOnlyHosts(t *testing.T) {
	httpsRoute := readyRoute(domain.ProtocolHTTPS, "app.example.com", "")
	handler, err := NewHTTPHandler("console.example.com", ":80", ":443", redirectingGateway(httpsRoute))
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

func TestHTTPHandlerServesPlainHTTPRouteWithoutRedirect(t *testing.T) {
	plain := readyRoute(domain.ProtocolHTTP, "plain.example.com", "")
	handler, err := NewHTTPHandler("console.example.com", ":80", ":443", redirectingGateway(plain))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://plain.example.com/", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	// The gateway attempts to proxy (no live tunnel in tests → 502): plain
	// HTTP is served directly rather than redirected.
	if res.Code != http.StatusBadGateway {
		t.Fatalf("status=%d want=502", res.Code)
	}
}

func TestHTTPHandlerUsesConfiguredHTTPSPortForManagementRedirect(t *testing.T) {
	handler, err := NewHTTPHandler("console.example.com", ":8080", ":8443", emptyGateway())
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

func TestHTTPHandlerReturnsServiceUnavailableWhenRouteLookupFails(t *testing.T) {
	failing := gateway.New(fakeRouteList{err: errors.New("database unavailable")}, gateway.WithHTTPSRedirect(443))
	handler, err := NewHTTPHandler("console.example.com", ":80", ":443", failing)
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

func TestCertificateManagerOnlyAuthorizesManagementAndEnabledHTTPSHosts(t *testing.T) {
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
