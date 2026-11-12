//go:build darwin || linux || windows

package rex

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/derp"
	"tailscale.com/derp/derphttp"
	"tailscale.com/net/netmon"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

// relayAdmits tells whether the relay of reg serves the key k: a DERP
// client of k connects and is told the server's info, or is cut off.
// It returns the client, connected, for the caller to watch.
func relayAdmits(t *testing.T, reg *tailcfg.DERPRegion, k key.NodePrivate) (*derphttp.Client, bool) {
	t.Helper()
	dc := derphttp.NewRegionClient(k, func(string, ...any) {}, netmon.NewStatic(), func() *tailcfg.DERPRegion { return reg })
	t.Cleanup(func() { dc.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := dc.Connect(ctx); err != nil {
		return dc, false
	}
	m, err := recvWithin(dc, 5*time.Second)
	_, info := m.(derp.ServerInfoMessage)
	return dc, err == nil && info
}

func recvWithin(dc *derphttp.Client, d time.Duration) (derp.ReceivedMessage, error) {
	type res struct {
		m   derp.ReceivedMessage
		err error
	}
	ch := make(chan res, 1)
	go func() {
		m, err := dc.Recv()
		ch <- res{m, err}
	}()
	select {
	case r := <-ch:
		return r.m, r.err
	case <-time.After(d):
		return nil, fmt.Errorf("nothing in %v", d)
	}
}

// TestReachableAt: with Reachable at set, the server runs a DERP relay on
// that address, pinned, and shows a second tailcat address through it,
// of the same keys; the first works as before. The relay serves the
// server's key and the allowlist's only, and a revoke ends its service.
func TestReachableAt(t *testing.T) {
	reg := tailcatEnv(t)
	s, _ := testServer(t)
	relayInsecure = true
	oldPort, oldIdle := ownRelayPort, ownRelayIdle
	ownRelayPort, ownRelayIdle = 0, 500*time.Millisecond
	t.Cleanup(func() { relayInsecure, ownRelayPort, ownRelayIdle = false, oldPort, oldIdle })
	c := pipe(s, "")
	defer c.Close()

	if err := c.SetRelay(fmt.Sprintf("localhost:%d", reg.Nodes[0].DERPPort)); err != nil {
		t.Fatal(err)
	}
	if err := c.EnableRemote(); err != nil {
		t.Fatal(err)
	}
	if err := c.AllowRemote("this machine", ClientPublicKey()); err != nil {
		t.Fatal(err)
	}
	st := waitRunning(t, c, true)
	if st.ReachableAt != "" || st.ReachableAddress != "" {
		t.Fatalf("Reachable at on by default: %+v", st)
	}
	for _, bad := range []string{"0.0.0.0", "::", "derp.example.com", "192.0.2.1:7464", "224.0.0.1"} {
		if err := c.SetReachable(bad); err == nil {
			t.Errorf("took %q", bad)
		}
	}
	// waitReachable waits for the second address, or for an error.
	waitReachable := func(what string, ok func(RemoteStatus) bool) RemoteStatus {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			st, err := c.RemoteStatus()
			if err != nil {
				t.Fatal(err)
			}
			if ok(st) {
				return st
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %+v", what, st)
			}
		}
	}

	// An address not of this machine: an error, the first address as is.
	if err := c.SetReachable("192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	st = waitReachable("an address not here", func(st RemoteStatus) bool { return st.ReachableError != "" })
	if !strings.Contains(st.ReachableError, "not an address of this machine") || !st.Running || st.ReachableAddress != "" {
		t.Errorf("not here: %+v", st)
	}

	if err := c.SetReachable("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	st = waitReachable("the second address", func(st RemoteStatus) bool { return st.ReachableAddress != "" })
	if readConfig(t).ReachableAt != "127.0.0.1" || st.ReachableError != "" {
		t.Errorf("saved %q, error %q", readConfig(t).ReachableAt, st.ReachableError)
	}
	first, err := tailcat.ParseAddr(tailcat.Addr(st.Address))
	if err != nil {
		t.Fatal(err)
	}
	second, err := tailcat.ParseAddr(tailcat.Addr(st.ReachableAddress))
	if err != nil {
		t.Fatal(err)
	}
	if first.ServerPublic != second.ServerPublic || first.PresharedKey != second.PresharedKey {
		t.Error("the second address has other keys")
	}
	if len(first.Region) != 1 || first.Region[0].Nodes[0].HostName != "localhost" {
		t.Errorf("the first address's relay changed: %+v", first.Region[0].Nodes[0])
	}
	own := second.Region[0]
	n := own.Nodes[0]
	if len(second.Region) != 1 || n.HostName != "127.0.0.1" || n.IPv4 != "127.0.0.1" || n.STUNPort != n.DERPPort ||
		!strings.HasPrefix(n.CertName, "sha256-raw:") || n.InsecureForTests {
		t.Fatalf("the second address's relay: %+v", n)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var rc *Client
	// Kept past its subtest: the ones after it watch it.
	defer func() {
		if rc != nil {
			rc.Close()
		}
	}()
	t.Run("second address", func(t *testing.T) {
		t0 := time.Now()
		if rc, err = ConnectRemote(ctx, st.ReachableAddress); err != nil {
			t.Fatal(err)
		}
		h, err := rc.Hello()
		if err != nil || h.Client == nil || h.Client.PublicKey != ClientPublicKey() {
			t.Fatalf("hello %+v %v", h.Client, err)
		}
		t.Logf("first connection through the server's own relay, server just started: %v", time.Since(t0).Round(time.Millisecond))
		cmd, work := testShell()
		info, err := rc.Create(CreateOptions{Command: cmd, Dir: work, Cols: 80, Rows: 24})
		if err != nil {
			t.Fatal(err)
		}
		if info.Owner != ClientPublicKey() {
			t.Errorf("session owner %q", info.Owner)
		}
		if err := rc.Kill(info.ID); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("first address", func(t *testing.T) {
		fc, err := ConnectRemote(ctx, st.Address)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		if _, err := fc.Hello(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("wrong pin", func(t *testing.T) {
		k := key.NewNode()
		if err := c.AllowRemote("pinned", k.Public().String()); err != nil {
			t.Fatal(err)
		}
		bad, _ := tailcat.ParseAddr(tailcat.Addr(st.ReachableAddress))
		bad.Region[0].Nodes[0].CertName = "sha256-raw:" + strings.Repeat("00", 32)
		dc, ok := relayAdmits(t, bad.Region[0], k)
		dc.Close()
		if ok {
			t.Fatal("a client went through a relay whose certificate is not the pinned one")
		}
		// The same key, the right pin: in.
		if _, ok := relayAdmits(t, own, k); !ok {
			t.Fatal("an allowed key with the right pin is not served")
		}
	})
	t.Run("stranger", func(t *testing.T) {
		if _, ok := relayAdmits(t, own, key.NewNode()); ok {
			t.Fatal("the relay serves a key not on the allowlist")
		}
	})
	t.Run("pairing window", func(t *testing.T) {
		// Open, any key is served, to ask through this address; closed,
		// its connection ends and a new one is refused.
		if err := c.StartPairing(); err != nil {
			t.Fatal(err)
		}
		k := key.NewNode()
		dc, ok := relayAdmits(t, own, k)
		if !ok {
			t.Fatal("the relay refuses a new key while the pairing window is open")
		}
		if err := c.StopPairing(); err != nil {
			t.Fatal(err)
		}
		for {
			if _, err := recvWithin(dc, 5*time.Second); err != nil {
				if strings.HasPrefix(err.Error(), "nothing in") {
					t.Fatal("a stranger's relay connection is kept after the window")
				}
				break
			}
		}
		if _, ok := relayAdmits(t, own, k); ok {
			t.Fatal("the relay serves a stranger after the window")
		}
	})
	t.Run("silent", func(t *testing.T) {
		// A connection that asks once and then says nothing is closed.
		nc, err := tls.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", n.DERPPort), &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			t.Fatal(err)
		}
		defer nc.Close()
		fmt.Fprint(nc, "GET /derp/probe HTTP/1.1\r\nHost: x\r\n\r\n")
		nc.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.Copy(io.Discard, nc); err != nil && strings.Contains(err.Error(), "timeout") {
			t.Error("a silent connection is kept")
		}
	})
	t.Run("revoke", func(t *testing.T) {
		k := key.NewNode()
		if err := c.AllowRemote("phone", k.Public().String()); err != nil {
			t.Fatal(err)
		}
		dc, ok := relayAdmits(t, own, k)
		if !ok {
			t.Fatal("an allowed key is not served")
		}
		if err := c.RevokeRemote(k.Public().String()); err != nil {
			t.Fatal(err)
		}
		// Its connection closes; a new one is refused.
		for {
			if _, err := recvWithin(dc, 5*time.Second); err != nil {
				if strings.HasPrefix(err.Error(), "nothing in") {
					t.Fatal("the revoked key's relay connection is kept")
				}
				break
			}
		}
		if _, ok := relayAdmits(t, own, k); ok {
			t.Fatal("the relay serves a revoked key")
		}
		// Another key's revoke leaves this machine's session.
		if rc != nil {
			if _, err := rc.Hello(); err != nil {
				t.Fatalf("a session ended with another key's revoke: %v", err)
			}
		}
		// This machine's session on the second address ends with its key.
		if rc == nil {
			t.Skip("no session to revoke")
		}
		if err := c.RevokeRemote(ClientPublicKey()); err != nil {
			t.Fatal(err)
		}
		select {
		case <-rc.Closed():
		case <-time.After(10 * time.Second):
			t.Error("the session's connection kept after revoke")
		}
	})
	t.Run("limits", func(t *testing.T) {
		// All at once: one that says nothing is closed in ownRelayIdle.
		var ncs []net.Conn
		for range ownRelayPerSource + 1 {
			nc, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", n.DERPPort))
			if err != nil {
				t.Fatal(err)
			}
			defer nc.Close()
			ncs = append(ncs, nc)
		}
		var cut int
		for _, nc := range ncs {
			nc.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			if _, err := nc.Read(make([]byte, 1)); err != nil && !strings.Contains(err.Error(), "timeout") {
				cut++
			}
		}
		if cut == 0 {
			t.Errorf("%d connections from one source, none closed", ownRelayPerSource+1)
		}
	})
	t.Run("off", func(t *testing.T) {
		if err := c.SetReachable(""); err != nil {
			t.Fatal(err)
		}
		st := waitReachable("off", func(st RemoteStatus) bool { return st.ReachableAddress == "" })
		if st.ReachableAt != "" || st.ReachableError != "" || !st.Running {
			t.Errorf("off: %+v", st)
		}
		if nc, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", n.DERPPort), time.Second); err == nil {
			nc.Close()
			t.Error("the relay still listens")
		}
	})
}

// The relay's client in this process dials its own address without the
// environment's proxy, and any other as before.
func TestNoProxyToOwnRelay(t *testing.T) {
	proxy, _ := url.Parse("http://proxy.example:3128")
	f := noProxyToOwnRelay(func(*http.Request) (*url.URL, error) { return proxy, nil })
	ownRelayHost.Store("192.0.2.10")
	t.Cleanup(func() { ownRelayHost.Store("") })
	for host, want := range map[string]*url.URL{"192.0.2.10": nil, "192.0.2.11": proxy, "derp.example.com": proxy} {
		got, _ := f(&http.Request{URL: &url.URL{Scheme: "https", Host: host}})
		if got != want {
			t.Errorf("%s: proxy %v, want %v", host, got, want)
		}
	}
}
