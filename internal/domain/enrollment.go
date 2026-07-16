package domain

import "time"

type EnrollmentTokenInfo struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
}
