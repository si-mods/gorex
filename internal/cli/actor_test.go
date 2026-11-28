package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gorex/internal/rex"
)

func TestCLIAgentActorAndFlags(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	c := startServer(t)

	// A human-created tab has createdBy="human" in ls --json.
	humanTab, err := c.Command(rex.LayoutCommand{Op: "tab.create"})
	if err != nil || humanTab.Code != "" {
		t.Fatalf("tab.create: %+v %v", humanTab, err)
	}

	// gorex new --agent agy stamps createdBy="cli", agent="agy", and lastCliInputAt.
	code, _, out, errOut := runMain(t, "new", "--agent", "agy", "--json")
	if code != ExitOK {
		t.Fatalf("new --agent agy: %d %s", code, errOut)
	}
	var newInfo rex.SessionInfo
	if err := json.Unmarshal([]byte(out), &newInfo); err != nil {
		t.Fatalf("unmarshal new: %v (%q)", err, out)
	}
	if newInfo.AgentActor.CreatedBy != rex.ActorCLI || newInfo.AgentActor.Agent != "agy" || newInfo.AgentActor.LastCLIInputAt.IsZero() || !newInfo.AgentActor.Active {
		t.Fatalf("new --agent agy actor = %+v", newInfo.AgentActor)
	}

	// gorex split --agent omp -- ... stamps createdBy="cli", agent="omp", and lastCliInputAt.
	code, _, out, errOut = runMain(t, "split", "-s", humanTab.New, "--agent", "omp", "--", "/bin/sh", "-c", "sleep 5")
	if code != ExitOK {
		t.Fatalf("split --agent omp: %d %s", code, errOut)
	}
	splitSID := strings.TrimSpace(out)
	if splitSID == "" {
		t.Fatal("split returned empty session id")
	}

	// gorex send (default --agent cli) and gorex paste (--agent claude) update lastCliInputAt and agent.
	time.Sleep(5 * time.Millisecond)
	code, _, _, errOut = runMain(t, "send", "-s", humanTab.New, "--enter", "echo hi")
	if code != ExitOK {
		t.Fatalf("send: %d %s", code, errOut)
	}
	code, _, _, errOut = runMain(t, "paste", "-s", newInfo.ID, "--agent", "claude", "echo pasted")
	if code != ExitOK {
		t.Fatalf("paste --agent claude: %d %s", code, errOut)
	}

	// gorex ls --json exposes agentActor: {createdBy, lastCliInputAt} on every session.
	code, _, out, errOut = runMain(t, "ls", "--json")
	if code != ExitOK {
		t.Fatalf("ls --json: %d %s", code, errOut)
	}
	var list []rex.SessionInfo
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("unmarshal ls: %v", err)
	}
	byID := map[string]rex.SessionInfo{}
	for _, in := range list {
		byID[in.ID] = in
	}

	if got := byID[humanTab.New].AgentActor; got.CreatedBy != rex.ActorHuman || got.Agent != rex.ActorCLI || got.LastCLIInputAt.IsZero() {
		t.Fatalf("human tab after default send: %+v", got)
	}
	if got := byID[newInfo.ID].AgentActor; got.CreatedBy != rex.ActorCLI || got.Agent != "claude" || !got.LastCLIInputAt.After(newInfo.AgentActor.LastCLIInputAt) {
		t.Fatalf("new session after paste --agent claude: %+v (was %v)", got, newInfo.AgentActor.LastCLIInputAt)
	}
	if got := byID[splitSID].AgentActor; got.CreatedBy != rex.ActorCLI || got.Agent != "omp" || got.LastCLIInputAt.IsZero() {
		t.Fatalf("split session actor: %+v", got)
	}
}

func TestCLIConfigAgentHighlight(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte("{\n  \"appearance\": \"dark\"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Default is on when agentHighlight is absent.
	code, _, out, errOut := runMain(t, "config", "get", "agent-highlight")
	if code != ExitOK || strings.TrimSpace(out) != "on" {
		t.Fatalf("get default: code=%d out=%q err=%q", code, out, errOut)
	}

	// Turn agent-highlight off and verify existing settings.json keys remain intact.
	code, _, _, errOut = runMain(t, "config", "set", "agent-highlight", "off")
	if code != ExitOK {
		t.Fatalf("set off: %d %s", code, errOut)
	}
	code, _, out, errOut = runMain(t, "config", "get", "agent-highlight", "--json")
	if code != ExitOK || !strings.Contains(out, `"agentHighlight": false`) {
		t.Fatalf("get off --json: code=%d out=%q err=%q", code, out, errOut)
	}
	rawBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(rawBytes, &raw); err != nil {
		t.Fatalf("settings.json invalid JSON: %v\n%s", err, rawBytes)
	}
	if raw["appearance"] != "dark" || raw["agentHighlight"] != false {
		t.Fatalf("settings.json = %+v, want appearance=dark and agentHighlight=false", raw)
	}

	// Turn agent-highlight back on.
	code, _, _, errOut = runMain(t, "config", "set", "agent-highlight", "on")
	if code != ExitOK {
		t.Fatalf("set on: %d %s", code, errOut)
	}
	code, _, out, errOut = runMain(t, "config", "get", "agent-highlight")
	if code != ExitOK || strings.TrimSpace(out) != "on" {
		t.Fatalf("get on: code=%d out=%q err=%q", code, out, errOut)
	}
}
