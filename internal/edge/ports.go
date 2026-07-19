package edge

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/gateway"
)

// PortRouteSource lists routes for the extra-port reconciler.
type PortRouteSource interface {
	ListRoutes(context.Context) ([]domain.Route, error)
}

const (
	PortStatusPublished = "published"
	PortStatusBindError = "bind_error"
	PortStatusConflict  = "conflict"
	PortStatusPending   = "pending"
)

// PortsManager publishes web routes with a non-default public port by opening
// one extra HTTP or HTTPS listener per port and dispatching into the gateway.
type PortsManager struct {
	routes       PortRouteSource
	gateway      *gateway.Handler
	tlsConfig    *tls.Config
	bindHost     string
	pollInterval time.Duration

	servers map[int]*portServer

	statusMu sync.RWMutex
	statuses map[int]string
}

type portServer struct {
	scheme   string
	server   *http.Server
	listener net.Listener
	done     chan struct{}
}

type PortsOption func(*PortsManager)

func WithPortsBindHost(host string) PortsOption {
	return func(manager *PortsManager) { manager.bindHost = host }
}

func WithPortsPollInterval(interval time.Duration) PortsOption {
	return func(manager *PortsManager) { manager.pollInterval = interval }
}

// NewPortsManager creates the extra-port reconciler. tlsConfig is required to
// serve HTTPS routes on non-default ports and may reuse the autocert config
// of the main HTTPS edge.
func NewPortsManager(routes PortRouteSource, gatewayHandler *gateway.Handler, tlsConfig *tls.Config, options ...PortsOption) (*PortsManager, error) {
	if routes == nil || gatewayHandler == nil {
		return nil, errors.New("route source and gateway handler are required")
	}
	manager := &PortsManager{
		routes:       routes,
		gateway:      gatewayHandler,
		tlsConfig:    tlsConfig,
		pollInterval: time.Second,
		servers:      map[int]*portServer{},
		statuses:     map[int]string{},
	}
	for _, option := range options {
		if option != nil {
			option(manager)
		}
	}
	return manager, nil
}

func (m *PortsManager) Run(ctx context.Context) error {
	m.reconcile(ctx)
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	defer m.stopAll()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			m.reconcile(ctx)
		}
	}
}

// PortStatus reports the listener state for a web route's extra port.
func (m *PortsManager) PortStatus(port int) string {
	m.statusMu.RLock()
	defer m.statusMu.RUnlock()
	if status, ok := m.statuses[port]; ok {
		return status
	}
	return PortStatusPending
}

func (m *PortsManager) reconcile(ctx context.Context) {
	routes, err := m.routes.ListRoutes(ctx)
	if err != nil {
		log.Printf("web port edge reconcile failed: %v", err)
		return
	}
	desired := map[int]string{}
	conflicts := map[int]bool{}
	for i := range routes {
		route := &routes[i]
		if !route.Protocol.IsWeb() || route.PublicPort == 0 || !route.Enabled {
			continue
		}
		scheme := string(route.Protocol)
		if existing, ok := desired[route.PublicPort]; ok && existing != scheme {
			conflicts[route.PublicPort] = true
			continue
		}
		desired[route.PublicPort] = scheme
	}
	statuses := map[int]string{}
	for port := range conflicts {
		delete(desired, port)
		statuses[port] = PortStatusConflict
	}
	for port, current := range m.servers {
		if scheme, ok := desired[port]; !ok || scheme != current.scheme {
			m.stop(port, current)
		}
	}
	for port, scheme := range desired {
		if current, ok := m.servers[port]; ok {
			statuses[port] = PortStatusPublished
			_ = current
			continue
		}
		if err := m.start(port, scheme); err != nil {
			statuses[port] = PortStatusBindError
			log.Printf("web port edge failed to listen on %s:%d: %v", m.bindHost, port, err)
			continue
		}
		statuses[port] = PortStatusPublished
		log.Printf("web port edge (%s) listening on %s:%d", scheme, m.bindHost, port)
	}
	m.statusMu.Lock()
	m.statuses = statuses
	m.statusMu.Unlock()
}

func (m *PortsManager) start(port int, scheme string) error {
	if scheme == "https" && m.tlsConfig == nil {
		return errors.New("TLS configuration unavailable for HTTPS port")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(m.bindHost, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	edgeInfo := gateway.Edge{Scheme: scheme, Port: port}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.gateway.ServeHTTP(w, r.WithContext(gateway.WithEdge(r.Context(), edgeInfo)))
	})
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	if scheme == "https" {
		server.TLSConfig = m.tlsConfig
		listener = tls.NewListener(listener, m.tlsConfig)
	}
	entry := &portServer{scheme: scheme, server: server, listener: listener, done: make(chan struct{})}
	m.servers[port] = entry
	go func() {
		defer close(entry.done)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("web port edge listener on port %d stopped: %v", port, err)
		}
	}()
	return nil
}

func (m *PortsManager) stop(port int, entry *portServer) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := entry.server.Shutdown(shutdownCtx); err != nil {
		_ = entry.server.Close()
	}
	select {
	case <-entry.done:
	case <-time.After(5 * time.Second):
	}
	delete(m.servers, port)
}

func (m *PortsManager) stopAll() {
	for port, entry := range m.servers {
		m.stop(port, entry)
	}
	m.statusMu.Lock()
	m.statuses = map[int]string{}
	m.statusMu.Unlock()
}
