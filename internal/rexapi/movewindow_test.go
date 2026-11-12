package rexapi

import (
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"gorex/internal/layout"
)

// spacesLayout is two sessions: work, with a zoomed window of a split and
// a minimized floating pane, then two plain windows; and play, of two
// windows, the second active.
func spacesLayout() layout.Store {
	return layout.Store{
		Version: layout.Version,
		Windows: []string{"work"},
		Sessions: []layout.Workspace{
			{ID: "work", Name: "work", Layout: layout.Layout{Tabs: []layout.Tab{
				{
					ID: "t-editor", Name: "editor", Root: split(pane("s1"), pane("s2"), false, 0.3), Focus: 1, Zoom: true,
					Floats: []layout.Float{{Pane: pane("f1"), X: 0.1, Y: 0.1, W: 0.5, H: 0.5, Hidden: true}},
				},
				{ID: "t-logs", Name: "logs", Root: pane("s3")},
				{ID: "t-last", Name: "last", Root: pane("s4")},
			}}},
			{ID: "play", Name: "play", Layout: layout.Layout{Active: 1, Tabs: []layout.Tab{
				{ID: "t-p1", Name: "p1", Root: pane("p1")},
				{ID: "t-p2", Name: "p2", Root: pane("p2")},
			}}},
		},
	}
}

// windowByLabel returns a view's window of a label.
func windowByLabel(t *testing.T, view map[string]any, label string) map[string]any {
	t.Helper()
	for _, raw := range view["windows"].([]any) {
		if w := raw.(map[string]any); w["label"] == label {
			return w
		}
	}
	t.Fatalf("no window %q in %v", label, view["windows"])
	return nil
}

// sessionEventsUntil reads session and server events until done is true
// of the types seen so far, by session ("" for server events).
func sessionEventsUntil(t *testing.T, c *testConn, done func(map[string][]string) bool) map[string][]string {
	t.Helper()
	seen := map[string][]string{}
	deadline := time.Now().Add(10 * time.Second)
	for !done(seen) {
		if time.Now().After(deadline) {
			t.Fatalf("events so far: %v", seen)
		}
		m := c.next()
		if m["type"] != "event" {
			continue
		}
		payload := m["payload"].(map[string]any)
		if payload["type"] == "session_event" {
			body := payload["payload"].(map[string]any)
			ev, _ := body["event"].(map[string]any)
			if ev != nil {
				sid := body["session_id"].(string)
				seen[sid] = append(seen[sid], ev["type"].(string))
			}
			continue
		}
		seen[""] = append(seen[""], payload["type"].(string))
	}
	return seen
}

