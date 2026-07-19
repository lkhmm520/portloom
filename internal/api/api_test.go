package api

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func TestRoutesCannotUseControlPlaneDomain(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateEnrollmentToken(context.Background(), "enroll", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	agent, _, err := s.ConsumeEnrollmentToken(context.Background(), "enroll", "nas-one")
	if err != nil {
		t.Fatal(err)
	}
	handler := New(s, Config{AdminToken: "admin-secret", PublicHost: "loom.example.com"})
	reserved := map[string]any{
		"client_id": agent.ID, "name": "reserved", "protocol": "http",
		"domain": "loom.example.com", "local_host": "127.0.0.1", "local_port": 8080, "enabled": true,
	}
	created := performJSON(t, handler, http.MethodPost, "/api/v1/routes", reserved, "admin-secret")
	if created.Code != http.StatusConflict || !bytes.Contains(created.Body.Bytes(), []byte("reserved_domain")) {
		t.Fatalf("reserved create status=%d body=%s", created.Code, created.Body.String())
	}
	reserved["domain"] = "app.example.com"
	created = performJSON(t, handler, http.MethodPost, "/api/v1/routes", reserved, "admin-secret")
	if created.Code != http.StatusCreated {
		t.Fatalf("allowed create status=%d body=%s", created.Code, created.Body.String())
	}
	var route domain.Route
	decodeResponse(t, created, &route)
	route.Domain = "loom.example.com"
	updated := performJSON(t, handler, http.MethodPut, "/api/v1/routes/"+route.ID, route, "admin-secret")
	if updated.Code != http.StatusConflict || !bytes.Contains(updated.Body.Bytes(), []byte("reserved_domain")) {
		t.Fatalf("reserved update status=%d body=%s", updated.Code, updated.Body.String())
	}
}

func TestTCPRouteRequiresEnabledEdgeAndNonReservedPort(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateEnrollmentToken(context.Background(), "enroll", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	agent, _, err := s.ConsumeEnrollmentToken(context.Background(), "enroll", "nas-one")
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"client_id": agent.ID, "name": "ssh", "protocol": "tcp", "public_port": 24443,
		"local_host": "127.0.0.1", "local_port": 22, "tunnel_group": "tcp", "enabled": true,
	}
	disabled := New(s, Config{AdminToken: "admin-secret"})
	response := performJSON(t, disabled, http.MethodPost, "/api/v1/routes", payload, "admin-secret")
	if response.Code != http.StatusConflict || !bytes.Contains(response.Body.Bytes(), []byte("tcp_edge_disabled")) {
		t.Fatalf("disabled TCP edge status=%d body=%s", response.Code, response.Body.String())
	}

	reserved := New(s, Config{AdminToken: "admin-secret", TCPEnabled: true, TCPPortReserved: func(port int) bool { return port == 24443 }})
	response = performJSON(t, reserved, http.MethodPost, "/api/v1/routes", payload, "admin-secret")
	if response.Code != http.StatusConflict || !bytes.Contains(response.Body.Bytes(), []byte("reserved_tcp_port")) {
		t.Fatalf("reserved TCP port status=%d body=%s", response.Code, response.Body.String())
	}

	enabled := New(s, Config{
		AdminToken: "admin-secret", TCPEnabled: true,
		TCPPortReserved: func(int) bool { return false },
		RoutePublicStatus: func(domain.Route) string { return "waiting_agent" },
	})
	response = performJSON(t, enabled, http.MethodPost, "/api/v1/routes", payload, "admin-secret")
	if response.Code != http.StatusCreated {
		t.Fatalf("enabled TCP edge status=%d body=%s", response.Code, response.Body.String())
	}
	var route domain.Route
	decodeResponse(t, response, &route)
	if route.Protocol != domain.ProtocolTCP || route.PublicPort != 24443 || route.PublicStatus != "waiting_agent" {
		t.Fatalf("created route=%#v", route)
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

func TestTLSCertificateAuthorizationAllowsOnlyConfiguredHTTPHosts(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateEnrollmentToken(context.Background(), "enroll", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	agent, _, err := s.ConsumeEnrollmentToken(context.Background(), "enroll", "nas")
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []domain.Route{
		{ClientID: agent.ID, Name: "app", Protocol: domain.ProtocolHTTPS, Domain: "app.example.com", LocalHost: "127.0.0.1", LocalPort: 8080, TunnelGroup: "web", Enabled: true},
		{ClientID: agent.ID, Name: "off", Protocol: domain.ProtocolHTTPS, Domain: "off.example.com", LocalHost: "127.0.0.1", LocalPort: 8081, TunnelGroup: "web", Enabled: false},
		{ClientID: agent.ID, Name: "plain", Protocol: domain.ProtocolHTTP, Domain: "plain.example.com", LocalHost: "127.0.0.1", LocalPort: 8082, TunnelGroup: "web", Enabled: true},
		{ClientID: agent.ID, Name: "tcp", Protocol: domain.ProtocolTCP, PublicPort: 24443, LocalHost: "127.0.0.1", LocalPort: 8443, TunnelGroup: "tcp", Enabled: true},
	} {
		if _, err := s.CreateRoute(context.Background(), route); err != nil {
			t.Fatal(err)
		}
	}
	config := Config{AdminToken: "admin", PublicHost: "loom.example.com", TLSAskToken: "ask-secret"}
	handler := NewTLSAskHandler(s, config)
	for _, test := range []struct {
		name, path string
		want       int
	}{
		{"admin host", "/api/v1/tls/allow?domain=LOOM.example.com.&token=ask-secret", http.StatusOK},
		{"enabled HTTPS route", "/api/v1/tls/allow?domain=app.example.com&token=ask-secret", http.StatusOK},
		{"disabled route", "/api/v1/tls/allow?domain=off.example.com&token=ask-secret", http.StatusForbidden},
		{"plain-HTTP route gets no certificate", "/api/v1/tls/allow?domain=plain.example.com&token=ask-secret", http.StatusForbidden},
		{"unknown route", "/api/v1/tls/allow?domain=unknown.example.com&token=ask-secret", http.StatusForbidden},
		{"wrong ask token", "/api/v1/tls/allow?domain=app.example.com&token=wrong", http.StatusUnauthorized},
		{"empty domain", "/api/v1/tls/allow?domain=&token=ask-secret", http.StatusBadRequest},
		{"domain with port", "/api/v1/tls/allow?domain=loom.example.com%3A443&token=ask-secret", http.StatusBadRequest},
		{"IP domain", "/api/v1/tls/allow?domain=127.0.0.1&token=ask-secret", http.StatusBadRequest},
		{"abbreviated IP domain", "/api/v1/tls/allow?domain=127.1&token=ask-secret", http.StatusBadRequest},
		{"integer IP domain", "/api/v1/tls/allow?domain=2130706433&token=ask-secret", http.StatusBadRequest},
		{"single-label domain", "/api/v1/tls/allow?domain=localhost&token=ask-secret", http.StatusBadRequest},
		{"numeric TLD domain", "/api/v1/tls/allow?domain=service.123&token=ask-secret", http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := performJSON(t, handler, http.MethodGet, test.path, nil, "")
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.want, response.Body.String())
			}
		})
	}
	publicResponse := performJSON(t, New(s, config), http.MethodGet, "/api/v1/tls/allow?domain=app.example.com&token=ask-secret", nil, "")
	if publicResponse.Code != http.StatusNotFound {
		t.Fatalf("TLS ask endpoint exposed by public API: status=%d", publicResponse.Code)
	}
}

func TestAgentEnrollmentClaimRetriesIdempotently(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateEnrollmentToken(context.Background(), "claim-token", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	handler := New(s, Config{AdminToken: "admin-secret"})
	requestID, agentToken := strings.Repeat("a", 64), strings.Repeat("b", 64)
	payload := map[string]string{"token": "claim-token", "name": "nas", "request_id": requestID, "agent_token": agentToken}
	first := performJSON(t, handler, http.MethodPost, "/api/v1/agent/enroll", payload, "")
	if first.Code != http.StatusCreated {
		t.Fatalf("first claim=%d %s", first.Code, first.Body.String())
	}
	retry := performJSON(t, handler, http.MethodPost, "/api/v1/agent/enroll", payload, "")
	if retry.Code != http.StatusCreated {
		t.Fatalf("retry claim=%d %s", retry.Code, retry.Body.String())
	}
	var firstBody, retryBody struct {
		Agent domain.Agent `json:"agent"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(retry.Body.Bytes(), &retryBody); err != nil {
		t.Fatal(err)
	}
	if firstBody.Agent.ID == "" || retryBody.Agent.ID != firstBody.Agent.ID {
		t.Fatalf("claim agents=%q/%q", firstBody.Agent.ID, retryBody.Agent.ID)
	}
	payload["request_id"] = strings.Repeat("c", 64)
	conflict := performJSON(t, handler, http.MethodPost, "/api/v1/agent/enroll", payload, "")
	if conflict.Code != http.StatusUnauthorized {
		t.Fatalf("different request status=%d", conflict.Code)
	}
	if authenticated, err := s.AuthenticateAgent(context.Background(), agentToken); err != nil || authenticated.ID != firstBody.Agent.ID {
		t.Fatalf("claimed token authentication: %#v %v", authenticated, err)
	}
}
