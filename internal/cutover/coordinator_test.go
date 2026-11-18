package cutover

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"github.com/lkhmm520/portloom/internal/domain"
	"github.com/lkhmm520/portloom/internal/store"
)

const (
	appHost = "app.example.test"
	apiHost = "api.example.test"
)

func TestCoordinatorForward(t *testing.T) {
	f := newSwitchFixture()
	p, err := f.coordinator().Start(context.Background(), f.spec)
	if err != nil || p.Status != SwitchComplete {
		t.Fatalf("start: %s, %v", p.Status, err)
	}
	f.want(t, "isolated:target", "source:blocked", "isolated:target", "target:frozen", "traffic:target", "public:target")
	// A new coordinator using the same journal must not repeat completed work.
	p, err = f.coordinator().Forward(context.Background(), f.spec.OperationID)
	if err != nil || p.Status != SwitchComplete {
		t.Fatalf("forward replay: %s, %v", p.Status, err)
	}
	f.want(t, "isolated:target", "source:blocked", "isolated:target", "target:frozen", "traffic:target", "public:target")
}

func TestCoordinatorRollbackAfterPublicFailure(t *testing.T) {
	f := newSwitchFixture()
	f.fail = "public:target"
	p, err := f.coordinator().Start(context.Background(), f.spec)
	if !errors.Is(err, errInjected) || p.Status != SwitchTrafficChanged {
		t.Fatalf("unverified switch reported %s, %v", p.Status, err)
	}
	f.fail = "public:source"
	p, err = f.coordinator().Rollback(context.Background(), f.spec.OperationID)
	if !errors.Is(err, errInjected) || p.Status != SwitchRestoring || p.Pending != "" {
		t.Fatalf("unverified rollback reported %s/%s, %v", p.Status, p.Pending, err)
	}
	if _, err = f.coordinator().Forward(context.Background(), f.spec.OperationID); !errors.Is(err, ErrSwitchConflict) {
		t.Fatalf("forward while restoring: %v", err)
	}
	f.fail = ""
	p, err = f.coordinator().Rollback(context.Background(), f.spec.OperationID)
	if err != nil || p.Status != SwitchRolledBack {
		t.Fatalf("rollback retry: %s, %v", p.Status, err)
	}
	f.want(t, "isolated:target", "source:blocked", "isolated:target", "target:frozen", "traffic:target", "public:target", "target:blocked", "source:frozen", "traffic:source", "public:source", "public:source")
	for _, host := range f.spec.Hosts {
		if p.SourceGates[host].Phase != store.PublicationFrozen || p.TargetGates[host].Phase != store.PublicationBlocked ||
			p.SourceGates[host].Generation != f.spec.Source.Generations[host]+2 || p.TargetGates[host].Generation != f.spec.Target.Generations[host]+2 {
			t.Fatalf("wrong restored gates for %s", host)
		}
	}
}

func TestCoordinatorNeverPromotesUntilSourceBlockReadback(t *testing.T) {
	f := newSwitchFixture()
	f.hideAfter, f.fail, f.failAfter = "source:blocked", "source:blocked", true
	p, err := f.coordinator().Start(context.Background(), f.spec)
	if !errors.Is(err, ErrSwitchUnsettled) || p.Status != SwitchPrepared || p.Pending != blockSource {
		t.Fatalf("unconfirmed source: %s/%s, %v", p.Status, p.Pending, err)
	}
	f.want(t, "isolated:target", "source:blocked")
	f.hidden = ""
	p, err = f.coordinator().Forward(context.Background(), f.spec.OperationID)
	if err != nil || p.Status != SwitchComplete {
		t.Fatalf("resume: %s, %v", p.Status, err)
	}
	f.want(t, "isolated:target", "source:blocked", "isolated:target", "target:frozen", "traffic:target", "public:target")
}

func TestCoordinatorNeverRestoresSourceWhileTargetBlockUnknown(t *testing.T) {
	f := newSwitchFixture()
	if _, err := f.coordinator().Start(context.Background(), f.spec); err != nil {
		t.Fatal(err)
	}
	f.events = nil
	f.hideAfter = "target:blocked"
	p, err := f.coordinator().Rollback(context.Background(), f.spec.OperationID)
	if !errors.Is(err, ErrSwitchUnsettled) || p.Status != SwitchRestoring || p.Pending != blockTarget {
		t.Fatalf("unconfirmed target: %s/%s, %v", p.Status, p.Pending, err)
	}
	f.want(t, "target:blocked")
	f.hidden = ""
	p, err = f.coordinator().Rollback(context.Background(), f.spec.OperationID)
	if err != nil || p.Status != SwitchRolledBack {
		t.Fatalf("resume rollback: %s, %v", p.Status, err)
	}
	f.want(t, "target:blocked", "source:frozen", "traffic:source", "public:source")
}

