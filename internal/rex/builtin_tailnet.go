package rex

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"time"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

// TailnetDevice is the gateway's built-in Tailscale node, as the broker
// uses it on Windows and macOS. Its dynamic type must be comparable, a
// pointer: the broker tells a replaced node by it.
type TailnetDevice interface {
	// StatusWithPeers is the node's status with the devices it reaches.
	StatusWithPeers(ctx context.Context) (*ipnstate.Status, error)
	WhoIs(ctx context.Context, addr string) (*apitype.WhoIsResponse, error)
	// Listen listens on the tailnet only, in the node's network stack.
	Listen(network, addr string) (net.Listener, error)
	// DialPeer dials a peer's own address, and fails for any other: it
	// never takes the system's network.
	DialPeer(ctx context.Context, network, addr string) (net.Conn, error)
}

// TailnetNode, when set, returns the built-in Tailscale node of the
// gateway in this process, or nil while it is off. The broker asks for it
// on every read.
var TailnetNode func() TailnetDevice

// EnableBuiltinTailscale, when set, turns built-in Tailscale on, as
// gorex rexapi tailscale on does. It keeps the saved
// allow_tailnet_members and never turns it on itself. Where the built-in
// node carries discovery, turning the tailnet mode on calls it, and so
// does a server that starts with the mode on.
var EnableBuiltinTailscale func() error

func currentTailnetNode() TailnetDevice {
	if TailnetNode == nil {
		return nil
	}
	return TailnetNode()
}

// tailnetVia is what the broker needs of Tailscale besides the status:
// who is at an address, a listener on port 7464, and a dial to a peer.
type tailnetVia interface {
	WhoIs(ctx context.Context, addr string) (*apitype.WhoIsResponse, error)
	listen(ctx context.Context, ip netip.Addr) (net.Listener, error)
	dial(ctx context.Context, from netip.Addr, addr string) (net.Conn, error)
}

// systemVia is the system client's: sockets bound to its interface.
type systemVia struct {
	api    systemTailscale
	device net.Interface
}

func (v systemVia) WhoIs(ctx context.Context, addr string) (*apitype.WhoIsResponse, error) {
	return v.api.WhoIs(ctx, addr)
}

func (v systemVia) listen(ctx context.Context, ip netip.Addr) (net.Listener, error) {
	lc := net.ListenConfig{Control: tailnetSocketControl(v.device)}
	return lc.Listen(ctx, "tcp4", netip.AddrPortFrom(ip, tailnetPort).String())
}

func (v systemVia) dial(ctx context.Context, from netip.Addr, addr string) (net.Conn, error) {
	d := net.Dialer{Control: tailnetSocketControl(v.device), LocalAddr: &net.TCPAddr{IP: from.AsSlice()}}
	return d.DialContext(ctx, "tcp4", addr)
}

// builtinVia is the built-in node's: its own network stack, which no
// interface of the machine reaches.
type builtinVia struct{ node TailnetDevice }

func (v builtinVia) WhoIs(ctx context.Context, addr string) (*apitype.WhoIsResponse, error) {
	return v.node.WhoIs(ctx, addr)
}

func (v builtinVia) listen(_ context.Context, ip netip.Addr) (net.Listener, error) {
	return v.node.Listen("tcp4", netip.AddrPortFrom(ip, tailnetPort).String())
}

func (v builtinVia) dial(ctx context.Context, _ netip.Addr, addr string) (net.Conn, error) {
	return v.node.DialPeer(ctx, "tcp4", addr)
}

// tailnetOff is an answer that the built-in node cannot carry discovery
// now, with the reason the status shows. A passing one, such as a node
// that reconnects, counts as a failed read; any other is definite.
type tailnetOff struct {
	reason  string
	passing bool
}

func (e *tailnetOff) Error() string { return e.reason }

// readBuiltin reads the built-in node's status and makes its snapshot.
func (t *systemTailnet) readBuiltin(ctx context.Context) (*tailnetSnapshot, error) {
	node := t.node()
	if node == nil {
		t.noteNode(NodeOff, "", "")
		return nil, &tailnetOff{reason: "built-in Tailscale is off"}
	}
	st, err := node.StatusWithPeers(ctx)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, errTailnetUnavailable
	}
	state, account, device := builtinNodeState(st)
	t.noteNode(state, account, device)
	switch {
	case state == NodeNeedsLogin:
		return nil, &tailnetOff{reason: "built-in Tailscale needs a sign-in"}
	case state == NodeNeedsApproval:
		return nil, &tailnetOff{reason: "the tailnet's admin has to approve this device"}
	case state != NodeRunning:
		// A node that reconnects keeps its links a while; a new one does
		// not.
		return nil, &tailnetOff{reason: "built-in Tailscale is starting", passing: t.sameNode(node)}
	case st.Self != nil && st.Self.IsTagged():
		return nil, &tailnetOff{reason: "built-in Tailscale's device is tagged: only a device a user signed in finds that user's devices"}
	case st.Self != nil && st.Self.Expired:
		return nil, &tailnetOff{reason: "built-in Tailscale's sign-in expired: sign in again"}
	}
	if err := tailnetUsable(st); err != nil {
		return nil, &tailnetOff{reason: "built-in Tailscale cannot find devices: " + err.Error()}
	}
	ip := tailnetIPv4(st.TailscaleIPs)
	if !ip.IsValid() {
		return nil, &tailnetOff{reason: "built-in Tailscale has no IPv4 address"}
	}
	return tailnetSnapshotOf(st, ip, net.Interface{}, node), nil
}

// builtinNodeState is a node's state as TailnetStatus tells it, with the
// account and device name of a running one.
func builtinNodeState(st *ipnstate.Status) (state, account, device string) {
	switch st.BackendState {
	case ipn.NeedsLogin.String():
		return NodeNeedsLogin, "", ""
	case ipn.NeedsMachineAuth.String():
		return NodeNeedsApproval, "", ""
	case ipn.Running.String():
	default:
		return NodeStarting, "", ""
	}
	if self := st.Self; self != nil {
		device = strings.TrimSuffix(self.DNSName, ".")
		if device == "" {
			device = self.HostName
		}
		if !self.IsTagged() {
			if u, ok := st.User[self.UserID]; ok {
				account = u.LoginName
			}
		}
	}
	return NodeRunning, account, device
}

// sameNode tells whether node is the one the snapshot was read from, or
// there is no snapshot.
func (t *systemTailnet) sameNode(node TailnetDevice) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snap == nil || t.snap.node == node
}

func (t *systemTailnet) noteNode(state, account, device string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nodeState, t.account, t.device = state, account, device
}

// unavailable drops the listener and the links at once, with the reason:
// the node's own word, unlike a failed read, is not a slow answer.
func (t *systemTailnet) unavailable(reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastRead = time.Now()
	t.failures = 0
	t.errText = reason
	t.replaceLocked(nil, nil)
}

// enableBuiltin turns the built-in node on for a broker that starts with
// the mode already on, as after an upgrade: remote.tailnetSet does it
// only when the mode is turned on. It runs off r.mu, since the gateway
// answers in its own time; its error shows in the status.
func (t *systemTailnet) enableBuiltin(enable func() error) {
	if err := enable(); err != nil {
		t.mu.Lock()
		t.enableErr = "turning built-in Tailscale on: " + err.Error()
		t.mu.Unlock()
		return
	}
	select {
	case t.wake <- struct{}{}:
	default:
	}
}
