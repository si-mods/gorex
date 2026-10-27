package coord

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorex/internal/coordproto"

	"tailscale.com/types/key"
)

// testNetwork runs a coordinator on its own directory and port, with
// the certificate init made; clients reach it only by its pinned join
// URL.
func testNetwork(t *testing.T) (joinURL, networkKey, dir string) {
	t.Helper()
	dir = t.TempDir()
	networkKey, fingerprint, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.LoadX509KeyPair(dir+"/tls.crt", dir+"/tls.key")
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(dir)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsLn := tls.NewListener(ln, &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	})
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(tlsLn)
	t.Cleanup(func() { srv.Close() })
	return coordproto.JoinURL(tlsLn.Addr().String(), fingerprint), networkKey, dir
}

func testClient(t *testing.T, joinURL, networkKey string) *coordproto.Client {
	t.Helper()
	c, err := coordproto.NewClient(joinURL, networkKey)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// testNodeKey is a fresh public key in its canonical form.
func testNodeKey() string { return key.NewNode().Public().String() }

// testAddr makes a tailcat address that reads: a public key in the
// wire form, hand-packed so the test needs no CBOR.
func testAddr() string {
	pub := key.NewNode().Public().AppendTo(nil)
	b := append([]byte{0xA1, 0x61, 'p', 0x58, byte(len(pub))}, pub...)
	return "tc" + base64.RawURLEncoding.EncodeToString(b)
}

func TestJoinAndMembers(t *testing.T) {
	joinURL, networkKey, _ := testNetwork(t)
	ctx := context.Background()
	c := testClient(t, joinURL, networkKey)

	a, b := testNodeKey(), testNodeKey()
	if a == b {
		t.Fatal("two fresh keys read the same")
	}
	ms, rev, err := c.Join(ctx, "alice", a, testAddr())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].Name != "alice" || ms[0].NodeKey != a {
		t.Fatalf("join answered %+v", ms)
	}
	ms, rev2, err := c.Join(ctx, "bob", b, testAddr())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || rev2 <= rev {
		t.Fatalf("second join answered %+v at %d after %d", ms, rev2, rev)
	}

	// Unchanged since: no answer to parse.
	if _, _, changed, err := c.Members(ctx, b, rev2); err != nil || changed {
		t.Fatalf("members since the current revision: changed=%v err=%v", changed, err)
	}
	// A member's poll marks it seen.
	if _, _, changed, err := c.Members(ctx, b, rev2-1); err != nil || !changed {
		t.Fatalf("members since an old revision: changed=%v err=%v", changed, err)
	}

	// Kick, and the directory the others poll loses the member.
	if err := c.Kick(ctx, "", "alice"); err != nil {
		t.Fatal(err)
	}
	ms, _, changed, err := c.Members(ctx, b, 0)
	if err != nil || !changed || len(ms) != 1 || ms[0].Name != "bob" {
		t.Fatalf("after the kick: changed=%v members=%+v err=%v", changed, ms, err)
	}

	// Leave; leaving again misses.
	if err := c.Leave(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := c.Leave(ctx, b); err == nil || !strings.Contains(err.Error(), "no such member") {
		t.Fatalf("leaving twice: %v", err)
	}
}

func TestJoinRefuses(t *testing.T) {
	joinURL, networkKey, _ := testNetwork(t)
	ctx := context.Background()
	c := testClient(t, joinURL, networkKey)

	tests := []struct {
		caseName, name, nodeKey, addr string
		want                          string
	}{
		{"bad key", "alice", "not-a-key", testAddr(), "public key"},
		{"bad address", "alice", testNodeKey(), "not-an-address", "address"},
		{"empty name", "", testNodeKey(), testAddr(), "name"},
		{"blank name", "  ", testNodeKey(), testAddr(), "name"},
	}
	for _, tt := range tests {
		_, _, err := c.Join(ctx, tt.name, tt.nodeKey, tt.addr)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: got %v, want the coordinator to refuse it", tt.caseName, err)
		}
	}

	// A second machine under a taken name is refused; the same machine
	// may rejoin, refreshing its address.
	k, first := testNodeKey(), testAddr()
	if _, _, err := c.Join(ctx, "alice", k, first); err != nil {
		t.Fatal(err)
	}
	second := testAddr()
	if _, _, err := c.Join(ctx, "alice", k, second); err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	ms, _, _, err := c.Members(ctx, "", 0)
	if err != nil || len(ms) != 1 || ms[0].Address != second || ms[0].NodeKey != k {
		t.Fatalf("after the rejoin: %+v err=%v", ms, err)
	}
	if _, _, err := c.Join(ctx, "alice", testNodeKey(), testAddr()); err == nil || !strings.Contains(err.Error(), "taken") {
		t.Fatalf("a taken name under another key: %v", err)
	}
}

