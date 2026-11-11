//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
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
		"203.0.113.7":               "!",
		"[2001:db8::1]:443":         "!",
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

// gatewayConn is a connection the server made to the gateway's socket,
// and the client its first line names.
type gatewayConn struct {
	c    net.Conn
	r    *bufio.Reader
	peer RemoteClient
}

// fakeGateway listens on the gateway's socket, as the gateway does.
func fakeGateway(t *testing.T) (net.Listener, <-chan gatewayConn) {
	t.Helper()
	ln, err := net.Listen("unix", GatewaySocketPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	ch := make(chan gatewayConn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			r := bufio.NewReader(c)
			line, _ := r.ReadBytes('\n')
			var peer RemoteClient
			json.Unmarshal(line, &peer)
			ch <- gatewayConn{c, r, peer}
		}
	}()
	return ln, ch
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
// tailcat address puts the relay in itself, and serves RexAPIPort to the
// allowed clients only, handing their connections to the gateway's socket
// with their key and name. A revoke and remote access off close them;
// clearing the relay goes back to tailcat's.
func TestRemoteGateway(t *testing.T) {
	reg := tailcatEnv(t)
	s, _ := testServer(t)
	relayInsecure = true
	t.Cleanup(func() { relayInsecure = false })
	gwLn, gw := fakeGateway(t)
	c := pipe(s, "")
	defer c.Close()

	for _, bad := range []string{"https://derp.example.com", fmt.Sprintf("127.0.0.1:%d", reg.Nodes[0].DERPPort)} {
		if err := c.SetRelay(bad); err == nil {
			t.Errorf("took %q for a relay", bad)
		}
	}
	relay := fmt.Sprintf("localhost:%d", reg.Nodes[0].DERPPort)
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
		ci.Region[0].Nodes[0].HostName != "localhost" || ci.Region[0].Nodes[0].DERPPort != reg.Nodes[0].DERPPort {
		t.Fatalf("the address's relay: %+v", ci.Region)
	}

	phoneKey := key.NewNode()
	phoneID := phoneKey.Public().String()
	if err := c.AllowRemote("phone", phoneID); err != nil {
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
	noGateway := func(what string) {
		t.Helper()
		select {
		case g := <-gw:
			t.Fatalf("%s: the gateway got a connection of %+v", what, g.peer)
		default:
		}
	}
	// reaches dials as the phone and returns the gateway's side.
	reaches := func() gatewayConn {
		t.Helper()
		pc := dial(phone)
		pc.Write([]byte("ping\n"))
		var g gatewayConn
		select {
		case g = <-gw:
		case <-time.After(30 * time.Second):
			t.Fatal("the gateway got nothing")
		}
		t.Cleanup(func() { g.c.Close() })
		if g.peer.PublicKey != phoneID || g.peer.Name != "phone" {
			t.Errorf("peer %+v", g.peer)
		}
		if line, err := g.r.ReadString('\n'); line != "ping\n" {
			t.Fatalf("the gateway read %q, %v", line, err)
		}
		g.c.Write([]byte("pong\n"))
		if line, err := bufio.NewReader(pc).ReadString('\n'); line != "pong\n" {
			t.Fatalf("the phone read %q, %v", line, err)
		}
		return g
	}

	// A stranger gets nothing.
	sc := dial(stranger)
	sc.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	wantEOF(t, sc, 10*time.Second, "a stranger's connection")
	noGateway("refused")

	// An allowed client reaches the gateway, named; a revoke closes it.
	g := reaches()
	if err := c.RevokeRemote(phoneID); err != nil {
		t.Fatal(err)
	}
	wantEOF(t, g.c, 5*time.Second, "revoked")

	// With no gateway, the connection closes at once.
	if err := c.AllowRemote("phone", phoneID); err != nil {
		t.Fatal(err)
	}
	gwLn.Close()
	t0 := time.Now()
	wantEOF(t, dial(phone), 10*time.Second, "no gateway")
	if d := time.Since(t0); d > 3*time.Second {
		t.Errorf("closed after %v with no gateway", d)
	}

	// Remote access off closes it.
	_, gw = fakeGateway(t)
	g = reaches()
	if err := c.DisableRemote(); err != nil {
		t.Fatal(err)
	}
	wantEOF(t, g.c, 5*time.Second, "remote access off")

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

// TestRemoteAPIAcceptFails: a failing RexAPIPort listener says so in the
// status, and starts again, as RemotePort's does.
func TestRemoteAPIAcceptFails(t *testing.T) {
	s, _ := testServer(t)
	oldRetry, oldAPI := remoteRetry, remoteAPIListen
	remoteRetry = 400 * time.Millisecond
	defer func() { remoteRetry, remoteAPIListen = oldRetry, oldAPI }()
	var calls atomic.Int32
	remoteAPIListen = func(ts *tailcat.Server) (net.Listener, error) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if calls.Add(1) == 1 {
			ln = failAccept{ln}
		}
		return ln, err
	}
	c := pipe(s, "")
	defer c.Close()
	if err := c.EnableRemote(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, err := c.RemoteStatus()
		if err != nil {
			t.Fatal(err)
		}
		if !st.Running && strings.Contains(st.LastError, "boom") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no error state: %+v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if st := waitRunning(t, c, true); st.LastError != "" || calls.Load() != 2 {
		t.Errorf("after the restart: %+v, %d listens", st, calls.Load())
	}
}
