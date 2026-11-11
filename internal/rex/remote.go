//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/tailscale/tailcat"
	"tailscale.com/envknob"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/util/dnsname"
)

// Remote access: the server also listens on a tailcat address (WireGuard
// over DERP, without a control plane). The address is a secret: it holds
// the server's key and the WireGuard pre-shared key. Never log it, nor any
// key. Every peer that knows it may shake hands; the server reads each
// connection's peer key and serves the clients of its allowlist only.
// While a pairing window is open, another client may ask to be allowed
// (remote.pair), and nothing else; the local user approves or rejects.

// RemotePort is the port the server listens on at its tailcat address.
const RemotePort = 7464

// RexAPIPort is Rex's API port, which the server's tailcat address serves
// too: its connections go to the Rex API gateway (toGateway).
const RexAPIPort = 7297

// RemoteConfig is the server's remote access, kept in Dir()/remote.json.
type RemoteConfig struct {
	Enabled bool `json:"enabled"`
	// Tailnet turns on the system Tailscale mode: the GoRex desktops on
	// this account's tailnet find this one, and may open terminals on it.
	// Off until the user turns it on.
	Tailnet      bool                 `json:"tailnet,omitempty"`
	NodeKey      key.NodePrivate      `json:"nodeKey,omitzero"`
	PresharedKey tailcat.PresharedKey `json:"presharedKey,omitzero"`
	// Region is the DERP region picked the first time, kept so that the
	// address stays the same.
	Region int `json:"region,omitempty"`
	// Relay is a DERP relay of the user's own, as derp.example.com, in
	// place of tailcat's: the address then names it, and Region waits for
	// it to be cleared.
	Relay   string         `json:"relay,omitempty"`
	Allowed []RemoteClient `json:"allowed"`
	// ShareLayout lets remote clients read the layout and subscribe to
	// its changes; off by default.
	ShareLayout bool `json:"shareLayout,omitempty"`
	// RemoteControl lets remote clients run app actions through this
	// window; off by default.
	RemoteControl bool `json:"remoteControl,omitempty"`
}

// The pairing window: how long it stays open, how many requests it
// keeps, and the longest name of one.
const (
	pairWindow  = 5 * time.Minute
	maxPending  = 8
	maxPairName = 40
)

func remotePath() string { return filepath.Join(Dir(), "remote.json") }

// writeJSON writes v to path, readable by the user only, atomically.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	os.Remove(tmp)
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// readJSON reads path into v; a file that does not exist leaves v as is.
func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("rex: reading %s: %w", filepath.Base(path), err)
	}
	return nil
}

// NormalizePublicKey checks a client's public key, "nodekey:" and 64 hex
// digits, and returns it in its canonical form.
func NormalizePublicKey(s string) (string, error) {
	k, err := parsePublicKey(s)
	if err != nil {
		return "", err
	}
	return k.String(), nil
}

// secretRun finds what could be a piece of the tailcat address or of a
// key: a long run of base64 characters.
var secretRun = regexp.MustCompile(`[A-Za-z0-9_+/=-]{32,}`)

// tailnetIP finds a tailcat node's IPv6 address, which holds a part of
// its key: the prefix and at most the five groups that follow it.
var tailnetIP = regexp.MustCompile(`(?i)fd7a:115c:a1e0(?::(?::)?[0-9a-f]{1,4}){0,5}`)

// scrub removes from s the tailcat address, and anything that looks like
// a part of one, so that an error or a log line never holds it.
func scrub(s string, addrs ...string) string {
	for _, a := range addrs {
		if a != "" {
			s = strings.ReplaceAll(s, a, "<address>")
		}
	}
	s = tailnetIP.ReplaceAllString(s, "<redacted>")
	return secretRun.ReplaceAllString(s, "<redacted>")
}

// Scrub removes from s the tailcat addresses addrs, and anything that
// looks like a part of one or of a key, for an error to show or log.
func Scrub(s string, addrs ...string) string { return scrub(s, addrs...) }

// NormalizeRelay checks a relay of one's own, a DERP server's host name
// with its port when not 443, and returns it trimmed; "" is tailcat's own
// relays. An IP address is refused: derper --hostname serves a
// certificate for a name, which TLS to an address would not match.
func NormalizeRelay(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	host := s
	if h, p, err := net.SplitHostPort(s); err == nil {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return "", errBadRelay
		}
		host = h
	}
	if _, err := netip.ParseAddr(host); err == nil || dnsname.ValidHostname(host) != nil {
		return "", errBadRelay
	}
	return s, nil
}

var errBadRelay = errors.New("rex: a relay is a host name, as derp.example.com, with :PORT when not 443: no IP address, no https://")

// relayInsecure lets a relay serve a certificate of no authority, for
// tests' relays on the loopback.
var relayInsecure = false

// relayRegion is the DERP region of a relay of one's own: the one node,
// on its DERP port (443 unless given) and STUN's 3478, as derper serves.
func relayRegion(relay string) *tailcfg.DERPRegion {
	host, port := relay, 0
	if h, p, err := net.SplitHostPort(relay); err == nil {
		host = h
		port, _ = strconv.Atoi(p)
	}
	return &tailcfg.DERPRegion{
		RegionID: 900, RegionCode: "own",
		Nodes: []*tailcfg.DERPNode{{Name: "900a", RegionID: 900, HostName: host, DERPPort: port, InsecureForTests: relayInsecure}},
	}
}

