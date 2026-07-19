package domain

import (
	"regexp"
	"time"
)

var agentNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func ValidAgentName(name string) bool {
	return agentNameRE.MatchString(name)
}

// Agent is an enrolled tunnel client.
type Agent struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	DesiredRevision  int64     `json:"desired_revision"`
	ObservedRevision int64     `json:"observed_revision"`
	LastSeenAt       time.Time `json:"last_seen_at,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// AgentSSHKey is the public key authorized for one enrolled Agent.
type AgentSSHKey struct {
	AgentID   string
	PublicKey string
}

// RouteObservation reports an agent's current state for one route.
type RouteObservation struct {
	RouteID          string `json:"route_id"`
	ObservedRevision int64  `json:"observed_revision"`
	LocalStatus      string `json:"local_status"`
	TunnelStatus     string `json:"tunnel_status"`
	LastError        string `json:"last_error,omitempty"`
}
