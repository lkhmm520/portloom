package api

import (
	"net/http"
	"testing"
)

func TestUIAndAgentCompatibilityEndpoints(t *testing.T) {
	s := openTestStore(t)
	handler := New(s, Config{AdminToken: "admin-secret"})

	issued := performJSON(t, handler, http.MethodPost, "/api/v1/enrollment-tokens", map[string]any{"expires_in": "1h"}, "admin-secret")
	if issued.Code != http.StatusCreated {
		t.Fatalf("issue status=%d body=%s", issued.Code, issued.Body.String())
	}
	var secret struct {
		Token string `json:"token"`
	}
	decodeResponse(t, issued, &secret)

	enrolled := performJSON(t, handler, http.MethodPost, "/api/v1/agent/enroll", map[string]any{"token": secret.Token, "name": "nas"}, "")
	if enrolled.Code != http.StatusCreated {
		t.Fatalf("enroll status=%d body=%s", enrolled.Code, enrolled.Body.String())
	}
	var credentials struct {
		Token string `json:"token"`
		Agent struct {
			ID string `json:"id"`
		} `json:"agent"`
	}
	decodeResponse(t, enrolled, &credentials)

	clients := performJSON(t, handler, http.MethodGet, "/api/v1/clients", nil, "admin-secret")
	if clients.Code != http.StatusOK {
		t.Fatalf("clients status=%d body=%s", clients.Code, clients.Body.String())
	}
	tokens := performJSON(t, handler, http.MethodGet, "/api/v1/enrollment-tokens", nil, "admin-secret")
	if tokens.Code != http.StatusOK {
		t.Fatalf("tokens status=%d body=%s", tokens.Code, tokens.Body.String())
	}

	created := performJSON(t, handler, http.MethodPost, "/api/v1/routes", map[string]any{
		"client_id": credentials.Agent.ID, "name": "app", "protocol": "http", "domain": "app.example.com",
		"local_host": "127.0.0.1", "local_port": 8080, "tunnel_group": "web", "enabled": true,
	}, "admin-secret")
	if created.Code != http.StatusCreated {
		t.Fatalf("route status=%d body=%s", created.Code, created.Body.String())
	}

	desired := performJSON(t, handler, http.MethodGet, "/api/v1/agent/desired", nil, credentials.Token)
	if desired.Code != http.StatusOK {
		t.Fatalf("desired status=%d body=%s", desired.Code, desired.Body.String())
	}
	observed := performJSON(t, handler, http.MethodPost, "/api/v1/agent/observed", map[string]any{
		"revision": 1, "routes": []map[string]any{{"route_id": "missing-is-ignored", "local_status": "up", "tunnel_status": "up"}},
	}, credentials.Token)
	if observed.Code != http.StatusNoContent {
		t.Fatalf("observed status=%d body=%s", observed.Code, observed.Body.String())
	}
	futureObserved := performJSON(t, handler, http.MethodPost, "/api/v1/agent/observed", map[string]any{
		"revision": 2, "routes": []map[string]any{},
	}, credentials.Token)
	if futureObserved.Code != http.StatusBadRequest {
		t.Fatalf("future observed status=%d body=%s", futureObserved.Code, futureObserved.Body.String())
	}
	futureHeartbeat := performJSON(t, handler, http.MethodPost, "/api/v1/agent/heartbeat", map[string]any{
		"observed_revision": 2, "routes": []map[string]any{},
	}, credentials.Token)
	if futureHeartbeat.Code != http.StatusBadRequest {
		t.Fatalf("future heartbeat status=%d body=%s", futureHeartbeat.Code, futureHeartbeat.Body.String())
	}
}

func TestEnrollmentTokenCompatibilityEndpointRejectsRemovedNameField(t *testing.T) {
	s := openTestStore(t)
	handler := New(s, Config{AdminToken: "admin-secret"})

	issued := performJSON(t, handler, http.MethodPost, "/api/v1/enrollment-tokens", map[string]any{
		"name":       "legacy-label",
		"expires_in": "1h",
	}, "admin-secret")
	if issued.Code != http.StatusBadRequest {
		t.Fatalf("issue status=%d body=%s", issued.Code, issued.Body.String())
	}
	var failure struct {
		Error string `json:"error"`
	}
	decodeResponse(t, issued, &failure)
	if failure.Error != "invalid_request" {
		t.Fatalf("error=%q, want invalid_request", failure.Error)
	}
}
