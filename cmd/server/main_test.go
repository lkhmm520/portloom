package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
