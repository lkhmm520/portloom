package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/api"
	"github.com/lkhmm520/portloom/internal/cutover"
	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/store"
)

func TestSwitchCLISettlesCommittedGateAfterReadbackLoss(t *testing.T) {
	if os.Getenv("PORTLOOM_TEST_BIND") != "1" {
		t.Skip("set PORTLOOM_TEST_BIND=1 to exercise CLI, TLS APIs, SQLite and real BIND")
	}
	const operation = "loss-recovery"
	const host = "app.cli.test"
	const zone = "cli.test"
	const key = "cli-key.cli.test"
	const marker = "epoch.cli.test"
	const body = "canary from standby\n"
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "journal"), 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	secret := base64.StdEncoding.EncodeToString(make([]byte, 32)) // disposable key for isolated BIND only
	write("dns.key", []byte(secret))
	dnsCfg := cutover.RFC2136Config{Primary: "127.0.0.1:53", Zone: zone, Hosts: []string{host}, Marker: marker, KeyName: key, Secret: secret, TTL: 1}
	seed, err := cutover.NewRFC2136Traffic(dnsCfg)
	if err != nil {
		t.Fatal(err)
	}
	markerValue, err := seed.MarkerValue(7)
	if err != nil {
		t.Fatal(err)
	}
	named := fmt.Sprintf(`options { directory "/tmp"; listen-on port 5353 { any; }; listen-on-v6 { none; }; recursion no; dnssec-validation no; notify no; }; controls { };
key "%s." { algorithm hmac-sha256; secret %q; };
zone "%s." { type primary; file "zone.db"; update-policy { grant %s. name %s. A AAAA; grant %s. name %s. TXT; }; };`, key, secret, zone, key, host, key, marker)
	db := fmt.Sprintf(`$ORIGIN %s.
$TTL 1
@ IN SOA ns.%s. hostmaster.%s. 1 60 60 600 30
@ IN NS ns.%s.
ns IN A 192.0.2.53
%s. IN A 192.0.2.10
%s. IN TXT %q
`, zone, zone, zone, zone, host, marker, markerValue)
	docker := func(ctx context.Context, args ...string) string {
		t.Helper()
		result, err := exec.CommandContext(ctx, "docker", args...).Output()
		if err != nil {
			t.Fatalf("Docker %s failed: %v", args[0], err)
		}
		return strings.TrimSpace(string(result))
	}
	const image = "internetsystemsconsortium/bind9:9.20@sha256:978201d91548ec63c3b1c1befd5e6a3c9fa127f79f9058fd82d8e3493fe90ce1"
	cid := docker(ctx, "run", "--rm", "-d", "--user", "bind", "-p", "127.0.0.1::5353/tcp", "--entrypoint", "/bin/sh", image, "-ec", `umask 077; printf '%s\n' "$1" >/tmp/test.conf; printf '%s\n' "$2" >/tmp/zone.db; exec /usr/sbin/named -g -n 1 -c /tmp/test.conf`, "sh", named, db)
	t.Cleanup(func() {
		c, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		docker(c, "rm", "-f", cid)
	})
	dnsCfg.Primary = docker(ctx, "port", cid, "5353/tcp")
	traffic, err := cutover.NewRFC2136Traffic(dnsCfg)
	if err != nil {
		t.Fatal(err)
	}
	ready, stop := context.WithTimeout(ctx, 12*time.Second)
	defer stop()
	for {
		if _, err = traffic.InspectTraffic(ready, []string{host}); err == nil {
			break
		}
		select {
		case <-ready.Done():
			t.Fatalf("BIND did not become ready: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}

	var failReadback atomic.Bool
	var sourceWrites, targetWrites atomic.Int32
	type fixtureNode struct {
		store   *store.Store
		route   domain.Route
		profile map[string]any
		job     map[string]any
	}
	node := func(id, ip string, phase store.PublicationPhase, writes *atomic.Int32) fixtureNode {
		mgmt := id + ".cli.test"
		state, err := store.Open(filepath.Join(dir, id+".sqlite"), store.Options{PortRangeStart: 43500, PortRangeEnd: 43550})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { state.Close() })
		token, _, err := state.IssueEnrollmentToken(ctx, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		agent, _, err := state.ConsumeEnrollmentToken(ctx, token, id)
		if err != nil {
			t.Fatal(err)
		}
		row, err := state.CreateRoute(ctx, domain.Route{ClientID: agent.ID, Name: "app", Domain: host, Protocol: domain.ProtocolHTTPS, LocalHost: "127.0.0.1", LocalPort: 9000, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := state.TransitionPublicationGates(ctx, mgmt, []store.PublicationGateExpectation{{Host: host}}, store.PublicationPolicy{Phase: phase, ClientID: agent.ID, OperationID: operation}); err != nil {
			t.Fatal(err)
		}
		token = id + "-test-only"
		write(id+".token", []byte(token))
		handler := api.New(state, api.Config{AdminToken: token, PublicHost: mgmt, EnableCutover: true})
		previewHost := id + "-preview.example.com"
		tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host == previewHost && r.TLS.ServerName == previewHost && r.URL.Path == "/canary" {
				w.Header().Set(cutover.HTTPSProbeNodeHeader, id)
				w.Header().Set(cutover.HTTPSProbeRouteHeader, "preview-"+row.ID)
				fmt.Fprint(w, body)
				return
			}
			if r.URL.Path == "/api/v1/cutover/gates/compare" {
				writes.Add(1)
				handler.ServeHTTP(w, r)
				if id == "source" && writes.Load() == 1 {
					failReadback.Store(true)
				}
				return
			}
			if id == "source" && failReadback.Load() {
				http.Error(w, "fixture readback interruption", http.StatusServiceUnavailable)
				return
			}
			handler.ServeHTTP(w, r)
		}))
		t.Cleanup(tlsServer.Close)
		write(id+".pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsServer.Certificate().Raw}))
		response := map[string]any{"status": 200, "sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(body)))}
		return fixtureNode{state, row, map[string]any{"id": id, "profile": map[string]any{"url": tlsServer.URL, "management_host": mgmt, "token_file": id + ".token", "ca_file": id + ".pem"}, "client_id": agent.ID, "generations": map[string]int{host: 1}, "addresses": []string{ip}}, map[string]any{
			"node_id": id, "route": cutover.MappingIdentity{ID: row.ID, Host: row.Domain, PathPrefix: row.PathPrefix, LocalHost: row.LocalHost, TunnelGroup: row.TunnelGroup, LocalPort: row.LocalPort, RemotePort: row.RemotePort, DesiredRevision: row.DesiredRevision, Enabled: true},
			"path": "/canary", "preview_host": previewHost, "preview_route_id": "preview-" + row.ID, "preview_address": strings.TrimPrefix(tlsServer.URL, "https://"), "public": response, "preview": response,
		}}
	}
	source := node("source", "192.0.2.10", store.PublicationFrozen, &sourceWrites)
	target := node("target", "192.0.2.11", store.PublicationBlocked, &targetWrites)
	config := map[string]any{"enabled": true, "operation_id": operation, "journal_dir": "journal", "hosts": []string{host}, "source": source.profile, "target": target.profile,
		"dns":   map[string]any{"primary": dnsCfg.Primary, "zone": zone, "marker": marker, "key_name": key, "secret_file": "dns.key", "ttl": 1},
		"https": map[string]any{"public_ca_file": "source.pem", "preview_ca_file": "target.pem", "routes": []any{source.job, target.job}},
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	write("switch.json", data)
	exe := filepath.Join(dir, "portloomctl")
	if output, err := exec.CommandContext(ctx, "go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, output)
	}
	invoke := func(verb, digest string, wantCode int, want string) string {
		t.Helper()
		args := []string{verb, "--operation", operation}
		if verb == "status" {
			args = append(args, "--journal-dir", filepath.Join(dir, "journal"))
		} else {
			args = append(args, "--config", filepath.Join(dir, "switch.json"))
		}
		if digest != "" {
			args = append(args, "--confirm", digest)
		}
		output, err := exec.CommandContext(ctx, exe, args...).CombinedOutput()
		got := 0
		if e, ok := err.(*exec.ExitError); ok {
			got = e.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if got != wantCode || !strings.Contains(string(output), want) {
			t.Fatalf("%s: exit %d, want %d and %q: %s", verb, got, wantCode, want, output)
		}
		for _, private := range []string{secret, "source-test-only", "target-test-only"} {
			if strings.Contains(string(output), private) {
				t.Fatalf("%s leaked fixture secret", verb)
			}
		}
		return string(output)
	}
	confirmation := func() string {
		t.Helper()
		var report struct{ Confirmation string }
		if err := json.Unmarshal([]byte(invoke("check", "", 0, `"confirmation"`)), &report); err != nil {
			t.Fatal(err)
		}
		return report.Confirmation
	}
	digest := confirmation()
	invoke("switch", strings.Repeat("0", 64), 2, "confirmation does not match")
	if sourceWrites.Load() != 0 || targetWrites.Load() != 0 {
		t.Fatal("unconfirmed operation reached a gate write")
	}
	invoke("switch", digest, 1, "readback for block_source")
	invoke("status", "", 0, `"pending": "block_source"`)
	// Prove the failed readback obscured a real committed write, not a stub.
	gate, err := source.store.GetPublicationGate(ctx, host)
	if err != nil || gate.Generation != 2 || gate.Phase != store.PublicationBlocked {
		t.Fatalf("source CAS did not commit: %+v: %v", gate, err)
	}
	failReadback.Store(false)
	write("switch.json", append(append([]byte{}, data...), '\n'))
	drift := confirmation()
	invoke("settle", drift, 1, "configuration differs")
	invoke("status", "", 0, `"pending": "block_source"`)
	write("switch.json", data)
	invoke("settle", digest, 0, `"status": "source_blocked"`)
	invoke("status", "", 0, `"status": "source_blocked"`)
	targetGate, err := target.store.GetPublicationGate(ctx, host)
	if err != nil || targetGate.Generation != 1 || targetGate.Phase != store.PublicationBlocked || sourceWrites.Load() != 1 || targetWrites.Load() != 0 {
		t.Fatalf("settlement replayed or advanced: source writes=%d target writes=%d target gate=%+v err=%v", sourceWrites.Load(), targetWrites.Load(), targetGate, err)
	}
	state, err := traffic.InspectTraffic(ctx, []string{host})
	if err != nil || state.Generation != 7 || len(state.Addresses) != 1 || state.Addresses[0].String() != "192.0.2.10" {
		t.Fatalf("settlement moved DNS: %+v: %v", state, err)
	}
	t.Log("CLI persisted an ambiguous gate, refused config drift, and adopted exactly one committed CAS; target and authoritative DNS were unchanged")
}
