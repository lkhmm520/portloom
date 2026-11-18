package cutover

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/netip"
	"reflect"
	"slices"
	"strings"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/store"
)

var (
	ErrSwitchConflict  = errors.New("cutover state or scope conflicts with the operation")
	ErrSwitchUnsettled = errors.New("cutover write is unsettled; inspect or retry the same intent")
	// ErrSwitchWriteRejected confirms that this specific traffic CAS was not
	// applied and cannot apply later. It says nothing about earlier attempts.
	ErrSwitchWriteRejected = errors.New("cutover traffic write definitively rejected")
)

// SwitchSpec names one exact, already fenced group. An operation ID cannot be
// reused for another group, even after rollback. Nodes must be separate trust
// domains bound to independently reachable control and verification paths.
type SwitchSpec struct {
	OperationID    string
	Hosts          []string
	Source, Target SwitchNode
}

type SwitchNode struct {
	ID, ClientID, ManagementHost string
	Generations                  map[string]int64 // approved starting gate generation for every host
	Addresses                    []netip.Addr
}

type NodeSnapshot struct {
	ID, ManagementHost string
	Gates              map[string]store.PublicationGate
	Routes             []domain.Route
}

// SwitchNodes returns the complete route membership (including disabled rows
// and foreign owners) and gates for the selected hosts from an authoritative
// snapshot. CompareNode must be atomic across the group with exact retry
// semantics, as in store.TransitionPublicationGates. All coordinators and
// writers must honor the operation's holds; out-of-band gate writes using the
// same operation identity invalidate the exclusivity guarantee.
type SwitchNodes interface {
	InspectNode(ctx context.Context, node string, hosts []string) (NodeSnapshot, error)
	CompareNode(ctx context.Context, node string, group []store.PublicationGateExpectation, policy store.PublicationPolicy) error
	// RefenceNode advances generations while preserving the exact blocked policy.
	RefenceNode(ctx context.Context, node string, group []store.PublicationGateExpectation, blocked store.PublicationPolicy) error
}

type TrafficState struct {
	Generation int64
	Addresses  []netip.Addr
}

// SwitchTraffic is the authority for the entire selected address group, not a
// DNS resolver. Inspect must fail for divergent/missing members, unsupported
// routing, or incomplete evidence. Compare must atomically change only the
// exact group from expected to {expected.Generation+1, addresses}; a replay
// must never increment twice. Providers without this contract are unsupported.
// Compare may return ErrSwitchWriteRejected only with definitive evidence
// that this attempt cannot take effect, not merely an observed pre-state.
type SwitchTraffic interface {
	InspectTraffic(ctx context.Context, hosts []string) (TrafficState, error)
	CompareTraffic(ctx context.Context, hosts []string, expected TrafficState, addresses []netip.Addr) error
}

type ProbeMode string

const (
	ProbeIsolated ProbeMode = "isolated"
	ProbePublic   ProbeMode = "public"
)

// SwitchHTTPS must verify trusted TLS with the selected Host and each enabled
// route's application behavior, identifying the actual serving node. Isolated
// mode tests the target without opening the public gate; public mode uses the
// normal public path and must not substitute forced resolution. routes is the
// exact validated node membership, including disabled routes.
type SwitchHTTPS interface {
	VerifyHTTPS(ctx context.Context, node SwitchNode, hosts []string, routes []MappingIdentity, mode ProbeMode) error
}

// SwitchJournal must serialize each operation ID across all callers, including
// recovery callers, for the entire callback (no expiring lock without fencing).
// Save must atomically and durably replace the record before returning nil;
// return nil progress only for an operation that has never been recorded.
// Participants and journal are trusted adapters; do not deserialize untrusted
// external input as progress. Adapters must honor context deadlines.
type SwitchJournal interface {
	WithOperation(ctx context.Context, operationID string, run func(current *SwitchProgress, save func(SwitchProgress) error) error) error
}

type SwitchStatus string

const (
	SwitchPrepared       SwitchStatus = "prepared"
	SwitchSourceBlocked  SwitchStatus = "source_blocked"
	SwitchTargetPromoted SwitchStatus = "target_promoted"
	SwitchTrafficChanged SwitchStatus = "traffic_changed"
	SwitchComplete       SwitchStatus = "complete"
	SwitchRestoring      SwitchStatus = "restoring"
	SwitchRolledBack     SwitchStatus = "rolled_back"
)

