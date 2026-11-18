package cutover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"unicode/utf8"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/store"
)

// GateNodes binds logical node IDs to trusted, independently reachable profiles.
// Keep that binding stable for the lifetime of any journaled operation.
type GateNodes struct{ clients map[string]*inventoryClient }

var _ SwitchNodes = (*GateNodes)(nil)

// OpenGateNodes loads the selected profiles using the same credential and TLS
// policy as Plan. It makes no requests and never falls back to inventory reads.
func OpenGateNodes(configPath string, ids ...string) (*GateNodes, error) {
	config, err := loadConfig(configPath)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: select at least one gate profile", ErrInput)
	}
	n := &GateNodes{clients: make(map[string]*inventoryClient, len(ids))}
	origins, management := map[string]bool{}, map[string]bool{}
	for _, id := range ids {
		p, ok := config.Profiles[id]
		if !ok || !domain.ValidAgentName(id) || n.clients[id] != nil {
			return nil, fmt.Errorf("%w: missing, invalid or repeated gate profile", ErrInput)
		}
		c, err := newInventoryClient(p, filepath.Dir(configPath))
		if err != nil {
			return nil, err
		}
		origin := originKey(c)
		if origins[origin] || management[c.managementHost] {
			return nil, fmt.Errorf("%w: gate profiles must have distinct API origins and management hosts", ErrInput)
		}
		origins[origin], management[c.managementHost] = true, true
		n.clients[id] = c
	}
	return n, nil
}

func (n *GateNodes) CloseIdleConnections() {
	for _, c := range n.clients {
		c.http.CloseIdleConnections()
	}
}

type gatePolicyWire struct {
	Phase       store.PublicationPhase `json:"phase"`
	OperationID string                 `json:"operation_id"`
	ClientID    string                 `json:"client_id"`
}
type gateExpectationWire struct {
	Host       string `json:"host"`
	Generation int64  `json:"generation"`
	gatePolicyWire
}
type gateObservationWire struct {
	Host        string                  `json:"host"`
	Generation  *int64                  `json:"generation"`
	Phase       *store.PublicationPhase `json:"phase"`
	OperationID *string                 `json:"operation_id"`
	ClientID    *string                 `json:"client_id"`
}

func (c *inventoryClient) gateScope(hosts []string) (map[string]bool, error) {
	if len(hosts) == 0 || len(hosts) > store.MaxPublicationHosts {
		return nil, fmt.Errorf("%w: invalid gate scope size", ErrInput)
	}
	scope := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		normalized, ok := domain.NormalizeDNSHost(host)
		if !ok || host != normalized || scope[host] || host == c.managementHost || host == domain.NormalizeHost(c.endpoint.Hostname()) {
			return nil, fmt.Errorf("%w: invalid, management or duplicate gate host", ErrInput)
		}
		scope[host] = true
	}
	return scope, nil
}

