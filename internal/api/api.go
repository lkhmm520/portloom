package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/store"
)

const (
	maxRequestBody   = 1 << 20
	maxEnrollmentTTL = 30 * 24 * time.Hour
)

type Config struct {
	AdminToken    string
	EnrollmentTTL time.Duration
}

type server struct {
	store  *store.Store
	config Config
}

func New(state *store.Store, config Config) http.Handler {
	if config.EnrollmentTTL <= 0 {
		config.EnrollmentTTL = time.Hour
	} else if config.EnrollmentTTL > maxEnrollmentTTL {
		config.EnrollmentTTL = maxEnrollmentTTL
	}
	s := &server{store: state, config: config}
	mux := http.NewServeMux()
	// Canonical UI endpoints.
	mux.HandleFunc("GET /api/v1/clients", s.admin(s.listClients))
	mux.HandleFunc("GET /api/v1/enrollment-tokens", s.admin(s.listEnrollmentTokens))
	mux.HandleFunc("POST /api/v1/enrollment-tokens", s.admin(s.issueEnrollmentToken))
	mux.HandleFunc("GET /api/v1/routes", s.admin(s.listRoutes))
	mux.HandleFunc("POST /api/v1/routes", s.admin(s.createRoute))
	mux.HandleFunc("GET /api/v1/routes/{id}", s.admin(s.getRoute))
	mux.HandleFunc("PUT /api/v1/routes/{id}", s.admin(s.updateRoute))
	mux.HandleFunc("DELETE /api/v1/routes/{id}", s.admin(s.deleteRoute))
	// Backward-compatible admin endpoints used by early clients and tests.
	mux.HandleFunc("POST /api/v1/admin/enrollment-tokens", s.admin(s.issueEnrollmentToken))
	mux.HandleFunc("GET /api/v1/admin/routes", s.admin(s.listRoutes))
	mux.HandleFunc("POST /api/v1/admin/routes", s.admin(s.createRoute))
	mux.HandleFunc("GET /api/v1/admin/routes/{id}", s.admin(s.getRoute))
	mux.HandleFunc("PUT /api/v1/admin/routes/{id}", s.admin(s.updateRoute))
	mux.HandleFunc("DELETE /api/v1/admin/routes/{id}", s.admin(s.deleteRoute))
	mux.HandleFunc("POST /api/v1/agent/enroll", s.enrollAgent)
	mux.HandleFunc("POST /api/v1/agent/heartbeat", s.agent(s.heartbeatAgent))
	mux.HandleFunc("GET /api/v1/agent/sync", s.agent(s.syncAgent))
	mux.HandleFunc("GET /api/v1/agent/desired", s.agent(s.syncAgent))
	mux.HandleFunc("POST /api/v1/agent/observed", s.agent(s.observedAgent))
	return mux
}

type apiHandler func(http.ResponseWriter, *http.Request)
type agentHandler func(http.ResponseWriter, *http.Request, domain.Agent)

func (s *server) admin(next apiHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !secureEqual(bearerToken(r), s.config.AdminToken) || s.config.AdminToken == "" {
			unauthorized(w)
			return
		}
		next(w, r)
	}
}

func (s *server) agent(next agentHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			unauthorized(w)
			return
		}
		agent, err := s.store.AuthenticateAgent(r.Context(), token)
		if err != nil {
			unauthorized(w)
			return
		}
		next(w, r, agent)
	}
}