type SwitchAction string

const (
	blockSource    SwitchAction = "block_source"
	promoteTarget  SwitchAction = "promote_target"
	moveTraffic    SwitchAction = "move_traffic"
	blockTarget    SwitchAction = "block_target"
	refenceTarget  SwitchAction = "refence_target"
	restoreSource  SwitchAction = "restore_source"
	restoreTraffic SwitchAction = "restore_traffic"
)

// MappingIdentity excludes changing observations. Within each node it binds
// the stable route IDs, local binding and route configuration against drift.
type MappingIdentity struct {
	ID, Host, PathPrefix, LocalHost, TunnelGroup string
	LocalPort, RemotePort                        int
	DesiredRevision                              int64
	Enabled, StripPath                           bool
}

type SwitchProgress struct {
	Spec                       SwitchSpec
	Status                     SwitchStatus
	Pending                    SwitchAction
	SourceGates, TargetGates   map[string]store.PublicationGate
	SourceRoutes, TargetRoutes []MappingIdentity
	Traffic                    TrafficState
}

// Coordinator implements break-before-make for new publication decisions. It
// does not drain in-flight requests or promise zero downtime during a switch.
// It never opens a hold for ordinary CRUD or removes a route.
type Coordinator struct {
	nodes   SwitchNodes
	traffic SwitchTraffic
	https   SwitchHTTPS
	journal SwitchJournal
}

func NewCoordinator(nodes SwitchNodes, traffic SwitchTraffic, https SwitchHTTPS, journal SwitchJournal) *Coordinator {
	return &Coordinator{nodes, traffic, https, journal}
}

// Start checks the approved holds, complete route parity, and target isolated
// HTTPS before recording any intent. Once started, resume by operation ID.
func (c *Coordinator) Start(ctx context.Context, spec SwitchSpec) (SwitchProgress, error) {
	spec, err := normalizeSwitchSpec(spec)
	if err != nil {
		return SwitchProgress{}, err
	}
	return c.run(ctx, spec.OperationID, &spec, false, false)
}

func (c *Coordinator) Forward(ctx context.Context, operationID string) (SwitchProgress, error) {
	return c.run(ctx, operationID, nil, false, false)
}

// SettlePending may apply the one already recorded CAS, but never starts the
// next step. A caller can use it explicitly before choosing Forward or Rollback.
func (c *Coordinator) SettlePending(ctx context.Context, operationID string) (SwitchProgress, error) {
	return c.run(ctx, operationID, nil, false, true)
}

// Rollback never applies a pending forward write. It can adopt an exact
// observed post-state, but a pre-state alone does not prove the write cannot
// still arrive. For pending promotion it advances the blocked target's
// generations to fence delayed delivery before restoring source. Other
// pending actions still require settlement. Holds remain after either outcome.
func (c *Coordinator) Rollback(ctx context.Context, operationID string) (SwitchProgress, error) {
	return c.run(ctx, operationID, nil, true, false)
}

