package rex

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/views"
)

var (
	builtinSelfIP  = netip.MustParseAddr("100.64.0.1")
	builtinAlpha   = netip.MustParseAddr("100.64.0.2")
	builtinBob     = netip.MustParseAddr("100.64.0.3")
	builtinTagged  = netip.MustParseAddr("100.64.0.4")
	builtinUser    = tailcfg.UserID(42)
	builtinBobUser = tailcfg.UserID(43)
)

// addrConn is a pipe end with the addresses of a tailnet connection.
type addrConn struct {
	net.Conn
	local, remote netip.AddrPort
}

func (c addrConn) LocalAddr() net.Addr  { return net.TCPAddrFromAddrPort(c.local) }
func (c addrConn) RemoteAddr() net.Addr { return net.TCPAddrFromAddrPort(c.remote) }

type fakeListener struct {
	conns     chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
}

func (l *fakeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *fakeListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *fakeListener) Addr() net.Addr {
	return net.TCPAddrFromAddrPort(netip.AddrPortFrom(builtinSelfIP, tailnetPort))
}

func (l *fakeListener) isClosed() bool {
	select {
	case <-l.closed:
		return true
	default:
		return false
	}
}

// fakeNode is a built-in Tailscale node with a fixed status, whose peers
// answer as serve does.
type fakeNode struct {
	mu     sync.Mutex
	st     *ipnstate.Status
	who    map[netip.Addr]*apitype.WhoIsResponse
	lns    []*fakeListener
	dialed []netip.Addr
	serve  func(peer netip.Addr, c net.Conn)
}

func (n *fakeNode) StatusWithPeers(context.Context) (*ipnstate.Status, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.st, nil
}

func (n *fakeNode) WhoIs(_ context.Context, addr string) (*apitype.WhoIsResponse, error) {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if w := n.who[ap.Addr()]; w != nil {
		return w, nil
	}
	return nil, errors.New("no such peer")
}

func (n *fakeNode) Listen(network, addr string) (net.Listener, error) {
	if network != "tcp4" || addr != netip.AddrPortFrom(builtinSelfIP, tailnetPort).String() {
		return nil, errors.New("unexpected listen " + network + " " + addr)
	}
	ln := &fakeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
	n.mu.Lock()
	n.lns = append(n.lns, ln)
	n.mu.Unlock()
	return ln, nil
}

func (n *fakeNode) DialPeer(_ context.Context, _, addr string) (net.Conn, error) {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	n.dialed = append(n.dialed, ap.Addr())
	serve := n.serve
	n.mu.Unlock()
	if serve == nil {
		return nil, errors.New("refused")
	}
	a, b := net.Pipe()
	local := netip.AddrPortFrom(builtinSelfIP, 40000)
	go serve(ap.Addr(), addrConn{b, ap, local})
	return addrConn{a, local, ap}, nil
}

func (n *fakeNode) listener() *fakeListener {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.lns) == 0 {
		return nil
	}
	return n.lns[len(n.lns)-1]
}

func builtinPeer(id tailcfg.StableNodeID, user tailcfg.UserID, host string, ip netip.Addr, tags ...string) *ipnstate.PeerStatus {
	p := &ipnstate.PeerStatus{
		ID: id, UserID: user, HostName: host, DNSName: host + ".example.ts.net.",
		TailscaleIPs: []netip.Addr{ip}, InNetworkMap: true,
	}
	if len(tags) > 0 {
		v := views.SliceOf(tags)
		p.Tags = &v
	}
	return p
}

func builtinWho(id tailcfg.StableNodeID, user tailcfg.UserID, ip netip.Addr, tags ...string) *apitype.WhoIsResponse {
	return &apitype.WhoIsResponse{
		Node:        &tailcfg.Node{User: user, StableID: id, Tags: tags, Addresses: []netip.Prefix{netip.PrefixFrom(ip, 32)}},
		UserProfile: &tailcfg.UserProfile{ID: user},
	}
}

