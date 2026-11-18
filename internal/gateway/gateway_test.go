package gateway

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/managedssh"
)

type staticRouteSource struct {
	routes []domain.Route
}

func (s *staticRouteSource) ListRoutes(context.Context) ([]domain.Route, error) {
	return s.routes, nil
}

func newBackend(t *testing.T) (*httptest.Server, int) {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "APP.Example.com:443" {
			t.Errorf("upstream Host = %q", r.Host)
		}
		fmt.Fprint(w, "proxied "+r.URL.Path)
	}))
	t.Cleanup(backend.Close)
	_, portValue, err := net.SplitHostPort(backend.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		t.Fatal(err)
	}
	return backend, port
}

func serveGateway(t *testing.T, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://gateway/health", nil)
	request.Host = "APP.Example.com:443"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestGatewayStripsUntrustedServingEvidence(t *testing.T) {
	for _, tc := range []struct{ name, id, want string }{
		{"unconfigured", "", ""},
		{"configured", "trusted-node", "trusted-node"},
		{"invalid", "forged, trusted-node", ""},
	} {
		t.Run(tc.name, func(t *testing.T) { testGatewayServingEvidence(t, tc.id, tc.want) })
	}
}

func testGatewayServingEvidence(t *testing.T, id, want string) {
	t.Helper()
	const routeID = "actual-route"
	headers := []string{"PortLoom-Serving-Node", "PortLoom-Serving-Route"}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, header := range headers {
			if got := r.Header.Values(header); len(got) != 0 {
				t.Errorf("upstream received caller %s evidence: %v", header, got)
			}
			w.Header().Add(header, "upstream-claim")
		}
		w.WriteHeader(http.StatusEarlyHints)
		for _, header := range headers {
			w.Header().Add(header, "second-upstream-claim")
		}
		w.Header().Add("Trailer", "PortLoom-Serving-Route")
		w.Header().Add("Trailer", "X-Application-Trailer")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "application body")
		w.Header().Set(http.TrailerPrefix+headers[0], "late-upstream-claim")
		w.Header().Set(headers[1], "announced-upstream-claim")
		w.Header().Set("X-Application-Trailer", "keep me")
	}))
	t.Cleanup(backend.Close)
	port := backend.Listener.Addr().(*net.TCPAddr).Port
	source := &staticRouteSource{routes: []domain.Route{{
		ID: routeID, Protocol: domain.ProtocolHTTPS, Domain: "app.example.com", RemotePort: port,
		Enabled: true, DesiredRevision: 3, ObservedRevision: 3, TunnelStatus: "up", AgentLastSeenAt: time.Now(),
	}}}
	handler := New(source, WithServingNodeID(id))
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	req, err := http.NewRequest(http.MethodGet, server.URL+"/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "app.example.com"
	for _, header := range headers {
		req.Header.Set(header, "caller-claim")
	}
	var earlyHints atomic.Int32
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		Got1xxResponse: func(code int, info textproto.MIMEHeader) error {
			if code == http.StatusEarlyHints {
				earlyHints.Add(1)
				for _, header := range headers {
					if got := info.Values(header); len(got) != 0 {
						t.Errorf("gateway passed upstream informational %s: %v", header, got)
					}
				}
			}
			return nil
		},
	}))
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || string(body) != "application body" {
		t.Fatalf("unexpected response: status=%d body=%q err=%v", resp.StatusCode, body, err)
	}
	wantRoute := ""
	if want != "" {
		wantRoute = routeID
	}
	for i, expected := range []string{want, wantRoute} {
		header := headers[i]
		if got := resp.Header.Values(header); expected == "" && len(got) != 0 || expected != "" && (len(got) != 1 || got[0] != expected) {
			t.Errorf("gateway %s identity = %v, want %q", header, got, expected)
		}
		if got := resp.Trailer.Values(header); len(got) != 0 {
			t.Errorf("gateway passed upstream identity trailer %s: %v", header, got)
		}
	}
	if earlyHints.Load() != 1 {
		t.Errorf("early hints count = %d", earlyHints.Load())
	}

	if got := resp.Trailer.Get("X-Application-Trailer"); got != "keep me" {
		t.Errorf("unrelated trailer = %q", got)
	}
}