// TestMoveWindowToSession moves windows between sessions with
// gorex.move_window: before a window, keeping the window's ID, label,
// blocks, zoom and floating layer, the other session's active window
// staying; then a session's last window, to the end, which destroys the
// session. The errors come first, and change nothing.
func TestMoveWindowToSession(t *testing.T) {
	f := startFixture(t)
	f.setLayout(spacesLayout())
	c := f.dialControl()
	c.hello(apiRevision)
	features, _ := c.welcome["features"].([]any)
	if !slices.Contains(features, any(featureMoveWindow)) {
		t.Fatalf("the welcome's features %v lack %s", features, featureMoveWindow)
	}
	ids := sessionIDs(t, c)
	work, play := ids["work"], ids["play"]
	callChecked(t, c, "session.attach", map[string]any{"session_id": work})
	callChecked(t, c, "session.attach", map[string]any{"session_id": play})
	// session_destroyed reaches the connections that hear of every session.
	obs := f.dialControl()
	obs.hello(apiRevision)
	callChecked(t, obs, "session.attach_all", map[string]any{"event_scope": "session"})
	view := viewOf(t, c, work)
	editor := windowByLabel(t, view, "editor")["window_id"].(string)
	logs := windowByLabel(t, view, "logs")["window_id"].(string)
	last := windowByLabel(t, view, "last")["window_id"].(string)
	pv := viewOf(t, c, play)
	p1 := windowByLabel(t, pv, "p1")["window_id"].(string)
	p2 := windowByLabel(t, pv, "p2")["window_id"].(string)
	before := windowByLabel(t, view, "editor")

	for _, tc := range []struct {
		payload map[string]any
		code    string
	}{
		{map[string]any{"session_id": work, "window_id": editor}, "bad_request"},
		{map[string]any{"session_id": work, "to_session_id": play}, "bad_request"},
		{map[string]any{"session_id": work, "window_id": editor, "to_session_id": work}, "bad_request"},
		{map[string]any{"session_id": work, "window_id": editor, "to_session_id": "session:nope"}, "session_not_found"},
		{map[string]any{"session_id": "session:nope", "window_id": editor, "to_session_id": play}, "session_not_found"},
		{map[string]any{"session_id": work, "window_id": "window:nope", "to_session_id": play}, "not_found"},
		{map[string]any{"session_id": work, "window_id": p1, "to_session_id": play}, "not_found"},
		{map[string]any{"session_id": work, "window_id": editor, "to_session_id": play, "before_window_id": logs}, "not_found"},
		{map[string]any{"session_id": work, "window_id": editor, "to_session_id": play, "index": 1}, "bad_request"},
	} {
		e := failure(t, c.request("bad", "gorex.move_window", tc.payload))
		if e["code"] != tc.code {
			t.Errorf("%v: %v, want %s", tc.payload, e, tc.code)
		}
	}
	if got := windowIDsOf(t, viewOf(t, c, work)); !slices.Equal(got, []string{editor, logs, last}) {
		t.Fatalf("a refused move changed the windows: %v", got)
	}

	// Before a window of the other session.
	res := callChecked(t, c, "gorex.move_window", map[string]any{
		"session_id": work, "window_id": editor, "to_session_id": play, "before_window_id": p2,
	})
	seen := sessionEventsUntil(t, c, func(s map[string][]string) bool {
		return slices.Contains(s[work], "session_view_changed") && slices.Contains(s[play], "session_view_changed")
	})
	for sid, want := range map[string][]string{work: {"window_closed", "block_closed"}, play: {"window_created", "block_created"}} {
		for _, typ := range want {
			if !slices.Contains(seen[sid], typ) {
				t.Errorf("%s sent %v, no %s", sid, seen[sid], typ)
			}
		}
	}
	view, pv = viewOf(t, c, work), viewOf(t, c, play)
	if got := windowIDsOf(t, view); !slices.Equal(got, []string{logs, last}) || view["active_window_id"] != logs {
		t.Errorf("work has %v, active %v; want logs then last, logs active", got, view["active_window_id"])
	}
	if got := windowIDsOf(t, pv); !slices.Equal(got, []string{p1, editor, p2}) || pv["active_window_id"] != p2 {
		t.Errorf("play has %v, active %v; want p1 editor p2, p2 active", got, pv["active_window_id"])
	}
	if res["revision"] != view["revision"] || res["to_revision"] != pv["revision"] || res["session_destroyed"] != nil {
		t.Errorf("result %v; work at %v, play at %v", res, view["revision"], pv["revision"])
	}
	after := windowByLabel(t, pv, "editor")
	for _, k := range []string{"window_id", "label", "focused_block_id", "zoomed_block_id"} {
		if after[k] != before[k] {
			t.Errorf("the window's %s was %v, is %v", k, before[k], after[k])
		}
	}
	if after["zoomed_block_id"] != blockID("s2") {
		t.Errorf("zoomed %v, want s2's block", after["zoomed_block_id"])
	}
	if got, want := blocksOfWindow(after), blocksOfWindow(before); !slices.Equal(got, want) || len(got) != 3 {
		t.Errorf("the window's blocks were %v, are %v", want, got)
	}
	if layer := layerOfBlock(t, map[string]any{"windows": []any{after}}, blockID("f1")); layer["state"] != "minimized" {
		t.Errorf("the floating layer: %v", layer)
	}

	// The session's last windows; the last one destroys it.
	callChecked(t, c, "gorex.move_window", map[string]any{"session_id": work, "window_id": logs, "to_session_id": play})
	res = callChecked(t, c, "gorex.move_window", map[string]any{"session_id": work, "window_id": last, "to_session_id": play})
	if res["session_destroyed"] != true || res["revision"] != nil {
		t.Errorf("the last window's move: %v", res)
	}
	sessionEventsUntil(t, obs, func(s map[string][]string) bool { return slices.Contains(s[""], "session_destroyed") })
	if _, ok := sessionIDs(t, c)["work"]; ok {
		t.Error("the emptied session stays")
	}
	pv = viewOf(t, c, play)
	if got := windowIDsOf(t, pv); !slices.Equal(got, []string{p1, editor, p2, logs, last}) || pv["active_window_id"] != p2 {
		t.Errorf("play has %v, active %v", got, pv["active_window_id"])
	}
	if res["to_revision"] != pv["revision"] {
		t.Errorf("to_revision %v, play at %v", res["to_revision"], pv["revision"])
	}
}

