package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/lkhmm520/portloom/internal/metrics"
	"github.com/lkhmm520/portloom/internal/sysinfo"
)

// MetricsSource exposes the traffic registry snapshot to the admin API.
type MetricsSource interface {
	Snapshot() metrics.Snapshot
}

// AgentSystem is the resource usage an agent reported with its last heartbeat.
type AgentSystem struct {
	sysinfo.Stats
	ReportedAt time.Time `json:"reported_at"`
}

// AgentSystemStore keeps the most recent per-agent resource reports in memory.
type AgentSystemStore struct {
	mu     sync.RWMutex
	agents map[string]AgentSystem
}

func NewAgentSystemStore() *AgentSystemStore {
	return &AgentSystemStore{agents: map[string]AgentSystem{}}
}

func (s *AgentSystemStore) Record(agentID string, stats sysinfo.Stats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[agentID] = AgentSystem{Stats: stats, ReportedAt: time.Now().UTC()}
}

func (s *AgentSystemStore) Snapshot() map[string]AgentSystem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := make(map[string]AgentSystem, len(s.agents))
	for id, entry := range s.agents {
		snapshot[id] = entry
	}
	return snapshot
}

// ServerStatsFunc samples the server process's own resource usage.
type ServerStatsFunc func() sysinfo.Stats

func (s *server) metrics(w http.ResponseWriter, _ *http.Request) {
	response := map[string]any{}
	if s.config.Metrics != nil {
		response["traffic"] = s.config.Metrics.Snapshot()
	}
	if s.config.ServerStats != nil {
		response["server"] = s.config.ServerStats()
	}
	if s.config.AgentSystemInfo != nil {
		response["agents"] = s.config.AgentSystemInfo.Snapshot()
	}
	writeJSON(w, http.StatusOK, response)
}