func TestGatewayDynamicallyProxiesEnabledConvergedRouteByHost(t *testing.T) {
	_, port := newBackend(t)
	source := &staticRouteSource{routes: []domain.Route{{
		Protocol: domain.ProtocolHTTP, Domain: "app.example.com", RemotePort: port,
		Enabled: true, DesiredRevision: 3, ObservedRevision: 3, TunnelStatus: "up", AgentLastSeenAt: time.Now(),
	}}}
	handler := New(source)

	response := serveGateway(t, handler)
	if response.Code != http.StatusOK || response.Body.String() != "proxied /health" {
		t.Fatalf("proxy response = %d %q", response.Code, response.Body.String())
	}

	source.routes[0].Enabled = false
	response = serveGateway(t, handler)
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled route status = %d", response.Code)
	}
}

func TestGatewayRejectsRouteUnlessTunnelIsUpAndRevisionConverged(t *testing.T) {
	_, port := newBackend(t)
	tests := []struct {
		name   string
		route  domain.Route
		status int
	}{
		{
			name: "tunnel not up",
			route: domain.Route{Enabled: true, DesiredRevision: 2, ObservedRevision: 2,
				TunnelStatus: "down"},
			status: http.StatusNotFound,
		},
		{
			name: "observed revision is stale",
			route: domain.Route{Enabled: true, DesiredRevision: 2, ObservedRevision: 1,
				TunnelStatus: "up", AgentLastSeenAt: time.Now()},
			status: http.StatusNotFound,
		},
		{
			name: "observed revision matches desired",
			route: domain.Route{Enabled: true, DesiredRevision: 2, ObservedRevision: 2,
				TunnelStatus: "up", AgentLastSeenAt: time.Now()},
			status: http.StatusOK,
		},
		{
			name: "observed revision is newer than desired",
			route: domain.Route{Enabled: true, DesiredRevision: 2, ObservedRevision: 3,
				TunnelStatus: "up", AgentLastSeenAt: time.Now()},
			status: http.StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.route.Protocol = domain.ProtocolHTTP
			tt.route.Domain = "app.example.com"
			tt.route.RemotePort = port
			response := serveGateway(t, New(&staticRouteSource{routes: []domain.Route{tt.route}}))
			if response.Code != tt.status {
				t.Fatalf("gateway status = %d, want %d", response.Code, tt.status)
			}
		})
	}
}