// newFakeNode is a signed-in node of user 42 that reaches alpha, the
// user's GoRex machine, bob's machine and a tagged server. Its status is
// tsnet's: no TUN.
func newFakeNode() *fakeNode {
	self := builtinPeer("self", builtinUser, "studio-gorex", builtinSelfIP)
	st := &ipnstate.Status{
		BackendState:   "Running",
		TailscaleIPs:   []netip.Addr{builtinSelfIP},
		Self:           self,
		CurrentTailnet: &ipnstate.TailnetStatus{MagicDNSSuffix: "example.ts.net"},
		User:           map[tailcfg.UserID]tailcfg.UserProfile{builtinUser: {ID: builtinUser, LoginName: "me@example.com"}},
		Peer: map[key.NodePublic]*ipnstate.PeerStatus{
			key.NewNode().Public(): builtinPeer("alpha", builtinUser, "alpha-gorex", builtinAlpha),
			key.NewNode().Public(): builtinPeer("bob", builtinBobUser, "bob-gorex", builtinBob),
			key.NewNode().Public(): builtinPeer("server", builtinUser, "server", builtinTagged, "tag:server"),
		},
	}
	return &fakeNode{st: st, who: map[netip.Addr]*apitype.WhoIsResponse{
		builtinAlpha:  builtinWho("alpha", builtinUser, builtinAlpha),
		builtinBob:    builtinWho("bob", builtinBobUser, builtinBob),
		builtinTagged: builtinWho("server", builtinUser, builtinTagged, "tag:server"),
	}}
}

// answerTailnet is a GoRex peer: it greets a probe or a relay, and echoes
// what follows a relay.
func answerTailnet(_ netip.Addr, c net.Conn) {
	defer c.Close()
	b := make([]byte, len(tailnetProbe))
	if _, err := io.ReadFull(c, b); err != nil {
		return
	}
	io.WriteString(c, tailnetGreeting())
	if string(b) == tailnetConnect {
		io.Copy(c, c)
	}
}

func builtinBroker(t *testing.T, node func() TailnetDevice) *systemTailnet {
	t.Helper()
	b := newTailnetBroker(nil, node)
	t.Cleanup(func() {
		b.cancel()
		b.replace(nil, nil)
	})
	return b
}

func nodeFunc(n *fakeNode) func() TailnetDevice {
	var mu sync.Mutex
	cur := TailnetDevice(n)
	return func() TailnetDevice {
		mu.Lock()
		defer mu.Unlock()
		return cur
	}
}

// TestBuiltinTailnetDiscovers finds the user's GoRex machine over the
// built-in node, by its name without -gorex, and neither another user's
// device nor a tagged one.
func TestBuiltinTailnetDiscovers(t *testing.T) {
	n := newFakeNode()
	n.serve = answerTailnet
	b := builtinBroker(t, nodeFunc(n))
	b.wanted = time.Now()
	b.refresh(nil)
	st := b.status()
	if !st.Running || st.Side != TailnetBuiltin || st.Error != "" {
		t.Fatalf("status = %+v; want running on the built-in side", st)
	}
	if st.Node != NodeRunning || st.Account != "me@example.com" || st.Device != "studio-gorex.example.ts.net" {
		t.Fatalf("node = %q %q %q", st.Node, st.Account, st.Device)
	}
	if len(st.Hosts) != 1 || st.Hosts[0].Name != "alpha" || !st.Owns(st.Hosts[0].Target) {
		t.Fatalf("hosts = %+v; want alpha alone", st.Hosts)
	}
	n.mu.Lock()
	dialed := slices.Clone(n.dialed)
	n.mu.Unlock()
	if !slices.Equal(dialed, []netip.Addr{builtinAlpha}) {
		t.Fatalf("dialed %v; want alpha alone", dialed)
	}
}

