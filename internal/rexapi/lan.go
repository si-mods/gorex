package rexapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"gorex/internal/atomicfile"
	"gorex/internal/pairaddr"
)

// LANPort is the port the gateway takes on the local network by default:
// Rex's own.
const LANPort = pairaddr.RexAPIPort

// LANConfig is the gateway's listener on the local network, kept in
// lan.json in the gateway's state directory. Rex's apps connect to it by
// plain HTTP. A caller must come from one of the networks allowed, and
// must show the pairing token, as "Authorization: Bearer <token>" or as
// the query parameter k; a caller from an address on the legacy list
// needs no token. Any other caller gets 403.
type LANConfig struct {
	Enabled bool `json:"enabled"`
	// Listen is the address to listen on, host:port.
	Listen string `json:"listen,omitempty"`
	// Allow are the networks let in, as CIDRs; a bare address stands for
	// itself alone.
	Allow []string `json:"allow,omitempty"`
	// Token is the pairing token: 32 random bytes, base64url. SaveLAN and
	// the gateway make one when it is missing.
	Token string `json:"token,omitempty"`
	// LegacyOpen are single addresses (/32 or /128) let in without the
	// token, for apps that cannot send one, such as Rex's own. They must
	// be in Allow too.
	LegacyOpen []string `json:"legacy_open,omitempty"`
}

// NewLANToken returns a new pairing token: 32 random bytes, base64url.
func NewLANToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// lanRefusalHint is what a caller without the token, and the log, are
// told to do.
func lanRefusalHint(a netip.Addr) string {
	return fmt.Sprintf("re-scan the pairing QR code, or run gorex rexapi lan legacy add %s", a)
}

