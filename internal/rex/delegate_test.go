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

	// Verify tab name resolution: explicit --name wins, else concise prompt summary.
	if got := delegateTabName("claude", "alice/s1", "Tech Debt Audit", "long prompt"); got != "Tech Debt Audit" {
		t.Fatalf("explicit tab name = %q", got)
	}
	if got := delegateTabName("claude", "alice/s1", "", "Check git status and summarize"); got != "Check git status and summari…" {
		t.Fatalf("prompt tab name = %q", got)
	}
	if got := delegateTabName("claude", "alice/s1", "", "Fix build"); got != "Fix build" {
		t.Fatalf("short prompt tab name = %q", got)
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
	if got := (claudeTranscript{}).FindFile(projDir, "", "", teamCfg, sid, time.Now(), nil); got != want {
		t.Fatalf("FindFile = %q, want %q", got, want)
	}
}

func TestStripANSIControlSequences(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"\x1bH", ""},
		{"\x1b[?9001h\x1b[?1004h\x1b[2J\x1bH\x1b(B\x1b=", ""},
		{"\x1b]0;GoRex\x07\x1b[?25l\x1b[?2004h", ""},
		{"\x1b[2J\x1b[H\x1b[32m❯\x1b[0m ", "❯"},
	}
	for _, tc := range cases {
		if got := stripANSI(tc.in); got != tc.want {
			t.Errorf("stripANSI(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWaitTUIReadyIgnoresControlOnlyOutput(t *testing.T) {
	origPoll := delegateReadyPoll
	origQuiet := delegateReadyQuiet
	origFallback := delegateReadyFallbackQuiet
	delegateReadyPoll = 5 * time.Millisecond
	delegateReadyQuiet = 25 * time.Millisecond
	delegateReadyFallbackQuiet = 60 * time.Millisecond
	t.Cleanup(func() {
		delegateReadyPoll = origPoll
		delegateReadyQuiet = origQuiet
		delegateReadyFallbackQuiet = origFallback
	})

	for _, agentID := range []string{"omp", "agy", "codex", "devin"} {
		t.Run(agentID+"_control_only_waits", func(t *testing.T) {
			start := time.Now()
			waitTUIReadyWith(func() tuiSnapshot {
				return tuiSnapshot{
					output:     16,
					lastOutput: start.Add(-time.Second),
					plainText:  stripANSI("\x1bH\x1b[2J\x1b[?25l"),
				}
			}, agentID, 80*time.Millisecond)
			if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
				t.Fatalf("%s: waitTUIReady returned in %v on control-only output, want full wait", agentID, elapsed)
			}
		})

		t.Run(agentID+"_ready_after_visible_settle", func(t *testing.T) {
			start := time.Now()
			readyAt := start.Add(20 * time.Millisecond)
			waitTUIReadyWith(func() tuiSnapshot {
				if time.Now().Before(readyAt) {
					return tuiSnapshot{
						output:     16,
						lastOutput: start,
						plainText:  stripANSI("\x1bH\x1b[2J"),
					}
				}
				return tuiSnapshot{
					output:     48,
					lastOutput: readyAt,
					bracketed:  true,
					plainText:  stripANSI("\x1b[2J\x1b[HWelcome\r\n❯ "),
				}
			}, agentID, 2*time.Second)
			elapsed := time.Since(start)
			if elapsed < 40*time.Millisecond || elapsed > 500*time.Millisecond {
				t.Fatalf("%s: waitTUIReady elapsed = %v, want between 40ms and 500ms", agentID, elapsed)
			}
		})

		t.Run(agentID+"_osc7501_idle_returns_immediately", func(t *testing.T) {
			start := time.Now()
			waitTUIReadyWith(func() tuiSnapshot {
				return tuiSnapshot{
					output:    4,
					idle:      true,
					plainText: stripANSI("\x1bH"),
				}
			}, agentID, time.Second)
			if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
				t.Fatalf("%s: waitTUIReady took %v despite OSC 7501 idle", agentID, elapsed)
			}
		})
	}
}