// TestTailnetSidesAdmit checks a probe and a relay on both sides: the
// user's untagged device is let in, another user's and a tagged one are
// not.
func TestTailnetSidesAdmit(t *testing.T) {
	for _, side := range []string{TailnetSystem, TailnetBuiltin} {
		for _, test := range []struct {
			name  string
			ip    netip.Addr
			id    tailcfg.StableNodeID
			hello string
			want  bool
		}{
			{"same user probe", builtinAlpha, "alpha", tailnetProbe, true},
			{"another user probe", builtinBob, "bob", tailnetProbe, false},
			{"another user relay", builtinBob, "bob", tailnetConnect, false},
			{"tagged probe", builtinTagged, "server", tailnetProbe, false},
			{"tagged relay", builtinTagged, "server", tailnetConnect, false},
		} {
			t.Run(side+"/"+test.name, func(t *testing.T) {
				n := newFakeNode()
				p := tailnetPeer{id: test.id, ip: test.ip}
				snap := &tailnetSnapshot{scope: "net", user: builtinUser, byIP: map[netip.Addr]tailnetPeer{test.ip: p}, expires: time.Now().Add(time.Minute)}
				b := &systemTailnet{ctx: context.Background(), snap: snap, slots: make(chan struct{}, 1), links: map[net.Conn]tailnetLink{}}
				if side == TailnetBuiltin {
					snap.node, b.node = n, nodeFunc(n)
				} else {
					b.api = tailnetTestAPI{n.who[test.ip]}
				}
				b.slots <- struct{}{}
				remote, client := net.Pipe()
				defer client.Close()
				client.SetDeadline(time.Now().Add(time.Second))
				go b.admit(nil, snap, nil, remote, p, netip.AddrPortFrom(test.ip, 12345).String())
				if _, err := io.WriteString(client, test.hello); err != nil {
					t.Fatal(err)
				}
				got := make([]byte, len(tailnetGreeting()))
				_, err := io.ReadFull(client, got)
				if test.want {
					if err != nil || string(got) != tailnetGreeting() {
						t.Fatalf("greeting = %q, %v", got, err)
					}
				} else if err != io.EOF {
					t.Fatalf("refused peer = %q, %v; want closed connection", got, err)
				}
			})
		}
	}
}

// TestBuiltinTailnetAcceptsOnlyPeers takes a connection on the node's
// listener from the user's device, and drops one from an address the
// snapshot does not have.
func TestBuiltinTailnetAcceptsOnlyPeers(t *testing.T) {
	n := newFakeNode()
	b := builtinBroker(t, nodeFunc(n))
	b.refresh(nil)
	ln := n.listener()
	if ln == nil {
		t.Fatal("no listener on the node")
	}
	for _, test := range []struct {
		from netip.Addr
		want bool
	}{
		{builtinAlpha, true},
		{builtinBob, false},
		{netip.MustParseAddr("192.168.1.10"), false},
	} {
		remote, client := net.Pipe()
		client.SetDeadline(time.Now().Add(time.Second))
		ln.conns <- addrConn{remote, netip.AddrPortFrom(builtinSelfIP, tailnetPort), netip.AddrPortFrom(test.from, 50000)}
		io.WriteString(client, tailnetProbe)
		got, err := bufio.NewReader(client).ReadString('\n')
		if test.want != (err == nil && got == tailnetGreeting()) {
			t.Errorf("probe from %s = %q, %v; want greeted %v", test.from, got, err, test.want)
		}
		client.Close()
	}
}

// TestBuiltinTailnetRelays opens a terminal's connection to the user's
// device over the node, and refuses one whose identity check fails.
func TestBuiltinTailnetRelays(t *testing.T) {
	n := newFakeNode()
	n.serve = answerTailnet
	b := builtinBroker(t, nodeFunc(n))
	b.refresh(nil)
	s := &Server{}
	s.rem.tailnet = b
	var target string
	for tg, p := range b.snap.byTarget {
		if p.ip == builtinAlpha {
			target = tg
		}
	}
	if target == "" {
		t.Fatal("alpha is not in the snapshot")
	}
	local, client := net.Pipe()
	defer client.Close()
	client.SetDeadline(time.Now().Add(2 * time.Second))
	go s.relayTailnet(local, local, target)
	r := bufio.NewReader(client)
	if line, err := r.ReadString('\n'); err != nil || line != tailnetGreeting() {
		t.Fatalf("relay greeting = %q, %v", line, err)
	}
	if !b.linked() {
		t.Fatal("the relay is not a link")
	}
	io.WriteString(client, "ping\n")
	if line, err := r.ReadString('\n'); err != nil || line != "ping\n" {
		t.Fatalf("relay echo = %q, %v", line, err)
	}

	// The peer's identity changes hands: the relay is refused.
	n.mu.Lock()
	n.who[builtinAlpha] = builtinWho("alpha", builtinBobUser, builtinAlpha)
	n.mu.Unlock()
	local2, client2 := net.Pipe()
	defer client2.Close()
	client2.SetDeadline(time.Now().Add(2 * time.Second))
	go s.relayTailnet(local2, local2, target)
	if line, err := bufio.NewReader(client2).ReadString('\n'); err != io.EOF {
		t.Fatalf("refused relay = %q, %v; want closed connection", line, err)
	}
}