// LANStatus is what the gateway did with its LANConfig, kept in
// lan-status.json beside it for the app and the CLI to show.
type LANStatus struct {
	// Running tells whether a gateway runs at all.
	Running bool `json:"running"`
	// Listening is the address the listener has, "" for none.
	Listening string `json:"listening,omitempty"`
	// Error is why it does not listen, when it should.
	Error string `json:"error,omitempty"`
	// WTListening is the WebTransport UDP address, "" for none.
	WTListening string `json:"wt_listening,omitempty"`
	// WTCertHash is the base64url SHA-256 hash of the WebTransport cert.
	WTCertHash string `json:"wt_cert_hash,omitempty"`
	// WTError is why the WebTransport listener failed, if any.
	WTError string    `json:"wt_error,omitempty"`
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
// up within a second. A c with no token keeps the one in dir, or gets a
// new one.
func SaveLAN(dir string, c LANConfig) error {
	if _, err := c.check(); err != nil {
		return err
	}
	fillLANToken(dir, &c)
	return writeJSON(lanConfPath(dir), c)
}

// fillLANToken gives c the token saved in dir when it has none, or a new
// one when dir has none either.
func fillLANToken(dir string, c *LANConfig) {
	if c.Token != "" {
		return
	}
	if old, err := LoadLAN(dir); err == nil && old.Token != "" {
		c.Token = old.Token
		return
	}
	c.Token = NewLANToken()
}

// RotateLANToken gives the listener in dir a new pairing token. A gateway
// running takes it within a second, and closes every connection on the
// local network: each device must show the new token.
func RotateLANToken(dir string) (LANConfig, error) {
	c, err := LoadLAN(dir)
	if err != nil {
		return c, err
	}
	c.Token = NewLANToken()
	return c, SaveLAN(dir, c)
}

// LoadLANStatus reads what the gateway last reported of its listener.
func LoadLANStatus(dir string) LANStatus {
	var s LANStatus
	if b, err := os.ReadFile(lanStatusPath(dir)); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

// PairURL returns the active listener's address with its pairing token,
// http://IP:PORT/#k=TOKEN, which another device on the same network
// scans. The token is in the fragment, which a client never sends. Rex
// pairs over its own Tailscale address (gorex rexapi pair); this is the
// local network's plain HTTP listener. Empty unless the listener is bound
// to a specific, reachable address: a wildcard or loopback one is only a
// bind address.
func (c LANConfig) PairURL(s LANStatus) string {
	if !c.Enabled || len(c.Allow) == 0 || c.Token == "" || !s.Running || s.Error != "" {
		return ""
	}
	configured, err := netip.ParseAddrPort(c.Listen)
	if err != nil {
		return ""
	}
	bound, err := netip.ParseAddrPort(s.Listening)
	if err != nil || bound != configured || bound.Port() == 0 ||
		!bound.Addr().IsGlobalUnicast() || bound.Addr().Zone() != "" {
		return ""
	}
	return "http://" + bound.String() + "/#k=" + c.Token
}

// WebOrigin is the page the web client is published at: the origin a host
// trusts, and the one a WebTransport pairing link opens.
const WebOrigin = "https://web.gorex.uk"

// WebPairURL is that page's address for this listener: the page, then the
// gateway's WebTransport address, its certificate's hash and the pairing
// token, all in the fragment, which a browser never sends anywhere
// (gorex rexapi lan qr prints it, and the desktop's pair dialog shows it
// as a code). Empty unless the listener is running with its WebTransport
// legs, a token and a reachable address: the page cannot reach it
// otherwise.
func (c LANConfig) WebPairURL(s LANStatus, origin string) string {
	if origin == "" || c.Token == "" || !s.Running || s.Error != "" ||
		s.WTListening == "" || s.WTCertHash == "" {
		return ""
	}
	addr := s.WTListening
	// A wildcard bind names no address to dial: the default listen's host
	// is the machine's own, which is what a device on the network opens.
	if host, port, err := net.SplitHostPort(addr); err == nil && (host == "0.0.0.0" || host == "::" || host == "") {
		if def, _ := DefaultLAN(); def != "" {
			if defHost, _, err := net.SplitHostPort(def); err == nil && defHost != "" {
				addr = net.JoinHostPort(defHost, port)
			}
		}
	}
	return fmt.Sprintf("%s/#host=%s&wt=sha256-%s&k=%s",
		strings.TrimRight(origin, "/"), url.QueryEscape("https://"+addr),
		url.QueryEscape(s.WTCertHash), url.QueryEscape(c.Token))
}

func saveLANStatus(dir string, s LANStatus) {
	s.Updated = time.Now()
	if err := writeJSON(lanStatusPath(dir), s); err != nil {
		logf("lan: %v", err)
	}
}

// writeJSON writes v to path for this user only, whole or not at all.
func writeJSON(path string, v any) error {
	return atomicfile.WriteJSON(path, v, 0o600)
}

// check validates c, and returns its networks.
func (c LANConfig) check() ([]netip.Prefix, error) {
	if _, err := c.legacy(); err != nil {
		return nil, err
	}
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

// legacy returns the addresses let in without the token: single
// addresses only, never a network.
func (c LANConfig) legacy() ([]netip.Addr, error) {
	out := make([]netip.Addr, 0, len(c.LegacyOpen))
	for _, s := range c.LegacyOpen {
		p, err := ParseLegacyAddr(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// ParseLegacyAddr reads an address for the legacy list: a bare address,
// or one written as /32 or /128. A network is refused: the list lets in
// devices one by one.
func ParseLegacyAddr(s string) (netip.Addr, error) {
	p, err := ParseNetwork(s)
	if err != nil {
		return netip.Addr{}, err
	}
	if !p.IsSingleIP() {
		return netip.Addr{}, fmt.Errorf("%q is a network: the legacy list takes single addresses only", strings.TrimSpace(s))
	}
	// An IPv4 address written as IPv6 (::ffff:a.b.c.d) is the IPv4 one, as
	// allowedPeer sees a caller.
	return p.Addr().Unmap(), nil
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
	wt  *wtListener

	mu      sync.Mutex
	conf    LANConfig
	nets    []netip.Prefix
	legacy  []netip.Addr
	ln      *trackedListener
	err     string
	applied bool
	// refused remembers when each refused address was logged last, so
	// that a client retrying does not fill the log.
	refused map[netip.Addr]time.Time
	// untokened is the same for callers allowed but with no token or a
	// wrong one.
	untokened map[netip.Addr]time.Time
}

func newLANListener(g *Gateway, dir string) *lanListener {
	return &lanListener{g: g, dir: dir, wt: newWTListener(g, dir),
		refused: map[netip.Addr]time.Time{}, untokened: map[netip.Addr]time.Time{}}
}

// apply makes the listener what c says: it listens on c.Listen while
// enabled, and lets in c.Allow with c.Token; the networks change at once,
// without closing the connections of callers still allowed. An enabled c
// with no token gets one, saved in lan.json: from then on, a caller
// without it is refused. A new token closes every connection.
func (l *lanListener) apply(c LANConfig) {
	nets, err := c.check()
	if err == nil && c.Enabled && c.Token == "" {
		var token string
		if token, err = l.saveNewToken(c); err == nil {
			c.Token = token
		}
	}
	legacy, _ := c.legacy()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.applied && sameLAN(l.conf, c) {
		return
	}
	addrChanged := !l.applied || l.conf.Enabled != c.Enabled || l.conf.Listen != c.Listen || l.ln == nil
	tokenChanged := l.applied && l.conf.Token != c.Token
	l.applied = true
	l.conf = c
	if err != nil {
		l.nets, l.legacy, l.err = nil, nil, err.Error()
		l.closeLocked()
		l.reportLocked()
		return
	}
	l.nets, l.legacy, l.err = nets, legacy, ""
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
		if l.wt != nil {
			l.wt.start(c.Listen)
		}
	} else if tokenChanged {
		// Every caller showed the old token: close them all, on HTTP,
		// WebSocket and WebTransport alike.
		l.ln.closeConns()
		if l.wt != nil {
			l.wt.close()
			l.wt.start(c.Listen)
		}
		logf("lan: the pairing token changed: closed every connection on the local network")
	}
	l.reportLocked()
}

// saveNewToken gives lan.json a pairing token, for c, a configuration
// with none, and returns it. The file is read again first: a token
// another writer saved meanwhile is the one, and only the token is added
// to what the file holds now, so that a concurrent SaveLAN is not undone.
// A token that cannot be saved is not used: a device could never learn
// it, so the listener stays off with the error instead.
func (l *lanListener) saveNewToken(c LANConfig) (string, error) {
	cur, err := LoadLAN(l.dir)
	if err == nil && cur.Token != "" {
		return cur.Token, nil
	}
	if err != nil {
		cur = c // no file yet: save what is applied
	}
	cur.Token = NewLANToken()
	if err := writeJSON(lanConfPath(l.dir), cur); err != nil {
		logf("lan: saving the pairing token: %v", err)
		return "", fmt.Errorf("could not save the pairing token: %w", err)
	}
	logf("lan: made a pairing token: a device without it is refused until it re-scans the pairing QR code, or until gorex rexapi lan legacy add lets its address in")
	return cur.Token, nil
}

// listener is the listener as server.status lists it, while it
// listens. Its callers are anonymous TCP ones, from the networks allowed.
func (l *lanListener) listener() (Listener, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ln == nil {
		return Listener{}, false
	}
	return Listener{
		Endpoint:  l.conf.Listen,
		Addresses: []string{l.ln.Addr().String()},
		Transport: transportTCP,
		Admission: AdmissionAnonymous,
	}, true
}

func sameLAN(a, b LANConfig) bool {
	return a.Enabled == b.Enabled && a.Listen == b.Listen && slices.Equal(a.Allow, b.Allow) &&
		a.Token == b.Token && slices.Equal(a.LegacyOpen, b.LegacyOpen)
}

func (l *lanListener) closeLocked() {
	if l.wt != nil {
		l.wt.close()
	}
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
	if l.wt != nil {
		s.WTListening, s.WTCertHash, s.WTError = l.wt.status()
	}
	saveLANStatus(l.dir, s)
}

// admit identifies a caller on the local network, or refuses it with 403
// when its address is in no network allowed, or when it shows no pairing
// token or a wrong one and its address is not on the legacy list. A
// request inside a WebTransport session that showed the token needs none
// (wtAuthenticated). A refusal is logged with the address, so that the
// user can find the one to add; the token is never logged.
func (l *lanListener) admit(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	l.mu.Lock()
	nets, legacy, token := l.nets, l.legacy, l.conf.Token
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
	if !wtAuthenticated(r.Context()) && !slices.Contains(legacy, a) && !tokenMatches(r, token) {
		l.mu.Lock()
		if time.Since(l.untokened[a]) > time.Minute {
			l.untokened[a] = time.Now()
			logf("lan: refused %s: no pairing token, or a wrong one (%s)", a, lanRefusalHint(a))
		}
		l.mu.Unlock()
		writeRefusal(w, fmt.Sprintf("GoRex needs this device's pairing token: %s on the computer", lanRefusalHint(a)))
		return nil, false
	}
	return map[string]any{
		"kind": "anonymous", "transport": transportTCP,
		"remote_addr": a.String(), "display": a.String(),
	}, true
}

// tokenMatches reports whether r shows token, as "Authorization: Bearer
// <token>" or as the query parameter k. No token lets no one in.
func tokenMatches(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	got := ""
	if scheme, rest, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok && strings.EqualFold(scheme, "Bearer") {
		got = strings.TrimSpace(rest)
	}
	if got == "" {
		got = r.URL.Query().Get("k")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// withoutToken returns r with the query parameter k taken out, so that
// nothing after admission, a trace among them, sees the token.
func withoutToken(r *http.Request) *http.Request {
	q := r.URL.Query()
	if !q.Has("k") {
		return r
	}
	q.Del("k")
	u := *r.URL
	u.RawQuery = q.Encode()
	r2 := r.WithContext(r.Context())
	r2.URL = &u
	r2.RequestURI = u.RequestURI()
	return r2
}

// wtAuthKey marks the context of a WebTransport session whose CONNECT
// showed the pairing token. Only the gateway sets it, in the context of
// the session's streams; no header or query a client sends can.
type wtAuthKey struct{}

func withWTAuth(ctx context.Context) context.Context {
	return context.WithValue(ctx, wtAuthKey{}, true)
}

func wtAuthenticated(ctx context.Context) bool {
	ok, _ := ctx.Value(wtAuthKey{}).(bool)
	return ok
}
