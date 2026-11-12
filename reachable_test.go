package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// recordingServer is a server on a Unix socket that answers every request
// with an empty result and keeps what it was asked.
type recordingServer struct {
	mu     sync.Mutex
	reqs   []rex.Request
	status rex.RemoteStatus // what remote.status answers
}

// show makes st what the server says and the page shows.
func (s *recordingServer) show(tt *ui.Tester, change func(*rex.RemoteStatus)) {
	time.Sleep(150 * time.Millisecond) // an ask on its way lands first
	tt.Frame()
	s.mu.Lock()
	change(&s.status)
	remoteUI.st = s.status
	s.mu.Unlock()
	tt.Frame()
}

func (s *recordingServer) asked(op string) (out []rex.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.reqs {
		if r.Op == op {
			out = append(out, r)
		}
	}
	return out
}

// serverPage shows the Server page of an app connected to a recording
// server, with st as the server's status.
func serverPage(t *testing.T, st rex.RemoteStatus) (*App, *ui.Tester, *recordingServer) {
	t.Helper()
	isolateSettings(t, "")
	registerFonts()
	dir := testDir(t)
	path := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	rs := &recordingServer{status: st}
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
					rs.mu.Lock()
					rs.reqs = append(rs.reqs, req)
					rs.mu.Unlock()
					data := json.RawMessage("{}")
					if req.Op == "remote.status" {
						rs.mu.Lock()
						data, _ = json.Marshal(rs.status)
						rs.mu.Unlock()
					}
					b, _ := json.Marshal(rex.Response{ID: req.ID, Data: data})
					c.Write(append(b, '\n'))
				}
			}()
		}
	}()
	client, err := rex.ConnectSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{client: client}
	t.Cleanup(func() {
		stopRemotePoll()
		client.Close()
		ln.Close()
		os.RemoveAll(dir)
	})
	settingsUI.section = "server"
	remoteUI.stop = make(chan struct{}) // the page is shown; no poll runs
	remoteUI.loaded, remoteUI.st, remoteUI.err = true, st, nil
	tt := ui.NewTester(a.settingsView, 780, 1600)
	tt.Frame()
	return a, tt, rs
}

// waitAsked waits until the server was asked op, and returns the asks.
func waitAsked(t *testing.T, tt *ui.Tester, rs *recordingServer, op string) []rex.Request {
	t.Helper()
	waitFor(t, tt, op, func() bool { return len(rs.asked(op)) > 0 })
	return rs.asked(op)
}

