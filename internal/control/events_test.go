package control

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	"gorex/internal/rex"
)

// setIntervals sets the bridge's intervals for a test.
func setIntervals(t *testing.T, poll, safety, settled time.Duration) {
	t.Helper()
	p, s, d := pollEvery, safetyEvery, settle
	pollEvery, safetyEvery, settle = poll, safety, settled
	t.Cleanup(func() { pollEvery, safetyEvery, settle = p, s, d })
}

// oneTab gives the desktop's layout one tab of a new session, named name.
func oneTab(t *testing.T, desk *rex.Client) func(name string) {
	t.Helper()
	shell := []string{"/bin/sh"}
	if runtime.GOOS == "windows" {
		shell = []string{"cmd.exe"}
	}
	in, err := desk.Create(rex.CreateOptions{Command: shell, Dir: os.TempDir(), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	name := func(name string) {
		t.Helper()
		if err := desk.SetLayout(json.RawMessage(fmt.Sprintf(`{"tabs":[{"name":%q,"root":{"sid":%q}}]}`, name, in.ID))); err != nil {
			t.Fatal(err)
		}
	}
	name("a")
	return name
}

// TestBridgePushed checks that the bridge, polling never, tells rootshell
// of a change of the layout at once, and answers a layout command as soon
// as the desktop saved it.
func TestBridgePushed(t *testing.T) {
	setIntervals(t, time.Hour, time.Hour, settle)
	desk := serve(t)
	name := oneTab(t, desk)
	r, _ := start(t)
	r.next("control.open", func(m map[string]any) bool { return m["id"] == "control.open" })
	snap := result(t, r.call("session.snapshot", map[string]any{}))["snapshot"].(map[string]any)
	tabID := snap["tabs"].([]any)[0].(map[string]any)["tab_id"].(string)

	began := time.Now()
	name("b")
	r.next("events.gap", ofType("events.gap"))
	if d := time.Since(began); d > 250*time.Millisecond {
		t.Errorf("a renamed tab told after %v", d)
	}
	tabs := result(t, r.call("tab.list", map[string]any{}))["tabs"].([]any)
	if l := tabs[0].(map[string]any)["label"]; l != "b" {
		t.Errorf("label %v", l)
	}

	// The desktop makes the rename, as the app does.
	go func() {
		cmd, err := desk.TakeCommand()
		if err != nil || cmd == nil {
			t.Errorf("take: %v %v", cmd, err)
			return
		}
		name(cmd.Name)
		desk.CommandDone(rex.LayoutCommand{ID: cmd.ID})
	}()
	time.Sleep(50 * time.Millisecond) // the take waits
	began = time.Now()
	res := result(t, r.call("tab.rename", map[string]any{"tab_id": tabID, "label": "c"}))
	if d := time.Since(began); d > 250*time.Millisecond {
		t.Errorf("tab.rename answered after %v", d)
	}
	if l := res["tab"].(map[string]any)["label"]; l != "c" {
		t.Errorf("renamed %v", res)
	}
}

// holdAnswers has the bridge take the app's answer to each command hold
// takes only once hold returns, as a loaded machine may.
func holdAnswers(t *testing.T, hold func(j *job)) {
	t.Helper()
	was := answered
	answered = hold
	t.Cleanup(func() { answered = was })
}

// TestSplitLandsBeforeAnswer checks that a split that lands before the
// bridge has its answer shows rootshell the new pane focused, as the
// answer makes it, and never the pane split first.
func TestSplitLandsBeforeAnswer(t *testing.T) {
	setIntervals(t, time.Hour, time.Hour, settle)
	desk := serve(t)
	oneTab(t, desk)
	holdAnswers(t, func(j *job) {
		if j.cmd.Op == "pane.split" {
			time.Sleep(300 * time.Millisecond) // the server tells of the split meanwhile
		}
	})
	r, _ := start(t)
	r.next("control.open", func(m map[string]any) bool { return m["id"] == "control.open" })
	snap := result(t, r.call("session.snapshot", map[string]any{}))["snapshot"].(map[string]any)
	split := snap["panes"].([]any)[0].(map[string]any)["pane_id"].(string)
	r.call("pane.focus", map[string]any{"pane_id": split})
	r.send("split", "pane.split", map[string]any{"target_pane_id": split, "direction": "right", "focus": true})
	var focused []any
	r.next("pane.split's answer", func(m map[string]any) bool {
		if l, ok := m["layout"].(map[string]any); ok && m["type"] == "tab.layout" && len(l["panes"].([]any)) == 2 {
			focused = append(focused, l["focused_pane_id"])
		}
		return m["id"] == "split"
	})
	if len(focused) == 0 || slices.Contains(focused, any(split)) {
		t.Errorf("focused %v before the answer, want the new pane only", focused)
	}
}

// TestQueuedChangeHoldsNoLayout checks that a change asked behind
// another, which the app holds, does not hold back what rootshell hears
// of the first before its answer.
func TestQueuedChangeHoldsNoLayout(t *testing.T) {
	setIntervals(t, time.Hour, time.Hour, settle)
	desk := serve(t)
	oneTab(t, desk)
	release := make(chan struct{})
	holdAnswers(t, func(j *job) {
		if j.cmd.Op == "pane.split" {
			<-release
		}
	})
	r, _ := start(t)
	t.Cleanup(func() { close(release) })
	r.next("control.open", func(m map[string]any) bool { return m["id"] == "control.open" })
	snap := result(t, r.call("session.snapshot", map[string]any{}))["snapshot"].(map[string]any)
	tabID := snap["tabs"].([]any)[0].(map[string]any)["tab_id"].(string)
	pane := snap["panes"].([]any)[0].(map[string]any)["pane_id"].(string)
	r.send("rename", "tab.rename", map[string]any{"tab_id": tabID, "label": "b"})
	r.send("split", "pane.split", map[string]any{"target_pane_id": pane, "direction": "right"})
	told := false
	m := r.next("tab.rename's answer", func(m map[string]any) bool {
		told = told || m["type"] == "events.gap"
		return m["id"] == "rename"
	})
	if !told {
		t.Errorf("tab.rename answered %v before rootshell heard of the rename", m)
	}
}

// TestBridgeSafetyPoll checks that a change the bridge is not told of
// reaches rootshell by the safety poll.
func TestBridgeSafetyPoll(t *testing.T) {
	setIntervals(t, time.Hour, 300*time.Millisecond, time.Hour)
	desk := serve(t)
	name := oneTab(t, desk)
	r, _ := start(t)
	r.next("control.open", func(m map[string]any) bool { return m["id"] == "control.open" })
	result(t, r.call("session.snapshot", map[string]any{}))
	name("b")
	r.next("events.gap", ofType("events.gap"))
	tabs := result(t, r.call("tab.list", map[string]any{}))["tabs"].([]any)
	if l := tabs[0].(map[string]any)["label"]; l != "b" {
		t.Errorf("label %v", l)
	}
}
