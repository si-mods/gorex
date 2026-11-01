package main

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

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

// noteTitle is the first line of a note that has text, shortened, "" for
// a note of none. It reads no further than that line.
func noteTitle(text string) string {
	for text != "" {
		var line string
		line, text, _ = strings.Cut(text, "\n")
		if line = strings.TrimSpace(line); line == "" {
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
	f, err := os.CreateTemp(noteDir(), n.id+".*.tmp")
	if err != nil {
		log.Printf("note pane: %v", err)
		return
	}
	_, err = f.WriteString(n.text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), n.path())
	}
	if err != nil {
		os.Remove(f.Name())
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