// ValidRemoteAddress tells whether s reads as a tailcat address.
func ValidRemoteAddress(s string) bool {
	_, err := tailcat.ParseAddr(tailcat.Addr(strings.TrimSpace(s)))
	return strings.TrimSpace(s) != "" && err == nil
}

// Fingerprint is a short form of a public key, the first 8 and last 4 of
// its hex digits, that both sides of a pairing show, to tell that the
// request is the one made; "" for what is not a key.
func Fingerprint(publicKey string) string {
	k, err := parsePublicKey(publicKey)
	if err != nil {
		return ""
	}
	_, h, _ := strings.Cut(k.String(), ":")
	return h[:8] + "-" + h[len(h)-4:]
}

// pairName cleans the name a client asks to pair with: printable
// characters only, at most maxPairName of them.
func pairName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxPairName {
		s = strings.TrimSpace(string(r[:maxPairName]))
	}
	return s
}

func parsePublicKey(s string) (key.NodePublic, error) {
	var k key.NodePublic
	if err := k.UnmarshalText([]byte(strings.TrimSpace(s))); err != nil || k.IsZero() {
		return k, errors.New(`rex: a client's public key reads "nodekey:" and 64 hex digits`)
	}
	return k, nil
}

// remote is the server's remote listener and its state.
type remote struct {
	mu      sync.Mutex
	cfg     RemoteConfig
	tailnet *systemTailnet
	gen     int                // changes on every stop, to drop starts in flight
	cancel  context.CancelFunc // non-nil while starting or running
	ts      *tailcat.Server    // non-nil while running
	ln      net.Listener
	api     net.Listener // RexAPIPort, beside ln
	addr    tailcat.Addr
	lastErr string
	conns   map[net.Conn]string // the remote connections, by their peer's key
	// pairUntil is when the pairing window closes, zero while closed;
	// pending holds the requests made in it, never saved.
	pairUntil time.Time
	pending   []RemotePending
}

// remotePeerKey returns the key of the peer of a connection accepted by
// ts, "" when tailcat does not know it; tests replace it.
var remotePeerKey = func(ts *tailcat.Server, c net.Conn) string {
	for _, kv := range ts.PeerEnv(c.LocalAddr(), c.RemoteAddr()) {
		if v, ok := strings.CutPrefix(kv, "TAILCAT_PEER_KEY="); ok {
			return v
		}
	}
	return ""
}

// allowedLocked tells whether k is in the allowlist.
func (r *remote) allowedLocked(k string) bool {
	return k != "" && slices.ContainsFunc(r.cfg.Allowed, func(c RemoteClient) bool { return c.PublicKey == k })
}

// pairOpenLocked tells whether the pairing window is open, closing it
// once it has expired.
func (r *remote) pairOpenLocked() bool {
	if !r.pairUntil.IsZero() && time.Now().After(r.pairUntil) {
		r.pairUntil, r.pending = time.Time{}, nil
	}
	return !r.pairUntil.IsZero()
}

// remoteListen starts ts and listens on RemotePort, in ts.Region when set
// (a relay of one's own), else in region, picking one when region is 0;
// tests replace it to stay off the network.
var remoteListen = func(ctx context.Context, ts *tailcat.Server, region int) (net.Listener, tailcat.Addr, int, error) {
	switch {
	case ts.Region != nil:
	case region == 0:
		ci := tailcat.ConnInfo{RegionID: -1}
		if err := ci.Expand(ctx, tailcat.ExpandForServer); err != nil {
			return nil, "", 0, err
		}
		ts.Region = ci.Region[0]
		region = int(ts.Region.RegionID)
	default:
		ts.RegionID = tailcfg.DERPRegionID(region)
	}
	ln, err := ts.Listen(ctx, "tcp", ":"+strconv.Itoa(RemotePort))
	if err != nil {
		return nil, "", 0, err
	}
	return ln, ts.TailcatAddr(), region, nil
}

// remoteAPIListen listens on RexAPIPort of ts, which remoteListen
// started; tests replace it.
var remoteAPIListen = func(ts *tailcat.Server) (net.Listener, error) {
	return ts.Listen(context.Background(), "tcp", ":"+strconv.Itoa(RexAPIPort))
}

// remoteControlEnabled tells whether remote clients may run app actions.
func (s *Server) remoteControlEnabled() bool {
	s.rem.mu.Lock()
	defer s.rem.mu.Unlock()
	return s.rem.cfg.RemoteControl
}

// remoteClientFor is what a connected peer is, for its Hello: its label,
// key and times.
func (s *Server) remoteClientFor(peer string) *RemoteClient {
	r := &s.rem
	r.mu.Lock()
	defer r.mu.Unlock()
	if IsTailnetTarget(peer) && r.tailnet != nil {
		return r.tailnet.clientFor(peer)
	}
	for _, c := range r.cfg.Allowed {
		if c.PublicKey == peer {
			c := c
			return &c
		}
	}
	return nil
}

