package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/locales"
)

func TestNoteTitle(t *testing.T) {
	long := strings.Repeat("x", 60)
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{" \n\t\n", ""},
		{"Buy milk", "Buy milk"},
		{"\n\n  Buy milk  \nand eggs", "Buy milk"},
		{long, strings.Repeat("x", noteTitleMax) + "…"},
	} {
		if got := noteTitle(c.in); got != c.want {
			t.Errorf("noteTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNoteTab(t *testing.T) {
	a, tt := newTestApp(t)
	first := a.tab()
	a.invokeCommand(&cmdNewNote, nil)
	tt.Frame()
	if len(a.tabs) != 2 || a.tab() == first {
		t.Fatalf("%d tabs, active one is the first", len(a.tabs))
	}
	p := a.tab().Focus
	if p == nil || p.note == nil || p.SID != "" || p.term != nil {
		t.Fatalf("focus is not a note pane: %+v", p)
	}
	if name, _ := p.label(); name != locales.NoteName() {
		t.Errorf("name %q", name)
	}
	if _, ok := tt.Find(locales.NotePlaceholder()); !ok {
		t.Errorf("no text area; texts %q", tt.Texts())
	}
	if a.findable() {
		t.Error("find is offered in a note pane")
	}
	// The text takes the keyboard of the new pane, and names it.
	tt.Type("Buy milk")
	tt.Key(0, ui.KeyEnter)
	tt.Type("and eggs")
	tt.Frame()
	if p.note.text != "Buy milk\nand eggs" {
		t.Fatalf("text %q", p.note.text)
	}
	if name, _ := p.label(); name != "Buy milk" {
		t.Errorf("name %q", name)
	}
	if !a.saveDue {
		t.Error("an edit does not ask for a save")
	}
}

func TestSplitNote(t *testing.T) {
	a, tt := newTestApp(t)
	tab := a.tab()
	a.invokeCommand(&cmdSplitNote, nil)
	tt.Frame()
	ps := tab.panes()
	if len(ps) != 2 {
		t.Fatalf("%d panes", len(ps))
	}
	p := tab.Focus
	if p == nil || p.note == nil || p == ps[0] {
		t.Fatalf("the new pane is not a note beside the shell: %+v", p)
	}
	if ps[0].note != nil || ps[0].term == nil {
		t.Error("the shell pane is gone")
	}
}

func findNote(n *layout.Node) *layout.Node {
	if n == nil {
		return nil
	}
	if n.Kind == noteKind {
		return n
	}
	if got := findNote(n.A); got != nil {
		return got
	}
	return findNote(n.B)
}

func TestNoteLayout(t *testing.T) {
	a, tt := newTestApp(t)
	a.invokeCommand(&cmdNewNote, nil)
	tt.Frame()
	p := a.tab().Focus
	p.Name = "Todo"
	p.note.text = "Buy milk\nand eggs"
	a.saveNow()

	l := savedWorkspaces(t, a.client).Sessions[0].Layout
	n := findNote(l.Tabs[len(l.Tabs)-1].Root)
	if n == nil {
		t.Fatalf("the layout keeps no note: %+v", l.Tabs[len(l.Tabs)-1].Root)
	}
	if n.SID != "" || n.Name != "Todo" || n.Note != "Buy milk\nand eggs" {
		t.Errorf("saved node %+v", n)
	}

	b := &App{client: a.client}
	if !b.restore(l, nil) {
		t.Fatal("nothing restored")
	}
	var got *Pane
	for _, tab := range b.tabs {
		for _, p := range tab.panes() {
			if p.note != nil {
				got = p
			}
		}
	}
	if got == nil {
		t.Fatal("the note pane is not restored")
	}
	if got.note.text != "Buy milk\nand eggs" || got.Name != "Todo" || got.SID != "" || got.term != nil {
		t.Errorf("restored %+v %q", got, got.note.text)
	}
}

func TestEmptyNoteLayout(t *testing.T) {
	a, tt := newTestApp(t)
	a.invokeCommand(&cmdNewNote, nil)
	tt.Frame()
	a.saveNow()
	l := savedWorkspaces(t, a.client).Sessions[0].Layout
	b := &App{client: a.client}
	if !b.restore(l, nil) {
		t.Fatal("nothing restored")
	}
	notes := 0
	for _, tab := range b.tabs {
		for _, p := range tab.panes() {
			if p.note != nil {
				notes++
			}
		}
	}
	if notes != 1 {
		t.Errorf("%d notes restored, want the empty one", notes)
	}
}

func TestMoveNoteToNewTab(t *testing.T) {
	a, tt := newTestApp(t)
	a.invokeCommand(&cmdSplitNote, nil)
	tt.Frame()
	p := a.tab().Focus
	p.note.text = "keep me"
	if !a.movePaneToNewTab(p, "") {
		t.Fatal("not moved")
	}
	if p.Tab == nil || len(p.Tab.panes()) != 1 || p.note == nil || p.note.text != "keep me" {
		t.Fatalf("moved pane %+v", p)
	}
}
