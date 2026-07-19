// Package tcpedge publishes TCP and UDP routes on dedicated public ports and
// forwards traffic into the loopback tunnel ports maintained by the agents.
// UDP datagrams are carried across the TCP-only SSH tunnel with the udpframe
// encapsulation, which the agent unwraps back into datagrams.
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
	"github.com/lkhmm520/portloom/internal/udpframe"
)

type RouteSource interface {
	ListRoutes(context.Context) ([]domain.Route, error)
	GetRoute(context.Context, string) (domain.Route, error)
}

// TrafficObserver receives per-route traffic accounting from stream workers.
type TrafficObserver interface {
	ObserveStream(routeID string, bytesIn, bytesOut int64)
}

type portKey struct {
	protocol domain.Protocol
	port     int
}

type streamWorker interface {
	workerRoute() domain.Route
	running() bool
	beginStop()
	finishStop()
}

type Manager struct {
	routes                RouteSource
	bindHost              string
	pollInterval          time.Duration
	isolatedAgentBindings bool
	workers               map[portKey]streamWorker
	globalSlots           chan struct{}
	perRouteLimit         int
	observer              TrafficObserver
	statusMu              sync.RWMutex
	statuses              map[string]publicationState
}

type publicationState struct {
	status string
	worker streamWorker
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

const udpSessionIdleTimeout = 60 * time.Second

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

func WithTrafficObserver(observer TrafficObserver) Option {
	return func(manager *Manager) { manager.observer = observer }
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
		routes:        routes,
		bindHost:      "0.0.0.0",
		pollInterval:  time.Second,
		workers:       make(map[portKey]streamWorker),
		globalSlots:   make(chan struct{}, 1024),
		perRouteLimit: 128,
		statuses:      make(map[string]publicationState),
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
		log.Printf("stream edge reconcile failed: %v", err)
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
				log.Printf("stream edge reconcile failed: %v", err)
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
	byPort := make(map[portKey][]domain.Route)
	for _, route := range routes {
		if !route.Protocol.IsStream() {
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
			key := portKey{protocol: route.Protocol, port: route.PublicPort}
			byPort[key] = append(byPort[key], route)
		}
	}
	desired := make(map[portKey]domain.Route)
	for key, candidates := range byPort {
		if len(candidates) != 1 {
			for _, route := range candidates {
				statuses[route.ID] = publicationState{status: StatusConflict}
			}
			continue
		}
		desired[key] = candidates[0]
	}
	for key, current := range m.workers {
		route, ok := desired[key]
		if !ok || !sameTarget(current.workerRoute(), route) || !current.running() {
			current.beginStop()
			current.finishStop()
			delete(m.workers, key)
		}
	}
	for key, route := range desired {
		if current, ok := m.workers[key]; ok {
			statuses[route.ID] = publicationState{status: StatusPublished, worker: current}
			continue
		}
		worker, err := m.start(route)
		if err != nil {
			statuses[route.ID] = publicationState{status: StatusBindError}
			log.Printf("stream edge route %q failed to listen on %s %s:%d: %v",
				route.Name, route.Protocol, m.bindHost, key.port, err)
			continue
		}
		m.workers[key] = worker
		statuses[route.ID] = publicationState{status: StatusPublished, worker: worker}
		log.Printf("stream edge route %q listening on %s %s:%d", route.Name, route.Protocol, m.bindHost, key.port)
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

// PublicStatus returns the observed public-listener state for a TCP/UDP route.
func (m *Manager) PublicStatus(route domain.Route) string {
	if !route.Protocol.IsStream() {
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
		current.Protocol == desired.Protocol &&
		current.RemotePort == desired.RemotePort && current.PublicPort == desired.PublicPort
}

func (m *Manager) backendAddress(route domain.Route) (string, error) {
	backendHost := managedssh.LegacyBindAddress
	if m.isolatedAgentBindings {
		var err error
		backendHost, err = managedssh.BindAddress(route.ClientID)
		if err != nil {
			return "", err
		}
	}
	return net.JoinHostPort(backendHost, strconv.Itoa(route.RemotePort)), nil
}

func (m *Manager) start(route domain.Route) (streamWorker, error) {
	backend, err := m.backendAddress(route)
	if err != nil {
		return nil, err
	}
	if route.Protocol == domain.ProtocolUDP {
		return m.startUDP(route, backend)
	}
	return m.startTCP(route, backend)
}

func (m *Manager) startTCP(route domain.Route, backend string) (streamWorker, error) {
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
		backend:     backend,
		listener:    listener,
		active:      make(map[net.Conn]struct{}),
		done:        make(chan struct{}),
		authorize:   m.authorizeAcceptedRoute,
		observer:    m.observer,
		ctx:         workerContext,
		cancel:      cancel,
		globalSlots: m.globalSlots,
		routeSlots:  make(chan struct{}, m.perRouteLimit),
	}
	worker.wait.Add(1)
	go worker.serve()
	return worker, nil
}

func (m *Manager) startUDP(route domain.Route, backend string) (streamWorker, error) {
	network := "udp6"
	if ip := net.ParseIP(m.bindHost); ip != nil && ip.To4() != nil {
		network = "udp4"
	}
	address, err := net.ResolveUDPAddr(network, net.JoinHostPort(m.bindHost, strconv.Itoa(route.PublicPort)))
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP(network, address)
	if err != nil {
		return nil, err
	}
	workerContext, cancel := context.WithCancel(context.Background())
	worker := &udpWorker{
		route:       route,
		backend:     backend,
		conn:        conn,
		sessions:    map[string]*udpSession{},
		done:        make(chan struct{}),
		authorize:   m.authorizeAcceptedRoute,
		observer:    m.observer,
		ctx:         workerContext,
		cancel:      cancel,
		globalSlots: m.globalSlots,
		routeSlots:  make(chan struct{}, m.perRouteLimit),
	}
	worker.wait.Add(1)
	go worker.serve()
	return worker, nil
}

func (m *Manager) authorizeAcceptedRoute(expected domain.Route) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	current, err := m.routes.GetRoute(ctx, expected.ID)
	return err == nil && current.Protocol == expected.Protocol && current.PublicationReady() &&
		sameTarget(expected, current)
}

func (m *Manager) stopAll() {
	workers := make([]streamWorker, 0, len(m.workers))
	for key, worker := range m.workers {
		workers = append(workers, worker)
		worker.beginStop()
		delete(m.workers, key)
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
		log.Printf("stream edge shutdown timed out with %d worker(s)", len(workers))
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
	observer    TrafficObserver
	mu          sync.Mutex
	active      map[net.Conn]struct{}
	wait        sync.WaitGroup
	stopOnce    sync.Once
	finishOnce  sync.Once
	stopped     bool
	done        chan struct{}
	authorize   func(domain.Route) bool
}

func (w *worker) workerRoute() domain.Route { return w.route }

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

	var inbound, outbound int64
	var copies sync.WaitGroup
	copies.Add(2)
	go copyStream(&copies, backend, client, &inbound)
	go copyStream(&copies, client, backend, &outbound)
	copies.Wait()
	if w.observer != nil {
		w.observer.ObserveStream(w.route.ID, inbound, outbound)
	}
}

func copyStream(wait *sync.WaitGroup, destination net.Conn, source net.Conn, counted *int64) {
	defer wait.Done()
	n, _ := io.Copy(destination, source)
	if counted != nil {
		*counted += n
	}
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

// udpWorker forwards datagrams between a public UDP port and the agent's
// framed TCP relay behind the SSH tunnel. Each public client address maps to
// one backend TCP connection; idle sessions expire after a timeout.
type udpWorker struct {
	route       domain.Route
	backend     string
	conn        *net.UDPConn
	ctx         context.Context
	cancel      context.CancelFunc
	globalSlots chan struct{}
	routeSlots  chan struct{}
	observer    TrafficObserver
	mu          sync.Mutex
	sessions    map[string]*udpSession
	wait        sync.WaitGroup
	stopOnce    sync.Once
	finishOnce  sync.Once
	stopped     bool
	done        chan struct{}
	authorize   func(domain.Route) bool
}

type udpSession struct {
	client   *net.UDPAddr
	backend  net.Conn
	lastSeen time.Time
	bytesIn  int64
	bytesOut int64
}

func (w *udpWorker) workerRoute() domain.Route { return w.route }

func (w *udpWorker) running() bool {
	select {
	case <-w.done:
		return false
	default:
		return true
	}
}

func (w *udpWorker) serve() {
	defer w.wait.Done()
	defer close(w.done)
	w.wait.Add(1)
	go w.expireIdleSessions()
	buffer := make([]byte, udpframe.MaxPayload)
	for {
		length, client, err := w.conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		if length == 0 {
			continue
		}
		session := w.session(client)
		if session == nil {
			continue
		}
		w.mu.Lock()
		session.lastSeen = time.Now()
		session.bytesIn += int64(length)
		w.mu.Unlock()
		if err := udpframe.Write(session.backend, buffer[:length]); err != nil {
			w.dropSession(client.String(), session)
		}
	}
}

func (w *udpWorker) session(client *net.UDPAddr) *udpSession {
	key := client.String()
	w.mu.Lock()
	existing, ok := w.sessions[key]
	w.mu.Unlock()
	if ok {
		return existing
	}
	if !w.authorize(w.route) || !w.acquireSlot() {
		return nil
	}
	dialContext, cancel := context.WithTimeout(w.ctx, 10*time.Second)
	defer cancel()
	backend, err := (&net.Dialer{}).DialContext(dialContext, "tcp", w.backend)
	if err != nil {
		w.releaseSlot()
		return nil
	}
	session := &udpSession{client: client, backend: backend, lastSeen: time.Now()}
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		_ = backend.Close()
		w.releaseSlot()
		return nil
	}
	if raced, ok := w.sessions[key]; ok {
		w.mu.Unlock()
		_ = backend.Close()
		w.releaseSlot()
		return raced
	}
	w.sessions[key] = session
	w.mu.Unlock()
	w.wait.Add(1)
	go w.readBackend(key, session)
	return session
}

func (w *udpWorker) readBackend(key string, session *udpSession) {
	defer w.wait.Done()
	buffer := make([]byte, udpframe.MaxPayload)
	for {
		length, err := udpframe.Read(session.backend, buffer)
		if err != nil {
			w.dropSession(key, session)
			return
		}
		if _, err := w.conn.WriteToUDP(buffer[:length], session.client); err != nil {
			w.dropSession(key, session)
			return
		}
		w.mu.Lock()
		session.lastSeen = time.Now()
		session.bytesOut += int64(length)
		w.mu.Unlock()
	}
}

func (w *udpWorker) dropSession(key string, session *udpSession) {
	w.mu.Lock()
	current, ok := w.sessions[key]
	if !ok || current != session {
		w.mu.Unlock()
		return
	}
	delete(w.sessions, key)
	bytesIn, bytesOut := session.bytesIn, session.bytesOut
	w.mu.Unlock()
	_ = session.backend.Close()
	w.releaseSlot()
	if w.observer != nil {
		w.observer.ObserveStream(w.route.ID, bytesIn, bytesOut)
	}
}

func (w *udpWorker) expireIdleSessions() {
	defer w.wait.Done()
	ticker := time.NewTicker(udpSessionIdleTimeout / 4)
	defer ticker.Stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-udpSessionIdleTimeout)
			w.mu.Lock()
			expired := make(map[string]*udpSession)
			for key, session := range w.sessions {
				if session.lastSeen.Before(cutoff) {
					expired[key] = session
				}
			}
			w.mu.Unlock()
			for key, session := range expired {
				w.dropSession(key, session)
			}
		}
	}
}

func (w *udpWorker) acquireSlot() bool {
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

func (w *udpWorker) releaseSlot() {
	<-w.routeSlots
	<-w.globalSlots
}

func (w *udpWorker) beginStop() {
	w.stopOnce.Do(func() {
		w.mu.Lock()
		w.stopped = true
		sessions := make(map[string]*udpSession, len(w.sessions))
		for key, session := range w.sessions {
			sessions[key] = session
		}
		w.mu.Unlock()
		w.cancel()
		_ = w.conn.Close()
		for key, session := range sessions {
			w.dropSession(key, session)
		}
	})
}

func (w *udpWorker) finishStop() {
	w.finishOnce.Do(func() { w.wait.Wait() })
}
