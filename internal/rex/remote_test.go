//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// testServer is a server on its own directory, not listening; remote
// access listens on the loopback instead of tailcat.
func testServer(t *testing.T) (*Server, *atomic.Int32) {
	t.Setenv("GOREX_DIR", t.TempDir())
	var listens atomic.Int32
	old, oldKey := remoteListen, remotePeerKey
	remotePeerKey = func(_ *tailcat.Server, c net.Conn) string {
		for range 200 {
			if k, ok := testPeers.Load(c.RemoteAddr().String()); ok {
				return k.(string)
			}
			time.Sleep(5 * time.Millisecond)
		}
		return ""
	}
	remoteListen = func(ctx context.Context, ts *tailcat.Server, region int) (net.Listener, tailcat.Addr, int, error) {
		listens.Add(1)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		return ln, "tcFAKE", 7, err
	}
	hostDone := make(chan struct{})
	close(hostDone)
	s := &Server{sessions: map[string]*session{}, started: time.Now(), layoutRev: 1, hostDone: hostDone, quit: make(chan struct{})}
	t.Cleanup(func() {
		s.remoteClose()
		remoteListen, remotePeerKey = old, oldKey
	})
	return s, &listens
}

// testPeers holds the peer key of each loopback connection to a test
// server, by the client's address, as tailcat would tell it.
var testPeers sync.Map

// dialAs connects to s's remote listener as the client of key k, "" for
// a peer tailcat does not know.
func dialAs(t *testing.T, s *Server, k string) *Client {
	t.Helper()
	s.rem.mu.Lock()
	addr := s.rem.ln.Addr().String()
	s.rem.mu.Unlock()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if k != "" {
		testPeers.Store(conn.LocalAddr().String(), k)
	}
	c := newClient(conn, nil)
	t.Cleanup(func() { c.Close() })
	return c
}

// wantClosed checks that the server drops c.
func wantClosed(t *testing.T, c *Client, what string) {
	t.Helper()
	select {
	case <-c.Closed():
	case <-time.After(3 * time.Second):
		t.Errorf("%s: connection kept", what)
	}
}

// testPeer is the key of a remote client of pipe.
var testPeer = "nodekey:" + strings.Repeat("0", 64)

// pipe connects to s's handler, as a local connection for peer "", else
// as the remote client of key peer.
func pipe(s *Server, peer string) *Client {
	a, b := net.Pipe()
	go s.handle(b, peer)
	return newClient(a, func() (net.Conn, error) {
		a, b := net.Pipe()
		go s.handle(b, peer)
		return a, nil
	})
}

