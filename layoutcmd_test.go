package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"gorex/internal/control"
	"gorex/internal/rex"
)

// phone drives a bridge as rootshell does, running the app's frames while
// it waits, as the app makes the changes it asks for in them.
type phone struct {
	t     *testing.T
	tt    *ui.Tester
	in    io.Writer
	lines chan map[string]any
	log   []map[string]any
	n     int
}

func (r *phone) next(what string, match func(m map[string]any) bool) map[string]any {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case m, ok := <-r.lines:
			if !ok {
				r.t.Fatalf("%s: the bridge ended", what)
			}
			r.log = append(r.log, m)
			if match(m) {
				return m
			}
		case <-time.After(20 * time.Millisecond):
			r.tt.Frame()
		}
	}
	r.t.Fatalf("timed out waiting for %s", what)
	return nil
}

// call sends a request and returns its result, failing on an error.
func (r *phone) call(method string, params map[string]any) map[string]any {
	r.t.Helper()
	r.n++
	id := fmt.Sprintf("r%d", r.n)
	b, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	r.in.Write(append(b, '\n'))
	m := r.next(method, func(m map[string]any) bool { return m["id"] == id })
	res, ok := m["result"].(map[string]any)
	if !ok {
		r.t.Fatalf("%s: %v", method, m)
	}
	return res
}

// since returns the lines read since the n-th.
func (r *phone) since(n int) []map[string]any { return r.log[n:] }

func (r *phone) snapshot() (tabs []string, labels map[string]string, panes map[string][]string) {
	snap := r.call("session.snapshot", map[string]any{})["snapshot"].(map[string]any)
	labels, panes = map[string]string{}, map[string][]string{}
	for _, x := range snap["tabs"].([]any) {
		t := x.(map[string]any)
		tabs = append(tabs, t["tab_id"].(string))
		labels[t["tab_id"].(string)] = t["label"].(string)
	}
	for _, x := range snap["panes"].([]any) {
		p := x.(map[string]any)
		panes[p["tab_id"].(string)] = append(panes[p["tab_id"].(string)], p["terminal_id"].(string))
	}
	return
}

