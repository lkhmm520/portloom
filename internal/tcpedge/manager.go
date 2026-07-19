package tcpedge

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/managedssh"
)

type RouteSource interface {
	ListRoutes(context.Context) ([]domain.Route, error)
	GetRoute(context.Context, string) (domain.Route, error)
}

type Manager struct {
	routes                RouteSource
	bindHost              string
	pollInterval          time.Duration
	isolatedAgentBindings bool
	workers               map[int]*worker
	globalSlots           chan struct{}
	perRouteLimit         int
	statusMu              sync.RWMutex
	statuses              map[string]publicationState
}

type publicationState struct {
	status string
	worker *worker
}

const (
	StatusDisabled     = "disabled"
	StatusInvalid      = "invalid"
	StatusWaitingAgent = "waiting_agent"
	StatusPending      = "pending"
	StatusConflict     = "conflict"
	StatusBindError    = "bind_error"
	StatusPublished    = "published"
)

type Option func(*Manager)

func WithBindHost(host string) Option {
	return func(manager *Manager) { manager.bindHost = host }
}

func WithPollInterval(interval time.Duration) Option {
	return func(manager *Manager) { manager.pollInterval = interval }
}

func WithIsolatedAgentBindings() Option {
	return func(manager *Manager) { manager.isolatedAgentBindings = true }
}

func WithConnectionLimits(global, perRoute int) Option {
	return func(manager *Manager) {
		if global > 0 {
			manager.globalSlots = make(chan struct{}, global)
		}
		if perRoute > 0 {
			manager.perRouteLimit = perRoute
		}
	}
}

func New(routes RouteSource, options ...Option) *Manager {
	manager := &Manager{
		routes:       routes,
		bindHost:     "0.0.0.0",
		pollInterval: time.Second,
		workers:      make(map[int]*worker),
		globalSlots:  make(chan struct{}, 1024),
		perRouteLimit: 128,
		statuses:     make(map[string]publicationState),
	}
	for _, option := range options {
		if option != nil {
			option(manager)
		}
	}
	return manager
}

func (m *Manager) Run(ctx context.Context) error {
	if err := m.reconcile(ctx); err != nil {
		log.Printf("TCP edge reconcile failed: %v", err)
	}
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	defer m.stopAll()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := m.reconcile(ctx); err != nil {
				log.Printf("TCP edge reconcile failed: %v", err)
			}
		}
	}
}

func (m *Manager) reconcile(ctx context.Context) error {
	routes, err := m.routes.ListRoutes(ctx)
	if err != nil {
		m.stopAll()
		m.replaceStatuses(nil)
		return fmt.Errorf("list routes: %w", err)
	}
	statuses := make(map[string]publicationState)
	byPort := make(map[int][]domain.Route)
	for _, route := range routes {
		if route.Protocol != domain.ProtocolTCP {
			continue
		}
		switch {
		case !route.Enabled:
			statuses[route.ID] = publicationState{status: StatusDisabled}
		case route.PublicPort < 1:
			statuses[route.ID] = publicationState{status: StatusInvalid}
		case !route.PublicationReady():
			statuses[route.ID] = publicationState{status: StatusWaitingAgent}
		default:
			statuses[route.ID] = publicationState{status: StatusPending}
			byPort[route.PublicPort] = append(byPort[route.PublicPort], route)
		}
	}
	desired := make(map[int]domain.Route)
	for port, candidates := range byPort {
		if len(candidates) != 1 {
			for _, route := range candidates {
				statuses[route.ID] = publicationState{status: StatusConflict}
			}
			continue
		}
		desired[port] = candidates[0]
	}
	for port, current := range m.workers {
		route, ok := desired[port]
		if !ok || !sameTarget(current.route, route) || !current.running() {
			current.beginStop()
			current.finishStop()
			delete(m.workers, port)
		}
	}
	for port, route := range desired {
		if current, ok := m.workers[port]; ok {
			statuses[route.ID] = publicationState{status: StatusPublished, worker: current}
			continue
		}
		worker, err := m.start(route)
		if err != nil {
			statuses[route.ID] = publicationState{status: StatusBindError}
			log.Printf("TCP edge route %q failed to listen on %s:%d: %v", route.Name, m.bindHost, port, err)
			continue
		}
		m.workers[port] = worker
		statuses[route.ID] = publicationState{status: StatusPublished, worker: worker}
		log.Printf("TCP edge route %q listening on %s:%d", route.Name, m.bindHost, port)
	}
	m.replaceStatuses(statuses)
	return nil
}

func (m *Manager) replaceStatuses(statuses map[string]publicationState) {
	if statuses == nil {
		statuses = make(map[string]publicationState)
	}
	m.statusMu.Lock()
	m.statuses = statuses
	m.statusMu.Unlock()
}

// PublicStatus returns the observed public-listener state for a TCP route.
func (m *Manager) PublicStatus(route domain.Route) string {
	if route.Protocol != domain.ProtocolTCP {
		return ""
	}
	if !route.Enabled {
		return StatusDisabled
	}
	if route.PublicPort < 1 {
		return StatusInvalid
	}
	if !route.PublicationReady() {
		return StatusWaitingAgent
	}
	m.statusMu.RLock()
	state, ok := m.statuses[route.ID]
	m.statusMu.RUnlock()
	if !ok {
		return StatusPending
	}
	if state.status == StatusPublished && (state.worker == nil || !state.worker.running()) {
		return StatusPending
	}
	return state.status
}

