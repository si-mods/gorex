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
	waitFor(t, tt, "the app to read the home", func() bool { return a.ws.Host == host && (sameDir(a.ws.Dir, dir) || a.ws.Dir == dir) })
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
	if !strings.Contains(a.err, rex.FolderMissing) || !strings.Contains(a.err, "gone") {
		t.Errorf("the error is %q", a.err)
	}
	if len(a.tabs) != tabs || len(sessionIDs(t, a.client)) != sessions {
		t.Errorf("%d tabs and %d sessions after, %d and %d before", len(a.tabs), len(sessionIDs(t, a.client)), tabs, sessions)
	}
}

// TestNewTabInHostProject makes New Tab in a project whose home is a
// host: the tab starts on the host in the folder though the focus is on
// this machine's pane; the host named by its machine is the same; a
// folder gone there, a host not among the hosts, and a host offline open
// no tab anywhere.
func TestNewTabInHostProject(t *testing.T) {
	a, tt := newTestApp(t)
	h, proxy, _, remote := remotePaneOf(t, a, tt, t.TempDir())
	folder := t.TempDir()
	setProjectHome(t, a, tt, h.ID, folder)
	a.active = slices.IndexFunc(a.tabs, func(t *Tab) bool { return t.host() == nil })
	if a.tab() == nil || a.currentHost() != nil {
		t.Fatal("no tab of this machine has the focus")
	}

	newOnHost := func() *Tab {
		t.Helper()
		n := len(a.tabsOn(h))
		a.newProjectTab()
		waitFor(t, tt, "the tab on the home host", func() bool { return len(a.tabsOn(h)) > n })
		return a.tab()
	}
	tab := newOnHost()
	if p := tab.Focus; p.host != h || !sameDir(p.startDir, folder) {
		t.Fatalf("the project's tab is on %v in %q", p.host, p.startDir)
	}
	waitFor(t, tt, "the shell in the folder", func() bool {
		for _, in := range sessionInfos(t, remote) {
			if in.ID == tab.Focus.SID {
				return sameDir(in.Dir, folder)
			}
		}
		return false
	})

	// The home named by the host's machine, as another device sets it.
	if h.Machine == "" {
		t.Fatal("the host has no machine")
	}
	setProjectHome(t, a, tt, layout.MachineHost(h.Machine), folder)
	newOnHost()

	// The folder gone on the host.
	setProjectHome(t, a, tt, h.ID, filepath.Join(folder, "gone"))
	n, sessions := len(a.tabs), len(sessionInfos(t, remote))
	a.newProjectTab()
	waitFor(t, tt, "the error", func() bool { return strings.Contains(a.err, rex.FolderMissing) })
	if len(a.tabs) != n || len(sessionInfos(t, remote)) != sessions {
		t.Errorf("a tab or a session of a folder gone: %d tabs, %d sessions", len(a.tabs), len(sessionInfos(t, remote)))
	}

	// A host removed from hosts.json: the project keeps its home, and New
	// Tab opens none.
	setProjectHome(t, a, tt, "h-removed", folder)
	a.err = ""
	if tab := a.newProjectTab(); tab != nil || len(a.tabs) != n || !strings.Contains(a.err, "not one of your hosts") {
		t.Errorf("New Tab of an unknown host: %v, %d tabs, %q", tab, len(a.tabs), a.err)
	}
	if a.ws.Host != "h-removed" || a.ws.Dir != folder {
		t.Errorf("the project's home is %q %q", a.ws.Host, a.ws.Dir)
	}

	// The host offline.
	setProjectHome(t, a, tt, h.ID, folder)
	proxy.setDown(true)
	waitFor(t, tt, "the host to drop", func() bool { return h.state != hostConnected && h.current() == nil })
	a.err, a.errMsg = "", nil
	if tab := a.newProjectTab(); tab != nil || len(a.tabs) != n || a.errMsg == nil {
		t.Errorf("New Tab of an offline host: %v, %d tabs, a reason %v", tab, len(a.tabs), a.errMsg != nil)
	}
}
