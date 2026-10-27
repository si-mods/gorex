package control

import (
	"fmt"
	"testing"
	"time"
)

// TestResizeSettles drags the window of a tab rootshell holds: a burst of
// sizes resizes each session once, at the last size, after one
// tab.layout, and a fresh snapshot of the pane follows. A resize on the
// desktop is followed by one too.
func TestResizeSettles(t *testing.T) {
	desk := serve(t)
	oneTab(t, desk)
	r, _ := start(t)
	r.next("control.open", func(m map[string]any) bool { return m["id"] == "control.open" })
	snap := result(t, r.call("session.snapshot", map[string]any{}))["snapshot"].(map[string]any)
	tabID := snap["tabs"].([]any)[0].(map[string]any)["tab_id"].(string)
	sid := sidOf(snap["panes"].([]any)[0].(map[string]any)["pane_id"].(string))
	size := func() string {
		in := sessions(t, desk)[sid]
		return fmt.Sprintf("%dx%d", in.Cols, in.Rows)
	}
	geometry := func(cols int, claim bool) map[string]any {
		return map[string]any{"tab_id": tabID, "cols": cols, "rows": 30, "chrome": "none", "claim": claim}
	}
	aid := result(t, r.call("terminal.attach", map[string]any{"target": sid}))["attach_id"].(string)
	r.next("the attach's snapshot", func(m map[string]any) bool { return m["type"] == "terminal.snapshot" && m["attach_id"] == aid })
	result(t, r.call("tab.set_geometry", geometry(100, true)))
	if size() != "100x30" {
		t.Fatalf("claimed %s", size())
	}
	r.next("the claim's snapshot", func(m map[string]any) bool { return m["type"] == "terminal.snapshot" && m["attach_id"] == aid })

	// A burst, in under 100 ms: every answer comes at once, and the
	// session stays at its size until it settles.
	began := time.Now()
	for i := range 20 {
		if res := r.call("tab.set_geometry", geometry(101+i, false)); res["result"] == nil {
			t.Fatalf("answered %v", res)
		}
		if s := size(); s != "100x30" {
			t.Errorf("resized to %s in the burst", s)
		}
	}
	if d := time.Since(began); d > 500*time.Millisecond {
		t.Logf("the burst took %v", d)
	}
	var layouts, snaps int
	var last map[string]any
	deadline := time.After(3*geometrySettle + 2*redrawAfter)
	for done := false; !done; {
		select {
		case m := <-r.lines:
			switch {
			case m["type"] == "tab.layout":
				layouts++
				last = m["layout"].(map[string]any)
			case m["type"] == "terminal.snapshot" && m["attach_id"] == aid:
				if layouts == 0 {
					t.Error("a snapshot before the layout")
				}
				snaps++
				if st := m["snapshot"].(map[string]any)["state"].(map[string]any); st["cols"] != 120.0 {
					t.Errorf("snapshot at %v", st)
				}
			}
		case <-deadline:
			done = true
		}
	}
	if layouts != 1 || snaps != 1 || size() != "120x30" {
		t.Errorf("after the burst: %d layouts, %d snapshots, size %s", layouts, snaps, size())
	}
	if last != nil && last["area"].(map[string]any)["width"] != 120.0 {
		t.Errorf("layout %v", last)
	}

	// The desktop takes the size back, and resizes it.
	desk.UnlockSize(sid, "", 0, 0)
	desk.Resize(sid, 90, 25)
	m := r.next("the desktop's snapshot", func(m map[string]any) bool { return m["type"] == "terminal.snapshot" && m["attach_id"] == aid })
	if st := m["snapshot"].(map[string]any)["state"].(map[string]any); st["cols"] != 90.0 || st["rows"] != 25.0 {
		t.Errorf("snapshot at %v", st)
	}
}
