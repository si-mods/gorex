package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
)

func TestFindBarSearches(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	waitFor(t, tt, "the shell", func() bool { return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt) })
	p.term.Feed([]byte("\r\nalpha beta alpha\r\n"))
	tt.Frame()

	a.openFind()
	tt.Frame()
	if a.findPane != p || !a.findable() {
		t.Fatal("the find bar did not open on the focused pane")
	}
	tt.Type("alpha")
	waitFor(t, tt, "two matches", func() bool { return a.findStatus.Total == 2 })
	if got := a.findCount(); !strings.Contains(got, "2") {
		t.Fatalf("count %q", got)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Key(ui.Shift, ui.KeyEnter)
	a.findNext()
	a.findPrevious()
	tt.Frame()
	if a.findPane != p {
		t.Fatal("Enter closed the find bar")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.findPane != nil || a.findText != "" || a.tab().Focus != p {
		t.Fatalf("after escape: pane %v text %q focus %v", a.findPane != nil, a.findText, a.tab().Focus == p)
	}
	a.findNext()
	tt.Frame()
	if a.findPane != p || a.findText != "alpha" {
		t.Fatalf("next after escape: pane %v text %q", a.findPane != nil, a.findText)
	}
	a.closeFind()
}

func TestPanePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Pane{info: rex.SessionInfo{Dir: dir}}
	if got, ok := panePath(p, "notes.txt:12:3"); !ok || got != path {
		t.Fatalf("relative path %q %v", got, ok)
	}
	if _, ok := panePath(p, "https://example.com"); ok {
		t.Fatal("opened a URL as a path")
	}
	if _, ok := panePath(p, "missing.txt"); ok {
		t.Fatal("opened a missing path")
	}
	if _, ok := panePath(&Pane{host: &hostConn{}, info: rex.SessionInfo{Dir: dir}}, "notes.txt"); ok {
		t.Fatal("resolved a host path locally")
	}
}
