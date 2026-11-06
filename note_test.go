package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
		{"# Title\nbody", "Title"},
		{"###### Six", "Six"},
		{"####### Seven", "####### Seven"},
		{"#hashtag", "#hashtag"},
		{"- item", "item"},
		{"* item", "item"},
		{"+ item", "item"},
		{"> quote", "quote"},
		{"-5 degrees", "-5 degrees"},
		{"#\nnext", "#"},
		{"# \nnext", "#"},
		{"\n\n  Buy milk  \nand eggs", "Buy milk"},
		{long, strings.Repeat("x", noteTitleMax) + "…"},
		{strings.Repeat("é", 100), strings.Repeat("é", noteTitleMax) + "…"},
		{strings.Repeat("\n", 1<<20) + "late", "late"},
		{"first\n" + strings.Repeat("y", 2<<20), "first"},
		{strings.Repeat("z", 2<<20), strings.Repeat("z", noteTitleMax) + "…"},
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

func restoredNotes(b *App) []*Pane {
	var out []*Pane
	for _, tab := range b.tabs {
		for _, p := range tab.panes() {
			if p.note != nil {
				out = append(out, p)
			}
		}
	}
	return out
}

// typeNote sets the text of the note pane p, as the text area does, and
// shows a frame, which writes the file.
func typeNote(tt *ui.Tester, p *Pane, text string) {
	p.note.text = text
	tt.Frame()
}

func TestNoteLayout(t *testing.T) {
	a, tt := newTestApp(t)
	a.invokeCommand(&cmdNewNote, nil)
	tt.Frame()
	p := a.tab().Focus
	p.Name = "Todo"
	typeNote(tt, p, "Buy milk\nand eggs")
	a.saveNow()

	l := savedWorkspaces(t, a.client).Sessions[0].Layout
	n := findNote(l.Tabs[len(l.Tabs)-1].Root)
	if n == nil {
		t.Fatalf("the layout keeps no note: %+v", l.Tabs[len(l.Tabs)-1].Root)
	}
	if n.SID != "" || n.Name != "Todo" || n.Note != p.note.id || !validNoteID(n.Note) {
		t.Errorf("saved node %+v", n)
	}
	raw, _ := json.Marshal(l)
	if strings.Contains(string(raw), "Buy milk") {
		t.Error("the note's text is in the layout")
	}

	b := &App{client: a.client}
	if !b.reconcile(l, nil, nil) {
		t.Fatal("nothing restored")
	}
	var got *Pane
	if ns := restoredNotes(b); len(ns) > 0 {
		got = ns[0]
	}
	if got == nil {
		t.Fatal("the note pane is not restored")
	}
	if got.note.text != "Buy milk\nand eggs" || got.note.id != p.note.id || got.Name != "Todo" || got.SID != "" || got.term != nil {
		t.Errorf("restored %+v %q", got, got.note.text)
	}
}

func TestLargeNoteStaysOutOfLayout(t *testing.T) {
	a, tt := newTestApp(t)
	a.invokeCommand(&cmdNewNote, nil)
	tt.Frame()
	p := a.tab().Focus
	text := strings.Repeat("0123456789abcdef\n", 2<<20/17)
	typeNote(tt, p, text)
	a.saveDue = false
	typeNote(tt, p, text+"more")
	if a.saveDue {
		t.Error("an edit of a note asks for a layout save")
	}
	raw, err := json.Marshal(a.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 64<<10 {
		t.Errorf("the layout is %d bytes with a large note", len(raw))
	}
	b, err := os.ReadFile(p.note.path())
	if err != nil || string(b) != text+"more" {
		t.Errorf("the note's file holds %d bytes, err %v", len(b), err)
	}
	if fi, err := os.Stat(p.note.path()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("file %v %v", fi, err)
	}
	if ents, _ := os.ReadDir(noteDir()); len(ents) != 1 {
		t.Errorf("%d files in the notes directory, want 1", len(ents))
	}
}

func TestNoteFileLife(t *testing.T) {
	a, tt := newTestApp(t)
	a.invokeCommand(&cmdSplitNote, nil)
	tt.Frame()
	p := a.tab().Focus
	typeNote(tt, p, "keep me")
	path := p.note.path()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	// Moving the pane keeps its file and its id.
	if !a.movePaneToNewTab(p, "") {
		t.Fatal("not moved")
	}
	if p.Tab == nil || len(p.Tab.panes()) != 1 || p.note == nil || p.note.text != "keep me" {
		t.Fatalf("moved pane %+v", p)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("moving deleted the file: %v", err)
	}
	// Closing it deletes the file.
	a.closePane(p)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("closing kept the file: %v", err)
	}

	a.invokeCommand(&cmdNewNote, nil)
	tt.Frame()
	q := a.tab().Focus
	typeNote(tt, q, "another")
	path = q.note.path()
	a.closeTab(a.tab())
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("closing the tab kept the file: %v", err)
	}
}

