package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/lkhmm520/portloom/internal/domain"
)

type PublicationPhase string

const (
	PublicationOpen    PublicationPhase = ""        // normal publication and ordinary CRUD
	PublicationFrozen  PublicationPhase = "frozen"  // normal publication, no ordinary CRUD
	PublicationBlocked PublicationPhase = "blocked" // no publication or ordinary CRUD
)

type PublicationPolicy struct {
	Phase       PublicationPhase
	OperationID string
	ClientID    string
}

type PublicationGate struct {
	PublicationPolicy
	Generation int64
}

type PublicationGateExpectation struct {
	Host     string
	Expected PublicationGate
}

const publicationGateSchema = `CREATE TABLE IF NOT EXISTS publication_gates (
  host TEXT PRIMARY KEY NOT NULL,
  phase TEXT NOT NULL CHECK (phase IN ('', 'frozen', 'blocked')),
  operation_id TEXT NOT NULL,
  client_id TEXT NOT NULL,
  generation INTEGER NOT NULL CHECK (generation > 0),
  last_transition BLOB NOT NULL,
  CHECK ((phase = '' AND operation_id = '' AND client_id = '') OR
         (phase != '' AND operation_id != '' AND client_id != ''))
);`

// v0.4.6 attempts to insert migration 1 unconditionally on every startup,
// before synchronizing SSH authorization or opening any listeners. A BEFORE
// INSERT trigger aborts that statement even though it uses OR IGNORE and the
// migration is already recorded. Gate-aware startup inserts only missing rows.
// Keep the interlock after release: an open tombstone is not a safe rollback
// point for an older binary that ignores publication generations.
const publicationDowngradeInterlock = `CREATE TRIGGER IF NOT EXISTS publication_gate_downgrade_interlock
BEFORE INSERT ON schema_migrations WHEN NEW.version = 1
BEGIN
  SELECT RAISE(ABORT, 'PortLoom: publication-gated database; restart with a gate-aware Server');
END;`

