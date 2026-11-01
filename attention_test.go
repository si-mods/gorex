package main

import (
	"testing"
	"time"

	"gorex/internal/rex"
)

func oneStatus(state string, at time.Time) []rex.ProgramStatus {
	return []rex.ProgramStatus{{State: state, Updated: at}}
}

func TestNeedsYouPredicate(t *testing.T) {
	now := time.Now()
	p := &Pane{}
	if p.needsYou() != 0 {
		t.Fatal("a quiet pane needs no one")
	}
	p.info.Statuses = oneStatus("working", now)
	if p.needsYou() != 0 {
		t.Fatal("working needs no one")
	}
	p.info.Statuses = oneStatus("done", now)
	done := p.needsYou()
	p.seen = now
	if done == 0 || p.needsYou() != 0 {
		t.Fatalf("done: unseen %d, seen %d", done, p.needsYou())
	}
	p.attention = true
	att := p.needsYou()
	p.info.Statuses = oneStatus("blocked", now)
	blocked := p.needsYou()
	if !(blocked > done && done > att && att > 0) {
		t.Fatalf("order: blocked %d, done %d, attention %d", blocked, done, att)
	}
	p.info.Statuses = oneStatus("error", now.Add(time.Second))
	if e := p.needsYou(); !(e > done && e < blocked) {
		t.Fatalf("error ranks %d", e)
	}
}

func TestJumpToNeedy(t *testing.T) {
	a, _ := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	now := time.Now()
	t1 := a.tab()
	first := t1.Focus
	t2 := a.newTab(t.TempDir())
	t3 := a.newTab(t.TempDir())
	a.syncBadge()
	if testBadge.Load() != 0 {
		t.Fatalf("badge %d without needs", testBadge.Load())
	}
	// Tab 3 has the focus and is blocked, tab 1 is done, tab 2 rang.
	t2.Focus.attention = true
	t3.Focus.info.Statuses = oneStatus("blocked", now)
	first.info.Statuses = oneStatus("done", now)
	a.syncBadge()
	if n := testBadge.Load(); n != 3 {
		t.Fatalf("badge %d, want 3", n)
	}
	got := a.needingList()
	if len(got) != 3 || got[0].pane != t3.Focus || got[1].pane != first || got[2].pane != t2.Focus {
		t.Fatalf("order wrong: %+v", got)
	}
	// Blocked is first but has the focus: on from the done one, to the bell,
	// and round to the blocked pane.
	want := []*Tab{t1, t2, t3}
	for i, w := range want {
		a.jumpToNeedy() // the focused blocked pane is skipped; going there sees the rest
		if a.tab() != w {
			t.Fatalf("press %d went to tab %d", i+1, a.active)
		}
		a.syncBadge()
	}
	if n := testBadge.Load(); n != 1 {
		t.Fatalf("badge %d, want only the blocked pane", n)
	}
	cur := a.active
	a.jumpToNeedy() // the only one asking has the focus
	if a.active != cur {
		t.Fatal("jumped with nothing else to jump to")
	}
	t3.Focus.info.Statuses = nil
	a.syncBadge()
	if n := testBadge.Load(); n != 0 {
		t.Fatalf("badge %d after all cleared", n)
	}
}

func TestDockBadgeSetting(t *testing.T) {
	a, _ := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	a.newTab(t.TempDir()) // the first tab is no longer shown
	a.tabs[0].Focus.attention = true
	a.syncBadge()
	if testBadge.Load() != 1 {
		t.Fatalf("badge %d, want 1", testBadge.Load())
	}
	prefs.HideDockBadge = true
	a.syncBadge()
	if testBadge.Load() != 0 {
		t.Fatalf("badge %d with the setting off", testBadge.Load())
	}
}

// TestJumpToNeedyCycles visits three blocked panes in turn, which stay
// blocked when seen.
func TestJumpToNeedyCycles(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	now := time.Now()
	ta := a.tab()
	tb := a.newTab(t.TempDir())
	tc := a.newTab(t.TempDir())
	for _, x := range []*Tab{ta, tb, tc} {
		x.Focus.info.Statuses = oneStatus("blocked", now)
	}
	a.selectTab(0)
	tt.Frame()
	for i, w := range []*Tab{tb, tc, ta, tb} {
		a.jumpToNeedy()
		if a.tab() != w {
			t.Fatalf("press %d went to tab %d", i+1, a.active)
		}
		tt.Frame()
	}
}

// needingList is the list of needing, for tests.
func (a *App) needingList() []needy {
	l, _ := a.needing(nil)
	return l
}

// TestJumpWalksInOrder checks that the jump goes on from the pane it went
// to though that pane left the list, and does not come back to a pane that
// stays blocked between the others.
func TestJumpWalksInOrder(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	now := time.Now()
	ta := a.tab()
	tb := a.newTab(t.TempDir())
	tc := a.newTab(t.TempDir())
	a.newTab(t.TempDir())
	ta.Focus.info.Statuses = oneStatus("blocked", now)
	tb.Focus.info.Statuses = oneStatus("done", now)
	tc.Focus.info.Statuses = oneStatus("blocked", now)
	tt.Frame()
	// Blocked first (a, c), then done (b), which leaves once seen, and round.
	for i, w := range []*Tab{ta, tc, tb, ta, tc} {
		a.jumpToNeedy()
		if a.tab() != w {
			t.Fatalf("press %d went to tab %d", i+1, a.active)
		}
		tt.Frame()
	}
}

// TestJumpIntoZoomedTab checks that the target is drawn, and that the pane
// the tab had in focus is not marked seen.
func TestJumpIntoZoomedTab(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	now := time.Now()
	t1 := a.tab()
	a.split(false)
	panes := t1.panes()
	first, second := panes[0], panes[1]
	t1.setFocus(first)
	a.zoom(first, true)
	second.info.Statuses = oneStatus("blocked", now)
	first.info.Statuses = oneStatus("done", now)
	a.newTab(t.TempDir()) // away from the first tab
	tt.Frame()
	a.jumpToNeedy()
	if a.tab() != t1 || t1.Focus != second || t1.Zoom != second {
		t.Fatalf("tab %d focus second %v zoom second %v", a.active, t1.Focus == second, t1.Zoom == second)
	}
	if first.status() == nil {
		t.Fatal("the pane the tab had in focus was marked seen")
	}
}
