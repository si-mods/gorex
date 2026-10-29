package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// dialGate, when set, holds the hosts' connections until it closes.
var dialGate atomic.Pointer[chan struct{}]

// fakeHosts serves hosts on Unix sockets, in place of tailcat addresses:
// an address is a socket's path.
func fakeHosts(t *testing.T) {
	t.Helper()
	oldDial, oldValid, oldPoll := dialHost, validAddress, pairPoll
	dialHost = func(ctx context.Context, address string) (*rex.Client, error) {
		if g := dialGate.Load(); g != nil {
			<-*g
		}
		return rex.ConnectSocketKeepalive(address)
	}
	validAddress = func(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), "/") }
	pairPoll = 50 * time.Millisecond
	t.Cleanup(func() { dialHost, validAddress, pairPoll = oldDial, oldValid, oldPoll })
}

// startHostServer starts a second session server, as a remote host's, and
// returns its socket and a client of it.
func startHostServer(t *testing.T) (string, *rex.Client) {
	t.Helper()
	dir := testDir(t)
	old := os.Getenv("GOREX_DIR")
	os.Setenv("GOREX_DIR", dir)
	sock := rex.SocketPath()
	go rex.Serve()
	// The server reads its directory as it starts: it has once it answers.
	var c *rex.Client
	for i := 0; ; i++ {
		var err error
		if c, err = rex.ConnectSocket(sock); err == nil {
			if _, err = c.Hello(); err == nil {
				break
			}
			c.Close()
		}
		if i == 200 {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	os.Setenv("GOREX_DIR", old)
	t.Cleanup(func() {
		c.Shutdown()
		time.Sleep(100 * time.Millisecond)
		os.RemoveAll(dir)
	})
	return sock, c
}

// hostProxy forwards the connections of a socket of its own to a host's,
// as a network between them that drops: down closes them all and refuses
// new ones until up. While gate is set, new connections wait for it to
// close, as over a slow network; held counts those waiting. blackhole is
// a network gone without a word: nothing passes and nothing closes, new
// connections are accepted and dead too, and the flows of then stay dead
// once it heals, as a NAT that forgot them.
type hostProxy struct {
	path, to string
	ln       net.Listener
	mu       sync.Mutex
	conns    []net.Conn
	down     bool
	out      bool
	gen      int // of the network: a flow of an older one is dead
	gate     chan struct{}
	held     atomic.Int32
}

func (p *hostProxy) blackhole() {
	p.mu.Lock()
	p.out = true
	p.gen++
	p.mu.Unlock()
}

func (p *hostProxy) heal() {
	p.mu.Lock()
	p.out = false
	p.mu.Unlock()
}

// alive tells whether a flow of network gen still passes.
func (p *hostProxy) alive(gen int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.out && p.gen == gen
}

// pump copies src to dst while the flow of network gen passes; it closes
// dst as src closes, but once the flow is dead it passes nothing.
func (p *hostProxy) pump(gen int, dst, src net.Conn) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		alive := p.alive(gen)
		if n > 0 && alive {
			dst.Write(buf[:n])
		}
		if err != nil {
			if alive {
				dst.Close()
			}
			return
		}
	}
}

func newHostProxy(t *testing.T, to string) *hostProxy {
	t.Helper()
	dir := testDir(t)
	t.Cleanup(func() { os.RemoveAll(dir) })
	p := &hostProxy{path: filepath.Join(dir, "p.sock"), to: to}
	ln, err := net.Listen("unix", p.path)
	if err != nil {
		t.Fatal(err)
	}
	p.ln = ln
	t.Cleanup(func() { ln.Close(); p.setDown(true) })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			down, gate, out, gen := p.down, p.gate, p.out, p.gen
			if !down {
				p.conns = append(p.conns, c)
			}
			p.mu.Unlock()
			if down {
				c.Close()
				continue
			}
			if out { // unanswered: it reads what comes, until closed
				go io.Copy(io.Discard, c)
				continue
			}
			go func() {
				if gate != nil {
					p.held.Add(1)
					<-gate
					p.held.Add(-1)
				}
				s, err := net.Dial("unix", to)
				if err != nil {
					c.Close()
					return
				}
				p.mu.Lock()
				if p.down { // went down while it waited
					p.mu.Unlock()
					c.Close()
					s.Close()
					return
				}
				p.conns = append(p.conns, s)
				p.mu.Unlock()
				go p.pump(gen, s, c)
				p.pump(gen, c, s)
			}()
		}
	}()
	return p
}

func (p *hostProxy) setDown(down bool) {
	p.mu.Lock()
	p.down = down
	conns := p.conns
	if down {
		p.conns = nil
	}
	p.mu.Unlock()
	if down {
		for _, c := range conns {
			c.Close()
		}
	}
}

// sessionInfos lists the sessions of a server.
func sessionInfos(t *testing.T, c *rex.Client) []rex.SessionInfo {
	t.Helper()
	infos, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	return infos
}

// sessionIDs lists the sessions a server runs.
func sessionIDs(t *testing.T, c *rex.Client) []string {
	t.Helper()
	infos, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, in := range infos {
		if !in.Exited {
			ids = append(ids, in.ID)
		}
	}
	return ids
}

