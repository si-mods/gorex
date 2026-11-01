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
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	now := time.Now()
	t1 := a.tab()
	first := t1.Focus
	t2 := a.newTab(t.TempDir())
	t3 := a.newTab(t.TempDir())
	tt.Frame()
	if testBadge.Load() != 0 {
		t.Fatalf("badge %d without needs", testBadge.Load())
	}
	// Tab 3 has the focus and is blocked, tab 1 is done, tab 2 rang.
	t2.Focus.attention = true
	t3.Focus.info.Statuses = oneStatus("blocked", now)
	first.info.Statuses = oneStatus("done", now)
	tt.Frame()
	if n := testBadge.Load(); n != 3 {
		t.Fatalf("badge %d, want 3", n)
	}
	got := a.needing()
	if len(got) != 3 || got[0].pane != t3.Focus || got[1].pane != first || got[2].pane != t2.Focus {
		t.Fatalf("order wrong: %+v", got)
	}
	want := []*Tab{t1, t3, t2, t3}
	for i, w := range want[:3] {
		a.jumpToNeedy() // the focused blocked pane is skipped; going there sees the rest
		if a.tab() != w {
			t.Fatalf("press %d went to tab %d", i+1, a.active)
		}
		tt.Frame()
	}
	if n := testBadge.Load(); n != 1 {
		t.Fatalf("badge %d, want only the blocked pane", n)
	}
	a.jumpToNeedy()
	if a.tab() != t3 {
		t.Fatalf("press 4 went to tab %d", a.active)
	}
	cur := a.active
	a.jumpToNeedy() // the only one asking has the focus
	if a.active != cur {
		t.Fatal("jumped with nothing else to jump to")
	}
	t3.Focus.info.Statuses = nil
	tt.Frame()
	if n := testBadge.Load(); n != 0 {
		t.Fatalf("badge %d after all cleared", n)
	}
}

func TestDockBadgeSetting(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs
	t.Cleanup(func() { prefs = old })
	prefs = settings{}
	a.newTab(t.TempDir()) // the first tab is no longer shown
	a.tabs[0].Focus.attention = true
	tt.Frame()
	if testBadge.Load() != 1 {
		t.Fatalf("badge %d, want 1", testBadge.Load())
	}
	prefs.HideDockBadge = true
	tt.Frame()
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
