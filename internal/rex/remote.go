//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
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

// RemoteConfig is the server's remote access, kept in Dir()/remote.json.
type RemoteConfig struct {
	Enabled      bool                 `json:"enabled"`
	NodeKey      key.NodePrivate      `json:"nodeKey,omitzero"`
	PresharedKey tailcat.PresharedKey `json:"presharedKey,omitzero"`
	// Region is the DERP region picked the first time, kept so that the
	// address stays the same.
	Region  int            `json:"region,omitempty"`
	Allowed []RemoteClient `json:"allowed"`
	// ShareSessions lets remote clients see and use every session; off,
	// each sees and uses only the sessions it created.
	ShareSessions bool `json:"shareSessions,omitempty"`
	// Network is the coordinator network this machine joined, if any;
	// its members are served beside Allowed, and kept in memory only.
	Network *NetworkConfig `json:"network,omitempty"`
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

// scrub removes from s the tailcat address, and anything that looks like
// a part of one, so that an error or a log line never holds it.
func scrub(s string, addrs ...string) string {
	for _, a := range addrs {
		if a != "" {
			s = strings.ReplaceAll(s, a, "<address>")
		}
	}
	return secretRun.ReplaceAllString(s, "<redacted>")
}

// Scrub removes from s the tailcat addresses addrs, and anything that
// looks like a part of one or of a key, for an error to show or log.
func Scrub(s string, addrs ...string) string { return scrub(s, addrs...) }

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
	gen     int                // changes on every stop, to drop starts in flight
	cancel  context.CancelFunc // non-nil while starting or running
	ts      *tailcat.Server    // non-nil while running
	ln      net.Listener
	addr    tailcat.Addr
	lastErr string
	conns   map[net.Conn]string // the remote connections, by their peer's key
	// pairUntil is when the pairing window closes, zero while closed;
	// pending holds the requests made in it, never saved.
	pairUntil time.Time
	pending   []RemotePending
	// The network, when this machine joined one: netCancel stops the
	// loop that follows it, netC is its coordinator, netMembers the
	// members beside the allowlist, netErr what the coordinator last
	// said wrong, netJoined the address last joined with.
	netCancel  context.CancelFunc
	netC       netClient
	netMembers []NetworkMember
	netErr     string
	netJoined  string
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

// remoteListen starts ts and listens on RemotePort, picking a region when
// region is 0; tests replace it to stay off the network.
var remoteListen = func(ctx context.Context, ts *tailcat.Server, region int) (net.Listener, tailcat.Addr, int, error) {
	if region == 0 {
		ci := tailcat.ConnInfo{RegionID: -1}
		if err := ci.Expand(ctx, tailcat.ExpandForServer); err != nil {
			return nil, "", 0, err
		}
		ts.Region = ci.Region[0]
		region = int(ts.Region.RegionID)
	} else {
		ts.RegionID = tailcfg.DERPRegionID(region)
	}
	ln, err := ts.Listen(ctx, "tcp", ":"+strconv.Itoa(RemotePort))
	if err != nil {
		return nil, "", 0, err
	}
	return ln, ts.TailcatAddr(), region, nil
}

// remoteAllowed tells the ops a remote connection may make; it may also
// attach to sessions.
func remoteAllowed(op string) bool {
	switch op {
	case "hello", "ping", "list", "create", "kill", "resize":
		return true
	}
	return false
}

// remoteLoad reads the configuration and starts listening when enabled.
func (s *Server) remoteLoad() {
	r := &s.rem
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := readJSON(remotePath(), &r.cfg); err != nil {
		log.Printf("rex: remote access stays off: %v", err)
		r.cfg, r.lastErr = RemoteConfig{}, err.Error()
		return
	}
	if r.cfg.Enabled {
		s.remoteStartLocked()
	}
	s.networkStartLocked()
}

func (s *Server) remoteEnabled() bool {
	s.rem.mu.Lock()
	defer s.rem.mu.Unlock()
	return s.rem.cancel != nil
}

// remoteClose stops listening and drops the remote connections.
func (s *Server) remoteClose() {
	s.rem.mu.Lock()
	netDrop := s.networkStopLocked()
	done := s.remoteStopLocked()
	s.rem.mu.Unlock()
	done()
	netDrop()
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
	ts, ln, conns := r.ts, r.ln, r.conns
	r.ts, r.ln, r.addr, r.conns = nil, nil, "", nil
	r.pairUntil, r.pending = time.Time{}, nil
	return func() {
		if ln != nil {
			ln.Close()
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
			ln, addr, region, err := remoteListen(ctx, ts, cfg.Region)
			r.mu.Lock()
			if r.gen != gen {
				r.mu.Unlock()
				if ln != nil {
					ln.Close()
				}
				ts.Close()
				return
			}
			if err == nil {
				r.ts, r.ln, r.addr, r.conns, r.lastErr = ts, ln, addr, map[net.Conn]string{}, ""
				if r.cfg.Region == 0 {
					r.cfg.Region = region
					if err := writeJSON(remotePath(), r.cfg); err != nil {
						log.Printf("rex: saving the remote region: %v", err)
					}
				}
				r.mu.Unlock()
				log.Printf("rex: remote access on")
				aerr := s.remoteAccept(ts, ln)
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
					r.ts, r.ln, r.addr, r.conns = nil, nil, "", nil
					done = func() {
						ln.Close()
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

// remoteAccept serves the remote connections until accepting fails: it
// returns that error, or errRemoteStopped when the listener was replaced.
// A connection of an allowed client is served marked remote; one of
// another client may only ask to pair, while the window is open; any
// other is closed, unlogged.
func (s *Server) remoteAccept(ts *tailcat.Server, ln net.Listener) error {
	r := &s.rem
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		r.mu.Lock()
		stopped := r.ln != ln
		r.mu.Unlock()
		if stopped {
			c.Close()
			return errRemoteStopped
		}
		go func() {
			k := remotePeerKey(ts, c)
			// Checked and kept under the lock, so that a revoke drops it.
			r.mu.Lock()
			allowed := r.allowedLocked(k) || r.netMemberLocked(k)
			if r.ln != ln {
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
			if allowed {
				s.handle(c, k)
			} else {
				s.pairConn(c, k)
			}
			c.Close()
			r.mu.Lock()
			delete(r.conns, c)
			r.mu.Unlock()
		}()
	}
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
		st := RemoteStatus{
			Enabled: r.cfg.Enabled, Running: r.ts != nil, Address: string(r.addr),
			Allowed: slices.Clone(r.cfg.Allowed), LastError: r.lastErr,
			ShareSessions: r.cfg.ShareSessions,
		}
		if r.pairOpenLocked() {
			st.Pairing = RemotePairing{Open: true, ExpiresAt: r.pairUntil}
			st.Pending = slices.Clone(r.pending)
		}
		if r.cfg.Network != nil {
			st.Network = &NetworkStatus{
				Name:        r.cfg.Network.Name,
				Coordinator: coordinatorHost(r.cfg.Network.URL),
				Members:     slices.Clone(r.netMembers),
				Revision:    r.cfg.Network.Revision,
				LastError:   r.netErr,
			}
		}
		return st, nil
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
	case "remote.join":
		if req.Network == nil {
			return nil, errors.New("rex: remote.join needs a network")
		}
		drop, err := s.networkJoin(req.Network)
		if err != nil {
			return nil, err
		}
		done = append(done, drop)
		return nil, nil
	case "remote.leave":
		drop, err := s.networkLeave()
		if err != nil {
			return nil, err
		}
		done = append(done, drop)
		return nil, nil
	case "remote.kick":
		name, nodeKey := "", ""
		if req.Remote != nil {
			name, nodeKey = networkName(req.Remote.Name), strings.TrimSpace(req.Remote.PublicKey)
			if nodeKey != "" {
				k, err := parsePublicKey(nodeKey)
				if err != nil {
					return nil, err
				}
				nodeKey = k.String()
			}
		}
		if err := s.networkKick(name, nodeKey); err != nil {
			return nil, err
		}
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
		cfg.Allowed = append(cfg.Allowed, RemoteClient{Name: name, PublicKey: k.String()})
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
			cfg.Allowed = append(cfg.Allowed, RemoteClient{Name: r.pending[i].Name, PublicKey: k})
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
	case "remote.shareSet":
		cfg.ShareSessions = req.On
		if !req.On {
			// Run once r.cfg says so: an attach checks it as it joins.
			done = append(done, s.dropUnshared)
		}
	default:
		return nil, fmt.Errorf("unknown op %q", req.Op)
	}
	if err := writeJSON(remotePath(), cfg); err != nil {
		return nil, err
	}
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
	case req.Op == "remote.regenerate", req.Op == "remote.enable" && r.cancel == nil:
		// New keys, or off until now: start again, which drops the
		// connections.
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

// ConnectRemote connects to the server at a tailcat address, as the
// client of ClientPublicKey.
func ConnectRemote(ctx context.Context, addr string) (*Client, error) {
	a := tailcat.Addr(strings.TrimSpace(addr))
	if a == "" {
		return nil, errors.New("rex: no remote address")
	}
	if _, err := tailcat.ParseAddr(a); err != nil {
		// Not the parser's error: it could quote the address.
		return nil, errors.New("rex: not a tailcat address")
	}
	k, err := remoteClientKey()
	if err != nil {
		return nil, err
	}
	tc := &tailcat.Client{Server: a, Key: k, Logf: func(string, ...any) {}}
	conn, err := tc.DialTCPPort(ctx, RemotePort)
	if err != nil {
		tc.Close()
		return nil, fmt.Errorf("rex: connecting to the remote server: %s", scrub(err.Error(), string(a)))
	}
	c := newClient(conn, func() (net.Conn, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return tc.DialTCPPort(ctx, RemotePort)
	})
	c.closer = tc
	c.keepalive = true
	return c, nil
}