func TestValidateHost(t *testing.T) {
	fakeHosts(t)
	hosts := []*hostConn{newHostConn(savedHost{ID: "1", Name: "office-mac", Address: "/tmp/a.sock"})}
	for _, tc := range []struct{ address, name, pairAs, want string }{
		{"/tmp/b.sock", "studio", "laptop", ""},
		{"/tmp/b.sock", " ", "laptop", "Enter a host name."},
		{"", "studio", "laptop", "Paste the host's address."},
		{"tcXYZ", "studio", "laptop", "not a GoRex remote address"},
		{"/tmp/b.sock", "studio", "", "Enter a name for this machine."},
		{" /tmp/a.sock ", "studio", "laptop", "That remote host is already saved."},
		{"/tmp/b.sock", "Office-Mac", "laptop", "has that name already"},
	} {
		wantMessage(t, "validateHost("+tc.address+", "+tc.name+")", validateHost(tc.address, tc.name, tc.pairAs, hosts), tc.want)
	}
	for _, s := range []string{"", "  ", "/tmp/a.sock", "tcXYZ"} {
		if rex.ValidRemoteAddress(s) {
			t.Errorf("%q passes for a tailcat address", s)
		}
	}
}

func TestHostsFile(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	if hs, err := loadHosts(); err != nil || len(hs) != 0 {
		t.Fatalf("no file: %v, %v", hs, err)
	}
	want := []savedHost{{ID: "a1", Name: "office-mac", Address: "tcSECRET"}, {ID: "b2", Name: "studio", Address: "tcOTHER"}}
	if err := saveHosts(want); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(hostsPath())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("hosts.json has mode %v", st.Mode().Perm())
	}
	got, err := loadHosts()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("read back %v, %v", got, err)
	}
	// The address is not in the settings.
	if b, _ := os.ReadFile(settingsPath()); strings.Contains(string(b), "tcSECRET") {
		t.Error("the settings hold an address")
	}

	// A file that does not parse is not written over: reading it moves it
	// aside, readable by the user only, and the next save starts anew.
	bad := `{"hosts": [{"id": "a1", "address": "tcSECRET"`
	os.WriteFile(hostsPath(), []byte(bad), 0o644)
	if err := saveHosts(want); err == nil {
		t.Error("saved over a file that does not parse")
	}
	if b, _ := os.ReadFile(hostsPath()); string(b) != bad {
		t.Errorf("the file became %q", b)
	}
	if hs, err := loadHosts(); err == nil || len(hs) != 0 || !strings.Contains(err.Error(), "moved aside") || strings.Contains(err.Error(), "tcSECRET") {
		t.Errorf("a bad file: %v, %v", hs, err)
	}
	aside, _ := filepath.Glob(hostsPath() + ".bad-*")
	if len(aside) != 1 {
		t.Fatalf("set aside: %v", aside)
	}
	if b, _ := os.ReadFile(aside[0]); string(b) != bad {
		t.Errorf("the file set aside holds %q", b)
	}
	if st, _ := os.Stat(aside[0]); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("the file set aside has mode %v", st.Mode().Perm())
	}
	if _, err := os.Stat(hostsPath()); err == nil {
		t.Error("hosts.json stays")
	}
	if err := saveHosts(want); err != nil {
		t.Errorf("saving after: %v", err)
	}
}

func TestHostBackoff(t *testing.T) {
	for n, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 5: 16 * time.Second, 6: 30 * time.Second, 40: 30 * time.Second} {
		if got := hostBackoff(n); got != want {
			t.Errorf("hostBackoff(%d) = %v, want %v", n, got, want)
		}
	}
}

