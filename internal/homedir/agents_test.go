package homedir

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestAgentConfigDirs(t *testing.T) {
	home := t.TempDir()

	t.Setenv("CLAUDE_CONFIG_DIR", "")
	got, err := ClaudeConfigDir(home)
	if err != nil || got != filepath.Join(home, ".claude") {
		t.Fatalf("ClaudeConfigDir default = %q, %v", got, err)
	}
	customClaude := filepath.Join(home, "custom-claude")
	t.Setenv("CLAUDE_CONFIG_DIR", customClaude)
	if got, err := ClaudeConfigDir(home); err != nil || got != customClaude {
		t.Fatalf("ClaudeConfigDir env = %q, %v", got, err)
	}

	t.Setenv("CODEX_HOME", "")
	got, err = CodexHome(home)
	if err != nil || got != filepath.Join(home, ".codex") {
		t.Fatalf("CodexHome default = %q, %v", got, err)
	}
	customCodex := filepath.Join(home, "custom-codex")
	t.Setenv("CODEX_HOME", customCodex)
	if got, err := CodexHome(home); err != nil || got != customCodex {
		t.Fatalf("CodexHome env = %q, %v", got, err)
	}

	customXDG := filepath.Join(home, "xdg")
	t.Setenv("XDG_CONFIG_HOME", customXDG)
	if got, err := XDGConfigHome(home); err != nil || got != customXDG {
		t.Fatalf("XDGConfigHome env = %q, %v", got, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if runtime.GOOS != "windows" {
		if got, err := XDGConfigHome(home); err != nil || got != filepath.Join(home, ".config") {
			t.Fatalf("XDGConfigHome default = %q, %v", got, err)
		}
	}
}
