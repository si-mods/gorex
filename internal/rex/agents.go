//go:build darwin || linux || windows

package rex

import (
	"sync"
	"time"
)

// knownAgent is an agent CLI GoRex has a brand tile for (programs.go,
// ios/Shared/Program.swift, web/src/programs.ts).
type knownAgent struct {
	ID   string
	Name string
}

// knownAgents are the agent CLIs GoRex detects on a host, in display order
// (docs/MOD-010-agent-launchers.md).
var knownAgents = []knownAgent{
	{ID: "claude", Name: "Claude Code"},
	{ID: "codex", Name: "Codex"},
	{ID: "agy", Name: "Antigravity"},
	{ID: "omp", Name: "omp"},
	{ID: "devin", Name: "Devin"},
}

// agentsCacheTTL is how long a server reuses its detected agent list
// before probing PATH again.
var agentsCacheTTL = newDuration(60 * time.Second)

type agentsCache struct {
	mu      sync.Mutex
	agents  []AgentInfo
	checked time.Time
}

var serverAgentsCache agentsCache

// ResetAgentsCacheForTest clears the cached agent list: for tests.
func ResetAgentsCacheForTest() {
	serverAgentsCache.mu.Lock()
	serverAgentsCache.agents = nil
	serverAgentsCache.checked = time.Time{}
	serverAgentsCache.mu.Unlock()
}

// doAgentsList returns the known agent CLIs installed on this machine
// (CapabilityAgents, docs/MOD-010-agent-launchers.md).
func (s *Server) doAgentsList(_ Request, _ string, _ *keptConn) (any, error) {
	home, _ := s.homeDir()
	return AgentsResult{Agents: cachedAgents(home, s.spawner().shell())}, nil
}

func cachedAgents(home, shell string) []AgentInfo {
	ttl := agentsCacheTTL.get()
	serverAgentsCache.mu.Lock()
	if ttl > 0 && !serverAgentsCache.checked.IsZero() && time.Since(serverAgentsCache.checked) < ttl {
		out := append([]AgentInfo(nil), serverAgentsCache.agents...)
		serverAgentsCache.mu.Unlock()
		return out
	}
	serverAgentsCache.mu.Unlock()

	found := detectAgents(home, shell)
	if found == nil {
		found = []AgentInfo{}
	}

	serverAgentsCache.mu.Lock()
	serverAgentsCache.agents = append([]AgentInfo(nil), found...)
	serverAgentsCache.checked = time.Now()
	serverAgentsCache.mu.Unlock()
	return found
}
