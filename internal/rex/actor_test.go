//go:build darwin || linux || windows

package rex

import (
	"testing"
	"time"
)

func sessionByID(t *testing.T, c *Client, sid string) SessionInfo {
	t.Helper()
	infos, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range infos {
		if in.ID == sid {
			return in
		}
	}
	t.Fatalf("session %q not found in list", sid)
	return SessionInfo{}
}

// TestAgentActorBookkeeping verifies server-side actor tracking across creation,
// CLI send/paste, terminal query replies vs human UI keystrokes, and re-arming.
func TestAgentActorBookkeeping(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()

	// A default window-created session starts as human with no active mark.
	human, err := c.Create(CreateOptions{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if got := human.AgentActor; got.CreatedBy != ActorHuman || !got.LastCLIInputAt.IsZero() || got.Active {
		t.Fatalf("human session actor = %+v, want human/zero/inactive", got)
	}

	// A CLI-created session records createdBy="cli", stamps lastCliInputAt, and sets active.
	cliSess, err := c.Create(CreateOptions{Cols: 80, Rows: 24, CreatedBy: ActorCLI, Agent: "agy"})
	if err != nil {
		t.Fatal(err)
	}
	if got := cliSess.AgentActor; got.CreatedBy != ActorCLI || got.Agent != "agy" || got.LastCLIInputAt.IsZero() || !got.Active {
		t.Fatalf("cli session actor = %+v, want cli/agy/stamped/active", got)
	}
	t0 := cliSess.AgentActor.LastCLIInputAt

	// Attach a UI terminal stream to cliSess.
	att, err := c.AttachConn(Attach{SID: cliSess.ID, Cols: 80, Rows: 24, Focused: true})
	if err != nil {
		t.Fatal(err)
	}
	defer att.Close()

	// Terminal query replies (DA1, CPR) and window focus reports must NOT clear active.
	if _, err := att.Write([]byte("\x1b[?1;2c\x1b[1;1R\x1b[I\x1b[O")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if got := sessionByID(t, c, cliSess.ID).AgentActor; !got.Active {
		t.Fatalf("query replies or focus reports cleared active: %+v", got)
	}

	// Real human typing on the attached stream clears active while keeping audit fields.
	if _, err := att.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := sessionByID(t, c, cliSess.ID).AgentActor
		if !got.Active {
			if got.CreatedBy != ActorCLI || !got.LastCLIInputAt.Equal(t0) {
				t.Fatalf("human takeover altered audit fields: %+v (want lastCliInputAt=%v)", got, t0)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("human UI keystroke did not clear active: %+v", got)
		}
		time.Sleep(15 * time.Millisecond)
	}

	// Subsequent CLI send stamps a newer lastCliInputAt, updates badge label, and re-arms active.
	time.Sleep(5 * time.Millisecond)
	if err := c.InputWithAgent(cliSess.ID, []byte("b"), "omp"); err != nil {
		t.Fatal(err)
	}
	afterSend := sessionByID(t, c, cliSess.ID).AgentActor
	if !afterSend.Active || afterSend.Agent != "omp" || !afterSend.LastCLIInputAt.After(t0) {
		t.Fatalf("after InputWithAgent: %+v (prev %v)", afterSend, t0)
	}

	// KeyInput (mobile UI typing) clears active; FocusReport and Mouse do not re-arm it.
	if err := c.SendKey(cliSess.ID, KeyInput{Key: "key_c", Text: "c"}); err != nil {
		t.Fatal(err)
	}
	if got := sessionByID(t, c, cliSess.ID).AgentActor; got.Active {
		t.Fatalf("SendKey did not clear active: %+v", got)
	}
	if err := c.SendFocus(cliSess.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := sessionByID(t, c, cliSess.ID).AgentActor; got.Active {
		t.Fatalf("SendFocus re-armed active: %+v", got)
	}

	// PasteWithAgent on a human-created session stamps lastCliInputAt and activates it.
	if err := c.PasteWithAgent(human.ID, "echo hi", "claude"); err != nil {
		t.Fatal(err)
	}
	pasted := sessionByID(t, c, human.ID).AgentActor
	if pasted.CreatedBy != ActorHuman || pasted.Agent != "claude" || pasted.LastCLIInputAt.IsZero() || !pasted.Active {
		t.Fatalf("after PasteWithAgent on human session: %+v", pasted)
	}

	// PasteHuman clears active, sets tookOverAt, and leaves lastCliInputAt intact.
	tPaste := pasted.LastCLIInputAt
	if err := c.PasteHuman(human.ID, "user text"); err != nil {
		t.Fatal(err)
	}
	afterHumanPaste := sessionByID(t, c, human.ID).AgentActor
	if afterHumanPaste.Active || afterHumanPaste.TookOverAt.IsZero() || !afterHumanPaste.LastCLIInputAt.Equal(tPaste) {
		t.Fatalf("after PasteHuman: %+v (want inactive, tookOverAt set, lastCliInputAt=%v)", afterHumanPaste, tPaste)
	}
}

func waitTakeoverEvent(t *testing.T, sub *Client, sid string, timeout time.Duration) (TakeoverEvent, bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-sub.Events():
			if ev.Kind == "takeover" && ev.Takeover != nil && ev.Takeover.SID == sid {
				return *ev.Takeover, true
			}
		case <-deadline:
			return TakeoverEvent{}, false
		}
	}
}

// TestAgentTakeoverEvents checks edge-triggered "takeover" events on subscribe
// streams (local and remote), parentSession propagation, query-reply filtering,
// and suppression of duplicate events across subsequent human keystrokes.
func TestAgentTakeoverEvents(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()

	h, err := c.Hello()
	if err != nil {
		t.Fatal(err)
	}
	if !h.Can(CapabilityAgentTakeover) {
		t.Fatalf("Hello capabilities missing %q: %v", CapabilityAgentTakeover, h.Capabilities)
	}

	localSub := pipe(s, "")
	defer localSub.Close()
	if err := localSub.Subscribe(); err != nil {
		t.Fatal(err)
	}

	const remotePeer = "nodekey:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := c.AllowRemote("alice-laptop", remotePeer); err != nil {
		t.Fatal(err)
	}
	remoteSub := pipe(s, remotePeer)
	defer remoteSub.Close()
	if err := remoteSub.Subscribe(); err != nil {
		t.Fatal(err)
	}

	cliSess, err := c.Create(CreateOptions{
		Cols:          80,
		Rows:          24,
		CreatedBy:     ActorCLI,
		Agent:         "agy",
		ParentSession: "parent-001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cliSess.AgentActor.ParentSession != "parent-001" {
		t.Fatalf("parentSession = %q, want parent-001", cliSess.AgentActor.ParentSession)
	}

	att, err := c.AttachConn(Attach{SID: cliSess.ID, Cols: 80, Rows: 24, Focused: true})
	if err != nil {
		t.Fatal(err)
	}
	defer att.Close()

	// Query replies and focus reports must NOT emit a takeover event.
	if _, err := att.Write([]byte("\x1b[?1;2c\x1b[1;1R\x1b[I\x1b[O")); err != nil {
		t.Fatal(err)
	}
	if ev, ok := waitTakeoverEvent(t, localSub, cliSess.ID, 80*time.Millisecond); ok {
		t.Fatalf("query replies emitted unexpected takeover event: %+v", ev)
	}

	// First real human keystroke emits one takeover event to both local and remote subscribers.
	if _, err := att.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	evLocal, ok := waitTakeoverEvent(t, localSub, cliSess.ID, 2*time.Second)
	if !ok {
		t.Fatal("local subscriber received no takeover event on PTY keystroke")
	}
	if evLocal.Agent != "agy" || evLocal.CreatedBy != ActorCLI || evLocal.ParentSession != "parent-001" || evLocal.Via != "pty" || evLocal.At.IsZero() {
		t.Fatalf("unexpected local takeover event: %+v", evLocal)
	}
	evRemote, ok := waitTakeoverEvent(t, remoteSub, cliSess.ID, 2*time.Second)
	if !ok {
		t.Fatal("remote subscriber received no takeover event")
	}
	if evRemote.SID != cliSess.ID || evRemote.ParentSession != "parent-001" {
		t.Fatalf("unexpected remote takeover event: %+v", evRemote)
	}

	// Subsequent human keystrokes while already inactive must NOT emit duplicate events.
	if _, err := att.Write([]byte("bcd")); err != nil {
		t.Fatal(err)
	}
	if dup, ok := waitTakeoverEvent(t, localSub, cliSess.ID, 80*time.Millisecond); ok {
		t.Fatalf("subsequent human keystrokes emitted duplicate takeover event: %+v", dup)
	}

	// Re-arm via InputFromSession clears TookOverAt and updates ParentSession; SendKey emits "key".
	if err := c.InputFromSession(cliSess.ID, []byte("x"), "omp", "parent-002"); err != nil {
		t.Fatal(err)
	}
	if act := sessionByID(t, c, cliSess.ID).AgentActor; !act.Active || !act.TookOverAt.IsZero() || act.ParentSession != "parent-002" {
		t.Fatalf("after re-arm: %+v", act)
	}
	if err := c.SendKey(cliSess.ID, KeyInput{Key: "key_k", Text: "k"}); err != nil {
		t.Fatal(err)
	}
	evKey, ok := waitTakeoverEvent(t, localSub, cliSess.ID, 2*time.Second)
	if !ok {
		t.Fatal("SendKey did not emit takeover event")
	}
	if evKey.Agent != "omp" || evKey.ParentSession != "parent-002" || evKey.Via != "key" {
		t.Fatalf("unexpected SendKey takeover event: %+v", evKey)
	}

	// Re-arm via PasteWithAgent; PasteHuman emits "paste".
	if err := c.PasteWithAgent(cliSess.ID, "echo 1", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := c.PasteHuman(cliSess.ID, "echo 2"); err != nil {
		t.Fatal(err)
	}
	evPaste, ok := waitTakeoverEvent(t, localSub, cliSess.ID, 2*time.Second)
	if !ok {
		t.Fatal("PasteHuman did not emit takeover event")
	}
	if evPaste.Agent != "claude" || evPaste.Via != "paste" {
		t.Fatalf("unexpected PasteHuman takeover event: %+v", evPaste)
	}
}

// TestAgentActorSplitAndHandoff verifies CLI pane.split actor bookkeeping and
// preservation of AgentActor across session state handoff.
func TestAgentActorSplitAndHandoff(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()

	tab, err := c.Command(LayoutCommand{Op: "tab.create"})
	if err != nil || tab.Code != "" {
		t.Fatalf("tab.create: %+v %v", tab, err)
	}

	// CLI split with initial command stamps createdBy="cli", agent, active, and lastCliInputAt.
	spCmd, err := c.Command(LayoutCommand{
		Op:        "pane.split",
		SID:       tab.New,
		CreatedBy: ActorCLI,
		Agent:     "agy",
		Command:   shellSleepCommand(),
	})
	if err != nil || spCmd.Code != "" {
		t.Fatalf("pane.split with command: %+v %v", spCmd, err)
	}
	gotCmd := sessionByID(t, c, spCmd.New).AgentActor
	if gotCmd.CreatedBy != ActorCLI || gotCmd.Agent != "agy" || gotCmd.LastCLIInputAt.IsZero() || !gotCmd.Active {
		t.Fatalf("CLI split with command: %+v", gotCmd)
	}

	// CLI split without initial command sets createdBy="cli" and active=true, with default "cli" label.
	spShell, err := c.Command(LayoutCommand{
		Op:        "pane.split",
		SID:       tab.New,
		CreatedBy: ActorCLI,
	})
	if err != nil || spShell.Code != "" {
		t.Fatalf("pane.split shell: %+v %v", spShell, err)
	}
	gotShell := sessionByID(t, c, spShell.New).AgentActor
	if gotShell.CreatedBy != ActorCLI || gotShell.Agent != ActorCLI || !gotShell.Active {
		t.Fatalf("CLI split shell: %+v", gotShell)
	}

	// Verify state() -> resumeSession() round-trips AgentActor for exited/paused handoff.
	ss, err := s.session(spCmd.New)
	if err != nil {
		t.Fatal(err)
	}
	st := ss.state()
	st.Master = -1
	st.Exited = true
	resumed, err := resumeSession(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.createdBy != ActorCLI || resumed.agentLabel != "agy" || !resumed.lastCLIInputAt.Equal(gotCmd.LastCLIInputAt) {
		t.Fatalf("resumed actor = (%q, %q, %v), want (%q, %q, %v)",
			resumed.createdBy, resumed.agentLabel, resumed.lastCLIInputAt,
			ActorCLI, "agy", gotCmd.LastCLIInputAt)
	}
}

func shellSleepCommand() []string {
	return []string{"/bin/sh", "-c", "sleep 5"}
}
