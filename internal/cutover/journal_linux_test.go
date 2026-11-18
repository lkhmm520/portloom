package cutover

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/store"
)

func privateJournalDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "journal")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFileJournalLocksAndFailsClosedOnCorruptRecord(t *testing.T) {
	ctx := context.Background()
	dir := privateJournalDir(t)
	j, err := OpenFileJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	other, err := OpenFileJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	f := newSwitchFixture()
	p, err := f.coordinator().prepare(ctx, f.spec)
	if err != nil {
		t.Fatal(err)
	}
	p.Pending = blockSource
	var escaped func(SwitchProgress) error
	if err := j.WithOperation(ctx, p.Spec.OperationID, func(cur *SwitchProgress, save func(SwitchProgress) error) error {
		if cur != nil {
			t.Fatal("unexpected existing record")
		}
		escaped = save
		if err := save(p); err != nil {
			return err
		}
		waiting, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		if err := other.WithOperation(waiting, p.Spec.OperationID, func(*SwitchProgress, func(SwitchProgress) error) error { t.Error("lock was not exclusive"); return nil }); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("wait error: %v", err)
		}
		return errInjected
	}); !errors.Is(err, errInjected) {
		t.Fatalf("callback error: %v", err)
	}
	if err := escaped(p); err == nil {
		t.Fatal("escaped Save remained usable")
	}
	if err := other.WithOperation(ctx, p.Spec.OperationID, func(cur *SwitchProgress, _ func(SwitchProgress) error) error {
		if cur == nil || !reflect.DeepEqual(*cur, p) {
			t.Fatalf("persisted progress: %+v", cur)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	records, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(records) != 1 {
		t.Fatalf("records: %v %v", records, err)
	}
	if info, err := os.Stat(records[0]); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
	if err := os.WriteFile(records[0], []byte(`{"version":1,"progress":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := j.WithOperation(ctx, p.Spec.OperationID, func(*SwitchProgress, func(SwitchProgress) error) error {
		t.Error("corruption was treated as progress or absence")
		return nil
	}); err == nil {
		t.Fatal("corrupt record accepted")
	}
}

func TestFileJournalProcessExitReleasesLockAndPreservesIntent(t *testing.T) {
	const env = "PORTLOOM_TEST_JOURNAL_DIR"
	ctx := context.Background()
	f := newSwitchFixture()
	if dir := os.Getenv(env); dir != "" {
		j, err := OpenFileJournal(dir)
		if err != nil {
			t.Fatal(err)
		}
		p, err := f.coordinator().prepare(ctx, f.spec)
		if err != nil {
			t.Fatal(err)
		}
		p.Pending = blockSource
		if err := j.WithOperation(ctx, p.Spec.OperationID, func(_ *SwitchProgress, save func(SwitchProgress) error) error {
			if err := save(p); err != nil {
				return err
			}
			fmt.Println("intent saved")
			// Simulate death without unwinding the journal callback or closing its FD.
			os.Exit(0)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return
	}
	dir := privateJournalDir(t)
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	child := exec.CommandContext(bounded, os.Args[0], "-test.run=^TestFileJournalProcessExitReleasesLockAndPreservesIntent$")
	child.Env = append(os.Environ(), env+"="+dir)
	out, err := child.Output()
	if err != nil || !bytes.HasPrefix(out, []byte("intent saved\n")) {
		t.Fatalf("child failed: %v (%s)", err, out)
	}
	j, err := OpenFileJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.WithOperation(bounded, f.spec.OperationID, func(cur *SwitchProgress, _ func(SwitchProgress) error) error {
		if cur == nil || cur.Status != SwitchPrepared || cur.Pending != blockSource {
			t.Fatalf("missing intent after process exit: %+v", cur)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFileJournalRejectsUnsafePaths(t *testing.T) {
	dir := privateJournalDir(t)
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFileJournal(link); err == nil {
		t.Fatal("symlink directory accepted")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFileJournal(dir); err == nil {
		t.Fatal("public directory accepted")
	}
}

func heldGateTestServers(t *testing.T, ctx context.Context, f *switchFixture) (*store.Store, *store.Store, http.Handler, http.Handler) {
	t.Helper()
	sourceDB, sourceClient, sourceHandler := gateTestServer(t, f.spec.Source.ManagementHost)
	targetDB, targetClient, targetHandler := gateTestServer(t, f.spec.Target.ManagementHost)
	f.spec.Source.ClientID, f.spec.Target.ClientID = sourceClient, targetClient
	for _, v := range []struct {
		db    *store.Store
		node  *SwitchNode
		phase store.PublicationPhase
	}{{sourceDB, &f.spec.Source, store.PublicationFrozen}, {targetDB, &f.spec.Target, store.PublicationBlocked}} {
		group := make([]store.PublicationGateExpectation, 0, len(f.spec.Hosts))
		for _, h := range f.spec.Hosts {
			group = append(group, store.PublicationGateExpectation{Host: h})
			v.node.Generations[h] = 1
		}
		if err := v.db.TransitionPublicationGates(ctx, v.node.ManagementHost, group, policy(f.spec, *v.node, v.phase)); err != nil {
			t.Fatal(err)
		}
	}
	return sourceDB, targetDB, sourceHandler, targetHandler
}

func TestCoordinatorRecoversCommittedGateWithRealAdapters(t *testing.T) {
	ctx := context.Background()
	f := newSwitchFixture()
	sourceDB, targetDB, sourceHandler, targetHandler := heldGateTestServers(t, ctx, f)
	// Commit the real CAS, then cancel the client request and its ensuing readback.
	firstCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceHandler.ServeHTTP(w, r)
		if r.URL.Path == "/api/v1/cutover/gates/compare" {
			cancel()
		}
	}))
	defer source.Close()
	target := httptest.NewTLSServer(targetHandler)
	defer target.Close()
	path := gateTestConfig(t, map[string]*httptest.Server{"source": source, "target": target}, map[string]string{"source": f.spec.Source.ManagementHost, "target": f.spec.Target.ManagementHost})
	nodes, err := OpenGateNodes(path, "source", "target")
	if err != nil {
		t.Fatal(err)
	}
	defer nodes.CloseIdleConnections()
	dir := privateJournalDir(t)
	journal, err := OpenFileJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	traffic := &fixtureTraffic{state: f.traffic}
	c := NewCoordinator(nodes, traffic, fixtureHTTPS{}, journal)
	p, err := c.Start(firstCtx, f.spec)
	if !errors.Is(err, ErrSwitchUnsettled) || p.Pending != blockSource {
		t.Fatalf("progress=%+v err=%v", p, err)
	}
	g, err := sourceDB.GetPublicationGate(ctx, appHost)
	if err != nil || g.Phase != store.PublicationBlocked || g.Generation != 2 {
		t.Fatalf("source CAS didn't commit: %+v %v", g, err)
	}
	journal, err = OpenFileJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	c = NewCoordinator(nodes, traffic, fixtureHTTPS{}, journal)
	p, err = c.SettlePending(ctx, f.spec.OperationID)
	if err != nil || p.Pending != "" || p.Status != SwitchSourceBlocked {
		t.Fatalf("resumed progress=%+v err=%v", p, err)
	}
	g, err = targetDB.GetPublicationGate(ctx, appHost)
	if err != nil || g.Phase != store.PublicationBlocked || g.Generation != 1 {
		t.Fatalf("settlement promoted target: %+v %v", g, err)
	}
	p, err = c.Forward(ctx, f.spec.OperationID)
	if err != nil || p.Status != SwitchComplete {
		t.Fatalf("forward progress=%+v err=%v", p, err)
	}
	p, err = c.Rollback(ctx, f.spec.OperationID)
	if err != nil || p.Status != SwitchRolledBack {
		t.Fatalf("rollback progress=%+v err=%v", p, err)
	}
	for _, host := range f.spec.Hosts {
		src, srcErr := sourceDB.GetPublicationGate(ctx, host)
		dst, dstErr := targetDB.GetPublicationGate(ctx, host)
		if srcErr != nil || dstErr != nil || src.Phase != store.PublicationFrozen || dst.Phase != store.PublicationBlocked || src.Generation != 3 || dst.Generation != 3 {
			t.Fatalf("rollback gates for %s: %v %v (%v %v)", host, src, dst, srcErr, dstErr)
		}
	}
}

func TestCoordinatorFencesPendingPromotionWithRealAdapters(t *testing.T) {
	for _, lateWins := range []bool{false, true} {
		t.Run(fmt.Sprintf("late_promotion_wins=%t", lateWins), func(t *testing.T) {
			ctx := context.Background()
			f := newSwitchFixture()
			sourceDB, targetDB, sourceHandler, targetHandler := heldGateTestServers(t, ctx, f)
			wantTargetGeneration := int64(2)
			if lateWins {
				wantTargetGeneration++
			}
			source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/cutover/gates/compare" {
					src, err := sourceDB.GetPublicationGate(r.Context(), appHost)
					if err != nil {
						t.Error(err)
					}
					if src.Phase == store.PublicationBlocked { // Source restore dispatch.
						for _, host := range f.spec.Hosts {
							dst, err := targetDB.GetPublicationGate(r.Context(), host)
							if err != nil || dst.Phase != store.PublicationBlocked || dst.Generation != wantTargetGeneration {
								t.Errorf("source restore preceded target fence for %s: %v %v", host, dst, err)
							}
						}
					}
				}
				sourceHandler.ServeHTTP(w, r)
			}))
			defer source.Close()
			target := httptest.NewTLSServer(targetHandler)
			defer target.Close()
			path := gateTestConfig(t, map[string]*httptest.Server{"source": source, "target": target}, map[string]string{"source": f.spec.Source.ManagementHost, "target": f.spec.Target.ManagementHost})
			nodes, err := OpenGateNodes(path, "source", "target")
			if err != nil {
				t.Fatal(err)
			}
			defer nodes.CloseIdleConnections()
			dir := privateJournalDir(t)
			reopen := func() *FileJournal {
				j, err := OpenFileJournal(dir)
				if err != nil {
					t.Fatal(err)
				}
				return j
			}
			firstCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			j := cancelOnIntent{reopen(), promoteTarget, cancel}
			traffic := &fixtureTraffic{state: f.traffic}
			p, err := NewCoordinator(nodes, traffic, f, j).Start(firstCtx, f.spec)
			if !errors.Is(err, context.Canceled) || p.Pending != promoteTarget {
				t.Fatalf("interrupted promotion: %s/%s, %v", p.Status, p.Pending, err)
			}
			group := make([]store.PublicationGateExpectation, 0, len(f.spec.Hosts))
			for _, host := range f.spec.Hosts {
				group = append(group, store.PublicationGateExpectation{Host: host, Expected: p.TargetGates[host]})
			}
			promotion := policy(f.spec, f.spec.Target, store.PublicationFrozen)
			f.fail, f.events = "isolated:target", nil
			rollbackCtx, loseReadback := context.WithCancel(ctx)
			defer loseReadback()
			fault := &refenceFault{GateNodes: nodes, hook: func() {
				if lateWins {
					// The delayed HTTP CAS lands after the coordinator has read
					// the old state but before the re-fence reaches SQLite.
					if err := nodes.CompareNode(ctx, "target", group, promotion); err != nil {
						t.Fatalf("delayed promotion did not win: %v", err)
					}
				}
			}, after: loseReadback}
			p, err = NewCoordinator(fault, traffic, f, reopen()).Rollback(rollbackCtx, f.spec.OperationID)
			if !errors.Is(err, ErrSwitchUnsettled) || p.Status != SwitchRestoring || p.Pending != refenceTarget {
				t.Fatalf("lost readback: %s/%s, %v", p.Status, p.Pending, err)
			}
			for _, host := range f.spec.Hosts {
				src, srcErr := sourceDB.GetPublicationGate(ctx, host)
				dst, dstErr := targetDB.GetPublicationGate(ctx, host)
				wantTarget := store.PublicationBlocked
				if lateWins {
					wantTarget = store.PublicationFrozen
				}
				if srcErr != nil || dstErr != nil || src.Phase != store.PublicationBlocked || dst.Phase != wantTarget || src.Generation != 2 || dst.Generation != 2 {
					t.Fatalf("unconfirmed barrier for %s: %v %v (%v %v)", host, src, dst, srcErr, dstErr)
				}
			}
			c := NewCoordinator(nodes, traffic, f, reopen())
			if _, err := c.Forward(ctx, f.spec.OperationID); !errors.Is(err, ErrSwitchConflict) {
				t.Fatalf("forward after persisted rollback: %v", err)
			}
			p, err = c.Rollback(ctx, f.spec.OperationID)
			if err != nil || p.Status != SwitchRolledBack || p.Pending != "" || !reflect.DeepEqual(p.Traffic, f.traffic) {
				t.Fatalf("retried rollback: %s/%s traffic=%v, %v", p.Status, p.Pending, p.Traffic, err)
			}
			f.want(t, "public:source")
			if err := nodes.CompareNode(ctx, "target", group, promotion); !errors.Is(err, ErrSwitchConflict) {
				t.Fatalf("old promotion not fenced after restoring source: %v", err)
			}
			for _, host := range f.spec.Hosts {
				src, srcErr := sourceDB.GetPublicationGate(ctx, host)
				dst, dstErr := targetDB.GetPublicationGate(ctx, host)
				if srcErr != nil || dstErr != nil || src != p.SourceGates[host] || dst != p.TargetGates[host] || src.Phase != store.PublicationFrozen || dst.Phase != store.PublicationBlocked || src.Generation != 3 || dst.Generation != wantTargetGeneration {
					t.Fatalf("restored gates for %s: %v %v (%v %v)", host, src, dst, srcErr, dstErr)
				}
			}
		})
	}
}

type cancelOnIntent struct {
	SwitchJournal
	action SwitchAction
	cancel func()
}

func (j cancelOnIntent) WithOperation(ctx context.Context, id string, run func(*SwitchProgress, func(SwitchProgress) error) error) error {
	return j.SwitchJournal.WithOperation(ctx, id, func(current *SwitchProgress, save func(SwitchProgress) error) error {
		return run(current, func(p SwitchProgress) error {
			err := save(p)
			if err == nil && p.Pending == j.action {
				j.cancel()
			}
			return err
		})
	})
}

type refenceFault struct {
	*GateNodes
	hook, after func()
}

func (n *refenceFault) RefenceNode(ctx context.Context, id string, group []store.PublicationGateExpectation, blocked store.PublicationPolicy) error {
	n.hook()
	_ = n.GateNodes.RefenceNode(ctx, id, group, blocked)
	n.after()
	return context.Canceled
}

type fixtureTraffic struct{ state TrafficState }

func (f *fixtureTraffic) InspectTraffic(context.Context, []string) (TrafficState, error) {
	return f.state, nil
}

func (f *fixtureTraffic) CompareTraffic(_ context.Context, _ []string, expected TrafficState, addresses []netip.Addr) error {
	if f.state.Generation != expected.Generation || !slices.Equal(f.state.Addresses, expected.Addresses) {
		return ErrSwitchConflict
	}
	f.state = TrafficState{Generation: expected.Generation + 1, Addresses: slices.Clone(addresses)}
	return nil
}

// No claim about the production HTTPS data path: this fixture isolates gate
// and journal behavior while the coordinator drives their real implementations.
type fixtureHTTPS struct{}

func (fixtureHTTPS) VerifyHTTPS(context.Context, SwitchNode, []string, []MappingIdentity, ProbeMode) error {
	return nil
}
