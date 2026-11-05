package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tempHome is a home directory no agent is installed in yet.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PI_CODING_AGENT_DIR", "")
	return home
}

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSkillSetupMergesHooks(t *testing.T) {
	home := tempHome(t)
	mkdirs(t, filepath.Join(home, ".claude"))
	settings := filepath.Join(home, ".claude", "settings.json")
	writeFile(t, settings, `{
  "theme": "x",
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command", "command": "other-hook"}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "stop-me"}]}]
  }
}`)

	code, _, out, errOut := runMain(t, "skill", "setup", "--agent", "claude", "--json")
	if code != ExitOK {
		t.Fatalf("setup: %d %q %s", code, out, errOut)
	}
	if !strings.Contains(out, `"action": "install"`) || !strings.Contains(out, `"change": "created"`) {
		t.Fatalf("setup JSON %q", out)
	}
	skill := readFile(t, filepath.Join(home, ".claude", "skills", "gorex", "SKILL.md"))
	if !strings.Contains(skill, skillMarker) || !strings.Contains(skill, "__internal skill") {
		t.Fatalf("skill %q", skill)
	}
	after := readFile(t, settings)
	if !strings.Contains(after, "other-hook") || !strings.Contains(after, "stop-me") || !strings.Contains(after, `"theme": "x"`) {
		t.Fatalf("settings lost a key: %s", after)
	}
	if strings.Count(after, "# gorex") != 1 || !strings.Contains(after, "__internal session-start --agent claude") {
		t.Fatalf("settings hook: %s", after)
	}

	code, _, out, errOut = runMain(t, "skill", "setup", "--agent", "claude", "--json")
	if code != ExitOK || !strings.Contains(out, `"change": "unchanged"`) {
		t.Fatalf("second setup: %d %q %s", code, out, errOut)
	}
	if strings.Count(readFile(t, settings), "# gorex") != 1 {
		t.Fatal("a second setup added a hook")
	}

	code, _, out, errOut = runMain(t, "skill", "status", "--agent", "claude", "--json")
	if code != ExitOK || !strings.Contains(out, `"state": "current"`) || !strings.Contains(out, `"state": "installed"`) {
		t.Fatalf("status: %d %q %s", code, out, errOut)
	}

	code, _, out, errOut = runMain(t, "skill", "remove", "--agent", "claude", "--json")
	if code != ExitOK || !strings.Contains(out, `"change": "removed"`) {
		t.Fatalf("remove: %d %q %s", code, out, errOut)
	}
	after = readFile(t, settings)
	if strings.Contains(after, "# gorex") || !strings.Contains(after, "other-hook") || !strings.Contains(after, "stop-me") {
		t.Fatalf("settings after remove: %s", after)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "gorex")); !os.IsNotExist(err) {
		t.Fatalf("skill remains: %v", err)
	}
}

func TestSkillDetectsAgents(t *testing.T) {
	home := tempHome(t)
	mkdirs(t, filepath.Join(home, ".codex"), filepath.Join(home, ".config", "opencode"))

	code, _, out, errOut := runMain(t, "skill", "setup", "--json")
	if code != ExitOK {
		t.Fatalf("setup: %d %q %s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "gorex", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "gorex")); !os.IsNotExist(err) {
		t.Fatalf("setup wrote the Claude skill without Claude: %v", err)
	}
	if b := readFile(t, filepath.Join(home, ".codex", "hooks.json")); !strings.Contains(b, "__internal session-start --agent codex") {
		t.Fatalf("codex hook %q", b)
	}

	code, _, out, errOut = runMain(t, "skill", "status", "--json")
	if code != ExitOK {
		t.Fatalf("status: %d %q %s", code, out, errOut)
	}
	var st statusResult
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatal(err)
	}
	byID := map[string]harnessStatus{}
	for _, h := range st.Harnesses {
		byID[h.ID] = h
	}
	if byID["claude"].Detected || !byID["codex"].Detected || !byID["opencode"].Detected || byID["pi"].Detected {
		t.Fatalf("detection %+v", byID)
	}
}

