package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// setProjectHome gives the window's space the home host and folder, as
// the server keeps them, and waits for the app to read them.
func setProjectHome(t *testing.T, a *App, tt *ui.Tester, host, dir string) {
	t.Helper()
	setProjectHomeAs(t, a, tt, host, dir, host)
}

// setProjectHomeAs is setProjectHome of a home the server keeps as want:
// a host's by its machine (layout.MachineHost) once hosts.json knows it.
func setProjectHomeAs(t *testing.T, a *App, tt *ui.Tester, host, dir, want string) {
	t.Helper()
	if res := clientCommand(a, rex.LayoutCommand{Op: "space.home", Space: a.ws.ID, Host: host, Dir: dir}); res.Code != "" {
		t.Fatalf("space.home: %+v", res)
	}
	caughtUp(t, a, tt)
	waitFor(t, tt, "the app to read the home", func() bool { return a.ws.Host == want && (sameDir(a.ws.Dir, dir) || a.ws.Dir == dir) })
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
// this machine's pane; a home given by the host's ID is kept by its
// machine; a folder gone there, a host not among the hosts, and a host
// offline open no tab anywhere.
func TestNewTabInHostProject(t *testing.T) {
	a, tt := newTestApp(t)
	h, proxy, _, remote := remotePaneOf(t, a, tt, t.TempDir())
	if h.Machine == "" {
		t.Fatal("the host has no machine")
	}
	home := layout.MachineHost(h.Machine)
	folder := t.TempDir()
	setProjectHomeAs(t, a, tt, h.ID, folder, home)
	focusLocal(t, a)

	tab := newOnHost(t, a, tt, h)
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

	// The folder gone on the host.
	setProjectHome(t, a, tt, home, filepath.Join(folder, "gone"))
	n, sessions := len(a.tabs), len(sessionInfos(t, remote))
	a.newProjectTab()
	waitFor(t, tt, "the error", func() bool { return strings.Contains(a.err, rex.FolderMissing) })
	if len(a.tabs) != n || len(sessionInfos(t, remote)) != sessions {
		t.Errorf("a tab or a session of a folder gone: %d tabs, %d sessions", len(a.tabs), len(sessionInfos(t, remote)))
	}

	// A machine not among the hosts: the project keeps its home, and New
	// Tab opens none.
	far := layout.MachineHost("machine-far")
	setProjectHome(t, a, tt, far, folder)
	a.err = ""
	if tab := a.newProjectTab(); tab != nil || len(a.tabs) != n || !strings.Contains(a.err, "not one of your hosts") {
		t.Errorf("New Tab of an unknown host: %v, %d tabs, %q", tab, len(a.tabs), a.err)
	}
	if a.ws.Host != far || a.ws.Dir != folder {
		t.Errorf("the project's home is %q %q", a.ws.Host, a.ws.Dir)
	}

	// The host offline.
	setProjectHome(t, a, tt, home, folder)
	proxy.setDown(true)
	waitFor(t, tt, "the host to drop", func() bool { return h.state != hostConnected && h.current() == nil })
	a.err, a.errMsg = "", nil
	if tab := a.newProjectTab(); tab != nil || len(a.tabs) != n || a.errMsg == nil {
		t.Errorf("New Tab of an offline host: %v, %d tabs, a reason %v", tab, len(a.tabs), a.errMsg != nil)
	}
}

// focusLocal gives a tab of this machine the focus.
func focusLocal(t *testing.T, a *App) {
	t.Helper()
	a.active = slices.IndexFunc(a.tabs, func(t *Tab) bool { return t.host() == nil })
	if a.tab() == nil || a.currentHost() != nil {
		t.Fatal("no tab of this machine has the focus")
	}
}

// newOnHost makes New Tab of the project, and returns the tab it opens on
// host h once the window shows it.
func newOnHost(t *testing.T, a *App, tt *ui.Tester, h *hostConn) *Tab {
	t.Helper()
	n := len(a.tabsOn(h))
	a.newProjectTab()
	waitFor(t, tt, "the tab on the home host", func() bool { return len(a.tabsOn(h)) > n })
	return a.tab()
}

// TestProjectHostAddedAgain removes the home host and adds it again under
// a new ID: the project's home, kept by the host's machine, is the host
// again.
func TestProjectHostAddedAgain(t *testing.T) {
	a, tt := newTestApp(t)
	h, proxy, _ := remotePane(t, a, tt, t.TempDir())
	folder := t.TempDir()
	setProjectHomeAs(t, a, tt, h.ID, folder, layout.MachineHost(h.Machine))
	a.removeHost(h)
	a.err = ""
	if tab := a.newProjectTab(); tab != nil || !strings.Contains(a.err, "not one of your hosts") {
		t.Fatalf("New Tab of a removed host: %v, %q", tab, a.err)
	}
	again := a.addHost(savedHost{ID: "h-again", Name: "office-mac", Address: proxy.path})
	waitFor(t, tt, "the host to connect again", func() bool { return again.state == hostConnected && again.Machine != "" })
	focusLocal(t, a)
	if p := newOnHost(t, a, tt, again).Focus; p.host != again || !sameDir(p.startDir, folder) {
		t.Errorf("the project's tab is on %v in %q", p.host, p.startDir)
	}
}

// TestProjectTabWaitsForTailnetHost makes New Tab of a project whose home
// is a tailnet host online but not connected: the tab opens in the folder
// once the host connects, a new one though the window has the host's.
func TestProjectTabWaitsForTailnetHost(t *testing.T) {
	a, tt := newTestApp(t)
	// The host's shells have these directories until it stops: made
	// first, they go after it, as Windows wants.
	first, folder := t.TempDir(), t.TempDir()
	fakeHosts(t)
	sock, _ := startHostServer(t)
	proxy := newHostProxy(t, sock)
	dial := dialHost
	dialHost = func(ctx context.Context, address string) (*rex.Client, error) {
		if rex.IsTailnetTarget(address) {
			address = proxy.path
		}
		return dial(ctx, address)
	}
	t.Cleanup(a.stopHosts)
	h := a.addHost(savedHost{ID: "h1", Name: "peer", Address: "tailscale:peer", TailnetOpened: true})
	waitFor(t, tt, "the host to connect", func() bool { return h.state == hostConnected && h.Machine != "" })
	hostTab(t, a, tt, h, first)
	setProjectHomeAs(t, a, tt, h.ID, folder, layout.MachineHost(h.Machine))

	gate := make(chan struct{})
	dialGate.Store(&gate)
	t.Cleanup(func() { dialGate.Store(nil) })
	proxy.setDown(true)
	waitFor(t, tt, "the host to drop", func() bool { return h.current() == nil })
	proxy.setDown(false)
	h.tailnetOnline = true
	focusLocal(t, a)
	n := len(a.tabsOn(h))
	if n == 0 {
		t.Fatal("the window has no tab of the host")
	}
	if tab := a.newProjectTab(); tab != nil || h.openOnConnect != a || !h.openStrict || h.openDir != folder {
		t.Fatalf("New Tab of a host not connected: %v, pending %v %v %q", tab, h.openOnConnect == a, h.openStrict, h.openDir)
	}
	dialGate.Store(nil)
	close(gate)
	waitFor(t, tt, "the project's tab on the host", func() bool { return len(a.tabsOn(h)) > n })
	if p := a.tab().Focus; p.host != h || !sameDir(p.startDir, folder) {
		t.Errorf("the project's tab is on %v in %q", p.host, p.startDir)
	}
}

// TestProjectTabRefusesOldHost makes New Tab of a project whose home host
// runs a GoRex that does not check folders: it opens none, and says why.
func TestProjectTabRefusesOldHost(t *testing.T) {
	a, tt := newTestApp(t)
	h, _, _ := remotePane(t, a, tt, t.TempDir())
	setProjectHomeAs(t, a, tt, h.ID, t.TempDir(), layout.MachineHost(h.Machine))
	h.hello.Capabilities = slices.DeleteFunc(slices.Clone(h.hello.Capabilities), func(c string) bool { return c == rex.CapabilitySpaceHome })
	focusLocal(t, a)
	n := len(a.tabsOn(h))
	a.err = ""
	if tab := a.newProjectTab(); tab != nil || !strings.Contains(a.err, "older GoRex") {
		t.Fatalf("New Tab of an older host: %v, %q", tab, a.err)
	}
	time.Sleep(300 * time.Millisecond)
	tt.Frame()
	if len(a.tabsOn(h)) != n {
		t.Error("a tab opened on the older host")
	}
}