// TestBuiltinTailnetNodeReplaced drops the listener and the links when
// the node is another one, and reports why when there is none.
func TestBuiltinTailnetNodeReplaced(t *testing.T) {
	first, second := newFakeNode(), newFakeNode()
	var mu sync.Mutex
	var cur TailnetDevice = first
	b := builtinBroker(t, func() TailnetDevice {
		mu.Lock()
		defer mu.Unlock()
		return cur
	})
	b.refresh(nil)
	remote, rp := net.Pipe()
	defer rp.Close()
	b.mu.Lock()
	b.links[remote] = tailnetLink{peer: b.snap.byIP[builtinAlpha]}
	b.mu.Unlock()

	// A read of the same node keeps the link.
	b.refresh(nil)
	if !b.linked() || first.listener().isClosed() {
		t.Fatal("a second read of the same node dropped the link or the listener")
	}

	mu.Lock()
	cur = second
	mu.Unlock()
	b.refresh(nil)
	if !first.listener().isClosed() {
		t.Fatal("the replaced node's listener stays open")
	}
	rp.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := rp.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("link after the node was replaced: %v; want EOF", err)
	}
	if second.listener() == nil || !b.status().Running {
		t.Fatal("the broker does not run on the new node")
	}

	mu.Lock()
	cur = nil
	mu.Unlock()
	b.refresh(nil)
	st := b.status()
	if st.Running || st.Node != NodeOff || !strings.Contains(st.Error, "off") || !second.listener().isClosed() {
		t.Fatalf("status with no node = %+v; want unavailable and off", st)
	}
}

// TestBuiltinTailnetUnavailable reports why a node that runs cannot carry
// discovery: it is not signed in, waits for approval, or is tagged.
func TestBuiltinTailnetUnavailable(t *testing.T) {
	for _, test := range []struct {
		name, node, reason string
		change             func(*ipnstate.Status)
	}{
		{"needs login", NodeNeedsLogin, "sign-in", func(st *ipnstate.Status) { st.BackendState = "NeedsLogin" }},
		{"needs approval", NodeNeedsApproval, "approve", func(st *ipnstate.Status) { st.BackendState = "NeedsMachineAuth" }},
		{"starting", NodeStarting, "starting", func(st *ipnstate.Status) { st.BackendState = "Starting" }},
		{"tagged", NodeRunning, "tagged", func(st *ipnstate.Status) {
			v := views.SliceOf([]string{"tag:desktop"})
			st.Self.Tags, st.Self.UserID = &v, 0
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			n := newFakeNode()
			test.change(n.st)
			b := builtinBroker(t, nodeFunc(n))
			b.refresh(nil)
			st := b.status()
			if st.Running || st.Node != test.node || !strings.Contains(st.Error, test.reason) || n.listener() != nil {
				t.Fatalf("status = %+v; want unavailable, node %q, reason with %q", st, test.node, test.reason)
			}
			if st.Account != "" {
				t.Fatalf("account %q shown for an unusable node", st.Account)
			}
		})
	}
}

// TestSystemSnapshotNeedsTUN keeps the system side's rule: a status
// without a TUN, as tsnet's, is no system tailnet.
func TestSystemSnapshotNeedsTUN(t *testing.T) {
	if _, err := tailnetSnapshotFrom(newFakeNode().st); err == nil {
		t.Fatal("a status without a TUN made a system snapshot")
	}
}

