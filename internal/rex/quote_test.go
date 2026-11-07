package rex

import (
	"os/exec"
	"strings"
	"testing"
)

// TestClaudeResume checks the command that resumes Claude Code's own
// conversation, in its config dir, by the shell it is typed into.
func TestClaudeResume(t *testing.T) {
	const id = "5d454f1b-0c2e-4b8a-9f00-0123456789ab"
	for _, c := range []struct {
		shell, config, want string
		ok                  bool
	}{
		{"bash", "", "claude --resume " + id, true},
		{"cmd.exe", "", "claude --resume " + id, true},
		{"/bin/zsh", "/home/u/.claude-work", "CLAUDE_CONFIG_DIR=/home/u/.claude-work claude --resume " + id, true},
		{"bash", "/tmp/a b", "CLAUDE_CONFIG_DIR='/tmp/a b' claude --resume " + id, true},
		{"fish", "/tmp/a b", "env CLAUDE_CONFIG_DIR='/tmp/a b' claude --resume " + id, true},
		{"tcsh", "/tmp/x", "env CLAUDE_CONFIG_DIR=/tmp/x claude --resume " + id, true},
		{"pwsh.exe", `D:\it's`, `$env:CLAUDE_CONFIG_DIR='D:\it''s'; claude --resume ` + id, true},
		{"powershell", `D:\c`, `$env:CLAUDE_CONFIG_DIR='D:\c'; claude --resume ` + id, true},
		{"cmd.exe", `D:\a & b`, `set "CLAUDE_CONFIG_DIR=D:\a & b" && claude --resume ` + id, true},
		{"cmd.exe", `D:\100%`, "", false},
		{"nu", "", "", false},
	} {
		got, ok := ClaudeResume(c.shell, id, c.config)
		if ok != c.ok || got != c.want {
			t.Errorf("ClaudeResume(%q, %q) = %q, %v; want %q, %v", c.shell, c.config, got, ok, c.want, c.ok)
		}
	}
}

// TestPowerShellCurlyQuotes checks that a word holding any quote
// PowerShell ends a single-quoted string with stays one word: its quotes,
// ASCII and curly, are doubled. With pwsh at hand, the word reads back.
func TestPowerShellCurlyQuotes(t *testing.T) {
	words := []string{"'"}
	for _, q := range []string{"‘", "’", "‚", "‛"} {
		words = append(words, q)
		hostile := "x" + q + "; Write-Output INJECTED; " + q
		got, _ := powerShellWord(hostile)
		if want := "'x" + q + q + "; Write-Output INJECTED; " + q + q + "'"; got != want {
			t.Errorf("powerShellWord(%q) = %q, want %q", hostile, got, want)
		}
	}
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("no pwsh to read the words back")
	}
	for _, q := range words {
		s := "x" + q + "; Write-Output INJECTED; " + q
		w, _ := powerShellWord(s)
		out, err := exec.Command(pwsh, "-NoProfile", "-NonInteractive", "-Command",
			"[Console]::OutputEncoding = [Text.Encoding]::UTF8; Write-Output "+w).Output()
		if err != nil {
			t.Fatalf("pwsh: %v", err)
		}
		if got := strings.TrimRight(string(out), "\r\n"); got != s {
			t.Errorf("pwsh read %q back as %q", s, got)
		}
	}
}

func TestShellName(t *testing.T) {
	for in, want := range map[string]string{
		"/bin/zsh":                               "zsh",
		`C:\Program Files\PowerShell\7\pwsh.exe`: "pwsh",
		`C:\WINDOWS\System32\CMD.EXE`:            "cmd",
		"fish":                                   "fish",
		"":                                       "",
	} {
		if got := ShellName(in); got != want {
			t.Errorf("ShellName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPathWord quotes a path for the shell, as sh for one it does not
// know, and refuses one the shell's quoting refuses: cmd.exe would expand
// its % or run what follows a newline.
func TestPathWord(t *testing.T) {
	for _, c := range []struct {
		shell, path, want string
		ok                bool
	}{
		{"/bin/zsh", "/tmp/a b", "'/tmp/a b'", true},
		{"", "/tmp/a b", "'/tmp/a b'", true},
		{`C:\Windows\System32\cmd.exe`, `C:\a b.txt`, `"C:\a b.txt"`, true},
		{`C:\Windows\System32\cmd.exe`, `C:\a&b%c%.txt`, "", false},
		{`C:\Windows\System32\cmd.exe`, "C:\\a\r\nb.txt", "", false},
	} {
		if w, ok := PathWord(c.shell, c.path); w != c.want || ok != c.ok {
			t.Errorf("PathWord(%q, %q) = %q, %v, want %q, %v", c.shell, c.path, w, ok, c.want, c.ok)
		}
	}
}
