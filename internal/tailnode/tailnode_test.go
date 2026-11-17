package tailnode

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHostnames(t *testing.T) {
	for name, ok := range map[string]bool{"studio": true, "a": true, "a-b-1": true, "-a": false, "a-": false, "": false, "a_b": false, strings.Repeat("a", 64): false} {
		if ValidHostname(name) != ok {
			t.Errorf("ValidHostname(%q) = %v", name, !ok)
		}
	}
}

// TestControlURLCleartext: a control server is https, or http on this
// machine or a private network, judged by the URL's literal host.
func TestControlURLCleartext(t *testing.T) {
	for _, s := range []string{
		"https://headscale.example.com",
		"https://203.0.113.7",
		"https://[2001:db8::1]:8443",
		"http://localhost:8080",
		"http://LocalHost",
		"http://localhost.:8080",
		"http://127.0.0.1",
		"http://[::1]:8080",
		"http://10.0.0.1:8080",
		"http://172.16.5.4",
		"http://192.168.1.10",
		"http://169.254.1.1",
		"http://[fd00::1]",
		"http://[fe80::1%25en0]:8080",
		"http://[::ffff:192.168.1.10]",
	} {
		if !ValidControlURL(s) {
			t.Errorf("%s was refused", s)
		}
	}
	for _, s := range []string{
		"http://headscale.example.com",
		"http://203.0.113.7",
		"http://[2001:db8::1]:8080",
		"http://[::ffff:203.0.113.7]",
		"http://100.64.0.1",
		"http://[fd7a:115c:a1e0::1]",
		"https://:8443",
		"http://0.0.0.0",
		"http://headscale",
		"http://headscale.local",
		"http://localhost.example.com",
	} {
		if ValidControlURL(s) {
			t.Errorf("%s was accepted", s)
		}
	}
}

// TestDialPeerRefusesNonPeers: an address that is not a peer's is never
// dialed, not even through the system's network, as tsnet's Dial would.
func TestDialPeerRefusesNonPeers(t *testing.T) {
	peer := netip.MustParseAddr("100.64.0.7")
	isPeer := func(_ context.Context, ip netip.Addr) bool { return ip == peer }
	dialed := 0
	dial := func(context.Context, netip.AddrPort) (net.Conn, error) {
		dialed++
		a, b := net.Pipe()
		b.Close()
		return a, nil
	}
	for _, test := range []struct{ network, addr string }{
		{"tcp", "127.0.0.1:7464"},
		{"tcp4", "192.168.1.10:7464"},
		{"tcp", "100.64.0.8:7464"},
		{"tcp", "[::1]:7464"},
		{"tcp6", "100.64.0.7:7464"},
		{"udp", "100.64.0.7:7464"},
		{"tcp", "example.com:7464"},
		{"tcp", "100.64.0.7"},
	} {
		if c, err := dialPeer(context.Background(), test.network, test.addr, isPeer, dial); err == nil {
			c.Close()
			t.Errorf("%s %s was dialed", test.network, test.addr)
		}
	}
	if dialed != 0 {
		t.Fatalf("%d refused addresses reached the dial", dialed)
	}
	for _, addr := range []string{"100.64.0.7:7464", "[::ffff:100.64.0.7]:7464"} {
		c, err := dialPeer(context.Background(), "tcp4", addr, isPeer, dial)
		if err != nil {
			t.Fatalf("peer %s: %v", addr, err)
		}
		c.Close()
	}
	// A node that never started has no network map: nothing is a peer.
	n := New(Options{Dir: t.TempDir(), Hostname: "test"})
	if c, err := n.DialPeer(context.Background(), "tcp", "127.0.0.1:7464"); !errors.Is(err, ErrNotPeer) {
		if c != nil {
			c.Close()
		}
		t.Fatalf("DialPeer of an unstarted node = %v; want ErrNotPeer", err)
	}
}

// TestListenOnlyOnTailnet: a listener on the device takes no connection
// from the machine's own interfaces. The device starts with an
// unreachable control server, so it signs in to nothing and needs no
// network.
func TestListenOnlyOnTailnet(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a tsnet device")
	}
	free, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback: %v", err)
	}
	port := free.Addr().(*net.TCPAddr).Port
	free.Close()
	n := New(Options{Dir: t.TempDir(), Hostname: "test", ControlURL: "http://127.0.0.1:1"})
	defer n.Close()
	if err := n.Start(); err != nil {
		t.Skipf("tsnet does not start here: %v", err)
	}
	ln, err := n.Listen("tcp4", ":"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
			t.Error("the tailnet listener took a connection from the machine")
		}
	}()
	addrs := []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")}
	if ifaces, err := net.InterfaceAddrs(); err == nil {
		for _, a := range ifaces {
			if p, err := netip.ParsePrefix(a.String()); err == nil && !p.Addr().IsLinkLocalUnicast() {
				addrs = append(addrs, p.Addr())
			}
		}
	}
	for _, ip := range addrs {
		c, err := net.DialTimeout("tcp", netip.AddrPortFrom(ip, uint16(port)).String(), time.Second)
		if err == nil {
			c.Close()
			t.Errorf("%s:%d reached the tailnet listener", ip, port)
		}
	}
}