func readConfig(t *testing.T) RemoteConfig {
	t.Helper()
	var cfg RemoteConfig
	b, err := os.ReadFile(remotePath())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func waitRunning(t *testing.T, c *Client, want bool) RemoteStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, err := c.RemoteStatus()
		if err != nil {
			t.Fatal(err)
		}
		if st.Running == want || time.Now().After(deadline) {
			if st.Running != want {
				t.Fatalf("running %v, want %v: %+v", st.Running, want, st)
			}
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func testShell() (cmd []string, dir string) {
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe"}, os.TempDir()
	}
	return []string{"/bin/sh"}, "/tmp"
}

func TestRemoteWithoutClients(t *testing.T) {
	s, listens := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	if err := c.EnableRemote(); err != nil {
		t.Fatalf("enable without clients: %v", err)
	}
	if st := waitRunning(t, c, true); !st.Enabled || len(st.Allowed) != 0 {
		t.Errorf("status %+v", st)
	}
	if cfg := readConfig(t); !cfg.Enabled {
		t.Error("not saved enabled")
	}
	if listens.Load() != 1 {
		t.Error("not listening")
	}
	// Nobody gets in: not a stranger, nor a peer of no key.
	for _, k := range []string{key.NewNode().Public().String(), ""} {
		rc := dialAs(t, s, k)
		if _, err := rc.List(); err == nil {
			t.Errorf("%q listed", k)
		}
		wantClosed(t, rc, "stranger")
	}
	if st, _ := c.RemoteStatus(); len(st.Pending) != 0 || st.Pairing.Open {
		t.Errorf("status %+v", st)
	}
	if err := c.AllowRemote("phone", "nodekey:nope"); err == nil {
		t.Error("allowed a bad key")
	}
	if err := c.AllowRemote(" ", key.NewNode().Public().String()); err == nil {
		t.Error("allowed a client without a name")
	}
}

func TestRemoteLifecycle(t *testing.T) {
	s, listens := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	phone, laptop := key.NewNode().Public().String(), key.NewNode().Public().String()
	if err := c.AllowRemote("phone", phone); err != nil {
		t.Fatal(err)
	}
	if err := c.AllowRemote("phone again", phone); err == nil {
		t.Error("allowed twice")
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(remotePath()); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("config mode %v %v", st.Mode(), err)
		}
	}
	if err := c.EnableRemote(); err != nil {
		t.Fatal(err)
	}
	st := waitRunning(t, c, true)
	if st.Address != "tcFAKE" || len(st.Allowed) != 1 || st.Allowed[0].Name != "phone" {
		t.Errorf("status %+v", st)
	}
	cfg := readConfig(t)
	if !cfg.Enabled || cfg.NodeKey.IsZero() || cfg.PresharedKey.IsZero() || cfg.Region != 7 {
		t.Errorf("config enabled %v, keys %v %v, region %d", cfg.Enabled, !cfg.NodeKey.IsZero(), !cfg.PresharedKey.IsZero(), cfg.Region)
	}

	// Allowing another keeps the listener; enabling again too.
	if err := c.AllowRemote("laptop", laptop); err != nil {
		t.Fatal(err)
	}
	if err := c.EnableRemote(); err != nil {
		t.Fatal(err)
	}
	if n := listens.Load(); n != 1 {
		t.Errorf("listened %d times", n)
	}

	// A remote connection, through the listener.
	rc := dialAs(t, s, phone)
	if _, err := rc.List(); err != nil {
		t.Fatal(err)
	}
	if err := rc.Shutdown(); err == nil {
		t.Error("remote shutdown")
	}
	if _, err := rc.RemoteStatus(); err == nil {
		t.Error("remote status")
	}

	// Regenerating makes new keys and listens again.
	if err := c.RegenerateRemote(); err != nil {
		t.Fatal(err)
	}
	if readConfig(t).NodeKey.Equal(cfg.NodeKey) {
		t.Error("same key")
	}
	waitRunning(t, c, true)
	if n := listens.Load(); n != 2 {
		t.Errorf("listened %d times", n)
	}
	wantClosed(t, rc, "regenerate")

	// Revoking one drops its connections only, and keeps listening;
	// revoking the last too.
	rp, rl := dialAs(t, s, phone), dialAs(t, s, laptop)
	for _, x := range []*Client{rp, rl} {
		if _, err := x.List(); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.RevokeRemote(phone); err != nil {
		t.Fatal(err)
	}
	if err := c.RevokeRemote(phone); err == nil {
		t.Error("revoked twice")
	}
	wantClosed(t, rp, "revoke")
	if _, err := rl.List(); err != nil {
		t.Errorf("the other client dropped: %v", err)
	}
	if _, err := dialAs(t, s, phone).List(); err == nil {
		t.Error("a revoked client listed")
	}
	if st := waitRunning(t, c, true); len(st.Allowed) != 1 || st.Allowed[0].PublicKey != laptop {
		t.Errorf("status %+v", st)
	}
	if err := c.RevokeRemote(laptop); err != nil {
		t.Fatal(err)
	}
	wantClosed(t, rl, "revoke last")
	if st := waitRunning(t, c, true); !st.Enabled || len(st.Allowed) != 0 {
		t.Errorf("status %+v", st)
	}
	if n := listens.Load(); n != 2 {
		t.Errorf("listened %d times", n)
	}
}

func TestRemotePairing(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	if err := c.StartPairing(); err == nil {
		t.Error("paired while off")
	}
	if err := c.EnableRemote(); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, c, true)
	phone := key.NewNode().Public().String()

	// No window: nothing, not even a request.
	rc := dialAs(t, s, phone)
	if _, err := rc.RequestPairing("phone"); err == nil || !strings.Contains(err.Error(), "not pairing") {
		t.Errorf("paired without a window: %v", err)
	}
	wantClosed(t, rc, "no window")
	if st, _ := c.RemoteStatus(); st.Pairing.Open || len(st.Pending) != 0 {
		t.Errorf("status %+v", st)
	}

	// A window: a request, and nothing else.
	if err := c.StartPairing(); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"hello", "list", "create", "kill", "resize", "remote.status", "remote.approve"} {
		rc := dialAs(t, s, phone)
		req := Request{Op: op, Create: &CreateOptions{}, Remote: &RemoteClient{PublicKey: phone}}
		if err := rc.call(req, nil); err == nil || !strings.Contains(err.Error(), "before pairing") {
			t.Errorf("%s before pairing: %v", op, err)
		}
		wantClosed(t, rc, op)
	}
	ac := dialAs(t, s, phone)
	b, _ := json.Marshal(Attach{Op: "attach", SID: "x", Cols: 80, Rows: 24})
	ac.conn.Write(append(b, '\n'))
	wantClosed(t, ac, "attach")
	if _, err := dialAs(t, s, phone).RequestPairing(" \x1b\n "); err == nil {
		t.Error("paired without a name")
	}
	name := "\x1b[31mphone" + strings.Repeat("x", 60)
	fp, err := dialAs(t, s, phone).RequestPairing(name)
	if err != nil {
		t.Fatal(err)
	}
	if fp != Fingerprint(phone) || len(fp) != 13 {
		t.Errorf("fingerprint %q, want %q", fp, Fingerprint(phone))
	}
	if _, err := dialAs(t, s, phone).RequestPairing("again"); err != nil {
		t.Fatal(err)
	}
	st, _ := c.RemoteStatus()
	if !st.Pairing.Open || time.Until(st.Pairing.ExpiresAt) < 4*time.Minute || len(st.Pending) != 1 {
		t.Fatalf("status %+v", st)
	}
	if p := st.Pending[0]; p.PublicKey != phone || p.Name != "[31mphone"+strings.Repeat("x", 31) || len(st.Allowed) != 0 {
		t.Errorf("pending %+v", p)
	}
	if b, _ := os.ReadFile(remotePath()); strings.Contains(string(b), phone) {
		t.Error("a request saved")
	}

	// Approving allows it, by the name it asked with.
	if err := c.ApprovePairing(key.NewNode().Public().String()); err == nil {
		t.Error("approved a stranger")
	}
	if err := c.ApprovePairing(phone); err != nil {
		t.Fatal(err)
	}
	asked := st.Pending[0].Name
	if st, _ := c.RemoteStatus(); len(st.Pending) != 0 || len(st.Allowed) != 1 || st.Allowed[0].Name != asked || st.Allowed[0].PublicKey != phone {
		t.Errorf("status %+v", st)
	}
	if cfg := readConfig(t); len(cfg.Allowed) != 1 || cfg.Allowed[0].PublicKey != phone {
		t.Errorf("config %+v", cfg)
	}
	rc = dialAs(t, s, phone)
	if _, err := rc.RequestPairing("phone"); err == nil || !strings.Contains(err.Error(), "allowed") {
		t.Errorf("paired twice: %v", err)
	}
	cmd, dir := testShell()
	info, err := rc.Create(CreateOptions{Command: cmd, Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if list, err := rc.List(); err != nil || len(list) != 1 {
		t.Errorf("list %v %v", list, err)
	}
	if err := rc.Kill(info.ID); err != nil {
		t.Fatal(err)
	}

	// The requests: deduped, at most maxPending, rejected one by one.
	var keys []string
	for i := range maxPending + 1 {
		k := key.NewNode().Public().String()
		keys = append(keys, k)
		_, err := dialAs(t, s, k).RequestPairing("laptop")
		if i < maxPending && err != nil || i == maxPending && err == nil {
			t.Errorf("request %d: %v", i, err)
		}
	}
	if st, _ := c.RemoteStatus(); len(st.Pending) != maxPending {
		t.Errorf("%d pending", len(st.Pending))
	}
	if err := c.RejectPairing(keys[0]); err != nil {
		t.Fatal(err)
	}
	if err := c.RejectPairing(keys[0]); err == nil {
		t.Error("rejected twice")
	}
	if err := c.ApprovePairing(keys[0]); err == nil {
		t.Error("approved a rejected one")
	}
	if st, _ := c.RemoteStatus(); len(st.Pending) != maxPending-1 {
		t.Errorf("%d pending", len(st.Pending))
	}

	// Stopping forgets them; so does the window's end.
	if err := c.StopPairing(); err != nil {
		t.Fatal(err)
	}
	if st, _ := c.RemoteStatus(); st.Pairing.Open || len(st.Pending) != 0 {
		t.Errorf("status %+v", st)
	}
	if err := c.StartPairing(); err != nil {
		t.Fatal(err)
	}
	if _, err := dialAs(t, s, keys[1]).RequestPairing("laptop"); err != nil {
		t.Fatal(err)
	}
	s.rem.mu.Lock()
	s.rem.pairUntil = time.Now().Add(-time.Second)
	s.rem.mu.Unlock()
	if st, _ := c.RemoteStatus(); st.Pairing.Open || len(st.Pending) != 0 {
		t.Errorf("status after expiry %+v", st)
	}
	rc = dialAs(t, s, keys[1])
	if _, err := rc.RequestPairing("laptop"); err == nil {
		t.Error("paired after expiry")
	}
	if err := c.ApprovePairing(keys[1]); err == nil {
		t.Error("approved after expiry")
	}

	// Turning it off closes the window.
	if err := c.StartPairing(); err != nil {
		t.Fatal(err)
	}
	if err := c.DisableRemote(); err != nil {
		t.Fatal(err)
	}
	if st, _ := c.RemoteStatus(); st.Pairing.Open {
		t.Errorf("status %+v", st)
	}
}

func TestPairName(t *testing.T) {
	for in, want := range map[string]string{
		"alice-laptop":          "alice-laptop",
		"  a\tb\x00c\u200bd  ":  "abcd",
		strings.Repeat("é", 50): strings.Repeat("é", 40),
		"\n":                    "",
	} {
		if got := pairName(in); got != want {
			t.Errorf("pairName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Fingerprint("nodekey:" + strings.Repeat("0123456789abcdef", 4)); got != "01234567-cdef" {
		t.Errorf("Fingerprint = %q", got)
	}
	if Fingerprint("nope") != "" {
		t.Error("fingerprint of no key")
	}
}

func TestRemoteDenied(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, testPeer)
	defer c.Close()
	if _, err := c.Hello(); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"shutdown", "setLayout", "getLayout", "remote.status", "remote.enable",
		"remote.disable", "remote.allow", "remote.revoke", "remote.regenerate", "remote.pairStart",
		"remote.pairStop", "remote.approve", "remote.reject", "remote.shareSet", "nope"} {
		// On, as a remote client would want sharing.
		req := Request{Op: op, On: true, Layout: json.RawMessage(`{}`), Remote: &RemoteClient{Name: "x", PublicKey: key.NewNode().Public().String()}}
		if err := c.call(req, nil); err == nil || !strings.Contains(err.Error(), "not allowed remotely") {
			t.Errorf("%s: %v", op, err)
		}
	}
	if _, err := os.Stat(remotePath()); !os.IsNotExist(err) {
		t.Errorf("config written: %v", err)
	}
	select {
	case <-s.quit:
		t.Fatal("shut down")
	case <-time.After(100 * time.Millisecond):
	}
	cmd, dir := testShell()
	info, err := c.Create(CreateOptions{Command: cmd, Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if list, err := c.List(); err != nil || len(list) != 1 {
		t.Fatalf("list %v %v", list, err)
	}
	if err := c.Resize(info.ID, 100, 30); err != nil {
		t.Fatal(err)
	}
	if err := c.Kill(info.ID); err != nil {
		t.Fatal(err)
	}
}

// readsUntilClosed reads st in the background and tells, on the channel,
// its error once it ends.
func readsUntilClosed(st *Stream) <-chan error {
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 32<<10)
		for {
			if _, err := st.Read(buf); err != nil {
				done <- err
				return
			}
		}
	}()
	return done
}

// TestShareSessions checks what remote clients see and use of the sessions
// of others, as the host shares them or not: local connections see all.
func TestShareSessions(t *testing.T) {
	s, _ := testServer(t)
	local := pipe(s, "")
	defer local.Close()
	alice, bob := key.NewNode().Public().String(), key.NewNode().Public().String()
	ac, bc := pipe(s, alice), pipe(s, bob)
	defer ac.Close()
	defer bc.Close()
	if st, err := local.RemoteStatus(); err != nil || st.ShareSessions {
		t.Fatalf("shared by default: %+v %v", st, err)
	}
	cmd, dir := testShell()
	create := func(c *Client) string {
		t.Helper()
		info, err := c.Create(CreateOptions{Command: cmd, Dir: dir, Cols: 80, Rows: 24})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { local.Kill(info.ID) })
		return info.ID
	}
	ls, as, bs := create(local), create(ac), create(bc)
	ids := func(c *Client) []string {
		t.Helper()
		list, err := c.List()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, in := range list {
			out = append(out, in.ID)
		}
		return out
	}
	wantIDs := func(what string, c *Client, want ...string) {
		t.Helper()
		if got := ids(c); !slices.Equal(got, want) {
			t.Errorf("%s lists %v, want %v", what, got, want)
		}
	}
	notShared := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "not shared") || !NotShared(err) {
			t.Errorf("%s: %v", what, err)
		}
	}

	// Off: each remote client its own; the local one all.
	wantIDs("local", local, ls, as, bs)
	wantIDs("alice", ac, as)
	wantIDs("bob", bc, bs)
	for _, sid := range []string{ls, bs} {
		notShared("kill "+sid, ac.Kill(sid))
		notShared("resize "+sid, ac.Resize(sid, 100, 30))
		st := ac.Stream(sid, 80, 24)
		st.Resize(80, 24)
		notShared("attach "+sid, <-readsUntilClosed(st))
	}
	if err := ac.Resize(as, 100, 30); err != nil {
		t.Error(err)
	}
	if err := local.Resize(bs, 100, 30); err != nil {
		t.Error(err)
	}
	wantIDs("local after", local, ls, as, bs)

	// On: every client all, saved.
	if err := local.ShareSessions(true); err != nil {
		t.Fatal(err)
	}
	if st, _ := local.RemoteStatus(); !st.ShareSessions || !readConfig(t).ShareSessions {
		t.Errorf("not shared: status %+v", st)
	}
	wantIDs("alice shared", ac, ls, as, bs)
	if err := ac.Resize(ls, 90, 30); err != nil {
		t.Error(err)
	}
	theirs, mine := ac.Stream(bs, 80, 24), ac.Stream(as, 80, 24)
	defer theirs.Close()
	defer mine.Close()
	var ends []<-chan error
	for _, st := range []*Stream{theirs, mine} {
		st.Resize(80, 24)
		ends = append(ends, readsUntilClosed(st))
	}
	waitAttached := func(sid string, n int) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			s.mu.Lock()
			ss := s.sessions[sid]
			s.mu.Unlock()
			if ss.info().Attached == n {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: not %d attached", sid, n)
			}
		}
	}
	waitAttached(bs, 1)
	waitAttached(as, 1)

	// Off again: alice's window on bob's session goes, hers stays.
	if err := local.ShareSessions(false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ends[0]:
	case <-time.After(3 * time.Second):
		t.Error("the window on another's session stays")
	}
	select {
	case err := <-ends[1]:
		t.Errorf("the window on her own session went: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	waitAttached(as, 1)
	wantIDs("alice unshared", ac, as)
	if readConfig(t).ShareSessions {
		t.Error("saved shared")
	}

	// Revoked and allowed again, the same key has its sessions still.
	if err := local.EnableRemote(); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, local, true)
	if err := local.AllowRemote("alice-laptop", alice); err != nil {
		t.Fatal(err)
	}
	wantIDs("alice through the listener", dialAs(t, s, alice), as)
	if err := local.RevokeRemote(alice); err != nil {
		t.Fatal(err)
	}
	if err := local.AllowRemote("alice-laptop", alice); err != nil {
		t.Fatal(err)
	}
	wantIDs("alice allowed again", dialAs(t, s, alice), as)
}

