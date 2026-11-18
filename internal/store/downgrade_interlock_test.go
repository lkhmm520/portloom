package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
)

const legacyMigrationWrite = `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));`
const downgradeDiagnostic = "PortLoom: publication-gated database; restart with a gate-aware Server"

func TestPublicationDowngradeInterlock(t *testing.T) {
	ctx := context.Background()
	s, path, opts := gateStore(t)
	a := gateAgent(t, s, "interlock-owner")
	r := gateRoute(t, s, a.ID, "gated.example.test", "", true)
	group := []PublicationGateExpectation{{Host: r.Domain}}
	policy := PublicationPolicy{Phase: PublicationBlocked, OperationID: "cutover", ClientID: a.ID}
	if _, err := s.db.ExecContext(ctx, legacyMigrationWrite); err != nil {
		t.Fatalf("unused gate schema rejected legacy startup: %v", err)
	}
	if err := s.TransitionPublicationGates(ctx, testManagementHost, group, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, legacyMigrationWrite); err == nil || !strings.Contains(err.Error(), downgradeDiagnostic) {
		t.Fatalf("legacy startup on blocked database: got %v, want downgrade refusal", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, opts)
	if err != nil {
		t.Fatalf("gate-aware restart: %v", err)
	}
	defer s.Close()
	group[0].Expected = PublicationGate{PublicationPolicy: policy, Generation: 1}
	if got, err := s.GetPublicationGate(ctx, r.Domain); got != group[0].Expected || err != nil {
		t.Fatalf("gate after restart: %+v, %v", got, err)
	}
	policy.Phase = PublicationOpen
	if err := s.TransitionPublicationGates(ctx, testManagementHost, group, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, legacyMigrationWrite); err == nil || !strings.Contains(err.Error(), downgradeDiagnostic) {
		t.Fatalf("legacy startup after release: got %v, want downgrade refusal", err)
	}
}

func TestPublicationDowngradeAtomicFailure(t *testing.T) {
	ctx := context.Background()
	s, _, _ := gateStore(t)
	a := gateAgent(t, s, "atomic-interlock-owner")
	r := gateRoute(t, s, a.ID, "gated.example.test", "", true)
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_gate_insert BEFORE INSERT ON publication_gates
		BEGIN SELECT RAISE(ABORT, 'injected gate write failure'); END`); err != nil {
		t.Fatal(err)
	}
	err := s.TransitionPublicationGates(ctx, testManagementHost, []PublicationGateExpectation{{Host: r.Domain}},
		PublicationPolicy{Phase: PublicationFrozen, OperationID: "cutover", ClientID: a.ID})
	if err == nil || !strings.Contains(err.Error(), "injected gate write failure") {
		t.Fatalf("gate write did not fail as injected: %v", err)
	}
	if got, err := s.GetPublicationGate(ctx, r.Domain); err != nil || got != (PublicationGate{}) {
		t.Fatalf("failed transition persisted a gate: %+v %v", got, err)
	}
	if _, err := s.db.ExecContext(ctx, legacyMigrationWrite); err != nil {
		t.Fatalf("failed transition persisted the downgrade interlock: %v", err)
	}
}

func TestPublicationDowngradeBackfill(t *testing.T) {
	ctx := context.Background()
	for _, phase := range []PublicationPhase{PublicationOpen, PublicationFrozen, PublicationBlocked} {
		t.Run("phase="+string(phase), func(t *testing.T) {
			s, path, opts := gateStore(t)
			policy := PublicationPolicy{Phase: phase}
			if phase != PublicationOpen {
				policy.OperationID, policy.ClientID = "cutover", "legacy-owner"
			}
			// Write the old database shape without invoking the guarded transition.
			const host = "gated.example.test"
			if _, err := s.db.ExecContext(ctx, `INSERT INTO publication_gates
				(host, phase, operation_id, client_id, generation, last_transition) VALUES (?, ?, ?, ?, 2, X'00')`,
				host, phase, policy.OperationID, policy.ClientID); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err := Open(path, opts)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if got, err := s.GetPublicationGate(ctx, host); err != nil || got != (PublicationGate{PublicationPolicy: policy, Generation: 2}) {
				t.Fatalf("backfill changed the gate: %+v %v", got, err)
			}
			if _, err := s.db.ExecContext(ctx, legacyMigrationWrite); err == nil || !strings.Contains(err.Error(), downgradeDiagnostic) {
				t.Fatalf("legacy startup after backfill: got %v, want downgrade refusal", err)
			}
		})
	}
}

func TestPublicationDowngradeExactPreviousServer(t *testing.T) {
	binary := os.Getenv("PORTLOOM_TEST_V046_SERVER")
	if binary == "" {
		t.Skip("run tests/downgrade_interlock_test.sh to build and test the exact previous Server")
	}
	ctx := context.Background()
	s, path, opts := gateStore(t)
	a := gateAgent(t, s, "legacy-binary-owner")
	r := gateRoute(t, s, a.ID, "gated.example.test", "", true)
	if err := s.Heartbeat(ctx, a.ID, r.DesiredRevision, []domain.RouteObservation{{RouteID: r.ID, ObservedRevision: r.DesiredRevision, TunnelStatus: "up"}}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetRoute(ctx, r.ID); err != nil || !got.PublicationReady() {
		t.Fatalf("fixture route is not ready before blocking: %+v %v", got, err)
	}
	runLegacy := func(limit time.Duration) (string, error) {
		t.Helper()
		runCtx, cancel := context.WithTimeout(ctx, limit)
		defer cancel()
		cmd := exec.CommandContext(runCtx, binary)
		cmd.Env = []string{"TM_DATABASE_PATH=" + path, "TM_WEB_DIR=" + t.TempDir(),
			"TM_ADMIN_TOKEN=disposable-interlock-token", "TM_LISTEN_ADDR=127.0.0.1:0", "TM_GATEWAY_ADDR=127.0.0.1:0",
			"TM_TCP_EDGE_BIND_HOST=127.0.0.1"}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if out, _ := runLegacy(time.Second); !strings.Contains(out, "control listening on") || strings.Contains(out, "server stopped:") {
		t.Fatalf("previous Server failed positive-control startup: %s", out)
	}
	s, err := Open(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TransitionPublicationGates(ctx, testManagementHost, []PublicationGateExpectation{{Host: r.Domain}},
		PublicationPolicy{Phase: PublicationBlocked, OperationID: "cutover", ClientID: a.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := runLegacy(3 * time.Second)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(out, downgradeDiagnostic) || strings.Contains(out, "control listening on") {
		t.Fatalf("previous Server must exit 1 before listeners: err=%v\n%s", err, out)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed downgrade changed the database: %v", err)
	}
	s, err = Open(path, opts)
	if err != nil {
		t.Fatalf("gate-aware restart after refused downgrade: %v", err)
	}
	defer s.Close()
	if got, err := s.GetPublicationGate(ctx, r.Domain); err != nil || got.Phase != PublicationBlocked || got.Generation != 1 {
		t.Fatalf("refused downgrade changed the gate: %+v, %v", got, err)
	}
	if got, err := s.GetRoute(ctx, r.ID); err != nil || !got.Enabled {
		t.Fatalf("refused downgrade changed the route: %+v, %v", got, err)
	}
}
