package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// hookCommand is what an agent's session-start hook runs. The trailing
// marker lets status and remove recognize our hook, and the failure is
// swallowed so an agent's start never fails for GoRex.
func hookCommand(agent string, paneNaming bool) string {
	name := "__internal session-start --agent " + agent
	if paneNaming {
		name += " --feature-pane-naming=true"
	} else {
		name += " --feature-pane-naming=false"
	}
	// `gorex` is on PATH in every GoRex terminal, through GOREX_BIN_DIR's
	// shim, so the hook keeps working when the app moves or updates.
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`cmd /c "gorex %s 2>nul & rem # gorex"`, name)
	}
	return `/bin/sh -c "gorex ` + name + ` 2>/dev/null || true # gorex"`
}

// isOurHook matches both this build's hook and the agent-hook of an older
// one, so setup replaces it instead of adding a second.
func isOurHook(cmd, agent string) bool {
	if !strings.Contains(cmd, "# gorex") {
		return false
	}
	return strings.Contains(cmd, "__internal session-start --agent "+agent) ||
		strings.Contains(cmd, "agent-hook --agent "+agent)
}

// readObject reads a JSON settings file. created is true when the file
// existed; an absent or empty file reads as an empty object.
func readObject(path string) (root map[string]any, created bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	created = true
	// Windows editors and PowerShell 5 start a UTF-8 file with a byte
	// order mark, which JSON does not allow.
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, true, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil, true, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, true, errors.New("trailing data after the JSON object")
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, true, nil
}

// writeObject writes a settings file atomically, with the permissions
// agent settings have.
func writeObject(path string, root map[string]any) error {
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return errors.New("settings is a symlink; not rewriting it")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		return err
	}
	b := buf.Bytes()
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

// hooksObject returns the hooks map and the SessionStart array, refusing
// shapes we would have to guess at.
func hooksObject(root map[string]any) (map[string]any, []any, error) {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		if _, exists := root["hooks"]; exists {
			return nil, nil, errors.New("hooks is not a JSON object")
		}
		hooks = map[string]any{}
	}
	entries, _ := hooks["SessionStart"].([]any)
	if _, exists := hooks["SessionStart"]; exists && entries == nil {
		return nil, nil, errors.New("SessionStart is not a JSON array")
	}
	return hooks, entries, nil
}

// agyHooksObject returns the gorex hook map and its SessionStart array for
// Antigravity CLI's hooks.json, refusing shapes we would have to guess at.
func agyHooksObject(root map[string]any) (map[string]any, []any, error) {
	gorex, _ := root["gorex"].(map[string]any)
	if gorex == nil {
		if _, exists := root["gorex"]; exists {
			return nil, nil, errors.New("gorex hook is not a JSON object")
		}
		gorex = map[string]any{}
	}
	entries, _ := gorex["SessionStart"].([]any)
	if _, exists := gorex["SessionStart"]; exists && entries == nil {
		return nil, nil, errors.New("SessionStart is not a JSON array")
	}
	return gorex, entries, nil
}

// hookCommands returns every command string in SessionStart for agent.
func hookCommands(root map[string]any, agent string) ([]string, error) {
	if agent == "agy" {
		_, entries, err := agyHooksObject(root)
		if err != nil {
			return nil, err
		}
		var cmds []string
		for _, h := range entries {
			hook, ok := h.(map[string]any)
			if !ok {
				return nil, errors.New("a hook is not a JSON object")
			}
			cmd, _ := hook["command"].(string)
			cmds = append(cmds, cmd)
		}
		return cmds, nil
	}
	_, entries, err := hooksObject(root)
	if err != nil {
		return nil, err
	}
	var cmds []string
	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			return nil, errors.New("a hook entry is not a JSON object")
		}
		items, _ := entry["hooks"].([]any)
		for _, h := range items {
			hook, ok := h.(map[string]any)
			if !ok {
				return nil, errors.New("a hook is not a JSON object")
			}
			cmd, _ := hook["command"].(string)
			cmds = append(cmds, cmd)
		}
	}
	return cmds, nil
}

// hookState tells whether the agent's session-start hook is installed and
// current, or why not.
func hookState(path, agent string) (string, error) {
	root, created, err := readObject(path)
	if err != nil {
		return "conflict", err
	}
	if !created {
		return "missing", nil
	}
	cmds, err := hookCommands(root, agent)
	if err != nil {
		return "conflict", err
	}
	state := "missing"
	for _, cmd := range cmds {
		if !isOurHook(cmd, agent) {
			continue
		}
		if strings.Contains(cmd, "__internal session-start --agent "+agent) &&
			!strings.Contains(cmd, "agent-hook --agent "+agent) {
			if state == "missing" {
				state = "installed"
			}
		} else {
			state = "outdated"
		}
	}
	return state, nil
}

// hookPaneNaming reports whether the installed hook asks agents to label
// the pane after their task.
func hookPaneNaming(path, agent string) bool {
	root, _, err := readObject(path)
	if err != nil {
		return false
	}
	cmds, err := hookCommands(root, agent)
	if err != nil {
		return false
	}
	for _, cmd := range cmds {
		if isOurHook(cmd, agent) && strings.Contains(cmd, "--feature-pane-naming=true") {
			return true
		}
	}
	return false
}

