//go:build darwin || linux || windows

package rex

import (
	"strings"
	"sync"
	"time"
)

// knownAgent is an agent CLI GoRex has a brand tile for (programs.go,
// ios/Shared/Program.swift, web/src/programs.ts) along with per-agent TUI
// input behavior used by send and delegate.
type knownAgent struct {
	ID      string
	Name    string
	Aliases []string
	// FlushKeys is the key sequence (encoded by EncodeKeySequence) that
	// flushes a queued mid-turn prompt immediately in the agent's TUI.
	FlushKeys string
	// SplashMarkers are case-insensitive screen substrings indicating a
	// dismissible startup splash modal covering the input prompt.
	SplashMarkers    []string
	SplashDismissKey string
}

// knownAgents are the agent CLIs GoRex detects on a host, in display order
// (docs/MOD-010-agent-launchers.md).
var knownAgents = []knownAgent{
	{ID: "claude", Name: "Claude Code", Aliases: []string{"claude-code"}},
	{ID: "codex", Name: "Codex"},
	{
		ID:               "agy",
		Name:             "Antigravity",
		Aliases:          []string{"antigravity", "antigravity-cli"},
		FlushKeys:        "alt+m",
		SplashMarkers:    []string{"esc to dismiss", "escape to dismiss", "esc to close"},
		SplashDismissKey: "esc",
	},
	{ID: "omp", Name: "omp"},
	{ID: "devin", Name: "Devin"},
}

var defaultSplashMarkers = []string{
	"esc to dismiss",
	"escape to dismiss",
	"esc to close",
	"escape to close",
}

// CanonicalAgentID resolves a program name, path, or alias to a canonical
// knownAgent ID (such as "agy" or "claude"), or "" when unrecognized.
func CanonicalAgentID(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return ""
	}
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimPrefix(s, "-")
	if i := indexFold(s, ".exe."); i > 0 {
		s = s[:i]
	}
	lower := strings.ToLower(s)
	for _, ext := range []string{".exe", ".cmd", ".bat", ".com", ".js", ".mjs", ".cjs"} {
		if strings.HasSuffix(lower, ext) {
			lower = strings.TrimSuffix(lower, ext)
			break
		}
	}
	for _, a := range knownAgents {
		if lower == a.ID {
			return a.ID
		}
		for _, alias := range a.Aliases {
			if lower == alias {
				return a.ID
			}
		}
	}
	return ""
}

// SessionAgentID returns the canonical knownAgent ID running in in, or ""
// when the session is not running a known coding agent.
func SessionAgentID(in SessionInfo) string {
	if id := CanonicalAgentID(in.DelegatedAgent); id != "" {
		return id
	}
	if in.Status != nil {
		if id := CanonicalAgentID(in.Status.App); id != "" {
			return id
		}
	}
	if id := CanonicalAgentID(in.Program); id != "" {
		return id
	}
	for _, arg := range in.Args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if id := CanonicalAgentID(arg); id != "" {
			return id
		}
	}
	if in.Command {
		if id := CanonicalAgentID(in.Shell); id != "" {
			return id
		}
	}
	return ""
}

// AgentFlushInput returns the terminal bytes configured in the agent registry
// to flush a queued message mid-turn for agentID, or nil when none is configured.
func AgentFlushInput(agentID string) []byte {
	id := CanonicalAgentID(agentID)
	if id == "" {
		return nil
	}
	for _, a := range knownAgents {
		if a.ID == id && a.FlushKeys != "" {
			b, err := EncodeKeySequence(a.FlushKeys)
			if err != nil {
				return nil
			}
			return b
		}
	}
	return nil
}

// agentSplashDismiss reports the key bytes needed to dismiss a visible startup
// splash modal on screen for agentID, or nil when no splash modal is detected.
func agentSplashDismiss(agentID, plainText string) []byte {
	if plainText == "" {
		return nil
	}
	lower := strings.ToLower(plainText)
	id := CanonicalAgentID(agentID)
	dismissSpec := "esc"
	markers := defaultSplashMarkers
	for _, a := range knownAgents {
		if a.ID == id {
			if len(a.SplashMarkers) > 0 {
				markers = a.SplashMarkers
			}
			if a.SplashDismissKey != "" {
				dismissSpec = a.SplashDismissKey
			}
			break
		}
	}
	for _, m := range markers {
		if m != "" && strings.Contains(lower, m) {
			b, err := EncodeKeySequence(dismissSpec)
			if err != nil {
				return []byte{0x1b}
			}
			return b
		}
	}
	return nil
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
