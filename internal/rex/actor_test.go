//go:build darwin || linux || windows

package rex

import (
	"runtime"
	"slices"
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

func waitCond(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
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

	// A CLI-created session records createdBy="cli", stamps createdAt and lastCliInputAt, and sets active.
	cliSess, err := c.Create(CreateOptions{Cols: 80, Rows: 24, CreatedBy: ActorCLI, Agent: "agy"})
	if err != nil {
		t.Fatal(err)
	}
	if got := cliSess.AgentActor; got.CreatedBy != ActorCLI || got.Agent != "agy" || got.CreatedAt.IsZero() || got.LastCLIInputAt.IsZero() || !got.Active {
		t.Fatalf("cli session actor = %+v, want cli/agy/createdAt+lastCliInputAt stamped/active", got)
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
	waitCond(t, "attach reply bytes processed", func() bool {
		return !sessionByID(t, c, cliSess.ID).LastInput.IsZero()
	})
	if got := sessionByID(t, c, cliSess.ID).AgentActor; !got.Active {
		t.Fatalf("query replies or focus reports cleared active: %+v", got)
	}

	// Real human typing on the attached stream clears active while keeping audit fields.
	if _, err := att.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	waitCond(t, "human UI keystroke clears active", func() bool {
		return !sessionByID(t, c, cliSess.ID).AgentActor.Active
	})
	if got := sessionByID(t, c, cliSess.ID).AgentActor; got.CreatedBy != ActorCLI || !got.LastCLIInputAt.Equal(t0) {
		t.Fatalf("human takeover altered audit fields: %+v (want lastCliInputAt=%v)", got, t0)
	}

	// Subsequent CLI send stamps a newer lastCliInputAt, updates badge label, and re-arms active.
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
	if resumed.createdBy != ActorCLI || resumed.agentLabel != "agy" || !resumed.lastCLIInputAt.Equal(gotCmd.LastCLIInputAt) || resumed.info().AgentActor.CreatedAt.IsZero() {
		t.Fatalf("resumed actor = %+v, want (%q, %q, %v, non-zero createdAt)",
			resumed.info().AgentActor,
			ActorCLI, "agy", gotCmd.LastCLIInputAt)
	}
}

// TestAgentActorOnlyHumanTypingSetsTookOverAt verifies #1290: delegated and CLI
// panes never record tookOverAt or drop active on attach, focus/blur, mouse
// motion/wheel/click/release, terminal query replies, clipboard read denials,
// Kitty key-release or modifier-only events, or CLI writes — only genuine UI
// keyboard typing or paste sets tookOverAt.
func TestAgentActorOnlyHumanTypingSetsTookOverAt(t *testing.T) {
	nonHumanPayloads := []struct {
		name string
		data string
	}{
		{"focus_in_out", "\x1b[I\x1b[O"},
		{"sgr_mouse_motion", "\x1b[<35;42;12M"},
		{"sgr_mouse_wheel", "\x1b[<64;42;12M\x1b[<65;42;12M"},
		{"sgr_mouse_click_release", "\x1b[<0;15;8M\x1b[<0;15;8m"},
		{"x10_mouse", "\x1b[M !!"},
		{"urxvt_mouse", "\x1b[32;15;8M"},
		{"query_replies", "\x1b[?62;22c\x1b[>1;10;0c\x1b[12;34R\x1b[0n\x1b[?2004;1$y\x1b[8;24;80t\x1b[?1u\x1b[?997;1n"},
		{"osc_color_reply", "\x1b]11;rgb:2222/2525/2f2f\x1b\\"},
		{"osc_5522_denial", "\x1b]5522;type=read:status=EPERM\x1b\\\x1b]5522;type=read:status=DONE\x1b\\"},
		{"kitty_key_release", "\x1b[97;1:3u\x1b[1;1:3A\x1b[27;1:3u"},
		{"kitty_modifier_only", "\x1b[57441;2u\x1b[57442;5u\x1b[57444;9u\x1b[57450;2u"},
	}
	for _, tc := range nonHumanPayloads {
		if HasHumanInputBytes([]byte(tc.data)) {
			t.Fatalf("HasHumanInputBytes(%s = %q) = true, want false", tc.name, tc.data)
		}
	}
	for _, human := range []string{
		"x",
		"\r",
		"\x1b[A",
		"\x1b[97;1u",
		"\x1b[97;1:2u",
		"\x1b[200~pasted\x1b[201~",
		"\x1b]5522;type=paste:mime=dGV4dC9wbGFpbg==\x1b\\",
		"\x1b]5522;type=read:status=DATA;YWJj\x1b\\",
	} {
		if !HasHumanInputBytes([]byte(human)) {
			t.Fatalf("HasHumanInputBytes(%q) = false, want true", human)
		}
	}
	for _, nonTypingKey := range []KeyInput{
		{Key: "key_a", Action: "release"},
		{Key: "shift_left"},
		{Key: "control_right"},
		{Key: "super_left"},
		{Key: "caps_lock"},
	} {
		if nonTypingKey.IsTypingPress() {
			t.Fatalf("KeyInput(%+v).IsTypingPress() = true, want false", nonTypingKey)
		}
	}
	if !(KeyInput{Key: "key_j", Text: "j"}).IsTypingPress() {
		t.Fatal("KeyInput(key_j).IsTypingPress() = false, want true")
	}

	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()

	sub := pipe(s, "")
	defer sub.Close()
	if err := sub.SubscribeTransfers(); err != nil {
		t.Fatal(err)
	}

	del, err := c.Create(CreateOptions{
		Cols:       80,
		Rows:       24,
		CreatedBy:  ActorDelegate,
		Agent:      "agy",
		StampInput: true,
		Command:    shellSleepCommand(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !del.AgentActor.Active || !del.AgentActor.TookOverAt.IsZero() || del.AgentActor.CreatedAt.IsZero() {
		t.Fatalf("initial delegate actor = %+v", del.AgentActor)
	}
	fenceActorEvents(t, s, sub)

	att, err := c.AttachConn(Attach{SID: del.ID, Cols: 80, Rows: 24, Focused: true})
	if err != nil {
		t.Fatal(err)
	}
	defer att.Close()

	// Exercise every non-human byte sequence on both the attached PTY stream
	// (desktop attach) and Client.InputHuman (Rex API blockWrite used by iOS/web).
	for _, tc := range nonHumanPayloads {
		if _, err := att.Write([]byte(tc.data)); err != nil {
			t.Fatalf("att.Write(%s): %v", tc.name, err)
		}
		if err := c.InputHuman(del.ID, []byte(tc.data)); err != nil {
			t.Fatalf("InputHuman(%s): %v", tc.name, err)
		}
	}
	waitCond(t, "non-human attach stream bytes processed", func() bool {
		return !sessionByID(t, c, del.ID).LastInput.IsZero()
	})
	if err := c.SendFocus(del.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := c.SendMouse(del.ID, MouseInput{Action: "press", Button: "left", Col: 4, Row: 4}); err != nil {
		t.Fatal(err)
	}
	if err := c.SendKey(del.ID, KeyInput{Key: "key_a", Action: "release"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SendKey(del.ID, KeyInput{Key: "shift_left"}); err != nil {
		t.Fatal(err)
	}

	// Assert active=true, zero tookOverAt, and zero takeover events BEFORE any CLI re-arm write.
	if evs := fenceActorEvents(t, s, sub); slices.ContainsFunc(evs, func(ev Event) bool { return ev.Kind == "takeover" }) {
		t.Fatalf("non-typing activity emitted false takeover event in %+v", evs)
	}
	got := sessionByID(t, c, del.ID).AgentActor
	if !got.Active || !got.TookOverAt.IsZero() {
		t.Fatalf("delegate actor after non-typing activity = %+v, want active=true and zero tookOverAt", got)
	}

	// Ordinary CLI / delegate writes also keep active=true and tookOverAt zero.
	if err := c.InputWithAgent(del.ID, []byte("\n"), "agy"); err != nil {
		t.Fatal(err)
	}
	if evs := fenceActorEvents(t, s, sub); slices.ContainsFunc(evs, func(ev Event) bool { return ev.Kind == "takeover" }) {
		t.Fatalf("CLI write emitted false takeover event in %+v", evs)
	}
	got = sessionByID(t, c, del.ID).AgentActor
	if !got.Active || !got.TookOverAt.IsZero() {
		t.Fatalf("delegate actor after CLI write = %+v, want active=true and zero tookOverAt", got)
	}

	// Real human input via InputHuman (iOS/web terminal write) sets tookOverAt.
	if err := c.InputHuman(del.ID, []byte("j")); err != nil {
		t.Fatal(err)
	}
	if _, ok := waitTakeoverEvent(t, sub, del.ID, 2*time.Second); !ok {
		t.Fatal("real InputHuman keystroke did not emit takeover event")
	}
	afterHuman := sessionByID(t, c, del.ID).AgentActor
	if afterHuman.Active || afterHuman.TookOverAt.IsZero() {
		t.Fatalf("after real InputHuman keystroke: %+v, want inactive with tookOverAt", afterHuman)
	}
}

func fenceActorEvents(t *testing.T, s *Server, sub *Client) []Event {
	t.Helper()
	id := newID()
	s.transferred(Transfer{ID: id, SID: "fence", Direction: "put", State: "done"})
	var evs []Event
	for deadline := time.After(5 * time.Second); ; {
		select {
		case ev := <-sub.Events():
			if ev.Kind == "transfer" && ev.Transfer != nil && ev.Transfer.ID == id {
				return evs
			}
			evs = append(evs, ev)
		case <-deadline:
			t.Fatalf("timed out waiting for event fence after %d events", len(evs))
		}
	}
}

func shellSleepCommand() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe", "/c", "ping -n 6 127.0.0.1 >nul"}
	}
	return []string{"/bin/sh", "-c", "sleep 5"}
}