// remoteSeen notes that an allowed client connected now.
func (s *Server) remoteSeen(peer string) {
	r := &s.rem
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.cfg.Allowed {
		if r.cfg.Allowed[i].PublicKey == peer {
			r.cfg.Allowed[i].LastSeen = time.Now()
			return // saved with the next configuration change
		}
	}
}

// remoteShareLayout tells whether remote clients may read and subscribe
// to the layout.
func (s *Server) remoteShareLayout() bool {
	s.rem.mu.Lock()
	defer s.rem.mu.Unlock()
	return s.rem.cfg.ShareLayout
}

// remoteAllowed tells the ops a remote connection may make; it may also
// attach to sessions.
func remoteAllowed(op string) bool {
	switch op {
	case "hello", "ping", "list", "create", "kill", "resize", "focus", "input", "paste", "capture", "clients",
		"key", "mouse", "focusReport", "modes", "format", "inputMode", "clear", "reset", "command":
		return true
	}
	return false
}

// remoteLoad reads the configuration and starts listening when enabled.
func (s *Server) remoteLoad() {
	r := &s.rem
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg = RemoteConfig{}
	if err := readJSON(remotePath(), &r.cfg); err != nil {
		log.Printf("rex: remote access stays off: %v", err)
		r.cfg, r.lastErr = RemoteConfig{}, err.Error()
		return
	}
	// Allowlists saved before identities had times get them now.
	now, changed := time.Now(), false
	for i := range r.cfg.Allowed {
		if r.cfg.Allowed[i].Created.IsZero() {
			r.cfg.Allowed[i].Created = now
			changed = true
		}
	}
	if changed {
		if err := writeJSON(remotePath(), r.cfg); err != nil {
			log.Printf("rex: saving the remote allowlist: %v", err)
		}
	}
	if r.cfg.Enabled {
		s.remoteStartLocked()
	}
	if r.cfg.Tailnet {
		s.tailnetStartLocked()
	}
}

// remoteEnabled tells whether a remote client may connect: the tailcat
// listener runs, or one of the system tailnet's links is open. The
// tailnet broker alone does not count, so that a server with no sessions
// and no app still exits when idle.
func (s *Server) remoteEnabled() bool {
	s.rem.mu.Lock()
	defer s.rem.mu.Unlock()
	if s.rem.cancel != nil {
		return true
	}
	if t := s.rem.tailnet; t != nil {
		return t.linked()
	}
	return false
}

// remoteClose stops listening and drops the remote connections.
func (s *Server) remoteClose() {
	s.rem.mu.Lock()
	s.tailnetStopLocked()
	done := s.remoteStopLocked()
	s.rem.mu.Unlock()
	done()
}

// remoteStopLocked stops the listener; the function it returns closes it
// and its connections, to call without the lock.
func (s *Server) remoteStopLocked() func() {
	r := &s.rem
	r.gen++
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	ts, ln, api, conns := r.ts, r.ln, r.api, r.conns
	r.ts, r.ln, r.api, r.addr, r.conns = nil, nil, nil, "", nil
	r.pairUntil, r.pending = time.Time{}, nil
	return func() {
		if ln != nil {
			ln.Close()
			api.Close()
		}
		for c := range conns {
			c.Close()
		}
		if ts != nil {
			ts.Close()
		}
	}
}

// remoteStartLocked starts listening in the background, retrying while
// it fails.
func (s *Server) remoteStartLocked() {
	tuneKeepalive()
	r := &s.rem
	ctx, cancel := context.WithCancel(context.Background())
	r.gen++
	gen, cfg := r.gen, r.cfg
	r.cancel, r.lastErr = cancel, ""
	go func() {
		for {
			// No AllowedClients: every peer that knows the address may
			// shake hands, and remoteAccept checks its key, so that one not
			// allowed yet may ask to pair.
			ts := &tailcat.Server{
				Key: cfg.NodeKey, PresharedKey: cfg.PresharedKey,
				Logf: func(string, ...any) {}, // it logs keys
			}
			if cfg.Relay != "" {
				ts.Region = relayRegion(cfg.Relay)
			}
			ln, addr, region, err := remoteListen(ctx, ts, cfg.Region)
			var api net.Listener
			if err == nil {
				if api, err = remoteAPIListen(ts); err != nil {
					ln.Close()
					ln = nil
				}
			}
			r.mu.Lock()
			if r.gen != gen {
				r.mu.Unlock()
				if ln != nil {
					ln.Close()
					api.Close()
				}
				ts.Close()
				return
			}
			if err == nil {
				r.ts, r.ln, r.api, r.addr, r.lastErr = ts, ln, api, addr, ""
				r.conns = map[net.Conn]string{}
				if r.cfg.Region == 0 && region != 0 {
					r.cfg.Region = region
					if err := writeJSON(remotePath(), r.cfg); err != nil {
						log.Printf("rex: saving the remote region: %v", err)
					}
				}
				r.mu.Unlock()
				log.Printf("rex: remote access on")
				// Either listener failing starts both again.
				errs := make(chan error, 2)
				go func() { errs <- s.remoteAccept(ts, api) }()
				go func() { errs <- s.remoteAccept(ts, ln) }()
				aerr := <-errs
				// Accepting failed, not stopped: say so, and start again as
				// after any failure, so that nothing claims to listen
				// while nothing accepts.
				r.mu.Lock()
				if r.gen != gen {
					r.mu.Unlock()
					return
				}
				msg := scrub(aerr.Error())
				r.lastErr = msg
				done := func() {}
				if r.ln == ln {
					ts, ln, conns := r.ts, r.ln, r.conns
					r.ts, r.ln, r.api, r.addr, r.conns = nil, nil, nil, "", nil
					done = func() {
						ln.Close()
						api.Close()
						for c := range conns {
							c.Close()
						}
						ts.Close()
					}
				}
				r.mu.Unlock()
				done()
				log.Printf("rex: remote access stopped accepting: %s", msg)
				select {
				case <-ctx.Done():
					return
				case <-time.After(remoteRetry):
				}
				continue
			}
			r.lastErr = scrub(err.Error())
			r.mu.Unlock()
			ts.Close()
			log.Printf("rex: starting remote access: %s", scrub(err.Error()))
			select {
			case <-ctx.Done():
				return
			case <-time.After(remoteRetry):
			}
		}
	}()
}

