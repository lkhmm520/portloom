package main

import (
	"bytes"
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanReadsBothProfilesOverTrustedHTTPS(t *testing.T) {
	dir := t.TempDir()
	source := apiFixture(t, "source-secret", `[{"id":"old-client"}]`, `[{"id":"r2","client_id":"old-client","domain":"media.example.net","path_prefix":"/alt","protocol":"https","local_host":"127.0.0.1","local_port":8080,"enabled":false,"last_error":"never-print-me"},{"id":"r1","client_id":"old-client","domain":"media.example.net","protocol":"https","local_host":"127.0.0.1","local_port":8081,"enabled":true}]`)
	target := apiFixture(t, "target-secret", `[{"id":"new-client"}]`, `[]`)
	writeProfile(t, dir, "old", source, "source-secret", "old.example.net")
	writeProfile(t, dir, "new", target, "target-secret", "new.example.net")
	var out, stderr bytes.Buffer
	code := run(context.Background(), []string{"plan", "--config", filepath.Join(dir, "profiles.json"), "--source", "old", "--target", "new", "--source-client", "old-client", "--target-client", "new-client", "--host", "MEDIA.example.net."}, &out, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out.String(), stderr.String())
	}
	for _, want := range []string{`"status": "preview"`, `"id": "r1"`, `"id": "r2"`, `"enabled": false`, `"local_target": "127.0.0.1:8080"`, `"execute": "unavailable"`, `"public": "not_performed"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %s in %s", want, &out)
		}
	}
	if strings.Index(out.String(), `"id": "r1"`) > strings.Index(out.String(), `"id": "r2"`) {
		t.Error("rows not sorted by host/path")
	}
	for _, secret := range []string{"source-secret", "target-secret", "never-print-me"} {
		if strings.Contains(out.String()+stderr.String(), secret) {
			t.Errorf("leaked %s", secret)
		}
	}
}

func apiFixture(t *testing.T, token, clients, routes string) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+token || r.URL.RawQuery != "" {
			t.Error("unexpected method, credential, or query")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/v1/clients":
			fmt.Fprint(w, clients)
		case "/api/v1/routes":
			fmt.Fprint(w, routes)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func writeProfile(t *testing.T, dir, name string, server *httptest.Server, token, managementHost string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, name+".token"), token+"\n")
	mustWrite(t, filepath.Join(dir, name+".pem"), string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})))
	var profiles string
	config := filepath.Join(dir, "profiles.json")
	if data, err := os.ReadFile(config); err == nil {
		profiles = strings.TrimSuffix(string(data), "}}") + ","
	} else {
		profiles = `{"profiles":{`
	}
	mustWrite(t, config, profiles+fmt.Sprintf(`%q:{"url":%q,"management_host":%q,"token_file":%q,"ca_file":%q}}}`, name, server.URL, managementHost, name+".token", name+".pem"))
}

func mustWrite(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}
