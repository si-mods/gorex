//go:build windows

package rex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// detectAgents finds which knownAgents are executable on Windows: first via
// exec.LookPath, then in the current registry PATH and common user tool
// directories (matching magpie's FindTool). Only .exe and .cmd files are
// accepted so CreateProcess never receives an extensionless npm sh shim.
func detectAgents(home, shell string) []AgentInfo {
	extraDirs := windowsAgentDirs(home)
	out := []AgentInfo{}
	for _, a := range knownAgents {
		if path, err := exec.LookPath(a.ID); err == nil {
			if cmd, ok := windowsAgentCommand(path, shell); ok {
				out = append(out, AgentInfo{ID: a.ID, Name: a.Name, Command: cmd})
				continue
			}
		}
		if path, ok := findWindowsTool(extraDirs, a.ID); ok {
			if cmd, ok := windowsAgentCommand(path, shell); ok {
				out = append(out, AgentInfo{ID: a.ID, Name: a.Name, Command: cmd})
			}
		}
	}
	return out
}

// agentShellLookPath resolves a shell's path; tests override it.
var agentShellLookPath = exec.LookPath

// windowsAgentShell is the PowerShell a new tab of the session's user
// runs: the user's own shell when it is one, else the first PowerShell on
// PATH. "" leaves the agent to run directly.
func windowsAgentShell(shell string) string {
	for _, sh := range []string{shell, os.Getenv("SHELL"), "pwsh.exe", "powershell.exe"} {
		if sh == "" {
			continue
		}
		if p, err := agentShellLookPath(sh); err == nil {
			switch ShellName(p) {
			case "pwsh", "powershell":
				return p
			}
		}
	}
	return ""
}

// windowsAgentCommand is the argv to launch an agent at path. A PowerShell
// runs it with &, so the profile's environment (a CLAUDE_CONFIG_DIR it
// sets, a proxy it exports) reaches the agent, as a shell -lic does on
// Unix. Without a PowerShell, an image CreateProcess runs (.exe, .com)
// runs as is, and a script of the command interpreter (.cmd, .bat — how
// npm installs CLIs on Windows) runs through %COMSPEC% /c, since
// CreateProcess cannot run it. Other extensions (an extensionless npm
// shim, .ps1) do not launch.
func windowsAgentCommand(path, shell string) ([]string, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".exe", ".com", ".cmd", ".bat":
	default:
		return nil, false
	}
	if sh := windowsAgentShell(shell); sh != "" {
		return []string{sh, "-Command", "& " + powerShellQuoted(path) + "; exit $LASTEXITCODE"}, true
	}
	if ext == ".exe" || ext == ".com" {
		return []string{path}, true
	}
	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		comspec = "cmd.exe"
	}
	return []string{comspec, "/c", path}, true
}

func windowsAgentDirs(home string) []string {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	var parts []string
	parts = append(parts, windowsRegistryPath()...)
	if appData := os.Getenv("APPDATA"); appData != "" {
		parts = append(parts, filepath.Join(appData, "npm"))
	}
	if home != "" {
		parts = append(parts, filepath.Join(home, ".local", "bin"))
	}
	if d := os.Getenv("BUN_INSTALL"); filepath.IsAbs(d) {
		parts = append(parts, filepath.Join(d, "bin"))
	}
	if home != "" {
		parts = append(parts, filepath.Join(home, ".bun", "bin"))
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		mise := filepath.Join(localAppData, "mise")
		if d := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
			mise = filepath.Join(d, "mise")
		}
		if d := os.Getenv("MISE_DATA_DIR"); filepath.IsAbs(d) {
			mise = d
		}
		parts = append(parts,
			filepath.Join(localAppData, "Volta", "bin"),
			filepath.Join(localAppData, "pnpm"),
			filepath.Join(mise, "shims"),
		)
	}
	seen := make(map[string]bool, len(parts))
	out := make([]string, 0, len(parts))
	for _, d := range parts {
		if d == "" || !filepath.IsAbs(d) {
			continue
		}
		key := strings.ToLower(d)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	return out
}

func windowsRegistryPath() []string {
	var dirs []string
	for _, k := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
		{registry.CURRENT_USER, `Environment`},
	} {
		key, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		if v, _, err := key.GetStringValue("Path"); err == nil {
			if x, err := registry.ExpandString(v); err == nil {
				v = x
			}
			for _, d := range strings.Split(v, ";") {
				if d = strings.TrimSpace(d); d != "" {
					dirs = append(dirs, d)
				}
			}
		}
		key.Close()
	}
	return dirs
}

func findWindowsTool(dirs []string, name string) (string, bool) {
	exts := []string{".exe", ".cmd"}
	for _, d := range dirs {
		for _, ext := range exts {
			p := filepath.Join(d, name+ext)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, true
			}
		}
	}
	return "", false
}
