package rex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
	if id = parseClaudeSession(b, pid, dir); id == "" {
		return "", ""
	}
	return id, config
}

// parseClaudeSession returns the conversation of Claude Code's file of
// process pid, whose working directory is dir; "" when the file is another
// process's, which had the id before, or names none GoRex would type.
func parseClaudeSession(b []byte, pid int, dir string) string {
	var f struct {
		PID       int    `json:"pid"`
		SessionID string `json:"sessionId"`
		Cwd       string `json:"cwd"`
	}
	if json.Unmarshal(b, &f) != nil || f.PID != pid || !IsUUID(f.SessionID) {
		return ""
	}
	// The file of a process that crashed stays, and names the id it had.
	if dir != "" && !samePath(f.Cwd, dir) {
		return ""
	}
	return f.SessionID
}

// IsUUID tells whether s is a UUID, 8-4-4-4-12 hex digits: a conversation
// id GoRex types into a shell.
func IsUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
	}
	return true
}

// samePath tells whether a and b name one directory: Windows takes no
// case, and a trailing separator is none.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
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
