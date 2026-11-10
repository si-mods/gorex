//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

func TestNormalizeRelay(t *testing.T) {
	for in, want := range map[string]string{
		"":                          "",
		"  derp.example.com ":       "derp.example.com",
		"derp.example.com:8443":     "derp.example.com:8443",
		"localhost":                 "localhost",
		"203.0.113.7":               "203.0.113.7",
		"[2001:db8::1]:443":         "[2001:db8::1]:443",
		"https://derp.example.com":  "!",
		"derp.example.com/":         "!",
		"derp.example.com:":         "!",
		"derp.example.com:0":        "!",
		"derp.example.com:70000":    "!",
		"derp..example.com":         "!",
		"-derp.example.com":         "!",
		"derp.example.com:443 junk": "!",
	} {
		got, err := NormalizeRelay(in)
		if want == "!" {
			if err == nil {
				t.Errorf("NormalizeRelay(%q) = %q, want an error", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("NormalizeRelay(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	n := relayRegion("derp.example.com:8443").Nodes[0]
	if n.HostName != "derp.example.com" || n.DERPPort != 8443 || n.STUNPort != 0 || n.InsecureForTests {
		t.Errorf("node %+v", n)
	}
	if n := relayRegion("derp.example.com").Nodes[0]; n.HostName != "derp.example.com" || n.DERPPort != 0 {
		t.Errorf("node %+v", n)
	}
}

// accepted is what an accept of the gateway's got.
type accepted struct {
	c    net.Conn
	peer RemoteClient
	err  error
}

// gatewayAccepts waits on the local socket as the gateway does, once.
func gatewayAccepts(ctx context.Context) <-chan accepted {
	ch := make(chan accepted, 1)
	go func() {
		c, peer, err := AcceptTailcat(ctx)
		ch <- accepted{c, peer, err}
	}()
	return ch
}

// wantEOF checks that c closes within d.
func wantEOF(t *testing.T, c net.Conn, d time.Duration, what string) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(d))
	if _, err := io.ReadAll(c); errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("%s: not closed", what)
	}
}

// TestRemoteGateway: through a relay of its own (an in-process DERP), the
// tailcat address puts the relay in itself, serves RexAPIPort to allowed
// clients only, and hands their connections to the gateway's accept, with
// their key and name; a revoke and remote access off close them, and
// clearing the relay goes back to tailcat's.
func TestRemoteGateway(t *testing.T) {
	reg := tailcatEnv(t)
	s, _ := testServer(t)
	localRelay(t, s, "")
	relayInsecure = true
	oldWait := gatewayWait
	gatewayWait = 2 * time.Second
	t.Cleanup(func() { relayInsecure, gatewayWait = false, oldWait })
	c := pipe(s, "")
	defer c.Close()

	if err := c.SetRelay("https://derp.example.com"); err == nil {
		t.Error("took a URL for a relay")
	}
	relay := fmt.Sprintf("127.0.0.1:%d", reg.Nodes[0].DERPPort)
	if err := c.SetRelay(relay); err != nil {
		t.Fatal(err)
	}
	if err := c.EnableRemote(); err != nil {
		t.Fatal(err)
	}
	st := waitRunning(t, c, true)
	if st.Relay != relay || readConfig(t).Relay != relay {
		t.Errorf("relay %q, saved %q", st.Relay, readConfig(t).Relay)
	}
	ci, err := tailcat.ParseAddr(tailcat.Addr(st.Address))
	if err != nil {
		t.Fatal(err)
	}
	if len(ci.Region) != 1 || len(ci.Region[0].Nodes) != 1 ||
		ci.Region[0].Nodes[0].HostName != "127.0.0.1" || ci.Region[0].Nodes[0].DERPPort != reg.Nodes[0].DERPPort {
		t.Fatalf("the address's relay: %+v", ci.Region)
	}

	phoneKey := key.NewNode()
	if err := c.AllowRemote("phone", phoneKey.Public().String()); err != nil {
		t.Fatal(err)
	}
	client := func(k key.NodePrivate) *tailcat.Client {
		tc := &tailcat.Client{Server: tailcat.Addr(st.Address), Key: k, Logf: func(string, ...any) {}}
		t.Cleanup(func() { tc.Close() })
		return tc
	}
	phone, stranger := client(phoneKey), client(key.NewNode())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dial := func(tc *tailcat.Client) net.Conn {
		t.Helper()
		conn, err := tc.DialTCPPort(ctx, RexAPIPort)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}

	// A stranger gets nothing, and the gateway sees nothing of it.
	acc := gatewayAccepts(ctx)
	sc := dial(stranger)
	sc.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	wantEOF(t, sc, 10*time.Second, "a stranger's connection")
	select {
	case a := <-acc:
		t.Fatalf("the gateway took a stranger's connection: %+v", a)
	default:
	}

	// An allowed client reaches the gateway, named.
	pc := dial(phone)
	pc.Write([]byte("ping\n"))
	a := <-acc
	if a.err != nil {
		t.Fatal(a.err)
	}
	defer a.c.Close()
	if a.peer.PublicKey != phoneKey.Public().String() || a.peer.Name != "phone" {
		t.Errorf("peer %+v", a.peer)
	}
	r := bufio.NewReader(a.c)
	if line, err := r.ReadString('\n'); line != "ping\n" {
		t.Fatalf("the gateway read %q, %v", line, err)
	}
	a.c.Write([]byte("pong\n"))
	if line, err := bufio.NewReader(pc).ReadString('\n'); line != "pong\n" {
		t.Fatalf("the phone read %q, %v", line, err)
	}

	// Remove closes it.
	if err := c.RevokeRemote(phoneKey.Public().String()); err != nil {
		t.Fatal(err)
	}
	wantEOF(t, a.c, 5*time.Second, "revoked")

	// With no accept waiting, a connection closes after gatewayWait.
	if err := c.AllowRemote("phone", phoneKey.Public().String()); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	wantEOF(t, dial(phone), 10*time.Second, "no gateway")
	if d := time.Since(t0); d < gatewayWait/2 {
		t.Errorf("closed after %v, before the gateway could take it", d)
	}

	// Remote access off closes it.
	acc = gatewayAccepts(ctx)
	dial(phone).Write([]byte("ping\n"))
	if a = <-acc; a.err != nil {
		t.Fatal(a.err)
	}
	defer a.c.Close()
	if err := c.DisableRemote(); err != nil {
		t.Fatal(err)
	}
	wantEOF(t, a.c, 5*time.Second, "remote access off")

	// Clearing the relay goes back to tailcat's relays (the loopback here).
	if err := c.EnableRemote(); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, c, true)
	if err := c.SetRelay(""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for st, _ = c.RemoteStatus(); st.Address != "tcFAKE" && time.Now().Before(deadline); st, _ = c.RemoteStatus() {
		time.Sleep(10 * time.Millisecond)
	}
	if st.Address != "tcFAKE" || st.Relay != "" || readConfig(t).Relay != "" {
		t.Errorf("after clearing the relay: address %q, relay %q", st.Address, st.Relay)
	}
}

// TestTailcatAcceptLocalOnly: a remote client cannot wait for the
// gateway's connections.
func TestTailcatAcceptLocalOnly(t *testing.T) {
	s, _ := testServer(t)
	rc := pipe(s, testPeer)
	defer rc.Close()
	if err := rc.call(Request{Op: "remote.tailcatAccept"}, nil); err == nil {
		t.Error("a remote client asked to accept")
	}
}
