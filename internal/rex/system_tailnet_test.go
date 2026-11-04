package rex

import (
	"context"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

type tailnetTestAPI struct {
	who *apitype.WhoIsResponse
}

func (a tailnetTestAPI) WhoIs(context.Context, string) (*apitype.WhoIsResponse, error) {
	return a.who, nil
}

func (tailnetTestAPI) Status(context.Context) (*ipnstate.Status, error) {
	panic("unexpected Status call")
}

func TestSystemTailnetRequiresLiveVerifiedIdentity(t *testing.T) {
	for _, test := range []struct {
		name    string
		user    tailcfg.UserID
		node    tailcfg.StableNodeID
		tags    []string
		expired bool
		want    bool
	}{
		{name: "same account", user: 42, node: "node-a", want: true},
		{name: "another account", user: 43, node: "node-a"},
		{name: "reassigned IP", user: 42, node: "node-b"},
		{name: "tagged device", user: 42, node: "node-a", tags: []string{"tag:server"}},
		{name: "expired discovery state", user: 42, node: "node-a", expired: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ip := netip.MustParseAddr("100.100.100.42")
			p := tailnetPeer{id: "node-a", ip: ip}
			snap := &tailnetSnapshot{scope: "net", user: 42, byIP: map[netip.Addr]tailnetPeer{ip: p}, expires: time.Now().Add(time.Minute)}
			if test.expired {
				snap.expires = time.Now().Add(-time.Second)
			}
			who := &apitype.WhoIsResponse{
				Node:        &tailcfg.Node{User: test.user, StableID: test.node, Tags: test.tags, Addresses: []netip.Prefix{netip.PrefixFrom(ip, 32)}},
				UserProfile: &tailcfg.UserProfile{ID: test.user},
			}
			remote, client := net.Pipe()
			defer client.Close()
			client.SetDeadline(time.Now().Add(time.Second))
			tailnet := &systemTailnet{api: tailnetTestAPI{who}, ctx: context.Background(), snap: snap, slots: make(chan struct{}, 1)}
			tailnet.slots <- struct{}{}
			go tailnet.admit(nil, snap, nil, remote, p, "100.100.100.42:12345")
			if _, err := io.WriteString(client, tailnetProbe); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, len(tailnetGreeting()))
			_, err := io.ReadFull(client, got)
			if test.want {
				if err != nil || string(got) != tailnetGreeting() {
					t.Fatalf("probe = %q, %v", got, err)
				}
			} else if err != io.EOF {
				t.Fatalf("unauthorized probe = %q, %v; want closed connection", got, err)
			}
		})
	}
}

func TestSystemTailnetRevokesBothSidesOfBroker(t *testing.T) {
	remote, rp := net.Pipe()
	defer rp.Close()
	local, lp := net.Pipe()
	defer lp.Close()
	tailnet := &systemTailnet{links: map[net.Conn]tailnetLink{remote: {local: local}}}
	tailnet.replace(nil, nil)
	for _, c := range []net.Conn{rp, lp} {
		c.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := c.Read(make([]byte, 1)); err != io.EOF {
			t.Fatalf("read after revocation: %v; want EOF", err)
		}
	}
}