func (c *Coordinator) run(ctx context.Context, id string, spec *SwitchSpec, rollback, pendingOnly bool) (SwitchProgress, error) {
	var result SwitchProgress
	if c.nodes == nil || c.traffic == nil || c.https == nil || c.journal == nil || !validSwitchID(id) {
		return result, fmt.Errorf("%w: coordinator dependencies and operation ID are required", ErrInput)
	}
	err := c.journal.WithOperation(ctx, id, func(current *SwitchProgress, save func(SwitchProgress) error) error {
		persist := func(p SwitchProgress) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := save(cloneProgress(p)); err != nil {
				return fmt.Errorf("save cutover progress: %w", err)
			}
			result = p
			return nil
		}
		if current == nil {
			if spec == nil {
				return fmt.Errorf("%w: operation not found", ErrSwitchConflict)
			}
			prepared, err := c.prepare(ctx, *spec)
			if err != nil {
				return err
			}
			if err = persist(prepared); err != nil {
				return err
			}
		} else {
			result = cloneProgress(*current)
			if result.Spec.OperationID != id || (spec != nil && !reflect.DeepEqual(*spec, result.Spec)) {
				return fmt.Errorf("%w: operation ID is bound to another specification", ErrSwitchConflict)
			}
		}
		if pendingOnly {
			if result.Pending == "" {
				return nil
			}
			return c.settle(ctx, result, false, false, persist)
		}
		if !rollback && (result.Status == SwitchRestoring || result.Status == SwitchRolledBack) {
			return fmt.Errorf("%w: operation is in rollback", ErrSwitchConflict)
		}
		if (rollback && result.Status == SwitchRolledBack) || (!rollback && result.Status == SwitchComplete) {
			return nil
		}
		freshIntent := false
		for {
			if result.Pending != "" {
				reversing := rollback && result.Status != SwitchRestoring
				if err := c.settle(ctx, result, reversing, freshIntent, persist); err != nil {
					return err
				}
				freshIntent = false
				continue
			}
			live, err := c.inspect(ctx, result.Spec)
			if err != nil {
				return err
			}
			if !sameSnapshot(result, live) {
				return fmt.Errorf("%w: drift from the last confirmed barrier", ErrSwitchConflict)
			}
			p := result
			if rollback {
				if p.Status != SwitchRestoring {
					p.Status = SwitchRestoring
				} else {
					switch {
					case p.TargetGates[p.Spec.Hosts[0]].Phase == store.PublicationFrozen:
						p.Pending = blockTarget
					case p.SourceGates[p.Spec.Hosts[0]].Phase == store.PublicationBlocked:
						p.Pending = restoreSource
					case !slices.Equal(p.Traffic.Addresses, p.Spec.Source.Addresses):
						p.Pending = restoreTraffic
					default:
						return c.finish(ctx, p, true, persist)
					}
				}
			} else {
				switch p.Status {
				case SwitchPrepared:
					p.Pending = blockSource
				case SwitchSourceBlocked:
					if err := c.probe(ctx, p, false, ProbeIsolated); err != nil {
						return err
					}
					p.Pending = promoteTarget
				case SwitchTargetPromoted:
					p.Pending = moveTraffic
				case SwitchTrafficChanged:
					return c.finish(ctx, p, false, persist)
				default:
					return fmt.Errorf("%w: unknown forward stage", ErrSwitchConflict)
				}
			}
			if err := persist(p); err != nil {
				return err
			}
			freshIntent = true
		}
	})
	return result, err
}

func (c *Coordinator) prepare(ctx context.Context, spec SwitchSpec) (SwitchProgress, error) {
	p, err := c.inspect(ctx, spec)
	if err != nil {
		return SwitchProgress{}, err
	}
	for _, host := range spec.Hosts {
		for _, v := range []struct {
			node  SwitchNode
			gates map[string]store.PublicationGate
			phase store.PublicationPhase
		}{
			{spec.Source, p.SourceGates, store.PublicationFrozen}, {spec.Target, p.TargetGates, store.PublicationBlocked},
		} {
			if v.gates[host] != (store.PublicationGate{PublicationPolicy: policy(spec, v.node, v.phase), Generation: v.node.Generations[host]}) {
				return SwitchProgress{}, fmt.Errorf("%w: initial hold mismatch on %s for %s", ErrSwitchConflict, v.node.ID, host)
			}
		}
	}
	if !slices.Equal(p.Traffic.Addresses, spec.Source.Addresses) || p.Traffic.Generation > math.MaxInt64-2 {
		return SwitchProgress{}, fmt.Errorf("%w: traffic baseline does not match source", ErrSwitchConflict)
	}
	if err := c.probe(ctx, p, false, ProbeIsolated); err != nil {
		return SwitchProgress{}, err
	}
	p.Status = SwitchPrepared
	return p, nil
}

func (c *Coordinator) finish(ctx context.Context, p SwitchProgress, rollback bool, save func(SwitchProgress) error) error {
	if err := c.probe(ctx, p, rollback, ProbePublic); err != nil {
		return err
	}
	live, err := c.inspect(ctx, p.Spec)
	if err != nil {
		return err
	}
	if !sameSnapshot(p, live) {
		return fmt.Errorf("%w: observed drift during public HTTPS verification", ErrSwitchConflict)
	}
	p.Status = SwitchComplete
	if rollback {
		p.Status = SwitchRolledBack
	}
	return save(p)
}

