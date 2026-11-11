package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

// TestCloseTabClosesFind closes a tab whose pane's find bar is open: the
// bar goes with it.
func TestCloseTabClosesFind(t *testing.T) {
	a, tt := newTestApp(t)
	tab := a.newTab(os.TempDir())
	waitFor(t, tt, "the terminal", func() bool { return tab.Focus.term != nil })
	a.openFind()
	if a.findPane != tab.Focus {
		t.Fatal("the find bar did not open on the focused pane")
	}
	a.closeTab(tab)
	if a.findPane != nil {
		t.Error("the find bar stays on a pane of the closed tab")
	}
}

// TestIsLink: a path printed in a pane of this machine is a link only once
// a stat, on another goroutine, found it there, so that prose such as w/
// or and/or is none; in a pane of a host every path but a directory is.
func TestIsLink(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	found := make(chan func(), 16)
	update := func(fn func()) { found <- fn }
	p := &Pane{info: rex.SessionInfo{Dir: dir}}
	for link, want := range map[string]bool{
		"docs/": true, "docs/:12": true, "./": true, "../": true, "~/": true, "notes.txt:3": true, "docs": true,
		"w/": false, "w/:3": false, "I/": false, "and/or": false, "notes.txt/": false,
	} {
		a := &App{}
		if a.isLink(p, link, update) {
			t.Errorf("%q is a link before a stat found it", link)
		}
		select {
		case fn := <-found:
			fn()
		case <-time.After(10 * time.Second):
			t.Fatalf("no stat of %q", link)
		}
		if got := a.isLink(p, link, update); got != want {
			t.Errorf("isLink(%q) = %v, want %v", link, got, want)
		}
		if len(found) > 0 {
			t.Errorf("%q, known a moment ago, is stat'ed again", link)
		}
	}
	a, host := &App{}, &Pane{info: p.info, host: &hostConn{}}
	for link, want := range map[string]bool{"docs/": false, "docs/:12": false, "w/": false, "and/or": true, "notes.txt": true, "support.github.com/tickets": true, "src.old/main.go": true} {
		if got := a.isLink(host, link, update); got != want {
			t.Errorf("isLink(%q) in a pane of a host = %v, want %v", link, got, want)
		}
	}
	if len(found) > 0 {
		t.Error("a pane of a host stats its paths here")
	}
	if !a.isLink(p, "support.github.com/tickets", update) {
		t.Error("a web address without its scheme is no link before a stat")
	}
}

func TestWebURL(t *testing.T) {
	for link, want := range map[string]string{
		"support.github.com/tickets":  "https://support.github.com/tickets",
		"github.com/si-mods/gorex:12": "https://github.com/si-mods/gorex",
		"src.old/main.go":             "",
		"v1.2/notes":                  "",
		"notes.txt":                   "",
		"github.com":                  "",
		"https://github.com/x":        "",
	} {
		if got, _ := webURL(link); got != want {
			t.Errorf("webURL(%q) = %q, want %q", link, got, want)
		}
	}
}

// TestHoverDirLink hovers, with Command, paths printed in a pane of this
// machine: the terminal asks the pane (Options.LinkPath) which are links,
// a directory there once its stat landed, a missing one never.
func TestHoverDirLink(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	waitFor(t, tt, "the shell", func() bool { return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt) })
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	there := filepath.ToSlash(filepath.Join(dir, "docs")) + "/"
	missing := filepath.ToSlash(filepath.Join(dir, "w")) + "/"
	p.term.Feed([]byte("\x1b[2J\x1b[H" + there + "\r\n" + missing + "\r\n"))
	tt.Frame()
	r, ok := tt.Find("Terminal")
	if !ok {
		t.Fatal("no terminal")
	}
	cols, rows := p.term.Size()
	cell := func(col, row int) (float32, float32) {
		return r.X + (float32(col)+0.5)*r.W/float32(cols), r.Y + (float32(row)+0.5)*r.H/float32(rows)
	}
	tt.Move(cell(3, 0))
	tt.HoldModifiers(ui.Cmd)
	waitFor(t, tt, "the directory a link", func() bool {
		link, path, _ := p.term.HoveredLink()
		return link == there && path
	})
	tt.Move(cell(3, 1))
	for range 10 {
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	if link, _, _ := p.term.HoveredLink(); link != "" {
		t.Errorf("a missing directory is a link: %q", link)
	}
	tt.HoldModifiers(0)
}

func TestPathStatsBounded(t *testing.T) {
	a := &App{}
	for i := range pathStatsMax + 10 {
		a.setPathStat(strconv.Itoa(i), pathStat{there: true})
	}
	if len(a.pathStats) > pathStatsMax {
		t.Errorf("%d paths kept, at most %d", len(a.pathStats), pathStatsMax)
	}
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
}
