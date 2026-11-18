package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPlanBlocksWholeHostWhenInventoryDisagrees(t *testing.T) {
	dir := t.TempDir()
	source := apiFixture(t, "source-secret", `[{"id":"old-client"},{"id":"excluded"}]`, `[
  {"id":"r1","client_id":"old-client","domain":"console.example.net","protocol":"https","local_host":"127.0.0.1","local_port":80,"enabled":true},
  {"id":"r2","client_id":"old-client","domain":"media.example.net","protocol":"https","public_port":8443,"local_host":"127.0.0.1","local_port":80,"enabled":false},
  {"id":"r3","client_id":"excluded","domain":"media.example.net","path_prefix":"/path","protocol":"http","local_host":"127.0.0.1","local_port":80,"enabled":true}
 ]`)
	target := apiFixture(t, "target-secret", `[]`, `[{"id":"conflict","client_id":"other","domain":"media.example.net","protocol":"http","path_prefix":"/different","enabled":false}]`)
	writeProfile(t, dir, "old", source, "source-secret", "console.example.net")
	writeProfile(t, dir, "new", target, "target-secret", "new.example.net")
	var out, stderr bytes.Buffer
	if code := run(context.Background(), argsFor(dir, "console.example.net", "media.example.net", "missing.example.net"), &out, &stderr); code != 1 {
		t.Fatalf("exit %d: %s %s", code, &out, &stderr)
	}
	for _, want := range []string{`"status": "blocked"`, `"id": "r1"`, `"id": "r2"`, `"id": "r3"`, `"route_id": "conflict"`, `"code": "management_host"`, `"code": "target_host_owned"`, `"code": "other_source_owner"`, `"code": "unsupported_public_endpoint"`, `"code": "target_client_not_found"`, `"code": "source_host_not_found_for_client"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %s in %s", want, &out)
		}
	}
}

func TestPlanDoesNotReplayTokenOnSameOriginRedirect(t *testing.T) {
	dir := t.TempDir()
	var requests atomic.Int32
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			requests.Add(1)
			return
		}
		w.Header().Set("Location", "/redirected?never-print-me")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	target := apiFixture(t, "target-secret", `[]`, `[]`)
	writeProfile(t, dir, "old", source, "source-secret", "old.example.net")
	writeProfile(t, dir, "new", target, "target-secret", "new.example.net")
	var out, stderr bytes.Buffer
	if code := run(context.Background(), argsFor(dir, "media.example.net"), &out, &stderr); code != 1 || requests.Load() != 0 || out.Len() != 0 || !strings.Contains(stderr.String(), "HTTP 307") || strings.Contains(stderr.String(), "never-print-me") {
		t.Fatalf("exit=%d followed=%d stdout=%s stderr=%s", code, requests.Load(), &out, &stderr)
	}
}

func TestPlanRejectsPublicTokenFileBeforeNetworking(t *testing.T) {
	dir := t.TempDir()
	never := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("made request before validating token file") }))
	defer never.Close()
	writeProfile(t, dir, "old", never, "source-secret", "old.example.net")
	writeProfile(t, dir, "new", never, "target-secret", "new.example.net")
	if err := os.Chmod(filepath.Join(dir, "old.token"), 0644); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := run(context.Background(), argsFor(dir, "media.example.net"), &out, &stderr); code != 2 || !strings.Contains(stderr.String(), "private regular file") || strings.Contains(stderr.String(), "source-secret") || out.Len() != 0 {
		t.Fatalf("exit %d stdout=%s stderr=%s", code, &out, &stderr)
	}
}

func TestApplyIsNotExposed(t *testing.T) {
	if code := run(context.Background(), []string{"apply"}, new(bytes.Buffer), new(bytes.Buffer)); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

func TestInvalidGroupInputDoesNotReadInventories(t *testing.T) {
	dir := t.TempDir()
	var requests atomic.Int32
	respond := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) })
	source := httptest.NewTLSServer(respond)
	defer source.Close()
	target := httptest.NewTLSServer(respond)
	defer target.Close()
	writeProfile(t, dir, "old", source, "source-secret", "old.example.net")
	writeProfile(t, dir, "new", target, "target-secret", "new.example.net")
	path := filepath.Join(dir, "profiles.json")
	config, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const valid = `{"media":{"hosts":["media.example.net"]}}`
	for _, tc := range []struct {
		name, groups, diagnostic string
		args                     []string
	}{
		{"missing selection", valid, "either one --group", nil},
		{"unknown group", valid, "selected group does not exist", []string{"--group", "never-print-me"}},
		{"invalid group selector", valid, "invalid group name", []string{"--group", "never-print-me/"}},
		{"mixed selectors", valid, "either one --group", []string{"--group", "media", "--host", "never-print-me.example.net"}},
		{"repeated group", valid, "Usage:", []string{"--group", "media", "--group", "never-print-me"}},
		{"empty group flag", valid, "Usage:", []string{"--group=", "--host", "media.example.net"}},
		{"bad name", `{"never-print-me/":{"hosts":["media.example.net"]}}`, "invalid group name", []string{"--host", "media.example.net"}},
		{"empty host set", `{"media":{"hosts":[]}}`, "at least one exact DNS hostname", []string{"--group", "media"}},
		{"null group", `{"media":null}`, "at least one exact DNS hostname", []string{"--group", "media"}},
		{"non-host in unused group", `{"media":{"hosts":["media.example.net"]},"unused":{"hosts":["*.never-print-me.example.net"]}}`, "expected an exact DNS hostname", []string{"--group", "media"}},
		{"duplicate group", `{"media":{"hosts":["media.example.net"]},"media":{"hosts":["never-print-me.example.net"]}}`, "duplicate JSON keys", []string{"--group", "media"}},
		{"duplicate hosts field", `{"media":{"hosts":["media.example.net"],"HOSTS":["never-print-me.example.net"]}}`, "duplicate JSON keys", []string{"--group", "media"}},
		{"embedded secret", `{"media":{"hosts":["media.example.net"],"token":"never-print-me"}}`, "expected a JSON config", []string{"--group", "media"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustWrite(t, path, fmt.Sprintf(`%s,"groups":%s}`, strings.TrimSuffix(string(config), "}"), tc.groups))
			var out, stderr bytes.Buffer
			if code := run(context.Background(), append(argsFor(dir), tc.args...), &out, &stderr); code != 2 || out.Len() != 0 || requests.Load() != 0 || !strings.Contains(stderr.String(), tc.diagnostic) || strings.Contains(stderr.String(), "never-print-me") {
				t.Fatalf("exit=%d requests=%d stdout=%s stderr=%s", code, requests.Load(), &out, &stderr)
			}
		})
	}
}

func argsFor(dir string, hosts ...string) []string {
	args := []string{"plan", "--config", filepath.Join(dir, "profiles.json"), "--source", "old", "--target", "new", "--source-client", "old-client", "--target-client", "new-client"}
	for _, host := range hosts {
		args = append(args, "--host", host)
	}
	return args
}
