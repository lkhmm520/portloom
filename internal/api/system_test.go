package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminSystemInfoReturnsManagedSSHHostKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh_host_ed25519_key.pub")
	if err := os.WriteFile(path, []byte(apiEd25519Key(9)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := New(openTestStore(t), Config{
		AdminToken: "admin-secret", AuthorizedKeysPath: filepath.Join(t.TempDir(), "authorized_keys"),
		SSHHostPublicKeyPath: path, ManagedSSHPort: 2222, ServerVersion: "v1.2.3",
	})
	unauthorized := performJSON(t, handler, http.MethodGet, "/api/v1/system", nil, "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	response := performJSON(t, handler, http.MethodGet, "/api/v1/system", nil, "admin-secret")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		ManagedSSH bool   `json:"managed_ssh"`
		SSHPort    int    `json:"ssh_port"`
		SSHHostKey string `json:"ssh_host_key"`
		Version    string `json:"version"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.ManagedSSH || body.SSHPort != 2222 || body.SSHHostKey == "" || body.Version != "v1.2.3" {
		t.Fatalf("body=%+v", body)
	}
	if body.SSHHostKey == apiEd25519Key(9) {
		t.Fatal("host key comment was not removed")
	}
}

func TestAdminSystemInfoRejectsOversizedSSHHostPublicKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh_host_ed25519_key.pub")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxSSHHostPublicKeyBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := New(openTestStore(t), Config{
		AdminToken: "admin-secret", AuthorizedKeysPath: filepath.Join(t.TempDir(), "authorized_keys"),
		SSHHostPublicKeyPath: path, ManagedSSHPort: 2222,
	})
	response := performJSON(t, handler, http.MethodGet, "/api/v1/system", nil, "admin-secret")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAdminSystemInfoReportsManagedSSHDisabledWithoutPaths(t *testing.T) {
	handler := New(openTestStore(t), Config{AdminToken: "admin-secret"})
	response := performJSON(t, handler, http.MethodGet, "/api/v1/system", nil, "admin-secret")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["managed_ssh"] != false {
		t.Fatalf("body=%+v", body)
	}
}