// TestRootshellLayout changes the window's layout from rootshell, through
// the bridge and the app's own code: new tabs, splits, zoom, closing,
// renaming and moving, each seen by rootshell before its answer.
func TestRootshellLayout(t *testing.T) {
	a, tt := newTestApp(t)
	first := a.tab()
	go a.st.takeCommands(a.client, a.post)
	a.saveNow()
	c, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() { control.Run(c, inR, outW); outW.Close() }()
	r := &phone{t: t, tt: tt, in: inW, lines: make(chan map[string]any, 1024)}
	go func() {
		defer close(r.lines)
		sc := bufio.NewScanner(outR)
		sc.Buffer(nil, 16<<20)
		for sc.Scan() {
			var m map[string]any
			json.Unmarshal(sc.Bytes(), &m)
			r.lines <- m
		}
	}()
	defer inW.Close()
	r.next("control.open", func(m map[string]any) bool { return m["id"] == "control.open" })
	tabs, _, panes := r.snapshot()
	if len(tabs) != 1 || len(panes[tabs[0]]) != 1 {
		t.Fatalf("tabs %v, panes %v", tabs, panes)
	}
	t1, sid1 := tabs[0], panes[tabs[0]][0]
	r.call("pane.focus", map[string]any{"pane_id": "p-" + sid1})

	// A new tab goes after the tab focused, the desktop's stays active,
	// and rootshell hears of it before the answer.
	mark := len(r.log)
	start := time.Now()
	res := r.call("tab.create", map[string]any{"workspace_id": "w", "focus": true})
	t.Logf("tab.create answered in %v", time.Since(start))
	tab, root := res["tab"].(map[string]any), res["root_pane"].(map[string]any)
	t2, sid2 := tab["tab_id"].(string), root["terminal_id"].(string)
	if root["tab_id"] != t2 || tab["label"] == nil || tab["pane_count"] != 1.0 || t2 == t1 {
		t.Errorf("tab.create %v", res)
	}
	if !slices.ContainsFunc(r.since(mark), func(m map[string]any) bool { return m["type"] == "events.gap" }) {
		t.Error("no events.gap before tab.create's answer")
	}
	if len(a.tabs) != 2 || a.tabs[1].Focus.SID != sid2 || a.tab() != first {
		t.Errorf("the desktop's tabs %d, active %v", len(a.tabs), a.tab() == first)
	}
	// Another goes after the first too; moving it last puts it after.
	t3 := r.call("tab.create", map[string]any{"workspace_id": "w", "focus": true})["tab"].(map[string]any)["tab_id"].(string)
	ids := func(res map[string]any) (out []string) {
		for _, x := range res["tabs"].([]any) {
			out = append(out, x.(map[string]any)["tab_id"].(string))
		}
		return
	}
	if got := ids(r.call("tab.list", map[string]any{"workspace_id": "w"})); !slices.Equal(got, []string{t1, t3, t2}) {
		t.Errorf("tab.list %v, want %v", got, []string{t1, t3, t2})
	}
	if got := ids(r.call("tab.move", map[string]any{"tab_id": t3, "insert_index": 3})); !slices.Equal(got, []string{t1, t2, t3}) {
		t.Errorf("tab.move %v", got)
	}
	if tabs, _, _ := r.snapshot(); !slices.Equal(tabs, []string{t1, t2, t3}) {
		t.Errorf("snapshot's tabs %v after the move", tabs)
	}

	// A split of the tab rootshell holds tiles it anew, the new pane
	// focused and sized before the answer.
	r.call("tab.set_geometry", map[string]any{"tab_id": t1, "cols": 100, "rows": 30, "chrome": "none", "claim": true})
	mark = len(r.log)
	r.call("pane.split", map[string]any{"target_pane_id": "p-" + sid1, "direction": "right", "focus": true})
	var layout map[string]any
	var focused []any
	for _, m := range r.since(mark) {
		if l, ok := m["layout"].(map[string]any); ok && m["type"] == "tab.layout" && l["tab_id"] == t1 {
			layout = l
			if len(l["panes"].([]any)) == 2 {
				focused = append(focused, l["focused_pane_id"])
			}
		}
	}
	if layout == nil || len(layout["panes"].([]any)) != 2 {
		t.Fatalf("no tab.layout of two panes before the split's answer: %v", layout)
	}
	_, _, panes = r.snapshot()
	sidNew := panes[t1][1]
	for _, f := range focused {
		if f != "p-"+sidNew {
			t.Errorf("focused %v, want the new pane", focused)
		}
	}
	size := func(sid string) string {
		list, _ := a.client.List()
		for _, in := range list {
			if in.ID == sid {
				return fmt.Sprintf("%dx%d", in.Cols, in.Rows)
			}
		}
		return "gone"
	}
	if size(sid1) != "50x30" || size(sidNew) != "50x30" {
		t.Errorf("tiles %s, %s", size(sid1), size(sidNew))
	}

	// Zoom fills the tab with the pane, and gives the room back.
	mark = len(r.log)
	r.call("pane.zoom", map[string]any{"pane_id": "p-" + sidNew, "mode": "on"})
	l := map[string]any{}
	for _, m := range r.since(mark) {
		if x, ok := m["layout"].(map[string]any); ok && x["tab_id"] == t1 && x["zoomed"] == true {
			l = x
		}
	}
	if ps, _ := l["panes"].([]any); l["focused_pane_id"] != "p-"+sidNew || len(ps) != 1 || size(sidNew) != "100x30" || first.Zoom == nil {
		t.Errorf("zoomed %v, size %s", l, size(sidNew))
	}
	r.call("pane.zoom", map[string]any{"pane_id": "p-" + sidNew, "mode": "off"})
	if first.Zoom != nil || size(sidNew) != "50x30" {
		t.Errorf("unzoomed: %v, %s", first.Zoom, size(sidNew))
	}

	// Closing a pane ends its session, its attach quietly.
	aid := r.call("terminal.attach", map[string]any{"target": sidNew})["attach_id"]
	r.next("its snapshot", func(m map[string]any) bool { return m["type"] == "terminal.snapshot" && m["attach_id"] == aid })
	mark = len(r.log)
	r.call("pane.close", map[string]any{"pane_id": "p-" + sidNew})
	if _, _, panes := r.snapshot(); len(panes[t1]) != 1 || len(first.panes()) != 1 {
		t.Errorf("after closing a pane: %v, the desktop's %d", panes[t1], len(first.panes()))
	}
	if slices.ContainsFunc(r.since(mark), func(m map[string]any) bool { return m["type"] == "terminal.detached" }) {
		t.Error("a pane closed from rootshell detached")
	}

	// Renaming shows in the next snapshot; closing a tab ends it.
	if got := r.call("tab.rename", map[string]any{"tab_id": t2, "label": " phone "})["tab"].(map[string]any)["label"]; got != "phone" {
		t.Errorf("renamed %v", got)
	}
	if _, labels, _ := r.snapshot(); labels[t2] != "phone" || a.tabs[1].Name != "phone" {
		t.Errorf("labels %v", labels)
	}
	r.call("tab.close", map[string]any{"tab_id": t2})
	if tabs, _, _ := r.snapshot(); !slices.Equal(tabs, []string{t1, t3}) || len(a.tabs) != 2 {
		t.Errorf("after closing a tab: %v, the desktop's %d", tabs, len(a.tabs))
	}
	waitFor(t, tt, "the closed tab's session to end", func() bool { return size(sid2) == "gone" })
}

