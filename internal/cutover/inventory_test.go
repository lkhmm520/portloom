package cutover

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestInventoryRejectsUntrustedHTTPSBeforeSendingRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	p, dir := testProfile(t, server.URL)
	client, err := newInventoryClient(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer client.http.CloseIdleConnections()
	if _, err := client.read(context.Background()); err == nil || requests.Load() != 0 {
		t.Fatalf("error = %v, HTTP requests = %d", err, requests.Load())
	}
}

func TestInventoryRejectsNonHTTPSAndSecretBearingURLs(t *testing.T) {
	for _, endpoint := range []string{"http://127.0.0.1", "https://user:secret@example.net", "https://example.net?token=secret", "https://example.net/#secret", "https://example.net/path"} {
		p, dir := testProfile(t, endpoint)
		if _, err := newInventoryClient(p, dir); !errors.Is(err, ErrInput) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe or missing error: %v", err)
		}
	}
}

func TestInventoryRejectsMalformedOrOversizedResponse(t *testing.T) {
	for _, fixture := range []struct{ name, body, want string }{
		{"null", "null", "expected a JSON inventory array"},
		{"trailing data", "[]{}", "expected a JSON inventory array"},
		{"oversized", strings.Repeat(" ", inventoryLimit+1), "oversized response"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, fixture.body) }))
			defer server.Close()
			p, dir := testProfile(t, server.URL)
			client, err := newInventoryClient(p, dir)
			if err != nil {
				t.Fatal(err)
			}
			// Use the fixture's certificate-verifying client as the transport for this
			// body test; HTTPS trust and redirect policy are exercised separately.
			client.http.Transport = server.Client().Transport
			defer client.http.CloseIdleConnections()
			if _, err := client.read(context.Background()); err == nil || !strings.Contains(err.Error(), fixture.want) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func testProfile(t *testing.T, endpoint string) (profile, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "admin.token"), []byte("fixture-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return profile{URL: endpoint, ManagementHost: "admin.example.net", TokenFile: "admin.token"}, dir
}