func (n *GateNodes) InspectNode(ctx context.Context, id string, hosts []string) (NodeSnapshot, error) {
	c := n.clients[id]
	if c == nil {
		return NodeSnapshot{}, fmt.Errorf("%w: unknown gate profile", ErrInput)
	}
	scope, err := c.gateScope(hosts)
	if err != nil {
		return NodeSnapshot{}, err
	}
	raw, err := gatePost(ctx, c, "/api/v1/cutover/gates/snapshot", struct {
		Hosts []string `json:"hosts"`
	}{hosts}, http.StatusOK)
	if err != nil {
		return NodeSnapshot{}, err
	}
	var body struct {
		ManagementHost string                `json:"management_host"`
		Gates          []gateObservationWire `json:"gates"`
		Routes         []inventoryRoute      `json:"routes"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	invalid := fmt.Errorf("%w: invalid or incomplete publication snapshot", ErrSwitchConflict)
	if !utf8.Valid(raw) || !json.Valid(raw) || dec.Decode(&body) != nil || !uniqueConfigKeys(json.NewDecoder(bytes.NewReader(raw))) || body.ManagementHost != c.managementHost || len(body.Gates) != len(scope) || body.Routes == nil {
		return NodeSnapshot{}, invalid
	}
	out := NodeSnapshot{ID: id, ManagementHost: body.ManagementHost, Gates: make(map[string]store.PublicationGate, len(scope)), Routes: make([]domain.Route, 0, len(body.Routes))}
	for _, gate := range body.Gates {
		if !scope[gate.Host] || gate.Generation == nil || gate.Phase == nil || gate.OperationID == nil || gate.ClientID == nil {
			return NodeSnapshot{}, invalid
		}
		if _, exists := out.Gates[gate.Host]; exists {
			return NodeSnapshot{}, invalid
		}
		value := store.PublicationGate{Generation: *gate.Generation, PublicationPolicy: store.PublicationPolicy{Phase: *gate.Phase, OperationID: *gate.OperationID, ClientID: *gate.ClientID}}
		if !validObservedGate(value) {
			return NodeSnapshot{}, invalid
		}
		out.Gates[gate.Host] = value
	}
	ids := make(map[string]bool, len(body.Routes))
	for _, row := range body.Routes {
		if !scope[row.Domain] || !validSwitchID(row.ID) || ids[row.ID] || !validSwitchID(row.ClientID) || row.Protocol == "" || row.Enabled == nil {
			return NodeSnapshot{}, invalid
		}
		ids[row.ID] = true
		row.Route.Enabled = *row.Enabled
		out.Routes = append(out.Routes, row.Route)
	}
	return out, nil
}

func validObservedGate(g store.PublicationGate) bool {
	switch g.Phase {
	case store.PublicationOpen:
		return g.Generation >= 0 && g.OperationID == "" && g.ClientID == ""
	case store.PublicationFrozen, store.PublicationBlocked:
		return g.Generation > 0 && validSwitchID(g.OperationID) && validSwitchID(g.ClientID)
	default:
		return false
	}
}

func (n *GateNodes) CompareNode(ctx context.Context, id string, group []store.PublicationGateExpectation, policy store.PublicationPolicy) error {
	return n.changeGates(ctx, id, group, policy, "/api/v1/cutover/gates/compare")
}

func (n *GateNodes) RefenceNode(ctx context.Context, id string, group []store.PublicationGateExpectation, policy store.PublicationPolicy) error {
	return n.changeGates(ctx, id, group, policy, "/api/v1/cutover/gates/refence")
}

func (n *GateNodes) changeGates(ctx context.Context, id string, group []store.PublicationGateExpectation, policy store.PublicationPolicy, path string) error {
	c := n.clients[id]
	if c == nil {
		return fmt.Errorf("%w: unknown gate profile", ErrInput)
	}
	if len(group) > store.MaxPublicationHosts || !validSwitchID(policy.OperationID) || !validSwitchID(policy.ClientID) || !utf8.ValidString(policy.OperationID) || !utf8.ValidString(policy.ClientID) ||
		(policy.Phase != store.PublicationOpen && policy.Phase != store.PublicationFrozen && policy.Phase != store.PublicationBlocked) {
		return fmt.Errorf("%w: invalid gate transition", ErrInput)
	}
	hosts := make([]string, len(group))
	expect := make([]gateExpectationWire, len(group))
	for i, m := range group {
		g := m.Expected
		if !validObservedGate(g) || g.Generation == math.MaxInt64 || !utf8.ValidString(g.OperationID) || !utf8.ValidString(g.ClientID) {
			return fmt.Errorf("%w: invalid gate expectation", ErrInput)
		}
		hosts[i] = m.Host
		expect[i] = gateExpectationWire{m.Host, g.Generation, gatePolicyWire{g.Phase, g.OperationID, g.ClientID}}
	}
	if _, err := c.gateScope(hosts); err != nil {
		return err
	}
	_, err := gatePost(ctx, c, path, struct {
		Expect []gateExpectationWire `json:"expect"`
		Next   gatePolicyWire        `json:"next"`
	}{expect, gatePolicyWire{policy.Phase, policy.OperationID, policy.ClientID}}, http.StatusNoContent)
	return err
}

func gatePost(ctx context.Context, c *inventoryClient, path string, body any, status int) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil || len(data) > 64<<10 {
		return nil, fmt.Errorf("%w: cannot encode bounded gate request", ErrInput)
	}
	return c.request(ctx, http.MethodPost, path, data, status)
}
