package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
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
