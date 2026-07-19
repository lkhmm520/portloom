package tcpedge

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/udpframe"
)

type mutableRouteSource struct {
	mu     sync.RWMutex
	routes []domain.Route
}

func (s *mutableRouteSource) ListRoutes(context.Context) ([]domain.Route, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]domain.Route(nil), s.routes...), nil
}

func (s *mutableRouteSource) GetRoute(_ context.Context, id string) (domain.Route, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, route := range s.routes {
		if route.ID == id {
			return route, nil
		}
	}
	return domain.Route{}, errors.New("route not found")
}

func (s *mutableRouteSource) set(routes ...domain.Route) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes = append([]domain.Route(nil), routes...)
}

func TestManagerExposesOnlyConvergedTCPRoutes(t *testing.T) {
	backend := listenTCP4(t)
	defer backend.Close()
	go serveEcho(backend)

	publicPort := reserveTCP4Port(t)
	route := domain.Route{
		ID: "route-1", ClientID: "agent-1", Name: "service", Protocol: domain.ProtocolTCP,
		LocalHost: "127.0.0.1", LocalPort: backend.Addr().(*net.TCPAddr).Port,
		RemotePort: backend.Addr().(*net.TCPAddr).Port, PublicPort: publicPort,
		Enabled: true, DesiredRevision: 1,
	}
	source := &mutableRouteSource{}
	source.set(route)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := New(source, WithBindHost("127.0.0.1"), WithPollInterval(10*time.Millisecond))
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()

	assertPortClosed(t, publicPort)
	if got := manager.PublicStatus(route); got != StatusWaitingAgent {
		t.Fatalf("initial public status=%q want %q", got, StatusWaitingAgent)
	}

	route.ObservedRevision = route.DesiredRevision
	route.TunnelStatus = "up"
	route.AgentLastSeenAt = time.Now()
	source.set(route)
	conn := waitForTCP(t, publicPort, true)
	if got := manager.PublicStatus(route); got != StatusPublished {
		t.Fatalf("ready public status=%q want %q", got, StatusPublished)
	}
	if _, err := conn.Write([]byte("portloom")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("portloom"))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "portloom" {
		t.Fatalf("echo=%q want portloom", got)
	}
	_ = conn.Close()

	route.Enabled = false
	source.set(route)
	waitForTCP(t, publicPort, false)

	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manager did not stop")
	}
}

func TestManagerRechecksReadinessForEveryAcceptedConnection(t *testing.T) {
	backend := listenTCP4(t)
	defer backend.Close()
	accepted := make(chan struct{}, 2)
	go func() {
		for {
			connection, err := backend.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			_ = connection.Close()
		}
	}()

	publicPort := reserveTCP4Port(t)
	route := domain.Route{
		ID: "route-recheck", ClientID: "agent-1", Name: "service", Protocol: domain.ProtocolTCP,
		RemotePort: backend.Addr().(*net.TCPAddr).Port, PublicPort: publicPort,
		Enabled: true, DesiredRevision: 1, ObservedRevision: 1, TunnelStatus: "up", AgentLastSeenAt: time.Now(),
	}
	source := &mutableRouteSource{}
	source.set(route)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := New(source, WithBindHost("127.0.0.1"), WithPollInterval(time.Hour))
	go func() { _ = manager.Run(ctx) }()

	baseline := waitForTCP(t, publicPort, true)
	_ = baseline.Close()
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("baseline connection did not reach backend")
	}

	route.Enabled = false
	source.set(route)
	connection, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", netPort(publicPort)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = connection.Write([]byte("must-not-proxy"))
	_ = connection.Close()
	select {
	case <-accepted:
		t.Fatal("unready route accepted a new backend connection before reconciliation")
	case <-time.After(250 * time.Millisecond):
	}
}

