package tailnet

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"testing"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/key"
)

// tailnet is a node's status with one peer, mac-gorex at 100.64.0.8.
func tailnet(state string) *ipnstate.Status {
	return &ipnstate.Status{BackendState: state, Peer: map[key.NodePublic]*ipnstate.PeerStatus{
		{}: {DNSName: "mac-gorex.example.ts.net.", TailscaleIPs: []netip.Addr{
			netip.MustParseAddr("100.64.0.8"), netip.MustParseAddr("fd7a:115c:a1e0::8")}},
	}}
}

func TestPeerAddr(t *testing.T) {
	for _, addr := range []string{"100.64.0.8:443", "[fd7a:115c:a1e0::8]:443", "mac-gorex.example.ts.net:443",
		"MAC-GOREX.example.ts.net.:443", "mac-gorex:443"} {
		got, err := peerAddr(tailnet("Running"), addr)
		if err != nil || got.Port() != 443 || !got.Addr().IsValid() {
			t.Errorf("peerAddr(%s) = %v, %v; want the peer", addr, got, err)
		}
	}
	for _, addr := range []string{"other.example.ts.net:443", "100.64.0.9:443", "1.1.1.1:443", "example.com:443", "mac-gorex"} {
		if got, err := peerAddr(tailnet("Running"), addr); err == nil {
			t.Errorf("peerAddr(%s) = %v; want refused", addr, got)
		}
	}
	// Before the network map, even a peer's address is refused.
	for _, st := range []*ipnstate.Status{nil, tailnet("Starting"), {BackendState: "Running"}} {
		if got, err := peerAddr(st, "100.64.0.8:443"); err == nil {
			t.Errorf("peerAddr with no network map = %v; want refused", got)
		}
	}
}

// socksConnect speaks SOCKS5 to the proxy at addr: user name and
// password, then CONNECT to the domain name host:port.
func socksConnect(addr, user, password, host string, port uint16) (net.Conn, error) {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (net.Conn, error) { c.Close(); return nil, err }
	b := make([]byte, 2)
	if _, err := c.Write([]byte{5, 1, 2}); err != nil {
		return fail(err)
	}
	if _, err := io.ReadFull(c, b); err != nil || b[1] != 2 {
		return fail(errors.New("no password method"))
	}
	auth := append([]byte{1, byte(len(user))}, user...)
	auth = append(append(auth, byte(len(password))), password...)
	if _, err := c.Write(auth); err != nil {
		return fail(err)
	}
	if _, err := io.ReadFull(c, b); err != nil || b[1] != 0 {
		return fail(errors.New("credentials refused"))
	}
	req := append([]byte{5, 1, 0, 3, byte(len(host))}, host...)
	req = binary.BigEndian.AppendUint16(req, port)
	if _, err := c.Write(req); err != nil {
		return fail(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(c, reply); err != nil || reply[1] != 0 {
		return fail(errors.New("connect refused"))
	}
	return c, nil
}

func TestProxy(t *testing.T) {
	// The peer: an echo server on loopback, which the fake dial reaches.
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go io.Copy(c, c)
		}
	}()
	var dialed []string
	p, err := startProxy(func(context.Context) (*ipnstate.Status, error) { return tailnet("Running"), nil },
		func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialed = append(dialed, addr)
			return new(net.Dialer).DialContext(ctx, network, echo.Addr().String())
		})
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	addr := p.ln.Addr().String()

	c, err := socksConnect(addr, "tsnet", p.password, "mac-gorex.example.ts.net", 443)
	if err != nil {
		t.Fatalf("connect to a peer by name: %v", err)
	}
	c.Write([]byte("hi"))
	b := make([]byte, 2)
	if _, err := io.ReadFull(c, b); err != nil || string(b) != "hi" {
		t.Errorf("relay: %q, %v", b, err)
	}
	c.Close()
	if len(dialed) != 1 || dialed[0] != "100.64.0.8:443" {
		t.Errorf("dialed %v, want the peer's address", dialed)
	}

	if _, err := socksConnect(addr, "tsnet", "wrong", "mac-gorex.example.ts.net", 443); err == nil {
		t.Error("a wrong password was let in")
	}
	if _, err := socksConnect(addr, "tsnet", p.password, "example.com", 443); err == nil {
		t.Error("an address off the tailnet was connected")
	}
	if len(dialed) != 1 {
		t.Errorf("dialed %v; refused connections must not dial", dialed)
	}
}
