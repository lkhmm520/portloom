package agent

import (
	"context"
	"encoding/json"
	"github.com/lkhmm520/portloom/internal/domain"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPServerClientFetchesDesiredStateWithAuthentication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/agent/desired" {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("observed_revision") != "12" {
			t.Errorf("query=%s", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer token-1" || r.Header.Get("X-Client-ID") != "nas-01" {
			t.Errorf("headers=%v", r.Header)
		}
		_ = json.NewEncoder(w).Encode(DesiredState{Revision: 13, Routes: []domain.Route{{ID: "r1"}}})
	}))
	defer server.Close()
	client, err := NewHTTPServerClient(server.URL+"/", "nas-01", "token-1", true, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.FetchDesired(context.Background(), 12)
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != 13 || len(state.Routes) != 1 || state.Routes[0].ID != "r1" {
		t.Fatalf("state=%#v", state)
	}
}
func TestHTTPServerClientReportsObservedState(t *testing.T) {
	var got ObservedState
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/agent/observed" {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type=%q", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := NewHTTPServerClient(server.URL, "nas-01", "token-1", true, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	want := ObservedState{Revision: 4, Routes: []RouteObservation{{RouteID: "r1", LocalStatus: StatusUp, TunnelStatus: StatusUp}}}
	if err := client.ReportObserved(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if got.Revision != 4 || len(got.Routes) != 1 || got.Routes[0].RouteID != "r1" {
		t.Fatalf("got=%#v", got)
	}
}
func TestHTTPServerClientRejectsUnexpectedStatusAndOversizedResponse(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{"status": func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", 502) }, "large": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(make([]byte, maxResponseBytes+1)) }} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			client, err := NewHTTPServerClient(server.URL, "nas-01", "token", true, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.FetchDesired(context.Background(), 0); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestHTTPServerClientRejectsPlaintextTokenTransportByDefault(t *testing.T) {
	if _, err := NewHTTPServerClient("http://localhost:8080", "nas-01", "token", false, nil); err == nil {
		t.Fatal("expected loopback HTTP rejection without explicit opt-in")
	}
	if _, err := NewHTTPServerClient("http://manager.example.com", "nas-01", "token", true, nil); err == nil {
		t.Fatal("expected non-loopback HTTP rejection with opt-in")
	}
	if _, err := NewHTTPServerClient("http://127.99.1.2:8080", "nas-01", "token", true, nil); err != nil {
		t.Fatalf("explicit loopback HTTP opt-in rejected: %v", err)
	}
}

func TestHTTPServerClientRejectsHTTPSRedirectToInsecureHTTP(t *testing.T) {
	insecureRequests := 0
	insecure := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { insecureRequests++ }))
	defer insecure.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, insecure.URL, http.StatusTemporaryRedirect)
	}))
	defer secure.Close()
	client, err := NewHTTPServerClient(secure.URL, "nas-01", "token", false, secure.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.FetchDesired(context.Background(), 0); err == nil {
		t.Fatal("expected insecure redirect rejection")
	}
	if insecureRequests != 0 {
		t.Fatalf("followed insecure redirect and sent %d request(s)", insecureRequests)
	}
}

func TestHTTPServerClientRejectsAllRedirectsBeforeForwardingBearerToken(t *testing.T) {
	redirectedRequests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/capture" {
			redirectedRequests++
			if r.Header.Get("Authorization") != "" {
				t.Errorf("redirect received bearer token")
			}
			return
		}
		http.Redirect(w, r, "/capture", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client, err := NewHTTPServerClient(server.URL, "nas-01", "token", false, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.FetchDesired(context.Background(), 0); err == nil {
		t.Fatal("expected redirect rejection")
	}
	if redirectedRequests != 0 {
		t.Fatalf("followed redirect %d time(s)", redirectedRequests)
	}
}
