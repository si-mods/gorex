package rexapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// LANPort is the port the gateway takes on the local network by default:
// Rex's own.
const LANPort = 7297

// LANConfig is the gateway's listener on the local network, kept in
// lan.json in the gateway's state directory. Rex's apps connect to it by
// plain HTTP. Rex's API has no password on such a listener, so who may
// connect is a list of networks: a caller from any other address gets
// 403.
type LANConfig struct {
	Enabled bool `json:"enabled"`
	// Listen is the address to listen on, host:port.
	Listen string `json:"listen,omitempty"`
	// Allow are the networks let in, as CIDRs; a bare address stands for
	// itself alone.
	Allow []string `json:"allow,omitempty"`
	// ShowDesktopSessions includes desktop Tailscale sessions outside the host layout.
	ShowDesktopSessions bool `json:"showDesktopSessions,omitempty"`
}

// LANStatus is what the gateway did with its LANConfig, kept in
// lan-status.json beside it for the app and the CLI to show.
type LANStatus struct {
	// Running tells whether a gateway runs at all.
	Running bool `json:"running"`
	// Listening is the address the listener has, "" for none.
	Listening string `json:"listening,omitempty"`
	// Error is why it does not listen, when it should.
	Error   string    `json:"error,omitempty"`
	Updated time.Time `json:"updated"`
}

func lanConfPath(dir string) string   { return filepath.Join(dir, "lan.json") }
func lanStatusPath(dir string) string { return filepath.Join(dir, "lan-status.json") }

// LoadLAN reads the local network configuration in dir; a missing file is
// the default, off.
func LoadLAN(dir string) (LANConfig, error) {
	var c LANConfig
	b, err := os.ReadFile(lanConfPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", lanConfPath(dir), err)
	}
	return c, nil
}

// SaveLAN checks c and writes it in dir, where a gateway running picks it
// up within a second.
func SaveLAN(dir string, c LANConfig) error {
	if _, err := c.check(); err != nil {
		return err
	}
	return writeJSON(lanConfPath(dir), c)
}

