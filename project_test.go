package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
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
	if n := a.note(); n == nil || n.action == nil || !strings.Contains(a.errText(), "gone") {
		t.Errorf("the note is %q, of a button %v", a.errText(), n != nil && n.action != nil)
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
	waitFor(t, tt, "the note", func() bool {
		n := a.note()
		return n != nil && n.action != nil && strings.Contains(a.errText(), "gone")
	})
	if len(a.tabs) != n || len(sessionInfos(t, remote)) != sessions {
		t.Errorf("a tab or a session of a folder gone: %d tabs, %d sessions", len(a.tabs), len(sessionInfos(t, remote)))
	}

	// A machine not among the hosts: the project keeps its home, and New
	// Tab opens none.
	far := layout.MachineHost("machine-far")
	setProjectHome(t, a, tt, far, folder)
	a.err, a.errMsg = "", nil
	if tab := a.newProjectTab(); tab != nil || len(a.tabs) != n || !strings.Contains(a.errText(), "not one of your hosts") {
		t.Errorf("New Tab of an unknown host: %v, %d tabs, %q", tab, len(a.tabs), a.errText())
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
	a.err, a.errMsg = "", nil
	if tab := a.newProjectTab(); tab != nil || !strings.Contains(a.errText(), "not one of your hosts") {
		t.Fatalf("New Tab of a removed host: %v, %q", tab, a.errText())
	}
	again := a.addHost(savedHost{ID: "h-again", Name: "office-mac", Address: proxy.path})
	waitFor(t, tt, "the host to connect again", func() bool { return again.state == hostConnected && again.Machine != "" })
	focusLocal(t, a)
	if p := newOnHost(t, a, tt, again).Focus; p.host != again || !sameDir(p.startDir, folder) {
		t.Errorf("the project's tab is on %v in %q", p.host, p.startDir)
	}
}

// tailnetHost is a host of the tailnet the test serves at a socket of its
// own, behind a proxy: the host, the proxy, a client of its server, and
// two directories made before it, which its shells have till it stops.
func tailnetHost(t *testing.T, a *App) (*hostConn, *hostProxy, *rex.Client, string, string) {
	t.Helper()
	first, folder := t.TempDir(), t.TempDir()
	fakeHosts(t)
	sock, remote := startHostServer(t)
	proxy := newHostProxy(t, sock)
	dial := dialHost
	dialHost = func(ctx context.Context, address string) (*rex.Client, error) {
		if rex.IsTailnetTarget(address) {
			address = proxy.path
		}
		return dial(ctx, address)
	}
	t.Cleanup(a.stopHosts)
	return nil, proxy, remote, first, folder
}

// holdDials holds the hosts' connections until the release it returns.
func holdDials(t *testing.T) (release func()) {
	t.Helper()
	gate := make(chan struct{})
	dialGate.Store(&gate)
	done := false
	release = func() {
		if !done {
			done = true
			dialGate.Store(nil)
			close(gate)
		}
	}
	t.Cleanup(release)
	return release
}

// pendingTailnetProject is a project whose home is a tailnet host that
// dropped, online but not connected, while the window has a tab of it.
func pendingTailnetProject(t *testing.T, a *App, tt *ui.Tester) (h *hostConn, folder string, release func()) {
	t.Helper()
	_, proxy, _, first, folder := tailnetHost(t, a)
	h = a.addHost(savedHost{ID: "h1", Name: "peer", Address: "tailscale:peer", TailnetOpened: true})
	waitFor(t, tt, "the host to connect", func() bool { return h.state == hostConnected && h.Machine != "" })
	hostTab(t, a, tt, h, first)
	setProjectHomeAs(t, a, tt, h.ID, folder, layout.MachineHost(h.Machine))
	release = holdDials(t)
	proxy.setDown(true)
	waitFor(t, tt, "the host to drop", func() bool { return h.current() == nil })
	proxy.setDown(false)
	h.tailnetOnline = true
	focusLocal(t, a)
	if len(a.tabsOn(h)) == 0 {
		t.Fatal("the window has no tab of the host")
	}
	return h, folder, release
}

// TestProjectTabWaitsForTailnetHost makes New Tab of a project whose home
// is a tailnet host online but not connected: the tab opens in the folder
// once the host connects, a new one though the window has the host's.
func TestProjectTabWaitsForTailnetHost(t *testing.T) {
	a, tt := newTestApp(t)
	h, folder, release := pendingTailnetProject(t, a, tt)
	n := len(a.tabsOn(h))
	if tab := a.newProjectTab(); tab != nil || a.projectOpen == nil || a.projectOpen.host != h || h.openOnConnect != nil {
		t.Fatalf("New Tab of a host not connected: %v, pending %+v", tab, a.projectOpen)
	}
	release()
	waitFor(t, tt, "the project's tab on the host", func() bool { return len(a.tabsOn(h)) > n })
	if p := a.tab().Focus; p.host != h || !sameDir(p.startDir, folder) {
		t.Errorf("the project's tab is on %v in %q", p.host, p.startDir)
	}
	if a.projectOpen != nil {
		t.Error("the wait stays")
	}
}

// TestProjectTabNotInAnotherProject makes New Tab of a project whose home
// host is not connected, then shows another project of the same home:
// the wait ends as the window switches, and once the host connects no tab
// opens, in either.
func TestProjectTabNotInAnotherProject(t *testing.T) {
	a, tt := newTestApp(t)
	h, _, release := pendingTailnetProject(t, a, tt)
	project := a.ws
	a.newProjectTab()
	// Another project of the same home: its folder is no reason to open
	// the tab there.
	other := a.st.addWorkspace()
	a.showWorkspace(other)
	if a.ws != other {
		t.Fatal("the window does not show the other project")
	}
	if a.projectOpen != nil {
		t.Error("the wait stays as the window shows another project")
	}
	setProjectHome(t, a, tt, project.Host, project.Dir)
	release()
	waitFor(t, tt, "the host to connect", func() bool { return h.current() != nil })
	time.Sleep(500 * time.Millisecond)
	tt.Frame()
	if len(a.tabsOn(h)) != 0 {
		t.Errorf("the other project has %d tabs of the host", len(a.tabsOn(h)))
	}
	a.showWorkspace(project)
	n := 0
	for _, w := range savedWorkspaces(t, a.client).Sessions {
		if w.ID == project.ID {
			for _, x := range w.Tabs {
				for _, r := range x.Roots() {
					for _, l := range layout.Leaves(r) {
						if l.Host != "" {
							n++
						}
					}
				}
			}
		}
	}
	if n != 1 {
		t.Errorf("the project has %d panes of the host, want the one it had", n)
	}
}

// TestProjectHostLearnedOnConnect makes New Tab of a project whose home
// machine no host is known to be yet: a host added again, whose machine
// the app learns as it connects, and a tailnet host never opened. Each
// connects, and the tab opens there once it has; with no host to try,
// none opens, and the window says the host is not connected.
func TestProjectHostLearnedOnConnect(t *testing.T) {
	for _, tailnet := range []bool{false, true} {
		t.Run(map[bool]string{false: "added", true: "tailnet"}[tailnet], func(t *testing.T) {
			a, tt := newTestApp(t)
			_, proxy, remote, _, folder := tailnetHost(t, a)
			hello, err := remote.Hello()
			if err != nil || hello.Machine == "" {
				t.Fatalf("the host's machine: %+v %v", hello, err)
			}
			release := holdDials(t)
			var h *hostConn
			if tailnet {
				h = newHostConn(savedHost{ID: "h-peer", Name: "peer", Address: "tailscale:peer"})
				a.hosts = append(slices.Clone(a.hosts), h)
				a.syncHosts()
			} else {
				h = a.addHost(savedHost{ID: "h-again", Name: "office-mac", Address: proxy.path})
			}
			setProjectHome(t, a, tt, layout.MachineHost(hello.Machine), folder)
			focusLocal(t, a)

			if tailnet {
				// Not online: nothing to try.
				a.err, a.errMsg = "", nil
				if tab := a.newProjectTab(); tab != nil || a.projectOpen != nil || !strings.Contains(a.errText(), "is not connected") {
					t.Fatalf("New Tab with no host to try: %v, %+v, %q", tab, a.projectOpen, a.errText())
				}
				h.tailnetOnline = true
			}
			a.err, a.errMsg = "", nil
			if tab := a.newProjectTab(); tab != nil || a.projectOpen == nil || !strings.Contains(a.errText(), "Connecting") {
				t.Fatalf("New Tab of a host not known yet: %v, %+v, %q", tab, a.projectOpen, a.errText())
			}
			if h.Machine != "" {
				t.Fatal("the host's machine is known before it connects")
			}
			release()
			waitFor(t, tt, "the project's tab on the host", func() bool { return len(a.tabsOn(h)) > 0 })
			if p := a.tab().Focus; p.host != h || !sameDir(p.startDir, folder) {
				t.Errorf("the project's tab is on %v in %q", p.host, p.startDir)
			}
			if tailnet {
				// Connected to learn its machine, the host has a tab now:
				// it is opened, and reaches it after a restart too.
				if !h.TailnetOpened || h.probing || !savedOpened(t, h.ID) {
					t.Errorf("the home host with a tab: opened %v, probing %v, saved %v", h.TailnetOpened, h.probing, savedOpened(t, h.ID))
				}
			}
		})
	}
}

// TestProjectHomeByIDMigrates loads a layout whose project names its home
// host by the host's ID, as one of before homes named machines: New Tab
// opens on the host, and the home is kept by the host's machine then.
func TestProjectHomeByIDMigrates(t *testing.T) {
	a, tt := newTestApp(t)
	h, _, _ := remotePane(t, a, tt, t.TempDir())
	folder := t.TempDir()
	st := savedWorkspaces(t, a.client)
	w := st.Space(a.ws.ID)
	w.Host, w.Dir = h.ID, folder
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.client.SetLayout(raw); err != nil {
		t.Fatal(err)
	}
	a.st.out.follow(a.client)
	caughtUp(t, a, tt)
	waitFor(t, tt, "the app to read the home", func() bool { return a.ws.Host == h.ID })
	focusLocal(t, a)
	if p := newOnHost(t, a, tt, h).Focus; !sameDir(p.startDir, folder) {
		t.Errorf("the project's tab starts in %q", p.startDir)
	}
	waitFor(t, tt, "the home kept by machine", func() bool {
		st := savedWorkspaces(t, a.client)
		return st.Space(a.ws.ID).Host == layout.MachineHost(h.Machine)
	})
}

// TestProjectHostPrefersConnected names a home machine two hosts are, as
// a host's LAN and tailnet entries: New Tab opens on the connected one.
func TestProjectHostPrefersConnected(t *testing.T) {
	a, tt := newTestApp(t)
	h, _, _ := remotePane(t, a, tt, t.TempDir())
	idle := newHostConn(savedHost{ID: "h0", Name: "office-mac (tailnet)", Address: "tailscale:office", Machine: h.Machine})
	a.hosts = append([]*hostConn{idle}, a.hosts...)
	t.Cleanup(func() { a.hosts = slices.DeleteFunc(a.hosts, func(x *hostConn) bool { return x == idle }) })
	setProjectHome(t, a, tt, layout.MachineHost(h.Machine), t.TempDir())
	if got := a.projectHost(a.ws); got != h {
		t.Fatalf("the home host is %v, want the connected %v", got, h)
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
	a.err, a.errMsg = "", nil
	if tab := a.newProjectTab(); tab != nil || !strings.Contains(a.errText(), "older GoRex") {
		t.Fatalf("New Tab of an older host: %v, %q", tab, a.errText())
	}
	time.Sleep(300 * time.Millisecond)
	tt.Frame()
	if len(a.tabsOn(h)) != n {
		t.Error("a tab opened on the older host")
	}
}

// TestProjectTabHomeMovedMeanwhile makes New Tab of a project whose home
// host is not connected, then moves the home to another machine: once the
// old host connects, no tab opens there, and the window says why.
func TestProjectTabHomeMovedMeanwhile(t *testing.T) {
	a, tt := newTestApp(t)
	h, folder, release := pendingTailnetProject(t, a, tt)
	n := len(a.tabsOn(h))
	a.newProjectTab()
	setProjectHome(t, a, tt, layout.MachineHost("machine-far"), filepath.Join(folder, "elsewhere"))
	a.err, a.errMsg = "", nil
	release()
	waitFor(t, tt, "the note", func() bool { return strings.Contains(a.errText(), "home changed") })
	time.Sleep(300 * time.Millisecond)
	tt.Frame()
	if len(a.tabsOn(h)) != n || a.projectOpen != nil {
		t.Errorf("%d tabs of the old home, %d before; wait %+v", len(a.tabsOn(h)), n, a.projectOpen)
	}
}

// unknownHomeProject is a project whose home is machine, and a host the
// app has not learned the machine of, whose connection is held till the
// release returned.
func unknownHomeProject(t *testing.T, a *App, tt *ui.Tester, machine string) (h *hostConn, proxy *hostProxy, release func()) {
	t.Helper()
	_, proxy, remote, _, folder := tailnetHost(t, a)
	if machine == "" {
		hello, err := remote.Hello()
		if err != nil {
			t.Fatal(err)
		}
		machine = hello.Machine
	}
	release = holdDials(t)
	h = a.addHost(savedHost{ID: "h-again", Name: "office-mac", Address: proxy.path})
	setProjectHome(t, a, tt, layout.MachineHost(machine), folder)
	focusLocal(t, a)
	return h, proxy, release
}

// TestProjectWaitEndsWhenNoHostIsHome makes New Tab of a project whose
// home machine is none of the hosts: once the host of unknown machine
// connects and is another machine, the wait ends, with a note, and no tab
// opens.
func TestProjectWaitEndsWhenNoHostIsHome(t *testing.T) {
	a, tt := newTestApp(t)
	h, _, release := unknownHomeProject(t, a, tt, "machine-elsewhere")
	if a.newProjectTab(); a.projectOpen == nil {
		t.Fatalf("no wait: %q", a.errText())
	}
	a.err, a.errMsg = "", nil
	release()
	waitFor(t, tt, "the note", func() bool { return strings.Contains(a.errText(), "not one of your hosts") })
	if a.projectOpen != nil || len(a.tabsOn(h)) != 0 {
		t.Errorf("wait %+v, %d tabs of the host", a.projectOpen, len(a.tabsOn(h)))
	}
}

// TestProjectWaitTimesOut makes New Tab of a project whose home host does
// not connect: the wait ends after projectWait, with a note. A New Tab
// again replaces the wait, whose time starts anew.
func TestProjectWaitTimesOut(t *testing.T) {
	old := projectWait
	projectWait = 400 * time.Millisecond
	t.Cleanup(func() { projectWait = old })
	a, tt := newTestApp(t)
	// The host does not answer: its connections fail, rather than wait
	// at a gate a cleanup opens after the test.
	h, proxy, release := unknownHomeProject(t, a, tt, "")
	proxy.setDown(true)
	release()
	a.newProjectTab()
	first := a.projectOpen
	time.Sleep(250 * time.Millisecond)
	a.newProjectTab()
	second := a.projectOpen
	if first == nil || second == nil || second == first {
		t.Fatalf("the waits %p %p: %q", first, second, a.errText())
	}
	a.err, a.errMsg = "", nil
	time.Sleep(250 * time.Millisecond)
	tt.Frame()
	if a.projectOpen != second {
		t.Fatal("the first wait's time ended the second")
	}
	waitFor(t, tt, "the note", func() bool { return strings.Contains(a.errText(), "did not connect") })
	if a.projectOpen != nil || len(a.tabsOn(h)) != 0 {
		t.Errorf("wait %+v, %d tabs of the host", a.projectOpen, len(a.tabsOn(h)))
	}
}

// savedOpened tells whether hosts.json keeps the host of id opened.
func savedOpened(t *testing.T, id string) bool {
	t.Helper()
	hs, err := loadHosts()
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(hs, func(s savedHost) bool { return s.ID == id })
	return i >= 0 && hs[i].TailnetOpened
}

// probedHost is a host of the tailnet, online, never opened, of unknown
// machine, in a project whose home is machine: its connections wait till
// the release returned, and dials counts them.
func probedHost(t *testing.T, a *App, tt *ui.Tester, machine string) (h *hostConn, dials *atomic.Int32, release func()) {
	t.Helper()
	tailnetHost(t, a)
	dials = new(atomic.Int32)
	dial := dialHost
	dialHost = func(ctx context.Context, address string) (*rex.Client, error) {
		if rex.IsTailnetTarget(address) {
			dials.Add(1)
		}
		return dial(ctx, address)
	}
	release = holdDials(t)
	h = newHostConn(savedHost{ID: "tailscale:peer", Name: "peer", Address: "tailscale:peer"})
	h.tailnetOnline = true
	a.hosts = append(slices.Clone(a.hosts), h)
	a.syncHosts()
	setProjectHome(t, a, tt, layout.MachineHost(machine), t.TempDir())
	focusLocal(t, a)
	return h, dials, release
}

// TestProbeNotHomeDisconnects connects a host of the tailnet to learn its
// machine, which is not the project's home: the connection stops, and the
// host stays in the list, not opened, as before.
func TestProbeNotHomeDisconnects(t *testing.T) {
	a, tt := newTestApp(t)
	h, _, release := probedHost(t, a, tt, "machine-elsewhere")
	a.newProjectTab()
	if !h.probing || !h.running {
		t.Fatalf("the host is not probed: probing %v, running %v", h.probing, h.running)
	}
	release()
	waitFor(t, tt, "the note", func() bool { return strings.Contains(a.errText(), "not one of your hosts") })
	now := a.hostByID(h.ID)
	if now == nil || now == h || now.current() != nil || now.running || now.TailnetOpened || h.current() != nil {
		t.Fatalf("after the probe: %+v, old connected %v", now, h.current() != nil)
	}
	if now.Machine == "" || savedOpened(t, h.ID) {
		t.Errorf("the host's machine %q; opened in hosts.json %v", now.Machine, savedOpened(t, h.ID))
	}
	time.Sleep(300 * time.Millisecond)
	tt.Frame()
	if now.current() != nil || len(a.tabsOn(now)) != 0 {
		t.Error("the host connected again")
	}
}

// TestProbeThenUserTabOpens opens a tab on a host that is being connected
// only to learn its machine: the host is opened and saved so, stays
// connected, and shows the tab.
func TestProbeThenUserTabOpens(t *testing.T) {
	a, tt := newTestApp(t)
	h, _, release := probedHost(t, a, tt, "machine-elsewhere")
	a.newProjectTab()
	a.newTabOn(h, "")
	if h.probing || !h.TailnetOpened || !savedOpened(t, h.ID) {
		t.Fatalf("the host with a tab asked: probing %v, opened %v, saved %v", h.probing, h.TailnetOpened, savedOpened(t, h.ID))
	}
	release()
	waitFor(t, tt, "the user's tab on the host", func() bool { return len(a.tabsOn(h)) > 0 })
	time.Sleep(300 * time.Millisecond)
	tt.Frame()
	if a.hostByID(h.ID) != h || h.current() == nil {
		t.Error("the opened host was disconnected")
	}
}

// TestProbeStartsOnce makes New Tab twice while the host of unknown
// machine is still connecting: it is dialed once, not by two loops.
func TestProbeStartsOnce(t *testing.T) {
	a, tt := newTestApp(t)
	_, dials, _ := probedHost(t, a, tt, "machine-elsewhere")
	a.newProjectTab()
	a.newProjectTab()
	time.Sleep(300 * time.Millisecond)
	if n := dials.Load(); n != 1 {
		t.Errorf("the host is dialed %d times, want 1", n)
	}
}