func (s *server) issueEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	ttl := s.config.EnrollmentTTL
	if r.ContentLength != 0 {
		var request struct {
			ExpiresIn        json.RawMessage `json:"expires_in"`
			ExpiresInSeconds json.RawMessage `json:"expires_in_seconds"`
		}
		if err := decodeJSON(w, r, &request); err != nil {
			return
		}
		if len(request.ExpiresIn) > 0 && len(request.ExpiresInSeconds) > 0 {
			writeError(w, http.StatusBadRequest, "invalid_expiry")
			return
		}
		if len(request.ExpiresIn) > 0 {
			var value string
			if err := json.Unmarshal(request.ExpiresIn, &value); err != nil || value == "" {
				writeError(w, http.StatusBadRequest, "invalid_expiry")
				return
			}
			parsed, err := time.ParseDuration(value)
			if err != nil || parsed <= 0 || parsed > maxEnrollmentTTL {
				writeError(w, http.StatusBadRequest, "invalid_expiry")
				return
			}
			ttl = parsed
		} else if len(request.ExpiresInSeconds) > 0 {
			var seconds int64
			if err := json.Unmarshal(request.ExpiresInSeconds, &seconds); err != nil || seconds <= 0 || seconds > int64(maxEnrollmentTTL/time.Second) {
				writeError(w, http.StatusBadRequest, "invalid_expiry")
				return
			}
			ttl = time.Duration(seconds) * time.Second
		}
	}
	token, expiresAt, err := s.store.IssueEnrollmentToken(r.Context(), ttl)
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "expires_at": expiresAt})
}

func (s *server) listClients(w http.ResponseWriter, r *http.Request) {
	clients, err := s.store.ListAgents(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, clients)
}

func (s *server) listEnrollmentTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.store.ListEnrollmentTokens(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

func (s *server) listRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := s.store.ListRoutes(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, routes)
}

func (s *server) createRoute(w http.ResponseWriter, r *http.Request) {
	var route domain.Route
	if err := decodeJSON(w, r, &route); err != nil {
		return
	}
	created, err := s.store.CreateRoute(r.Context(), route)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *server) getRoute(w http.ResponseWriter, r *http.Request) {
	route, err := s.store.GetRoute(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func (s *server) updateRoute(w http.ResponseWriter, r *http.Request) {
	var route domain.Route
	if err := decodeJSON(w, r, &route); err != nil {
		return
	}
	updated, err := s.store.UpdateRoute(r.Context(), r.PathValue("id"), route)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *server) deleteRoute(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRoute(r.Context(), r.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) enrollAgent(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Token string `json:"token"`
		Name  string `json:"name"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	agent, token, err := s.store.ConsumeEnrollmentToken(r.Context(), request.Token, request.Name)
	if errors.Is(err, store.ErrInvalidEnrollmentToken) {
		unauthorized(w)
		return
	}
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"agent": agent, "token": token})
}

func (s *server) heartbeatAgent(w http.ResponseWriter, r *http.Request, agent domain.Agent) {
	var request struct {
		ObservedRevision int64                     `json:"observed_revision"`
		Routes           []domain.RouteObservation `json:"routes"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	if err := s.store.Heartbeat(r.Context(), agent.ID, request.ObservedRevision, request.Routes); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) observedAgent(w http.ResponseWriter, r *http.Request, agent domain.Agent) {
	var request struct {
		Revision int64 `json:"revision"`
		Routes   []struct {
			RouteID      string `json:"route_id"`
			LocalStatus  string `json:"local_status"`
			TunnelStatus string `json:"tunnel_status"`
			Error        string `json:"error"`
		} `json:"routes"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	observations := make([]domain.RouteObservation, 0, len(request.Routes))
	for _, route := range request.Routes {
		observations = append(observations, domain.RouteObservation{
			RouteID: route.RouteID, ObservedRevision: request.Revision,
			LocalStatus: route.LocalStatus, TunnelStatus: route.TunnelStatus, LastError: route.Error,
		})
	}
	if err := s.store.Heartbeat(r.Context(), agent.ID, request.Revision, observations); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) syncAgent(w http.ResponseWriter, r *http.Request, authenticated domain.Agent) {
	agent, routes, err := s.store.SyncAgent(r.Context(), authenticated.ID)
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agent": agent, "revision": agent.DesiredRevision, "routes": routes,
	})
}

func bearerToken(r *http.Request) string {
	value := r.Header.Get("Authorization")
	if !strings.HasPrefix(value, "Bearer ") {
		return ""
	}
	token := strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return ""
	}
	return token
}

func secureEqual(left, right string) bool {
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrPortRangeExhausted):
		writeError(w, http.StatusConflict, "conflict")
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request")
	default:
		internalError(w)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="portloom"`)
	writeError(w, http.StatusUnauthorized, "unauthorized")
}

func internalError(w http.ResponseWriter) {
	writeError(w, http.StatusInternalServerError, "internal_error")
}
