package rex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"gorex/internal/layout"
)

func TestBuildDelegateLaunch(t *testing.T) {
	args, env, err := buildDelegateLaunch("claude", "read-only", "", "11111111-2222-4333-8444-555555555555")
	if err != nil {
		t.Fatalf("claude read-only: %v", err)
	}
	if !slices.Equal(args, []string{"--session-id", "11111111-2222-4333-8444-555555555555", "--permission-mode", "plan"}) {
		t.Fatalf("claude args = %v", args)
	}
	if !slices.Contains(env, "CLAUDECODE=") || !slices.Contains(env, "CLAUDE_CODE_ENTRYPOINT=") {
		t.Fatalf("claude env = %v", env)
	}

	args, _, err = buildDelegateLaunch("claude", "full", "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", "")
	if err != nil || !slices.Equal(args, []string{"--resume", "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}) {
		t.Fatalf("claude resume: args=%v err=%v", args, err)
	}

	args, _, err = buildDelegateLaunch("codex", "read-only", "thr-1", "")
	if err != nil || !slices.Equal(args, []string{"resume", "thr-1", "--sandbox", "read-only"}) {
		t.Fatalf("codex: args=%v err=%v", args, err)
	}

	_, env, err = buildDelegateLaunch("devin", "full", "", "")
	if err != nil || !slices.Contains(env, "DEVIN_SESSION_STORE=files") {
		t.Fatalf("devin: env=%v err=%v", env, err)
	}

	args, _, err = buildDelegateLaunch("claude", "yolo", "", "11111111-2222-4333-8444-555555555555")
	if err != nil || !slices.Equal(args, []string{"--session-id", "11111111-2222-4333-8444-555555555555", "--permission-mode", "bypassPermissions"}) {
		t.Fatalf("claude yolo: args=%v err=%v", args, err)
	}

	args, _, err = buildDelegateLaunch("codex", "yolo", "", "")
	if err != nil || !slices.Equal(args, []string{"--full-auto"}) {
		t.Fatalf("codex yolo: args=%v err=%v", args, err)
	}

	for _, id := range []string{"omp", "agy", "devin"} {
		if _, _, err := buildDelegateLaunch(id, "read-only", "", ""); err == nil {
			t.Errorf("%s read-only should be refused", id)
		}
		if _, _, err := buildDelegateLaunch(id, "yolo", "", ""); err == nil {
			t.Errorf("%s yolo should be refused", id)
		}
	}

	// Verify a blocked turn can transition to completed once approved.
	run := &delegatedRun{status: "running", turnDone: make(chan struct{})}
	run.finishTurn("blocked", "", "approve Bash: git log")
	if run.status != "blocked" || run.errText != "approve Bash: git log" {
		t.Fatalf("blocked state = %q %q", run.status, run.errText)
	}
	run.finishTurn("completed", "all good", "")
	if run.status != "completed" || run.result != "all good" || run.errText != "" {
		t.Fatalf("recovered state = %q %q %q", run.status, run.result, run.errText)
	}
}

func TestAgentCommandWithArgsQuoting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell quoting test")
	}
	evil := "$(rm -rf /); 'quoted'"
	cmd := agentCommandWithArgs("/bin/zsh", "claude", "/usr/local/bin/claude", []string{"--session-id", evil})
	if len(cmd) != 6 || cmd[0] != "/bin/zsh" || cmd[1] != "-lic" || cmd[3] != "--" || cmd[5] != evil {
		t.Fatalf("zsh cmd = %#v", cmd)
	}
	if strings.Contains(cmd[2], evil) {
		t.Fatalf("evil arg leaked into shell string: %q", cmd[2])
	}
	fishCmd := agentCommandWithArgs("/opt/homebrew/bin/fish", "claude", "/usr/local/bin/claude", []string{"--session-id", evil})
	if !strings.Contains(fishCmd[2], "$argv") || fishCmd[5] != evil {
		t.Fatalf("fish cmd = %#v", fishCmd)
	}
}

