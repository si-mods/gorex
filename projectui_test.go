package main

import (
	"fmt"
	"path/filepath"
	"testing"
)

// manyProjects adds n projects to the window's store, named p01, p02….
func manyProjects(a *App, n int) []*Workspace {
	var ws []*Workspace
	for i := range n {
		w := a.st.newTestSpace()
		w.Name = fmt.Sprintf("p%02d", i+1)
		w.Color = "blue"
		ws = append(ws, w)
	}
	return ws
}

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
