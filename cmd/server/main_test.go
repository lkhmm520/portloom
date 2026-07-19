package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/store"
)

func TestLoadConfigRequiresAdminTokenAndUsesDefaults(t *testing.T) {
	if _, err := loadConfig(func(string) string { return "" }); err == nil {
		t.Fatal("expected missing token error")
	}
	cfg, err := loadConfig(func(key string) string {
		if key == "TM_ADMIN_TOKEN" {
			return "a-very-long-admin-token"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "127.0.0.1:8080" || cfg.GatewayAddr != "127.0.0.1:8081" || cfg.PortRangeStart != 20000 {
		t.Fatalf("cfg=%#v", cfg)
	}
}

func TestLoadConfigRequiresLiteralIPTCPBindHost(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "2001:db8::1"} {
		env := map[string]string{
			"TM_ADMIN_TOKEN":        "a-very-long-admin-token",
			"TM_TCP_EDGE_BIND_HOST": host,
		}
		if _, err := loadConfig(func(key string) string { return env[key] }); err != nil {
			t.Fatalf("literal TCP bind host %q rejected: %v", host, err)
		}
	}
	for _, host := range []string{"example.com", "127.0.0.1:9000", "[2001:db8::1]:9000"} {
		env := map[string]string{
			"TM_ADMIN_TOKEN":        "a-very-long-admin-token",
			"TM_TCP_EDGE_BIND_HOST": host,
		}
		if _, err := loadConfig(func(key string) string { return env[key] }); err == nil {
			t.Fatalf("non-literal TCP bind host %q accepted", host)
		}
	}
}

func TestMainHandlerServesHealthAPIAndWeb(t *testing.T) {
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("dashboard"), 0o600); err != nil {
		t.Fatal(err)
	}
	apiHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	handler := newMainHandler(apiHandler, web)
	for path, status := range map[string]int{"/healthz": http.StatusOK, "/api/v1/routes": http.StatusTeapot, "/": http.StatusOK} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != status {
			t.Fatalf("%s status=%d body=%s", path, res.Code, res.Body.String())
		}
	}
}