func TestTranscriptFindFileSkipsOldAndPreExisting(t *testing.T) {
	home := t.TempDir()
	projDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. omp: old session file in the same dir must NOT be matched even when tty
	// is set (no terminal-sessions entry yet) or when a pre-existing file is
	// modified after startedAt.
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, ".omp", "agent"))
	ompDir := filepath.Join(home, ".omp", "agent", "sessions", ompSlug(projDir, home))
	if err := os.MkdirAll(ompDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldOmp := filepath.Join(ompDir, "2026-11-26T10-00-00Z_11111111-2222-4333-8444-555555555555.jsonl")
	if err := os.WriteFile(oldOmp, []byte(`{"type":"session","id":"11111111-2222-4333-8444-555555555555"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-10 * time.Minute)
	_ = os.Chtimes(oldOmp, oldTime, oldTime)

	omp := ompTranscript{}
	excludeOmp := omp.ExistingFiles(projDir, home, "")
	startedAt := time.Now()

	// Without exclude, old mtime must still be rejected even with a tty.
	if got := omp.FindFile(projDir, "/dev/ttys009", home, "", "", startedAt, nil); got != "" {
		t.Fatalf("omp FindFile matched old file without exclude: %q", got)
	}
	// Even if another active omp session touches the pre-existing file after
	// startedAt, exclude must prevent binding to it.
	now := time.Now().Add(time.Second)
	_ = os.Chtimes(oldOmp, now, now)
	if got := omp.FindFile(projDir, "/dev/ttys009", home, "", "", startedAt, excludeOmp); got != "" {
		t.Fatalf("omp FindFile matched pre-existing active file: %q", got)
	}
	// Once the new session creates its own file, FindFile returns it.
	newOmp := filepath.Join(ompDir, "2026-11-27T18-00-00Z_aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee.jsonl")
	if err := os.WriteFile(newOmp, []byte(`{"type":"session","id":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := omp.FindFile(projDir, "/dev/ttys009", home, "", "", startedAt, excludeOmp); got != filepath.Clean(newOmp) {
		t.Fatalf("omp FindFile = %q, want %q", got, newOmp)
	}

	// 2. agy: caller's own active transcript_full.jsonl must be excluded.
	oldAgy := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "caller-conv", ".system_generated", "logs", "transcript_full.jsonl")
	if err := os.MkdirAll(filepath.Dir(oldAgy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldAgy, []byte(`{"type":"USER_INPUT","content":"caller"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	agy := agyTranscript{}
	excludeAgy := agy.ExistingFiles(projDir, home, "")
	agyStart := time.Now()
	// Simulate caller writing to its own transcript after delegate starts.
	if err := os.WriteFile(oldAgy, []byte(`{"type":"USER_INPUT","content":"caller2"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := agy.FindFile(projDir, "", home, "", "", agyStart, excludeAgy); got != "" {
		t.Fatalf("agy FindFile matched caller's pre-existing transcript: %q", got)
	}
	newAgy := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "child-conv", ".system_generated", "logs", "transcript_full.jsonl")
	if err := os.MkdirAll(filepath.Dir(newAgy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newAgy, []byte(`{"type":"USER_INPUT","content":"child"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := agy.FindFile(projDir, "", home, "", "", agyStart, excludeAgy); got != filepath.Clean(newAgy) {
		t.Fatalf("agy FindFile = %q, want %q", got, newAgy)
	}

	// 3. codex: pre-existing rollout must be excluded; new rollout matched.
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	oldCodex := filepath.Join(codexHome, "sessions", "2026", "11", "27", "rollout-old.jsonl")
	if err := os.MkdirAll(filepath.Dir(oldCodex), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldCodex, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cx := codexTranscript{}
	excludeCx := cx.ExistingFiles(projDir, home, "")
	cxStart := time.Now()
	if err := os.WriteFile(oldCodex, []byte(`{"type":"event_msg"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cx.FindFile(projDir, "", home, "", "", cxStart, excludeCx); got != "" {
		t.Fatalf("codex FindFile matched pre-existing rollout: %q", got)
	}
	newCodex := filepath.Join(codexHome, "sessions", "2026", "11", "27", "rollout-new.jsonl")
	if err := os.WriteFile(newCodex, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cx.FindFile(projDir, "", home, "", "", cxStart, excludeCx); got != filepath.Clean(newCodex) {
		t.Fatalf("codex FindFile = %q, want %q", got, newCodex)
	}

	// 4. devin: pre-existing log.jsonl must be excluded; new log.jsonl matched.
	devinDir := filepath.Join(home, "devin")
	t.Setenv("DEVIN_SESSIONS_DIR", devinDir)
	oldDevin := filepath.Join(devinDir, "sessions", "old-id", "log.jsonl")
	if err := os.MkdirAll(filepath.Dir(oldDevin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldDevin, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dv := devinTranscript{}
	excludeDv := dv.ExistingFiles(projDir, home, "")
	dvStart := time.Now()
	if err := os.WriteFile(oldDevin, []byte(`{"role":"user"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dv.FindFile(projDir, "", home, "", "", dvStart, excludeDv); got != "" {
		t.Fatalf("devin FindFile matched pre-existing log: %q", got)
	}
	newDevin := filepath.Join(devinDir, "sessions", "new-id", "log.jsonl")
	if err := os.MkdirAll(filepath.Dir(newDevin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newDevin, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dv.FindFile(projDir, "", home, "", "", dvStart, excludeDv); got != filepath.Clean(newDevin) {
		t.Fatalf("devin FindFile = %q, want %q", got, newDevin)
	}
}

func TestPollTranscriptOmpIgnoresOldFileAndResumeSeeksEnd(t *testing.T) {
	home := t.TempDir()
	projDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, ".omp", "agent"))
	ompDir := filepath.Join(home, ".omp", "agent", "sessions", ompSlug(projDir, home))
	if err := os.MkdirAll(ompDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Plant a completed old omp session in the same directory.
	oldID := "11111111-2222-4333-8444-555555555555"
	oldFile := filepath.Join(ompDir, "2026-11-26T10-00-00Z_"+oldID+".jsonl")
	oldContent := `{"type":"session","id":"` + oldID + `"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":"old prompt"}}` + "\n" +
		`{"type":"message","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"old reply"}]}}` + "\n"
	if err := os.WriteFile(oldFile, []byte(oldContent), 0o600); err != nil {
		t.Fatal(err)
	}

	adapter := ompTranscript{}
	srv := &Server{}
	run := &delegatedRun{
		sid:          "s1",
		agent:        "omp",
		dir:          projDir,
		home:         home,
		startedAt:    time.Now(),
		adapter:      adapter,
		excludeFiles: adapter.ExistingFiles(projDir, home, ""),
		promptAckCh:  make(chan struct{}, 1),
		status:       "running",
		turnDone:     make(chan struct{}),
		stopCh:       make(chan struct{}),
	}

	var partial strings.Builder
	srv.pollTranscriptOnce(run, nil, &partial)

	// Old file must NOT trigger promptAckCh, must NOT complete the turn, and
	// must NOT latch the old agentSession.
	select {
	case <-run.promptAckCh:
		t.Fatal("old omp transcript falsely triggered promptAckCh")
	default:
	}
	if run.status != "running" || run.result != "" || run.agentSession != "" || run.filePath != "" {
		t.Fatalf("old omp transcript polluted run state: status=%q result=%q sid=%q file=%q",
			run.status, run.result, run.agentSession, run.filePath)
	}

	// Now the new omp session writes its own file.
	newID := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	newFile := filepath.Join(ompDir, "2026-11-27T18-00-00Z_"+newID+".jsonl")
	newContent := `{"type":"session","id":"` + newID + `"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":"new prompt"}}` + "\n" +
		`{"type":"message","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"new reply"}]}}` + "\n"
	if err := os.WriteFile(newFile, []byte(newContent), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.pollTranscriptOnce(run, nil, &partial)
	select {
	case <-run.promptAckCh:
	default:
		t.Fatal("new omp transcript did not trigger promptAckCh")
	}
	if run.status != "completed" || run.result != "new reply" || run.agentSession != newID {
		t.Fatalf("new omp run = status:%q result:%q sid:%q", run.status, run.result, run.agentSession)
	}

	// Verify --resume seeks to the end of the existing file so old turns do not
	// falsely ack or complete the resumed turn.
	resumedRun := &delegatedRun{
		sid:          "s2",
		agent:        "omp",
		dir:          projDir,
		home:         home,
		agentSession: oldID,
		resumed:      true,
		startedAt:    time.Now(),
		adapter:      adapter,
		promptAckCh:  make(chan struct{}, 1),
		status:       "running",
		turnDone:     make(chan struct{}),
		stopCh:       make(chan struct{}),
	}
	resumedRun.seekResumedTranscript(nil)
	var resumedPartial strings.Builder
	srv.pollTranscriptOnce(resumedRun, nil, &resumedPartial)
	select {
	case <-resumedRun.promptAckCh:
		t.Fatal("resumed transcript falsely acked from prior turn")
	default:
	}
	if resumedRun.status != "running" {
		t.Fatalf("resumed run status = %q before new turn, want running", resumedRun.status)
	}

	// Append a new turn to the resumed file and verify it completes cleanly.
	f, err := os.OpenFile(oldFile, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"message","message":{"role":"user","content":"follow up"}}` + "\n")
	_, _ = f.WriteString(`{"type":"message","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"follow up reply"}]}}` + "\n")
	_ = f.Close()

	srv.pollTranscriptOnce(resumedRun, nil, &resumedPartial)
	if resumedRun.status != "completed" || resumedRun.result != "follow up reply" {
		t.Fatalf("resumed turn = status:%q result:%q", resumedRun.status, resumedRun.result)
	}
}

func TestDelegateInteractiveTUIEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake POSIX TUI script")
	}

	origReady := delegateReadyWait
	origReadyPoll := delegateReadyPoll
	origReadyQuiet := delegateReadyQuiet
	origReadyFallback := delegateReadyFallbackQuiet
	origPaste := delegatePasteDelay
	origPoll := delegatePollInterval
	delegateReadyWait = 3 * time.Second
	delegateReadyPoll = 10 * time.Millisecond
	delegateReadyQuiet = 30 * time.Millisecond
	delegateReadyFallbackQuiet = 80 * time.Millisecond
	delegatePasteDelay = 30 * time.Millisecond
	delegatePollInterval = 25 * time.Millisecond
	t.Cleanup(func() {
		delegateReadyWait = origReady
		delegateReadyPoll = origReadyPoll
		delegateReadyQuiet = origReadyQuiet
		delegateReadyFallbackQuiet = origReadyFallback
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
