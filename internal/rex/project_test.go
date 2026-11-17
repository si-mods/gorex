//go:build darwin || linux || windows

package rex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorex/internal/layout"
)

// sessionCount is how many sessions the server runs.
func sessionCount(t *testing.T, c *Client) int {
	t.Helper()
	infos, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	return len(infos)
}

// TestSpaceHome sets, checks and clears a space's home.
func TestSpaceHome(t *testing.T) {
	_, c := applyFixture(t)
	folder := t.TempDir()
	work := command(t, c, LayoutCommand{Op: "space.create", Name: "work"}).Space
	command(t, c, LayoutCommand{Op: "space.home", Space: work, Dir: folder + string(filepath.Separator)})
	if w := spaceOf(t, c, work); w.Host != "" || w.Dir != filepath.Clean(folder) {
		t.Fatalf("the home of this machine is %q %q", w.Host, w.Dir)
	}
	// Another host's folder is kept as given, a Windows one on any system.
	command(t, c, LayoutCommand{Op: "space.home", Space: work, Host: "h1", Dir: `C:\code\app`})
	if w := spaceOf(t, c, work); w.Host != "h1" || w.Dir != `C:\code\app` {
		t.Fatalf("the home of a host is %q %q", w.Host, w.Dir)
	}
	// A rename keeps the home.
	command(t, c, LayoutCommand{Op: "space.rename", Space: work, Name: "renamed", Color: "blue"})
	if w := spaceOf(t, c, work); w.Host != "h1" || w.Dir != `C:\code\app` {
		t.Fatalf("a rename changed the home to %q %q", w.Host, w.Dir)
	}
	for _, x := range []LayoutCommand{
		{Op: "space.home", Space: work, Dir: "relative/dir"},
		{Op: "space.home", Space: "none", Dir: folder},
	} {
		if res, _ := c.Command(x); res.Code == "" {
			t.Errorf("space.home of %q in %q: %+v", x.Dir, x.Space, res)
		}
	}
	command(t, c, LayoutCommand{Op: "space.home", Space: work, Host: "h1"})
	if w := spaceOf(t, c, work); w.Host != "" || w.Dir != "" {
		t.Fatalf("a cleared home is %q %q", w.Host, w.Dir)
	}
}

// TestNewTabInProject makes tabs in a project whose home is this machine:
// one of no directory starts in the folder, after a pane elsewhere too; a
// directory given wins; a split starts where its pane started, not in the
// folder (decision 3).
func TestNewTabInProject(t *testing.T) {
	_, c := applyFixture(t)
	cmd, other := testShell()
	folder := t.TempDir()
	work := command(t, c, LayoutCommand{Op: "space.create", Name: "work"}).Space
	command(t, c, LayoutCommand{Op: "space.home", Space: work, Dir: folder})

	dirOf := func(tab string) string {
		for _, x := range spaceOf(t, c, work).Tabs {
			if x.ID == tab {
				return x.Root.Dir
			}
		}
		return ""
	}
	given := command(t, c, LayoutCommand{Op: "tab.create", Space: work, Dir: other, Command: cmd})
	if dir := dirOf(given.Tab); !sameDir(dir, other) {
		t.Errorf("a tab of a directory given starts in %q, want %q", dir, other)
	}
	tab := command(t, c, LayoutCommand{Op: "tab.create", Space: work, After: given.New, Command: cmd})
	if dir := dirOf(tab.Tab); !sameDir(dir, folder) {
		t.Errorf("a new tab of the project starts in %q, want %q", dir, folder)
	}

	split := command(t, c, LayoutCommand{Op: "pane.split", SID: given.New, Command: cmd})
	if n := layout.Find(spaceOf(t, c, work).Tabs[0].Root, split.New); n == nil || !sameDir(n.Dir, other) {
		t.Errorf("the split starts in %+v, want %q", n, other)
	}

	// A project whose home is another host: a tab made here starts as
	// before, the app's to reach the host.
	far := command(t, c, LayoutCommand{Op: "space.create", Name: "far"}).Space
	command(t, c, LayoutCommand{Op: "space.home", Space: far, Host: "h1", Dir: filepath.Join(folder, "nowhere")})
	if res, err := c.Command(LayoutCommand{Op: "tab.create", Space: far, Command: cmd}); err != nil || res.Code != "" {
		t.Errorf("a tab of a project of another host: %+v %v", res, err)
	}
}

