package rex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// claudeSession returns the conversation Claude Code's process pid has
// open, from the file the process keeps of itself in its config dir, and
// that dir when the process was given one: CLAUDE_CONFIG_DIR, against its
// working directory dir. Both are "" when the file tells none.
func claudeSession(pid int, dir string) (id, config string) {
	config = procEnv(pid, "CLAUDE_CONFIG_DIR")
	base := config
	switch {
	case base == "":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", ""
		}
		base = filepath.Join(home, ".claude")
	case !filepath.IsAbs(base):
		if dir == "" {
			return "", ""
		}
		base = filepath.Join(dir, base)
		config = base
	}
	b, err := os.ReadFile(filepath.Join(base, "sessions", strconv.Itoa(pid)+".json"))
	if err != nil {
		return "", ""
	}
	if id = parseClaudeSession(b, pid); id == "" {
		return "", ""
	}
	return id, config
}

// parseClaudeSession returns the conversation of Claude Code's file of
// process pid, "" when the file is another process's, which had the id
// before, or names none GoRex would type.
func parseClaudeSession(b []byte, pid int) string {
	var f struct {
		PID       int    `json:"pid"`
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(b, &f) != nil || f.PID != pid || f.SessionID == "" {
		return ""
	}
	// A UUID: the id is typed into a shell.
	if strings.Trim(f.SessionID, "0123456789abcdefABCDEF-") != "" {
		return ""
	}
	return f.SessionID
}

// envValue returns the variable name of env, NAME=value strings.
func envValue(env []string, name string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v
		}
	}
	return ""
}
