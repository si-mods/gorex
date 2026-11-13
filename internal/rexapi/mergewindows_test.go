package rexapi

import (
	"slices"
	"testing"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// TestMergeWindowsAndBlockToNewWindow merges a window into another with
// gorex.merge_windows, which keeps the into window's ID and makes it
// active, then moves a block of it to a window of its own with
// gorex.move_block_to_new_window. The errors change nothing.
func TestMergeWindowsAndBlockToNewWindow(t *testing.T) {
	f := startFixture(t)
	f.setLayout(spacesLayout())
	c := f.dialControl()
	c.hello(apiRevision)
	features, _ := c.welcome["features"].([]any)
	for _, want := range []string{featureMergeWindows, featureNewWindowFromBlock} {
		if !slices.Contains(features, any(want)) {
			t.Fatalf("the welcome's features %v lack %s", features, want)
		}
	}
	work := sessionIDs(t, c)["work"]
	callChecked(t, c, "session.attach", map[string]any{"session_id": work})
	view := viewOf(t, c, work)
	editor := windowByLabel(t, view, "editor")["window_id"].(string)
	logs := windowByLabel(t, view, "logs")["window_id"].(string)
	last := windowByLabel(t, view, "last")["window_id"].(string)

	for _, tc := range []struct {
		method  string
		payload map[string]any
		code    string
	}{
		{"gorex.merge_windows", map[string]any{"session_id": work, "window_id": logs, "into_window_id": logs}, "bad_request"},
		{"gorex.merge_windows", map[string]any{"session_id": work, "window_id": logs, "into_window_id": "window:nope"}, "not_found"},
		{"gorex.merge_windows", map[string]any{"session_id": work, "window_id": logs}, "bad_request"},
		{"gorex.move_block_to_new_window", map[string]any{"session_id": work, "block_id": blockID("s3")}, "conflict"},
		{"gorex.move_block_to_new_window", map[string]any{"session_id": work, "block_id": blockID("f1")}, "conflict"},
		{"gorex.move_block_to_new_window", map[string]any{"session_id": work, "block_id": blockID("s1")}, "conflict"},
		{"gorex.move_block_to_new_window", map[string]any{"session_id": work, "block_id": "block:nope"}, "not_found"},
	} {
		e := failure(t, c.request("bad", tc.method, tc.payload))
		if e["code"] != tc.code {
			t.Errorf("%s %v: %v, want %s", tc.method, tc.payload, e, tc.code)
		}
	}
	if got := windowIDsOf(t, viewOf(t, c, work)); !slices.Equal(got, []string{editor, logs, last}) {
		t.Fatalf("a refused call changed the windows: %v", got)
	}

	// logs merges into last: last keeps its ID, has s4 then s3 side by
	// side, and is active.
	res := callChecked(t, c, "gorex.merge_windows", map[string]any{"session_id": work, "window_id": logs, "into_window_id": last})
	view = viewOf(t, c, work)
	if got := windowIDsOf(t, view); !slices.Equal(got, []string{editor, last}) || view["active_window_id"] != last {
		t.Fatalf("work has %v, active %v; want editor then last, last active", got, view["active_window_id"])
	}
	if res["revision"] != view["revision"] {
		t.Errorf("result %v; work at %v", res, view["revision"])
	}
	merged := windowByLabel(t, view, "last")
	split := merged["layers"].([]any)[0].(map[string]any)["layout"].(map[string]any)["split"].(map[string]any)
	if b, a := split["before"].(map[string]any)["block_id"], split["after"].(map[string]any)["block_id"]; split["direction"] != "horizontal" || b != blockID("s4") || a != blockID("s3") {
		t.Errorf("the merged window's split: %v", split)
	}
	if merged["focused_block_id"] != blockID("s3") {
		t.Errorf("the merged window's focus is %v, want the merged block", merged["focused_block_id"])
	}

	// s3 goes to a window of its own, after last; last stays active.
	res = callChecked(t, c, "gorex.move_block_to_new_window", map[string]any{"session_id": work, "block_id": blockID("s3")})
	view = viewOf(t, c, work)
	to, _ := res["window_id"].(string)
	if got := windowIDsOf(t, view); !slices.Equal(got, []string{editor, last, to}) || view["active_window_id"] != last {
		t.Fatalf("work has %v, active %v; want editor, last, then %s, last active", got, view["active_window_id"], to)
	}
	if res["revision"] != view["revision"] {
		t.Errorf("result %v; work at %v", res, view["revision"])
	}
	for id, want := range map[string][]string{last: {blockID("s4")}, to: {blockID("s3")}} {
		for _, raw := range view["windows"].([]any) {
			if w := raw.(map[string]any); w["window_id"] == id && !slices.Equal(blocksOfWindow(w), want) {
				t.Errorf("window %s has %v, want %v", id, blocksOfWindow(w), want)
			}
		}
	}
	// s4 is its window's only block now.
	e := failure(t, c.request("bad", "gorex.move_block_to_new_window", map[string]any{"session_id": work, "block_id": blockID("s4")}))
	if e["code"] != "conflict" {
		t.Errorf("moving a window's only block: %v", e)
	}
}

// TestMergeWindowsRefuses refuses to merge a window that holds a pane of
// no terminal, which tab.set would leave behind in both windows, and
// shows that the merge's batch is refused whole when the source window
// changed since it was read (its Base), so no part of it is merged.
func TestMergeWindowsRefuses(t *testing.T) {
	f := startFixture(t)
	st := spacesLayout()
	tabs := st.Sessions[0].Tabs
	tabs[1].Root = split(pane("s3"), &layout.Node{Kind: "note", Note: "n1"}, false, 0.5)
	f.setLayout(st)
	c := f.dialControl()
	c.hello(apiRevision)
	work := sessionIDs(t, c)["work"]
	callChecked(t, c, "session.attach", map[string]any{"session_id": work})
	view := viewOf(t, c, work)
	logs := windowByLabel(t, view, "logs")["window_id"].(string)
	last := windowByLabel(t, view, "last")["window_id"].(string)
	e := failure(t, c.request("bad", "gorex.merge_windows", map[string]any{"session_id": work, "window_id": logs, "into_window_id": last}))
	if e["code"] != "conflict" {
		t.Errorf("merging a window with a note: %v, want conflict", e)
	}
	if got := windowIDsOf(t, viewOf(t, c, work)); len(got) != 3 {
		t.Fatalf("a refused merge changed the windows: %v", got)
	}

	// The batch the gateway sends, built on a source revision that moved on.
	raw, err := f.desk.Layout()
	if err != nil {
		t.Fatal(err)
	}
	parsed := layout.Parse(raw)
	ws := parsed.Space("work")
	from, into := ws.Tabs[2], ws.Tabs[0] // last into editor: both of terminals only
	merged := into
	merged.Root = &layout.Node{A: into.Root, B: from.Root, Ratio: 0.5}
	res, err := f.desk.Command(rex.LayoutCommand{Op: "layout.apply", Batch: []rex.LayoutCommand{
		{Op: "space.rename", Space: "work", Tab: from.ID, Base: from.Rev + 1},
		{Op: "tab.set", Space: "work", TabLayout: &merged, Focus: true, Base: into.Rev},
	}})
	if err != nil || res.Code != "conflict" {
		t.Fatalf("a stale source revision: %+v %v, want conflict", res, err)
	}
	if after, _ := f.desk.Layout(); string(after) != string(raw) {
		t.Errorf("a refused batch changed the layout:\n%s\n%s", raw, after)
	}
	// The same batch on the revisions as read is taken: the refusal was the Base's.
	res, err = f.desk.Command(rex.LayoutCommand{Op: "layout.apply", Batch: []rex.LayoutCommand{
		{Op: "space.rename", Space: "work", Tab: from.ID, Base: from.Rev},
		{Op: "tab.set", Space: "work", TabLayout: &merged, Focus: true, Base: into.Rev},
	}})
	if err != nil || res.Code != "" {
		t.Fatalf("the batch on the current revisions: %+v %v", res, err)
	}
}