// TestReachableRow: the Reachable at row sets an address, checked first,
// clears it, and says why the relay does not run.
func TestReachableRow(t *testing.T) {
	a, tt, rs := serverPage(t, rex.RemoteStatus{Enabled: true, Running: true})
	for _, s := range []string{locales.RemoteReachableTitle(), locales.RemoteReachableNote("7464")} {
		if !tt.HasText(s) {
			t.Fatalf("no %q in %q", s, tt.Texts())
		}
	}
	if tt.HasText(locales.RemoteReachableOff()) {
		t.Error("Turn Off shows while it is off")
	}
	// A bad address is refused here.
	remoteUI.reach, remoteUI.reachEdited = "not an ip", true
	tt.Frame()
	if err := tt.Click(locales.RemoteReachableTitle() + ": " + locales.RemoteLanSave()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(locales.RemoteReachableInvalid()) || len(rs.asked("remote.reachableSet")) != 0 {
		t.Fatalf("a bad address went through; texts %q", tt.Texts())
	}
	// A good one is saved, as it is normalized.
	remoteUI.reach = " 192.0.2.10 "
	tt.Frame()
	if err := tt.Click(locales.RemoteReachableTitle() + ": " + locales.RemoteLanSave()); err != nil {
		t.Fatal(err)
	}
	reqs := waitAsked(t, tt, rs, "remote.reachableSet")
	if reqs[0].ReachableAt != "192.0.2.10" {
		t.Errorf("asked %q", reqs[0].ReachableAt)
	}
	waitFor(t, tt, "the page to apply it", func() bool { return !remoteUI.busy })

	// On, with an error: Turn Off clears it, and the error shows.
	rs.show(tt, func(st *rex.RemoteStatus) {
		st.ReachableAt, st.ReachableError = "192.0.2.10", "listen tcp 192.0.2.10:7464: bind: cannot assign"
	})
	if !tt.HasText(remoteUI.st.ReachableError) {
		t.Fatalf("no error in %q", tt.Texts())
	}
	if err := tt.Click(locales.RemoteReachableOff()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "Turn Off", func() bool { return len(rs.asked("remote.reachableSet")) == 2 })
	if got := rs.asked("remote.reachableSet")[1].ReachableAt; got != "" {
		t.Errorf("Turn Off asked %q", got)
	}
	_ = a
}

// TestDirectAddress: while Reachable at runs, the second address and its
// QR code are offered, hidden as the first is; off, they are not.
func TestDirectAddress(t *testing.T) {
	_, tt, rs := serverPage(t, rex.RemoteStatus{Enabled: true, Running: true})
	remoteUI.details = true
	tt.Frame()
	if tt.HasText(locales.RemoteDirectAddress()) || tt.HasText(locales.RemoteDirectQR()) {
		t.Fatalf("the direct address shows with Reachable at off; texts %q", tt.Texts())
	}
	rs.show(tt, func(st *rex.RemoteStatus) { st.ReachableAt = "192.0.2.10" })
	for _, s := range []string{locales.RemoteDirectAddress(), locales.RemoteDirectAddressNote(), locales.RemoteDirectQR()} {
		if !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	// A failed relay offers none.
	rs.show(tt, func(st *rex.RemoteStatus) { st.ReachableError = "it failed" })
	if tt.HasText(locales.RemoteDirectAddress()) || tt.HasText(locales.RemoteDirectQR()) {
		t.Errorf("the direct address shows with the relay down; texts %q", tt.Texts())
	}
}

// TestPendingByAddress: a request by address shows its code, name and
// source; Approve and Reject ask the server by the code; with two waiting
// Approve is off and a line says to reject them all; with none the page
// is the closed one.
func TestPendingByAddress(t *testing.T) {
	now := time.Now()
	one := rex.RemoteAddrPending{Code: "123456", Name: "alice-laptop", Source: "192.0.2.20:51000", RequestedAt: now}
	_, tt, rs := serverPage(t, rex.RemoteStatus{
		Enabled: true, Running: true, Pairing: rex.RemotePairing{Open: true, ExpiresAt: now.Add(time.Minute)},
		PendingByAddress: []rex.RemoteAddrPending{one},
	})
	for _, s := range []string{"123456", "alice-laptop", locales.RemotePairAddrFrom("192.0.2.20:51000"), locales.RemotePairAddrCompare()} {
		if !tt.HasText(s) {
			t.Fatalf("no %q in %q", s, tt.Texts())
		}
	}
	if err := tt.Click(locales.RemoteApprove()); err != nil {
		t.Fatal(err)
	}
	if got := waitAsked(t, tt, rs, "remote.approve"); got[0].Code != "123456" {
		t.Errorf("approved %+v", got[0])
	}
	waitFor(t, tt, "the change to end", func() bool { return !remoteUI.busy })
	if err := tt.Click(locales.RemoteReject()); err != nil {
		t.Fatal(err)
	}
	if got := waitAsked(t, tt, rs, "remote.reject"); got[0].Code != "123456" {
		t.Errorf("rejected %+v", got[0])
	}
	waitFor(t, tt, "the change to end", func() bool { return !remoteUI.busy })

	// Two: none can be approved.
	two := one
	two.Code, two.Name = "654321", "mallory"
	rs.show(tt, func(st *rex.RemoteStatus) { st.PendingByAddress = []rex.RemoteAddrPending{one, two} })
	if !tt.HasText(locales.RemotePairAddrMany()) || tt.HasText(locales.RemotePairAddrCompare()) {
		t.Fatalf("two waiting; texts %q", tt.Texts())
	}
	n := len(rs.asked("remote.approve"))
	for i := 0; i < 2; i++ {
		_ = tt.Click(locales.RemoteApprove())
		tt.Frame()
	}
	time.Sleep(100 * time.Millisecond)
	if len(rs.asked("remote.approve")) != n {
		t.Error("Approve asked the server with two requests waiting")
	}

	// The window closed after a request ended: the normal closed state.
	rs.show(tt, func(st *rex.RemoteStatus) { st.PendingByAddress, st.Pairing = nil, rex.RemotePairing{} })
	if tt.HasText("123456") || tt.HasText(locales.RemotePairAddrMany()) || !tt.HasText(locales.RemotePairNew()) {
		t.Errorf("closed state; texts %q", tt.Texts())
	}
}

// fakeAddrPairing is a pairing by address whose answers the test gives.
type fakeAddrPairing struct {
	confirm  chan struct{} // closed to let Confirm answer
	addrs    []string
	err      error
	mismatch atomic.Int32
	closed   atomic.Int32
}

func (f *fakeAddrPairing) Confirm(ctx context.Context) ([]string, error) {
	select {
	case <-f.confirm:
		return f.addrs, f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (f *fakeAddrPairing) Mismatch() error {
	f.mismatch.Add(1)
	return errors.New("rex: the machine may have let in an unknown device")
}
func (f *fakeAddrPairing) Close() error { f.closed.Add(1); return nil }

func fakeByAddress(t *testing.T, f *fakeAddrPairing, asked *[]string) {
	t.Helper()
	old := pairByAddress
	pairByAddress = func(ctx context.Context, addr, name string) (string, addrPairing, error) {
		*asked = append(*asked, addr+"|"+name)
		return "482913", f, nil
	}
	t.Cleanup(func() { pairByAddress = old })
}

// addByIP opens the sheet and submits an IP address.
func addByIP(t *testing.T) (*App, *ui.Tester) {
	a, tt := hostsPage(t)
	a.openAddHost()
	hostsUI.address, hostsUI.name, hostsUI.pairAs = "192.0.2.30", "office-mac", "alice-laptop"
	tt.Frame()
	if err := tt.Click("Add"); err != nil {
		t.Fatal(err)
	}
	return a, tt
}

// TestAddHostByIP: an IP address starts pairing by address: the code shows
// with the two answers; "match" waits for the approval and saves the host
// by the first address returned.
func TestAddHostByIP(t *testing.T) {
	f := &fakeAddrPairing{confirm: make(chan struct{}), addrs: []string{"tcFIRST", "tcSECOND"}}
	var asked []string
	fakeByAddress(t, f, &asked)
	a, tt := addByIP(t)
	waitFor(t, tt, "the code", func() bool { return hostsUI.stage == addCode })
	for _, s := range []string{"482913", locales.HostsCodeIntro("office-mac"), locales.HostsCodeMatch(), locales.HostsCodeDiffer()} {
		if !tt.HasText(s) {
			t.Fatalf("no %q in %q", s, tt.Texts())
		}
	}
	if len(asked) != 1 || asked[0] != "192.0.2.30|alice-laptop" {
		t.Errorf("asked %v", asked)
	}
	if len(a.hosts) != 0 {
		t.Fatal("a host was saved before the codes matched")
	}
	if err := tt.Click(locales.HostsCodeMatch()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(locales.HostsCodeApprove("office-mac")) || tt.HasText(locales.HostsCodeMatch()) {
		t.Fatalf("waiting for the approval; texts %q", tt.Texts())
	}
	if len(a.hosts) != 0 {
		t.Fatal("a host was saved before the approval")
	}
	close(f.confirm)
	waitFor(t, tt, "the host", func() bool { return len(a.hosts) == 1 && !hostsUI.adding })
	if a.hosts[0].Address != "tcFIRST" || a.hosts[0].Name != "office-mac" {
		t.Errorf("saved %q at %q", a.hosts[0].Name, a.hosts[0].Address)
	}
	if hs, _ := loadHosts(); len(hs) != 1 || hs[0].Address != "tcFIRST" {
		t.Errorf("hosts.json has %v", hs)
	}
}

// TestAddHostByIPDiffers: "They differ" saves nothing and shows the
// warning; so does an error of the approval; Cancel closes the request.
func TestAddHostByIPDiffers(t *testing.T) {
	f := &fakeAddrPairing{confirm: make(chan struct{})}
	var asked []string
	fakeByAddress(t, f, &asked)
	a, tt := addByIP(t)
	waitFor(t, tt, "the code", func() bool { return hostsUI.stage == addCode })
	if err := tt.Click(locales.HostsCodeDiffer()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if f.mismatch.Load() != 1 || hostsUI.stage != addForm || len(a.hosts) != 0 {
		t.Fatalf("mismatch %d, stage %v, %d hosts", f.mismatch.Load(), hostsUI.stage, len(a.hosts))
	}
	if !tt.HasText("The machine may have let in an unknown device") {
		t.Errorf("no warning in %q", tt.Texts())
	}

	// An error of the approval.
	f2 := &fakeAddrPairing{confirm: make(chan struct{}), err: errors.New("rex: the machine rejected the request, or nobody approved it in time")}
	close(f2.confirm)
	fakeByAddress(t, f2, &asked)
	a.submitHost()
	waitFor(t, tt, "the code", func() bool { return hostsUI.stage == addCode })
	tt.Click(locales.HostsCodeMatch())
	waitFor(t, tt, "the error", func() bool { return hostsUI.stage == addForm })
	if !tt.HasText("The machine rejected the request, or nobody approved it in time") || len(a.hosts) != 0 {
		t.Errorf("texts %q, %d hosts", tt.Texts(), len(a.hosts))
	}

	// Cancel closes the request.
	f3 := &fakeAddrPairing{confirm: make(chan struct{})}
	fakeByAddress(t, f3, &asked)
	a.submitHost()
	waitFor(t, tt, "the code", func() bool { return hostsUI.stage == addCode })
	if err := tt.Click(locales.RemoteCancel()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "Close", func() bool { return f3.closed.Load() > 0 })
	if len(a.hosts) != 0 {
		t.Error("a host was saved")
	}
}

// TestIsIPAddress: an IP address, with a port or not, is paired by its
// code; a pasted address is not.
func TestIsIPAddress(t *testing.T) {
	for in, want := range map[string]bool{
		"192.0.2.30": true, " 192.0.2.30:7464 ": true, "[2001:db8::1]:7464": true, "2001:db8::1": true,
		"tcAbc-_9": false, "example.com": false, "": false, "/tmp/s.sock": false,
	} {
		if got := isIPAddress(in); got != want {
			t.Errorf("isIPAddress(%q) = %v", in, got)
		}
	}
	if !strings.Contains(locales.HostsBadAddress(), "IP address") {
		t.Error("the bad address message does not mention an IP address")
	}
}
