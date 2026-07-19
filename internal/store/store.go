package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound               = errors.New("not found")
	ErrConflict               = errors.New("conflict")
	ErrInvalid                = errors.New("invalid input")
	ErrPortRangeExhausted     = errors.New("port range exhausted")
	ErrInvalidEnrollmentToken = errors.New("invalid or expired enrollment token")
	ErrInvalidAgentToken      = errors.New("invalid agent token")
)

type Options struct {
	PortRangeStart int
	PortRangeEnd   int
}

type Store struct {
	db        *sql.DB
	options   Options
	sshKeysMu sync.Mutex
}

func Open(path string, options Options) (*Store, error) {
	if options.PortRangeStart < 1 || options.PortRangeEnd > 65535 || options.PortRangeStart > options.PortRangeEnd {
		return nil, errors.New("invalid port range")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, options: options}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS enrollment_tokens (
  token_hash TEXT PRIMARY KEY,
  expires_at TEXT NOT NULL,
  expires_at_unix_nano INTEGER NOT NULL,
  used_at TEXT,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS agents (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  ssh_public_key TEXT NOT NULL DEFAULT '',
  desired_revision INTEGER NOT NULL DEFAULT 0,
  observed_revision INTEGER NOT NULL DEFAULT 0,
  last_seen_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS routes (
  id TEXT PRIMARY KEY,
  client_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  protocol TEXT NOT NULL,
  domain TEXT NOT NULL DEFAULT '',
  local_host TEXT NOT NULL,
  local_port INTEGER NOT NULL,
  remote_port INTEGER NOT NULL UNIQUE,
  public_port INTEGER NOT NULL DEFAULT 0,
  tunnel_group TEXT NOT NULL,
  enabled INTEGER NOT NULL,
  desired_revision INTEGER NOT NULL,
  observed_revision INTEGER NOT NULL DEFAULT 0,
  local_status TEXT NOT NULL DEFAULT '',
  tunnel_status TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS routes_http_domain
  ON routes(domain) WHERE protocol = 'http';
CREATE UNIQUE INDEX IF NOT EXISTS routes_public_port
  ON routes(public_port) WHERE public_port > 0;
INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate sqlite: %w", err)
	}
	if err := s.ensureEnrollmentExpiryNanos(ctx); err != nil {
		return err
	}
	if err := s.ensureAgentSSHKey(ctx); err != nil {
		return err
	}
	if err := s.ensureEnrollmentClaims(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureAgentSSHKey(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(agents)`)
	if err != nil {
		return fmt.Errorf("inspect agent schema: %w", err)
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect agent column: %w", err)
		}
		if name == "ssh_public_key" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate agent schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close agent schema rows: %w", err)
	}
	if !found {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE agents ADD COLUMN ssh_public_key TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add agent SSH key: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (3, ?)`, formatTime(time.Now().UTC())); err != nil {
		return fmt.Errorf("record agent SSH key migration: %w", err)
	}
	return nil
}

func (s *Store) ensureEnrollmentClaims(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(enrollment_tokens)`)
	if err != nil {
		return fmt.Errorf("inspect enrollment claim schema: %w", err)
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect enrollment claim column: %w", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate enrollment claim schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close enrollment claim schema: %w", err)
	}
	if !columns["request_id"] {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE enrollment_tokens ADD COLUMN request_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add enrollment request ID: %w", err)
		}
	}
	if !columns["agent_id"] {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE enrollment_tokens ADD COLUMN agent_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add enrollment agent ID: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (4, ?)`, formatTime(time.Now().UTC())); err != nil {
		return fmt.Errorf("record enrollment claim migration: %w", err)
	}
	return nil
}

func (s *Store) ensureEnrollmentExpiryNanos(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(enrollment_tokens)`)
	if err != nil {
		return fmt.Errorf("inspect enrollment token schema: %w", err)
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect enrollment token column: %w", err)
		}
		if name == "expires_at_unix_nano" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate enrollment token schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close enrollment schema rows: %w", err)
	}
	if !found {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE enrollment_tokens ADD COLUMN expires_at_unix_nano INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add enrollment expiry timestamp: %w", err)
		}
	}

	type legacyExpiry struct {
		hash string
		text string
	}
	legacyRows, err := s.db.QueryContext(ctx, `SELECT token_hash, expires_at FROM enrollment_tokens WHERE expires_at_unix_nano = 0`)
	if err != nil {
		return fmt.Errorf("list legacy enrollment expiries: %w", err)
	}
	var expiries []legacyExpiry
	for legacyRows.Next() {
		var item legacyExpiry
		if err := legacyRows.Scan(&item.hash, &item.text); err != nil {
			_ = legacyRows.Close()
			return fmt.Errorf("scan legacy enrollment expiry: %w", err)
		}
		expiries = append(expiries, item)
	}
	if err := legacyRows.Err(); err != nil {
		_ = legacyRows.Close()
		return fmt.Errorf("iterate legacy enrollment rows: %w", err)
	}
	if err := legacyRows.Close(); err != nil {
		return fmt.Errorf("close legacy enrollment rows: %w", err)
	}
	for _, item := range expiries {
		expiresAt, err := time.Parse(time.RFC3339Nano, item.text)
		if err != nil {
			return fmt.Errorf("parse legacy enrollment expiry: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE enrollment_tokens SET expires_at_unix_nano = ? WHERE token_hash = ?`, expiresAt.UnixNano(), item.hash); err != nil {
			return fmt.Errorf("backfill enrollment expiry timestamp: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (2, ?)`, formatTime(time.Now().UTC())); err != nil {
		return fmt.Errorf("record enrollment expiry migration: %w", err)
	}
	return nil
}

func (s *Store) IssueEnrollmentToken(ctx context.Context, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 {
		return "", time.Time{}, errors.New("enrollment token TTL must be positive")
	}
	token, err := randomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().UTC().Add(ttl)
	if err := s.CreateEnrollmentToken(ctx, token, expiresAt); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func (s *Store) CreateEnrollmentToken(ctx context.Context, token string, expiresAt time.Time) error {
	if strings.TrimSpace(token) == "" || !expiresAt.After(time.Now()) {
		return errors.New("token and future expiration are required")
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO enrollment_tokens(token_hash, expires_at, expires_at_unix_nano, created_at) VALUES (?, ?, ?, ?)`,
		hashToken(token), formatTime(expiresAt), expiresAt.UTC().UnixNano(), formatTime(now))
	if err != nil {
		return fmt.Errorf("create enrollment token: %w", err)
	}
	return nil
}

func (s *Store) ConsumeEnrollmentToken(ctx context.Context, token, agentName string) (domain.Agent, string, error) {
	var agent domain.Agent
	agentName = strings.TrimSpace(agentName)
	if token == "" || !domain.ValidAgentName(agentName) {
		return agent, "", ErrInvalidEnrollmentToken
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return agent, "", fmt.Errorf("begin enrollment: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE enrollment_tokens SET used_at = ?
		WHERE token_hash = ? AND used_at IS NULL AND expires_at_unix_nano > ?`,
		formatTime(now), hashToken(token), now.UnixNano())
	if err != nil {
		return agent, "", fmt.Errorf("consume enrollment token: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return agent, "", ErrInvalidEnrollmentToken
	}

	apiToken, err := randomToken(32)
	if err != nil {
		return agent, "", err
	}
	agentID, err := randomID()
	if err != nil {
		return agent, "", err
	}
	agent = domain.Agent{
		ID:        agentID,
		Name:      agentName,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agents
		(id, name, token_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		agent.ID, agent.Name, hashToken(apiToken), formatTime(now), formatTime(now))
	if err != nil {
		return domain.Agent{}, "", fmt.Errorf("create agent: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Agent{}, "", fmt.Errorf("commit enrollment: %w", err)
	}
	return agent, apiToken, nil
}

func (s *Store) ClaimEnrollmentToken(ctx context.Context, token, agentName, requestID, agentToken string) (domain.Agent, error) {
	var agent domain.Agent
	agentName = strings.TrimSpace(agentName)
	requestID = strings.TrimSpace(requestID)
	agentToken = strings.TrimSpace(agentToken)
	if token == "" || !domain.ValidAgentName(agentName) || len(requestID) != 64 || len(agentToken) != 64 {
		return agent, ErrInvalidEnrollmentToken
	}
	if _, err := hex.DecodeString(requestID); err != nil {
		return agent, ErrInvalidEnrollmentToken
	}
	if _, err := hex.DecodeString(agentToken); err != nil {
		return agent, ErrInvalidEnrollmentToken
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return agent, fmt.Errorf("begin enrollment claim: %w", err)
	}
	defer tx.Rollback()
	var expiresAt int64
	var usedAt sql.NullString
	var storedRequestID, storedAgentID string
	err = tx.QueryRowContext(ctx, `SELECT expires_at_unix_nano, used_at, request_id, agent_id FROM enrollment_tokens WHERE token_hash = ?`, hashToken(token)).Scan(&expiresAt, &usedAt, &storedRequestID, &storedAgentID)
	if errors.Is(err, sql.ErrNoRows) {
		return agent, ErrInvalidEnrollmentToken
	}
	if err != nil {
		return agent, fmt.Errorf("read enrollment claim: %w", err)
	}
	if usedAt.Valid {
		if storedRequestID != requestID || storedAgentID == "" {
			return agent, ErrInvalidEnrollmentToken
		}
		var createdAt, updatedAt, storedAgentTokenHash string
		err = tx.QueryRowContext(ctx, `SELECT id, name, token_hash, desired_revision, observed_revision, created_at, updated_at FROM agents WHERE id = ?`, storedAgentID).Scan(
			&agent.ID, &agent.Name, &storedAgentTokenHash, &agent.DesiredRevision, &agent.ObservedRevision, &createdAt, &updatedAt)
		if errors.Is(err, sql.ErrNoRows) || agent.Name != agentName || storedAgentTokenHash != hashToken(agentToken) {
			return domain.Agent{}, ErrInvalidEnrollmentToken
		}
		if err != nil {
			return domain.Agent{}, fmt.Errorf("read claimed agent: %w", err)
		}
		agent.CreatedAt, agent.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
		return agent, nil
	}
	now := time.Now().UTC()
	if expiresAt <= now.UnixNano() {
		return agent, ErrInvalidEnrollmentToken
	}
	agentID, err := randomID()
	if err != nil {
		return agent, err
	}
	agent = domain.Agent{ID: agentID, Name: agentName, CreatedAt: now, UpdatedAt: now}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agents (id, name, token_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		agent.ID, agent.Name, hashToken(agentToken), formatTime(now), formatTime(now)); err != nil {
		return domain.Agent{}, fmt.Errorf("create claimed agent: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE enrollment_tokens SET used_at = ?, request_id = ?, agent_id = ? WHERE token_hash = ? AND used_at IS NULL`,
		formatTime(now), requestID, agent.ID, hashToken(token))
	if err != nil {
		return domain.Agent{}, fmt.Errorf("consume enrollment claim: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil || rowsAffected != 1 {
		return domain.Agent{}, ErrInvalidEnrollmentToken
	}
	if err := tx.Commit(); err != nil {
		return domain.Agent{}, fmt.Errorf("commit enrollment claim: %w", err)
	}
	return agent, nil
}

func (s *Store) AuthenticateAgent(ctx context.Context, token string) (domain.Agent, error) {
	var agent domain.Agent
	var createdAt, updatedAt string
	var lastSeen sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, name, desired_revision, observed_revision,
		last_seen_at, created_at, updated_at FROM agents WHERE token_hash = ?`, hashToken(token)).Scan(
		&agent.ID, &agent.Name, &agent.DesiredRevision, &agent.ObservedRevision,
		&lastSeen, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Agent{}, ErrInvalidAgentToken
	}
	if err != nil {
		return domain.Agent{}, fmt.Errorf("authenticate agent: %w", err)
	}
	agent.CreatedAt = parseTime(createdAt)
	agent.UpdatedAt = parseTime(updatedAt)
	if lastSeen.Valid {
		agent.LastSeenAt = parseTime(lastSeen.String)
	}
	return agent, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate secure token: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate secure ID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}
