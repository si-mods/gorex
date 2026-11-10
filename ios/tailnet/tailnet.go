// Package tailnet is Rex Go's built-in Tailscale: a device of its own on
// the user's tailnet, in the app's process, with a SOCKS5 proxy on
// loopback through which the app reaches hosts on the tailnet. No system
// VPN is involved. gomobile binds it for Swift (ios/scripts/build-tailnet);
// docs/ios-tailnet.md has the design.
package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"time"

	"gorex/internal/tailnode"

	"tailscale.com/ipn/ipnstate"
)

// Callback gets the status, as Status gives it, each time it may have
// changed. It is called on a goroutine of its own.
type Callback interface {
	Changed(status string)
}

// config is Start's JSON: the device's name, its control server (empty
// for Tailscale's own) and an auth key, used once and never kept.
type config struct {
	Hostname   string `json:"hostname"`
	ControlURL string `json:"control_url"`
	AuthKey    string `json:"auth_key"`
}

func parseConfig(s string) (config, error) {
	var c config
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return c, err
	}
	if !tailnode.ValidHostname(c.Hostname) {
		return c, errors.New("invalid device name")
	}
	if c.ControlURL != "" && !tailnode.ValidControlURL(c.ControlURL) {
		return c, errors.New("invalid control server URL")
	}
	return c, nil
}

var (
	mu     sync.Mutex
	node   *tailnode.Node
	prox   *proxy
	cancel context.CancelFunc
)

// Start starts the device with configJSON, keeping its state in
// stateDir, after stopping one already running. It signs in with the
// auth key when there is one, else asks for an interactive login whose
// URL the status carries.
func Start(configJSON, stateDir string, cb Callback) error {
	c, err := parseConfig(configJSON)
	if err != nil {
		return err
	}
	Stop()
	n := tailnode.New(tailnode.Options{Dir: stateDir, Hostname: c.Hostname, AuthKey: c.AuthKey, ControlURL: c.ControlURL,
		Logf: func(string, ...any) {}})
	if err := n.Start(); err != nil {
		n.Close()
		return err
	}
	p, err := startProxy(n.StatusWithPeers, n.Dial)
	if err != nil {
		n.Close()
		return err
	}
	ctx, stop := context.WithCancel(context.Background())
	mu.Lock()
	node, prox, cancel = n, p, stop
	mu.Unlock()
	if cb != nil {
		go n.Watch(ctx, func() { cb.Changed(status(ctx, n)) })
	}
	return nil
}

// running is the device, or an error when it isn't started.
func running() (*tailnode.Node, error) {
	mu.Lock()
	defer mu.Unlock()
	if node == nil {
		return nil, errors.New("tailnet not started")
	}
	return node, nil
}

// Login starts an interactive login; its URL arrives in the status.
func Login() error {
	n, err := running()
	if err != nil {
		return err
	}
	ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	return n.StartLogin(ctx)
}

// SetHostname renames the running device, as the gateway does.
func SetHostname(name string) error {
	if !tailnode.ValidHostname(name) {
		return errors.New("invalid device name")
	}
	n, err := running()
	if err != nil {
		return err
	}
	ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	return n.SetHostname(ctx, name)
}

// Logout signs the device out.
func Logout() error {
	n, err := running()
	if err != nil {
		return err
	}
	ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	return n.Logout(ctx)
}

// Status is the device's status as JSON: its backend state, the URL of
// a login it waits for, its name and addresses, the tailnet's MagicDNS
// suffix and the peers. Not started, the state is "Stopped"; an error is in
// "error".
func Status() string {
	n, err := running()
	if err != nil {
		return encode(statusJSON{State: "Stopped"})
	}
	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	return status(ctx, n)
}

type statusJSON struct {
	State   string   `json:"state"`
	AuthURL string   `json:"auth_url,omitempty"`
	Name    string   `json:"name,omitempty"`
	IPs     []string `json:"ips,omitempty"`
	Suffix  string   `json:"suffix,omitempty"`
	Peers   []peer   `json:"peers,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// peer is a device the node can reach: its MagicDNS name and addresses,
// whether it is online, and, while the node talks to it, how: "direct",
// "derp" or "peer-relay", as tailscale status tells.
type peer struct {
	Name   string   `json:"name"`
	IPs    []string `json:"ips"`
	Online bool     `json:"online"`
	Path   string   `json:"path,omitempty"`
}

func status(ctx context.Context, n *tailnode.Node) string {
	st, err := n.StatusWithPeers(ctx)
	if err != nil {
		return encode(statusJSON{State: "Unknown", Error: err.Error()})
	}
	return encode(fromStatus(st))
}

func fromStatus(st *ipnstate.Status) statusJSON {
	s := statusJSON{State: st.BackendState, AuthURL: st.AuthURL}
	if st.Self != nil {
		s.Name = strings.TrimSuffix(st.Self.DNSName, ".")
	}
	s.IPs = addrs(st.TailscaleIPs)
	for _, k := range st.Peers() {
		p := st.Peer[k]
		s.Peers = append(s.Peers, peer{Name: strings.TrimSuffix(p.DNSName, "."), IPs: addrs(p.TailscaleIPs), Online: p.Online, Path: path(p)})
	}
	if st.CurrentTailnet != nil {
		s.Suffix = st.CurrentTailnet.MagicDNSSuffix
	}
	return s
}

// path is how the node reaches an active peer, as tailscale status
// tells; empty for an idle one.
func path(p *ipnstate.PeerStatus) string {
	switch {
	case !p.Active:
		return ""
	case p.CurAddr != "":
		return "direct"
	case p.PeerRelay != "":
		return "peer-relay"
	case p.Relay != "":
		return "derp"
	}
	return ""
}

func addrs(ips []netip.Addr) []string {
	var s []string
	for _, ip := range ips {
		s = append(s, ip.String())
	}
	return s
}

func encode(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Proxy is the device's SOCKS5 proxy (proxy.go) as JSON: its address,
// as 127.0.0.1:port, and its password; the user name is "tsnet". Each
// start of the device has a new port and password.
func Proxy() (string, error) {
	mu.Lock()
	p := prox
	mu.Unlock()
	if p == nil {
		return "", errors.New("tailnet not started")
	}
	return encode(map[string]string{"address": p.ln.Addr().String(), "password": p.password}), nil
}

// Stop stops the device and its proxy.
func Stop() {
	mu.Lock()
	n, p, stop := node, prox, cancel
	node, prox, cancel = nil, nil, nil
	mu.Unlock()
	if n != nil {
		stop()
		p.close()
		n.Close()
	}
}

// ValidControlURL tells a control server's URL, as the gateway does.
func ValidControlURL(s string) bool { return tailnode.ValidControlURL(s) }

// ValidHostname tells a device name, as the gateway does.
func ValidHostname(s string) bool { return tailnode.ValidHostname(s) }
