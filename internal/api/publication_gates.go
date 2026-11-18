package api

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/store"
)

const maxGateHosts = store.MaxPublicationHosts

// These wire types are deliberately independent of the store's representation.
type publicationGateJSON struct {
	Host        string                 `json:"host"`
	Generation  int64                  `json:"generation"`
	Phase       store.PublicationPhase `json:"phase"`
	OperationID string                 `json:"operation_id"`
	ClientID    string                 `json:"client_id"`
}

func (s *server) registerCutover(mux *http.ServeMux) {
	managementHost, valid := domain.NormalizeDNSHost(s.config.PublicHost)
	if !s.config.EnableCutover || !valid {
		return
	}
	mux.HandleFunc("POST /api/v1/cutover/gates/read", s.admin(func(w http.ResponseWriter, r *http.Request) {
		readPublicationGates(w, r, s.store, managementHost)
	}))
	mux.HandleFunc("POST /api/v1/cutover/gates/transition", s.admin(func(w http.ResponseWriter, r *http.Request) {
		transitionPublicationGates(w, r, managementHost, s.store.TransitionPublicationGates)
	}))
	mux.HandleFunc("POST /api/v1/cutover/gates/snapshot", s.admin(func(w http.ResponseWriter, r *http.Request) {
		readPublicationSnapshot(w, r, s.store, managementHost)
	}))
	for path, apply := range map[string]gateChange{
		"/api/v1/cutover/gates/compare": s.store.TransitionPublicationGates,
		"/api/v1/cutover/gates/refence": s.store.RefencePublicationGates,
	} {
		mux.HandleFunc("POST "+path, s.admin(func(w http.ResponseWriter, r *http.Request) {
			hosts := r.Header.Values("PortLoom-Expected-Management-Host")
			if len(hosts) != 1 || hosts[0] != managementHost {
				writeError(w, http.StatusConflict, "management_host_mismatch")
				return
			}
			transitionPublicationGates(w, r, managementHost, apply)
		}))
	}
}

func readPublicationSnapshot(w http.ResponseWriter, r *http.Request, state *store.Store, managementHost string) {
	w.Header().Set("Cache-Control", "no-store")
	var request json.RawMessage
	if decodeJSON(w, r, &request) != nil {
		return
	}
	var hosts []string
	if !gateFields(request, map[string]any{"hosts": &hosts}) || !canonicalGateHosts(hosts, managementHost) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	all, routes, err := state.PublicationSnapshot(r.Context(), hosts)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	sort.Strings(hosts)
	gates := make([]publicationGateJSON, 0, len(hosts))
	for _, host := range hosts {
		gate := all[host]
		gates = append(gates, publicationGateJSON{Host: host, Generation: gate.Generation, Phase: gate.Phase, OperationID: gate.OperationID, ClientID: gate.ClientID})
	}
	writeJSON(w, http.StatusOK, struct {
		ManagementHost string                `json:"management_host"`
		Gates          []publicationGateJSON `json:"gates"`
		Routes         []domain.Route        `json:"routes"`
	}{managementHost, gates, routes})
}

func readPublicationGates(w http.ResponseWriter, r *http.Request, state *store.Store, managementHost string) {
	w.Header().Set("Cache-Control", "no-store")
	var request json.RawMessage
	if decodeJSON(w, r, &request) != nil {
		return
	}
	var hosts []string
	if !gateFields(request, map[string]any{"hosts": &hosts}) || !canonicalGateHosts(hosts, managementHost) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	sort.Strings(hosts)
	// These observations are CAS inputs, not an atomic snapshot of all hosts.
	// The transition transaction checks every expectation against current state.
	gates := make([]publicationGateJSON, 0, len(hosts))
	for _, host := range hosts {
		gate, err := state.GetPublicationGate(r.Context(), host)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		gates = append(gates, publicationGateJSON{Host: host, Generation: gate.Generation, Phase: gate.Phase, OperationID: gate.OperationID, ClientID: gate.ClientID})
	}
	writeJSON(w, http.StatusOK, struct {
		Gates []publicationGateJSON `json:"gates"`
	}{gates})
}

type gateChange func(context.Context, string, []store.PublicationGateExpectation, store.PublicationPolicy) error

func transitionPublicationGates(w http.ResponseWriter, r *http.Request, managementHost string, apply gateChange) {
	var request json.RawMessage
	if decodeJSON(w, r, &request) != nil {
		return
	}
	expect, next, valid := parseGateTransition(request, managementHost)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := apply(r.Context(), managementHost, expect, next); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseGateTransition(request json.RawMessage, managementHost string) ([]store.PublicationGateExpectation, store.PublicationPolicy, bool) {
	var next store.PublicationPolicy
	var members []json.RawMessage
	var nextJSON json.RawMessage
	if !gateFields(request, map[string]any{"expect": &members, "next": &nextJSON}) || len(members) == 0 || len(members) > maxGateHosts ||
		!gateFields(nextJSON, map[string]any{"phase": &next.Phase, "operation_id": &next.OperationID, "client_id": &next.ClientID}) ||
		!validGatePhase(next.Phase) || !hasGateOwner(next) {
		return nil, next, false
	}
	expect := make([]store.PublicationGateExpectation, len(members))
	hosts := make([]string, len(members))
	for i, raw := range members {
		gate := &expect[i].Expected
		if !gateFields(raw, map[string]any{"host": &hosts[i], "generation": &gate.Generation, "phase": &gate.Phase, "operation_id": &gate.OperationID, "client_id": &gate.ClientID}) ||
			!validGateExpectation(*gate) {
			return nil, next, false
		}
	}
	if !canonicalGateHosts(hosts, managementHost) {
		return nil, next, false
	}
	for i, host := range hosts {
		expect[i].Host = host
	}
	return expect, next, true
}

func validGatePhase(phase store.PublicationPhase) bool {
	return phase == store.PublicationOpen || phase == store.PublicationFrozen || phase == store.PublicationBlocked
}

func hasGateOwner(policy store.PublicationPolicy) bool {
	return strings.TrimSpace(policy.OperationID) != "" && strings.TrimSpace(policy.ClientID) != ""
}

func validGateExpectation(gate store.PublicationGate) bool {
	if gate.Generation < 0 || gate.Generation == math.MaxInt64 || !validGatePhase(gate.Phase) {
		return false
	}
	if gate.Phase == store.PublicationOpen {
		return gate.OperationID == "" && gate.ClientID == ""
	}
	return gate.Generation > 0 && hasGateOwner(gate.PublicationPolicy)
}

// canonicalGateHosts validates the whole group before any store call.
func canonicalGateHosts(hosts []string, managementHost string) bool {
	if len(hosts) == 0 || len(hosts) > maxGateHosts {
		return false
	}
	seen := make(map[string]bool, len(hosts))
	for i, value := range hosts {
		host, valid := domain.NormalizeDNSHost(value)
		if !valid || host == managementHost || seen[host] {
			return false
		}
		seen[host] = true
		hosts[i] = host
	}
	return true
}

// gateFields decodes one object with exactly the named non-null fields. In a
// CAS request silently accepting duplicate keys (last value wins) is ambiguous.
func gateFields(raw json.RawMessage, fields map[string]any) bool {
	if !utf8.Valid(raw) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return false
	}
	seen := make(map[string]bool, len(fields))
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		target, known := fields[key]
		if err != nil || !ok || !known || seen[key] {
			return false
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(value, []byte("null")) || json.Unmarshal(value, target) != nil {
			return false
		}
	}
	token, err := decoder.Token()
	return err == nil && token == json.Delim('}') && len(seen) == len(fields)
}
