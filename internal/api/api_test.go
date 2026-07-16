package api

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/store"
)

func TestAdminBearerIssuesOneTimeEnrollmentToken(t *testing.T) {
	s := openTestStore(t)
	handler := New(s, Config{AdminToken: "admin-secret", EnrollmentTTL: time.Hour})

	unauthorized := performJSON(t, handler, http.MethodPost, "/api/v1/admin/enrollment-tokens", nil, "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	issued := performJSON(t, handler, http.MethodPost, "/api/v1/admin/enrollment-tokens", nil, "admin-secret")
	if issued.Code != http.StatusCreated {
		t.Fatalf("issue status = %d body=%s", issued.Code, issued.Body.String())
	}
	var issueBody struct {
		Token string `json:"token"`
	}
	decodeResponse(t, issued, &issueBody)
	if issueBody.Token == "" {
		t.Fatal("empty enrollment token")
	}

	enrollBody := map[string]any{"token": issueBody.Token, "name": "nas-one"}
	enrolled := performJSON(t, handler, http.MethodPost, "/api/v1/agent/enroll", enrollBody, "")
	if enrolled.Code != http.StatusCreated {
		t.Fatalf("enroll status = %d body=%s", enrolled.Code, enrolled.Body.String())
	}
	var credentials struct {
		Token string `json:"token"`
		Agent struct {
			ID string `json:"id"`
		} `json:"agent"`
	}
	decodeResponse(t, enrolled, &credentials)
	if credentials.Token == "" || credentials.Agent.ID == "" {
		t.Fatalf("missing credentials: %+v", credentials)
	}

	reused := performJSON(t, handler, http.MethodPost, "/api/v1/agent/enroll", enrollBody, "")
	if reused.Code != http.StatusUnauthorized {
		t.Fatalf("reused token status = %d body=%s", reused.Code, reused.Body.String())
	}
	sync := performJSON(t, handler, http.MethodGet, "/api/v1/agent/sync", nil, credentials.Token)
	if sync.Code != http.StatusOK {
		t.Fatalf("sync status = %d body=%s", sync.Code, sync.Body.String())
	}
}

func TestEnrollmentTokenExpiryValidation(t *testing.T) {
	tests := []struct {
		name   string
		body   map[string]any
		status int
	}{
		{name: "zero seconds", body: map[string]any{"expires_in_seconds": 0}, status: http.StatusBadRequest},
		{name: "negative seconds", body: map[string]any{"expires_in_seconds": -1}, status: http.StatusBadRequest},
		{name: "null seconds", body: map[string]any{"expires_in_seconds": nil}, status: http.StatusBadRequest},
		{name: "overflow seconds", body: map[string]any{"expires_in_seconds": int64(math.MaxInt64)}, status: http.StatusBadRequest},
		{name: "empty duration", body: map[string]any{"expires_in": ""}, status: http.StatusBadRequest},
		{name: "null duration", body: map[string]any{"expires_in": nil}, status: http.StatusBadRequest},
		{name: "duration exceeds limit", body: map[string]any{"expires_in": "721h"}, status: http.StatusBadRequest},
		{name: "ambiguous fields", body: map[string]any{"expires_in": "1h", "expires_in_seconds": 3600}, status: http.StatusBadRequest},
		{name: "empty duration with seconds", body: map[string]any{"expires_in": "", "expires_in_seconds": 3600}, status: http.StatusBadRequest},
		{name: "null duration with seconds", body: map[string]any{"expires_in": nil, "expires_in_seconds": 3600}, status: http.StatusBadRequest},
		{name: "duration with null seconds", body: map[string]any{"expires_in": "1h", "expires_in_seconds": nil}, status: http.StatusBadRequest},
		{name: "maximum duration", body: map[string]any{"expires_in": "720h"}, status: http.StatusCreated},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := New(openTestStore(t), Config{AdminToken: "admin-secret"})
			response := performJSON(t, handler, http.MethodPost, "/api/v1/enrollment-tokens", test.body, "admin-secret")
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
		})
	}
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"), store.Options{PortRangeStart: 33000, PortRangeEnd: 33005})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestAdminRouteCRUDAndAgentHeartbeat(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateEnrollmentToken(context.Background(), "enroll", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	agent, agentToken, err := s.ConsumeEnrollmentToken(context.Background(), "enroll", "nas-one")
	if err != nil {
		t.Fatal(err)
	}
	handler := New(s, Config{AdminToken: "admin-secret"})

	create := performJSON(t, handler, http.MethodPost, "/api/v1/admin/routes", map[string]any{
		"client_id": agent.ID, "name": "app", "protocol": "http",
		"domain": "app.example.com", "local_host": "localhost", "local_port": 8080, "enabled": true,
	}, "admin-secret")
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var route domain.Route
	decodeResponse(t, create, &route)
	if route.ID == "" || route.RemotePort != 33000 || route.DesiredRevision != 1 {
		t.Fatalf("unexpected route: %+v", route)
	}

	list := performJSON(t, handler, http.MethodGet, "/api/v1/admin/routes", nil, "admin-secret")
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", list.Code, list.Body.String())
	}
	var routes []domain.Route
	decodeResponse(t, list, &routes)
	if len(routes) != 1 || routes[0].ID != route.ID {
		t.Fatalf("unexpected routes: %+v", routes)
	}
	get := performJSON(t, handler, http.MethodGet, "/api/v1/admin/routes/"+route.ID, nil, "admin-secret")
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", get.Code, get.Body.String())
	}

	updateBody := route
	updateBody.Name = "app-v2"
	update := performJSON(t, handler, http.MethodPut, "/api/v1/admin/routes/"+route.ID, updateBody, "admin-secret")
	if update.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", update.Code, update.Body.String())
	}
	decodeResponse(t, update, &route)
	if route.Name != "app-v2" || route.DesiredRevision != 2 {
		t.Fatalf("unexpected update: %+v", route)
	}

	heartbeat := performJSON(t, handler, http.MethodPost, "/api/v1/agent/heartbeat", map[string]any{
		"observed_revision": route.DesiredRevision,
		"routes":            []map[string]any{{"route_id": route.ID, "observed_revision": route.DesiredRevision, "tunnel_status": "connected"}},
	}, agentToken)
	if heartbeat.Code != http.StatusNoContent {
		t.Fatalf("heartbeat status = %d body=%s", heartbeat.Code, heartbeat.Body.String())
	}

	deleted := performJSON(t, handler, http.MethodDelete, "/api/v1/admin/routes/"+route.ID, nil, "admin-secret")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s", deleted.Code, deleted.Body.String())
	}
}

func performJSON(t *testing.T, handler http.Handler, method, path string, body any, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	var encoded bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &encoded).WithContext(context.Background())
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}
