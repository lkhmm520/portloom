package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
)

func (s *Store) GetRoute(ctx context.Context, id string) (domain.Route, error) {
	return getRoute(ctx, s.db, id)
}

func (s *Store) HTTPDomainEnabled(ctx context.Context, domainName string) (bool, error) {
	var enabled bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM routes INDEXED BY routes_http_domain
		WHERE protocol = 'http' AND domain = ? AND enabled = 1
	)`, domainName).Scan(&enabled)
	if err != nil {
		return false, fmt.Errorf("check enabled HTTP domain: %w", err)
	}
	return enabled, nil
}

func (s *Store) ListRoutes(ctx context.Context) ([]domain.Route, error) {
	rows, err := s.db.QueryContext(ctx, routeSelect+` ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}
	defer rows.Close()
	routes := make([]domain.Route, 0)
	for rows.Next() {
		route, err := scanRoute(rows)
		if err != nil {
			return nil, fmt.Errorf("list routes: %w", err)
		}
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}
	return routes, nil
}

func (s *Store) CreateRoute(ctx context.Context, route domain.Route) (domain.Route, error) {
	if err := route.Validate(); err != nil {
		return domain.Route{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Route{}, fmt.Errorf("begin create route: %w", err)
	}
	defer tx.Rollback()

	revision, err := bumpDesiredRevision(ctx, tx, route.ClientID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Route{}, ErrNotFound
	}
	if err != nil {
		return domain.Route{}, fmt.Errorf("advance desired revision: %w", err)
	}
	remotePort, err := s.allocatePort(ctx, tx)
	if err != nil {
		return domain.Route{}, err
	}
	now := time.Now().UTC()
	routeID, err := randomID()
	if err != nil {
		return domain.Route{}, err
	}
	route.ID = routeID
	route.RemotePort = remotePort
	route.DesiredRevision = revision
	route.ObservedRevision = 0
	route.LocalStatus = ""
	route.TunnelStatus = ""
	route.LastError = ""
	route.CreatedAt = now
	route.UpdatedAt = now

	_, err = tx.ExecContext(ctx, `INSERT INTO routes
		(id, client_id, name, protocol, domain, local_host, local_port, remote_port,
		 public_port, tunnel_group, enabled, desired_revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		route.ID, route.ClientID, route.Name, route.Protocol, route.Domain, route.LocalHost,
		route.LocalPort, route.RemotePort, route.PublicPort, route.TunnelGroup, route.Enabled,
		route.DesiredRevision, formatTime(now), formatTime(now))
	if err != nil {
		return domain.Route{}, mapConstraintError("create route", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Route{}, fmt.Errorf("commit create route: %w", err)
	}
	return route, nil
}

func (s *Store) UpdateRoute(ctx context.Context, id string, update domain.Route) (domain.Route, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Route{}, fmt.Errorf("begin update route: %w", err)
	}
	defer tx.Rollback()

	current, err := getRoute(ctx, tx, id)
	if err != nil {
		return domain.Route{}, err
	}
	update.ID = current.ID
	update.ClientID = current.ClientID
	update.RemotePort = current.RemotePort
	update.CreatedAt = current.CreatedAt
	update.ObservedRevision = current.ObservedRevision
	update.LocalStatus = current.LocalStatus
	update.TunnelStatus = current.TunnelStatus
	update.LastError = current.LastError
	if err := update.Validate(); err != nil {
		return domain.Route{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	revision, err := bumpDesiredRevision(ctx, tx, current.ClientID)
	if err != nil {
		return domain.Route{}, fmt.Errorf("advance desired revision: %w", err)
	}
	update.DesiredRevision = revision
	update.UpdatedAt = time.Now().UTC()
	_, err = tx.ExecContext(ctx, `UPDATE routes SET name=?, protocol=?, domain=?, local_host=?,
		local_port=?, public_port=?, tunnel_group=?, enabled=?, desired_revision=?, updated_at=? WHERE id=?`,
		update.Name, update.Protocol, update.Domain, update.LocalHost, update.LocalPort,
		update.PublicPort, update.TunnelGroup, update.Enabled, revision, formatTime(update.UpdatedAt), id)
	if err != nil {
		return domain.Route{}, mapConstraintError("update route", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Route{}, fmt.Errorf("commit update route: %w", err)
	}
	return update, nil
}

func (s *Store) DeleteRoute(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete route: %w", err)
	}
	defer tx.Rollback()
	current, err := getRoute(ctx, tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM routes WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete route: %w", err)
	}
	if _, err := bumpDesiredRevision(ctx, tx, current.ClientID); err != nil {
		return fmt.Errorf("advance desired revision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete route: %w", err)
	}
	return nil
}

func (s *Store) SyncAgent(ctx context.Context, agentID string) (domain.Agent, []domain.Route, error) {
	agent, err := s.getAgent(ctx, agentID)
	if err != nil {
		return domain.Agent{}, nil, err
	}
	rows, err := s.db.QueryContext(ctx, routeSelect+` WHERE client_id = ? ORDER BY remote_port`, agentID)
	if err != nil {
		return domain.Agent{}, nil, fmt.Errorf("list agent routes: %w", err)
	}
	defer rows.Close()
	var routes []domain.Route
	for rows.Next() {
		route, err := scanRoute(rows)
		if err != nil {
			return domain.Agent{}, nil, err
		}
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return domain.Agent{}, nil, fmt.Errorf("list agent routes: %w", err)
	}
	if routes == nil {
		routes = []domain.Route{}
	}
	return agent, routes, nil
}

func (s *Store) Heartbeat(ctx context.Context, agentID string, observedRevision int64, observations []domain.RouteObservation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin heartbeat: %w", err)
	}
	defer tx.Rollback()

	var desiredRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT desired_revision FROM agents WHERE id = ?`, agentID).Scan(&desiredRevision); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("get heartbeat revision: %w", err)
	}
	if observedRevision < 0 || observedRevision > desiredRevision {
		return fmt.Errorf("%w: observed revision %d is outside [0, %d]", ErrInvalid, observedRevision, desiredRevision)
	}
	for _, observation := range observations {
		if observation.ObservedRevision < 0 || observation.ObservedRevision > desiredRevision {
			return fmt.Errorf("%w: route observed revision %d is outside [0, %d]", ErrInvalid, observation.ObservedRevision, desiredRevision)
		}
	}

	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE agents SET
		observed_revision = CASE WHEN observed_revision < ? THEN ? ELSE observed_revision END,
		last_seen_at = ?, updated_at = ? WHERE id = ?`,
		observedRevision, observedRevision, formatTime(now), formatTime(now), agentID)
	if err != nil {
		return fmt.Errorf("update heartbeat: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return ErrNotFound
	}
	for _, observation := range observations {
		_, err := tx.ExecContext(ctx, `UPDATE routes SET
			observed_revision = ?, local_status = ?, tunnel_status = ?, last_error = ?, updated_at = ?
			WHERE id = ? AND client_id = ? AND desired_revision <= ? AND observed_revision <= ?`,
			observation.ObservedRevision, observation.LocalStatus, observation.TunnelStatus,
			observation.LastError, formatTime(now), observation.RouteID, agentID,
			observation.ObservedRevision, observation.ObservedRevision)
		if err != nil {
			return fmt.Errorf("update route observation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit heartbeat: %w", err)
	}
	return nil
}

func (s *Store) getAgent(ctx context.Context, id string) (domain.Agent, error) {
	var agent domain.Agent
	var createdAt, updatedAt string
	var lastSeen sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, name, desired_revision, observed_revision,
		last_seen_at, created_at, updated_at FROM agents WHERE id = ?`, id).Scan(
		&agent.ID, &agent.Name, &agent.DesiredRevision, &agent.ObservedRevision,
		&lastSeen, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Agent{}, ErrNotFound
	}
	if err != nil {
		return domain.Agent{}, fmt.Errorf("get agent: %w", err)
	}
	agent.CreatedAt = parseTime(createdAt)
	agent.UpdatedAt = parseTime(updatedAt)
	if lastSeen.Valid {
		agent.LastSeenAt = parseTime(lastSeen.String)
	}
	return agent, nil
}

