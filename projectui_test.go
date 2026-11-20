package main

import (
	"path/filepath"
	"testing"
)

// TestProjectNoteDropped shows another project: a note about the one the
// window left goes; a note of no project stays.
func TestProjectNoteDropped(t *testing.T) {
	a, tt := newTestApp(t)
	first := a.ws
	setProjectHome(t, a, tt, "", filepath.Join(appTempDir(t, a), "gone"))
	a.folderMissing("")
	if a.note() == nil {
		t.Fatal("no note")
	}
	other := a.st.newTestSpace()
	a.showWorkspace(other)
	tt.Frame()
	if a.errText() != "" {
		t.Errorf("the note of %s stays in %s: %q", first.Name, other.Name, a.errText())
	}
	a.err = "something else"
	a.showWorkspace(first)
	tt.Frame()
	if a.errText() != "something else" {
		t.Errorf("an error of no project went: %q", a.errText())
	}
}
