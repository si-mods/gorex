//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

func TestLineLimit(t *testing.T) {
	s, _ := testServer(t)
	big := append(bytes.Repeat([]byte("x"), maxLine+1), '\n')
	for _, name := range []string{"first line", "later line"} {
		a, b := net.Pipe()
		go s.handle(b, false)
		r := bufio.NewReader(a)
		if name == "later line" {
			a.Write([]byte(`{"op":"hello","id":1}` + "\n"))
			if _, err := r.ReadString('\n'); err != nil {
				t.Fatal(err)
			}
		}
		go a.Write(big)
		line, err := r.ReadString('\n')
		if err != nil || !strings.Contains(line, "too long") {
			t.Errorf("%s: %q %v", name, line, err)
		}
		if _, err := r.ReadString('\n'); err == nil {
			t.Errorf("%s: the connection stayed open", name)
		}
		a.Close()
	}
}

func TestClampSize(t *testing.T) {
	for in, want := range map[int]int{-5: 1, 0: 1, 1: 1, 80: 80, 1000: 1000, 1001: 1000, 1 << 30: 1000} {
		if got := clampSize(in); got != want {
			t.Errorf("clampSize(%d) = %d, want %d", in, got, want)
		}
	}
	s, _ := testServer(t)
	c := pipe(s, true)
	defer c.Close()
	cmd, dir := testShell()
	info, err := c.Create(CreateOptions{Command: cmd, Dir: dir, Cols: 1 << 20, Rows: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Kill(info.ID)
	if info.Cols != 1000 || info.Rows != 1000 {
		t.Errorf("created at %dx%d", info.Cols, info.Rows)
	}
	if err := c.Resize(info.ID, 5000, -1); err != nil {
		t.Fatal(err)
	}
	list, err := c.List()
	if err != nil || len(list) != 1 || list[0].Cols != 1000 || list[0].Rows != 1 {
		t.Errorf("resized: %+v %v", list, err)
	}
}

func TestKillsAreBounded(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, true)
	defer c.Close()
	var cur, most atomic.Int32
	release := make(chan struct{})
	killHook = func() {
		n := cur.Add(1)
		for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
		}
		<-release
		cur.Add(-1)
	}
	defer func() { killHook = nil }()
	var wg sync.WaitGroup
	for range 3 * maxKills {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Kill("nothing")
		}()
	}
	time.Sleep(300 * time.Millisecond)
	if got := cur.Load(); got != maxKills {
		t.Errorf("%d kills at once, want %d", got, maxKills)
	}
	close(release)
	wg.Wait()
	if most.Load() > maxKills {
		t.Errorf("at most %d kills at once, want %d", most.Load(), maxKills)
	}
}

func TestAddressNotInErrors(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	secret := "tc" + strings.Repeat("Zq9_-x", 12) + "!!"
	_, err := ConnectRemote(context.Background(), secret)
	if err == nil {
		t.Fatal("connected")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), secret[2:30]) {
		t.Errorf("the error holds the address: %v", err)
	}
	got := scrub("dial tcp "+secret+": refused, near "+strings.Repeat("Ab1-", 12), secret)
	if strings.Contains(got, "Ab1-Ab1-") || strings.Contains(got, "Zq9_") || !strings.Contains(got, "refused") {
		t.Errorf("scrub = %q", got)
	}
}

// failAccept is a listener whose accepting fails.
type failAccept struct{ net.Listener }

func (failAccept) Accept() (net.Conn, error) { return nil, errors.New("boom") }

func TestAcceptFailureRestarts(t *testing.T) {
	s, _ := testServer(t)
	oldRetry, oldListen := remoteRetry, remoteListen
	remoteRetry = 400 * time.Millisecond
	defer func() { remoteRetry, remoteListen = oldRetry, oldListen }()
	var calls atomic.Int32
	remoteListen = func(ctx context.Context, ts *tailcat.Server, region int) (net.Listener, tailcat.Addr, int, error) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if calls.Add(1) == 1 {
			ln = failAccept{ln}
		}
		return ln, "tcFAKE", 7, err
	}
	c := pipe(s, false)
	defer c.Close()
	if err := c.AllowRemote("alice-laptop", key.NewNode().Public().String()); err != nil {
		t.Fatal(err)
	}
	if err := c.EnableRemote(); err != nil {
		t.Fatal(err)
	}
	// While it waits to start again, it does not claim to listen.
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, err := c.RemoteStatus()
		if err != nil {
			t.Fatal(err)
		}
		if !st.Running && strings.Contains(st.LastError, "boom") && st.Address == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no error state: %+v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
	st := waitRunning(t, c, true)
	if st.LastError != "" || calls.Load() != 2 {
		t.Errorf("after the restart: %+v, %d listens", st, calls.Load())
	}
}

func TestClientKeyConcurrent(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	keys := make([]string, 16)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			keys[i] = ClientPublicKey()
		}()
	}
	wg.Wait()
	for _, k := range keys {
		if k == "" || k != keys[0] {
			t.Fatalf("keys differ: %q and %q", k, keys[0])
		}
	}
}

func TestNormalizePublicKey(t *testing.T) {
	k := key.NewNode().Public().String()
	if got, err := NormalizePublicKey("  " + strings.ToUpper(k[8:]) + " "); err == nil {
		t.Errorf("accepted a key without its prefix: %q", got)
	}
	if got, err := NormalizePublicKey(" " + k + "\n"); err != nil || got != k {
		t.Errorf("NormalizePublicKey = %q, %v", got, err)
	}
	for _, bad := range []string{"", "nodekey:", "nodekey:abc", "x"} {
		if _, err := NormalizePublicKey(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