func (c *Coordinator) probe(ctx context.Context, p SwitchProgress, source bool, mode ProbeMode) error {
	node, routes := p.Spec.Target, p.TargetRoutes
	if source {
		node, routes = p.Spec.Source, p.SourceRoutes
	}
	if err := c.https.VerifyHTTPS(ctx, cloneSwitchNode(node), slices.Clone(p.Spec.Hosts), slices.Clone(routes), mode); err != nil {
		return fmt.Errorf("%s HTTPS verification on %s: %w", mode, node.ID, err)
	}
	return nil
}

// settle matches either an exact pre-state or the exact post-state of the
// journaled CAS. Anything else (even a matching phase with another generation)
// is not our barrier. No other step can run while this proof is missing.
func (c *Coordinator) settle(ctx context.Context, p SwitchProgress, readOnly, freshIntent bool, save func(SwitchProgress) error) error {
	next, apply, err := c.transition(p)
	if err != nil {
		return err
	}
	// The exact promotion post-state is the only alternative to a successful
	// re-fence. If promotion wins the race, persist the intent to re-block it.
	var promoted SwitchProgress
	if p.Pending == refenceTarget {
		prior := p
		prior.Status, prior.Pending = SwitchSourceBlocked, promoteTarget
		promoted, _, err = c.transition(prior)
		if err != nil {
			return err
		}
	}
	confirm := func(live SwitchProgress) (bool, error) {
		if p.Pending == refenceTarget && sameSnapshot(promoted, live) {
			promoted.Status, promoted.Pending = SwitchRestoring, blockTarget
			return true, save(promoted)
		}
		if sameSnapshot(next, live) {
			if readOnly && p.Pending == promoteTarget {
				next.Status, next.Pending = SwitchRestoring, blockTarget
			}
			return true, save(next)
		}
		return false, nil
	}
	live, err := c.inspect(ctx, p.Spec)
	if err != nil {
		return errors.Join(ErrSwitchUnsettled, err)
	}
	if confirmed, err := confirm(live); confirmed {
		return err
	}
	if !sameSnapshot(p, live) {
		return fmt.Errorf("%w: %s does not match either exact CAS barrier", ErrSwitchConflict, p.Pending)
	}
	if readOnly {
		if p.Pending == promoteTarget {
			// Record the rollback decision before dispatch. After restart,
			// neither Forward nor SettlePending can send the old promotion.
			p.Status, p.Pending = SwitchRestoring, refenceTarget
			return save(p)
		}
		return fmt.Errorf("%w: pending forward action %s", ErrSwitchUnsettled, p.Pending)
	}
	if p.Pending == promoteTarget && !freshIntent {
		if err := c.probe(ctx, p, false, ProbeIsolated); err != nil {
			return errors.Join(ErrSwitchUnsettled, err)
		}
		live, err := c.inspect(ctx, p.Spec)
		if err != nil {
			return errors.Join(ErrSwitchUnsettled, err)
		}
		if sameSnapshot(next, live) {
			return save(next)
		}
		if !sameSnapshot(p, live) {
			return fmt.Errorf("%w: drift during isolated HTTPS verification", ErrSwitchConflict)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	writeErr := apply(ctx)
	live, err = c.inspect(ctx, p.Spec)
	if err != nil {
		return errors.Join(fmt.Errorf("%w: readback for %s", ErrSwitchUnsettled, p.Pending), writeErr, err)
	}
	if confirmed, err := confirm(live); confirmed {
		return err
	}
	if !sameSnapshot(p, live) {
		return errors.Join(fmt.Errorf("%w: unexpected readback for %s", ErrSwitchConflict, p.Pending), writeErr)
	}
	// Only the first attempt can clear this intent on rejection. A rejected
	// replay plus pre-state readback cannot fence an earlier in-flight CAS.
	if p.Pending == moveTraffic && freshIntent && errors.Is(writeErr, ErrSwitchWriteRejected) && !errors.Is(writeErr, ErrSwitchUnsettled) {
		p.Pending = ""
		if err := save(p); err != nil {
			return errors.Join(ErrSwitchUnsettled, writeErr, err)
		}
		return writeErr // Stop forward; explicit rollback can now use this barrier.
	}
	return errors.Join(fmt.Errorf("%w: %s is not confirmed", ErrSwitchUnsettled, p.Pending), writeErr)
}

func (c *Coordinator) transition(p SwitchProgress) (SwitchProgress, func(context.Context) error, error) {
	next := cloneProgress(p)
	next.Pending = ""
	gate := func(node SwitchNode, from, to map[string]store.PublicationGate, phase store.PublicationPhase) func(context.Context) error {
		desired := policy(p.Spec, node, phase)
		group := make([]store.PublicationGateExpectation, 0, len(p.Spec.Hosts))
		for _, host := range p.Spec.Hosts {
			group = append(group, store.PublicationGateExpectation{Host: host, Expected: from[host]})
			to[host] = store.PublicationGate{PublicationPolicy: desired, Generation: from[host].Generation + 1}
		}
		if p.Pending == refenceTarget {
			return func(ctx context.Context) error { return c.nodes.RefenceNode(ctx, node.ID, group, desired) }
		}
		return func(ctx context.Context) error { return c.nodes.CompareNode(ctx, node.ID, group, desired) }
	}
	traffic := func(node SwitchNode) func(context.Context) error {
		next.Traffic = TrafficState{Generation: p.Traffic.Generation + 1, Addresses: slices.Clone(node.Addresses)}
		return func(ctx context.Context) error {
			expected := p.Traffic
			expected.Addresses = slices.Clone(expected.Addresses)
			return c.traffic.CompareTraffic(ctx, slices.Clone(p.Spec.Hosts), expected, slices.Clone(node.Addresses))
		}
	}
	var apply func(context.Context) error
	switch p.Pending {
	case blockSource:
		if p.Status == SwitchPrepared {
			next.Status = SwitchSourceBlocked
			apply = gate(p.Spec.Source, p.SourceGates, next.SourceGates, store.PublicationBlocked)
		}
	case promoteTarget:
		if p.Status == SwitchSourceBlocked {
			next.Status = SwitchTargetPromoted
			apply = gate(p.Spec.Target, p.TargetGates, next.TargetGates, store.PublicationFrozen)
		}
	case moveTraffic:
		if p.Status == SwitchTargetPromoted {
			next.Status = SwitchTrafficChanged
			apply = traffic(p.Spec.Target)
		}
	case blockTarget, refenceTarget:
		if p.Status == SwitchRestoring {
			if p.Pending == refenceTarget {
				for _, host := range p.Spec.Hosts {
					if p.SourceGates[host] != (store.PublicationGate{PublicationPolicy: policy(p.Spec, p.Spec.Source, store.PublicationBlocked), Generation: p.Spec.Source.Generations[host] + 1}) ||
						p.TargetGates[host] != (store.PublicationGate{PublicationPolicy: policy(p.Spec, p.Spec.Target, store.PublicationBlocked), Generation: p.Spec.Target.Generations[host]}) {
						return SwitchProgress{}, nil, fmt.Errorf("%w: invalid pending promotion fence for %s", ErrSwitchConflict, host)
					}
				}
			}
			apply = gate(p.Spec.Target, p.TargetGates, next.TargetGates, store.PublicationBlocked)
		}
	case restoreSource:
		if p.Status == SwitchRestoring {
			apply = gate(p.Spec.Source, p.SourceGates, next.SourceGates, store.PublicationFrozen)
		}
	case restoreTraffic:
		if p.Status == SwitchRestoring {
			apply = traffic(p.Spec.Source)
		}
	}
	if apply == nil {
		return SwitchProgress{}, nil, fmt.Errorf("%w: invalid pending action %s for %s", ErrSwitchConflict, p.Pending, p.Status)
	}
	return next, apply, nil
}

func policy(s SwitchSpec, n SwitchNode, phase store.PublicationPhase) store.PublicationPolicy {
	return store.PublicationPolicy{Phase: phase, OperationID: s.OperationID, ClientID: n.ClientID}
}

func sameSnapshot(a, b SwitchProgress) bool {
	return maps.Equal(a.SourceGates, b.SourceGates) && maps.Equal(a.TargetGates, b.TargetGates) &&
		slices.Equal(a.SourceRoutes, b.SourceRoutes) && slices.Equal(a.TargetRoutes, b.TargetRoutes) &&
		a.Traffic.Generation == b.Traffic.Generation && slices.Equal(a.Traffic.Addresses, b.Traffic.Addresses)
}

func (c *Coordinator) inspect(ctx context.Context, spec SwitchSpec) (SwitchProgress, error) {
	inspect := func(n SwitchNode) (NodeSnapshot, []MappingIdentity, error) {
		live, err := c.nodes.InspectNode(ctx, n.ID, slices.Clone(spec.Hosts))
		if err != nil {
			return NodeSnapshot{}, nil, fmt.Errorf("inspect node %s: %w", n.ID, err)
		}
		if live.ID != n.ID || live.ManagementHost != n.ManagementHost || len(live.Gates) != len(spec.Hosts) {
			return NodeSnapshot{}, nil, fmt.Errorf("%w: unexpected node identity, management host or gate scope for %s", ErrSwitchConflict, n.ID)
		}
		live.Gates = maps.Clone(live.Gates)
		rows, err := projectRoutes(spec.Hosts, n, live.Routes)
		return live, rows, err
	}
	src, sourceRows, err := inspect(spec.Source)
	if err != nil {
		return SwitchProgress{}, err
	}
	dst, targetRows, err := inspect(spec.Target)
	if err != nil {
		return SwitchProgress{}, err
	}
	if !slices.EqualFunc(sourceRows, targetRows, func(a, b MappingIdentity) bool {
		return a.Host == b.Host && a.PathPrefix == b.PathPrefix && a.StripPath == b.StripPath &&
			a.LocalHost == b.LocalHost && a.LocalPort == b.LocalPort && a.Enabled == b.Enabled
	}) {
		return SwitchProgress{}, fmt.Errorf("%w: source and target route membership or semantics differ", ErrSwitchConflict)
	}
	traffic, err := c.traffic.InspectTraffic(ctx, slices.Clone(spec.Hosts))
	if err != nil {
		return SwitchProgress{}, fmt.Errorf("inspect traffic authority: %w", err)
	}
	traffic.Addresses, err = normalizeAddresses(traffic.Addresses)
	if err != nil || traffic.Generation < 0 {
		return SwitchProgress{}, fmt.Errorf("%w: invalid traffic snapshot", ErrSwitchConflict)
	}
	return SwitchProgress{Spec: spec, SourceGates: src.Gates, TargetGates: dst.Gates, SourceRoutes: sourceRows, TargetRoutes: targetRows, Traffic: traffic}, nil
}

func projectRoutes(hosts []string, n SwitchNode, routes []domain.Route) ([]MappingIdentity, error) {
	selected := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		selected[host] = false
	}
	ids := make(map[string]bool)
	var rows []MappingIdentity
	for _, r := range routes {
		h, ok := domain.NormalizeDNSHost(r.Domain)
		if !ok && r.Protocol.IsWeb() {
			return nil, fmt.Errorf("%w: malformed host in %s inventory", ErrSwitchConflict, n.ID)
		}
		if _, wanted := selected[h]; !wanted {
			continue
		}
		path, valid := domain.NormalizePathPrefix(r.PathPrefix)
		if r.Protocol != domain.ProtocolHTTPS || r.PublicPort != 0 || r.ClientID != n.ClientID || !valid || path != r.PathPrefix || r.Domain != h ||
			(r.StripPath && path == "") || !domain.ValidHost(r.LocalHost) || r.LocalPort < 1 || r.LocalPort > 65535 || !validSwitchID(r.ID) || ids[r.ID] {
			return nil, fmt.Errorf("%w: unexpected route in selected host %s on %s", ErrSwitchConflict, h, n.ID)
		}
		ids[r.ID] = true
		selected[h] = selected[h] || r.Enabled
		rows = append(rows, MappingIdentity{ID: r.ID, Host: h, PathPrefix: path, LocalHost: r.LocalHost, TunnelGroup: r.TunnelGroup, LocalPort: r.LocalPort, RemotePort: r.RemotePort, DesiredRevision: r.DesiredRevision, Enabled: r.Enabled, StripPath: r.StripPath})
	}
	for _, host := range hosts {
		if !selected[host] {
			return nil, fmt.Errorf("%w: no enabled route for %s on %s", ErrSwitchConflict, host, n.ID)
		}
	}
	slices.SortFunc(rows, func(a, b MappingIdentity) int {
		if v := strings.Compare(a.Host, b.Host); v != 0 {
			return v
		}
		return strings.Compare(a.PathPrefix, b.PathPrefix)
	})
	for i := 1; i < len(rows); i++ {
		if rows[i].Host == rows[i-1].Host && rows[i].PathPrefix == rows[i-1].PathPrefix {
			return nil, fmt.Errorf("%w: duplicate host/path on %s", ErrSwitchConflict, n.ID)
		}
	}
	return rows, nil
}

func normalizeSwitchSpec(s SwitchSpec) (SwitchSpec, error) {
	if !validSwitchID(s.OperationID) || !validSwitchID(s.Source.ID) || !validSwitchID(s.Target.ID) || s.Source.ID == s.Target.ID {
		return SwitchSpec{}, fmt.Errorf("%w: operation ID and two distinct nodes are required", ErrInput)
	}
	hosts, err := normalizeHosts(s.Hosts)
	if err != nil {
		return SwitchSpec{}, err
	}
	if len(hosts) != len(s.Hosts) {
		return SwitchSpec{}, fmt.Errorf("%w: duplicate group host", ErrInput)
	}
	s.Hosts = hosts
	for _, n := range []*SwitchNode{&s.Source, &s.Target} {
		management, ok := domain.NormalizeDNSHost(n.ManagementHost)
		if !ok || slices.Contains(hosts, management) || !validSwitchID(n.ClientID) || len(hosts) != len(n.Generations) {
			return SwitchSpec{}, fmt.Errorf("%w: management host, client or gate scope is invalid", ErrInput)
		}
		n.ManagementHost = management
		n.Generations = maps.Clone(n.Generations)
		for _, host := range hosts {
			if g := n.Generations[host]; g <= 0 || g > math.MaxInt64-2 {
				return SwitchSpec{}, fmt.Errorf("%w: invalid starting gate generation", ErrInput)
			}
		}
		n.Addresses, err = normalizeAddresses(n.Addresses)
		if err != nil {
			return SwitchSpec{}, err
		}
	}
	for _, addr := range s.Source.Addresses {
		if slices.Contains(s.Target.Addresses, addr) {
			return SwitchSpec{}, fmt.Errorf("%w: source and target address sets overlap", ErrInput)
		}
	}
	return s, nil
}

func normalizeAddresses(addresses []netip.Addr) ([]netip.Addr, error) {
	out := slices.Clone(addresses)
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: empty traffic address group", ErrInput)
	}
	for i, a := range out {
		if a.Zone() != "" || !a.IsGlobalUnicast() {
			return nil, fmt.Errorf("%w: invalid traffic address", ErrInput)
		}
		out[i] = a.Unmap()
	}
	slices.SortFunc(out, netip.Addr.Compare)
	if len(slices.Compact(slices.Clone(out))) != len(out) {
		return nil, fmt.Errorf("%w: duplicate traffic address", ErrInput)
	}
	return out, nil
}

func validSwitchID(s string) bool {
	return s != "" && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\x00\r\n\t")
}

func cloneSwitchNode(n SwitchNode) SwitchNode {
	n.Generations = maps.Clone(n.Generations)
	n.Addresses = slices.Clone(n.Addresses)
	return n
}

func cloneProgress(p SwitchProgress) SwitchProgress {
	p.Spec.Hosts = slices.Clone(p.Spec.Hosts)
	p.Spec.Source = cloneSwitchNode(p.Spec.Source)
	p.Spec.Target = cloneSwitchNode(p.Spec.Target)
	p.SourceGates, p.TargetGates = maps.Clone(p.SourceGates), maps.Clone(p.TargetGates)
	p.SourceRoutes, p.TargetRoutes = slices.Clone(p.SourceRoutes), slices.Clone(p.TargetRoutes)
	p.Traffic.Addresses = slices.Clone(p.Traffic.Addresses)
	return p
}