func TestWrongKey(t *testing.T) {
	joinURL, networkKey, _ := testNetwork(t)
	ctx := context.Background()
	wrong := testClient(t, joinURL, "a wrong key")
	for range maxKeyFails {
		if _, _, err := wrong.Join(ctx, "eve", testNodeKey(), testAddr()); err == nil {
			t.Fatal("a wrong key joined")
		}
	}
	// The address is now locked out of guessing; the right key from it
	// still comes through.
	if _, _, err := wrong.Join(ctx, "eve", testNodeKey(), testAddr()); err == nil {
		t.Fatal("a locked out address kept guessing")
	}
	right := testClient(t, joinURL, networkKey)
	if _, _, err := right.Join(ctx, "alice", testNodeKey(), testAddr()); err != nil {
		t.Fatalf("the right key after a lockout: %v", err)
	}
}

func TestPin(t *testing.T) {
	joinURL, networkKey, _ := testNetwork(t)
	host, _, err := coordproto.SplitJoinURL(joinURL)
	if err != nil {
		t.Fatal(err)
	}
	c, err := coordproto.NewClient(coordproto.JoinURL(host, "0123456789abcdef"), networkKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Join(context.Background(), "alice", testNodeKey(), testAddr()); err == nil {
		t.Fatal("a wrong fingerprint joined")
	}
}

func TestStatePersists(t *testing.T) {
	joinURL, networkKey, dir := testNetwork(t)
	c := testClient(t, joinURL, networkKey)
	k := testNodeKey()
	if _, _, err := c.Join(context.Background(), "alice", k, testAddr()); err != nil {
		t.Fatal(err)
	}
	// A server started over the same directory keeps the member, and
	// the offline readers see it too.
	ms, err := Members(dir)
	if err != nil || len(ms) != 1 || ms[0].NodeKey != k {
		t.Fatalf("offline members: %+v err=%v", ms, err)
	}
	s2, err := NewServer(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Revision() == 0 {
		t.Fatal("the reloaded server forgot the revision")
	}
	if err := s2.Kick("alice", ""); err != nil {
		t.Fatalf("the reloaded server cannot kick: %v", err)
	}
	if ms, _ := Members(dir); len(ms) != 0 {
		t.Fatalf("after the offline kick: %+v", ms)
	}
}

func TestInitTwice(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Init(dir); err == nil {
		t.Fatal("a second init made a second network")
	}
}

func TestCleanName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"alice", "alice"},
		{"  alice  ", "alice"},
		{"a\x00b", "ab"},
		{"汉字", ""},
		{strings.Repeat("x", MaxName+5), strings.Repeat("x", MaxName)},
	}
	for _, tt := range tests {
		if got := CleanName(tt.in); got != tt.want {
			t.Errorf("CleanName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHealth(t *testing.T) {
	joinURL, _, _ := testNetwork(t)
	host, _, _ := coordproto.SplitJoinURL(joinURL)
	// health answers without a key and holds no secrets.
	// The pinned client cannot fetch it (it is not in the protocol), so
	// ask plainly; the certificate is unknown to the default transport,
	// so take the insecure path for this one request.
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	resp, err := client.Get("https://" + host + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health answered %d", resp.StatusCode)
	}
}

func TestCheckKey(t *testing.T) {
	s := &state{KeyMAC: fmt.Sprintf("%x", keyMAC("right key"))}
	if !checkKey(s, "right key") || checkKey(s, "wrong key") || checkKey(s, "") {
		t.Fatal("checkKey told the keys apart wrongly")
	}
}

// TestConcurrentJoins hammers join and members together: the lock keeps
// the directory whole.
func TestConcurrentJoins(t *testing.T) {
	joinURL, networkKey, _ := testNetwork(t)
	var ok atomic.Int32
	done := make(chan struct{})
	for i := range 4 {
		go func(i int) {
			c, err := coordproto.NewClient(joinURL, networkKey)
			if err != nil {
				t.Error(err)
				return
			}
			for range 10 {
				if _, _, err := c.Join(context.Background(), fmt.Sprintf("m%d", i), testNodeKey(), testAddr()); err != nil {
					t.Error(err)
					return
				}
				if _, _, _, err := c.Members(context.Background(), "", 0); err != nil {
					t.Error(err)
					return
				}
				ok.Add(1)
			}
			done <- struct{}{}
		}(i)
	}
	for range 4 {
		<-done
	}
	if ok.Load() != 40 {
		t.Fatalf("only %d of 40 rounds came through", ok.Load())
	}
}