// The TCP keepalive of the remote connections: probes after
// netstackIdle of silence, every netstackInterval, nine of them unanswered
// before the connection drops, in about two minutes. Netstack's defaults,
// two hours and 75 seconds, kept the connections of a client whose
// network dropped, and the sessions they attached, for over two hours.
// It works whatever the client, an older one included, whose own netstack
// answers the probes while it runs, and resets the connections it forgot.
const (
	netstackIdle     = "30s"
	netstackInterval = "10s"
)

var tuneOnce sync.Once

// tuneKeepalive sets the TCP keepalive of the connections tailcat
// accepts, unless the environment set it; out of the environment the
// sessions get.
func tuneKeepalive() {
	tuneOnce.Do(func() {
		for k, v := range map[string]string{
			"TS_NETSTACK_KEEPALIVE_IDLE":     netstackIdle,
			"TS_NETSTACK_KEEPALIVE_INTERVAL": netstackInterval,
		} {
			if _, set := os.LookupEnv(k); !set {
				envknob.Setenv(k, v)
				os.Unsetenv(k)
			}
		}
	})
}

// remoteRetry is how long a failed listener waits before it starts again.
var remoteRetry = 30 * time.Second

var errRemoteStopped = errors.New("rex: remote access stopped")

// remoteAccept serves the remote connections of ln, RemotePort's or
// RexAPIPort's, until accepting fails: it returns that error, or
// errRemoteStopped when the listener was replaced. A connection of an
// allowed client is served marked remote, or goes to the gateway on
// RexAPIPort; one of another client may only ask to pair, on RemotePort
// while the window is open; any other is closed, unlogged.
func (s *Server) remoteAccept(ts *tailcat.Server, ln net.Listener) error {
	r := &s.rem
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		r.mu.Lock()
		stopped := r.ln != ln && r.api != ln
		r.mu.Unlock()
		if stopped {
			c.Close()
			return errRemoteStopped
		}
		go func() {
			k := remotePeerKey(ts, c)
			// Checked and kept under the lock, so that a revoke drops it.
			r.mu.Lock()
			allowed := r.allowedLocked(k)
			api := r.api == ln
			// Stopped, or for the gateway and not allowed: no answer.
			if r.ln != ln && !api || api && !allowed {
				r.mu.Unlock()
				c.Close()
				return
			}
			if k == "" || !allowed && !r.pairOpenLocked() {
				r.mu.Unlock()
				refuseRemote(c)
				return
			}
			r.conns[c] = k
			r.mu.Unlock()
			switch {
			case api:
				s.remoteSeen(k)
				s.toGateway(c, k)
			case allowed:
				s.remoteSeen(k)
				s.handle(c, k)
			default:
				s.pairConn(c, k)
			}
			c.Close()
			r.mu.Lock()
			delete(r.conns, c)
			r.mu.Unlock()
		}()
	}
}

// toGateway hands c, a connection of the allowed client k to RexAPIPort,
// to the Rex API gateway on its socket: a line that names the client, then
// the bytes both ways. With no gateway there, c closes, and its client
// dials again.
func (s *Server) toGateway(c net.Conn, k string) {
	g, err := net.DialTimeout("unix", GatewaySocketPath(), 5*time.Second)
	if err != nil {
		return
	}
	peer := RemoteClient{PublicKey: k}
	if rc := s.remoteClientFor(k); rc != nil {
		peer.Name = rc.Name
	}
	b, _ := json.Marshal(peer)
	if _, err := g.Write(append(b, '\n')); err != nil {
		g.Close()
		return
	}
	splice(g, g, c)
}

// refuseRemote tells a client it does not allow so, unprompted, and closes
// the connection once the client has read it: the client tells a refusal
// from a connection lost on the way.
func refuseRemote(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	b, _ := json.Marshal(Response{Error: refusedMessage})
	if _, err := c.Write(append(b, '\n')); err != nil {
		return
	}
	// Closing with what the client sent unread could reset the connection
	// before the answer reaches it: half close, and read until it closes.
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	}
	io.Copy(io.Discard, io.LimitReader(c, 64<<10))
}