func TestLocalAllowed(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	if err := c.SetLayout(json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if l, err := c.Layout(); err != nil || !bytes.Equal(l, []byte(`{"a":1}`)) {
		t.Fatalf("layout %s %v", l, err)
	}
	if _, err := c.RemoteStatus(); err != nil {
		t.Fatal(err)
	}
	if err := c.DisableRemote(); err != nil {
		t.Fatal(err)
	}
	if err := c.Shutdown(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.quit:
	case <-time.After(2 * time.Second):
		t.Fatal("not shut down")
	}
}

func TestClientKey(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	k := ClientPublicKey()
	if _, err := parsePublicKey(k); err != nil {
		t.Fatalf("%q: %v", k, err)
	}
	if again := ClientPublicKey(); again != k {
		t.Error("key changed")
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(filepath.Join(Dir(), "remote-client.json")); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("mode %v %v", st.Mode(), err)
		}
	}
	for _, addr := range []string{"", "nope", "tc!!"} {
		if _, err := ConnectRemote(context.Background(), addr); err == nil {
			t.Errorf("connected to %q", addr)
		}
	}
}

// TestRemoteTailcat connects through tailcat for real: it needs the
// internet, for the DERP relays.
func TestRemoteTailcat(t *testing.T) {
	if os.Getenv("GOREX_TEST_TAILCAT") != "1" {
		t.Skip("set GOREX_TEST_TAILCAT=1 to connect through tailcat")
	}
	base := "/tmp"
	if runtime.GOOS == "windows" {
		base = ""
	}
	dir, err := os.MkdirTemp(base, "rex")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("GOREX_DIR", dir)
	go Serve()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(filepath.Join(dir, "server.sock")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	c, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	// On with no client: pairing is how one gets in.
	if err := c.EnableRemote(); err != nil {
		t.Fatal(err)
	}
	var st RemoteStatus
	for deadline := time.Now().Add(60 * time.Second); !st.Running; time.Sleep(200 * time.Millisecond) {
		if st, err = c.RemoteStatus(); err != nil || time.Now().After(deadline) {
			t.Fatalf("status %v %v %q", st.Running, err, st.LastError)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A client not allowed gets nothing, not even a request.
	stranger := &tailcat.Client{Server: tailcat.Addr(st.Address), Key: key.NewNode(), Logf: func(string, ...any) {}}
	if conn, err := stranger.DialTCPPort(ctx, RemotePort); err == nil {
		conn.Write([]byte(`{"id":1,"op":"list"}` + "\n"))
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if n, _ := conn.Read(make([]byte, 64)); n != 0 {
			t.Error("a stranger got an answer")
		}
		conn.Close()
	}
	stranger.Close()
	pair := func() (string, error) {
		rc, err := ConnectRemote(ctx, st.Address)
		if err != nil {
			return "", err
		}
		defer rc.Close()
		return rc.RequestPairing("self")
	}
	if _, err := pair(); err == nil || !strings.Contains(err.Error(), "not pairing") {
		t.Errorf("paired without a window: %v", err)
	}

	// Pairing: the server reads the peer's key from tailcat.
	if err := c.StartPairing(); err != nil {
		t.Fatal(err)
	}
	fp, err := pair()
	if err != nil {
		t.Fatal(err)
	}
	if fp != Fingerprint(ClientPublicKey()) {
		t.Errorf("fingerprint %q, want %q", fp, Fingerprint(ClientPublicKey()))
	}
	if st, err := c.RemoteStatus(); err != nil || len(st.Pending) != 1 || st.Pending[0].PublicKey != ClientPublicKey() {
		t.Fatalf("pending %+v %v", st.Pending, err)
	}
	if err := c.ApprovePairing(ClientPublicKey()); err != nil {
		t.Fatal(err)
	}

	rc, err := ConnectRemote(ctx, st.Address)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if _, err := rc.Hello(); err != nil {
		t.Fatal(err)
	}
	if err := rc.Shutdown(); err == nil {
		t.Error("remote shutdown")
	}
	cmd, work := testShell()
	typed := "echo he''llo\n"
	if runtime.GOOS == "windows" {
		typed = "echo he^llo\r"
	}
	info, err := rc.Create(CreateOptions{Command: cmd, Dir: work, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	s := rc.Stream(info.ID, 80, 24)
	s.Resize(80, 24)
	s.Write([]byte(typed))
	var out bytes.Buffer
	buf := make([]byte, 4096)
	for deadline := time.Now().Add(15 * time.Second); !strings.Contains(out.String(), "hello") && time.Now().Before(deadline); {
		n, err := s.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if !strings.Contains(out.String(), "hello") {
		t.Errorf("output %q", out.String())
	}
	s.Close()
	if err := rc.Kill(info.ID); err != nil {
		t.Fatal(err)
	}

	// Revoking it drops its connection.
	if err := c.RevokeRemote(ClientPublicKey()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rc.Closed():
	case <-time.After(10 * time.Second):
		t.Error("connection kept after revoke")
	}
}

// TestRemoteRefusal checks that a client not allowed is told so, rather
// than finding its connection lost, and whether its request waits.
func TestRemoteRefusal(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	if err := c.EnableRemote(); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, c, true)
	phone := key.NewNode().Public().String()
	hello := func() error {
		_, err := dialAs(t, s, phone).Hello()
		return err
	}
	if err := hello(); err == nil || !IsRefused(err) || IsPairingPending(err) {
		t.Errorf("no window: %v", err)
	}
	if err := c.StartPairing(); err != nil {
		t.Fatal(err)
	}
	if err := hello(); err == nil || !IsRefused(err) || IsPairingPending(err) {
		t.Errorf("no request: %v", err)
	}
	if _, err := dialAs(t, s, phone).RequestPairing("phone"); err != nil {
		t.Fatal(err)
	}
	if err := hello(); err == nil || !IsPairingPending(err) {
		t.Errorf("a request waiting: %v", err)
	}
	if err := c.RejectPairing(phone); err != nil {
		t.Fatal(err)
	}
	if err := hello(); err == nil || !IsRefused(err) || IsPairingPending(err) {
		t.Errorf("rejected: %v", err)
	}
	if IsRefused(errors.New("rex: connection to the server lost")) {
		t.Error("a connection lost counts as a refusal")
	}
}

// TestRemoteControlGatesActions checks that a remote action is refused
// until the server's Remote Control is on.
func TestRemoteControlGatesActions(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, testPeer)
	defer c.Close()
	if _, err := c.Action("", "tab.rename", nil); err == nil || !strings.Contains(err.Error(), "not allowed remotely") {
		t.Fatalf("action with control off: %v", err)
	}
	s.rem.mu.Lock()
	s.rem.cfg.RemoteControl = true
	s.rem.mu.Unlock()
	res, err := c.Action("", "tab.rename", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != "desktop_unavailable" {
		t.Fatalf("action with control on: %+v", res)
	}
}