// TestNewTabFolderMissing makes a tab in a project whose folder is gone:
// it fails with FolderMissing, starts no shell and adds no tab.
func TestNewTabFolderMissing(t *testing.T) {
	_, c := applyFixture(t)
	cmd, _ := testShell()
	gone := filepath.Join(t.TempDir(), "gone")
	work := command(t, c, LayoutCommand{Op: "space.create", Name: "work"}).Space
	command(t, c, LayoutCommand{Op: "space.home", Space: work, Dir: gone})
	before := sessionCount(t, c)
	res, err := c.Command(LayoutCommand{Op: "tab.create", Space: work, Command: cmd})
	if err != nil || res.Code != FolderMissing || !strings.Contains(res.Error, gone) {
		t.Fatalf("a tab in a folder gone: %+v %v", res, err)
	}
	if n := sessionCount(t, c); n != before {
		t.Errorf("%d sessions after the failed tab, %d before", n, before)
	}
	if w := spaceOf(t, c, work); len(w.Tabs) != 0 {
		t.Errorf("the project has tabs %+v", w.Tabs)
	}
	// A file in the folder's place is no folder either.
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, []byte("x"), 0o600)
	command(t, c, LayoutCommand{Op: "space.home", Space: work, Dir: file})
	if res, _ := c.Command(LayoutCommand{Op: "tab.create", Space: work, Command: cmd}); res.Code != FolderMissing {
		t.Errorf("a tab in a file: %+v", res)
	}
}

// TestCreateDirStrict creates a session of a strict directory gone, as the
// app asks a host for a tab of a project there: it fails and starts none.
func TestCreateDirStrict(t *testing.T) {
	_, c := applyFixture(t)
	cmd, _ := testShell()
	gone := filepath.Join(t.TempDir(), "gone")
	before := sessionCount(t, c)
	if _, err := c.Create(CreateOptions{Command: cmd, Dir: gone, DirStrict: true}); err == nil || !strings.Contains(err.Error(), FolderMissing) {
		t.Fatalf("a strict create of a directory gone: %v", err)
	}
	if n := sessionCount(t, c); n != before {
		t.Errorf("%d sessions after the failed create, %d before", n, before)
	}
	// Without DirStrict it starts at home, as before.
	in, err := c.Create(CreateOptions{Command: cmd, Dir: gone})
	if err != nil {
		t.Fatal(err)
	}
	c.Kill(in.ID)
}

// TestRestoreInProjectFallsBack starts again the lost session of a project
// whose folder is gone, after a restart: it starts at home, as every
// session of the layout does, and keeps its tab.
func TestRestoreInProjectFallsBack(t *testing.T) {
	s, c := applyFixture(t)
	gone := filepath.Join(t.TempDir(), "gone")
	t.Cleanup(func() {
		if ss := s.sessions["lost"]; ss != nil {
			ss.kill()
		}
	})
	st := layout.Store{Version: layout.Version, Windows: []string{"w"}, Sessions: []layout.Workspace{{
		ID: "w", Name: "work", Dir: gone,
		Layout: layout.Layout{Tabs: []layout.Tab{{ID: "a", Root: &layout.Node{SID: "lost", Dir: gone}}}},
	}}}
	raw, _ := json.Marshal(st)
	if err := c.SetLayout(raw); err != nil {
		t.Fatal(err)
	}
	s.restoreLost(nil, s.lostPanes())
	home, _ := os.UserHomeDir()
	if in, ok := liveSession(t, c, "lost"); !ok || !sameDir(s.sessions["lost"].dir, home) {
		t.Fatalf("the restored session is %+v, %v", in, ok)
	}
	if w := spaceOf(t, c, "w"); w == nil || len(w.Tabs) != 1 || w.Dir != gone {
		t.Errorf("the project after the restore: %+v", w)
	}
}

// spaceOf reads the space of id the server keeps, nil for none.
func spaceOf(t *testing.T, c *Client, id string) *layout.Workspace {
	t.Helper()
	st := storeOf(t, c)
	return st.Space(id)
}