// pairConn serves the connection of a client not allowed: one
// remote.pair, and nothing else.
func (s *Server) pairConn(c net.Conn, k string) {
	c.SetDeadline(time.Now().Add(30 * time.Second))
	line, err := readLine(bufio.NewReaderSize(c, 4096))
	if err != nil {
		refuseLine(c, err)
		return
	}
	var req Request
	res := Response{}
	switch {
	case json.Unmarshal(line, &req) != nil:
		res.Error = "rex: not a request"
	case req.Op != "remote.pair":
		res.ID, res.Error = req.ID, fmt.Sprintf("%s is not allowed before pairing", req.Op)
		// A client whose request waits is told so: one that hears not, made
		// none, or was rejected.
		r := &s.rem
		r.mu.Lock()
		if r.pairOpenLocked() && slices.ContainsFunc(r.pending, func(p RemotePending) bool { return p.PublicKey == k }) {
			res.Error += ": " + pendingMessage
		}
		r.mu.Unlock()
	default:
		res.ID = req.ID
		name := ""
		if req.Remote != nil {
			name = req.Remote.Name
		}
		if fp, err := s.remotePair(k, name); err != nil {
			res.Error = err.Error()
		} else {
			res.Data, _ = json.Marshal(PairResult{Fingerprint: fp})
		}
	}
	b, _ := json.Marshal(res)
	c.Write(append(b, '\n'))
}

// remotePair records the request of the client of key k to pair, while
// the window is open, and returns the key's fingerprint.
func (s *Server) remotePair(k, name string) (string, error) {
	name = pairName(name)
	if name == "" {
		return "", errors.New("rex: name this machine to pair it")
	}
	r := &s.rem
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.pairOpenLocked() {
		return "", errors.New("rex: the server is not pairing")
	}
	if !slices.ContainsFunc(r.pending, func(p RemotePending) bool { return p.PublicKey == k }) {
		if len(r.pending) >= maxPending {
			return "", errors.New("rex: too many pairing requests; try again later")
		}
		r.pending = append(r.pending, RemotePending{Name: name, PublicKey: k, RequestedAt: time.Now()})
		log.Print("rex: a pairing request")
	}
	return Fingerprint(k), nil
}

func (s *Server) remoteStatusLocked(tailnet *TailnetStatus) RemoteStatus {
	r := &s.rem
	st := RemoteStatus{
		Enabled: r.cfg.Enabled, Running: r.ts != nil, Address: string(r.addr), Relay: r.cfg.Relay,
		Allowed: slices.Clone(r.cfg.Allowed), LastError: r.lastErr,
		ShareLayout: r.cfg.ShareLayout, RemoteControl: r.cfg.RemoteControl,
	}
	if r.pairOpenLocked() {
		st.Pairing = RemotePairing{Open: true, ExpiresAt: r.pairUntil}
		st.Pending = slices.Clone(r.pending)
	}
	st.Tailnet = tailnet
	return st
}

