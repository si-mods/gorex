package homedir

import (
	"os"
	"path/filepath"
	"runtime"
)

// XDGConfigHome resolves the XDG config directory: $XDG_CONFIG_HOME when set,
// otherwise os.UserConfigDir on Windows or <home>/.config on Unix. When home is
// empty, UserHomeDir is queried.
func XDGConfigHome(home string) (string, error) {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return base, nil
	}
	if runtime.GOOS == "windows" {
		return UserConfigDir()
	}
	if home == "" {
		var err error
		home, err = UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".config"), nil
}

// ClaudeConfigDir resolves Claude Code's config directory: $CLAUDE_CONFIG_DIR
// when set, otherwise <home>/.claude. When home is empty, UserHomeDir is queried.
func ClaudeConfigDir(home string) (string, error) {
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return v, nil
	}
	if home == "" {
		var err error
		home, err = UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".claude"), nil
}

// CodexHome resolves Codex's config directory: $CODEX_HOME when set, otherwise
// <home>/.codex. When home is empty, UserHomeDir is queried.
func CodexHome(home string) (string, error) {
	if v := os.Getenv("CODEX_HOME"); v != "" {
		return v, nil
	}
	if home == "" {
		var err error
		home, err = UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".codex"), nil
}
