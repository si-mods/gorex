// Package tailnode is a copy of Tailscale in the process, joining a
// tailnet as a device of its own, with the checks on its settings.
package tailnode

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/net/tsaddr"
	"tailscale.com/tailcfg"
	"tailscale.com/tsd"
	"tailscale.com/tsnet"
)

// Node is a tsnet device. Its methods other than Start, ListenTLS and
// Close need it started.
type Node struct {
	srv *tsnet.Server
	lc  *local.Client
}

// Options is what New takes. It stores the device's state in Dir, and
// signs in to the control server at ControlURL when that is not empty.
// Logf gets the device's user log.
type Options struct {
	Dir        string
	Hostname   string
	AuthKey    string
	ControlURL string
	Tags       []string
	Logf       func(format string, args ...any)
}

// New makes a Node. It does not start it.
func New(o Options) *Node {
	return &Node{srv: &tsnet.Server{
		Dir:           o.Dir,
		Hostname:      o.Hostname,
		AuthKey:       o.AuthKey,
		ControlURL:    o.ControlURL,
		AdvertiseTags: o.Tags,
		UserLogf:      o.Logf,
		Logf:          func(string, ...any) {},
	}}
}

// Start starts the device, which signs in when it can.
func (n *Node) Start() error {
	if err := n.srv.Start(); err != nil {
		return err
	}
	lc, err := n.srv.LocalClient()
	if err != nil {
		return err
	}
	n.lc = lc
	return nil
}

func (n *Node) Status(ctx context.Context) (*ipnstate.Status, error) {
	return n.lc.StatusWithoutPeers(ctx)
}

// StatusWithPeers is Status with the devices the node can reach.
func (n *Node) StatusWithPeers(ctx context.Context) (*ipnstate.Status, error) {
	return n.lc.Status(ctx)
}

// Watch calls changed each time the device's state may have changed,
// until ctx ends.
func (n *Node) Watch(ctx context.Context, changed func()) {
	for ctx.Err() == nil {
		w, err := n.lc.WatchIPNBus(ctx, ipn.NotifyInitialState)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				continue
			}
		}
		for {
			if _, err := w.Next(); err != nil {
				break
			}
			changed()
		}
		w.Close()
	}
}

// ListenTLS listens for HTTPS on port 443 of the tailnet. It blocks
// until the device runs.
func (n *Node) ListenTLS() (net.Listener, error) { return n.srv.ListenTLS("tcp", ":443") }

func (n *Node) WhoIs(ctx context.Context, addr string) (*apitype.WhoIsResponse, error) {
	return n.lc.WhoIs(ctx, addr)
}

func (n *Node) StartLogin(ctx context.Context) error { return n.lc.StartLoginInteractive(ctx) }

func (n *Node) Logout(ctx context.Context) error { return n.lc.Logout(ctx) }

func (n *Node) SetHostname(ctx context.Context, name string) error {
	_, err := n.lc.EditPrefs(ctx, &ipn.MaskedPrefs{Prefs: ipn.Prefs{Hostname: name}, HostnameSet: true})
	return err
}

// Dial connects to addr on the tailnet, once the device runs. An
// address that is not a known peer's is dialed through the system's
// network instead, so callers check it first, or use DialPeer.
func (n *Node) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return n.srv.Dial(ctx, network, addr)
}

// Listen listens on the tailnet only, as tsnet's Server.Listen: the
// listener is in the device's own network stack, and nothing on the
// machine's interfaces reaches it.
func (n *Node) Listen(network, addr string) (net.Listener, error) {
	return n.srv.Listen(network, addr)
}

// ErrNotPeer is DialPeer's error for an address that is not a peer's.
var ErrNotPeer = errors.New("tailnode: not a peer's address")

// DialPeer connects over TCP to addr, an IP and port, only when the IP is
// one of a peer's own addresses in the device's network map. Unlike Dial,
// it never takes the system's network: it dials in the device's network
// stack, or fails.
func (n *Node) DialPeer(ctx context.Context, network, addr string) (net.Conn, error) {
	return dialPeer(ctx, network, addr, n.isPeer, n.dialNetstack)
}

// dialPeer is DialPeer with its checks and dial given.
func dialPeer(ctx context.Context, network, addr string, isPeer func(context.Context, netip.Addr) bool,
	dial func(context.Context, netip.AddrPort) (net.Conn, error)) (net.Conn, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
	default:
		return nil, fmt.Errorf("tailnode: DialPeer takes TCP, not %q", network)
	}
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return nil, fmt.Errorf("tailnode: DialPeer takes an IP and port: %w", err)
	}
	ap = netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
	ip := ap.Addr()
	if network == "tcp4" && !ip.Is4() || network == "tcp6" && !ip.Is6() || !isPeer(ctx, ip) {
		return nil, ErrNotPeer
	}
	return dial(ctx, ap)
}

// isPeer tells whether ip is one of a peer's own addresses in the
// network map. A WhoIs with no port looks at those alone, not at the
// routes a peer advertises.
func (n *Node) isPeer(ctx context.Context, ip netip.Addr) bool {
	if n.lc == nil || !ip.IsValid() {
		return false
	}
	if ip4, ip6 := n.srv.TailscaleIPs(); ip == ip4 || ip == ip6 {
		return false
	}
	who, err := n.lc.WhoIs(ctx, ip.String())
	return err == nil && who != nil && who.Node != nil
}

// dialNetstack dials in the device's network stack, where an address
// with no peer goes nowhere.
func (n *Node) dialNetstack(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
	sys := n.srv.Sys()
	if sys == nil {
		return nil, ErrNotPeer
	}
	d, ok := sys.Dialer.GetOK()
	if !ok || d == nil || d.NetstackDialTCP == nil {
		return nil, ErrNotPeer
	}
	return d.NetstackDialTCP(ctx, dst)
}

// Ping sends a disco ping of size bytes to the peer at ip, as tailscale
// ping does, over the path the node would use: direct or through DERP.
func (n *Node) Ping(ctx context.Context, ip netip.Addr, size int) (*ipnstate.PingResult, error) {
	return n.lc.PingWithOpts(ctx, ip, tailcfg.PingDisco, local.PingOpts{Size: size})
}

func (n *Node) Close() error { return n.srv.Close() }

// Sys is the started device's subsystems, as tsnet's Server.Sys, which
// is not a stable API.
func (n *Node) Sys() *tsd.System { return n.srv.Sys() }

var hostnameRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// ValidHostname tells a Tailscale device name: 1 to 63 ASCII letters,
// digits and hyphens, not starting or ending with a hyphen.
func ValidHostname(s string) bool { return hostnameRE.MatchString(s) }

// ValidControlURL tells a Tailscale control server's URL: an https URL
// with a host, such as https://headscale.example.com. An http URL is one
// only when its host is localhost or a loopback, private or link-local IP
// address outside Tailscale's own ranges: the device fetches the server's
// key over it unauthenticated, so anyone on the path could swap the key.
// The host is judged as written; a name is not resolved.
func ValidControlURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		if strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), "localhost") {
			return true
		}
		ip, err := netip.ParseAddr(u.Hostname())
		ip = ip.Unmap()
		return err == nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) &&
			!tsaddr.TailscaleULARange().Contains(ip)
	}
	return false
}