func TestCoordinatorRollbackFromAmbiguousPromotion(t *testing.T) {
	for _, applied := range []bool{false, true} {
		name := "unapplied"
		if applied {
			name = "applied"
		}
		t.Run(name, func(t *testing.T) {
			f := newSwitchFixture()
			f.fail, f.failAfter = "target:frozen", applied
			if applied {
				f.hideAfter = f.fail
			}
			p, err := f.coordinator().Start(context.Background(), f.spec)
			if !errors.Is(err, ErrSwitchUnsettled) || p.Pending != promoteTarget {
				t.Fatalf("start: %s, %v", p.Pending, err)
			}
			f.events, f.hidden = nil, ""
			f.fail = "isolated:target"
			p, err = f.coordinator().Rollback(context.Background(), f.spec.OperationID)
			if err != nil || p.Status != SwitchRolledBack {
				t.Fatalf("rollback: %s/%s, %v", p.Status, p.Pending, err)
			}
			if applied {
				f.want(t, "target:blocked", "source:frozen", "public:source")
			} else {
				f.want(t, "target:refence", "source:frozen", "public:source")
			}
		})
	}
}

func TestCoordinatorRollbackAfterUnsentPromotion(t *testing.T) {
	f := newSwitchFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.failSave = func(p SwitchProgress) bool {
		if p.Pending == promoteTarget {
			cancel() // Journal save succeeds, but dispatch sees the canceled context.
		}
		return false
	}
	p, err := f.coordinator().Start(ctx, f.spec)
	if !errors.Is(err, context.Canceled) || p.Status != SwitchSourceBlocked || p.Pending != promoteTarget {
		t.Fatalf("interrupted start: %s/%s, %v", p.Status, p.Pending, err)
	}
	f.want(t, "isolated:target", "source:blocked", "isolated:target")
	f.fail = "isolated:target"
	ctx = context.Background()
	if _, err := f.coordinator().SettlePending(ctx, f.spec.OperationID); !errors.Is(err, errInjected) {
		t.Fatalf("forward settlement did not require target preview: %v", err)
	}
	f.events = nil
	p, err = f.coordinator().Rollback(ctx, f.spec.OperationID)
	if err != nil || p.Status != SwitchRolledBack || !reflect.DeepEqual(p.Traffic, f.traffic) || p.Traffic.Generation != 7 {
		t.Fatalf("rollback: %s/%s, %v", p.Status, p.Pending, err)
	}
	f.want(t, "target:refence", "source:frozen", "public:source")
}

func TestCoordinatorVerifiesIsolatedTargetAgainAfterSourceBlockDelay(t *testing.T) {
	f := newSwitchFixture()
	f.hideAfter = "source:blocked"
	if _, err := f.coordinator().Start(context.Background(), f.spec); !errors.Is(err, ErrSwitchUnsettled) {
		t.Fatal(err)
	}
	f.hidden, f.fail = "", "isolated:target"
	p, err := f.coordinator().Forward(context.Background(), f.spec.OperationID)
	if !errors.Is(err, errInjected) || p.Status != SwitchSourceBlocked || p.Pending != "" {
		t.Fatalf("resumed with stale isolated proof: %s/%s, %v", p.Status, p.Pending, err)
	}
	f.want(t, "isolated:target", "source:blocked", "isolated:target")
}

func TestCoordinatorRejectsUnsafePreflight(t *testing.T) {
	for name, change := range map[string]func(*switchFixture){
		"wrong generation":          func(f *switchFixture) { f.spec.Target.Generations[appHost]++ },
		"management host":           func(f *switchFixture) { f.spec.Target.ManagementHost = appHost },
		"missing disabled prefix":   func(f *switchFixture) { n := f.nodes["target"]; n.state.Routes = n.state.Routes[:2] },
		"divergent strip semantics": func(f *switchFixture) { f.nodes["target"].state.Routes[2].StripPath = false },
		"foreign owner":             func(f *switchFixture) { f.nodes["target"].state.Routes[2].ClientID = "someone-else" },
		"alternate HTTPS port":      func(f *switchFixture) { f.nodes["target"].state.Routes[2].PublicPort = 8443 },
		"traffic already elsewhere": func(f *switchFixture) { f.traffic.Addresses = f.spec.Target.Addresses },
		"isolated TLS failure":      func(f *switchFixture) { f.fail = "isolated:target" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newSwitchFixture()
			change(f)
			if _, err := f.coordinator().Start(context.Background(), f.spec); err == nil || f.saved != nil {
				t.Fatalf("unsafe preflight: progress=%+v error=%v", f.saved, err)
			}
			if f.fail != "" {
				f.want(t, f.fail)
			} else {
				f.want(t)
			}
		})
	}
}