func TestManagerFailsClosedOnDuplicatePublicPorts(t *testing.T) {
	publicPort := reserveTCP4Port(t)
	ready := func(id string, remotePort int) domain.Route {
		return domain.Route{
			ID: id, ClientID: "agent-1", Name: id, Protocol: domain.ProtocolTCP,
			RemotePort: remotePort, PublicPort: publicPort, Enabled: true,
			DesiredRevision: 1, ObservedRevision: 1, TunnelStatus: "up", AgentLastSeenAt: time.Now(),
		}
	}
	source := &mutableRouteSource{}
	source.set(ready("duplicate-a", reserveTCP4Port(t)), ready("duplicate-b", reserveTCP4Port(t)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := New(source, WithBindHost("127.0.0.1"), WithPollInterval(10*time.Millisecond))
	go func() { _ = manager.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)
	assertPortClosed(t, publicPort)
	for _, route := range source.routes {
		if got := manager.PublicStatus(route); got != StatusConflict {
			t.Fatalf("duplicate route %s status=%q want %q", route.ID, got, StatusConflict)
		}
	}
}

func TestManagerReportsBindFailure(t *testing.T) {
	occupied := listenTCP4(t)
	defer occupied.Close()
	publicPort := occupied.Addr().(*net.TCPAddr).Port
	route := domain.Route{
		ID: "occupied", ClientID: "agent-1", Name: "occupied", Protocol: domain.ProtocolTCP,
		RemotePort: reserveTCP4Port(t), PublicPort: publicPort, Enabled: true,
		DesiredRevision: 1, ObservedRevision: 1, TunnelStatus: "up", AgentLastSeenAt: time.Now(),
	}
	source := &mutableRouteSource{}
	source.set(route)
	manager := New(source, WithBindHost("127.0.0.1"))
	if err := manager.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := manager.PublicStatus(route); got != StatusBindError {
		t.Fatalf("bind failure status=%q want %q", got, StatusBindError)
	}
}

func TestManagerRestartsUnexpectedlyClosedListener(t *testing.T) {
	publicPort := reserveTCP4Port(t)
	route := domain.Route{
		ID: "restart-listener", ClientID: "agent-1", Name: "restart-listener", Protocol: domain.ProtocolTCP,
		RemotePort: reserveTCP4Port(t), PublicPort: publicPort, Enabled: true,
		DesiredRevision: 1, ObservedRevision: 1, TunnelStatus: "up", AgentLastSeenAt: time.Now(),
	}
	source := &mutableRouteSource{}
	source.set(route)
	manager := New(source, WithBindHost("127.0.0.1"))
	ctx := context.Background()
	if err := manager.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	key := portKey{protocol: domain.ProtocolTCP, port: publicPort}
	original, _ := manager.workers[key].(*worker)
	if original == nil {
		t.Fatal("listener was not created")
	}
	_ = original.listener.Close()
	original.wait.Wait()
	if err := manager.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if manager.workers[key] == streamWorker(original) {
		t.Fatal("reconcile retained a worker whose accept loop had exited")
	}
	manager.stopAll()
}

func TestManagerPublishesUDPRouteThroughFramedBackend(t *testing.T) {
	// Fake agent-side relay: framed TCP listener answering each datagram with
	// its uppercase echo, exercising the udpframe encapsulation end to end.
	backend := listenTCP4(t)
	defer backend.Close()
	go func() {
		for {
			conn, err := backend.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				buf := make([]byte, udpframe.MaxPayload)
				for {
					n, err := udpframe.Read(conn, buf)
					if err != nil {
						return
					}
					reply := bytes.ToUpper(buf[:n])
					if err := udpframe.Write(conn, reply); err != nil {
						return
					}
				}
			}()
		}
	}()

	publicPort := reserveUDP4Port(t)
	route := domain.Route{
		ID: "udp-route", ClientID: "agent-1", Name: "udp", Protocol: domain.ProtocolUDP,
		RemotePort: backend.Addr().(*net.TCPAddr).Port, PublicPort: publicPort,
		Enabled: true, DesiredRevision: 1, ObservedRevision: 1, TunnelStatus: "up", AgentLastSeenAt: time.Now(),
	}
	source := &mutableRouteSource{}
	source.set(route)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := New(source, WithBindHost("127.0.0.1"), WithPollInterval(10*time.Millisecond))
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	var conn net.Conn
	var err error
	for time.Now().Before(deadline) {
		if manager.PublicStatus(route) == StatusPublished {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := manager.PublicStatus(route); got != StatusPublished {
		t.Fatalf("udp status=%q want %q", got, StatusPublished)
	}
	conn, err = net.Dial("udp4", net.JoinHostPort("127.0.0.1", netPort(publicPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	reply := make([]byte, 64)
	for attempt := 0; attempt < 20; attempt++ {
		if _, err := conn.Write([]byte("portloom")); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, err := conn.Read(reply)
		if err == nil {
			if string(reply[:n]) != "PORTLOOM" {
				t.Fatalf("udp echo=%q want PORTLOOM", reply[:n])
			}
			cancel()
			select {
			case runErr := <-done:
				if runErr != nil && !errors.Is(runErr, context.Canceled) {
					t.Fatalf("Run: %v", runErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("manager did not stop")
			}
			return
		}
	}
	t.Fatal("no UDP echo received")
}

func reserveUDP4Port(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	_ = conn.Close()
	return port
}

func listenTCP4(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return listener
}

func reserveTCP4Port(t *testing.T) int {
	t.Helper()
	listener := listenTCP4(t)
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func serveEcho(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}()
	}
}

func assertPortClosed(t *testing.T, port int) {
	t.Helper()
	conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", netPort(port)), 50*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("port %d unexpectedly open before convergence", port)
	}
}

func waitForTCP(t *testing.T, port int, wantOpen bool) net.Conn {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	address := net.JoinHostPort("127.0.0.1", netPort(port))
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp4", address, 50*time.Millisecond)
		if wantOpen && err == nil {
			return conn
		}
		if !wantOpen && err != nil {
			return nil
		}
		if conn != nil {
			_ = conn.Close()
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("port %d open=%v did not become %v", port, !wantOpen, wantOpen)
	return nil
}

func netPort(port int) string {
	const digits = "0123456789"
	if port == 0 {
		return "0"
	}
	var buf [5]byte
	i := len(buf)
	for port > 0 {
		i--
		buf[i] = digits[port%10]
		port /= 10
	}
	return string(buf[i:])
}