func TestGatewayRebuildsForwardedHeaders(t *testing.T) {
	headers := make(chan http.Header, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	_, portText, err := net.SplitHostPort(backend.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	route := domain.Route{Protocol: domain.ProtocolHTTP, Domain: "app.example.com", RemotePort: port,
		Enabled: true, DesiredRevision: 1, ObservedRevision: 1, TunnelStatus: "up", AgentLastSeenAt: time.Now()}
	request := httptest.NewRequest(http.MethodGet, "http://gateway/", nil)
	request.Host = "app.example.com"
	request.RemoteAddr = "203.0.113.10:54321"
	request.Header.Set("X-Forwarded-For", "198.51.100.99")
	request.Header.Set("X-Forwarded-Host", "evil.example")
	request.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()
	New(&staticRouteSource{routes: []domain.Route{route}}).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	got := <-headers
	if got.Get("X-Forwarded-For") != "203.0.113.10" || got.Get("X-Forwarded-Host") != "app.example.com" || got.Get("X-Forwarded-Proto") != "http" {
		t.Fatalf("forwarded headers=%v", got)
	}
}

func TestGatewayDoesNotExposeLoopbackDialErrors(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	route := domain.Route{Protocol: domain.ProtocolHTTP, Domain: "app.example.com", RemotePort: port,
		Enabled: true, DesiredRevision: 1, ObservedRevision: 1, TunnelStatus: "up", AgentLastSeenAt: time.Now()}
	response := serveGateway(t, New(&staticRouteSource{routes: []domain.Route{route}}))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status=%d", response.Code)
	}
	body := response.Body.String()
	if strings.Contains(body, "127.0.0.1") || strings.Contains(body, strconv.Itoa(port)) || body != "upstream unavailable\n" {
		t.Fatalf("unsafe gateway error body=%q", body)
	}
}

func TestGatewayUsesRouteAgentIsolatedBindAddress(t *testing.T) {
	const agentID = "agent-isolated"
	address, err := managedssh.BindAddress(agentID)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort(address, "0"))
	if err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	backend.Listener = listener
	backend.Start()
	defer backend.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	route := domain.Route{ClientID: agentID, Protocol: domain.ProtocolHTTP, Domain: "app.example.com", RemotePort: port,
		Enabled: true, DesiredRevision: 1, ObservedRevision: 1, TunnelStatus: "up", AgentLastSeenAt: time.Now()}
	response := serveGateway(t, New(&staticRouteSource{routes: []domain.Route{route}}, WithIsolatedAgentBindings()))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGatewayReusesUpstreamTransportConnections(t *testing.T) {
	var connections atomic.Int32
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	backend.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	backend.Start()
	defer backend.Close()
	_, portText, err := net.SplitHostPort(backend.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	handler := New(&staticRouteSource{routes: []domain.Route{{
		Protocol: domain.ProtocolHTTP, Domain: "app.example.com", RemotePort: port,
		Enabled: true, DesiredRevision: 1, ObservedRevision: 1, TunnelStatus: "up", AgentLastSeenAt: time.Now(),
	}}})
	for range 2 {
		response := serveGateway(t, handler)
		if response.Code != http.StatusNoContent {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("upstream connections=%d, want 1 reused connection", got)
	}
}

func readyWebRoute(protocol domain.Protocol, host, prefix string, publicPort, remotePort int) domain.Route {
	return domain.Route{
		ID: "r-" + host + prefix, Protocol: protocol, Domain: host, PathPrefix: prefix, PublicPort: publicPort,
		RemotePort: remotePort, Enabled: true, DesiredRevision: 1, ObservedRevision: 1,
		TunnelStatus: "up", AgentLastSeenAt: time.Now(),
	}
}

func TestGatewayMatchesSchemePathAndPort(t *testing.T) {
	prefixBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "prefix "+r.URL.Path)
	}))
	t.Cleanup(prefixBackend.Close)
	prefixPort := prefixBackend.Listener.Addr().(*net.TCPAddr).Port
	rootBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "root "+r.URL.Path)
	}))
	t.Cleanup(rootBackend.Close)
	rootPort := rootBackend.Listener.Addr().(*net.TCPAddr).Port

	source := &staticRouteSource{routes: []domain.Route{
		readyWebRoute(domain.ProtocolHTTPS, "app.example.com", "", 0, rootPort),
		readyWebRoute(domain.ProtocolHTTPS, "app.example.com", "/media", 0, prefixPort),
	}}
	handler := New(source)

	serve := func(edge Edge, path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "https://app.example.com"+path, nil)
		request = request.WithContext(WithEdge(request.Context(), edge))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	httpsEdge := Edge{Scheme: "https", Port: 443, Default: true}
	if got := serve(httpsEdge, "/media/tv").Body.String(); got != "prefix /media/tv" {
		t.Fatalf("longest prefix match failed: %q", got)
	}
	if got := serve(httpsEdge, "/other").Body.String(); got != "root /other" {
		t.Fatalf("root fallback failed: %q", got)
	}
	// The HTTPS-only domain must not be served on the plain-HTTP edge.
	if code := serve(Edge{Scheme: "http", Port: 80, Default: true}, "/other").Code; code != http.StatusNotFound {
		t.Fatalf("plain edge served an HTTPS route: %d", code)
	}
	// A non-default edge port must not match default-port routes.
	if code := serve(Edge{Scheme: "https", Port: 8443}, "/other").Code; code != http.StatusNotFound {
		t.Fatalf("extra port matched default-port route: %d", code)
	}
}

