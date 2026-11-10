// Package tailnode is a copy of Tailscale in the process, joining a
// tailnet as a device of its own, with the checks on its settings.
package tailnode

import (
	"context"
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
// network instead, so callers check it first.
func (n *Node) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return n.srv.Dial(ctx, network, addr)
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
