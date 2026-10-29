package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// hookSpec is where an agent keeps its session-start hooks.
type hookSpec struct {
	Agent string
	Path  func() (string, error)
	Event string
}

func hookSpecs() map[string]hookSpec {
	return map[string]hookSpec{
		"claude": {Agent: "claude", Path: claudeSettings, Event: "SessionStart"},
		"codex":  {Agent: "codex", Path: codexHooks, Event: "SessionStart"},
	}
}

func claudeSettings() (string, error) {
	base := os.Getenv("CLAUDE_CONFIG_DIR")
	if base == "" {
		h, err := homeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(h, ".claude")
	}
	return filepath.Join(base, "settings.json"), nil
}

func codexHooks() (string, error) {
	base := os.Getenv("CODEX_HOME")
	if base == "" {
		h, err := homeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(h, ".codex")
	}
	return filepath.Join(base, "hooks.json"), nil
}

func hookCommand(agent string) string {
	exe, err := os.Executable()
	if err != nil {
		exe = "gorex"
	}
	inner := shellQuote(exe) + " __internal session-start --agent " + agent + " 2>/dev/null || true # gorex"
	return "/bin/sh -c " + shellQuote(inner)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func hookMarker(agent string) string { return "__internal session-start --agent " + agent }

// hookState tells whether an agent's session-start hook is installed.
func hookState(agent string) (string, error) {
	spec, ok := hookSpecs()[agent]
	if !ok {
		return "not supported", nil
	}
	path, err := spec.Path()
	if err != nil {
		return "", err
	}
	root, err := readObject(path)
	if err != nil {
		return "conflict", err
	}
	if root == nil {
		return "missing", nil
	}
	return hasHook(root, spec.Event, hookMarker(agent)), nil
}

func hasHook(root map[string]any, event, marker string) string {
	hooks, _ := root["hooks"].(map[string]any)
	entries, _ := hooks[event].([]any)
	for _, e := range entries {
		entry, _ := e.(map[string]any)
		items, _ := entry["hooks"].([]any)
		for _, h := range items {
			hook, _ := h.(map[string]any)
			cmd, _ := hook["command"].(string)
			if isOurHook(cmd, marker) {
				return "installed"
			}
		}
	}
	return "missing"
}

func isOurHook(cmd, marker string) bool {
	return strings.Contains(cmd, marker) && strings.Contains(cmd, "# gorex")
}

func readObject(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var root map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

func writeObject(path string, root map[string]any) error {
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return errors.New("settings is a symlink; not rewriting it")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// setHook adds or removes this executable's session-start hook, leaving
// every other hook as it is.
func setHook(agent string, install, dry bool) (string, error) {
	spec, ok := hookSpecs()[agent]
	if !ok {
		return "not supported", nil
	}
	path, err := spec.Path()
	if err != nil {
		return "", err
	}
	root, err := readObject(path)
	if err != nil {
		return "conflict", err
	}
	if root == nil {
		root = map[string]any{}
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		if _, exists := root["hooks"]; exists {
			return "conflict", errors.New("hooks is not a JSON object")
		}
		hooks = map[string]any{}
	}
	entries, _ := hooks[spec.Event].([]any)
	if _, exists := hooks[spec.Event]; exists {
		if _, ok := hooks[spec.Event].([]any); !ok {
			return "conflict", errors.New(spec.Event + " is not a JSON array")
		}
	}
	marker := hookMarker(agent)
	command := hookCommand(agent)
	found, current := false, false
	var kept []any
	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			return "conflict", errors.New("a hook entry is not a JSON object")
		}
		items, ok := entry["hooks"].([]any)
		if !ok {
			if _, exists := entry["hooks"]; exists {
				return "conflict", errors.New("a hook entry's hooks is not a JSON array")
			}
			kept = append(kept, entry)
			continue
		}
		var keep []any
		for _, h := range items {
			hook, ok := h.(map[string]any)
			if !ok {
				return "conflict", errors.New("a hook is not a JSON object")
			}
			cmd, _ := hook["command"].(string)
			if isOurHook(cmd, marker) {
				found = true
				if install && cmd == command {
					current = true
					keep = append(keep, h)
				}
				continue
			}
			keep = append(keep, h)
		}
		if len(keep) > 0 || len(entry) > 1 {
			if len(keep) > 0 {
				entry["hooks"] = keep
			}
			kept = append(kept, entry)
		}
	}
	if install && current {
		return "current", nil
	}
	if install && (!found || !current) {
		kept = append(kept, map[string]any{"hooks": []any{map[string]any{
			"type": "command", "command": command, "timeout": 5,
		}}})
	}
	if !install && !found {
		return "missing", nil
	}
	if dry {
		if found {
			return "would remove", nil
		}
		return "would install", nil
	}
	if len(kept) == 0 {
		delete(hooks, spec.Event)
	} else {
		hooks[spec.Event] = kept
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooks
	}
	if err := writeObject(path, root); err != nil {
		return "failed", err
	}
	if install {
		return "installed", nil
	}
	return "removed", nil
}