// TestRemoteHost runs tabs on a second server as a remote host: grouped
// in the sidebar, created and killed there, kept in the layout, through a
// connection that drops and comes back, and removed.
func TestRemoteHost(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	a, tt := newTestApp(t)
	fakeHosts(t)
	sock, remote := startHostServer(t)
	proxy := newHostProxy(t, sock)
	t.Cleanup(a.stopHosts)
	h := a.addHost(savedHost{ID: "h1", Name: "office-mac", Address: proxy.path})
	if b, err := os.ReadFile(hostsPath()); err != nil || !strings.Contains(string(b), proxy.path) {
		t.Fatalf("hosts.json: %s, %v", b, err)
	}
	waitFor(t, tt, "the host to connect", func() bool { return h.state == hostConnected })

	// A tab on the host, from the active local tab, goes last, under the
	// host's name in the sidebar.
	local := a.tab()
	a.selectTab(0)
	rt := a.newTabOn(h, "")
	if rt == nil || a.tab() != rt || a.tabs[len(a.tabs)-1] != rt || rt.host() != h {
		t.Fatalf("the remote tab is %v, active %d", rt, a.active)
	}
	p := rt.Focus
	if !slices.Contains(sessionIDs(t, remote), p.SID) || slices.Contains(sessionIDs(t, a.client), p.SID) {
		t.Fatalf("session %s is not the host's alone", p.SID)
	}
	a.selectTab(0)
	a.newTab(os.TempDir()) // local, as the active tab
	if a.tabs[1].host() != nil || a.tabs[2] != rt {
		t.Error("a local tab went among the host's")
	}
	prefs = settings{TabPlacement: "left"}
	tt.Frame()
	mine, ok1 := tt.Find(thisMachine())
	theirs, ok2 := tt.Find("office-mac")
	if !ok1 || !ok2 || mine.Y >= theirs.Y {
		t.Errorf("host sections: %v %v, %v %v; texts %q", mine, ok1, theirs, ok2, tt.Texts())
	}

	// The remote shell runs what is typed; a new tab and a split from it go
	// to the host, and closing kills there.
	a.selectTab(slices.Index(a.tabs, rt))
	tt.Frame()
	waitFor(t, tt, "the remote shell", func() bool { return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt) })
	p.term.Send([]byte("echo REMOTE_$((6*7))\r"))
	waitFor(t, tt, "the remote echo", func() bool { return strings.Contains(p.term.Text(), "REMOTE_42") })
	a.split(false)
	q := rt.Focus
	if q == p || q.host != h || !slices.Contains(sessionIDs(t, remote), q.SID) {
		t.Fatalf("the split is on %v", q.host)
	}
	a.closePane(q)
	waitFor(t, tt, "the split's session to end on the host", func() bool { return !slices.Contains(sessionIDs(t, remote), q.SID) })
	nt := a.newTab(a.currentDir())
	if nt == nil || nt.host() != h {
		t.Fatal("New Tab from a remote tab did not open on its host")
	}
	a.closeTab(nt)
	if !slices.ContainsFunc(a.paletteItems(), func(it paletteItem) bool { return it.title == "New Tab on office-mac" }) {
		t.Error("the palette has no New Tab on office-mac")
	}

	// The layout names the host of the remote panes.
	l := a.snapshot()
	hosts := map[string]string{}
	for _, st := range l.Tabs {
		hosts[st.Root.SID] = st.Root.Host
	}
	if hosts[p.SID] != "h1" || hosts[local.Focus.SID] != "" {
		t.Errorf("layout hosts %v", hosts)
	}

	// The connection drops: the pane says so and keeps its screen, typing
	// is dropped, and it all comes back.
	gate := make(chan struct{})
	dialGate.Store(&gate)
	proxy.setDown(true)
	waitFor(t, tt, "the host to reconnect", func() bool { return h.state == hostReconnecting && h.attempt == 1 })
	if label, detail, up := hostStatus(h); up || label != "Reconnecting (attempt 1)…" || !strings.Contains(detail, "connection dropped") {
		t.Errorf("status %q, %q, %v", label, detail, up)
	}
	dialGate.Store(nil)
	close(gate)
	waitFor(t, tt, "the attempt to fail", func() bool { return h.state == hostFailed && h.attempt == 2 })
	if label, _, _ := hostStatus(h); label != "Not connected" {
		t.Errorf("status %q", label)
	}
	if !tt.HasText("Reconnecting to office-mac…") {
		t.Errorf("no reconnecting banner; texts %q", tt.Texts())
	}
	if !strings.Contains(p.term.Text(), "REMOTE_42") {
		t.Error("the screen was lost")
	}
	if a.newTabOn(h, "") != nil || a.err == "" && a.errMsg == nil {
		t.Error("a tab opened on a host not connected")
	}
	a.err, a.errMsg = "", nil
	p.term.Send([]byte("echo LOST_$((1+1))\r"))
	proxy.setDown(false)
	h.poke()
	waitFor(t, tt, "the host to come back", func() bool { return h.state == hostConnected && !p.remote.Detached() })
	if tt.HasText("Reconnecting to office-mac…") {
		t.Error("the banner stays")
	}
	p.term.Send([]byte("echo BACK_$((6*7))\r"))
	waitFor(t, tt, "the echo after reconnecting", func() bool { return strings.Contains(p.term.Text(), "BACK_42") })
	if strings.Contains(p.term.Text(), "LOST_2") {
		t.Error("what was typed while disconnected reached the session")
	}

	// A session that ends on the host closes its pane.
	a.selectTab(0)
	gone := a.newTabOn(h, "")
	waitFor(t, tt, "the shell", func() bool {
		return gone.Focus.term != nil && strings.Contains(gone.Focus.term.Text(), testShell.prompt)
	})
	remote.Kill(gone.Focus.SID)
	waitFor(t, tt, "the tab of the ended session to close", func() bool { return !slices.Contains(a.tabs, gone) })

	// Removing the host closes its tabs here; its sessions go on.
	a.removeHost(h)
	if slices.Contains(a.tabs, rt) || len(a.hosts) != 0 {
		t.Fatal("the host's tab stays")
	}
	time.Sleep(200 * time.Millisecond)
	if !slices.Contains(sessionIDs(t, remote), p.SID) {
		t.Error("removing the host ended its session")
	}
	if hs, _ := loadHosts(); len(hs) != 0 {
		t.Errorf("hosts.json keeps %v", hs)
	}
}