// blocksOfWindow returns the IDs of the blocks of a window of a view,
// every layer's.
func blocksOfWindow(w map[string]any) []string {
	var out []string
	for _, l := range w["layers"].([]any) {
		for _, b := range l.(map[string]any)["blocks"].([]any) {
			out = append(out, b.(map[string]any)["block_id"].(string))
		}
	}
	slices.Sort(out)
	return out
}

// TestMoveWindowOwners moves a window whose block a client attached only
// to the session it leaves owns: the block goes to the next client in
// line attached to the session it went to, with reason disconnect. An
// owner attached to both keeps its block.
func TestMoveWindowOwners(t *testing.T) {
	f := startFixture(t)
	f.setLayout(emptyStore())
	c := f.dialControl()
	c.hello(apiRevision)
	c1 := welcomeID(t, c.welcome)
	a := callChecked(t, c, "session.create", map[string]any{"label": "a", "initial_windows": []any{
		map[string]any{"window_label": "one", "layout": blockLayout()},
		map[string]any{"window_label": "two", "layout": blockLayout()},
	}})
	b := callChecked(t, c, "session.create", map[string]any{"label": "b", "initial_windows": []any{
		map[string]any{"window_label": "three", "layout": blockLayout()},
	}})
	sa, sb := a["session_id"].(string), b["session_id"].(string)
	one := a["initial_windows"].([]any)[0].(map[string]any)
	two := a["initial_windows"].([]any)[1].(map[string]any)
	block := one["block_ids"].([]any)[0].(string)
	kept := two["block_ids"].([]any)[0].(string)
	callChecked(t, c, "session.attach", map[string]any{"session_id": sa})
	callChecked(t, c, "session.attach", map[string]any{"session_id": sb})
	other := f.dialControl()
	other.hello(apiRevision)
	c2 := welcomeID(t, other.welcome)
	callChecked(t, other, "session.attach", map[string]any{"session_id": sa})
	callChecked(t, other, "session.own_block", map[string]any{"session_id": sa, "block_ids": []string{block}})
	if owner := ownerOfBlock(t, c, sa, block); owner != c2 {
		t.Fatalf("owner %q, want the other client %q", owner, c2)
	}

	callChecked(t, c, "gorex.move_window", map[string]any{
		"session_id": sa, "window_id": one["window_id"], "to_session_id": sb,
	})
	for {
		ev := waitEvent(t, c, "block_owner_changed")
		if ev["block_id"] != block || ev["reason"] != "disconnect" {
			continue
		}
		if ev["owner"] != c1 || ev["previous"] != c2 {
			t.Errorf("owner event: %v", ev)
		}
		break
	}
	if owner := ownerOfBlock(t, c, sb, block); owner != c1 {
		t.Errorf("owner %q in the session it went to, want %q", owner, c1)
	}
	// The other client's claim went with it: owning it again needs an
	// attach to that session.
	if e := failure(t, other.request("own", "session.own_block", map[string]any{"session_id": sb, "block_ids": []string{block}})); e["code"] != "bad_request" {
		t.Errorf("own_block of a session not attached to: %v", e)
	}

	callChecked(t, c, "gorex.move_window", map[string]any{
		"session_id": sa, "window_id": two["window_id"], "to_session_id": sb,
	})
	if owner := ownerOfBlock(t, c, sb, kept); owner != c1 {
		t.Errorf("owner %q of the block whose owner is attached to both, want %q", owner, c1)
	}
}

// TestMoveWindowHelp checks that session.help, help.all and /llms.txt
// describe gorex.move_window, and that its result schema compiles.
func TestMoveWindowHelp(t *testing.T) {
	for name, doc := range map[string]map[string]json.RawMessage{
		"session.help": helpDocument(apiRevision, true),
		"help.all":     helpAllDocument(apiRevision),
		"revision 0":   helpDocument(0, false),
	} {
		var methods []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(doc["methods"], &methods); err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(methods, func(m struct {
			Name string `json:"name"`
		}) bool {
			return m.Name == "gorex.move_window"
		}) {
			t.Errorf("%s does not list gorex.move_window", name)
		}
	}
	if schemaFor(t, "gorex.move_window") == nil {
		t.Error("gorex.move_window has no result schema")
	}
	f := startFixture(t)
	res, err := httpGet(f.srv.URL + "/llms.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "### gorex.move_window") {
		t.Error("/llms.txt does not describe gorex.move_window")
	}
}
