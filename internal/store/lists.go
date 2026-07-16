package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lkhmm520/portloom/internal/domain"
)

func (s *Store) ListAgents(ctx context.Context) ([]domain.Agent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, desired_revision, observed_revision, last_seen_at, created_at, updated_at FROM agents ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Agent, 0)
	for rows.Next() {
		var item domain.Agent
		var lastSeen sql.NullString
		var createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.Name, &item.DesiredRevision, &item.ObservedRevision, &lastSeen, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
		if lastSeen.Valid {
			item.LastSeenAt = parseTime(lastSeen.String)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListEnrollmentTokens(ctx context.Context) ([]domain.EnrollmentTokenInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT token_hash, expires_at, used_at, created_at FROM enrollment_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list enrollment tokens: %w", err)
	}
	defer rows.Close()
	items := make([]domain.EnrollmentTokenInfo, 0)
	now := time.Now().UTC()
	for rows.Next() {
		var hash, expiresAt, createdAt string
		var usedAt sql.NullString
		if err := rows.Scan(&hash, &expiresAt, &usedAt, &createdAt); err != nil {
			return nil, fmt.Errorf("scan enrollment token: %w", err)
		}
		item := domain.EnrollmentTokenInfo{ID: "token-" + hash[:12], ExpiresAt: parseTime(expiresAt), CreatedAt: parseTime(createdAt), Status: "available"}
		if usedAt.Valid {
			used := parseTime(usedAt.String)
			item.UsedAt = &used
			item.Status = "used"
		} else if !item.ExpiresAt.After(now) {
			item.Status = "expired"
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