// TestHostBlackholeReattaches is a remote pane whose network goes away
// without a word, again and again, as the Mac's to a Windows host on an
// unstable network: each time the app connects anew and the pane attaches
// again. The host must count one window attached to the session, not one
// more per drop, and the app must keep no goroutine of the connections
// gone.
func TestHostBlackholeReattaches(t *testing.T) {
	defer rex.SetTimeoutsForTest(600*time.Millisecond, 2*time.Second)()
	fakeHosts(t)
	oldHealthy := hostHealthy
	hostHealthy = 0 // a drop tries again at once
	t.Cleanup(func() { hostHealthy = oldHealthy })
	sock, remote := startHostServer(t)
	info, err := remote.Create(rex.CreateOptions{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	proxy := newHostProxy(t, sock)
	h := newHostConn(savedHost{ID: "h1", Name: "office-pc", Address: proxy.path})
	a := &App{}
	go a.runHost(h, func(fn func()) {})
	t.Cleanup(h.close)
	s := newHostStream(h, info.ID, 80, 24)
	t.Cleanup(func() { s.Close() })
	go func() {
		buf := make([]byte, 32<<10)
		for {
			if _, err := s.Read(buf); err != nil {
				return
			}
		}
	}()
	attached := func() int {
		infos, err := remote.List()
		if err != nil {
			t.Fatal(err)
		}
		for _, in := range infos {
			if in.ID == info.ID {
				return in.Attached
			}
		}
		t.Fatal("the session is gone")
		return 0
	}
	attaches := func() int {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.attaches
	}
	until := func(what string, cond func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !cond(); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s; attached %d", what, attached())
			}
		}
	}

	const cycles = 4
	base := 0
	var counts []int
	for i := range cycles {
		until("the pane to attach", func() bool { return attaches() == i+1 && !s.Detached() })
		if i == 0 {
			time.Sleep(200 * time.Millisecond)
			base = runtime.NumGoroutine()
		}
		proxy.blackhole()
		until("the pane to notice", s.Detached)
		proxy.heal()
		h.poke()
		until("the pane to attach again", func() bool { return attaches() == i+2 && !s.Detached() })
		// The host drops the connections of before in a bounded time.
		for deadline := time.Now().Add(5 * time.Second); attached() != 1 && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
		}
		counts = append(counts, attached())
	}
	for _, n := range counts {
		if n != 1 {
			t.Fatalf("the host counts %v windows attached after each drop, want 1 each time", counts)
		}
	}
	// What is left of each drop, on the app's side: nothing, but the dead
	// flows of the proxy that wait for their ends to close.
	want := base + 4
	n := runtime.NumGoroutine()
	for deadline := time.Now().Add(5 * time.Second); n > want && time.Now().Before(deadline); n = runtime.NumGoroutine() {
		time.Sleep(20 * time.Millisecond)
	}
	if n > want {
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		t.Errorf("%d goroutines after %d drops, want at most %d\n%s", n, cycles, want, buf)
	}
}