func (s *Store) allocatePort(ctx context.Context, tx *sql.Tx) (int, error) {
	for port := s.options.PortRangeStart; port <= s.options.PortRangeEnd; port++ {
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM routes WHERE remote_port = ?)`, port).Scan(&exists)
		if err != nil {
			return 0, fmt.Errorf("check allocated port: %w", err)
		}
		if exists == 0 && loopbackPortAvailable(ctx, port) {
			return port, nil
		}
	}
	return 0, ErrPortRangeExhausted
}

func loopbackPortAvailable(ctx context.Context, port int) bool {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func bumpDesiredRevision(ctx context.Context, tx *sql.Tx, agentID string) (int64, error) {
	var revision int64
	err := tx.QueryRowContext(ctx, `UPDATE agents SET desired_revision = desired_revision + 1,
		updated_at = ? WHERE id = ? RETURNING desired_revision`, formatTime(time.Now()), agentID).Scan(&revision)
	return revision, err
}

const routeSelect = `SELECT id, client_id, name, protocol, domain, local_host, local_port,
	remote_port, public_port, tunnel_group, enabled, desired_revision, observed_revision,
	local_status, tunnel_status, last_error, created_at, updated_at FROM routes`

type rowScanner interface {
	Scan(dest ...any) error
}

func getRoute(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (domain.Route, error) {
	route, err := scanRoute(query.QueryRowContext(ctx, routeSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Route{}, ErrNotFound
	}
	return route, err
}

func scanRoute(scanner rowScanner) (domain.Route, error) {
	var route domain.Route
	var protocol string
	var createdAt, updatedAt string
	err := scanner.Scan(&route.ID, &route.ClientID, &route.Name, &protocol, &route.Domain,
		&route.LocalHost, &route.LocalPort, &route.RemotePort, &route.PublicPort,
		&route.TunnelGroup, &route.Enabled, &route.DesiredRevision, &route.ObservedRevision,
		&route.LocalStatus, &route.TunnelStatus, &route.LastError, &createdAt, &updatedAt)
	if err != nil {
		return domain.Route{}, err
	}
	route.Protocol = domain.Protocol(protocol)
	route.CreatedAt = parseTime(createdAt)
	route.UpdatedAt = parseTime(updatedAt)
	return route, nil
}

func mapConstraintError(operation string, err error) error {
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("%s: %w", operation, ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