func TestCoordinatorRejectsPartialBarrier(t *testing.T) {
	f := newSwitchFixture()
	f.hideAfter = "source:blocked"
	if _, err := f.coordinator().Start(context.Background(), f.spec); !errors.Is(err, ErrSwitchUnsettled) {
		t.Fatal(err)
	}
	f.hidden = ""
	g := f.nodes["source"].state.Gates[apiHost]
	g.Generation++ // Same operation and phase, different generation on just one member.
	f.nodes["source"].state.Gates[apiHost] = g
	for _, resume := range []func(context.Context, string) (SwitchProgress, error){f.coordinator().Forward, f.coordinator().Rollback} {
		if _, err := resume(context.Background(), f.spec.OperationID); !errors.Is(err, ErrSwitchConflict) {
			t.Fatal(err)
		}
	}
	f.want(t, "isolated:target", "source:blocked")
}

func TestCoordinatorConfirmsTrafficBeforePublicVerification(t *testing.T) {
	f := newSwitchFixture()
	f.hideAfter, f.fail, f.failAfter = "traffic:target", "traffic:target", true
	p, err := f.coordinator().Start(context.Background(), f.spec)
	if !errors.Is(err, ErrSwitchUnsettled) || p.Status != SwitchTargetPromoted || p.Pending != moveTraffic {
		t.Fatalf("traffic timeout: %s/%s, %v", p.Status, p.Pending, err)
	}
	f.want(t, "isolated:target", "source:blocked", "isolated:target", "target:frozen", "traffic:target")
	f.hidden = ""
	p, err = f.coordinator().Forward(context.Background(), f.spec.OperationID)
	if err != nil || p.Status != SwitchComplete || p.Traffic.Generation != 8 {
		t.Fatalf("traffic recovery: %+v, %v", p, err)
	}
	f.want(t, "isolated:target", "source:blocked", "isolated:target", "target:frozen", "traffic:target", "public:target")
}

func TestCoordinatorRollbackAdoptsLateTrafficCommit(t *testing.T) {
	ctx := context.Background()
	f := newSwitchFixture()
	f.fail = "traffic:target"
	p, err := f.coordinator().Start(ctx, f.spec)
	if !errors.Is(err, ErrSwitchUnsettled) || p.Pending != moveTraffic {
		t.Fatalf("start = %s/%s, %v", p.Status, p.Pending, err)
	}
	f.events = nil
	p, err = f.coordinator().Rollback(ctx, f.spec.OperationID)
	if !errors.Is(err, ErrSwitchUnsettled) || p.Pending != moveTraffic {
		t.Fatalf("rollback before late commit = %s/%s, %v", p.Status, p.Pending, err)
	}
	f.want(t)
	// Deliver the original CAS after rollback has observed the exact pre-state.
	f.traffic = TrafficState{Generation: p.Traffic.Generation + 1, Addresses: f.spec.Target.Addresses}
	p, err = f.coordinator().Rollback(ctx, f.spec.OperationID)
	if err != nil || p.Status != SwitchRolledBack || p.Pending != "" || p.Traffic.Generation != 9 {
		t.Fatalf("rollback after late commit = %s/%s, generation %d, %v", p.Status, p.Pending, p.Traffic.Generation, err)
	}
	f.want(t, "target:blocked", "source:frozen", "traffic:source", "public:source")
}