// remoteDo serves the remote.* ops, of local connections only.
func (s *Server) remoteDo(req Request) (any, error) {
	var done []func()
	defer func() {
		for _, f := range done {
			f()
		}
	}()
	r := &s.rem
	r.mu.Lock()
	defer r.mu.Unlock()
	reqKey := func() (string, error) {
		if req.Remote == nil {
			return "", fmt.Errorf("rex: %s needs a client", req.Op)
		}
		k, err := parsePublicKey(req.Remote.PublicKey)
		return k.String(), err
	}
	pendingIndex := func(k string) int {
		if !r.pairOpenLocked() {
			return -1
		}
		return slices.IndexFunc(r.pending, func(p RemotePending) bool { return p.PublicKey == k })
	}
	switch req.Op {
	case "remote.status":
		if r.tailnet != nil {
			status := r.tailnet.status()
			return s.remoteStatusLocked(&status), nil
		}
		return s.remoteStatusLocked(&TailnetStatus{Enabled: r.cfg.Tailnet}), nil
	case "remote.tailnetSet":
		cfg := r.cfg
		cfg.Tailnet = req.On
		if err := writeJSON(remotePath(), cfg); err != nil {
			return nil, err
		}
		r.cfg = cfg
		if cfg.Tailnet {
			s.tailnetStartLocked()
		} else {
			s.tailnetStopLocked()
		}
		return nil, nil
	case "remote.pairStart":
		if !r.cfg.Enabled {
			return nil, errors.New("rex: turn remote access on to pair")
		}
		r.pairOpenLocked()
		r.pairUntil = time.Now().Add(pairWindow)
		log.Printf("rex: %s", req.Op)
		return nil, nil
	case "remote.pairStop":
		r.pairUntil, r.pending = time.Time{}, nil
		log.Printf("rex: %s", req.Op)
		return nil, nil
	case "remote.reject":
		k, err := reqKey()
		if err != nil {
			return nil, err
		}
		i := pendingIndex(k)
		if i < 0 {
			return nil, errors.New("rex: no such request")
		}
		r.pending = slices.Delete(r.pending, i, i+1)
		log.Printf("rex: %s", req.Op)
		return nil, nil
	}
	cfg := r.cfg
	cfg.Allowed = slices.Clone(cfg.Allowed)
	var revoked string
	approved := -1
	switch req.Op {
	case "remote.enable":
		if cfg.NodeKey.IsZero() || cfg.PresharedKey.IsZero() {
			cfg.NodeKey, cfg.PresharedKey = key.NewNode(), tailcat.NewPresharedKey()
		}
		cfg.Enabled = true
	case "remote.disable":
		cfg.Enabled = false
	case "remote.allow":
		if req.Remote == nil {
			return nil, errors.New("rex: remote.allow needs a client")
		}
		name := strings.TrimSpace(req.Remote.Name)
		k, err := parsePublicKey(req.Remote.PublicKey)
		if err != nil {
			return nil, err
		}
		if name == "" {
			return nil, errors.New("rex: name the client")
		}
		if slices.ContainsFunc(cfg.Allowed, func(c RemoteClient) bool { return c.PublicKey == k.String() }) {
			return nil, errors.New("rex: that client is allowed already")
		}
		cfg.Allowed = append(cfg.Allowed, RemoteClient{Name: name, PublicKey: k.String(), Created: time.Now()})
	case "remote.approve":
		k, err := reqKey()
		if err != nil {
			return nil, err
		}
		i := pendingIndex(k)
		if i < 0 {
			return nil, errors.New("rex: no such request")
		}
		if !r.allowedLocked(k) {
			cfg.Allowed = append(cfg.Allowed, RemoteClient{Name: r.pending[i].Name, PublicKey: k, Created: time.Now()})
		}
		approved = i
	case "remote.revoke":
		k, err := reqKey()
		if err != nil {
			return nil, err
		}
		i := slices.IndexFunc(cfg.Allowed, func(c RemoteClient) bool { return c.PublicKey == k })
		if i < 0 {
			return nil, errors.New("rex: no such client")
		}
		cfg.Allowed = slices.Delete(cfg.Allowed, i, i+1)
		revoked = k
	case "remote.regenerate":
		cfg.NodeKey, cfg.PresharedKey = key.NewNode(), tailcat.NewPresharedKey()
	case "remote.relaySet":
		relay, err := NormalizeRelay(req.Relay)
		if err != nil {
			return nil, err
		}
		cfg.Relay = relay
	case "remote.shareLayoutSet":
		cfg.ShareLayout = req.On
	case "remote.controlSet":
		cfg.RemoteControl = req.On
	default:
		return nil, fmt.Errorf("unknown op %q", req.Op)
	}
	if err := writeJSON(remotePath(), cfg); err != nil {
		return nil, err
	}
	relayWas := r.cfg.Relay
	r.cfg = cfg
	if approved >= 0 {
		r.pending = slices.Delete(r.pending, approved, approved+1)
	}
	switch {
	case !cfg.Enabled:
		done = append(done, s.remoteStopLocked())
		r.lastErr = ""
	case revoked != "":
		// Drop its connections; accepting checks the allowlist again.
		for c, k := range r.conns {
			if k == revoked {
				delete(r.conns, c)
				done = append(done, func() { c.Close() })
			}
		}
	case req.Op == "remote.regenerate", req.Op == "remote.relaySet" && relayWas != cfg.Relay,
		req.Op == "remote.enable" && r.cancel == nil:
		// New keys, a new relay, or off until now: start again, which
		// makes the address anew and drops the connections.
		done = append(done, s.remoteStopLocked())
		s.remoteStartLocked()
	}
	log.Printf("rex: %s", req.Op)
	return nil, nil
}

// remoteClientKey returns this machine's identity as a remote client,
// made the first time, kept in Dir()/remote-client.json.
func remoteClientKey() (key.NodePrivate, error) {
	// Two creators end with the same key: one at a time in this process,
	// and the file is linked in place, which fails when another process
	// made it first; then that one is read.
	clientKeyMu.Lock()
	defer clientKeyMu.Unlock()
	type identity struct {
		NodeKey key.NodePrivate `json:"nodeKey,omitzero"`
	}
	var id identity
	path := filepath.Join(Dir(), "remote-client.json")
	if err := readJSON(path, &id); err != nil {
		return key.NodePrivate{}, err
	}
	if !id.NodeKey.IsZero() {
		return id.NodeKey, nil
	}
	id.NodeKey = key.NewNode()
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := writeJSON(tmp, id); err != nil {
		return key.NodePrivate{}, err
	}
	defer os.Remove(tmp)
	if err := os.Link(tmp, path); err != nil {
		if _, serr := os.Stat(path); serr != nil {
			// No hard links here: write the file as it is.
			return id.NodeKey, writeJSON(path, id)
		}
		var other identity
		if err := readJSON(path, &other); err != nil || other.NodeKey.IsZero() {
			return key.NodePrivate{}, errors.New("rex: reading the remote client key")
		}
		return other.NodeKey, nil
	}
	return id.NodeKey, nil
}

var clientKeyMu sync.Mutex

// ClientPublicKey returns this machine's public key as a remote client,
// for the allowlist of a server; "" when it cannot be read nor made.
func ClientPublicKey() string {
	k, err := remoteClientKey()
	if err != nil {
		log.Printf("rex: the remote client key: %v", err)
		return ""
	}
	return k.Public().String()
}