func TestGatewayStripsPathPrefixWhenRequested(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "stripped "+r.URL.Path)
	}))
	t.Cleanup(backend.Close)
	port := backend.Listener.Addr().(*net.TCPAddr).Port
	route := readyWebRoute(domain.ProtocolHTTPS, "app.example.com", "/media", 0, port)
	route.StripPath = true
	handler := New(&staticRouteSource{routes: []domain.Route{route}})

	request := httptest.NewRequest(http.MethodGet, "https://app.example.com/media/tv/1", nil)
	request = request.WithContext(WithEdge(request.Context(), Edge{Scheme: "https", Port: 443, Default: true}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Body.String(); got != "stripped /tv/1" {
		t.Fatalf("strip path result=%q", got)
	}
	request = httptest.NewRequest(http.MethodGet, "https://app.example.com/media", nil)
	request = request.WithContext(WithEdge(request.Context(), Edge{Scheme: "https", Port: 443, Default: true}))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Body.String(); got != "stripped /" {
		t.Fatalf("bare prefix strip result=%q", got)
	}
}

func TestGatewayRedirectsPlainHTTPOnlyWhenHTTPSRouteExists(t *testing.T) {
	route := readyWebRoute(domain.ProtocolHTTPS, "secure.example.com", "", 0, 1)
	handler := New(&staticRouteSource{routes: []domain.Route{route}}, WithHTTPSRedirect(443))
	request := httptest.NewRequest(http.MethodGet, "http://secure.example.com/x?a=1", nil)
	request = request.WithContext(WithEdge(request.Context(), Edge{Scheme: "http", Port: 80, Default: true}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusPermanentRedirect {
		t.Fatalf("status=%d", response.Code)
	}
	if got := response.Header().Get("Location"); got != "https://secure.example.com/x?a=1" {
		t.Fatalf("location=%q", got)
	}
	// Legacy listeners (no edge context) never redirect.
	request = httptest.NewRequest(http.MethodGet, "http://secure.example.com/x", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code == http.StatusPermanentRedirect {
		t.Fatal("legacy listener unexpectedly redirected")
	}
}

type recordingObserver struct {
	requests atomic.Int64
	in       atomic.Int64
	out      atomic.Int64
}

func (o *recordingObserver) ObserveHTTP(_ string, requestBytes, responseBytes int64) {
	o.requests.Add(1)
	o.in.Add(requestBytes)
	o.out.Add(responseBytes)
}

func TestGatewayCountsTraffic(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("y", 32))
	}))
	t.Cleanup(backend.Close)
	port := backend.Listener.Addr().(*net.TCPAddr).Port
	observer := &recordingObserver{}
	handler := New(&staticRouteSource{routes: []domain.Route{
		readyWebRoute(domain.ProtocolHTTPS, "app.example.com", "", 0, port),
	}}, WithTrafficObserver(observer))
	request := httptest.NewRequest(http.MethodPost, "https://app.example.com/upload", strings.NewReader(strings.Repeat("x", 64)))
	request = request.WithContext(WithEdge(request.Context(), Edge{Scheme: "https", Port: 443, Default: true}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	if observer.requests.Load() != 1 || observer.in.Load() != 64 || observer.out.Load() != 32 {
		t.Fatalf("observed requests=%d in=%d out=%d", observer.requests.Load(), observer.in.Load(), observer.out.Load())
	}
}

func TestGatewayServesDefaultPortRoutesOnCustomPrimaryEdgePorts(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "custom-edge "+r.URL.Path)
	}))
	t.Cleanup(backend.Close)
	port := backend.Listener.Addr().(*net.TCPAddr).Port
	handler := New(&staticRouteSource{routes: []domain.Route{
		readyWebRoute(domain.ProtocolHTTPS, "app.example.com", "", 0, port),
	}})
	// The primary HTTPS edge may listen on any port (e.g. --https-port 8443);
	// default-port routes must still be served there.
	request := httptest.NewRequest(http.MethodGet, "https://app.example.com/x", nil)
	request = request.WithContext(WithEdge(request.Context(), Edge{Scheme: "https", Port: 8443, Default: true}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Body.String(); got != "custom-edge /x" {
		t.Fatalf("custom primary port result=%q", got)
	}
	// An extra-port listener on the same number must not serve it.
	request = httptest.NewRequest(http.MethodGet, "https://app.example.com/x", nil)
	request = request.WithContext(WithEdge(request.Context(), Edge{Scheme: "https", Port: 8443}))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("extra-port listener served a default-port route: %d", response.Code)
	}
}
