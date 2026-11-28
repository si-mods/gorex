package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
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

	// A human-created project tab has createdBy="human" in ls --json, and gives
	// gorex new a project to place tabs into.
	projDir := t.TempDir()
	if real, err := filepath.EvalSymlinks(projDir); err == nil {
		projDir = real
	}
	humanTab, err := c.Command(rex.LayoutCommand{Op: "project.create", Name: "demo", Dir: projDir})
	if err != nil || humanTab.Code != "" {
		t.Fatalf("project.create: %+v %v", humanTab, err)
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
	code, _, _, errOut = runMain(t, "send", "-s", humanTab.New, "--enter", "echo hi")
	if code != ExitOK {
		t.Fatalf("send: %d %s", code, errOut)
	}
	code, _, _, errOut = runMain(t, "paste", "-s", newInfo.ID, "--agent", "claude", "echo pasted")
	if code != ExitOK {
		t.Fatalf("paste --agent claude: %d %s", code, errOut)
	}
	code, _, _, errOut = runMain(t, "send", "-s", splitSID, "--key", "enter", "--agent", "codex")
	if code != ExitOK {
		t.Fatalf("send --key enter --agent codex: %d %s", code, errOut)
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
	if got := byID[splitSID].AgentActor; got.CreatedBy != rex.ActorCLI || got.Agent != "codex" || got.LastCLIInputAt.IsZero() || !got.Active {
		t.Fatalf("split session actor after send --key: %+v", got)
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

func TestCLITakeoverEventsAndWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	c := startServer(t)

	parentTab, err := c.Command(rex.LayoutCommand{Op: "tab.create"})
	if err != nil || parentTab.Code != "" {
		t.Fatalf("tab.create: %+v %v", parentTab, err)
	}
	parentSID := parentTab.New
	t.Setenv("GOREX_SESSION", parentSID)

	// Split a child pane from inside parentSID with --agent agy.
	code, _, out, errOut := runMain(t, "split", "-s", parentSID, "--agent", "agy", "--", "/bin/sh", "-c", "sleep 60")
	if code != ExitOK {
		t.Fatalf("split: %d %s", code, errOut)
	}
	childSID := strings.TrimSpace(out)
	t.Cleanup(func() { _ = c.Kill(childSID) })

	// Verify wait --takeover times out cleanly when no human takes over.
	code, _, _, _ = runMain(t, "wait", "-s", childSID, "--takeover", "--timeout", "100ms")
	if code != ExitFailed {
		t.Fatalf("wait --takeover timeout code = %d, want %d", code, ExitFailed)
	}

	// Stream gorex events while a human takes over childSID; verify a single "takeover" line prints.
	evR, evW := io.Pipe()
	defer evR.Close()
	go func() {
		var errBuf strings.Builder
		_, _ = Main([]string{"events"}, evW, &errBuf, "1.2.3")
		_ = evW.Close()
	}()
	// Trigger a layout event first so we know `gorex events` is subscribed before PasteHuman.
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(evR)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	subscribed := false
	for i := range 200 {
		if _, err := c.Command(rex.LayoutCommand{Op: "pane.rename", SID: childSID, Name: fmt.Sprintf("worker-%d", i)}); err != nil {
			t.Fatal(err)
		}
		select {
		case line := <-lines:
			if strings.Contains(line, `"event":"layout"`) {
				subscribed = true
			}
		case <-time.After(20 * time.Millisecond):
		}
		if subscribed {
			break
		}
	}
	if !subscribed {
		t.Fatal("timed out waiting for gorex events subscription")
	}

	// Start wait --takeover --json concurrently and trigger human takeover.
	type waitRes struct {
		code int
		out  string
		err  string
	}
	done := make(chan waitRes, 1)
	go func() {
		cCode, _, cOut, cErr := runMain(t, "wait", "-s", childSID, "--takeover", "--timeout", "3s", "--json")
		done <- waitRes{cCode, cOut, cErr}
	}()
	if err := c.PasteHuman(childSID, "human input"); err != nil {
		t.Fatal(err)
	}

	// Verify `gorex events` printed the exact takeover JSON line.
	var gotEvent rex.Event
	for line := range lines {
		if err := json.Unmarshal([]byte(line), &gotEvent); err == nil && gotEvent.Kind == "takeover" {
			break
		}
	}
	if gotEvent.Takeover == nil || gotEvent.Takeover.SID != childSID || gotEvent.Takeover.Agent != "agy" || gotEvent.Takeover.ParentSession != parentSID || gotEvent.Takeover.Via != "paste" {
		t.Fatalf("gorex events takeover = %+v", gotEvent.Takeover)
	}

	res := <-done
	if res.code != ExitOK {
		t.Fatalf("wait --takeover failed: code=%d err=%s out=%s", res.code, res.err, res.out)
	}
	var parsed struct {
		Session  string            `json:"session"`
		Takeover rex.TakeoverEvent `json:"takeover"`
	}
	if err := json.Unmarshal([]byte(res.out), &parsed); err != nil {
		t.Fatalf("unmarshal wait --takeover: %v (%q)", err, res.out)
	}
	if parsed.Session != childSID || parsed.Takeover.SID != childSID || parsed.Takeover.Agent != "agy" || parsed.Takeover.ParentSession != parentSID || (parsed.Takeover.Via != "paste" && parsed.Takeover.Via != "prior") {
		t.Fatalf("unexpected wait --takeover output: %+v", parsed)
	}

	// Late caller invoking wait --takeover after takeover already occurred returns immediately via TookOverAt.
	code, _, out, errOut = runMain(t, "wait", "-s", childSID, "--takeover", "--timeout", "200ms", "--json")
	if code != ExitOK {
		t.Fatalf("late wait --takeover failed: %d %s", code, errOut)
	}
	if !strings.Contains(out, `"via": "prior"`) {
		t.Fatalf("late wait --takeover output = %q, want via=prior", out)
	}
}