func TestCoordinatorSettlePendingDoesNotContinueForward(t *testing.T) {
	f := newSwitchFixture()
	f.fail = "source:blocked"
	if _, err := f.coordinator().Start(context.Background(), f.spec); !errors.Is(err, ErrSwitchUnsettled) {
		t.Fatal(err)
	}
	f.fail = ""
	p, err := f.coordinator().SettlePending(context.Background(), f.spec.OperationID)
	if err != nil || p.Status != SwitchSourceBlocked || p.Pending != "" {
		t.Fatalf("settle: %s/%s, %v", p.Status, p.Pending, err)
	}
	f.want(t, "isolated:target", "source:blocked", "source:blocked")
	p, err = f.coordinator().Rollback(context.Background(), f.spec.OperationID)
	if err != nil || p.Status != SwitchRolledBack {
		t.Fatalf("rollback: %s, %v", p.Status, err)
	}
	f.want(t, "isolated:target", "source:blocked", "source:blocked", "source:frozen", "public:source")
}

func TestCoordinatorJournalFailureAtSourceBarrier(t *testing.T) {
	for _, status := range []SwitchStatus{SwitchPrepared, SwitchSourceBlocked} {
		t.Run(string(status), func(t *testing.T) {
			f := newSwitchFixture()
			f.failSave = func(p SwitchProgress) bool {
				return p.Status == status && (status != SwitchPrepared || p.Pending == blockSource)
			}
			if _, err := f.coordinator().Start(context.Background(), f.spec); !errors.Is(err, errInjected) {
				t.Fatal(err)
			}
			if status == SwitchPrepared {
				f.want(t, "isolated:target")
			} else {
				f.want(t, "isolated:target", "source:blocked")
			}
			f.failSave = nil
			if p, err := f.coordinator().Forward(context.Background(), f.spec.OperationID); err != nil || p.Status != SwitchComplete {
				t.Fatalf("resume: %s, %v", p.Status, err)
			}
			f.want(t, "isolated:target", "source:blocked", "isolated:target", "target:frozen", "traffic:target", "public:target")
		})
	}
}

var errInjected = errors.New("injected failure")

type switchFixture struct {
	spec      SwitchSpec
	nodes     map[string]*switchNodeFake
	traffic   TrafficState
	events    []string
	fail      string
	failAfter bool
	hideAfter string
	hidden    string
	saved     *SwitchProgress
	failSave  func(SwitchProgress) bool
}

type switchNodeFake struct {
	f     *switchFixture
	state NodeSnapshot
}

func newSwitchFixture() *switchFixture {
	old, next := netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2")
	f := &switchFixture{spec: SwitchSpec{OperationID: "switch-1", Hosts: []string{apiHost, appHost},
		Source: SwitchNode{ID: "source", ClientID: "old-client", ManagementHost: "old.example.test", Generations: map[string]int64{appHost: 2, apiHost: 5}, Addresses: []netip.Addr{old}},
		Target: SwitchNode{ID: "target", ClientID: "new-client", ManagementHost: "new.example.test", Generations: map[string]int64{appHost: 4, apiHost: 8}, Addresses: []netip.Addr{next}},
	}, traffic: TrafficState{Generation: 7, Addresses: []netip.Addr{old}}}
	f.nodes = make(map[string]*switchNodeFake)
	for _, n := range []SwitchNode{f.spec.Source, f.spec.Target} {
		phase := store.PublicationFrozen
		if n.ID == "target" {
			phase = store.PublicationBlocked
		}
		snap := NodeSnapshot{ID: n.ID, ManagementHost: n.ManagementHost, Gates: make(map[string]store.PublicationGate)}
		for h, generation := range n.Generations {
			snap.Gates[h] = store.PublicationGate{PublicationPolicy: store.PublicationPolicy{Phase: phase, OperationID: f.spec.OperationID, ClientID: n.ClientID}, Generation: generation}
			snap.Routes = append(snap.Routes, domain.Route{ID: n.ID + h, ClientID: n.ClientID, Domain: h, Protocol: domain.ProtocolHTTPS, Enabled: true, LocalHost: "127.0.0.1", LocalPort: 8080})
		}
		snap.Routes = append(snap.Routes, domain.Route{ID: n.ID + "prefix", ClientID: n.ClientID, Domain: appHost, PathPrefix: "/admin", StripPath: true, Protocol: domain.ProtocolHTTPS, Enabled: false, LocalHost: "127.0.0.1", LocalPort: 8081})
		f.nodes[n.ID] = &switchNodeFake{f: f, state: snap}
	}
	return f
}

func (f *switchFixture) coordinator() *Coordinator {
	return NewCoordinator(f, f, f, f)
}

func (f *switchFixture) WithOperation(_ context.Context, _ string, run func(*SwitchProgress, func(SwitchProgress) error) error) error {
	return run(f.saved, func(p SwitchProgress) error {
		if f.failSave != nil && f.failSave(p) {
			return errInjected
		}
		f.saved = &p
		return nil
	})
}