func TestControlServerHasRequestReadAndWriteDeadlines(t *testing.T) {
	server := newControlServer("127.0.0.1:0", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	if server.ReadHeaderTimeout != 10*time.Second {
		t.Fatalf("ReadHeaderTimeout=%s", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout <= server.ReadHeaderTimeout {
		t.Fatalf("ReadTimeout=%s must cover request bodies and exceed header timeout", server.ReadTimeout)
	}
	if server.WriteTimeout <= 0 {
		t.Fatalf("WriteTimeout=%s must be positive", server.WriteTimeout)
	}
}

func TestLoadConfigReadsManagedSSHSettingsAtomically(t *testing.T) {
	env := map[string]string{
		"TM_ADMIN_TOKEN":              "a-very-long-admin-token",
		"TM_AUTHORIZED_KEYS_PATH":     "/ssh/authorized_keys",
		"TM_SSH_HOST_PUBLIC_KEY_PATH": "/ssh/ssh_host_ed25519_key.pub",
		"TM_MANAGED_SSH_PORT":         "2222",
		"TM_MANAGED_SSH_ISOLATED":     "true",
	}
	cfg, err := loadConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthorizedKeysPath != "/ssh/authorized_keys" || cfg.SSHHostPublicKeyPath != "/ssh/ssh_host_ed25519_key.pub" || cfg.ManagedSSHPort != 2222 || !cfg.ManagedSSHIsolated {
		t.Fatalf("cfg=%#v", cfg)
	}
	delete(env, "TM_SSH_HOST_PUBLIC_KEY_PATH")
	if _, err := loadConfig(func(key string) string { return env[key] }); err == nil {
		t.Fatal("partial managed SSH configuration accepted")
	}
}

func TestSyncAuthorizedKeysRebuildsFileFromStore(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"), store.Options{PortRangeStart: 35000, PortRangeEnd: 35001})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.CreateEnrollmentToken(ctx, "enroll", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	agent, _, err := state.ConsumeEnrollmentToken(ctx, "enroll", "nas")
	if err != nil {
		t.Fatal(err)
	}
	algorithm, key := []byte("ssh-ed25519"), make([]byte, 32)
	wire := make([]byte, 4+len(algorithm)+4+len(key))
	binary.BigEndian.PutUint32(wire[:4], uint32(len(algorithm)))
	copy(wire[4:], algorithm)
	offset := 4 + len(algorithm)
	binary.BigEndian.PutUint32(wire[offset:offset+4], uint32(len(key)))
	copy(wire[offset+4:], key)
	publicKey := "ssh-ed25519 " + base64.StdEncoding.EncodeToString(wire)
	if err := state.SetAgentSSHKey(ctx, agent.ID, publicKey); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "authorized_keys")
	if err := syncAuthorizedKeys(ctx, state, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "portloom-agent:"+agent.ID) {
		t.Fatalf("body=%q", data)
	}
}

func TestLoadConfigEnablesNativeEdgeOnlyWithCompletePublicTLSConfiguration(t *testing.T) {
	base := map[string]string{
		"TM_ADMIN_TOKEN":     "a-very-long-admin-token",
		"TM_PUBLIC_HOST":     "console.example.com",
		"TM_EDGE_HTTP_ADDR":  ":80",
		"TM_EDGE_HTTPS_ADDR": ":443",
		"TM_TLS_CACHE_DIR":   "/data/certs",
	}
	cfg, err := loadConfig(func(key string) string { return base[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EdgeHTTPAddr != ":80" || cfg.EdgeHTTPSAddr != ":443" || cfg.TLSCacheDir != "/data/certs" {
		t.Fatalf("cfg=%#v", cfg)
	}
	delete(base, "TM_EDGE_HTTPS_ADDR")
	if _, err := loadConfig(func(key string) string { return base[key] }); err == nil {
		t.Fatal("partial edge configuration accepted")
	}
}

func TestLoadConfigRejectsMalformedNativeEdgePublicHost(t *testing.T) {
	for _, publicHost := range []string{
		"bad host.example.com",
		"console.example.com:443",
		"console..example.com",
		"127.0.0.1",
		"127.1",
		"2130706433",
		"localhost",
		"2001:db8::1",
		strings.Repeat("a", 64) + ".example.com",
	} {
		t.Run(publicHost, func(t *testing.T) {
			env := map[string]string{
				"TM_ADMIN_TOKEN":     "a-very-long-admin-token",
				"TM_PUBLIC_HOST":     publicHost,
				"TM_EDGE_HTTP_ADDR":  ":80",
				"TM_EDGE_HTTPS_ADDR": ":443",
				"TM_TLS_CACHE_DIR":   "/data/certs",
			}
			if _, err := loadConfig(func(key string) string { return env[key] }); err == nil {
				t.Fatal("malformed native-edge public host accepted")
			}
		})
	}
}

func TestRunStartsConvergenceGatedTCPPublicEdge(t *testing.T) {
	remotePort := reserveServerTestPort(t)
	publicPort := reserveServerTestPort(t)
	for publicPort == remotePort {
		publicPort = reserveServerTestPort(t)
	}
	databasePath := filepath.Join(t.TempDir(), "state.db")
	state, err := store.Open(databasePath, store.Options{PortRangeStart: remotePort, PortRangeEnd: remotePort})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := state.CreateEnrollmentToken(ctx, "enroll", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	agent, _, err := state.ConsumeEnrollmentToken(ctx, "enroll", "nas")
	if err != nil {
		t.Fatal(err)
	}
	route, err := state.CreateRoute(ctx, domain.Route{
		ClientID: agent.ID, Name: "tcp-service", Protocol: domain.ProtocolTCP,
		LocalHost: "127.0.0.1", LocalPort: remotePort, PublicPort: publicPort, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Heartbeat(ctx, agent.ID, route.DesiredRevision, []domain.RouteObservation{{
		RouteID: route.ID, ObservedRevision: route.DesiredRevision,
		LocalStatus: "healthy", TunnelStatus: "up",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	backend, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", fmt.Sprint(remotePort)))
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go serveServerTestEcho(backend)

	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("dashboard"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"TM_ADMIN_TOKEN":        "a-very-long-admin-token",
		"TM_DATABASE_PATH":      databasePath,
		"TM_WEB_DIR":            web,
		"TM_LISTEN_ADDR":        "127.0.0.1:0",
		"TM_GATEWAY_ADDR":       "127.0.0.1:0",
		"TM_TCP_EDGE_BIND_HOST": "127.0.0.1",
		"TM_PORT_RANGE_START":   fmt.Sprint(remotePort),
		"TM_PORT_RANGE_END":     fmt.Sprint(remotePort),
	}
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(runCtx, func(key string) string { return env[key] }) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("run: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("server did not stop")
		}
	}()

	connection := waitForServerTestPort(t, publicPort)
	defer connection.Close()
	if _, err := connection.Write([]byte("edge")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("edge"))
	if _, err := io.ReadFull(connection, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "edge" {
		t.Fatalf("echo=%q want edge", got)
	}
}

func reserveServerTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func serveServerTestEcho(listener net.Listener) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer connection.Close()
			_, _ = io.Copy(connection, connection)
		}()
	}
}

func waitForServerTestPort(t *testing.T, port int) net.Conn {
	t.Helper()
	address := net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp4", address, 50*time.Millisecond)
		if err == nil {
			return connection
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("TCP public edge did not listen on %s", address)
	return nil
}
