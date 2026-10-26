package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
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
		return rex.ConnectSocket(address)
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
// new ones until up.
type hostProxy struct {
	path, to string
	ln       net.Listener
	mu       sync.Mutex
	conns    []net.Conn
	down     bool
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
			down := p.down
			p.mu.Unlock()
			if down {
				c.Close()
				continue
			}
			s, err := net.Dial("unix", to)
			if err != nil {
				c.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, c, s)
			p.mu.Unlock()
			go func() { io.Copy(s, c); s.Close() }()
			go func() { io.Copy(c, s); c.Close() }()
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
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("read back %v, %v", got, err)
	}
	// The address is not in the settings.
	if b, _ := os.ReadFile(settingsPath()); strings.Contains(string(b), "tcSECRET") {
		t.Error("the settings hold an address")
	}
	os.WriteFile(hostsPath(), []byte("{"), 0o600)
	if _, err := loadHosts(); err == nil || strings.Contains(err.Error(), "{") {
		t.Errorf("a bad file: %v", err)
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
	var l savedLayout
	raw, _ := a.client.Layout()
	json.Unmarshal(raw, &l)
	l.Tabs = append(l.Tabs, savedTab{Root: &savedNode{SID: "x", Host: "gone"}})
	raw, _ = json.Marshal(l)
	a.client.SetLayout(raw)
	for _, t := range a.tabs {
		for _, p := range t.panes() {
			p.term.Close()
		}
	}
	a.stopHosts()

	b := &App{client: a.client}
	b.loadHosts()
	t.Cleanup(b.stopHosts)
	if !b.restore() {
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

// fakePairServer answers as a server that does not allow this machine
// until approved: before, it answers remote.pair with a fingerprint, when
// pairing, and refuses all else.
func fakePairServer(t *testing.T, pairing bool, approved *atomic.Bool) string {
	t.Helper()
	dir := testDir(t)
	path := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", path)
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
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadBytes('\n')
					if err != nil {
						return
					}
					var req rex.Request
					json.Unmarshal(line, &req)
					res := rex.Response{ID: req.ID}
					switch {
					case approved.Load() && req.Op == "list":
						res.Data = json.RawMessage("[]")
					case approved.Load():
						res.Data, _ = json.Marshal(rex.Hello{PID: 7, Version: rex.ProtocolVersion})
					case !pairing:
						return // closes, saying nothing
					case req.Op == "remote.pair":
						res.Data, _ = json.Marshal(rex.PairResult{Fingerprint: "01234567-cdef"})
					default:
						res.Error = req.Op + " is not allowed before pairing"
					}
					b, _ := json.Marshal(res)
					c.Write(append(b, '\n'))
					if !approved.Load() {
						return
					}
				}
			}()
		}
	}()
	return path
}

// TestAddHostPairs adds a host through the sheet: the fields are checked,
// the fingerprint shows while the host's user is asked, and the host is
// saved once approved.
func TestAddHostPairs(t *testing.T) {
	isolateSettings(t, "")
	t.Setenv("GOREX_DIR", t.TempDir())
	fakeHosts(t)
	var approved atomic.Bool
	addr := fakePairServer(t, true, &approved)
	a := &App{}
	t.Cleanup(a.stopHosts)
	t.Cleanup(closeAddHost)
	settingsUI.section = "hosts"
	tt := ui.NewTester(a.settingsView, 780, 540)
	tt.Frame()
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
	tt.Type(addr) // the address field has the focus
	hostsUI.name, hostsUI.pairAs = "office-mac", "alice-laptop"
	tt.Frame()
	if tt.HasText(addr) {
		t.Error("the address shows")
	}
	if err := tt.Click("Add"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the fingerprint", func() bool { return hostsUI.stage == addWaiting })
	if !tt.HasText("01234567-cdef") || !tt.HasText("On office-mac, approve the request showing this fingerprint.") {
		t.Errorf("texts %q", tt.Texts())
	}
	if len(a.hosts) != 0 {
		t.Fatal("saved before approval")
	}
	approved.Store(true)
	waitFor(t, tt, "the host to be saved", func() bool { return len(a.hosts) == 1 && !hostsUI.adding })
	if hostsUI.address != "" {
		t.Error("the sheet keeps the address")
	}
	if hs, _ := loadHosts(); len(hs) != 1 || hs[0].Address != addr || hs[0].Name != "office-mac" {
		t.Errorf("hosts.json has %v", hs)
	}
	waitFor(t, tt, "the host to connect", func() bool { return a.hosts[0].state == hostConnected && tt.HasText("Connected") })

	// A host not pairing says how to start.
	other := fakePairServer(t, false, new(atomic.Bool))
	a.openAddHost()
	hostsUI.address, hostsUI.name = other, "studio"
	a.submitHost()
	waitFor(t, tt, "the error", func() bool { return hostsUI.stage == addForm && hostsUI.formErr != nil })
	if msg := hostsUI.formErr(); !strings.Contains(msg, "studio is not pairing") {
		t.Errorf("error %q", msg)
	}
	if len(a.hosts) != 1 {
		t.Error("a host not paired was saved")
	}
}

func TestHostMessagesInChinese(t *testing.T) {
	zh, en := locales.In("zh-Hans"), locales.In("en")
	for got, want := range map[string]string{
		zh.HostsReconnecting(3):  "正在重新连接（第 3 次）…",
		zh.HostsSessions(2):      "2 个标签页",
		en.HostsSessions(1):      "1 tab",
		en.HostsSessions(0):      "No tabs",
		zh.CmdNewTabOn("studio"): "在 studio 上新建标签页",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