// MachineID names this machine for good, whichever way an app reaches
// it: a hash of its key as a remote client (ClientPublicKey), which the
// machine keeps from the first time and no one renames. The hash tells
// nothing of the key. "" when the key cannot be read nor made.
func MachineID() string {
	k := ClientPublicKey()
	if k == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("gorex machine\x00" + k))
	return hex.EncodeToString(sum[:16])
}

// ConnectRemote connects to the server at a tailcat address, as the
// client of ClientPublicKey, through the local server: one tailcat client
// per machine, as two of one key take each other's packets (#212). It
// starts the local server when none runs, as Connect does; it dials itself
// only when that fails, or the server does not relay yet.
func ConnectRemote(ctx context.Context, addr string) (*Client, error) {
	addr = strings.TrimSpace(addr)
	if IsTailnetTarget(addr) {
		return connectTailnet(ctx, addr)
	}
	a := tailcat.Addr(addr)
	if a == "" {
		return nil, errors.New("rex: no remote address")
	}
	if _, err := tailcat.ParseAddr(a); err != nil {
		// Not the parser's error: it could quote the address.
		return nil, errors.New("rex: not a tailcat address")
	}
	conn, err := dialRemote(ctx, addr)
	if err != nil {
		return nil, err
	}
	c := newClient(conn, func() (net.Conn, error) {
		ctx, cancel := context.WithTimeout(context.Background(), tailcatRedial)
		defer cancel()
		return dialRemote(ctx, addr)
	})
	c.keepalive = true
	return c, nil
}

// tailcatRelayed is what the local server answers remote.tailcatDial once
// it connected; then the connection is the remote server's.
const tailcatRelayed = "gorex-tailcat/1 ok\n"

// tailcatRedial bounds a stream's dial again: longer than tailcatDial,
// so that the local server answers first.
const tailcatRedial = 20 * time.Second

// dialRemote connects to RemotePort at addr through the local server, or
// itself when there is none or it does not know remote.tailcatDial.
func dialRemote(ctx context.Context, addr string) (net.Conn, error) {
	c, err := relayDial(ctx, addr)
	if errors.Is(err, errNoRelay) {
		c, err = dialTailcat(ctx, addr)
	}
	if err != nil {
		return nil, fmt.Errorf("rex: connecting to the remote server: %s", scrub(err.Error(), addr))
	}
	return c, nil
}

var errNoRelay = errors.New("no relay")

// spawnServer starts the local server; tests replace it.
var spawnServer = Spawn

// relayDial asks the local server to connect to addr, starting it when
// none runs. It is errNoRelay when that server cannot be reached or does
// not relay; another error is the relay's, which dialing again here would
// not mend.
func relayDial(ctx context.Context, addr string) (net.Conn, error) {
	c, err := dialSocket()
	if err != nil {
		if spawnServer() != nil {
			return nil, errNoRelay
		}
		if c, err = dialUntil(time.Now().Add(5 * time.Second)); err != nil {
			return nil, errNoRelay
		}
	}
	stop := context.AfterFunc(ctx, func() { c.Close() })
	b, _ := json.Marshal(Request{Op: "remote.tailcatDial", TailcatAddress: addr})
	var line []byte
	if _, err = c.Write(append(b, '\n')); err == nil {
		// A byte at a time: what follows the line is the remote server's.
		line, err = readByteLine(c)
	}
	if !stop() {
		err = ctx.Err()
	}
	if err == nil && string(line) == tailcatRelayed {
		return c, nil
	}
	c.Close()
	if err != nil {
		return nil, err
	}
	var resp Response
	if json.Unmarshal(line, &resp) != nil || resp.Error == "" {
		return nil, errors.New("the local server answered something else")
	}
	if strings.HasPrefix(resp.Error, "unknown op") {
		return nil, errNoRelay
	}
	return nil, errors.New(resp.Error)
}

func readByteLine(c net.Conn) ([]byte, error) {
	var line []byte
	b := make([]byte, 1)
	for len(line) < maxLine {
		if _, err := io.ReadFull(c, b); err != nil {
			return nil, err
		}
		line = append(line, b[0])
		if b[0] == '\n' {
			return line, nil
		}
	}
	return nil, errLineTooLong
}

// relayTailcat is reached through the local socket, after a local client
// names a tailcat address: the connection goes on to its server, through
// the tailcat client of this process.
func (s *Server) relayTailcat(local net.Conn, input *bufio.Reader, addr string) {
	// A remote session through it keeps the server, as a control
	// connection does. An upgrade drops it: its client dials again.
	s.mu.Lock()
	s.controls++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.controls--
		s.idleFrom = time.Now()
		s.mu.Unlock()
	}()
	defer local.Close()
	ctx, cancel := context.WithTimeout(context.Background(), tailcatDial)
	defer cancel()
	// The client says nothing before the answer: what it reads is its
	// close, then the dial is no longer wanted.
	asked := make(chan struct{})
	go func() {
		if _, err := input.Peek(1); err != nil {
			cancel()
		}
		close(asked)
	}()
	var c net.Conn
	var err error
	if _, perr := tailcat.ParseAddr(tailcat.Addr(addr)); perr != nil {
		err = errors.New("not a tailcat address")
	} else {
		c, err = dialTailcat(ctx, addr)
	}
	if err != nil {
		b, _ := json.Marshal(Response{Error: scrub(err.Error(), addr)})
		local.Write(append(b, '\n'))
		return
	}
	defer c.Close()
	if _, err := io.WriteString(local, tailcatRelayed); err != nil {
		return
	}
	splice(local, waitReader{asked, input}, c)
}

