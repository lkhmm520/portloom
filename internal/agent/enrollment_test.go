package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestEnrollAndPersistCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/agent/enroll" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		var request map[string]string
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["name"] != "nas-home" || request["token"] != "one-time" {
			t.Fatalf("request=%v", request)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"agent": map[string]string{"id": "agent-1"}, "token": "long-lived"})
	}))
	defer server.Close()

	credentials, err := Enroll(context.Background(), server.URL, "nas-home", "one-time", true, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "agent.json")
	if err := SaveCredentials(path, credentials); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ClientID != "agent-1" || loaded.Token != "long-lived" {
		t.Fatalf("loaded=%#v", loaded)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}

func TestResolveCredentialsLoadsStateBeforeEnrollment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")
	if err := SaveCredentials(path, Credentials{ClientID: "saved-id", Token: "saved-token"}); err != nil {
		t.Fatal(err)
	}
	cfg := Config{StatePath: path, ClientName: "nas", EnrollmentToken: "invalid-if-used"}
	credentials, err := ResolveCredentials(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.ClientID != "saved-id" {
		t.Fatalf("credentials=%#v", credentials)
	}
}

func TestEnrollRejectsInsecureControlPlaneURLBeforeSendingToken(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()

	if _, err := Enroll(context.Background(), server.URL, "nas-home", "one-time", false, server.Client()); err == nil {
		t.Fatal("expected HTTP URL rejection")
	}
	if requests != 0 {
		t.Fatalf("sent enrollment token in %d request(s)", requests)
	}
	if _, err := Enroll(context.Background(), "http://manager.example.com", "nas-home", "one-time", true, server.Client()); err == nil {
		t.Fatal("expected non-loopback HTTP URL rejection")
	}
}

func TestEnrollRejectsHTTPSRedirectToInsecureHTTP(t *testing.T) {
	insecureRequests := 0
	insecure := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { insecureRequests++ }))
	defer insecure.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, insecure.URL, http.StatusTemporaryRedirect)
	}))
	defer secure.Close()

	if _, err := Enroll(context.Background(), secure.URL, "nas-home", "one-time", false, secure.Client()); err == nil {
		t.Fatal("expected insecure redirect rejection")
	}
	if insecureRequests != 0 {
		t.Fatalf("followed insecure redirect and sent %d request(s)", insecureRequests)
	}
}

func TestEnrollRejectsAllRedirectsBeforeForwardingOneTimeToken(t *testing.T) {
	redirectedRequests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/capture" {
			redirectedRequests++
			return
		}
		http.Redirect(w, r, "/capture", http.StatusPermanentRedirect)
	}))
	defer server.Close()
	if _, err := Enroll(context.Background(), server.URL, "nas-home", "one-time", false, server.Client()); err == nil {
		t.Fatal("expected redirect rejection")
	}
	if redirectedRequests != 0 {
		t.Fatalf("followed redirect %d time(s)", redirectedRequests)
	}
}