func TestNoteIDSafe(t *testing.T) {
	for id, want := range map[string]bool{
		newNoteID():                      true,
		"":                               false,
		"../../etc/passwd":               false,
		strings.Repeat("a", 31):          false,
		strings.Repeat("A", 32):          false,
		strings.Repeat("a", 31) + "/":    false,
		strings.Repeat("g", 32):          false,
		strings.Repeat("a", 32) + "\x00": false,
	} {
		if got := validNoteID(id); got != want {
			t.Errorf("validNoteID(%q) = %v", id, got)
		}
	}
	a, _ := newTestApp(t)
	// An unsafe id makes a new note, and reads and deletes no other file.
	secret := filepath.Join(noteDir(), "..", "secret.txt")
	os.MkdirAll(noteDir(), 0o700)
	os.WriteFile(secret, []byte("x"), 0o600)
	p := a.newNotePane("../secret")
	if p.note.text != "" || !validNoteID(p.note.id) {
		t.Errorf("note %+v", p.note)
	}
	(&notePane{id: "../secret"}).destroy()
	if _, err := os.Stat(secret); err != nil {
		t.Error("an unsafe id deleted a file")
	}
}

func TestEmptyNoteLayout(t *testing.T) {
	a, tt := newTestApp(t)
	a.invokeCommand(&cmdNewNote, nil)
	tt.Frame()
	a.saveNow()
	l := savedWorkspaces(t, a.client).Sessions[0].Layout
	b := &App{client: a.client}
	if !b.reconcile(l, nil, nil) {
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

func TestNoteZoom(t *testing.T) {
	n := &notePane{}
	if n.textSize() != noteDefaultSize {
		t.Fatalf("default size %v", n.textSize())
	}
	n.zoom(noteSizeStep)
	if n.textSize() != noteDefaultSize+1 {
		t.Errorf("bigger %v", n.textSize())
	}
	n.zoom(-noteSizeStep)
	if n.size != 0 {
		t.Errorf("back at the default the size is %v, want 0", n.size)
	}
	for i := 0; i < 100; i++ {
		n.zoom(noteSizeStep)
	}
	if n.textSize() != noteMaxSize {
		t.Errorf("max %v", n.textSize())
	}
	for i := 0; i < 100; i++ {
		n.zoom(-noteSizeStep)
	}
	if n.textSize() != noteMinSize {
		t.Errorf("min %v", n.textSize())
	}
	n.zoom(0)
	if n.size != 0 || n.textSize() != noteDefaultSize {
		t.Errorf("reset %v", n.size)
	}
	if clampNoteSize(1000) != noteMaxSize || clampNoteSize(-3) != 0 || clampNoteSize(2) != noteMinSize {
		t.Error("clampNoteSize")
	}
}

func TestNoteZoomCommands(t *testing.T) {
	a, tt := newTestApp(t)
	termSize := termFont.Size
	// A terminal has the focus: the keys change its text, not a note's.
	a.invokeCommand(&cmdSplitNote, nil)
	tt.Frame()
	note := a.tab().Focus
	a.tab().setFocus(a.tab().panes()[0])
	a.invokeCommand(&cmdBigger, nil)
	if note.note.size != 0 || termFont.Size != termSize+1 {
		t.Errorf("terminal focused: note %v term %v", note.note.size, termFont.Size)
	}
	a.invokeCommand(&cmdActualSize, nil)
	// A note has it: its size changes, the terminals' does not.
	a.tab().setFocus(note)
	a.saveDue = false
	a.invokeCommand(&cmdBigger, nil)
	a.invokeCommand(&cmdBigger, nil)
	a.invokeCommand(&cmdSmaller, nil)
	if note.note.size != noteDefaultSize+1 || termFont.Size != termSize {
		t.Errorf("note focused: note %v term %v", note.note.size, termFont.Size)
	}
	if !a.saveDue {
		t.Error("a new size does not ask for a layout save")
	}
	a.invokeCommand(&cmdActualSize, nil)
	if note.note.size != 0 {
		t.Errorf("reset %v", note.note.size)
	}
}

func TestNotePreviewAvailable(t *testing.T) {
	a, tt := newTestApp(t)
	if cmdNotePreview.Available(a) {
		t.Error("toggle preview is offered on a terminal")
	}
	// On a terminal it is a silent no-op, and its key is not taken.
	a.invokeCommand(&cmdNotePreview, nil)
	if a.err != "" {
		t.Errorf("error %q with no note focused", a.err)
	}
	if !cmdNotePreview.Passthrough {
		t.Error("its key is taken from a terminal")
	}
	a.invokeCommand(&cmdNewNote, nil)
	tt.Frame()
	if !cmdNotePreview.Available(a) {
		t.Fatal("toggle preview is not offered on a note")
	}
	p := a.tab().Focus
	a.focusReq = nil
	a.invokeCommand(&cmdNotePreview, nil)
	if a.focusReq != p {
		t.Error("the toggle does not give the note the keyboard")
	}
	if !p.note.preview {
		t.Error("not previewing")
	}
	a.invokeCommand(&cmdNotePreview, nil)
	if p.note.preview {
		t.Error("still previewing")
	}
}

func TestNoteSizeAndPreviewPersist(t *testing.T) {
	a, tt := newTestApp(t)
	a.invokeCommand(&cmdNewNote, nil)
	tt.Frame()
	p := a.tab().Focus
	typeNote(tt, p, "# Title\n\nbody")
	p.note.size, p.note.preview = 18, true
	a.saveNow()
	l := savedWorkspaces(t, a.client).Sessions[0].Layout
	n := findNote(l.Tabs[len(l.Tabs)-1].Root)
	if n == nil || n.NoteSize != 18 || !n.NotePreview {
		t.Fatalf("saved node %+v", n)
	}
	b := &App{client: a.client}
	if !b.reconcile(l, nil, nil) {
		t.Fatal("nothing restored")
	}
	ns := restoredNotes(b)
	if len(ns) == 0 || ns[0].note.size != 18 || !ns[0].note.preview {
		t.Fatalf("restored %+v", ns)
	}
	// Moving to another window keeps both.
	w := a.st.openWindow(a.st.addWorkspace(), false)
	a.movePaneToWindow(p, w)
	ms := restoredNotes(w)
	if len(ms) != 1 || ms[0].note.size != 18 || !ms[0].note.preview {
		t.Fatalf("moved %+v", ms)
	}
}

func TestNotePreviewRenders(t *testing.T) {
	a, tt := newTestApp(t)
	a.invokeCommand(&cmdNewNote, nil)
	tt.Frame()
	p := a.tab().Focus
	typeNote(tt, p, "# Shopping list\n\n- milk")
	a.invokeCommand(&cmdNotePreview, nil)
	tt.Frame()
	if _, ok := tt.Find("Shopping list"); !ok {
		t.Errorf("no heading; texts %q", tt.Texts())
	}
	if _, ok := tt.Find(locales.NotePlaceholder()); ok {
		t.Error("placeholder on a note with text")
	}
	typeNote(tt, p, "")
	tt.Frame()
	if _, ok := tt.Find(locales.NotePlaceholder()); !ok {
		t.Errorf("no placeholder in an empty preview; texts %q", tt.Texts())
	}
}

func TestTextSizeRangeShared(t *testing.T) {
	a, _ := newTestApp(t)
	old := prefs.FontSize
	t.Cleanup(func() { a.setFontSize(old) })
	a.setFontSize(1000)
	if prefs.FontSize != maxTextSize || noteMaxSize != maxTextSize {
		t.Errorf("max: terminal %v note %v", prefs.FontSize, noteMaxSize)
	}
	a.setFontSize(1)
	if prefs.FontSize != minTextSize || noteMinSize != minTextSize {
		t.Errorf("min: terminal %v note %v", prefs.FontSize, noteMinSize)
	}
}
