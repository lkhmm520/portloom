package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestAgentSSHKeyIsBoundOnceAndIdempotent(t *testing.T) {
	ctx := context.Background()
	s, agent := enrolledStore(t, 34000, 34001)
	if err := s.SetAgentSSHKey(ctx, agent.ID, "ssh-ed25519 first"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAgentSSHKey(ctx, agent.ID, "ssh-ed25519 first"); err != nil {
		t.Fatalf("idempotent registration failed: %v", err)
	}
	if err := s.SetAgentSSHKey(ctx, agent.ID, "ssh-ed25519 second"); err != ErrConflict {
		t.Fatalf("replacement error = %v, want ErrConflict", err)
	}
	entries, err := s.ListAgentSSHKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].AgentID != agent.ID || entries[0].PublicKey != "ssh-ed25519 first" {
		t.Fatalf("unexpected SSH keys: %+v", entries)
	}
}

func TestOpenAddsSSHKeyColumnToVersionOneDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
CREATE TABLE enrollment_tokens (
 token_hash TEXT PRIMARY KEY, expires_at TEXT NOT NULL, expires_at_unix_nano INTEGER NOT NULL, used_at TEXT, created_at TEXT NOT NULL
);
CREATE TABLE agents (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE,
 desired_revision INTEGER NOT NULL DEFAULT 0, observed_revision INTEGER NOT NULL DEFAULT 0,
 last_seen_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
INSERT INTO schema_migrations(version, applied_at) VALUES (1, '2025-01-01T00:00:00Z');
INSERT INTO agents(id,name,token_hash,created_at,updated_at) VALUES ('legacy-agent','old','hash','2025-01-01T00:00:00Z','2025-01-01T00:00:00Z');
`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path, Options{PortRangeStart: 34010, PortRangeEnd: 34011})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetAgentSSHKey(ctx, "legacy-agent", "ssh-ed25519 migrated"); err != nil {
		t.Fatal(err)
	}
	entries, err := s.ListAgentSSHKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].PublicKey != "ssh-ed25519 migrated" {
		t.Fatalf("migration lost key: %+v", entries)
	}
	var migration int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 3`).Scan(&migration); err != nil {
		t.Fatal(err)
	}
	if migration != 1 {
		t.Fatalf("migration 3 count = %d", migration)
	}
}

func TestSetAgentSSHKeyRejectsUnknownAgent(t *testing.T) {
	s, _ := enrolledStore(t, 34020, 34021)
	err := s.SetAgentSSHKey(context.Background(), "missing", "ssh-ed25519 key")
	if err != ErrNotFound {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}
