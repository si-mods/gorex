//go:build darwin || linux

package rex

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// shellPathProbeTimeout bounds how long probing the user's login shell for
// PATH may take.
const shellPathProbeTimeout = 2 * time.Second

// probeShellPathFunc probes the user's login interactive shell for PATH;
// tests override it to avoid spawning a real shell.
var probeShellPathFunc = probeShellPath

// detectAgents finds which knownAgents are executable in the user's PATH.
func detectAgents(home, shell string) []AgentInfo {
	sh := resolveUserShell(shell)
	dirs := agentSearchDirs(home, sh)
	return detectAgentsInDirs(dirs, sh)
}

func resolveUserShell(shell string) string {
	if shell != "" {
		return shell
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return defaultShell
}

func detectAgentsInDirs(dirs []string, shell string) []AgentInfo {
	out := []AgentInfo{}
	for _, a := range knownAgents {
		if path, ok := findExecutableInDirs(dirs, a.ID); ok {
			out = append(out, AgentInfo{
				ID:      a.ID,
				Name:    a.Name,
				Command: agentCommand(shell, a.ID, path),
			})
		}
	}
	return out
}

func agentSearchDirs(home, shell string) []string {
	var parts []string
	if p := probeShellPathFunc(shell); p != "" {
		parts = append(parts, filepath.SplitList(p)...)
	}
	if p := os.Getenv("PATH"); p != "" {
		parts = append(parts, filepath.SplitList(p)...)
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if d := os.Getenv("BUN_INSTALL"); filepath.IsAbs(d) {
		parts = append(parts, filepath.Join(d, "bin"))
	}
	if home != "" {
		mise := filepath.Join(home, ".local", "share", "mise")
		if d := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
			mise = filepath.Join(d, "mise")
		}
		if d := os.Getenv("MISE_DATA_DIR"); filepath.IsAbs(d) {
			mise = d
		}
		parts = append(parts,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".bun", "bin"),
			filepath.Join(home, ".npm-global", "bin"),
			filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, ".local", "share", "pnpm"),
			filepath.Join(mise, "shims"),
			filepath.Join(home, ".asdf", "shims"),
			filepath.Join(home, ".cargo", "bin"),
		)
	}
	parts = append(parts, "/opt/homebrew/bin", "/usr/local/bin")
	seen := make(map[string]bool, len(parts))
	out := make([]string, 0, len(parts))
	for _, d := range parts {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

func probeShellPath(shell string) string {
	if shell == "" || !supportsLoginExec(shell) {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), shellPathProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-lic", `printf "%s" "$PATH"`)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	// Take the last non-empty line in case shell rc files printed banners.
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" && strings.Contains(line, "/") {
			return line
		}
	}
	return ""
}

func findExecutableInDirs(dirs []string, name string) (string, bool) {
	for _, d := range dirs {
		p := filepath.Join(d, name)
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		if st.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

// agentCommand returns the argv to launch agent id directly so exiting it
// ends the session. For POSIX/fish shells, "-lic exec <path>" loads the
// user's login/interactive environment (PATH, API keys, proxies) and
// replaces the shell process with the agent binary — the resolved path,
// not the id, in case a fallback directory found it off the login PATH.
func agentCommand(shell, id, resolvedPath string) []string {
	return agentCommandWithArgs(shell, id, resolvedPath, nil)
}

// agentCommandWithArgs wraps resolvedPath with the user's login shell and
// passes extra arguments as positional parameters so prompts and flags are
// never interpolated into a shell command string (docs/MOD-016-peer-delegation.md).
func agentCommandWithArgs(shell, _, resolvedPath string, extra []string) []string {
	if shell != "" && supportsLoginExec(shell) {
		if len(extra) == 0 {
			return []string{shell, "-lic", "exec " + shellQuote(resolvedPath)}
		}
		if filepath.Base(shell) == "fish" {
			return append([]string{shell, "-lic", "exec " + shellQuote(resolvedPath) + " $argv", "--"}, extra...)
		}
		return append([]string{shell, "-lic", "exec " + shellQuote(resolvedPath) + ` "$@"`, "--"}, extra...)
	}
	if len(extra) == 0 {
		return []string{resolvedPath}
	}
	return append([]string{resolvedPath}, extra...)
}

// findAgentBinary resolves the executable path of agentID on this host.
func findAgentBinary(agentID, home, shell string) (string, bool) {
	sh := resolveUserShell(shell)
	dirs := agentSearchDirs(home, sh)
	return findExecutableInDirs(dirs, agentID)
}

// shellQuote single-quotes a path for a POSIX shell command line.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func supportsLoginExec(shell string) bool {
	switch filepath.Base(shell) {
	case "zsh", "bash", "sh", "fish", "ksh", "dash":
		return true
	}
	return false
}
