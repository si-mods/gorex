package main

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"gorex/internal/rex"
)

// notePane is a pane of free text, a sticky note. Its text lives in a file
// of its own, notes/<id>.txt in GoRex's data directory, written as it
// changes; the layout keeps only the id, so a large note never makes the
// layout too long to save.
type notePane struct {
	id   string
	text string
	// last is the text the file was last written with, to see an edit.
	last string
	// size is the text size in points, 0 for noteDefaultSize, and preview
	// shows the text rendered as Markdown rather than for editing.
	size    float32
	preview bool
	// md is how the preview shows Markdown; it keeps what it loads.
	md       markdown.Options
	theme    *markdown.Theme
	themeKey themeKey
}

// The text size of a note, as a terminal's: noteDefaultSize at first, a
// step at a time between noteMinSize and noteMaxSize.
const (
	noteDefaultSize = 13
	noteMinSize     = minTextSize
	noteMaxSize     = maxTextSize
	noteSizeStep    = 1
)

// textSize is the size the note's text shows in.
func (n *notePane) textSize() float32 {
	if n.size <= 0 {
		return noteDefaultSize
	}
	return n.size
}

// clampNoteSize keeps a saved size in range; 0, the default, stays.
func clampNoteSize(s float32) float32 {
	if s <= 0 || s != s {
		return 0
	}
	return min(max(s, noteMinSize), noteMaxSize)
}

// zoom changes the size by delta points; 0 goes back to the default.
// It reports whether the size changed.
func (n *notePane) zoom(delta float32) bool {
	old := n.size
	if delta == 0 {
		n.size = 0
	} else {
		n.size = clampNoteSize(n.textSize() + delta)
		if n.size == noteDefaultSize {
			n.size = 0
		}
	}
	return n.size != old
}

// focusedNote is the note pane that has the focus, nil if another has.
func (a *App) focusedNote() *notePane {
	if t := a.tab(); t != nil && t.Focus != nil {
		return t.Focus.note
	}
	return nil
}

// zoomNote changes the text size of the focused note, if one has the
// focus, and reports whether it did take the command.
func (a *App) zoomNote(delta float32) bool {
	n := a.focusedNote()
	if n == nil {
		return false
	}
	if n.zoom(delta) {
		a.changed()
	}
	return true
}

// toggleNotePreview switches the note of pane p between editing and its
// Markdown preview, and gives the pane the keyboard.
func (a *App) toggleNotePreview(p *Pane) {
	if p == nil || p.note == nil {
		return
	}
	p.note.preview = !p.note.preview
	a.focusReq = p
	a.changed()
}

// noteMenuItem is the menu's Toggle Note Preview, and noteMenuOn whether
// it is enabled: while it is not, its key goes to the terminal or page.
var (
	noteMenuItem *mygo.MenuItem
	noteMenuOn   = true
)

// syncNoteItem enables the menu's Toggle Note Preview while a note has
// the focus.
func (a *App) syncNoteItem() {
	on := a.focusedNote() != nil
	if noteMenuItem != nil && on != noteMenuOn {
		noteMenuOn = on
		noteMenuItem.SetEnabled(on)
	}
}

// previewTheme is the Markdown theme of the note at its size in the
// colors k, kept until either changes.
func (n *notePane) previewTheme(k *colors) *markdown.Theme {
	key := themeKey{n.textSize(), k.text, k.textMuted, k.textFaint, k.accent, k.cardBorder}
	if n.theme != nil && n.themeKey == key {
		return n.theme
	}
	th := &markdown.Theme{
		FontSize: key.size, Text: k.text, Strong: k.text, Muted: k.textFaint, Link: k.accent,
		Code: k.text, CodeBackground: k.text.Alpha(0.07),
		QuoteBar: k.cardBorder, QuoteText: k.textMuted, Rule: k.cardBorder,
		CodeBlockBackground: k.text.Alpha(0.04), CodeBlockBorder: k.cardBorder,
		TableBorder: k.cardBorder, TableHeader: k.text.Alpha(0.04), Checkbox: k.accent,
	}
	for i := range th.Headings {
		th.Headings[i].Color = k.text
	}
	th.Headings[5].Color = k.textMuted
	n.theme, n.themeKey = th, key
	return th
}

// themeKey is what a note's Markdown theme is made from.
type themeKey struct {
	size                                   float32
	text, textMuted, textFaint, accent, bd ui.Color
}

// noteKind is the Kind of a note pane in the layout.
const noteKind = "note"

// noteTitleMax is how many characters of a note's first line name its pane.
const noteTitleMax = 40

// validNoteID tells whether id is one newNoteID could make: hex, so that it
// is safe in a file name.
func validNoteID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func newNoteID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func noteDir() string { return filepath.Join(rex.Dir(), "notes") }

func (n *notePane) path() string { return filepath.Join(noteDir(), n.id+".txt") }

// stripMarkdownMarker drops a heading marker (one to six # and a space) or
// a list or quote marker ("- ", "* ", "+ ", "> ") from the start of line.
func stripMarkdownMarker(line string) string {
	if h := strings.TrimLeft(line, "#"); len(line)-len(h) <= 6 && len(h) < len(line) && strings.HasPrefix(h, " ") {
		return strings.TrimSpace(h)
	}
	if len(line) > 1 && strings.ContainsRune("-*+>", rune(line[0])) && line[1] == ' ' {
		return strings.TrimSpace(line[2:])
	}
	return line
}

// noteTitle is the first line of a note that has text, shortened, "" for
// a note of none. It reads no further than that line.
func noteTitle(text string) string {
	for text != "" {
		var line string
		line, text, _ = strings.Cut(text, "\n")
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		if line = stripMarkdownMarker(line); line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > noteTitleMax {
			r := []rune(line[:min(len(line), noteTitleMax*utf8.UTFMax)])
			return strings.TrimSpace(string(r[:noteTitleMax])) + "…"
		}
		return line
	}
	return ""
}

// newNotePane makes a note pane of the note with id, whose text is read
// from its file: none, or an id that is no note's, makes a new, empty
// note.
func (a *App) newNotePane(id string) *Pane {
	n := &notePane{id: id}
	if !validNoteID(id) {
		n.id = newNoteID()
	} else if b, err := os.ReadFile(n.path()); err == nil {
		n.text = string(b)
	}
	n.last = n.text
	return &Pane{ID: a.id(), note: n}
}

// save writes the text to the note's file, whole and atomically, when it
// changed.
func (n *notePane) save() {
	if n == nil || n.text == n.last {
		return
	}
	n.last = n.text
	if err := os.MkdirAll(noteDir(), 0o700); err != nil {
		log.Printf("note pane: %v", err)
		return
	}
	if err := writeFileAtomic(n.path(), []byte(n.text), 0o600); err != nil {
		log.Printf("note pane: %v", err)
	}
}

// destroy deletes the note's file, for a pane that is closed for good; a
// pane that moves keeps it.
func (n *notePane) destroy() {
	if n != nil && validNoteID(n.id) {
		os.Remove(n.path())
	}
}