func TestTranscriptParsers(t *testing.T) {
	cl := claudeTranscript{}
	if ev := cl.ParseRow([]byte(`{"type":"user","message":{"role":"user","content":"hi"}}`)); !ev.PromptAccepted {
		t.Fatalf("claude user row: %+v", ev)
	}
	if ev := cl.ParseRow([]byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`)); ev.PromptAccepted {
		t.Fatalf("claude tool_result should not count as prompt: %+v", ev)
	}
	if ev := cl.ParseRow([]byte(`{"type":"assistant","message":{"model":"claude-sonnet","content":[{"type":"text","text":"done"}]}}`)); ev.AssistantText != "done" || ev.TurnDone {
		t.Fatalf("claude assistant row: %+v", ev)
	}
	if ev := cl.ParseRow([]byte(`{"type":"system","subtype":"turn_duration"}`)); !ev.TurnDone || ev.IsError {
		t.Fatalf("claude turn_duration: %+v", ev)
	}
	if ev := cl.ParseRow([]byte(`{"type":"assistant","isApiErrorMessage":true,"message":{"model":"<synthetic>","content":[{"type":"text","text":"rate limited"}]}}`)); !ev.TurnDone || !ev.IsError || ev.ErrorText != "rate limited" {
		t.Fatalf("claude api error: %+v", ev)
	}

	omp := ompTranscript{}
	if ev := omp.ParseRow([]byte(`{"type":"session","id":"omp-1"}`)); ev.SessionID != "omp-1" {
		t.Fatalf("omp session: %+v", ev)
	}
	if ev := omp.ParseRow([]byte(`{"type":"message","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"omp ok"}]}}`)); ev.AssistantText != "omp ok" || !ev.TurnDone {
		t.Fatalf("omp assistant: %+v", ev)
	}

	cx := codexTranscript{}
	if ev := cx.ParseRow([]byte(`{"type":"session_meta","payload":{"id":"cx-1"}}`)); ev.SessionID != "cx-1" {
		t.Fatalf("codex meta: %+v", ev)
	}
	if ev := cx.ParseRow([]byte(`{"type":"event_msg","payload":{"type":"agent_message","message":"codex ok"}}`)); ev.AssistantText != "codex ok" {
		t.Fatalf("codex message: %+v", ev)
	}
	if ev := cx.ParseRow([]byte(`{"type":"event_msg","payload":{"type":"task_complete"}}`)); !ev.TurnDone {
		t.Fatalf("codex done: %+v", ev)
	}

	agy := agyTranscript{}
	if ev := agy.ParseRow([]byte(`{"type":"USER_INPUT","content":"go"}`)); !ev.PromptAccepted {
		t.Fatalf("agy user: %+v", ev)
	}
	if ev := agy.ParseRow([]byte(`{"type":"ASSISTANT_RESPONSE","content":"agy ok"}`)); ev.AssistantText != "agy ok" {
		t.Fatalf("agy assistant: %+v", ev)
	}

	dv := devinTranscript{}
	if ev := dv.ParseRow([]byte(`{"role":"assistant","type":"turn_end","content":"devin ok"}`)); ev.AssistantText != "devin ok" || !ev.TurnDone {
		t.Fatalf("devin: %+v", ev)
	}
}

func TestDelegateCallTimeoutAndConfigDir(t *testing.T) {
	if got := delegateCallTimeout(false, 90_000); got != 120*time.Second {
		t.Fatalf("sync 90s timeout = %v, want 120s", got)
	}
	if got := delegateCallTimeout(true, 90_000); got < 45*time.Second {
		t.Fatalf("async timeout = %v, want >= 45s", got)
	}

	// claudeTranscript.FindFile prefers explicit AgentConfigDir over CLAUDE_CONFIG_DIR.
	teamCfg := t.TempDir()
	otherCfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", otherCfg)
	projDir := filepath.Join(t.TempDir(), "app")
	sid := "11111111-2222-4333-8444-555555555555"
	want := filepath.Join(teamCfg, "projects", claudeSlug(projDir), sid+".jsonl")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := (claudeTranscript{}).FindFile(projDir, "", "", teamCfg, sid, time.Now()); got != want {
		t.Fatalf("FindFile = %q, want %q", got, want)
	}
}

func TestDelegateInteractiveTUIEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake POSIX TUI script")
	}

	origReady := delegateReadyWait
	origPaste := delegatePasteDelay
	origPoll := delegatePollInterval
	delegateReadyWait = 3 * time.Second
	delegatePasteDelay = 30 * time.Millisecond
	delegatePollInterval = 25 * time.Millisecond
	t.Cleanup(func() {
		delegateReadyWait = origReady
		delegatePasteDelay = origPaste
		delegatePollInterval = origPoll
	})

	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	claudeCfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeCfg)
	projDir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Fake interactive claude TUI: reads pasted lines from stdin and writes
	// genuine Claude Code JSONL rows to CLAUDE_CONFIG_DIR/projects/<slug>/<sid>.jsonl.
	binDir := t.TempDir()
	slug := claudeSlug(projDir)
	script := fmt.Sprintf(`#!/bin/sh
sid=""
while [ $# -gt 0 ]; do
  case "$1" in
    --session-id|--resume) sid="$2"; shift 2 ;;
    *) shift ;;
  esac
done
mkdir -p "%s/projects/%s"
out="%s/projects/%s/${sid}.jsonl"
printf '> '
while IFS= read -r line; do
  clean=$(printf '%%s' "$line" | tr -d '\r' | sed 's/\x1b\[200~//g; s/\x1b\[201~//g')
  [ -z "$clean" ] && continue
  printf '{"type":"user","sessionId":"%%s","message":{"role":"user","content":"%%s"}}\n' "$sid" "$clean" >> "$out"
  printf '{"type":"assistant","sessionId":"%%s","message":{"model":"claude-sonnet","content":[{"type":"text","text":"reply:%%s"}]}}\n' "$sid" "$clean" >> "$out"
  printf '{"type":"system","subtype":"turn_duration"}\n' >> "$out"
  printf 'reply:%%s\n> ' "$clean"
done
`, claudeCfg, slug, claudeCfg, slug)
	fakeClaude := filepath.Join(binDir, "claude")
	if err := os.WriteFile(fakeClaude, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	t.Setenv("SHELL", "/bin/sh")
	ResetAgentsCacheForTest()
	t.Cleanup(ResetAgentsCacheForTest)

	go func() { _ = Serve() }()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(SocketPath()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	c, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Shutdown() })

	// Seed project "app" in layout so delegate attaches under it.
	seed := layout.Store{
		Version:  layout.Version,
		Sessions: []layout.Workspace{{ID: "proj-app", Name: "app", Dir: projDir}},
	}
	rawSeed, _ := json.Marshal(seed)
	if err := c.SetLayout(rawSeed); err != nil {
		t.Fatal(err)
	}

	// 1. Synchronous turn with Keep: true.
	res, err := c.Delegate(DelegateOptions{
		Agent:     "claude",
		Dir:       projDir,
		Prompt:    "hello turn 1",
		From:      "alice-laptop/s1",
		Keep:      true,
		TimeoutMs: 5000,
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if res.Status != "completed" || res.Result != "reply:hello turn 1" || !IsUUID(res.AgentSession) || res.Session == "" {
		t.Fatalf("Delegate result = %+v", res)
	}

	// Verify tab was attached under project "app" in layout.
	rawLayout, err := c.Layout()
	if err != nil {
		t.Fatal(err)
	}
	st := layout.Parse(rawLayout)
	p := st.Project(projDir)
	if p == nil || len(p.Tabs) != 1 || p.Tabs[0].Root.SID != res.Session {
		t.Fatalf("delegated tab not attached under project app: %+v", p)
	}

	// Verify SessionInfo carries DelegatedFrom and DelegatedAgent.
	list, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	foundMeta := false
	for _, in := range list {
		if in.ID == res.Session && in.DelegatedFrom == "alice-laptop/s1" && in.DelegatedAgent == "claude" {
			foundMeta = true
		}
	}
	if !foundMeta {
		t.Fatalf("session list missing delegation metadata: %+v", list)
	}

	// 2. Warm multi-turn follow-up on the same live PTY session.
	res2, err := c.DelegateSend(DelegateSendOptions{
		SID:       res.Session,
		Prompt:    "follow up turn 2",
		TimeoutMs: 5000,
	})
	if err != nil {
		t.Fatalf("DelegateSend: %v", err)
	}
	if res2.Status != "completed" || res2.Result != "reply:follow up turn 2" || res2.Session != res.Session {
		t.Fatalf("DelegateSend result = %+v", res2)
	}

	// 3. DelegateResult returns the latest extracted output.
	got, err := c.DelegateResult(res.Session)
	if err != nil || got.Result != "reply:follow up turn 2" {
		t.Fatalf("DelegateResult = %+v, err=%v", got, err)
	}
	_ = c.Kill(res.Session)

	// 4. Synchronous turn without Keep closes the session on clean completion.
	res3, err := c.Delegate(DelegateOptions{
		Agent:     "claude",
		Dir:       projDir,
		Prompt:    "ephemeral turn",
		Keep:      false,
		TimeoutMs: 5000,
	})
	if err != nil || res3.Status != "completed" || res3.Result != "reply:ephemeral turn" {
		t.Fatalf("ephemeral Delegate = %+v, err=%v", res3, err)
	}
	listAfter, _ := c.List()
	for _, in := range listAfter {
		if in.ID == res3.Session {
			t.Fatalf("ephemeral delegated session %s was not closed on clean exit", res3.Session)
		}
	}
}