// waitReader reads r once wait is closed.
type waitReader struct {
	wait <-chan struct{}
	r    io.Reader
}

func (w waitReader) Read(b []byte) (int, error) {
	<-w.wait
	return w.r.Read(b)
}

// The tailcat clients of this process, one per address: a client's
// connections share it, and it closes once it had none for tailcatIdle.
var (
	tailcatMu      sync.Mutex
	tailcatClients = map[string]*pooledTailcat{}
	tailcatIdle    = 3 * time.Minute
)

// tailcatDial bounds a dial; tailcatTry an attempt of a client that
// connected before, which fails when its server restarted.
var (
	tailcatDial = 15 * time.Second
	tailcatTry  = 3 * time.Second
)

type pooledTailcat struct {
	tc    *tailcat.Client
	conns int  // open, or being dialed
	used  bool // it connected once
	idle  *time.Timer
}

// dialTailcat connects to RemotePort at addr with the tailcat client of
// addr. A client that connected before and fails now may have a server
// that restarted and forgot it: it registers again (Ping) and tries once
// more; then, when no other connection uses it, another client replaces
// it. A client other connections use is never closed here: one slow dial
// says little of them, and a restarted server's close soon. A dial its
// caller gave up (canceled) closes no client.
func dialTailcat(ctx context.Context, addr string) (net.Conn, error) {
	k, err := remoteClientKey()
	if err != nil {
		return nil, err
	}
	tailcatMu.Lock()
	p := tailcatClients[addr]
	if p == nil {
		p = newPooledTailcat(addr, k)
	}
	if p.idle != nil {
		p.idle.Stop()
		p.idle = nil
	}
	p.conns++
	used := p.used
	tailcatMu.Unlock()

	var c net.Conn
	if used {
		c, err = p.try(ctx, tailcatTry)
		if err != nil && ctx.Err() == nil {
			pctx, cancel := context.WithTimeout(ctx, tailcatTry)
			_, err = p.tc.Ping(pctx)
			cancel()
			if err == nil {
				c, err = p.try(ctx, tailcatTry)
			}
		}
		if err != nil && ctx.Err() == nil && p.drop(addr) {
			old := p
			tailcatMu.Lock()
			if p = tailcatClients[addr]; p == nil {
				p = newPooledTailcat(addr, k)
			}
			p.conns++
			tailcatMu.Unlock()
			old.tc.Close()
			old.release(addr)
			c, err = p.tc.DialTCPPort(ctx, RemotePort)
		}
	} else {
		c, err = p.tc.DialTCPPort(ctx, RemotePort)
	}
	if err != nil {
		if !errors.Is(ctx.Err(), context.Canceled) && p.drop(addr) {
			p.tc.Close()
		}
		p.release(addr)
		return nil, err
	}
	tailcatMu.Lock()
	p.used = true
	tailcatMu.Unlock()
	return &tailcatConn{Conn: c, p: p, addr: addr}, nil
}

// drop takes p out of the pool when the dial under way is its only
// connection, and reports whether it did.
func (p *pooledTailcat) drop(addr string) bool {
	tailcatMu.Lock()
	defer tailcatMu.Unlock()
	if p.conns != 1 || tailcatClients[addr] != p {
		return false
	}
	delete(tailcatClients, addr)
	return true
}

// newPooledTailcat makes the client of addr. tailcatMu must be held.
func newPooledTailcat(addr string, k key.NodePrivate) *pooledTailcat {
	p := &pooledTailcat{tc: &tailcat.Client{Server: tailcat.Addr(addr), Key: k, Logf: func(string, ...any) {}}}
	tailcatClients[addr] = p
	return p
}

// try dials within d.
func (p *pooledTailcat) try(ctx context.Context, d time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return p.tc.DialTCPPort(ctx, RemotePort)
}

// release counts a connection of p closed, and closes p after a while
// without one.
func (p *pooledTailcat) release(addr string) {
	tailcatMu.Lock()
	defer tailcatMu.Unlock()
	p.conns--
	if p.conns > 0 || tailcatClients[addr] != p {
		return
	}
	var t *time.Timer
	t = time.AfterFunc(tailcatIdle, func() {
		tailcatMu.Lock()
		idle := p.idle == t && p.conns == 0 && tailcatClients[addr] == p
		if idle {
			delete(tailcatClients, addr)
		}
		tailcatMu.Unlock()
		if idle {
			p.tc.Close()
		}
	})
	p.idle = t
}

type tailcatConn struct {
	net.Conn
	p    *pooledTailcat
	addr string
	once sync.Once
}

func (c *tailcatConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.p.release(c.addr) })
	return err
}