func (s *Store) ensurePublicationGateSchema(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin publication schema migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, publicationGateSchema); err != nil {
		return fmt.Errorf("create publication gates table: %w", err)
	}
	// Also protect databases used by pre-interlock cutover builds on first open.
	var used bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM publication_gates)`).Scan(&used); err != nil {
		return fmt.Errorf("inspect publication gate history: %w", err)
	}
	if used {
		if _, err := tx.ExecContext(ctx, publicationDowngradeInterlock); err != nil {
			return fmt.Errorf("install publication downgrade interlock: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit publication schema migration: %w", err)
	}
	return nil
}

type gateReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readPublicationGate(ctx context.Context, q gateReader, host string) (PublicationGate, []byte, error) {
	var gate PublicationGate
	var last []byte
	err := q.QueryRowContext(ctx, `SELECT phase, operation_id, client_id, generation, last_transition
		FROM publication_gates WHERE host = ?`, host).Scan(&gate.Phase, &gate.OperationID, &gate.ClientID, &gate.Generation, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return PublicationGate{}, nil, nil
	}
	if err != nil {
		return PublicationGate{}, nil, fmt.Errorf("read publication gate: %w", err)
	}
	return gate, last, nil
}

// GetPublicationGate returns the versioned state of an exact DNS host. A host
// never gated before returns the zero value; release retains its generation.
func (s *Store) GetPublicationGate(ctx context.Context, host string) (PublicationGate, error) {
	host, ok := domain.NormalizeDNSHost(host)
	if !ok {
		return PublicationGate{}, fmt.Errorf("%w: expected exact DNS host", ErrInvalid)
	}
	gate, _, err := readPublicationGate(ctx, s.db, host)
	return gate, err
}

const MaxPublicationHosts = 64

// PublicationSnapshot reads the selected gates and all their route rows,
// including disabled routes and other owners, from one SQLite read transaction.
func (s *Store) PublicationSnapshot(ctx context.Context, hosts []string) (map[string]PublicationGate, []domain.Route, error) {
	if len(hosts) == 0 || len(hosts) > MaxPublicationHosts {
		return nil, nil, ErrInvalid
	}
	args := make([]any, len(hosts))
	for i, host := range hosts {
		if canonical, ok := domain.NormalizeDNSHost(host); !ok || host != canonical {
			return nil, nil, ErrInvalid
		}
		args[i] = host
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	gates := make(map[string]PublicationGate, len(hosts))
	for _, host := range hosts {
		gate, _, err := readPublicationGate(ctx, tx, host)
		if err != nil {
			return nil, nil, err
		}
		gates[host] = gate
	}
	rows, err := tx.QueryContext(ctx, routeSelect+` WHERE domain IN (?`+strings.Repeat(",?", len(args)-1)+`) ORDER BY domain, path_prefix, id`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	routes := []domain.Route{}
	for rows.Next() {
		route, err := scanRoute(rows)
		if err != nil {
			return nil, nil, err
		}
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return gates, routes, nil
}

// TransitionPublicationGates compares and changes the whole host group in one
// local SQLite transaction. The caller must supply the server's configured
// management host (not a request-derived hostname). Only groups of existing
// default HTTPS hosts entirely owned by next.ClientID are eligible. The same
// operation and client must own a freeze through release. Release still takes
// those credentials as identifiers, but persists an ownerless open tombstone.
// An exact retry succeeds only while every member still reflects that transition.
// No expiry is applied and no route or Agent desired state is changed.
func (s *Store) TransitionPublicationGates(ctx context.Context, managementHost string, group []PublicationGateExpectation, next PublicationPolicy) error {
	return s.transitionPublicationGates(ctx, managementHost, group, next, false)
}

// RefencePublicationGates advances the exact blocked group without changing its
// policy. Any delayed promotion using the old generations then loses its CAS.
// This is not a general same-phase transition and never accepts a mixed group.
func (s *Store) RefencePublicationGates(ctx context.Context, managementHost string, group []PublicationGateExpectation, blocked PublicationPolicy) error {
	return s.transitionPublicationGates(ctx, managementHost, group, blocked, true)
}

func (s *Store) transitionPublicationGates(ctx context.Context, managementHost string, group []PublicationGateExpectation, next PublicationPolicy, refence bool) error {
	managementHost, ok := domain.NormalizeDNSHost(managementHost)
	if !ok || len(group) == 0 || (refence && (len(group) > MaxPublicationHosts || next.Phase != PublicationBlocked)) || strings.TrimSpace(next.OperationID) == "" || strings.TrimSpace(next.ClientID) == "" ||
		!utf8.ValidString(next.OperationID) || !utf8.ValidString(next.ClientID) {
		return fmt.Errorf("%w: management host, group, operation and client are required", ErrInvalid)
	}
	switch next.Phase {
	case PublicationOpen, PublicationFrozen, PublicationBlocked:
	default:
		return fmt.Errorf("%w: unknown publication phase", ErrInvalid)
	}
	members := append([]PublicationGateExpectation(nil), group...)
	for i := range members {
		host, ok := domain.NormalizeDNSHost(members[i].Host)
		expected := members[i].Expected
		if !ok || host == managementHost || expected.Generation < 0 || expected.Generation == math.MaxInt64 ||
			!utf8.ValidString(expected.OperationID) || !utf8.ValidString(expected.ClientID) || !utf8.ValidString(string(expected.Phase)) ||
			(refence && (expected.Generation == 0 || expected.PublicationPolicy != next)) {
			return fmt.Errorf("%w: invalid publication gate member %q", ErrInvalid, members[i].Host)
		}
		members[i].Host = host
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Host < members[j].Host })
	for i := 1; i < len(members); i++ {
		if members[i-1].Host == members[i].Host {
			return fmt.Errorf("%w: duplicate publication gate host %q", ErrInvalid, members[i].Host)
		}
	}
	receipt, err := json.Marshal(struct {
		Members []PublicationGateExpectation
		Next    PublicationPolicy
	}{members, next})
	if err != nil {
		return fmt.Errorf("encode publication transition: %w", err)
	}
	digest := sha256.Sum256(receipt)
	policy := next
	if next.Phase == PublicationOpen {
		policy = PublicationPolicy{}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin publication transition: %w", err)
	}
	defer tx.Rollback()

	replayed := 0
	for _, member := range members {
		current, last, err := readPublicationGate(ctx, tx, member.Host)
		if err != nil {
			return err
		}
		if current == (PublicationGate{PublicationPolicy: policy, Generation: member.Expected.Generation + 1}) && bytes.Equal(last, digest[:]) {
			replayed++
			continue
		}
		if current != member.Expected || (!refence && current.Phase == next.Phase) ||
			(current.Phase != PublicationOpen && (current.OperationID != next.OperationID || current.ClientID != next.ClientID)) {
			return fmt.Errorf("publication gate %q: %w", member.Host, ErrConflict)
		}
		if err := validatePublicationHost(ctx, tx, member.Host, next.ClientID); err != nil {
			return err
		}
	}
	if replayed == len(members) {
		return nil
	}
	if replayed != 0 {
		return fmt.Errorf("partial publication transition retry: %w", ErrConflict)
	}
	// Commit the interlock and the gate together, or persist neither.
	if _, err := tx.ExecContext(ctx, publicationDowngradeInterlock); err != nil {
		return fmt.Errorf("install publication downgrade interlock: %w", err)
	}
	for _, member := range members {
		if _, err := tx.ExecContext(ctx, `INSERT INTO publication_gates
			(host, phase, operation_id, client_id, generation, last_transition) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(host) DO UPDATE SET phase=excluded.phase, operation_id=excluded.operation_id,
			client_id=excluded.client_id, generation=excluded.generation, last_transition=excluded.last_transition`,
			member.Host, policy.Phase, policy.OperationID, policy.ClientID, member.Expected.Generation+1, digest[:]); err != nil {
			return fmt.Errorf("write publication gate %q: %w", member.Host, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit publication transition: %w", err)
	}
	return nil
}

// The check and desired write share a transaction. If a concurrent SQLite
// writer wins after this read, upgrading a stale read transaction cannot commit.
func checkRouteNotFrozen(ctx context.Context, tx *sql.Tx, r domain.Route) error {
	if !r.Protocol.IsWeb() {
		return nil
	}
	var frozen bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM publication_gates WHERE host = ? AND phase != '')`, r.Domain).Scan(&frozen); err != nil {
		return fmt.Errorf("check publication freeze: %w", err)
	}
	if frozen {
		return fmt.Errorf("route host %q is frozen: %w", r.Domain, ErrConflict)
	}
	return nil
}

func validatePublicationHost(ctx context.Context, tx *sql.Tx, host, clientID string) error {
	var hasRoutes, invalidRoutes bool
	err := tx.QueryRowContext(ctx, `SELECT count(*) > 0,
		count(CASE WHEN client_id != ? OR protocol != 'https' OR public_port != 0 THEN 1 END) > 0
		FROM routes WHERE protocol IN ('http', 'https') AND domain = ?`, clientID, host).Scan(&hasRoutes, &invalidRoutes)
	if err != nil {
		return fmt.Errorf("validate publication host %q: %w", host, err)
	}
	if !hasRoutes || invalidRoutes {
		return fmt.Errorf("publication host %q must contain only default HTTPS routes owned by the selected client: %w", host, ErrConflict)
	}
	return nil
}