func TestTailnetNames(t *testing.T) {
	for host, want := range map[string]string{
		"studio-gorex": "studio",
		"studio":       "studio",
		"gorex":        "gorex",
		"-gorex":       "-gorex",
		"a-gorex-box":  "a-gorex-box",
	} {
		if got := tailnetName(host); got != want {
			t.Errorf("tailnetName(%q) = %q; want %q", host, got, want)
		}
	}
}

// TestBuiltinTailnetEnablesAtStart turns the built-in node on when the
// server starts with the mode already on, as after an upgrade, and shows
// why when that fails.
func TestBuiltinTailnetEnablesAtStart(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s, _ := testServer(t)
		oldSide, oldEnable, oldNode := tailnetBuiltin, EnableBuiltinTailscale, TailnetNode
		t.Cleanup(func() { tailnetBuiltin, EnableBuiltinTailscale, TailnetNode = oldSide, oldEnable, oldNode })
		tailnetBuiltin, TailnetNode = true, nil
		called := make(chan struct{}, 4)
		EnableBuiltinTailscale = func() error {
			called <- struct{}{}
			if fail {
				return errors.New("the gateway is not running")
			}
			return nil
		}
		if err := writeJSON(remotePath(), RemoteConfig{Tailnet: true}); err != nil {
			t.Fatal(err)
		}
		s.remoteLoad()
		t.Cleanup(func() {
			s.rem.mu.Lock()
			s.tailnetStopLocked()
			s.rem.mu.Unlock()
		})
		select {
		case <-called:
		case <-time.After(2 * time.Second):
			t.Fatal("the server started with the mode on and did not turn the built-in node on")
		}
		s.rem.mu.Lock()
		b := s.rem.tailnet
		s.rem.mu.Unlock()
		if b == nil {
			t.Fatal("no broker")
		}
		var st TailnetStatus
		for range 100 {
			if st = b.status(); !fail || strings.Contains(st.Error, "gateway is not running") {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if fail != strings.Contains(st.Error, "gateway is not running") {
			t.Fatalf("fail %v: status = %+v", fail, st)
		}
		if len(called) != 0 {
			t.Fatalf("fail %v: the node was turned on more than once", fail)
		}
	}
}

// TestBuiltinTailnetStartingBlip keeps the links over a node that reads
// as starting a little while, and drops them once it stays so. A node
// that needs a sign-in drops them at once.
func TestBuiltinTailnetStartingBlip(t *testing.T) {
	setState := func(n *fakeNode, state string) {
		n.mu.Lock()
		n.st.BackendState = state
		n.mu.Unlock()
	}
	link := func(b *systemTailnet) net.Conn {
		remote, rp := net.Pipe()
		t.Cleanup(func() { rp.Close() })
		b.mu.Lock()
		b.links[remote] = tailnetLink{peer: b.snap.byIP[builtinAlpha]}
		b.mu.Unlock()
		return rp
	}

	n := newFakeNode()
	b := builtinBroker(t, nodeFunc(n))
	b.refresh(nil)
	link(b)
	setState(n, "Starting")
	b.refresh(nil)
	st := b.status()
	if !b.linked() || n.listener().isClosed() {
		t.Fatal("one read of a starting node dropped the link or the listener")
	}
	if st.Node != NodeStarting || !strings.Contains(st.Error, "starting") {
		t.Fatalf("status = %+v; want starting with its reason", st)
	}
	for range tailnetFailures - 1 {
		b.refresh(nil)
	}
	if b.linked() || !n.listener().isClosed() {
		t.Fatalf("%d reads of a starting node kept the link", tailnetFailures)
	}

	n = newFakeNode()
	b = builtinBroker(t, nodeFunc(n))
	b.refresh(nil)
	link(b)
	setState(n, "NeedsLogin")
	b.refresh(nil)
	if b.linked() || !n.listener().isClosed() {
		t.Fatal("a node that needs a sign-in kept the link")
	}
}