// setHook adds, replaces or removes our session-start hook, leaving every
// other hook and setting as it was.
func setHook(path, agent string, install, paneNaming, dry bool) (string, error) {
	if agent == "agy" {
		return setAgyHook(path, install, paneNaming, dry)
	}
	root, created, err := readObject(path)
	if err != nil {
		return "conflict", err
	}
	hooks, entries, err := hooksObject(root)
	if err != nil {
		return "conflict", err
	}
	command := hookCommand(agent, paneNaming)
	found, current, keptCurrent, stale := false, false, false, false
	kept := make([]any, 0, len(entries))
	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			return "conflict", errors.New("a hook entry is not a JSON object")
		}
		items, _ := entry["hooks"].([]any)
		if _, exists := entry["hooks"]; exists && items == nil {
			return "conflict", errors.New("a hook entry's hooks is not a JSON array")
		}
		var keep []any
		dropped := false
		for _, h := range items {
			hook, ok := h.(map[string]any)
			if !ok {
				return "conflict", errors.New("a hook is not a JSON object")
			}
			cmd, _ := hook["command"].(string)
			if isOurHook(cmd, agent) {
				found = true
				if install && cmd == command && !keptCurrent {
					keptCurrent = true
					current = true
					keep = append(keep, h)
				} else {
					dropped, stale = true, true
				}
				continue
			}
			keep = append(keep, h)
		}
		switch {
		case len(keep) > 0:
			entry["hooks"] = keep
			kept = append(kept, entry)
		case !dropped:
			kept = append(kept, entry)
		}
	}
	newEntry := map[string]any{"hooks": []any{map[string]any{
		"type": "command", "command": command, "timeout": 5,
	}}}
	return commitHookSessionStart(path, root, hooks, "hooks", kept, newEntry, created, install, found, current, stale, dry)
}

// setAgyHook adds, replaces or removes our session-start hook in
// Antigravity CLI's hooks.json, leaving every other named hook as it was.
func setAgyHook(path string, install, paneNaming, dry bool) (string, error) {
	root, created, err := readObject(path)
	if err != nil {
		return "conflict", err
	}
	gorex, entries, err := agyHooksObject(root)
	if err != nil {
		return "conflict", err
	}
	command := hookCommand("agy", paneNaming)
	found, current, keptCurrent, stale := false, false, false, false
	kept := make([]any, 0, len(entries))
	for _, h := range entries {
		hook, ok := h.(map[string]any)
		if !ok {
			return "conflict", errors.New("a hook is not a JSON object")
		}
		cmd, _ := hook["command"].(string)
		if isOurHook(cmd, "agy") {
			found = true
			if install && cmd == command && !keptCurrent {
				keptCurrent = true
				current = true
				kept = append(kept, h)
			} else {
				stale = true
			}
			continue
		}
		kept = append(kept, h)
	}
	newEntry := map[string]any{"type": "command", "command": command, "timeout": 5}
	return commitHookSessionStart(path, root, gorex, "gorex", kept, newEntry, created, install, found, current, stale, dry)
}

func commitHookSessionStart(path string, root, container map[string]any, containerKey string, kept []any, newEntry any, created, install, found, current, stale, dry bool) (string, error) {
	if install {
		switch {
		case current && !stale:
			return "unchanged", nil
		case current:
			if dry {
				return "would replace", nil
			}
			container["SessionStart"] = kept
			root[containerKey] = container
			if err := writeObject(path, root); err != nil {
				return "failed", err
			}
			return "replaced", nil
		case found:
			kept = append(kept, newEntry)
			if dry {
				return "would replace", nil
			}
			container["SessionStart"] = kept
			root[containerKey] = container
			if err := writeObject(path, root); err != nil {
				return "failed", err
			}
			return "replaced", nil
		default:
			kept = append(kept, newEntry)
			if dry {
				return "would create", nil
			}
			container["SessionStart"] = kept
			root[containerKey] = container
			if err := writeObject(path, root); err != nil {
				return "failed", err
			}
			return "created", nil
		}
	}
	if !found {
		return "absent", nil
	}
	if dry {
		return "would remove", nil
	}
	if len(kept) == 0 {
		delete(container, "SessionStart")
	} else {
		container["SessionStart"] = kept
	}
	if len(container) == 0 {
		delete(root, containerKey)
	} else {
		root[containerKey] = container
	}
	if !created {
		return "removed", nil
	}
	if err := writeObject(path, root); err != nil {
		return "failed", err
	}
	return "removed", nil
}

// existingPaneNaming reports whether the installed hooks already ask
// agents to label panes, so a setup without --pane-naming keeps the
// choice.
func existingPaneNaming(selected []agentInfo) bool {
	for _, a := range selected {
		if a.HookPath != "" && hookPaneNaming(a.HookPath, a.ID) {
			return true
		}
	}
	return false
}