// TestCommandCreateFails checks that a tab or a pane whose session does
// not start leaves the layout as it was, and tells why.
func TestCommandCreateFails(t *testing.T) {
	a, _ := newTestApp(t)
	first := a.tab()
	a.err = "an older error"
	a.client.Close() // no session starts
	res := a.command(rex.LayoutCommand{Op: "tab.create"})
	if res.Code != "create_failed" || res.Error == "" || res.Error == "an older error" || len(a.tabs) != 1 {
		t.Errorf("tab.create: %+v, %d tabs", res, len(a.tabs))
	}
	res = a.command(rex.LayoutCommand{Op: "pane.split", SID: first.Focus.SID})
	if res.Code != "create_failed" || len(first.panes()) != 1 || first.Root.Pane != first.Focus {
		t.Errorf("pane.split: %+v, %d panes", res, len(first.panes()))
	}
}

// TestActionFromClient runs an app action through the command queue, as
// the CLI does, and sees the window change.
func TestClientActionsCannotQuit(t *testing.T) {
	a, _ := newTestApp(t)
	for _, id := range []string{"app.quit", "config.reload", "mode.enter", "screen.clear"} {
		res := a.command(rex.LayoutCommand{Op: "action", Action: id})
		if res.Code != "not_allowed" {
			t.Fatalf("%s: %+v", id, res)
		}
	}
}

