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
func detectAgents(home, _ string) []AgentInfo {
	extraDirs := windowsAgentDirs(home)
	out := []AgentInfo{}
	for _, a := range knownAgents {
		if path, err := exec.LookPath(a.ID); err == nil {
			if cmd, ok := windowsAgentCommand(path); ok {
				out = append(out, AgentInfo{ID: a.ID, Name: a.Name, Command: cmd})
				continue
			}
		}
		if path, ok := findWindowsTool(extraDirs, a.ID); ok {
			if cmd, ok := windowsAgentCommand(path); ok {
				out = append(out, AgentInfo{ID: a.ID, Name: a.Name, Command: cmd})
			}
		}
	}
	return out
}

// windowsAgentCommand is the argv to launch an agent at path: an image
// CreateProcess runs (.exe, .com) as is, a script of the command
// interpreter (.cmd, .bat — how npm installs CLIs on Windows) through
// %COMSPEC% /c, since CreateProcess cannot run it. Other extensions (an
// extensionless npm shim, .ps1) do not launch.
func windowsAgentCommand(path string) ([]string, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".exe", ".com":
		return []string{path}, true
	case ".cmd", ".bat":
		comspec := os.Getenv("COMSPEC")
		if comspec == "" {
			comspec = "cmd.exe"
		}
		return []string{comspec, "/c", path}, true
	}
	return nil, false
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
