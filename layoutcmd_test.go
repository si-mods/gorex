package main

import (
	"encoding/json"
	"fmt"
	"gorex/locales"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// TestCommandLayout changes the window's layout from a client, through
// the server and the app's own code: new tabs, splits, zoom, closing,
// renaming and moving.
func TestCommandLayout(t *testing.T) {
	a, _ := newTestApp(t)
	first := a.tab()
	sid1 := first.Focus.SID
	alive := func(sid string) bool {
		list, _ := a.client.List()
		return slices.ContainsFunc(list, func(in rex.SessionInfo) bool { return in.ID == sid })
	}
	sids := func() (out []string) {
		for _, t := range a.tabs {
			out = append(out, t.panes()[0].SID)
		}
		return
	}

	// A new tab goes after the tab named, and the window's stays active.
	res := clientCommand(a, rex.LayoutCommand{Op: "tab.create", After: sid1})
	sid2 := res.New
	if res.Code != "" || sid2 == "" {
		t.Fatalf("tab.create %+v", res)
	}
	if len(a.tabs) != 2 || a.tabs[1].Focus.SID != sid2 || a.tab() != first {
		t.Errorf("the window's tabs %d, active %v", len(a.tabs), a.tab() == first)
	}
	// Another goes after the first too; moving it last puts it after.
	sid3 := clientCommand(a, rex.LayoutCommand{Op: "tab.create", After: sid1}).New
	if got := sids(); !slices.Equal(got, []string{sid1, sid3, sid2}) {
		t.Errorf("tabs %v, want %v", got, []string{sid1, sid3, sid2})
	}
	if res := clientCommand(a, rex.LayoutCommand{Op: "tab.move", SID: sid3, Index: 3}); res.Code != "" {
		t.Fatalf("tab.move %+v", res)
	}
	if got := sids(); !slices.Equal(got, []string{sid1, sid2, sid3}) {
		t.Errorf("tabs %v after the move", got)
	}

	// A split's new pane has the focus in its tab.
	res = clientCommand(a, rex.LayoutCommand{Op: "pane.split", SID: sid1})
	sidNew := res.New
	if res.Code != "" || sidNew == "" || len(first.panes()) != 2 || first.Focus.SID != sidNew {
		t.Fatalf("pane.split %+v, panes %d", res, len(first.panes()))
	}

	// Zoom fills the tab with the pane, and gives the room back.
	res = clientCommand(a, rex.LayoutCommand{Op: "pane.zoom", SID: sidNew, Mode: "on"})
	if res.Code != "" || !res.Zoomed || first.Zoom == nil || first.Zoom.SID != sidNew {
		t.Errorf("zoomed %+v", res)
	}
	clientCommand(a, rex.LayoutCommand{Op: "pane.zoom", SID: sidNew, Mode: "off"})
	if first.Zoom != nil {
		t.Errorf("unzoomed: %v", first.Zoom)
	}

	// Closing a pane ends its session.
	if res := clientCommand(a, rex.LayoutCommand{Op: "pane.close", SID: sidNew}); res.Code != "" || len(first.panes()) != 1 {
		t.Errorf("pane.close %+v, panes %d", res, len(first.panes()))
	}
	waitForThat(t, func() bool { return !alive(sidNew) }, "the closed pane's session to end")

	// Renaming shows in the window; closing a tab ends it.
	if res := clientCommand(a, rex.LayoutCommand{Op: "tab.rename", SID: sid2, Name: " phone "}); res.Code != "" || a.tabs[1].Name != "phone" {
		t.Errorf("tab.rename %+v, name %q", res, a.tabs[1].Name)
	}
	if res := clientCommand(a, rex.LayoutCommand{Op: "tab.close", SID: sid2}); res.Code != "" {
		t.Fatalf("tab.close %+v", res)
	}
	if got := sids(); !slices.Equal(got, []string{sid1, sid3}) {
		t.Errorf("tabs %v after closing a tab", got)
	}
	waitForThat(t, func() bool { return !alive(sid2) }, "the closed tab's session to end")
}

// TestCommandCreateFails checks that a tab or a pane whose session does
// not start leaves the layout as it was, and tells why.
func TestCommandCreateFails(t *testing.T) {
	a, _ := newTestApp(t)
	first := a.tab()
	a.err = "an older error"
	a.client.Close() // no session starts
	if a.newTab(os.TempDir()) != nil || a.err == "" || a.err == "an older error" || len(a.tabs) != 1 {
		t.Errorf("a new tab: %q, %d tabs", a.err, len(a.tabs))
	}
	a.err = ""
	a.split(false)
	if a.err == "" || len(first.panes()) != 1 || first.Root.Pane != first.Focus {
		t.Errorf("a split: %q, %d panes", a.err, len(first.panes()))
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
	followApp(t, a)
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
	res := clientCommand(a, rex.LayoutCommand{Op: "pane.rename", SID: p.SID, Name: " logs "})
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
	saved := layout.Parse(raw)
	if saved.Sessions[0].Tabs[0].Root.Name != "logs" {
		t.Fatalf("saved %+v", saved.Sessions[0].Tabs[0].Root)
	}
	b := &App{client: a.client}
	if !b.reconcile(saved.Sessions[0].Layout) {
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

func TestMergeTab(t *testing.T) {
	a, src, dst := newMergeTestTabs()
	src.Name, dst.Name = "source", "target"
	src.Root.Vertical = true
	src.Root.Ratio = 0.63
	srcRoot := src.Root
	sourcePanes := slices.Clone(src.panes())
	sourceNodes := make([]*Node, len(sourcePanes))
	sids := make([]string, len(sourcePanes))
	terms := make([]any, len(sourcePanes))
	for i, p := range sourcePanes {
		sourceNodes[i], sids[i], terms[i] = p.Node, p.SID, p.term
	}
	focused := src.Focus
	dst.Zoom = dst.Focus
	dstRoot := dst.Root
	targetPane := dst.Focus
	targetSID, targetTerm := targetPane.SID, targetPane.term

	a.mergeTab(src, dst)

	if len(a.tabs) != 1 || a.tabs[0] != dst || a.tab() != dst {
		t.Fatalf("tabs %d, active target %v", len(a.tabs), a.tab() == dst)
	}
	if dst.Root == nil || dst.Root.A != dstRoot || dst.Root.B != srcRoot || dst.Root.Vertical || dst.Root.Ratio != 0.5 {
		t.Fatalf("merged root %+v", dst.Root)
	}
	if dst.Root.ID == srcRoot.ID || dst.Root.ID == dstRoot.ID || dstRoot.Parent != dst.Root || srcRoot.Parent != dst.Root {
		t.Fatal("root split ID or parent pointers are wrong")
	}
	if len(dst.panes()) != 3 || dst.Focus != focused || a.focusReq != focused || dst.Zoom != nil {
		t.Fatalf("panes %d, focus %v, focus request %v, zoom %v", len(dst.panes()), dst.Focus == focused, a.focusReq == focused, dst.Zoom)
	}
	if dst.Name != "target" || slices.Contains(a.tabs, src) {
		t.Fatalf("destination name %q, source still present %v", dst.Name, slices.Contains(a.tabs, src))
	}
	if dstRoot.Pane != targetPane || targetPane.Tab != dst || targetPane.SID != targetSID || targetPane.term != targetTerm {
		t.Fatal("destination pane changed during merge")
	}
	for i, p := range sourcePanes {
		if p.Tab != dst || p.Node != sourceNodes[i] || p.SID != sids[i] || p.term != terms[i] || p.closed {
			t.Fatalf("source pane %d changed during merge", i)
		}
		if !slices.Contains(dst.recent, p) {
			t.Fatalf("source pane %d missing from recent focus history", i)
		}
	}
	if srcRoot.Ratio != 0.63 || srcRoot.A.Parent != srcRoot || srcRoot.B.Parent != srcRoot {
		t.Fatal("source split tree changed during merge")
	}
}

func TestMergeTabSelf(t *testing.T) {
	a, tab, _ := newMergeTestTabs()
	roots, focus := tab.Root, tab.Focus
	tabs, activeTab := slices.Clone(a.tabs), a.tab()
	active := a.active
	a.mergeTab(tab, tab)
	if !slices.Equal(a.tabs, tabs) || a.tab() != activeTab || a.active != active || tab.Root != roots || tab.Focus != focus {
		t.Fatal("merging a tab into itself changed it")
	}
}

func TestMergeTabNoopCases(t *testing.T) {
	a, _, dst := newMergeTestTabs()
	empty := &Tab{ID: 20}
	a.tabs = append(a.tabs, empty)
	dstRoot := dst.Root
	emptyRoot := empty.Root
	active := a.active
	a.mergeTab(nil, dst)
	a.mergeTab(dst, nil)
	a.mergeTab(empty, dst)
	if len(a.tabs) != 3 || dst.Root != dstRoot || empty.Root != emptyRoot || a.active != active {
		t.Fatal("a nil or empty source changed the tabs")
	}
}

func TestMergeTabDifferentHosts(t *testing.T) {
	a, src, dst := newMergeTestTabs()
	for _, p := range src.panes() {
		p.host = &hostConn{ID: "source-host"}
	}
	dst.Focus.host = &hostConn{ID: "target-host"}
	srcRoot, dstRoot, active := src.Root, dst.Root, a.active
	a.mergeTab(src, dst)
	if len(a.tabs) != 2 || src.Root != srcRoot || dst.Root != dstRoot || a.active != active {
		t.Fatal("tabs on different hosts were merged")
	}
}

func TestMergeTabIntoEmptyTarget(t *testing.T) {
	a, src, dst := newMergeTestTabs()
	sourceRoot := src.Root
	dst.Root, dst.Focus = nil, nil
	a.mergeTab(src, dst)
	if len(a.tabs) != 1 || a.tab() != dst || dst.Root != sourceRoot || sourceRoot.Parent != nil {
		t.Fatal("source tree was not adopted by the empty target")
	}
	for _, p := range dst.panes() {
		if p.Tab != dst {
			t.Fatal("adopted pane still points at the source tab")
		}
	}
}

func newMergeTestTabs() (*App, *Tab, *Tab) {
	app := &App{active: 1, nextID: 7}
	src := &Tab{ID: 1}
	sourcePanes := make([]*Pane, 2)
	sourceLeaves := make([]*Node, 2)
	for i := range sourcePanes {
		sourcePanes[i] = &Pane{ID: i + 2, SID: fmt.Sprintf("source-%d", i+1), term: new(terminal.Terminal), Tab: src}
		sourceLeaves[i] = &Node{ID: i + 4, Pane: sourcePanes[i]}
		sourcePanes[i].Node = sourceLeaves[i]
	}
	src.Root = &Node{ID: 6, A: sourceLeaves[0], B: sourceLeaves[1], Ratio: 0.5}
	sourceLeaves[0].Parent, sourceLeaves[1].Parent = src.Root, src.Root
	src.Focus, src.recent = sourcePanes[1], slices.Clone(sourcePanes)

	dst := &Tab{ID: 7}
	targetPane := &Pane{ID: 8, SID: "target", term: new(terminal.Terminal), Tab: dst}
	dst.Root = &Node{ID: 9, Pane: targetPane}
	targetPane.Node, dst.Focus, dst.recent = dst.Root, targetPane, []*Pane{targetPane}
	app.tabs = []*Tab{src, dst}
	app.nextID = 9
	return app, src, dst
}

func TestTabDropZone(t *testing.T) {
	tests := []struct {
		pos, size float32
		merge     bool
		before    bool
	}{
		{0, 100, false, true},
		{24.9, 100, false, true},
		{25, 100, true, true},
		{49, 100, true, true},
		{50, 100, true, false},
		{74.9, 100, true, false},
		{75, 100, false, false},
		{100, 100, false, false},
	}
	for _, tt := range tests {
		merge, before := tabDropZone(tt.pos, tt.size)
		if merge != tt.merge || before != tt.before {
			t.Errorf("tabDropZone(%v, %v) = (%v, %v), want (%v, %v)", tt.pos, tt.size, merge, before, tt.merge, tt.before)
		}
	}
}

func TestTabEventsDropMergesHeadless(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	for _, place := range []string{"left", ""} {
		name := place
		if name == "" {
			name = "top"
		}
		t.Run(name, func(t *testing.T) {
			prefs = settings{TabPlacement: place}
			a, src, dst := newMergeTestTabs()
			src.Name, dst.Name = "source", "target"
			sourceRoot := src.Root
			view := func(c *ui.Context) {
				k := colorsOf(c)
				drawTabs := func() {
					for i, tab := range a.tabs {
						e := ui.Box(c.Key(tab.ID)).Size(120, 32).Label(tab.Name)
						a.tabEvents(c, e, k, i, tab)
					}
				}
				if place == "" {
					ui.Row(c).Gap(0).Children(drawTabs)
				} else {
					ui.Column(c).Gap(0).Children(drawTabs)
				}
			}
			tt := ui.NewTester(view, 300, 100)
			source, sourceOK := tt.Find("source")
			target, targetOK := tt.Find("target")
			if !sourceOK || !targetOK {
				t.Fatalf("tabs not laid out: %q", tt.Texts())
			}
			sx, sy := source.X+source.W/2, source.Y+source.H/2
			tx, ty := target.X+target.W/2, target.Y+target.H/2
			tt.Press(sx, sy)
			tt.Frame()
			tt.Move(sx+5, sy)
			tt.Frame()
			tt.Move(tx, ty)
			tt.Frame()
			tt.Release(tx, ty)
			tt.Frame()
			if len(a.tabs) != 1 || a.tab() != dst || dst.Root.B != sourceRoot || len(dst.panes()) != 3 {
				t.Fatalf("merge failed for placement %q: tabs %d, active target %v, panes %d", place, len(a.tabs), a.tab() == dst, len(dst.panes()))
			}
		})
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
	if got := layout.PaneName("  logs\x01  "); got != "logs" {
		t.Fatalf("sanitized %q", got)
	}
	if got := layout.PaneName(strings.Repeat("界", 70)); len([]rune(got)) != 64 || !utf8.ValidString(got) {
		t.Fatalf("capped runes %d", len([]rune(got)))
	}
}

func TestMovePaneToWindow(t *testing.T) {
	a, _ := newTestApp(t)
	b := a.st.openWindow(a.st.addWorkspace())
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
	b := a.st.openWindow(a.st.addWorkspace())
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

// TestCommandPanes resizes, swaps, renames and moves panes from a client,
// through the server and the app.
func TestCommandPanes(t *testing.T) {
	a, _ := newTestApp(t)
	tab := a.tab()
	sid1 := tab.Focus.SID
	sid2 := clientCommand(a, rex.LayoutCommand{Op: "pane.split", SID: sid1}).New
	if len(tab.panes()) != 2 {
		t.Fatalf("panes %d after a split", len(tab.panes()))
	}

	// A divider drag moves the nearest split's ratio.
	res := clientCommand(a, rex.LayoutCommand{Op: "pane.resize", SID: sid2, Direction: "left", Amount: 0.1})
	if res.Code != "" || !res.Changed {
		t.Fatalf("pane.resize %+v", res)
	}
	if tab.Root == nil || tab.Root.Ratio < 0.39 || tab.Root.Ratio > 0.41 {
		t.Fatalf("ratio %v", tab.Root)
	}

	// A swap puts the panes in each other's places, and focuses the source.
	res = clientCommand(a, rex.LayoutCommand{Op: "pane.swap", SID: sid1, Target: sid2})
	if res.Code != "" || !res.Changed {
		t.Fatalf("pane.swap %+v", res)
	}
	if ps := tab.panes(); ps[0].SID != sid2 || ps[1].SID != sid1 || tab.Focus.SID != sid1 {
		t.Fatalf("panes %v, focus %s", []string{ps[0].SID, ps[1].SID}, tab.Focus.SID)
	}

	// The name a client gives is the app's sanitized one.
	res = clientCommand(a, rex.LayoutCommand{Op: "pane.rename", SID: sid1, Name: " lo\x01gs "})
	if res.Code != "" || res.Name != "logs" || a.localPane(sid1).Name != "logs" {
		t.Fatalf("sanitized pane.rename %+v", res)
	}

	// A second tab to move panes into.
	sid3 := clientCommand(a, rex.LayoutCommand{Op: "tab.create"}).New

	// A tab of one pane has no divider to move.
	res = clientCommand(a, rex.LayoutCommand{Op: "pane.resize", SID: sid3, Direction: "left", Amount: 0.1})
	if res.Changed || res.Reason != "unchanged" {
		t.Fatalf("resize of one pane %+v", res)
	}

	// A swap across tabs changes nothing.
	res = clientCommand(a, rex.LayoutCommand{Op: "pane.swap", SID: sid1, Target: sid3})
	if res.Changed || res.Reason != "cross_tab" {
		t.Fatalf("cross-tab swap %+v", res)
	}

	// A move into another tab keeps the session.
	res = clientCommand(a, rex.LayoutCommand{Op: "pane.move", SID: sid2, Target: sid3, Direction: "down"})
	if res.Code != "" || !res.Changed {
		t.Fatalf("pane.move %+v", res)
	}
	if p, q := a.localPane(sid2), a.localPane(sid3); p == nil || q == nil || p.Tab != q.Tab || p.Tab == tab {
		t.Fatal("the pane did not move into the other tab")
	}
	list, _ := a.client.List()
	if !slices.ContainsFunc(list, func(in rex.SessionInfo) bool { return in.ID == sid2 }) {
		t.Fatal("the moved pane's session ended")
	}

	// A move into its own tab changes nothing.
	res = clientCommand(a, rex.LayoutCommand{Op: "pane.move", SID: sid2, Target: sid3})
	if res.Changed || res.Reason != "same_tab" {
		t.Fatalf("same-tab move %+v", res)
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
	res := clientCommand(a, rex.LayoutCommand{Op: "pane.move", SID: p.SID, Target: target.SID, Direction: "down", Ratio: 0.4})
	if res.Code != "" || !res.Changed || p.Tab != target.Tab || len(first.panes()) != 1 {
		t.Fatalf("pane.move: %+v, pane tab %v, source panes %d", res, p.Tab == target.Tab, len(first.panes()))
	}
	if target.Node == nil || target.Node.Parent == nil || target.Node.Parent.Vertical != true || target.Node.Parent.Ratio < 0.39 {
		t.Fatalf("moved split %+v", target.Node.Parent)
	}
	p = target.Tab.Focus
	a.newTab(t.TempDir())
	src := p.Tab
	res = clientCommand(a, rex.LayoutCommand{Op: "pane.move", SID: p.SID, NewTab: true, Name: "moved"})
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

// TestCommandPaneToNewTab moves the focused pane into a tab of its own,
// after its source tab, keeping its session.
func TestCommandPaneToNewTab(t *testing.T) {
	a, _ := newTestApp(t)
	a.split(false)
	src := a.tab()
	p := src.Focus
	sid, term := p.SID, p.term
	at := slices.Index(a.tabs, src)

	cmdPaneToNewTab.Run(a)

	if len(a.tabs) != 2 || at < 0 {
		t.Fatalf("tabs %d", len(a.tabs))
	}
	moved := a.tabs[at+1]
	if moved == src || !slices.Contains(a.tabs, src) {
		t.Fatal("the new tab is not after the source tab")
	}
	if ps := moved.panes(); len(ps) != 1 || ps[0] != p {
		t.Fatalf("new tab panes %d", len(ps))
	}
	if len(src.panes()) != 1 {
		t.Fatalf("source panes %d", len(src.panes()))
	}
	if p.Tab != moved || p.SID != sid || p.term != term {
		t.Fatal("the moved pane's tab or session changed")
	}
}

// TestCommandPaneToNewTabSinglePane leaves a tab alone when its only pane
// is the focused one.
func TestCommandPaneToNewTabSinglePane(t *testing.T) {
	a, _ := newTestApp(t)
	p := a.tab().Focus

	cmdPaneToNewTab.Run(a)

	if len(a.tabs) != 1 || a.tab().Focus != p || p.Tab != a.tab() {
		t.Fatalf("tabs %d, focus %v", len(a.tabs), a.tab().Focus == p)
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
	res := clientCommand(a, rex.LayoutCommand{Op: "pane.resize", SID: b.SID, Direction: "left", Amount: 0.1})
	if res.Code != "" || !res.Changed || !near(ratio(root), 0.4) || !near(ratio(s), 0.5) {
		t.Fatalf("left of B: %+v, root %v, split %v", res, ratio(root), ratio(s))
	}
	// B's right edge is its parent split.
	res = clientCommand(a, rex.LayoutCommand{Op: "pane.resize", SID: b.SID, Direction: "right", Amount: 0.1})
	if res.Code != "" || !res.Changed || !near(ratio(root), 0.4) || !near(ratio(s), 0.6) {
		t.Fatalf("right of B: %+v, root %v, split %v", res, ratio(root), ratio(s))
	}
	// C's left edge is its parent split.
	res = clientCommand(a, rex.LayoutCommand{Op: "pane.resize", SID: c.SID, Direction: "left", Amount: 0.1})
	if res.Code != "" || !res.Changed || !near(ratio(root), 0.4) || !near(ratio(s), 0.5) {
		t.Fatalf("left of C: %+v, root %v, split %v", res, ratio(root), ratio(s))
	}
	// C's outer right edge moves its nearest split the other way.
	res = clientCommand(a, rex.LayoutCommand{Op: "pane.resize", SID: c.SID, Direction: "right", Amount: 0.1})
	if res.Code != "" || !res.Changed || !near(ratio(root), 0.4) || !near(ratio(s), 0.6) {
		t.Fatalf("outer right of C: %+v, root %v, split %v", res, ratio(root), ratio(s))
	}
}

func TestPaneFocusCommand(t *testing.T) {
	a, _ := newTestApp(t)
	first := a.tab().Focus
	a.split(false)
	second := a.tab().Focus
	res := clientCommand(a, rex.LayoutCommand{Op: "pane.focus", SID: first.SID})
	if res.Error != "" || a.tab().Focus != first || a.focusReq != first {
		t.Fatalf("focus: %+v focus %v", res, a.tab().Focus == first)
	}
	if second == first {
		t.Fatal("same pane")
	}
}

// TestCommandSpaces makes and names a space from a client, opens a tab in
// it with the command the client names, and closes it: what a Rex client
// asks for when it creates a session.
func TestCommandSpaces(t *testing.T) {
	a, _ := newTestApp(t)
	orig, origSID := a.ws.ID, a.tab().Focus.SID
	res := clientCommand(a, rex.LayoutCommand{Op: "space.create", Name: "work"})
	if res.Code != "" || res.Space == "" {
		t.Fatalf("space.create: %+v", res)
	}
	space := res.Space
	w := a.st.byID(space)
	if w == nil || w.Name != "work" || w.app != nil {
		t.Fatalf("the new space: %+v", w)
	}
	// The program outlives the checks: the server takes the tab of one
	// that ended out of the layout at once.
	res = clientCommand(a, rex.LayoutCommand{
		Op: "tab.create", Space: space, Name: "editor", Dir: os.TempDir(),
		Command: []string{"/bin/sh", "-c", "echo made; exec sleep 60"}, Focus: true,
	})
	if res.Code != "" || res.New == "" {
		t.Fatalf("tab.create in a space: %+v", res)
	}
	// The space no window showed has the tab, and the window shows it, as
	// the command asks for the focus.
	w = a.st.byID(space)
	if w.app != a || a.ws != w || len(a.tabs) != 1 || a.tab().Name != "editor" || a.tab().Focus.SID != res.New {
		t.Fatalf("the window shows %s, the space's tabs: %+v", a.ws.ID, w.layout)
	}
	if savedTabSID(a.st.saved(), orig) != origSID {
		t.Fatalf("the window's space lost its tab: %+v", a.st.saved())
	}
	// Closing the space ends its tab's program and the space itself.
	sid := res.New
	if res = clientCommand(a, rex.LayoutCommand{Op: "space.close", Space: space}); res.Code != "" {
		t.Fatalf("space.close: %+v", res)
	}
	if a.st.byID(space) != nil {
		t.Error("the space stays after space.close")
	}
	if a.ws == nil || a.tabs == nil || len(a.tabs) == 0 {
		t.Error("the window shows no space after closing one")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, err := a.client.List()
		alive := err == nil && slices.ContainsFunc(info, func(in rex.SessionInfo) bool { return in.ID == sid })
		if !alive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the closed space's session stays: %v %v", info, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestCommandTabFocus shows a space no window shows for a tab a client
// makes in it with the focus, and the server forgets it then. Without the
// focus nothing moves the window: a tab made in an empty space or one with
// tabs, spaces a batch makes, a pane moved to a tab of its own, and a tab
// the space had that becomes the active one by a move, a focus or a close.
func TestCommandTabFocus(t *testing.T) {
	a, _ := newTestApp(t)
	orig := a.ws
	space := clientCommand(a, rex.LayoutCommand{Op: "space.create", Name: "work"}).Space
	if res := clientCommand(a, rex.LayoutCommand{Op: "tab.create", Space: space, Focus: true}); res.Code != "" || res.New == "" {
		t.Fatalf("tab.create with the focus: %+v", res)
	}
	w := a.ws
	if w.ID != space {
		t.Fatalf("the window shows %s, not %s", w.ID, space)
	}
	if ls, err := a.client.LayoutState(); err != nil || layout.Parse(ls.Layout).Show != "" {
		t.Fatalf("the server still asks to show a space: %s %v", ls.Layout, err)
	}
	stays := func(c rex.LayoutCommand) rex.LayoutCommand {
		t.Helper()
		res := clientCommand(a, c)
		if res.Code != "" {
			t.Fatalf("%s: %+v", c.Op, res)
		}
		if a.ws != w {
			t.Fatalf("%s: the window shows %s", c.Op, a.ws.ID)
		}
		return res
	}
	empty := stays(rex.LayoutCommand{Op: "space.create", Name: "empty"}).Space
	stays(rex.LayoutCommand{Op: "tab.create", Space: empty})
	stays(rex.LayoutCommand{Op: "layout.apply", Batch: []rex.LayoutCommand{{Op: "space.create", Name: "one"}, {Op: "space.create", Name: "two"}}})
	made := stays(rex.LayoutCommand{Op: "tab.create", Space: orig.ID})
	split := stays(rex.LayoutCommand{Op: "pane.split", SID: made.New})
	stays(rex.LayoutCommand{Op: "pane.move", SID: split.New, NewTab: true})
	if orig.app != nil || len(orig.layout.Tabs) != 3 {
		t.Fatalf("%s has %d tabs", orig.ID, len(orig.layout.Tabs))
	}
	first, second := orig.layout.Tabs[0], orig.layout.Tabs[1]
	stays(rex.LayoutCommand{Op: "tab.move", Space: orig.ID, Tab: second.ID, Index: 0})
	stays(rex.LayoutCommand{Op: "pane.focus", Space: orig.ID, SID: second.Root.SID})
	stays(rex.LayoutCommand{Op: "tab.close", Space: orig.ID, Tab: second.ID})
	if len(orig.layout.Tabs) != 2 || orig.layout.Tabs[0].ID != first.ID {
		t.Fatalf("%s has tabs %+v", orig.ID, orig.layout.Tabs)
	}
}

// TestCommandTabFocusWindow shows the space of a client's tab made with
// the focus in the window focused last.
func TestCommandTabFocusWindow(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	b := st.openWindow(st.addWorkspace())
	for _, front := range []*App{b, a} {
		st.raise(front)
		back := a
		if front == a {
			back = b
		}
		was := back.ws
		space := clientCommand(a, rex.LayoutCommand{Op: "space.create"}).Space
		clientCommand(a, rex.LayoutCommand{Op: "tab.create", Space: space, Focus: true})
		if front.ws == nil || front.ws.ID != space || back.ws != was {
			t.Fatalf("the window focused last shows %v, the other %v", front.ws, back.ws)
		}
	}
}

// TestCommandSplitRunsCommand splits a pane with a program of the
// client's, as Rex's session.new_split does.
func TestCommandSplitRunsCommand(t *testing.T) {
	a, _ := newTestApp(t)
	first := a.tab().Focus
	// The program outlives the checks, as in TestCommandSpaces.
	res := clientCommand(a, rex.LayoutCommand{
		Op: "pane.split", SID: first.SID, Dir: os.TempDir(), Vertical: true,
		Command: []string{"/bin/sh", "-c", "echo split; exec sleep 60"},
	})
	if res.Code != "" || res.New == "" {
		t.Fatalf("pane.split: %+v", res)
	}
	var split *Pane
	for _, p := range a.tab().panes() {
		if p.SID == res.New {
			split = p
		}
	}
	if split == nil {
		t.Fatal("the new pane is not in the tab")
	}
	if a.tab().Root.Vertical != true {
		t.Error("the split is not vertical")
	}
}

// savedTabSID returns the session of a workspace's first saved tab, "" for
// none.
func savedTabSID(st savedStore, workspace string) string {
	for _, w := range st.Sessions {
		if w.ID == workspace && len(w.Layout.Tabs) > 0 && w.Layout.Tabs[0].Root != nil {
			return w.Layout.Tabs[0].Root.SID
		}
	}
	return ""
}

// diffOps writes the commands diffLayout sends, one line each.
func diffOps(base, now savedStore) []string {
	var got []string
	for _, c := range diffLayout(base, now) {
		s := c.Op + " " + c.Space + c.SID + c.Tab
		if c.TabLayout != nil {
			s += fmt.Sprintf("%s after=%s focus=%v", c.TabLayout.ID, c.After, c.Focus)
		}
		if c.Op == "space.rename" || c.Op == "space.create" {
			s += " " + c.Name + "/" + c.Color
		}
		if c.Op == "windows.set" {
			s += strings.Join(c.Spaces, ",")
		}
		got = append(got, s)
	}
	return got
}

// TestDiffHostDirs checks that the directories of another host's panes, which
// no server here stamps, go as the server's tab with them alone: not the
// directories of this machine's panes, which the server keeps.
func TestDiffHostDirs(t *testing.T) {
	far := func(dir string) *layout.Node { return &layout.Node{SID: "x", Host: "h", Dir: dir} }
	base := savedStore{Windows: []string{"w"}, Sessions: []savedWorkspace{{ID: "w", Layout: layout.Layout{Tabs: []layout.Tab{
		{ID: "a", Rev: 3, Focus: 0, Root: &layout.Node{Ratio: 0.5, A: &layout.Node{SID: "1", Dir: "/one"}, B: far("/old")}},
	}}}}}
	now := savedStore{Windows: []string{"w"}, Sessions: []savedWorkspace{{ID: "w", Layout: layout.Layout{Tabs: []layout.Tab{
		// This machine's pane's directory moved too.
		{ID: "a", Root: &layout.Node{Ratio: 0.5, A: &layout.Node{SID: "1", Dir: "/elsewhere"}, B: far("/new")}},
	}}}}}
	out := diffLayout(base, now)
	if len(out) != 1 || out[0].Op != "tab.set" || out[0].Focus {
		t.Fatalf("commands %+v", out)
	}
	got := out[0].TabLayout
	if got.Focus != 0 || got.Root.B.Dir != "/new" || got.Root.A.Dir != "/one" || got.Root.Ratio != 0.5 {
		t.Errorf("the tab sent %+v %+v %+v", got, got.Root.A, got.Root.B)
	}
	if base.Sessions[0].Tabs[0].Root.B.Dir != "/old" {
		t.Error("the base changed")
	}
	// This machine's directories alone send nothing: the server's.
	now.Sessions[0].Tabs[0].Root.B.Dir = "/old"
	if out := diffLayout(base, now); len(out) != 0 {
		t.Errorf("facts of this machine sent %+v", out)
	}
}

// TestDiffLayout checks the commands the app sends for what it changed:
// new spaces first; the sessions it closed end; a tab that takes a session
// from another goes before the tab that gives it; a tab the app made
// active takes the focus, a tab changed otherwise does not, so another
// client's focus stays; then closed tabs and spaces, names, and windows.
// A close or a move of one tab sends nothing of the others.
func TestDiffLayout(t *testing.T) {
	leaf := func(sid string) *layout.Node { return &layout.Node{SID: sid} }
	split := func(a, b *layout.Node) *layout.Node { return &layout.Node{Ratio: 0.5, A: a, B: b} }
	tab := func(id string, root *layout.Node) layout.Tab { return layout.Tab{ID: id, Root: root} }
	space := func(id string, tabs ...layout.Tab) savedWorkspace {
		return savedWorkspace{ID: id, Name: id, Layout: layout.Layout{Tabs: tabs}}
	}
	base := savedStore{Windows: []string{"w"}, Sessions: []savedWorkspace{
		space("w", tab("a", split(leaf("1"), leaf("2"))), tab("b", leaf("3")), tab("c", leaf("4"))),
		space("old", tab("d", leaf("5"))),
	}}
	now := savedStore{Windows: []string{"w", "new"}, Sessions: []savedWorkspace{
		// 2 closed, 3 moves from b into a, c closed, b gets a browser.
		{ID: "w", Name: "renamed", Color: "teal", Layout: layout.Layout{Tabs: []layout.Tab{
			tab("a", split(leaf("1"), leaf("3"))),
			tab("b", &layout.Node{URL: "https://example.com/", ID: "web"}),
		}}},
		space("new"),
	}}
	want := []string{
		"space.create new new/default",
		"pane.close w2a",
		"tab.set wa after= focus=false",
		"tab.set wb after=a focus=false",
		"tab.close wc",
		"space.close old",
		"space.rename w renamed/teal",
		"windows.set w,new",
	}
	if got := diffOps(base, now); !slices.Equal(got, want) {
		t.Errorf("commands\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// The app's active tab, made so here, takes the focus; nothing else
	// changed sends nothing more.
	next := now
	next.Sessions = slices.Clone(now.Sessions)
	next.Sessions[0].Active = 1
	if cmds := diffLayout(now, next); len(cmds) != 1 || cmds[0].Op != "tab.set" || cmds[0].TabLayout.ID != "b" || !cmds[0].Focus {
		t.Errorf("a tab made active: %+v", cmds)
	}
	if cmds := diffLayout(now, now); len(cmds) != 0 {
		t.Errorf("no change: %+v", cmds)
	}
	// Closing the first of five tabs closes it alone; moving the last
	// first moves it alone.
	five := savedStore{Sessions: []savedWorkspace{space("w", tab("t1", leaf("1")), tab("t2", leaf("2")), tab("t3", leaf("3")), tab("t4", leaf("4")), tab("t5", leaf("5")))}}
	closed := savedStore{Sessions: []savedWorkspace{space("w", five.Sessions[0].Tabs[1:]...)}}
	closed.Sessions[0].Active = 0
	five.Sessions[0].Active = 1
	if got := diffOps(five, closed); !slices.Equal(got, []string{"tab.close wt1"}) {
		t.Errorf("a close: %v", got)
	}
	ts := five.Sessions[0].Tabs
	moved := savedStore{Sessions: []savedWorkspace{space("w", ts[4], ts[0], ts[1], ts[2], ts[3])}}
	moved.Sessions[0].Active = 2
	if got := diffOps(five, moved); !slices.Equal(got, []string{"tab.set wt5 after= focus=false"}) {
		t.Errorf("a move: %v", got)
	}
}

// TestDiffLayoutApplies has the server make what diffLayout sends, as one
// batch, from layouts of the server's sessions: two tabs that swap panes,
// a reorder, a tab to another space, a merge. The server's layout is the
// app's then.
func TestDiffLayoutApplies(t *testing.T) {
	a, _ := newTestApp(t)
	c := a.client
	var sids []string
	for range 4 {
		in, err := c.Create(rex.CreateOptions{Dir: os.TempDir(), Command: []string{testShell.path, "-c", "sleep 60"}})
		if err != nil {
			t.Fatal(err)
		}
		sids = append(sids, in.ID)
	}
	leaf := func(i int) *layout.Node { return &layout.Node{SID: sids[i]} }
	split := func(x, y *layout.Node) *layout.Node { return &layout.Node{Ratio: 0.5, A: x, B: y} }
	tab := func(id string, root *layout.Node) layout.Tab { return layout.Tab{ID: id, Root: root} }
	store := func(spaces ...savedWorkspace) savedStore {
		return savedStore{Version: layout.Version, Windows: []string{"w"}, Sessions: spaces}
	}
	space := func(id string, tabs ...layout.Tab) savedWorkspace {
		return savedWorkspace{ID: id, Name: id, Layout: layout.Layout{Tabs: tabs}}
	}
	steps := []savedStore{
		store(space("w", tab("a", split(leaf(0), leaf(1))), tab("b", split(leaf(2), leaf(3))))),
		// a and b swap their second panes.
		store(space("w", tab("a", split(leaf(0), leaf(3))), tab("b", split(leaf(2), leaf(1))))),
		// b goes first.
		store(space("w", tab("b", split(leaf(2), leaf(1))), tab("a", split(leaf(0), leaf(3))))),
		// a goes to a space of its own.
		store(space("w", tab("b", split(leaf(2), leaf(1)))), space("x", tab("a", split(leaf(0), leaf(3))))),
		// a merges into b.
		store(space("w", tab("b", split(split(leaf(2), leaf(1)), split(leaf(0), leaf(3))))), space("x")),
	}
	raw, _ := json.Marshal(steps[0])
	if err := c.SetLayout(raw); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(steps); i++ {
		batch := diffLayout(steps[i-1], steps[i])
		if res, err := c.Command(rex.LayoutCommand{Op: "layout.apply", Batch: batch}); err != nil || res.Code != "" {
			t.Fatalf("step %d: %+v %v", i, res, err)
		}
		got := savedWorkspaces(t, c)
		got.Windows = steps[i].Windows
		if !sameArrangement(got, steps[i]) {
			g, _ := json.Marshal(got.Sessions)
			w, _ := json.Marshal(steps[i].Sessions)
			t.Fatalf("step %d: the server has\n%s\nwant\n%s", i, g, w)
		}
	}
}

// TestReadOnlyCommandSays checks the answer of a layout change an old
// server queues to the app, which is read-only on it ("Later"): the
// banner's words, which say to restart the server, not that the server
// makes it.
func TestReadOnlyCommandSays(t *testing.T) {
	a, _ := newTestApp(t)
	for _, ro := range []bool{true, false} {
		a.st.readOnly = ro
		res := a.command(rex.LayoutCommand{Op: "pane.split", SID: a.tab().Focus.SID})
		switch {
		case ro && (res.Code != "read_only" || res.Error != locales.ServerLayoutReadOnly()):
			t.Errorf("read-only: %+v", res)
		case !ro && res.Code != "desktop_unavailable":
			t.Errorf("on a server that makes it: %+v", res)
		}
	}
	a.st.readOnly = false
}

// TestOutboxUseForgetsRevision checks that a connection to a server that
// started again, whose revisions count from 1, starts the app's from
// nothing: the revision it follows, and its tabs', which it reads again
// from the layout, which keeps the tabs' own (layout.Tab.Rev).
func TestOutboxUseForgetsRevision(t *testing.T) {
	var o outbox
	o.took(12, []byte(`{"version":2,"sessions":[{"id":"w","tabs":[{"id":"a","rev":7,"root":{"sid":"1"}}]}]}`))
	if o.revision() != 12 || o.revs["a"] != 7 {
		t.Fatalf("took %d %v", o.revision(), o.revs)
	}
	o.use(nil, nil, nil)
	if o.revision() != 0 || o.revs != nil {
		t.Errorf("after a new connection: %d %v", o.revision(), o.revs)
	}
	o.took(2, []byte(`{"version":2,"sessions":[{"id":"w","tabs":[{"id":"a","rev":7,"root":{"sid":"1"}}]}]}`))
	if o.revision() != 2 || o.revs["a"] != 7 {
		t.Errorf("after the restarted server's layout: %d %v", o.revision(), o.revs)
	}
}
