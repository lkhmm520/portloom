package agent

import (
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/lkhmm520/portloom/internal/udpframe"
)

// udpRelay bridges the TCP-only SSH tunnel to a local UDP service. It listens
// on an ephemeral loopback TCP port (the target of the SSH remote forward),
// unwraps framed datagrams toward the UDP target, and frames replies back.
// One accepted TCP connection corresponds to one public client session.
type udpRelay struct {
	listener net.Listener
	target   string

	mu     sync.Mutex
	closed bool
	conns  map[net.Conn]struct{}
	wait   sync.WaitGroup
}

func newUDPRelay(localHost string, localPort int) (*udpRelay, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen UDP relay: %w", err)
	}
	relay := &udpRelay{
		listener: listener,
		target:   net.JoinHostPort(localHost, strconv.Itoa(localPort)),
		conns:    map[net.Conn]struct{}{},
	}
	relay.wait.Add(1)
	go relay.serve()
	return relay, nil
}

// Port returns the loopback TCP port the SSH remote forward should target.
func (r *udpRelay) Port() int {
	return r.listener.Addr().(*net.TCPAddr).Port
}

// Target returns the UDP destination the relay forwards to.
func (r *udpRelay) Target() string { return r.target }

func (r *udpRelay) serve() {
	defer r.wait.Done()
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			return
		}
		if !r.track(conn) {
			_ = conn.Close()
			return
		}
		r.wait.Add(1)
		go r.bridge(conn)
	}
}

func (r *udpRelay) bridge(tunnel net.Conn) {
	defer r.wait.Done()
	defer r.untrack(tunnel)
	defer tunnel.Close()
	socket, err := net.Dial("udp", r.target)
	if err != nil {
		return
	}
	if !r.track(socket) {
		_ = socket.Close()
		return
	}
	defer r.untrack(socket)
	defer socket.Close()

	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		buffer := make([]byte, udpframe.MaxPayload)
		for {
			length, err := udpframe.Read(tunnel, buffer)
			if err != nil {
				return
			}
			if _, err := socket.Write(buffer[:length]); err != nil {
				return
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		buffer := make([]byte, udpframe.MaxPayload)
		for {
			length, err := socket.Read(buffer)
			if err != nil {
				return
			}
			if err := udpframe.Write(tunnel, buffer[:length]); err != nil {
				return
			}
		}
	}()
	<-done
}

func (r *udpRelay) track(conn net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	r.conns[conn] = struct{}{}
	return true
}

func (r *udpRelay) untrack(conn net.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.conns, conn)
}

func (r *udpRelay) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	conns := make([]net.Conn, 0, len(r.conns))
	for conn := range r.conns {
		conns = append(conns, conn)
	}
	r.mu.Unlock()
	_ = r.listener.Close()
	for _, conn := range conns {
		_ = conn.Close()
	}
	r.wait.Wait()
}
