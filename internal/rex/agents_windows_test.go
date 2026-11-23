//go:build windows

package rex

import (
	"errors"
	"slices"
	"testing"
)

// noShell stubs agentShellLookPath to find no PowerShell, so commands are
// the direct ones.
func noShell(t *testing.T) {
	t.Helper()
	old := agentShellLookPath
	agentShellLookPath = func(string) (string, error) { return "", errors.New("no such program") }
	t.Cleanup(func() { agentShellLookPath = old })
	t.Setenv("SHELL", "")
}

func TestWindowsAgentCommand(t *testing.T) {
	noShell(t)
	exe := `C:\Tools\claude.exe`
	if cmd, ok := windowsAgentCommand(exe, ""); !ok || !slices.Equal(cmd, []string{exe}) {
		t.Errorf("exe command = %v, %v, want [%s], true", cmd, ok, exe)
	}
	cmd0 := `C:\Users\alice\AppData\Roaming\npm\claude.cmd`
	cmd, ok := windowsAgentCommand(cmd0, "")
	if !ok || len(cmd) != 3 || cmd[1] != "/c" || cmd[2] != cmd0 {
		t.Errorf("cmd command = %v, %v, want [comspec /c %s], true", cmd, ok, cmd0)
	}
	if cmd[0] == "" {
		t.Error("comspec empty")
	}
	bat := `C:\Tools\codex.bat`
	if _, ok := windowsAgentCommand(bat, ""); !ok {
		t.Errorf("bat %s: want a cmd /c command", bat)
	}
	for _, no := range []string{`C:\Tools\claude`, `C:\Tools\claude.ps1`} {
		if _, ok := windowsAgentCommand(no, ""); ok {
			t.Errorf("%s: want no command (not launchable)", no)
		}
	}
}

func TestWindowsAgentCommandShell(t *testing.T) {
	old := agentShellLookPath
	t.Cleanup(func() { agentShellLookPath = old })
	t.Setenv("SHELL", "")
	pwsh := `C:\Program Files\PowerShell\7\pwsh.exe`
	agentShellLookPath = func(name string) (string, error) {
		if name == "pwsh.exe" {
			return pwsh, nil
		}
		return "", errors.New("no such program")
	}
	exe := `C:\Tools\claude.exe`
	want := []string{pwsh, "-Command", "& 'C:\\Tools\\claude.exe'; exit $LASTEXITCODE"}
	if cmd, ok := windowsAgentCommand(exe, ""); !ok || !slices.Equal(cmd, want) {
		t.Errorf("command = %v, %v, want %v, true", cmd, ok, want)
	}
	// A .cmd runs through the same call: PowerShell runs batch files.
	cmd0 := `C:\Users\alice\AppData\Roaming\npm\claude.cmd`
	want = []string{pwsh, "-Command", "& 'C:\\Users\\alice\\AppData\\Roaming\\npm\\claude.cmd'; exit $LASTEXITCODE"}
	if cmd, ok := windowsAgentCommand(cmd0, ""); !ok || !slices.Equal(cmd, want) {
		t.Errorf("command = %v, %v, want %v, true", cmd, ok, want)
	}
	// The session user's own PowerShell comes first.
	own := `C:\WINDOWS\System32\WindowsPowerShell\v1.0\powershell.exe`
	agentShellLookPath = func(name string) (string, error) {
		if name == own {
			return own, nil
		}
		return "", errors.New("no such program")
	}
	want = []string{own, "-Command", "& 'C:\\Tools\\claude.exe'; exit $LASTEXITCODE"}
	if cmd, ok := windowsAgentCommand(exe, own); !ok || !slices.Equal(cmd, want) {
		t.Errorf("command = %v, %v, want %v, true", cmd, ok, want)
	}
	// A path with an apostrophe survives quoting.
	spaced := `D:\it's\claude.exe`
	want = []string{pwsh, "-Command", `& 'D:\it''s\claude.exe'; exit $LASTEXITCODE`}
	agentShellLookPath = func(name string) (string, error) {
		if name == "pwsh.exe" {
			return pwsh, nil
		}
		return "", errors.New("no such program")
	}
	if cmd, ok := windowsAgentCommand(spaced, ""); !ok || !slices.Equal(cmd, want) {
		t.Errorf("command = %v, %v, want %v, true", cmd, ok, want)
	}
}
