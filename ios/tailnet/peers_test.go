package tailnet

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/key"
)

// The proxy's dials take a status of every peer only when the watch told
// a change, or the host is not in the status kept.
func TestPeerCache(t *testing.T) {
	fetches := 0
	st := tailnet("Running")
	var pc peerCache
	var onFetch func()
	status := func(context.Context) (*ipnstate.Status, error) {
		fetches++
		if onFetch != nil {
			onFetch()
		}
		return st, nil
	}
	var dialed []string
	dial := func(_ context.Context, _, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		a, b := net.Pipe()
		b.Close()
		return a, nil
	}
	try := func(addr string, wantFetches int) {
		t.Helper()
		c, err := dialTailnet(context.Background(), &pc, status, dial, addr)
		if err != nil {
			t.Fatalf("dial %s: %v", addr, err)
		}
		c.Close()
		if fetches != wantFetches {
			t.Fatalf("dial %s: %d statuses, want %d", addr, fetches, wantFetches)
		}
	}
	// Not watched yet: each dial asks.
	try("mac-gorex:443", 1)
	try("mac-gorex:443", 2)
	pc.changed(true)
	try("mac-gorex:443", 3)
	try("100.64.0.8:7464", 3)
	try("mac-gorex.example.ts.net:443", 3)
	// A change told: the next dial asks again.
	pc.changed(true)
	try("mac-gorex:443", 4)
	try("mac-gorex:443", 4)
	// A peer added and not told yet: the dial asks, and finds it.
	st = tailnet("Running")
	st.Peer[key.NewNode().Public()] = &ipnstate.PeerStatus{DNSName: "new-gorex.example.ts.net.",
		TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.9")}}
	try("new-gorex:443", 5)
	try("new-gorex:443", 5)
	// A change told while a status is read: that status is not kept.
	pc.changed(true)
	onFetch = func() { pc.changed(true) }
	try("mac-gorex:443", 6)
	onFetch = nil
	try("mac-gorex:443", 7)
	try("mac-gorex:443", 7)
	// The watch broke: nothing is kept until it watches again.
	pc.changed(false)
	try("mac-gorex:443", 8)
	try("mac-gorex:443", 9)
	// A peer removed and told: it is refused.
	pc.changed(true)
	try("new-gorex:443", 10)
	st = tailnet("Running")
	pc.changed(true)
	if _, err := dialTailnet(context.Background(), &pc, status, dial, "new-gorex:443"); err == nil {
		t.Error("a peer removed was dialed")
	}
	if dialed[0] != "100.64.0.8:443" || dialed[3] != "100.64.0.8:7464" {
		t.Errorf("dialed %v", dialed)
	}
}