func TestActionFromClient(t *testing.T) {
	a, tt := newTestApp(t)
	go a.st.takeCommands(a.client, a.post)
	a.saveNow()
	c, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	type result struct {
		res rex.LayoutCommand
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := c.Action("", "tab.rename", map[string]any{"name": "cli-test"})
		done <- result{res, err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		tt.Frame()
		select {
		case r := <-done:
			if r.err != nil || r.res.Code != "" {
				t.Fatalf("action: %+v %v", r.res, r.err)
			}
			if a.tab().Name != "cli-test" {
				t.Fatalf("the action renamed the tab %q", a.tab().Name)
			}
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the action")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPaneRename(t *testing.T) {
	a, _ := newTestApp(t)
	p := a.tab().Focus
	res := a.command(rex.LayoutCommand{Op: "pane.rename", SID: p.SID, Name: " logs "})
	if res.Code != "" || p.Name != "logs" {
		t.Fatalf("rename: %+v %q", res, p.Name)
	}
	if name, _ := p.label(); name != "logs" {
		t.Fatalf("label %q", name)
	}
	a.saveNow()
	raw, err := a.client.Layout()
	if err != nil {
		t.Fatal(err)
	}
	saved := parseLayout(raw)
	if saved.Sessions[0].Tabs[0].Root.Name != "logs" {
		t.Fatalf("saved %+v", saved.Sessions[0].Tabs[0].Root)
	}
	b := &App{client: a.client}
	if !b.restore(saved.Sessions[0].savedLayout, nil) {
		t.Fatal("nothing restored")
	}
	if b.tab().Focus.Name != "logs" {
		t.Fatalf("restored %q", b.tab().Focus.Name)
	}
}

func TestMovePane(t *testing.T) {
	a, _ := newTestApp(t)
	dir := t.TempDir()
	first := a.tab()
	a.newTab(dir)
	second := a.tab()
	p := first.Focus
	sid, term := p.SID, p.term
	a.movePaneToTab(p, second)
	if len(a.tabs) != 1 || a.tab() != second {
		t.Fatalf("tabs %d, active %v", len(a.tabs), a.tab() == second)
	}
	if p.Tab != second || p.SID != sid || p.term != term {
		t.Fatalf("moved pane %+v", p)
	}
	if len(second.panes()) != 2 || second.Focus != p {
		t.Fatalf("target panes %d, focus %v", len(second.panes()), second.Focus == p)
	}
	if second.Zoom != nil {
		t.Fatal("zoom survived the move")
	}
}

func TestResizeAmounts(t *testing.T) {
	a, _ := newTestApp(t)
	a.split(false)
	tb := a.tab()
	before := tb.Root.Ratio
	a.resizeFocusedBy(1, 0, 10)
	if got := tb.Root.Ratio; got < before+0.09 || got > before+0.11 {
		t.Fatalf("resize amount: %v -> %v", before, got)
	}
	a.setSplitRatio(0.7)
	if tb.Root.Ratio != 0.7 {
		t.Fatalf("ratio %v", tb.Root.Ratio)
	}
	a.setSplitRatio(2)
	if tb.Root.Ratio != 0.9 {
		t.Fatalf("ratio clamp %v", tb.Root.Ratio)
	}
}

func TestMovePaneWithinTab(t *testing.T) {
	a, _ := newTestApp(t)
	first := a.tab().Focus
	a.split(false)
	second := a.tab().Focus
	sid, term := first.SID, first.term
	a.movePaneToPane(first, second)
	tb := a.tab()
	if tb.Root.A == nil || tb.Root.B == nil || len(tb.panes()) != 2 || tb.Focus != first {
		t.Fatalf("after move: focus %v panes %d", tb.Focus == first, len(tb.panes()))
	}
	if first.SID != sid || first.term != term {
		t.Fatal("the moved pane's session changed")
	}
}

func TestMovePaneFromActiveTab(t *testing.T) {
	a, _ := newTestApp(t)
	dir := t.TempDir()
	first := a.tab()
	a.newTab(dir)
	second := a.tab()
	a.newTab(dir)
	third := a.tab()
	a.selectTab(slices.Index(a.tabs, first))
	p := first.Focus
	a.movePaneToTab(p, third)
	if a.tab() != third || a.focusReq != p {
		t.Fatalf("active %v focus %v", a.tab() == third, a.focusReq == p)
	}
	if len(a.tabs) != 2 || slices.Contains(a.tabs, first) || !slices.Contains(a.tabs, second) || !slices.Contains(a.tabs, third) {
		t.Fatalf("tabs %d", len(a.tabs))
	}
}

func TestMovePaneKeepsActiveTabFocus(t *testing.T) {
	a, _ := newTestApp(t)
	a.split(false)
	first := a.tab()
	dir := t.TempDir()
	a.newTab(dir)
	second := a.tab()
	a.selectTab(slices.Index(a.tabs, first))
	p := first.Focus
	a.movePaneToTab(p, second)
	if a.tab() != first || a.focusReq != first.Focus {
		t.Fatalf("active %v focus %v", a.tab() == first, a.focusReq == first.Focus)
	}
	if p.Tab != second {
		t.Fatal("pane did not move")
	}
}

func TestPaneName(t *testing.T) {
	if got := paneName("  logs\x01  "); got != "logs" {
		t.Fatalf("sanitized %q", got)
	}
	if got := paneName(strings.Repeat("界", 70)); len([]rune(got)) != 64 || !utf8.ValidString(got) {
		t.Fatalf("capped runes %d", len([]rune(got)))
	}
}