func TestSkillNoAgentsInstallsShared(t *testing.T) {
	home := tempHome(t)
	code, _, out, errOut := runMain(t, "skill", "setup", "--json")
	if code != ExitOK {
		t.Fatalf("setup: %d %q %s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "gorex", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".claude", ".codex"} {
		if _, err := os.Stat(filepath.Join(home, p)); !os.IsNotExist(err) {
			t.Fatalf("setup wrote %s without an agent: %v", p, err)
		}
	}
}

func TestSkillConflictAndForce(t *testing.T) {
	home := tempHome(t)
	path := filepath.Join(home, ".claude", "skills", "gorex", "SKILL.md")
	writeFile(t, path, "mine\n")

	code, _, _, _ := runMain(t, "skill", "setup", "--agent", "claude", "--dry-run")
	if code != ExitFailed {
		t.Fatalf("dry run over a user's skill: %d", code)
	}
	if got := readFile(t, path); got != "mine\n" {
		t.Fatalf("dry run changed the user's skill: %q", got)
	}
	code, _, _, _ = runMain(t, "skill", "setup", "--agent", "claude")
	if code != ExitFailed {
		t.Fatalf("setup over a user's skill: %d", code)
	}
	if got := readFile(t, path); got != "mine\n" {
		t.Fatalf("the user's skill changed: %q", got)
	}
	code, _, _, _ = runMain(t, "skill", "remove", "--agent", "claude")
	if code != ExitFailed || readFile(t, path) != "mine\n" {
		t.Fatalf("remove took the user's skill: %d", code)
	}
	code, _, _, _ = runMain(t, "skill", "setup", "--agent", "claude", "--force")
	if code != ExitOK {
		t.Fatalf("setup --force: %d", code)
	}
	if got := readFile(t, path); !strings.Contains(got, skillMarker) {
		t.Fatalf("force did not install: %q", got)
	}
}

func TestSkillReplacesLegacyInstall(t *testing.T) {
	home := tempHome(t)
	mkdirs(t, filepath.Join(home, ".claude"))
	settings := filepath.Join(home, ".claude", "settings.json")
	// A current hook and a legacy one: setup keeps one and drops the other.
	current := strings.ReplaceAll(hookCommand("claude", false), `"`, `\"`)
	writeFile(t, settings, `{"theme":"x","hooks":{"SessionStart":[`+
		`{"hooks":[{"type":"command","command":"keep-me"}]},`+
		`{"hooks":[{"type":"command","command":"`+current+`"}]},`+
		`{"hooks":[{"type":"command","command":"/bin/sh -c '/old/gorex agent-hook --agent claude 2>/dev/null || true # gorex'"}]}]}}`)
	skill := filepath.Join(home, ".claude", "skills", "gorex", "SKILL.md")
	writeFile(t, skill, legacySkillMarker+"\nold guide\n")
	piExt := filepath.Join(home, ".pi", "agent", "extensions", "gorex", "index.ts")
	ompExt := filepath.Join(home, ".omp", "agent", "extensions", "gorex", "index.ts")
	writeFile(t, piExt, legacyExtensionMarker+": old\n")
	writeFile(t, ompExt, legacyExtensionMarker+": old\n")

	code, _, out, errOut := runMain(t, "skill", "setup", "--agent", "claude", "--json")
	if code != ExitOK {
		t.Fatalf("setup: %d %q %s", code, out, errOut)
	}
	after := readFile(t, settings)
	if strings.Contains(after, "agent-hook") || strings.Count(after, "# gorex") != 1 || !strings.Contains(after, "keep-me") {
		t.Fatalf("settings after replacing the legacy hook: %s", after)
	}
	if got := readFile(t, skill); !strings.Contains(got, skillMarker) {
		t.Fatalf("legacy skill not replaced: %q", got)
	}
	for _, p := range []string{piExt, ompExt} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("legacy extension remains at %s: %v", p, err)
		}
	}
}

