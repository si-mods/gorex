//go:build windows

package rex

import (
	"slices"
	"testing"
)

func TestWindowsAgentCommand(t *testing.T) {
	exe := `C:\Tools\claude.exe`
	if cmd, ok := windowsAgentCommand(exe); !ok || !slices.Equal(cmd, []string{exe}) {
		t.Errorf("exe command = %v, %v, want [%s], true", cmd, ok, exe)
	}
	cmd0 := `C:\Users\alice\AppData\Roaming\npm\claude.cmd`
	cmd, ok := windowsAgentCommand(cmd0)
	if !ok || len(cmd) != 3 || cmd[1] != "/c" || cmd[2] != cmd0 {
		t.Errorf("cmd command = %v, %v, want [comspec /c %s], true", cmd, ok, cmd0)
	}
	if cmd[0] == "" {
		t.Error("comspec empty")
	}
	bat := `C:\Tools\codex.bat`
	if _, ok := windowsAgentCommand(bat); !ok {
		t.Errorf("bat %s: want a cmd /c command", bat)
	}
	for _, no := range []string{`C:\Tools\claude`, `C:\Tools\claude.ps1`} {
		if _, ok := windowsAgentCommand(no); ok {
			t.Errorf("%s: want no command (not launchable)", no)
		}
	}
}
