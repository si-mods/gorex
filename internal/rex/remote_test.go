//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	old := remoteListen
	remoteListen = func(ctx context.Context, ts *tailcat.Server, region int) (net.Listener, tailcat.Addr, int, error) {
		listens.Add(1)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		return ln, "tcFAKE", 7, err
	}
	hostDone := make(chan struct{})
	close(hostDone)
	s := &Server{sessions: map[string]*session{}, started: time.Now(), hostDone: hostDone, quit: make(chan struct{})}
	t.Cleanup(func() {
		s.remoteClose()
		remoteListen = old
	})
	return s, &listens
}

// pipe connects to s's handler, as a local or remote connection.
func pipe(s *Server, remote bool) *Client {
	a, b := net.Pipe()
	go s.handle(b, remote)
	return newClient(a, nil)
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

func TestRemoteNeedsAllowlist(t *testing.T) {
	s, listens := testServer(t)
	c := pipe(s, false)
	defer c.Close()
	if err := c.EnableRemote(); err == nil || !strings.Contains(err.Error(), "allow a client") {
		t.Fatalf("enable without clients: %v", err)
	}
	if st, _ := c.RemoteStatus(); st.Enabled || st.Running {
		t.Errorf("status %+v", st)
	}
	if _, err := os.Stat(remotePath()); !os.IsNotExist(err) {
		t.Errorf("config written: %v", err)
	}
	if listens.Load() != 0 {
		t.Error("listened")
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
	c := pipe(s, false)
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
	s.rem.mu.Lock()
	addr := s.rem.ln.Addr().String()
	s.rem.mu.Unlock()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	rc := newClient(conn, nil)
	defer rc.Close()
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
	select {
	case <-rc.Closed():
	case <-time.After(2 * time.Second):
		t.Error("remote connection kept after regenerate")
	}

	// Revoking one keeps it on; revoking the last turns it off.
	if err := c.RevokeRemote(phone); err != nil {
		t.Fatal(err)
	}
	if err := c.RevokeRemote(phone); err == nil {
		t.Error("revoked twice")
	}
	if st := waitRunning(t, c, true); len(st.Allowed) != 1 || st.Allowed[0].PublicKey != laptop {
		t.Errorf("status %+v", st)
	}
	if err := c.RevokeRemote(laptop); err != nil {
		t.Fatal(err)
	}
	if st := waitRunning(t, c, false); st.Enabled || st.Address != "" {
		t.Errorf("status %+v", st)
	}
	if cfg := readConfig(t); cfg.Enabled || len(cfg.Allowed) != 0 {
		t.Errorf("config %+v", cfg)
	}
}

func TestRemoteDenied(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, true)
	defer c.Close()
	if _, err := c.Hello(); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"shutdown", "setLayout", "getLayout", "remote.status", "remote.enable",
		"remote.disable", "remote.allow", "remote.revoke", "remote.regenerate", "nope"} {
		req := Request{Op: op, Layout: json.RawMessage(`{}`), Remote: &RemoteClient{Name: "x", PublicKey: key.NewNode().Public().String()}}
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

func TestLocalAllowed(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, false)
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
	if err := c.AllowRemote("self", ClientPublicKey()); err != nil {
		t.Fatal(err)
	}
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

	// A client not allowed gets nothing.
	stranger := &tailcat.Client{Server: tailcat.Addr(st.Address), Logf: func(string, ...any) {}}
	if conn, err := stranger.DialTCPPort(ctx, RemotePort); err == nil {
		conn.Close()
		t.Error("a stranger connected")
	}
	stranger.Close()

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