func sameTarget(current, desired domain.Route) bool {
	return current.ID == desired.ID && current.ClientID == desired.ClientID &&
		current.RemotePort == desired.RemotePort && current.PublicPort == desired.PublicPort
}

func (m *Manager) start(route domain.Route) (*worker, error) {
	backendHost := managedssh.LegacyBindAddress
	if m.isolatedAgentBindings {
		var err error
		backendHost, err = managedssh.BindAddress(route.ClientID)
		if err != nil {
			return nil, err
		}
	}
	network := "tcp6"
	if ip := net.ParseIP(m.bindHost); ip != nil && ip.To4() != nil {
		network = "tcp4"
	}
	listener, err := net.Listen(network, net.JoinHostPort(m.bindHost, strconv.Itoa(route.PublicPort)))
	if err != nil {
		return nil, err
	}
	workerContext, cancel := context.WithCancel(context.Background())
	worker := &worker{
		route:       route,
		backend:     net.JoinHostPort(backendHost, strconv.Itoa(route.RemotePort)),
		listener:    listener,
		active:      make(map[net.Conn]struct{}),
		done:        make(chan struct{}),
		authorize:   m.authorizeAcceptedRoute,
		ctx:         workerContext,
		cancel:      cancel,
		globalSlots: m.globalSlots,
		routeSlots:  make(chan struct{}, m.perRouteLimit),
	}
	m.workers[route.PublicPort] = worker
	m.statusMu.Lock()
	m.statuses[route.ID] = publicationState{status: StatusPublished, worker: worker}
	m.statusMu.Unlock()
	worker.wait.Add(1)
	go worker.serve()
	return worker, nil
}

func (m *Manager) authorizeAcceptedRoute(expected domain.Route) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	current, err := m.routes.GetRoute(ctx, expected.ID)
	return err == nil && current.Protocol == domain.ProtocolTCP && current.PublicationReady() &&
		sameTarget(expected, current)
}

func (m *Manager) stopAll() {
	workers := make([]*worker, 0, len(m.workers))
	for port, worker := range m.workers {
		workers = append(workers, worker)
		worker.beginStop()
		delete(m.workers, port)
	}
	finished := make(chan struct{})
	go func() {
		for _, worker := range workers {
			worker.finishStop()
		}
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		log.Printf("TCP edge shutdown timed out with %d worker(s)", len(workers))
	}
	m.replaceStatuses(nil)
}

type worker struct {
	route       domain.Route
	backend     string
	listener    net.Listener
	ctx         context.Context
	cancel      context.CancelFunc
	globalSlots chan struct{}
	routeSlots  chan struct{}
	mu          sync.Mutex
	active      map[net.Conn]struct{}
	wait        sync.WaitGroup
	stopOnce    sync.Once
	finishOnce  sync.Once
	stopped     bool
	done        chan struct{}
	authorize   func(domain.Route) bool
}

func (w *worker) serve() {
	defer w.wait.Done()
	defer close(w.done)
	for {
		client, err := w.listener.Accept()
		if err != nil {
			return
		}
		if !w.authorize(w.route) {
			_ = client.Close()
			continue
		}
		if !w.acquireSlot() {
			_ = client.Close()
			continue
		}
		if !w.track(client) {
			w.releaseSlot()
			_ = client.Close()
			continue
		}
		w.wait.Add(1)
		go w.proxy(client)
	}
}

func (w *worker) running() bool {
	select {
	case <-w.done:
		return false
	default:
		return true
	}
}

func (w *worker) proxy(client net.Conn) {
	defer w.wait.Done()
	defer w.releaseSlot()
	defer w.untrack(client)
	defer client.Close()
	dialContext, cancel := context.WithTimeout(w.ctx, 10*time.Second)
	defer cancel()
	backend, err := (&net.Dialer{}).DialContext(dialContext, "tcp", w.backend)
	if err != nil {
		return
	}
	if !w.track(backend) {
		_ = backend.Close()
		return
	}
	defer w.untrack(backend)
	defer backend.Close()

	var copies sync.WaitGroup
	copies.Add(2)
	go copyStream(&copies, backend, client)
	go copyStream(&copies, client, backend)
	copies.Wait()
}

func copyStream(wait *sync.WaitGroup, destination net.Conn, source net.Conn) {
	defer wait.Done()
	_, _ = io.Copy(destination, source)
	if closer, ok := destination.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
	}
}

func (w *worker) acquireSlot() bool {
	select {
	case w.globalSlots <- struct{}{}:
	default:
		return false
	}
	select {
	case w.routeSlots <- struct{}{}:
		return true
	default:
		<-w.globalSlots
		return false
	}
}

func (w *worker) releaseSlot() {
	<-w.routeSlots
	<-w.globalSlots
}

func (w *worker) track(connection net.Conn) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return false
	}
	w.active[connection] = struct{}{}
	return true
}

func (w *worker) untrack(connection net.Conn) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.active, connection)
}

func (w *worker) beginStop() {
	w.stopOnce.Do(func() {
		w.mu.Lock()
		w.stopped = true
		w.mu.Unlock()
		w.cancel()
		_ = w.listener.Close()
		w.mu.Lock()
		for connection := range w.active {
			_ = connection.Close()
		}
		w.mu.Unlock()
	})
}

func (w *worker) finishStop() {
	w.finishOnce.Do(func() { w.wait.Wait() })
}
