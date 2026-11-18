package cutover

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/api"
	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/store"
)

func gateTestConfig(t *testing.T, servers map[string]*httptest.Server, hosts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	profiles := map[string]profile{}
	for id, server := range servers {
		ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
		if err := os.WriteFile(filepath.Join(dir, id+".pem"), ca, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+".token"), []byte("fixture-token\n"), 0600); err != nil {
			t.Fatal(err)
		}
		profiles[id] = profile{URL: server.URL, ManagementHost: hosts[id], CAFile: id + ".pem", TokenFile: id + ".token"}
	}
	path := filepath.Join(dir, "profiles.json")
	data, err := json.Marshal(planConfig{Profiles: profiles})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func gateTestServer(t *testing.T, management string) (*store.Store, string, http.Handler) {
	t.Helper()
	ctx := context.Background()
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"), store.Options{PortRangeStart: 40000, PortRangeEnd: 49999})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { state.Close() })
	if err := state.CreateEnrollmentToken(ctx, "enroll", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	a, _, err := state.ConsumeEnrollmentToken(ctx, "enroll", "fixture-client")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		host, path string
		enabled    bool
	}{{appHost, "", true}, {appHost, "/off", false}, {apiHost, "", true}, {"unrelated.example.test", "", true}} {
		if _, err := state.CreateRoute(ctx, domain.Route{ClientID: a.ID, Name: "fixture", Protocol: domain.ProtocolHTTPS, Domain: row.host, PathPrefix: row.path, LocalHost: "localhost", LocalPort: 8080, Enabled: row.enabled}); err != nil {
			t.Fatal(err)
		}
	}
	return state, a.ID, api.New(state, api.Config{AdminToken: "fixture-token", PublicHost: management, EnableCutover: true})
}

func TestGateClientAuthenticatedSnapshotAndGroupCAS(t *testing.T) {
	ctx := context.Background()
	const management = "source-admin.example.test"
	_, clientID, handler := gateTestServer(t, management)
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	path := gateTestConfig(t, map[string]*httptest.Server{"source": server}, map[string]string{"source": management})
	nodes, err := OpenGateNodes(path, "source")
	if err != nil {
		t.Fatal(err)
	}
	defer nodes.CloseIdleConnections()
	hosts := []string{apiHost, appHost}
	live, err := nodes.InspectNode(ctx, "source", hosts)
	if err != nil || live.ID != "source" || live.ManagementHost != management || len(live.Gates) != 2 || len(live.Routes) != 3 || live.Gates[appHost] != (store.PublicationGate{}) {
		t.Fatalf("snapshot=%+v error=%v", live, err)
	}
	disabled := 0
	for _, r := range live.Routes {
		if !r.Enabled {
			disabled++
		}
	}
	if disabled != 1 {
		t.Fatal("disabled route missing")
	}
	group := []store.PublicationGateExpectation{{Host: apiHost}, {Host: appHost}}
	next := store.PublicationPolicy{Phase: store.PublicationFrozen, OperationID: "operation", ClientID: clientID}
	for range 2 {
		if err := nodes.CompareNode(ctx, "source", group, next); err != nil {
			t.Fatal(err)
		}
	}
	live, err = nodes.InspectNode(ctx, "source", hosts)
	if err != nil || live.Gates[appHost] != (store.PublicationGate{PublicationPolicy: next, Generation: 1}) || live.Gates[apiHost] != live.Gates[appHost] {
		t.Fatalf("snapshot=%+v error=%v", live, err)
	}
	group[0].Expected = live.Gates[apiHost]
	next.Phase = store.PublicationBlocked
	if err := nodes.CompareNode(ctx, "source", group, next); !errors.Is(err, ErrSwitchConflict) {
		t.Fatalf("stale group: %v", err)
	}
	after, err := nodes.InspectNode(ctx, "source", hosts)
	if err != nil || after.Gates[appHost] != live.Gates[appHost] || after.Gates[apiHost] != live.Gates[apiHost] {
		t.Fatalf("stale group changed a member: %v %v", after.Gates, err)
	}

	wrong, err := OpenGateNodes(gateTestConfig(t, map[string]*httptest.Server{"source": server}, map[string]string{"source": "wrong.example.test"}), "source")
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.CloseIdleConnections()
	group[1].Expected = live.Gates[appHost]
	if err := wrong.CompareNode(ctx, "source", group, next); !errors.Is(err, ErrSwitchConflict) {
		t.Fatalf("wrong identity: %v", err)
	}
	after, err = nodes.InspectNode(ctx, "source", hosts)
	if err != nil || after.Gates[apiHost] != live.Gates[apiHost] {
		t.Fatal("wrong identity changed gate")
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "source.token"), []byte("incorrect"), 0600); err != nil {
		t.Fatal(err)
	}
	unauth, err := OpenGateNodes(path, "source")
	if err != nil {
		t.Fatal(err)
	}
	defer unauth.CloseIdleConnections()
	if _, err := unauth.InspectNode(ctx, "source", hosts); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("authentication failure: %v", err)
	}
}

func TestGateClientRejectsRedirectWithoutReplayingCredentials(t *testing.T) {
	var redirected atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-target" {
			redirected.Add(1)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("missing authorization")
		}
		http.Redirect(w, r, "/redirect-target", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	nodes, err := OpenGateNodes(gateTestConfig(t, map[string]*httptest.Server{"source": server}, map[string]string{"source": "admin.example.test"}), "source")
	if err != nil {
		t.Fatal(err)
	}
	defer nodes.CloseIdleConnections()
	err = nodes.CompareNode(context.Background(), "source", []store.PublicationGateExpectation{{Host: appHost}}, store.PublicationPolicy{Phase: store.PublicationFrozen, OperationID: "switch", ClientID: "client"})
	if err == nil || strings.Contains(err.Error(), "fixture-token") || redirected.Load() != 0 {
		t.Fatalf("err=%v redirected=%d", err, redirected.Load())
	}
}
