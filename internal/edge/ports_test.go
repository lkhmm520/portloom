package edge

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/gateway"
)

func reservePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func TestPortsManagerPublishesPlainHTTPRouteOnExtraPort(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "extra-port "+r.URL.Path)
	}))
	t.Cleanup(backend.Close)
	backendPort := backend.Listener.Addr().(*net.TCPAddr).Port

	publicPort := reservePort(t)
	route := readyRoute(domain.ProtocolHTTP, "extra.example.com", "")
	route.PublicPort = publicPort
	route.RemotePort = backendPort
	source := fakeRouteList{routes: []domain.Route{route}}
	manager, err := NewPortsManager(source, gateway.New(source), nil, WithPortsBindHost("127.0.0.1"), WithPortsPollInterval(10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()

	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		request, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(publicPort)+"/x", nil)
		request.Host = "extra.example.com"
		response, err := client.Do(request)
		if err == nil {
			body := make([]byte, 64)
			n, _ := response.Body.Read(body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK && string(body[:n]) == "extra-port /x" {
				if got := manager.PortStatus(publicPort); got != PortStatusPublished {
					t.Fatalf("port status=%q", got)
				}
				cancel()
				<-done
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("extra-port route was never served")
}

func TestPortsManagerReportsMixedSchemeConflict(t *testing.T) {
	publicPort := reservePort(t)
	httpRoute := readyRoute(domain.ProtocolHTTP, "a.example.com", "")
	httpRoute.PublicPort = publicPort
	httpsRoute := readyRoute(domain.ProtocolHTTPS, "b.example.com", "")
	httpsRoute.PublicPort = publicPort
	source := fakeRouteList{routes: []domain.Route{httpRoute, httpsRoute}}
	manager, err := NewPortsManager(source, gateway.New(source), nil, WithPortsBindHost("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	manager.reconcile(context.Background())
	if got := manager.PortStatus(publicPort); got != PortStatusConflict {
		t.Fatalf("port status=%q want conflict", got)
	}
	manager.stopAll()
}
