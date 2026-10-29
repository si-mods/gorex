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

// startPhone runs the app's command loop and a bridge, and returns
// rootshell's end of it, opened.
func startPhone(t *testing.T, a *App, tt *ui.Tester) *phone {
	t.Helper()
	go a.st.takeCommands(a.client, a.post)
	a.saveNow()
	c, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	t.Cleanup(func() { inW.Close() })
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
	r.next("control.open", func(m map[string]any) bool { return m["id"] == "control.open" })
	return r
}

// TestRootshellLayout changes the window's layout from rootshell, through
// the bridge and the app's own code: new tabs, splits, zoom, closing,
// renaming and moving, each seen by rootshell before its answer.
func TestRootshellLayout(t *testing.T) {
	a, tt := newTestApp(t)
	first := a.tab()
	r := startPhone(t, a, tt)
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
	if !b.restore(saved.Sessions[0].Layout, nil) {
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

func TestMovePaneToWindow(t *testing.T) {
	a, _ := newTestApp(t)
	b := a.st.openWindow(a.st.addWorkspace(), false)
	a.split(false)
	p := a.tab().Focus
	p.Name = "logs"
	sid, src := p.SID, p.Tab
	a.movePaneToWindow(p, b)
	if !p.closed || len(src.panes()) != 1 {
		t.Fatalf("source: closed %v panes %d", p.closed, len(src.panes()))
	}
	var moved *Pane
	for _, q := range b.tab().panes() {
		if q.SID == sid {
			moved = q
		}
	}
	if moved == nil || moved.Name != "logs" || b.focusReq != moved || moved.term == nil {
		t.Fatalf("target: moved %v name %q focus %v", moved != nil, moved.Name, b.focusReq == moved)
	}
	infos, _ := a.client.List()
	found := false
	for _, in := range infos {
		if in.ID == sid {
			found = true
		}
	}
	if !found {
		t.Fatal("the session ended")
	}
}

func TestMoveOnlyPaneToWindow(t *testing.T) {
	a, _ := newTestApp(t)
	b := a.st.openWindow(a.st.addWorkspace(), false)
	p := a.tab().Focus
	sid := p.SID
	a.movePaneToWindow(p, b)
	if len(a.tabs) != 0 {
		t.Fatalf("source kept %d tabs", len(a.tabs))
	}
	found := false
	for _, q := range b.tab().panes() {
		if q.SID == sid && q.term != nil {
			found = true
		}
	}
	if !found {
		t.Fatal("the only pane did not move")
	}
}

// TestRootshellPaneCommands resizes, swaps, renames and moves panes from
// rootshell, through the bridge and the app, as herdr's pane methods do.
func TestRootshellPaneCommands(t *testing.T) {
	a, tt := newTestApp(t)
	r := startPhone(t, a, tt)
	tabs, _, panes := r.snapshot()
	if len(tabs) != 1 || len(panes[tabs[0]]) != 1 {
		t.Fatalf("tabs %v, panes %v", tabs, panes)
	}
	t1, sid1 := tabs[0], panes[tabs[0]][0]
	r.call("pane.focus", map[string]any{"pane_id": "p-" + sid1})
	r.call("tab.set_geometry", map[string]any{"tab_id": t1, "cols": 100, "rows": 30, "chrome": "none", "claim": true})
	r.call("pane.split", map[string]any{"target_pane_id": "p-" + sid1, "direction": "right", "focus": true})
	_, _, panes = r.snapshot()
	if len(panes[t1]) != 2 {
		t.Fatalf("panes %v after a split", panes)
	}
	sid2 := panes[t1][1]
	tab := a.tab()

	// A divider drag moves the nearest split's ratio.
	res := r.call("pane.resize", map[string]any{"pane_id": "p-" + sid2, "direction": "left", "amount": 0.1})
	if resize := res["resize"].(map[string]any); resize["changed"] != true || resize["layout"] == nil {
		t.Fatalf("pane.resize %v", res)
	}
	if tab.Root == nil || tab.Root.Ratio < 0.39 || tab.Root.Ratio > 0.41 {
		t.Fatalf("ratio %v", tab.Root)
	}

	// A swap puts the panes in each other's places, and focuses the source.
	res = r.call("pane.swap", map[string]any{"source_pane_id": "p-" + sid1, "target_pane_id": "p-" + sid2})
	if swap := res["swap"].(map[string]any); swap["changed"] != true {
		t.Fatalf("pane.swap %v", res)
	}
	if ps := tab.panes(); ps[0].SID != sid2 || ps[1].SID != sid1 || tab.Focus.SID != sid1 {
		t.Fatalf("panes %v, focus %s", []string{ps[0].SID, ps[1].SID}, tab.Focus.SID)
	}

	// A label rootshell gave shows in the answer and the snapshot.
	res = r.call("pane.rename", map[string]any{"pane_id": "p-" + sid1, "label": " logs "})
	if pane := res["pane"].(map[string]any); pane["label"] != "logs" {
		t.Fatalf("pane.rename %v", res)
	}
	// The app's sanitizing is what the wait compares against.
	res = r.call("pane.rename", map[string]any{"pane_id": "p-" + sid1, "label": " lo\x01gs "})
	if pane := res["pane"].(map[string]any); pane["label"] != "logs" {
		t.Fatalf("sanitized pane.rename %v", res)
	}
	snap := r.call("session.snapshot", map[string]any{})["snapshot"].(map[string]any)
	found := false
	for _, x := range snap["panes"].([]any) {
		if p := x.(map[string]any); p["terminal_id"] == sid1 && p["label"] == "logs" {
			found = true
		}
	}
	if !found {
		t.Fatalf("snapshot without the label: %v", snap["panes"])
	}

	// A second tab to move panes into.
	t2 := r.call("tab.create", map[string]any{"workspace_id": "w", "focus": true})["tab"].(map[string]any)["tab_id"].(string)
	_, _, panes = r.snapshot()
	sid3 := panes[t2][0]

	// A tab of one pane has no divider to move.
	res = r.call("pane.resize", map[string]any{"pane_id": "p-" + sid3, "direction": "left", "amount": 0.1})
	if resize := res["resize"].(map[string]any); resize["changed"] != false || resize["reason"] != "unchanged" {
		t.Fatalf("resize of one pane %v", res)
	}

	// A swap across tabs changes nothing.
	res = r.call("pane.swap", map[string]any{"source_pane_id": "p-" + sid1, "target_pane_id": "p-" + sid3})
	if swap := res["swap"].(map[string]any); swap["changed"] != false || swap["reason"] != "cross_tab" {
		t.Fatalf("cross-tab swap %v", res)
	}

	// A move into another tab keeps the session.
	res = r.call("pane.move", map[string]any{"pane_id": "p-" + sid2, "destination": map[string]any{"type": "tab", "tab_id": t2, "split": "down"}})
	move := res["move_result"].(map[string]any)
	if move["changed"] != true || move["previous_tab_id"] != t1 || move["pane"].(map[string]any)["tab_id"] != t2 {
		t.Fatalf("pane.move %v", res)
	}
	_, _, panes = r.snapshot()
	if !slices.Contains(panes[t2], sid2) || slices.Contains(panes[t1], sid2) {
		t.Fatalf("panes after the move: %v", panes)
	}
	list, _ := a.client.List()
	alive := false
	for _, in := range list {
		if in.ID == sid2 {
			alive = true
		}
	}
	if !alive {
		t.Fatal("the moved pane's session ended")
	}

	// A move into its own tab changes nothing.
	res = r.call("pane.move", map[string]any{"pane_id": "p-" + sid2, "destination": map[string]any{"type": "tab", "tab_id": t2}})
	if move := res["move_result"].(map[string]any); move["changed"] != false || move["reason"] != "same_tab" {
		t.Fatalf("same-tab move %v", res)
	}

	// A move into a new tab makes one, keeping the pane.
	res = r.call("pane.move", map[string]any{"pane_id": "p-" + sid1, "destination": map[string]any{"type": "new_tab", "label": "moved"}})
	move = res["move_result"].(map[string]any)
	created, _ := move["created_tab"].(map[string]any)
	if move["changed"] != true || created == nil || created["label"] != "moved" {
		t.Fatalf("new-tab move %v", res)
	}
	if _, _, panes := r.snapshot(); !slices.Contains(panes[created["tab_id"].(string)], sid1) {
		t.Fatalf("panes after the new-tab move: %v", panes)
	}
}

// TestCommandPaneMove moves a pane into another tab and into a new one.
func TestCommandPaneMove(t *testing.T) {
	a, _ := newTestApp(t)
	a.split(false)
	first := a.tab()
	p := first.Focus
	a.newTab(t.TempDir())
	target := a.tab().Focus
	res := a.command(rex.LayoutCommand{Op: "pane.move", SID: p.SID, Target: target.SID, Direction: "down", Ratio: 0.4})
	if res.Code != "" || !res.Changed || p.Tab != target.Tab || len(first.panes()) != 1 {
		t.Fatalf("pane.move: %+v, pane tab %v, source panes %d", res, p.Tab == target.Tab, len(first.panes()))
	}
	if target.Node == nil || target.Node.Parent == nil || target.Node.Parent.Vertical != true || target.Node.Parent.Ratio < 0.39 {
		t.Fatalf("moved split %+v", target.Node.Parent)
	}
	p = target.Tab.Focus
	a.newTab(t.TempDir())
	src := p.Tab
	res = a.command(rex.LayoutCommand{Op: "pane.move", SID: p.SID, NewTab: true, Name: "moved"})
	if res.Code != "" || !res.Changed || p.Tab == src || p.Tab.Name != "moved" {
		t.Fatalf("new-tab move: %+v, tab %v %q", res, p.Tab != src, p.Tab.Name)
	}
}

// TestMovePaneToNewTabKeepsActive leaves the window on its tab when a
// pane of another tab goes to a tab of its own.
func TestMovePaneToNewTabKeepsActive(t *testing.T) {
	a, _ := newTestApp(t)
	first := a.tab()
	a.newTab(t.TempDir())
	second := a.tab()
	a.newTab(t.TempDir())
	a.selectTab(slices.Index(a.tabs, second))
	p := first.Focus
	src := p.Tab
	if !a.movePaneToNewTab(p, "moved") {
		t.Fatal("no new tab")
	}
	if p.Tab == src || !slices.Contains(a.tabs, p.Tab) || a.tab() != second {
		t.Fatalf("tab %v, active %v", p.Tab == src, a.tab() == second)
	}
	if slices.Contains(a.tabs, first) {
		t.Fatal("the emptied tab stayed")
	}
}

// TestCommandPaneResizeNested moves the divider a nested pane's edge
// belongs to, not its nearest same-axis split.
func TestCommandPaneResizeNested(t *testing.T) {
	a, _ := newTestApp(t)
	a.split(false)
	b := a.tab().Focus
	a.split(false)
	c := a.tab().Focus
	tab := a.tab()
	root := tab.Root
	s := root.B
	if root.Vertical || s == nil || s.Vertical || s.A != b.Node || s.B != c.Node {
		t.Fatalf("tree %+v", root)
	}
	ratio := func(n *Node) float32 { return n.Ratio }
	near := func(got, want float32) bool { return got > want-0.001 && got < want+0.001 }

	// B's left edge is the root divider: the root moves, not B's parent.
	res := a.command(rex.LayoutCommand{Op: "pane.resize", SID: b.SID, Direction: "left", Amount: 0.1})
	if res.Code != "" || !res.Changed || !near(ratio(root), 0.4) || !near(ratio(s), 0.5) {
		t.Fatalf("left of B: %+v, root %v, split %v", res, ratio(root), ratio(s))
	}
	// B's right edge is its parent split.
	res = a.command(rex.LayoutCommand{Op: "pane.resize", SID: b.SID, Direction: "right", Amount: 0.1})
	if res.Code != "" || !res.Changed || !near(ratio(root), 0.4) || !near(ratio(s), 0.6) {
		t.Fatalf("right of B: %+v, root %v, split %v", res, ratio(root), ratio(s))
	}
	// C's left edge is its parent split.
	res = a.command(rex.LayoutCommand{Op: "pane.resize", SID: c.SID, Direction: "left", Amount: 0.1})
	if res.Code != "" || !res.Changed || !near(ratio(root), 0.4) || !near(ratio(s), 0.5) {
		t.Fatalf("left of C: %+v, root %v, split %v", res, ratio(root), ratio(s))
	}
	// C's outer right edge moves its nearest split the other way, as herdr.
	res = a.command(rex.LayoutCommand{Op: "pane.resize", SID: c.SID, Direction: "right", Amount: 0.1})
	if res.Code != "" || !res.Changed || !near(ratio(root), 0.4) || !near(ratio(s), 0.6) {
		t.Fatalf("outer right of C: %+v, root %v, split %v", res, ratio(root), ratio(s))
	}
}