func (f *switchFixture) InspectNode(_ context.Context, id string, _ []string) (NodeSnapshot, error) {
	if f.hidden == id {
		return NodeSnapshot{}, errInjected
	}
	return f.nodes[id].state, nil
}

func (f *switchFixture) CompareNode(_ context.Context, id string, group []store.PublicationGateExpectation, policy store.PublicationPolicy) error {
	return f.changeGates(id, group, policy, false)
}

func (f *switchFixture) RefenceNode(_ context.Context, id string, group []store.PublicationGateExpectation, policy store.PublicationPolicy) error {
	return f.changeGates(id, group, policy, true)
}

func (f *switchFixture) changeGates(id string, group []store.PublicationGateExpectation, policy store.PublicationPolicy, refence bool) error {
	n := f.nodes[id]
	event := id + ":" + string(policy.Phase)
	if refence {
		event = id + ":refence"
		if f.saved == nil || f.saved.Status != SwitchRestoring || f.saved.Pending != refenceTarget {
			return errors.New("re-fence without persisted rollback intent")
		}
	}
	f.events = append(f.events, event)
	if f.saved == nil || f.saved.Pending == "" {
		return errors.New("write without persisted intent")
	}
	if f.fail == event && !f.failAfter {
		return errInjected
	}
	if len(group) != 2 {
		return errors.New("unexpected gate scope")
	}
	for _, g := range group {
		if n.state.Gates[g.Host] != g.Expected || (refence && (policy.Phase != store.PublicationBlocked || policy != g.Expected.PublicationPolicy)) {
			return errors.New("gate CAS conflict")
		}
	}
	if policy.Phase == store.PublicationFrozen {
		other := "source"
		if id == "source" {
			other = "target"
		}
		for _, g := range f.nodes[other].state.Gates {
			if g.Phase != store.PublicationBlocked {
				return errors.New("other node was not blocked")
			}
		}
	}
	for _, g := range group {
		n.state.Gates[g.Host] = store.PublicationGate{PublicationPolicy: policy, Generation: g.Expected.Generation + 1}
	}
	if f.hideAfter == event {
		f.hidden = id
	}
	if f.fail == event {
		return errInjected
	}
	return nil
}

func (f *switchFixture) InspectTraffic(_ context.Context, hosts []string) (TrafficState, error) {
	if !reflect.DeepEqual(hosts, f.spec.Hosts) {
		return TrafficState{}, errors.New("unexpected traffic scope")
	}
	if f.hidden == "traffic" {
		return TrafficState{}, errInjected
	}
	return f.traffic, nil
}

func (f *switchFixture) CompareTraffic(_ context.Context, hosts []string, expected TrafficState, addresses []netip.Addr) error {
	if !reflect.DeepEqual(hosts, f.spec.Hosts) || !reflect.DeepEqual(expected, f.traffic) {
		return errors.New("traffic CAS conflict")
	}
	if f.saved == nil || f.saved.Pending == "" {
		return errors.New("traffic write without persisted intent")
	}
	id := "source"
	if reflect.DeepEqual(addresses, f.spec.Target.Addresses) {
		id = "target"
	}
	event := "traffic:" + id
	f.events = append(f.events, event)
	if f.fail == event && !f.failAfter {
		return errInjected
	}
	f.traffic = TrafficState{Generation: f.traffic.Generation + 1, Addresses: addresses}
	if f.hideAfter == event {
		f.hidden = "traffic"
	}
	if f.fail == event {
		return errInjected
	}
	return nil
}

func (f *switchFixture) VerifyHTTPS(_ context.Context, node SwitchNode, hosts []string, routes []MappingIdentity, mode ProbeMode) error {
	if len(routes) != 3 {
		return errors.New("incomplete route verification scope")
	}
	if !reflect.DeepEqual(hosts, f.spec.Hosts) {
		return errors.New("unexpected verification scope")
	}
	event := string(mode) + ":" + node.ID
	f.events = append(f.events, event)
	if f.fail == event {
		return errInjected
	}
	if mode == ProbePublic && !reflect.DeepEqual(f.traffic.Addresses, node.Addresses) {
		return errors.New("public traffic does not select expected node")
	}
	return nil
}

func (f *switchFixture) want(t *testing.T, events ...string) {
	t.Helper()
	if !reflect.DeepEqual(f.events, events) {
		t.Fatalf("events %v, want %v", f.events, events)
	}
}
