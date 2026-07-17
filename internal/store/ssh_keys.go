package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
)

func (s *Store) SetAgentSSHKey(ctx context.Context, agentID, publicKey string) error {
	agentID, publicKey = strings.TrimSpace(agentID), strings.TrimSpace(publicKey)
	if agentID == "" || publicKey == "" {
		return ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE agents SET ssh_public_key = ?, updated_at = ?
WHERE id = ? AND (ssh_public_key = '' OR ssh_public_key = ?)
`, publicKey, formatTime(time.Now().UTC()), agentID, publicKey)
	if err != nil {
		return fmt.Errorf("set agent SSH key: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated agent SSH keys: %w", err)
	}
	if changed == 1 {
		return nil
	}
	var existing string
	if err := s.db.QueryRowContext(ctx, `SELECT ssh_public_key FROM agents WHERE id = ?`, agentID).Scan(&existing); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("read existing agent SSH key: %w", err)
	}
	if existing == publicKey {
		return nil
	}
	return ErrConflict
}

func (s *Store) ListAgentSSHKeys(ctx context.Context) ([]domain.AgentSSHKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, ssh_public_key FROM agents WHERE ssh_public_key <> '' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list agent SSH keys: %w", err)
	}
	defer rows.Close()
	entries := make([]domain.AgentSSHKey, 0)
	for rows.Next() {
		var entry domain.AgentSSHKey
		if err := rows.Scan(&entry.AgentID, &entry.PublicKey); err != nil {
			return nil, fmt.Errorf("scan agent SSH key: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent SSH keys: %w", err)
	}
	return entries, nil
}

// BindAgentSSHKeyAndSync serializes the complete key-binding and file-sync
// sequence so an older snapshot can never replace a newer authorized_keys file.
func (s *Store) BindAgentSSHKeyAndSync(ctx context.Context, agentID, publicKey string, syncKeys func([]domain.AgentSSHKey) error) error {
	if syncKeys == nil {
		return ErrInvalid
	}
	s.sshKeysMu.Lock()
	defer s.sshKeysMu.Unlock()
	if err := s.SetAgentSSHKey(ctx, agentID, publicKey); err != nil {
		return err
	}
	keys, err := s.ListAgentSSHKeys(ctx)
	if err != nil {
		return err
	}
	return syncKeys(keys)
}

func (s *Store) SyncAgentSSHKeys(ctx context.Context, syncKeys func([]domain.AgentSSHKey) error) error {
	if syncKeys == nil {
		return ErrInvalid
	}
	s.sshKeysMu.Lock()
	defer s.sshKeysMu.Unlock()
	keys, err := s.ListAgentSSHKeys(ctx)
	if err != nil {
		return err
	}
	return syncKeys(keys)
}
