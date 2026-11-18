package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/api"
	"github.com/lkhmm520/portloom/internal/cutover"
	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/store"
)

func TestPlanAgainstTwoPortLoomAPIHandlers(t *testing.T) {
	dir := t.TempDir()
	source, oldDB, oldClient := portLoomFixture(t, "old", "source-secret")
	target, newDB, newClient := portLoomFixture(t, "new", "target-secret")
	ctx := context.Background()
	route, err := oldDB.CreateRoute(ctx, domain.Route{ClientID: oldClient.ID, Name: "photos", Domain: "photos.example.net", Protocol: domain.ProtocolHTTPS, PathPrefix: "/album", StripPath: true, LocalHost: "::1", LocalPort: 8080, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	writeProfile(t, dir, "old", source, "source-secret", "old.example.net")
	writeProfile(t, dir, "new", target, "target-secret", "new.example.net")
	configPath := filepath.Join(dir, "profiles.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, configPath, strings.TrimSuffix(string(data), "}")+`,"groups":{"media":{"hosts":["PHOTOS.example.net.","photos.example.net"]},"other":{"hosts":["unselected.example.net"]}}}`)
	args := []string{"plan", "--config", configPath, "--source", "old", "--target", "new", "--source-client", oldClient.ID, "--target-client", newClient.ID}
	for _, mode := range []struct{ flag, value, group string }{
		{"--host", route.Domain, ""},
		{"--group", "media", "media"},
	} {
		t.Run(mode.flag, func(t *testing.T) {
			var out, stderr bytes.Buffer
			if code := run(ctx, append(slices.Clone(args), mode.flag, mode.value), &out, &stderr); code != 0 {
				t.Fatalf("exit %d stdout=%s stderr=%s", code, &out, &stderr)
			}
			var report cutover.Report
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Status != "preview" || !slices.Equal(report.Hosts, []string{route.Domain}) || len(report.Routes) != 1 || report.Routes[0].ID != route.ID || report.Routes[0].LocalTarget != "[::1]:8080" || report.Routes[0].PathPrefix != "/album" || !report.Routes[0].StripPath {
				t.Fatalf("unexpected report: %+v", report)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(out.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			if mode.group == "" && fields["group"] != nil || mode.group != "" && string(fields["group"]) != `"media"` {
				t.Fatalf("unexpected group provenance: %s", fields["group"])
			}
		})
	}
	if routes, err := oldDB.ListRoutes(ctx); err != nil || len(routes) != 1 || routes[0] != route {
		t.Fatalf("source inventory changed: %v %v", routes, err)
	}
	if routes, err := newDB.ListRoutes(ctx); err != nil || len(routes) != 0 {
		t.Fatalf("target inventory changed: %v %v", routes, err)
	}
}

func portLoomFixture(t *testing.T, name, token string) (*httptest.Server, *store.Store, domain.Agent) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "portloom.db"), store.Options{PortRangeStart: 42000, PortRangeEnd: 42099})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	enrollment, _, err := db.IssueEnrollmentToken(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	client, _, err := db.ConsumeEnrollmentToken(ctx, enrollment, name)
	if err != nil {
		t.Fatal(err)
	}
	api := api.New(db, api.Config{AdminToken: token})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || (r.URL.Path != "/api/v1/clients" && r.URL.Path != "/api/v1/routes") {
			t.Error("unexpected request")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		api.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server, db, client
}
