package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// setProjectHome gives the window's space the home host and folder, as
// the server keeps them, and waits for the app to read them.
func setProjectHome(t *testing.T, a *App, tt *ui.Tester, host, dir string) {
	t.Helper()
	if res := clientCommand(a, rex.LayoutCommand{Op: "space.home", Space: a.ws.ID, Host: host, Dir: dir}); res.Code != "" {
		t.Fatalf("space.home: %+v", res)
	}
	caughtUp(t, a, tt)
	waitFor(t, tt, "the app to read the home", func() bool { return host == "" && (sameDir(a.ws.Dir, dir) || a.ws.Dir == dir) })
}

// sameDir tells two paths of one directory, as a process resolves its own.
func sameDir(a, b string) bool {
	a, _ = filepath.EvalSymlinks(a)
	b, _ = filepath.EvalSymlinks(b)
	return a != "" && a == b
}

// localDirOf is the directory the server started the pane of session sid
// in, as its layout keeps it.
func localDirOf(t *testing.T, a *App, sid string) string {
	t.Helper()
	for _, w := range savedWorkspaces(t, a.client).Sessions {
		for _, tab := range w.Tabs {
			if n := layout.Find(tab.Root, sid); n != nil {
				return n.Dir
			}
		}
	}
	return ""
}

// TestNewTabInLocalProject makes New Tab in a project whose home is this
// machine: it starts in the folder, whichever pane has the focus; a
// directory given wins; a split starts where its pane started; a folder
// gone opens no tab, says why, and starts no shell.
func TestNewTabInLocalProject(t *testing.T) {
	a, tt := newTestApp(t)
	folder, other := appTempDir(t, a), appTempDir(t, a)
	setProjectHome(t, a, tt, "", folder)

	tab := a.newProjectTab()
	if tab == nil || !sameDir(localDirOf(t, a, tab.Focus.SID), folder) {
		t.Fatalf("New Tab of the project starts in %q, want %q", localDirOf(t, a, tab.Focus.SID), folder)
	}
	// The command with a dir, as Lua and the CLI give one: it wins.
	if err := cmdNewTab.RunArgs(a, map[string]any{"dir": other}); err != nil {
		t.Fatal(err)
	}
	given := a.tab()
	if given == tab || !sameDir(localDirOf(t, a, given.Focus.SID), other) {
		t.Fatalf("New Tab of a dir starts in %q, want %q", localDirOf(t, a, given.Focus.SID), other)
	}
	// A split of it starts where its pane started, not in the folder.
	a.split(false)
	waitFor(t, tt, "the split", func() bool { return len(given.panes()) == 2 })
	if p := given.Focus; !sameDir(localDirOf(t, a, p.SID), other) {
		t.Errorf("the split starts in %q, want %q", localDirOf(t, a, p.SID), other)
	}
	// Without a dir, the command is New Tab of the project.
	cmdNewTab.RunArgs(a, map[string]any{})
	if p := a.tab().Focus; a.tab() == given || !sameDir(localDirOf(t, a, p.SID), folder) {
		t.Errorf("the command of no dir starts in %q, want %q", localDirOf(t, a, p.SID), folder)
	}

	gone := filepath.Join(folder, "gone")
	setProjectHome(t, a, tt, "", gone)
	tabs, sessions := len(a.tabs), len(sessionIDs(t, a.client))
	if tab := a.newProjectTab(); tab != nil {
		t.Errorf("New Tab in a folder gone opened %+v", tab)
	}
	if n := a.note(); n == nil || n.action == nil || !strings.Contains(a.errText(), "gone") {
		t.Errorf("the note is %q, of a button %v", a.errText(), n != nil && n.action != nil)
	}
	if len(a.tabs) != tabs || len(sessionIDs(t, a.client)) != sessions {
		t.Errorf("%d tabs and %d sessions after, %d and %d before", len(a.tabs), len(sessionIDs(t, a.client)), tabs, sessions)
	}
}

// focusLocal gives a tab of this machine the focus.
func focusLocal(t *testing.T, a *App) {
	t.Helper()
	// A tab of this machine's is in one of its workspaces
	// (docs/projects.md): the window shows its tabs with no project.
	if a.ws != nil && a.ws.host != nil {
		a.showWorkspace(a.st.addWorkspace())
	}
	a.active = slices.IndexFunc(a.tabs, func(t *Tab) bool { return t.host() == nil })
	if a.tab() == nil || a.currentHost() != nil {
		t.Fatal("no tab of this machine has the focus")
	}
}