// TestHostStreamResizeWhileAttaching resizes a remote pane while its
// stream attaches, as the view does as it first lays out a new tab, made
// at 80x24, over a slow network: the session takes the window's size. At
// the old one, a Windows host's console draws its scrolling rows for a
// screen other than the window's, and the lines of ls land on each other.
func TestHostStreamResizeWhileAttaching(t *testing.T) {
	sock, remote := startHostServer(t)
	info, err := remote.Create(rex.CreateOptions{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	proxy := newHostProxy(t, sock)
	c, err := rex.ConnectSocket(proxy.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	h := newHostConn(savedHost{ID: "h1", Name: "office-pc"})
	h.client = c
	gate := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(open)
	proxy.mu.Lock()
	proxy.gate = gate
	proxy.mu.Unlock()

	s := newHostStream(h, info.ID, 80, 24)
	t.Cleanup(func() { s.Close() })
	// The stream returns what the session prints once it attached and
	// told the session its size.
	printed := make(chan struct{})
	go func() {
		buf := make([]byte, 32<<10)
		for first := true; ; first = false {
			if _, err := s.Read(buf); err != nil {
				return
			}
			if first {
				close(printed)
			}
		}
	}()
	for proxy.held.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	s.Resize(121, 41)
	open()
	select {
	case <-printed:
	case <-time.After(10 * time.Second):
		t.Fatal("the stream does not attach")
	}
	// The session takes the size the stream told it once attached, as its
	// last size settles (the server debounces a window's resize).
	waitForThat(t, func() bool {
		for _, in := range sessionInfos(t, remote) {
			if in.ID == info.ID {
				return in.Cols == 121 && in.Rows == 41
			}
		}
		return false
	}, "the session to take the window's size")
}

// TestRestoreRemote checks that a layout with remote panes comes back on
// the host's panes, which attach once it connects, and drops those of a
// host no longer saved.
func TestRestoreRemote(t *testing.T) {
	a, tt := newTestApp(t)
	fakeHosts(t)
	sock, remote := startHostServer(t)
	t.Cleanup(a.stopHosts)
	h := a.addHost(savedHost{ID: "h1", Name: "studio", Address: sock})
	waitFor(t, tt, "the host to connect", func() bool { return h.state == hostConnected })
	rt := a.newTabOn(h, "")
	a.split(true)
	sids := []string{rt.panes()[0].SID, rt.panes()[1].SID}
	a.saveNow()
	// The layout, plus a tab of a host forgotten since.
	l := savedWorkspaces(t, a.client).Sessions[0].Layout
	l.Tabs = append(l.Tabs, savedTab{Root: &savedNode{SID: "x", Host: "gone"}})
	for _, t := range a.tabs {
		for _, p := range t.panes() {
			p.term.Close()
		}
	}
	a.stopHosts()

	b := &App{client: a.client}
	b.loadHosts()
	t.Cleanup(b.stopHosts)
	if !b.restore(l, nil) {
		t.Fatal("nothing restored")
	}
	if len(b.tabs) != 2 {
		t.Fatalf("restored %d tabs", len(b.tabs))
	}
	got := b.tabs[1]
	if got.host() == nil || got.host().ID != "h1" || got.Root.Pane != nil || !got.Root.Vertical {
		t.Fatalf("the remote tab came back as %+v", got.Root)
	}
	tb := ui.NewTester(b.view, 1000, 620)
	b.selectTab(1)
	waitFor(t, tb, "the restored panes to attach", func() bool {
		for _, p := range got.panes() {
			if p.term == nil || !strings.Contains(p.term.Text(), testShell.prompt) {
				return false
			}
		}
		return true
	})
	for i, p := range got.panes() {
		if p.SID != sids[i] {
			t.Errorf("pane %d attached to %s, not %s", i, p.SID, sids[i])
		}
	}
	if n := len(sessionIDs(t, remote)); n != 2 {
		t.Errorf("the host runs %d sessions", n)
	}
}

// TestAdoptHostSessions checks that the sessions opened on a host by its
// own window get a tab here, once, through reconnects and a restored
// layout; and that a tab closed here does not come back from a list made
// before its session ended, nor an exited session.
func TestAdoptHostSessions(t *testing.T) {
	a, tt := newTestApp(t)
	fakeHosts(t)
	sock, remote := startHostServer(t)
	proxy := newHostProxy(t, sock)
	t.Cleanup(a.stopHosts)
	h := a.addHost(savedHost{ID: "h1", Name: "office-mac", Address: proxy.path})
	waitFor(t, tt, "the host to connect", func() bool { return h.state == hostConnected })
	if len(a.tabs) != 1 {
		t.Fatalf("%d tabs before the host opened any", len(a.tabs))
	}
	info, err := remote.Create(rex.CreateOptions{Cols: 97, Rows: 31})
	if err != nil {
		t.Fatal(err)
	}
	tabsOf := func(app *App, sid string) []*Tab {
		var out []*Tab
		for _, t := range app.tabs {
			for _, p := range t.panes() {
				if p.SID == sid && p.host != nil && p.host.ID == "h1" {
					out = append(out, t)
				}
			}
		}
		return out
	}
	waitFor(t, tt, "the host's session to get a tab", func() bool { return len(tabsOf(a, info.ID)) == 1 })
	got := tabsOf(a, info.ID)[0]
	if a.tabs[len(a.tabs)-1] != got || a.tab() == got {
		t.Errorf("the adopted tab is not last, or took the focus")
	}
	if cols, rows := got.Focus.term.Size(); cols != 97 || rows != 31 {
		t.Errorf("attached at %dx%d", cols, rows)
	}
	a.selectTab(slices.Index(a.tabs, got))
	waitFor(t, tt, "its shell", func() bool { return strings.Contains(got.Focus.term.Text(), testShell.prompt) })

	// Polls and a reconnect open no other.
	proxy.setDown(true)
	waitFor(t, tt, "the host to drop", func() bool { return h.state != hostConnected })
	proxy.setDown(false)
	h.poke()
	waitFor(t, tt, "the host to come back", func() bool { return h.state == hostConnected })
	for range 3 {
		time.Sleep(400 * time.Millisecond)
		tt.Frame()
	}
	if n := len(tabsOf(a, info.ID)); n != 1 {
		t.Fatalf("%d tabs of the session", n)
	}

	// A restored layout's pane, not attached yet, is the session's.
	a.saveNow()
	for _, t := range a.tabs {
		for _, p := range t.panes() {
			p.term.Close()
		}
	}
	a.stopHosts()
	b := &App{client: a.client}
	b.loadHosts()
	t.Cleanup(b.stopHosts)
	if !b.restore(savedWorkspaces(t, a.client).Sessions[0].Layout, nil) {
		t.Fatal("nothing restored")
	}
	tb := ui.NewTester(b.view, 1000, 620)
	bh := b.hosts[0]
	waitFor(t, tb, "the host to connect", func() bool { return bh.state == hostConnected })
	for range 3 {
		time.Sleep(400 * time.Millisecond)
		tb.Frame()
	}
	if n := len(tabsOf(b, info.ID)); n != 1 {
		t.Fatalf("%d tabs of the session after restoring", n)
	}

	// Closed here, it ends; a list from before does not open it again.
	stale := map[string]rex.SessionInfo{info.ID: info}
	b.closeTab(tabsOf(b, info.ID)[0])
	waitFor(t, tb, "the session to end", func() bool { return !slices.Contains(sessionIDs(t, remote), info.ID) })
	waitFor(t, tb, "the kill to be done", func() bool {
		bh.mu.Lock()
		defer bh.mu.Unlock()
		return len(bh.Kills) == 0
	})
	b.apply(bh, stale)
	if n := len(tabsOf(b, info.ID)); n != 0 {
		t.Errorf("the closed session came back in %d tabs", n)
	}
	// Nor an exited session.
	b.apply(bh, map[string]rex.SessionInfo{"x": {ID: "x", Exited: true}})
	if n := len(tabsOf(b, "x")); n != 0 {
		t.Error("an exited session got a tab")
	}

	// One the host ends closes here, and is not kept to end.
	other, err := remote.Create(rex.CreateOptions{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, tb, "the other session's tab", func() bool { return len(tabsOf(b, other.ID)) == 1 })
	remote.Kill(other.ID)
	waitFor(t, tb, "its tab to close", func() bool { return len(tabsOf(b, other.ID)) == 0 })
	if hs, _ := loadHosts(); len(hs) != 1 || slices.Contains(hs[0].Kills, other.ID) {
		t.Errorf("hosts.json keeps %+v", hs)
	}
}

// What a fake host does with a connection.
const (
	fakeRefuse   int32 = iota // not pairing: says it refuses, and closes
	fakePairing               // serves remote.pair only
	fakeApproved              // serves hello and list
	fakeSilent                // closes, saying nothing, as a connection lost
	fakeFlaky                 // greets, then drops
)

// fakeHost is a server that does not allow this machine until approved,
// on a Unix socket, which counts the connections made to it.
type fakeHost struct {
	path      string
	mode      atomic.Int32
	requested atomic.Bool // a request to pair waits
	conns     atomic.Int32
}

func newFakeHost(t *testing.T, mode int32) *fakeHost {
	t.Helper()
	dir := testDir(t)
	f := &fakeHost{path: filepath.Join(dir, "s.sock")}
	f.mode.Store(mode)
	ln, err := net.Listen("unix", f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.RemoveAll(dir) })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.conns.Add(1)
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeHost) serve(c net.Conn) {
	defer c.Close()
	reply := func(res rex.Response) {
		b, _ := json.Marshal(res)
		c.Write(append(b, '\n'))
	}
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var req rex.Request
		json.Unmarshal(line, &req)
		res := rex.Response{ID: req.ID}
		switch f.mode.Load() {
		case fakeSilent:
			return
		case fakeRefuse:
			reply(rex.Response{Error: "rex: this machine is not allowed here"})
			return
		case fakePairing:
			if req.Op == "remote.pair" {
				f.requested.Store(true)
				res.Data, _ = json.Marshal(rex.PairResult{Fingerprint: "01234567-cdef"})
			} else {
				res.Error = req.Op + " is not allowed before pairing"
				if f.requested.Load() {
					res.Error += ": the request waits for approval"
				}
			}
			reply(res)
			return
		case fakeFlaky:
			if req.Op != "hello" {
				return
			}
			res.Data, _ = json.Marshal(rex.Hello{PID: 7, Version: rex.ProtocolVersion})
		default:
			if req.Op == "list" {
				res.Data = json.RawMessage("[]")
			} else {
				res.Data, _ = json.Marshal(rex.Hello{PID: 7, Version: rex.ProtocolVersion})
			}
		}
		reply(res)
	}
}

// hostsPage is the Remote Hosts page of an app with no terminal window.
func hostsPage(t *testing.T) (*App, *ui.Tester) {
	isolateSettings(t, "")
	t.Setenv("GOREX_DIR", t.TempDir())
	fakeHosts(t)
	a := &App{}
	t.Cleanup(a.stopHosts)
	t.Cleanup(closeAddHost)
	settingsUI.section = "hosts"
	tt := ui.NewTester(a.settingsView, 780, 540)
	tt.Frame()
	return a, tt
}

// TestAddHostPairs adds a host through the sheet: the fields are checked,
// the fingerprint shows while the host's user is asked, a host not reached
// meanwhile is waited for, a rejection ends it, and the host is saved once
// approved.
func TestAddHostPairs(t *testing.T) {
	a, tt := hostsPage(t)
	f := newFakeHost(t, fakePairing)
	if !tt.HasText("No remote hosts yet.") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if err := tt.Click("Add Remote Host…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Add"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Enter a host name.") {
		t.Errorf("no error for an empty form; texts %q", tt.Texts())
	}
	tt.Type(f.path) // the address field has the focus
	hostsUI.name, hostsUI.pairAs = "office-mac", "alice-laptop"
	tt.Frame()
	if tt.HasText(f.path) {
		t.Error("the address shows")
	}
	if err := tt.Click("Add"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the fingerprint", func() bool { return hostsUI.stage == addWaiting })
	if !tt.HasText("01234567-cdef") || !tt.HasText("On office-mac, approve the request showing this fingerprint.") {
		t.Errorf("texts %q", tt.Texts())
	}

	// The host lost on the way is no rejection: the sheet waits, and says.
	f.mode.Store(fakeSilent)
	waitFor(t, tt, "the note", func() bool { return hostsUI.note != nil })
	if hostsUI.stage != addWaiting || !tt.HasText(locales.HostsUnreachable()) {
		t.Errorf("stage %v; texts %q", hostsUI.stage, tt.Texts())
	}
	f.mode.Store(fakePairing)
	waitFor(t, tt, "the note to go", func() bool { return hostsUI.note == nil })

	// Rejected: the sheet says so, and stops asking.
	f.requested.Store(false)
	waitFor(t, tt, "the rejection", func() bool { return hostsUI.stage == addForm && hostsUI.formErr != nil })
	if msg := hostsUI.formErr(); !strings.Contains(msg, "office-mac did not approve this machine") {
		t.Errorf("error %q", msg)
	}
	n := f.conns.Load()
	time.Sleep(10 * pairPoll)
	if m := f.conns.Load(); m != n {
		t.Errorf("%d connections after the rejection", m-n)
	}
	if len(a.hosts) != 0 {
		t.Fatal("saved a host rejected")
	}

	// Asked again, and approved.
	a.submitHost()
	waitFor(t, tt, "the fingerprint again", func() bool { return hostsUI.stage == addWaiting })
	f.mode.Store(fakeApproved)
	waitFor(t, tt, "the host to be saved", func() bool { return len(a.hosts) == 1 && !hostsUI.adding })
	if hostsUI.address != "" {
		t.Error("the sheet keeps the address")
	}
	if hs, _ := loadHosts(); len(hs) != 1 || hs[0].Address != f.path || hs[0].Name != "office-mac" {
		t.Errorf("hosts.json has %v", hs)
	}
	waitFor(t, tt, "the host to connect", func() bool { return a.hosts[0].state == hostConnected && tt.HasText("Connected") })

	// A host not pairing says how to start; one lost on the way is not
	// asked to pair.
	for _, tc := range []struct {
		mode int32
		want string
	}{{fakeRefuse, "studio is not pairing"}, {fakeSilent, "Could not reach the host"}} {
		other := newFakeHost(t, tc.mode)
		a.openAddHost()
		hostsUI.address, hostsUI.name = other.path, "studio"
		a.submitHost()
		waitFor(t, tt, "the error", func() bool { return hostsUI.stage == addForm && hostsUI.formErr != nil })
		if msg := hostsUI.formErr(); !strings.Contains(msg, tc.want) {
			t.Errorf("error %q, want %q", msg, tc.want)
		}
		if other.requested.Load() || len(a.hosts) != 1 {
			t.Error("a host not paired was saved, or asked")
		}
	}
}

// TestHostFailures checks what a saved host says when it is not reached,
// and when it refuses this machine: a connection lost is not a refusal.
func TestHostFailures(t *testing.T) {
	a, tt := hostsPage(t)
	f := newFakeHost(t, fakeSilent)
	h := a.addHost(savedHost{ID: "h1", Name: "studio", Address: f.path})
	waitFor(t, tt, "the failure", func() bool { return h.state == hostFailed })
	if _, detail, _ := hostStatus(h); detail != locales.HostsUnreachable() {
		t.Errorf("lost on the way: %q", detail)
	}
	f.mode.Store(fakeRefuse)
	h.poke()
	waitFor(t, tt, "the refusal", func() bool {
		_, detail, _ := hostStatus(h)
		return detail == locales.HostsNotAllowed()
	})
}

// TestHostDropsBackOff checks that a host that greets and drops at once is
// not dialed in a loop: the failures wait longer each time.
func TestHostDropsBackOff(t *testing.T) {
	a, tt := hostsPage(t)
	f := newFakeHost(t, fakeFlaky)
	h := a.addHost(savedHost{ID: "h1", Name: "studio", Address: f.path})
	deadline := time.Now().Add(3500 * time.Millisecond)
	for time.Now().Before(deadline) {
		tt.Frame()
		time.Sleep(50 * time.Millisecond)
	}
	// Dialed at 0, then after 1 s and 2 s more.
	if n := f.conns.Load(); n < 2 || n > 4 {
		t.Errorf("%d connections in 3.5 s", n)
	}
	if h.attempt < 2 {
		t.Errorf("attempt %d", h.attempt)
	}

	// Up a while, a drop tries again at once.
	old := hostHealthy
	hostHealthy = 0
	t.Cleanup(func() { hostHealthy = old })
	f.mode.Store(fakeApproved)
	h.poke()
	waitFor(t, tt, "the host to connect", func() bool { return h.state == hostConnected })
	n := f.conns.Load()
	f.mode.Store(fakeSilent) // the next list drops it
	waitFor(t, tt, "the host to drop", func() bool { return h.state != hostConnected })
	f.mode.Store(fakeApproved)
	waitFor(t, tt, "the host to connect again", func() bool { return h.state == hostConnected })
	if f.conns.Load() == n {
		t.Error("no new connection")
	}
}

// TestPendingKills checks that closing a host's tab while it is away ends
// its session once the host is back, though the app quit meanwhile.
func TestPendingKills(t *testing.T) {
	a, tt := newTestApp(t)
	fakeHosts(t)
	sock, remote := startHostServer(t)
	proxy := newHostProxy(t, sock)
	t.Cleanup(a.stopHosts)
	h := a.addHost(savedHost{ID: "h1", Name: "studio", Address: proxy.path})
	waitFor(t, tt, "the host to connect", func() bool { return h.state == hostConnected })
	rt := a.newTabOn(h, "")
	sid := rt.Focus.SID
	proxy.setDown(true)
	waitFor(t, tt, "the host to drop", func() bool { return h.state != hostConnected && h.current() == nil })
	a.closeTab(rt)
	if hs, _ := loadHosts(); len(hs) != 1 || !slices.Contains(hs[0].Kills, sid) {
		t.Fatalf("hosts.json has %+v", hs)
	}
	if !slices.Contains(sessionIDs(t, remote), sid) {
		t.Fatal("the session ended while the host was away")
	}
	// The app quits, and opens again with the host back.
	a.stopHosts()
	proxy.setDown(false)
	b := &App{client: a.client}
	b.loadHosts()
	t.Cleanup(b.stopHosts)
	tb := ui.NewTester(b.view, 1000, 620)
	waitFor(t, tb, "the session to end on the host", func() bool { return !slices.Contains(sessionIDs(t, remote), sid) })
	waitFor(t, tb, "the kill to be forgotten", func() bool {
		hs, _ := loadHosts()
		return len(hs) == 1 && len(hs[0].Kills) == 0
	})
}

// TestTabWidthBadge checks that only a tab with a host's badge grows past
// the widest of tabs.
func TestTabWidthBadge(t *testing.T) {
	registerFonts()
	h := newHostConn(savedHost{ID: "h1", Name: "office-mac"})
	tab := func(host *hostConn) *Tab {
		t := &Tab{Name: strings.Repeat("a very long name ", 4)}
		p := &Pane{host: host, Tab: t}
		t.Root = &Node{Pane: p}
		p.Node = t.Root
		return t
	}
	local, remote := tab(nil), tab(h)
	var lw, rw float32
	tt := ui.NewTester(func(c *ui.Context) { lw, rw = tabWidth(c, local), tabWidth(c, remote) }, 400, 100)
	tt.Frame()
	if lw != tabMaxW {
		t.Errorf("a long local tab is %v wide, not %v", lw, tabMaxW)
	}
	if rw <= tabMaxW || rw > tabMaxW+badgeMaxW+21 {
		t.Errorf("a long remote tab is %v wide", rw)
	}
}

func TestHostMessagesInChinese(t *testing.T) {
	zh, en := locales.In("zh-Hans"), locales.In("en")
	for got, want := range map[string]string{
		zh.HostsReconnecting(3):  "正在重新连接（第 3 次）…",
		zh.HostsSessions(2):      "2 个终端",
		en.HostsSessions(1):      "1 terminal",
		en.HostsSessions(0):      "No terminals",
		zh.CmdNewTabOn("studio"): "在 studio 上新建标签页",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestReconcileNetwork(t *testing.T) {
	a, _ := newTestApp(t)
	fakeHosts(t)
	t.Cleanup(a.stopHosts)

	mk := func(i int, name string) rex.NetworkMember {
		return rex.NetworkMember{Name: name, PublicKey: fmt.Sprintf("nodekey:%064x", i), Address: "tcfake" + name}
	}
	bob, carol := mk(1, "bob"), mk(2, "carol")

	// Members become hosts of their own, flagged as the network's.
	a.reconcileNetwork(&rex.NetworkStatus{Name: "alice", Members: []rex.NetworkMember{bob}})
	if len(a.hosts) != 1 || !a.hosts[0].Network || a.hosts[0].ID != networkHostID(bob.PublicKey) {
		t.Fatalf("the member became %+v", a.hosts)
	}
	saved, err := loadHosts()
	if err != nil || len(saved) != 1 || !saved[0].Network || saved[0].ID != networkHostID(bob.PublicKey) {
		t.Fatalf("hosts.json: %+v, %v", saved, err)
	}

	// A renamed member keeps its host; a new one adds; one gone goes.
	bob.Name = "bobby"
	a.reconcileNetwork(&rex.NetworkStatus{Members: []rex.NetworkMember{bob}})
	if len(a.hosts) != 1 || a.hosts[0].Name != "bobby" {
		t.Fatalf("the renamed member: %+v", a.hosts)
	}
	a.reconcileNetwork(&rex.NetworkStatus{Members: []rex.NetworkMember{carol}})
	if len(a.hosts) != 1 || a.hosts[0].ID != networkHostID(carol.PublicKey) {
		t.Fatalf("the member changed: %+v", a.hosts)
	}

	// A host added by hand stays whatever the directory says.
	manual := a.addHost(savedHost{ID: "m1", Name: "manual", Address: "/tmp/nowhere"})
	a.reconcileNetwork(&rex.NetworkStatus{})
	if len(a.hosts) != 1 || a.hosts[0].ID != "m1" || manual == nil {
		t.Fatalf("the manual host: %+v", a.hosts)
	}
}

func TestLayoutLines(t *testing.T) {
	n := &savedNode{A: &savedNode{Name: "left", SID: "a", Dir: "/tmp"}, B: &savedNode{Name: "right", SID: "b"}}
	lines := layoutLines(n, 1)
	if len(lines) != 2 || lines[0].text != "  left — /tmp" || lines[0].sid != "a" || lines[1].text != "  right" || lines[1].sid != "b" {
		t.Fatalf("lines %+v", lines)
	}
	if got := layoutTabName(savedTab{}, 2); got != "Tab 3" {
		t.Fatalf("tab %q", got)
	}
}
