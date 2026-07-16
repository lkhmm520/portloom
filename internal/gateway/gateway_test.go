package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lkhmm520/portloom/internal/domain"
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

func TestGatewayDynamicallyProxiesEnabledConvergedRouteByHost(t *testing.T) {
	_, port := newBackend(t)
	source := &staticRouteSource{routes: []domain.Route{{
		Protocol: domain.ProtocolHTTP, Domain: "app.example.com", RemotePort: port,
		Enabled: true, DesiredRevision: 3, ObservedRevision: 3, TunnelStatus: "up",
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
				TunnelStatus: "up"},
			status: http.StatusNotFound,
		},
		{
			name: "observed revision matches desired",
			route: domain.Route{Enabled: true, DesiredRevision: 2, ObservedRevision: 2,
				TunnelStatus: "up"},
			status: http.StatusOK,
		},
		{
			name: "observed revision is newer than desired",
			route: domain.Route{Enabled: true, DesiredRevision: 2, ObservedRevision: 3,
				TunnelStatus: "up"},
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
		Enabled: true, DesiredRevision: 1, ObservedRevision: 1, TunnelStatus: "up"}
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
		Enabled: true, DesiredRevision: 1, ObservedRevision: 1, TunnelStatus: "up"}
	response := serveGateway(t, New(&staticRouteSource{routes: []domain.Route{route}}))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status=%d", response.Code)
	}
	body := response.Body.String()
	if strings.Contains(body, "127.0.0.1") || strings.Contains(body, strconv.Itoa(port)) || body != "upstream unavailable\n" {
		t.Fatalf("unsafe gateway error body=%q", body)
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
		Enabled: true, DesiredRevision: 1, ObservedRevision: 1, TunnelStatus: "up",
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