// LoadLANStatus reads what the gateway last reported of its listener.
func LoadLANStatus(dir string) LANStatus {
	var s LANStatus
	if b, err := os.ReadFile(lanStatusPath(dir)); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

// SystemTailnetPairURL reports the gateway address when the configured
// mobile listener is active on this machine's system Tailscale IP.
func SystemTailnetPairURL(ip netip.Addr) string {
	if !ip.IsValid() {
		return ""
	}
	dir := TailnetDir()
	conf, err := LoadLAN(dir)
	if err != nil || !conf.Enabled || len(conf.Allow) == 0 {
		return ""
	}
	status := LoadLANStatus(dir)
	addr, err := netip.ParseAddrPort(status.Listening)
	if err != nil || !status.Running || status.Error != "" || addr.Addr() != ip || status.Listening != conf.Listen {
		return ""
	}
	return "http://" + addr.String()
}

func saveLANStatus(dir string, s LANStatus) {
	s.Updated = time.Now()
	if err := writeJSON(lanStatusPath(dir), s); err != nil {
		logf("lan: %v", err)
	}
}

// writeJSON writes v to path for this user only, whole or not at all.
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// check validates c, and returns its networks.
func (c LANConfig) check() ([]netip.Prefix, error) {
	if c.Listen != "" {
		host, port, err := net.SplitHostPort(c.Listen)
		if err != nil || port == "" {
			return nil, fmt.Errorf("listen address %q is not host:port", c.Listen)
		}
		if host != "" {
			if _, err := netip.ParseAddr(host); err != nil {
				return nil, fmt.Errorf("listen address %q is not an IP address and a port", c.Listen)
			}
		}
	}
	return ParseNetworks(c.Allow)
}

// ParseNetworks reads networks written as CIDRs or bare addresses.
func ParseNetworks(list []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(list))
	for _, s := range list {
		p, err := ParseNetwork(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// ParseNetwork reads one network: a CIDR, such as 192.168.88.0/24, or an
// address, which stands for itself (/32 or /128).
func ParseNetwork(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%q is not a network such as 192.168.1.0/24", s)
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not an address nor a network such as 192.168.1.0/24", s)
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// allowedPeer reports whether a caller at remoteAddr, host:port, is in one
// of the networks, and returns its address.
func allowedPeer(nets []netip.Prefix, remoteAddr string) (netip.Addr, bool) {
	ap, err := netip.ParseAddrPort(remoteAddr)
	var a netip.Addr
	if err == nil {
		a = ap.Addr()
	} else if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		a, _ = netip.ParseAddr(host)
	}
	if !a.IsValid() {
		return a, false
	}
	a = a.Unmap()
	for _, p := range nets {
		if p.Contains(a) {
			return a, true
		}
	}
	return a, false
}

// DefaultLAN returns the listener's defaults: the machine's private IPv4
// address on its local network, with Rex's port, and that network alone.
// It returns "" and none when the machine has no such address.
func DefaultLAN() (listen string, allow []string) {
	ifs, err := net.Interfaces()
	if err != nil {
		return "", nil
	}
	type cand struct {
		name string
		net  *net.IPNet
	}
	var cands []cand
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || virtualInterface(ifc.Name) {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || !n.IP.IsPrivate() {
				continue
			}
			cands = append(cands, cand{ifc.Name, n})
		}
	}
	if len(cands) == 0 {
		return "", nil
	}
	// en0 first on a Mac, else the order the system gives.
	slices.SortStableFunc(cands, func(x, y cand) int {
		switch {
		case x.name == "en0" && y.name != "en0":
			return -1
		case y.name == "en0" && x.name != "en0":
			return 1
		}
		return 0
	})
	n := cands[0].net
	ip := n.IP.To4()
	ones, _ := n.Mask.Size()
	prefix := netip.PrefixFrom(netip.AddrFrom4([4]byte(ip)), ones).Masked()
	return net.JoinHostPort(ip.String(), fmt.Sprint(LANPort)), []string{prefix.String()}
}

// virtualInterface tells the interfaces of tunnels, bridges and virtual
// machines, which are not the local network a phone reaches.
func virtualInterface(name string) bool {
	for _, p := range []string{"utun", "tailscale", "tun", "tap", "wg", "bridge", "vmnet", "vboxnet", "docker", "veth", "awdl", "llw", "anpi", "gif", "stf", "ap"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// WithDefaults fills an empty listen address and an empty list of
// networks with DefaultLAN's.
func (c LANConfig) WithDefaults() LANConfig {
	if c.Listen != "" && len(c.Allow) > 0 {
		return c
	}
	listen, allow := DefaultLAN()
	if c.Listen == "" {
		c.Listen = listen
	}
	if len(c.Allow) == 0 {
		if host, _, err := net.SplitHostPort(c.Listen); err == nil && host != "" && host != "0.0.0.0" && host != "::" {
			// The network of the address listened on.
			if a, err := netip.ParseAddr(host); err == nil {
				if p := networkOf(a); p.IsValid() {
					allow = []string{p.String()}
				}
			}
		}
		c.Allow = allow
	}
	return c
}

// networkOf returns the network of the interface holding address a.
func networkOf(a netip.Addr) netip.Prefix {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return netip.Prefix{}
	}
	for _, x := range addrs {
		n, ok := x.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(n.IP)
		if !ok || ip.Unmap() != a {
			continue
		}
		ones, _ := n.Mask.Size()
		return netip.PrefixFrom(a, ones).Masked()
	}
	return netip.Prefix{}
}

// lanListener is the gateway's listener on the local network.
type lanListener struct {
	g   *Gateway
	dir string

	mu      sync.Mutex
	conf    LANConfig
	nets    []netip.Prefix
	ln      *trackedListener
	err     string
	applied bool
	// refused remembers when each refused address was logged last, so
	// that a client retrying does not fill the log.
	refused map[netip.Addr]time.Time
}

func newLANListener(g *Gateway, dir string) *lanListener {
	return &lanListener{g: g, dir: dir, refused: map[netip.Addr]time.Time{}}
}

// apply makes the listener what c says: it listens on c.Listen while
// enabled, and lets in c.Allow; the networks change at once, without
// closing the connections of callers still allowed.
func (l *lanListener) apply(c LANConfig) {
	nets, err := c.check()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.applied && sameLAN(l.conf, c) {
		return
	}
	addrChanged := !l.applied || l.conf.Enabled != c.Enabled || l.conf.Listen != c.Listen || l.ln == nil
	l.applied = true
	l.conf = c
	if err != nil {
		l.nets, l.err = nil, err.Error()
		l.closeLocked()
		l.reportLocked()
		return
	}
	l.nets, l.err = nets, ""
	if !c.Enabled {
		l.closeLocked()
		l.reportLocked()
		return
	}
	if c.Listen == "" {
		l.closeLocked()
		l.err = "no address to listen on: set one"
		l.reportLocked()
		return
	}
	if len(nets) == 0 {
		l.closeLocked()
		l.err = "no network is allowed: add one"
		l.reportLocked()
		return
	}
	if addrChanged {
		l.closeLocked()
		ln, err := net.Listen("tcp", c.Listen)
		if err != nil {
			l.err = err.Error()
			l.reportLocked()
			logf("lan: %v", err)
			return
		}
		tl := newTrackedListener(ln)
		l.ln = tl
		go func() {
			if err := l.g.serveOn(tl, transportLAN); err != nil {
				logf("lan: %v", err)
			}
		}()
		logf("lan: listening on http://%s for %s", ln.Addr(), strings.Join(c.Allow, ", "))
	}
	l.reportLocked()
}

func sameLAN(a, b LANConfig) bool {
	return a.Enabled == b.Enabled && a.Listen == b.Listen && a.ShowDesktopSessions == b.ShowDesktopSessions && slices.Equal(a.Allow, b.Allow)
}

func (l *lanListener) showDesktopSessions() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ln != nil && l.conf.ShowDesktopSessions
}

func (l *lanListener) closeLocked() {
	if l.ln != nil {
		l.ln.Close()
		l.ln = nil
	}
}

// close stops listening, as the gateway stops.
func (l *lanListener) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closeLocked()
	saveLANStatus(l.dir, LANStatus{})
}

func (l *lanListener) reportLocked() {
	s := LANStatus{Running: true, Error: l.err}
	if l.ln != nil {
		s.Listening = l.ln.Addr().String()
	}
	saveLANStatus(l.dir, s)
}

// admit identifies a caller on the local network, or refuses it with 403
// when its address is in no network allowed. A refusal is logged with the
// address, so that the user can find the one to add.
func (l *lanListener) admit(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	l.mu.Lock()
	nets := l.nets
	l.mu.Unlock()
	a, ok := allowedPeer(nets, r.RemoteAddr)
	if !ok {
		l.mu.Lock()
		if time.Since(l.refused[a]) > time.Minute {
			l.refused[a] = time.Now()
			logf("lan: refused %s: not in the networks allowed (add it with gorex rexapi lan allow %s)", a, a)
		}
		l.mu.Unlock()
		writeRefusal(w, fmt.Sprintf("GoRex lets in only the networks it allows, and %s is in none of them", a))
		return nil, false
	}
	return map[string]any{
		"kind": "anonymous", "transport": transportTCP,
		"remote_addr": a.String(), "display": a.String(),
	}, true
}
