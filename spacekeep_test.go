package main

import (
	"slices"
	"strings"
	"testing"
)

// shellUp waits for the pane's shell to print its prompt.
func shellUp(t *testing.T, a *App, p *Pane) {
	t.Helper()
	waitForThat(t, func() bool {
		a.runPosted()
		return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt)
	}, "the shell")
}

// TestSpaceKeepsPanes checks that a window going back to a space it showed
// shows the same panes, attached to the same streams, their titles kept,
// while the space's layout names them for the saved layout.
func TestSpaceKeepsPanes(t *testing.T) {
	a, tt := newTestApp(t)
	st := a.st
	first := a.ws
	a.split(false)
	a.newTab(testShell.dir)
	a.selectTab(0)
	panes := a.tabs[0].allPanes()
	p := panes[0]
	shellUp(t, a, p)
	term, stream, sids := p.term, p.stream, sidsOf(a)
	p.title = "vim"

	a.newWorkspace()
	tt.Frame()
	if first.kept == nil || first.app != nil || p.closed {
		t.Fatalf("the space left kept %v, its pane closed %v", first.kept, p.closed)
	}
	if len(first.layout.Tabs) != 2 || first.layout.Tabs[0].Root.A == nil {
		t.Errorf("the kept space's layout is %+v", first.layout)
	}
	a.showWorkspace(first)
	tt.Frame()
	if first.kept != nil || len(a.tabs) != 2 || a.active != 0 || !slices.Equal(a.tabs[0].allPanes(), panes) {
		t.Fatalf("the space came back on tabs %d, active %d", len(a.tabs), a.active)
	}
	if p.closed || p.term != term || p.stream != stream || p.title != "vim" || !slices.Equal(sidsOf(a), sids) {
		t.Errorf("the pane came back closed %v, attached again %v, titled %q", p.closed, p.term != term || p.stream != stream, p.title)
	}
	// Next Space round the two, as the shortcut does.
	a.cycleWorkspace(1)
	a.cycleWorkspace(1)
	if a.ws != first || p.term != term || p.closed {
		t.Errorf("Next Space came back to %s, attached again %v", a.ws.Name, p.term != term)
	}
	quit(st)
	if !p.closed {
		t.Error("quitting left a kept pane open")
	}
}

// TestKeptSpaceInOtherWindow checks that another window showing a space
// this one keeps closes the kept panes, whose callbacks are this window's,
// and attaches to the same sessions from the layout.
func TestKeptSpaceInOtherWindow(t *testing.T) {
	a, tt := newTestApp(t)
	st := a.st
	first := a.ws
	a.split(true)
	panes, sids := a.tab().allPanes(), sidsOf(a)
	a.newWorkspace()
	tt.Frame()
	b := st.openWindow(st.addWorkspace(), false)
	b.showWorkspace(first)
	if b.ws != first || first.app != b || first.kept != nil {
		t.Fatalf("the other window shows %s, kept %v", b.ws.Name, first.kept)
	}
	for _, p := range panes {
		if !p.closed {
			t.Errorf("kept pane %s stayed open", p.SID)
		}
	}
	if !slices.Equal(sidsOf(b), sids) || slices.Contains(b.tab().allPanes(), panes[0]) {
		t.Errorf("the other window shows %q, not %q from the layout", sidsOf(b), sids)
	}
	// The space is the other window's now: this one fronts it.
	a.showWorkspace(first)
	if a.ws == first || st.apps[0] != b {
		t.Errorf("the window took the space back")
	}
	quit(st)
}

// TestKeptPaneExits checks that a kept pane whose session ends leaves the
// tabs the window shows as they were: its tab goes from the kept ones, and
// from the space's layout.
func TestKeptPaneExits(t *testing.T) {
	a, tt := newTestApp(t)
	st := a.st
	first := a.ws
	a.newTab(testShell.dir)
	gone, stays := a.tabs[0].Focus, a.tabs[1]
	shellUp(t, a, gone)
	a.selectTab(1)
	a.newWorkspace()
	tt.Frame()
	shown := slices.Clone(a.tabs)
	if err := a.client.Kill(gone.SID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the kept pane to end", func() bool { return gone.closed })
	if a.ws == first || !slices.Equal(a.tabs, shown) || a.tab() != shown[0] || a.tab().Root == nil {
		t.Fatalf("the shown space changed: %d tabs, on %s", len(a.tabs), a.ws.Name)
	}
	if k := first.kept; k == nil || !slices.Equal(k.tabs, []*Tab{stays}) || k.active != 0 {
		t.Fatalf("the kept tabs are %+v", first.kept)
	}
	if l := first.layout; len(l.Tabs) != 1 || l.Tabs[0].Root.SID != stays.Focus.SID {
		t.Errorf("the kept space's layout is %+v", l)
	}
	a.showWorkspace(first)
	if len(a.tabs) != 1 || a.tab() != stays {
		t.Errorf("the space came back on %d tabs", len(a.tabs))
	}
	quit(st)
}