func TestSkillProject(t *testing.T) {
	home := tempHome(t)
	project := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })

	code, _, out, errOut := runMain(t, "skill", "setup", "--project", "--agent", "claude", "--json")
	if code != ExitOK {
		t.Fatalf("project setup: %d %q %s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "gorex", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	hook := readFile(t, filepath.Join(project, ".claude", "settings.local.json"))
	if !strings.Contains(hook, "__internal session-start --agent claude") {
		t.Fatalf("project hook %q", hook)
	}

	code, _, _, errOut = runMain(t, "skill", "remove", "--project")
	if code != ExitOK {
		t.Fatalf("project remove: %d %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "gorex")); !os.IsNotExist(err) {
		t.Fatalf("project skill remains: %v", err)
	}
	if b := readFile(t, filepath.Join(project, ".claude", "settings.local.json")); strings.Contains(b, "# gorex") {
		t.Fatalf("project hook remains: %s", b)
	}

	// A project setup without an agent writes only the shared skill.
	code, _, _, errOut = runMain(t, "skill", "setup", "--project")
	if code != ExitOK {
		t.Fatalf("shared project setup: %d %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "gorex", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	_ = home
}

func TestSessionStart(t *testing.T) {
	t.Setenv("GOREX_SESSION", "")
	t.Setenv("GOREX_PANE", "")
	code, _, out, _ := runMain(t, "__internal", "session-start", "--agent", "claude")
	if code != ExitOK || out != "" {
		t.Fatalf("outside GoRex: %d %q", code, out)
	}

	t.Setenv("GOREX_SESSION", "abc123")
	t.Setenv("GOREX_PANE", "4")
	code, _, out, _ = runMain(t, "__internal", "session-start", "--agent", "claude", "--feature-session-naming=false")
	if code != ExitOK || !strings.Contains(out, "GOREX_SESSION=abc123") || !strings.Contains(out, "GOREX_PANE=4") ||
		!strings.Contains(out, "GoRex terminal") || strings.Contains(out, "Name the session") {
		t.Fatalf("hook output: %d %q", code, out)
	}
	_, _, out, _ = runMain(t, "__internal", "session-start", "--agent", "claude", "--feature-session-naming=true")
	if !strings.Contains(out, "Name the session") {
		t.Fatalf("naming hook output: %q", out)
	}
}

func TestSkillBOMAndInvalidJSON(t *testing.T) {
	home := tempHome(t)
	mkdirs(t, filepath.Join(home, ".claude"))
	settings := filepath.Join(home, ".claude", "settings.json")
	writeFile(t, settings, "\ufeff"+`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"keep"}]}]}}`)
	code, _, out, errOut := runMain(t, "skill", "setup", "--agent", "claude", "--json")
	if code != ExitOK {
		t.Fatalf("BOM setup: %d %q %s", code, out, errOut)
	}
	if b := readFile(t, settings); !strings.Contains(b, "keep") || strings.Contains(b, "\ufeff") {
		t.Fatalf("BOM settings %q", b)
	}

	writeFile(t, settings, `{} {}`)
	code, _, out, errOut = runMain(t, "skill", "setup", "--agent", "claude", "--json")
	if code != ExitFailed || !strings.Contains(out, `"change": "conflict"`) {
		t.Fatalf("trailing JSON setup: %d %q %s", code, out, errOut)
	}
	if got := readFile(t, settings); got != `{} {}` {
		t.Fatalf("trailing settings changed: %q", got)
	}

	writeFile(t, settings, "not json")
	code, _, out, errOut = runMain(t, "skill", "setup", "--agent", "claude", "--json")
	if code != ExitFailed || !strings.Contains(out, `"change": "conflict"`) {
		t.Fatalf("invalid JSON setup: %d %q %s", code, out, errOut)
	}
	if got := readFile(t, settings); got != "not json" {
		t.Fatalf("invalid settings changed: %q", got)
	}
}

func TestSkillShowIsTheGuide(t *testing.T) {
	code, _, out, _ := runMain(t, "skill", "show")
	if code != ExitOK || !strings.HasPrefix(out, "# GoRex") || !strings.Contains(out, "gorex capture") {
		t.Fatalf("skill show: %d %q", code, out)
	}
	code, _, internal, _ := runMain(t, "__internal", "skill")
	if code != ExitOK || internal != out {
		t.Fatalf("internal skill differs: %d %q", code, internal)
	}
}

// TestSkillStatusJSONShape keeps the fields agents and scripts read.
func TestSkillStatusJSONShape(t *testing.T) {
	home := tempHome(t)
	mkdirs(t, filepath.Join(home, ".claude"))
	code, _, out, _ := runMain(t, "skill", "status", "--json")
	if code != ExitOK {
		t.Fatalf("status: %d", code)
	}
	var raw map[string]any
	if err := json.Unmarshal(bytes.TrimSpace([]byte(out)), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["scope"] != "user" {
		t.Fatalf("scope %v", raw["scope"])
	}
	hs, _ := raw["harnesses"].([]any)
	if len(hs) != 4 {
		t.Fatalf("harnesses %v", hs)
	}
}
