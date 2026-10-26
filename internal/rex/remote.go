//go:build darwin || linux || windows

package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

// Remote access: the server also listens on a tailcat address (WireGuard
// over DERP, without a control plane), for the clients of an allowlist
// only. The address is a secret: it holds the server's key and the
// WireGuard pre-shared key. Never log it, nor any key.

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
}

var errNoRemoteClients = errors.New("rex: allow a client before enabling remote access")

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
	conns   map[net.Conn]struct{}
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
	case "hello", "list", "create", "kill", "resize":
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
	if r.cfg.Enabled && len(r.cfg.Allowed) > 0 {
		s.remoteStartLocked()
	}
}

func (s *Server) remoteEnabled() bool {
	s.rem.mu.Lock()
	defer s.rem.mu.Unlock()
	return s.rem.cancel != nil
}

// remoteClose stops listening and drops the remote connections.
func (s *Server) remoteClose() {
	s.rem.mu.Lock()
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
	ts, ln, conns := r.ts, r.ln, r.conns
	r.ts, r.ln, r.addr, r.conns = nil, nil, "", nil
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
	r := &s.rem
	ctx, cancel := context.WithCancel(context.Background())
	r.gen++
	gen, cfg := r.gen, r.cfg
	r.cancel, r.lastErr = cancel, ""
	var keys []key.NodePublic
	for _, c := range cfg.Allowed {
		if k, err := parsePublicKey(c.PublicKey); err == nil {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		// tailcat allows every client when the allowlist is empty.
		cancel()
		r.cancel, r.lastErr = nil, errNoRemoteClients.Error()
		return
	}
	go func() {
		for {
			ts := &tailcat.Server{
				Key: cfg.NodeKey, PresharedKey: cfg.PresharedKey, AllowedClients: keys,
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
				r.ts, r.ln, r.addr, r.conns = ts, ln, addr, map[net.Conn]struct{}{}
				if r.cfg.Region == 0 {
					r.cfg.Region = region
					if err := writeJSON(remotePath(), r.cfg); err != nil {
						log.Printf("rex: saving the remote region: %v", err)
					}
				}
				r.mu.Unlock()
				log.Printf("rex: remote access on, for %d clients", len(keys))
				s.remoteAccept(ln)
				return
			}
			r.lastErr = err.Error()
			r.mu.Unlock()
			ts.Close()
			log.Printf("rex: starting remote access: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
		}
	}()
}

// remoteAccept serves the remote connections, marked remote.
func (s *Server) remoteAccept(ln net.Listener) {
	r := &s.rem
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		r.mu.Lock()
		if r.ln != ln {
			r.mu.Unlock()
			c.Close()
			return
		}
		r.conns[c] = struct{}{}
		r.mu.Unlock()
		go func() {
			s.handle(c, true)
			c.Close()
			r.mu.Lock()
			delete(r.conns, c)
			r.mu.Unlock()
		}()
	}
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
	if req.Op == "remote.status" {
		return RemoteStatus{
			Enabled: r.cfg.Enabled, Running: r.ts != nil, Address: string(r.addr),
			Allowed: slices.Clone(r.cfg.Allowed), LastError: r.lastErr,
		}, nil
	}
	cfg := r.cfg
	cfg.Allowed = slices.Clone(cfg.Allowed)
	var allowed key.NodePublic
	switch req.Op {
	case "remote.enable":
		if len(cfg.Allowed) == 0 {
			return nil, errNoRemoteClients
		}
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
		allowed = k
	case "remote.revoke":
		var k key.NodePublic
		if req.Remote != nil {
			var err error
			if k, err = parsePublicKey(req.Remote.PublicKey); err != nil {
				return nil, err
			}
		}
		i := slices.IndexFunc(cfg.Allowed, func(c RemoteClient) bool { return c.PublicKey == k.String() })
		if i < 0 {
			return nil, errors.New("rex: no such client")
		}
		cfg.Allowed = slices.Delete(cfg.Allowed, i, i+1)
		if len(cfg.Allowed) == 0 {
			cfg.Enabled = false
		}
	case "remote.regenerate":
		cfg.NodeKey, cfg.PresharedKey = key.NewNode(), tailcat.NewPresharedKey()
	default:
		return nil, fmt.Errorf("unknown op %q", req.Op)
	}
	if err := writeJSON(remotePath(), cfg); err != nil {
		return nil, err
	}
	r.cfg = cfg
	switch {
	case !cfg.Enabled:
		done = append(done, s.remoteStopLocked())
		r.lastErr = ""
	case req.Op == "remote.allow" && r.ts != nil:
		r.ts.AddAllowedClient(allowed)
	case req.Op == "remote.enable" && r.cancel != nil:
		// on already
	default:
		// A new key, or one revoked: start again, which drops its
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
	var id struct {
		NodeKey key.NodePrivate `json:"nodeKey,omitzero"`
	}
	path := filepath.Join(Dir(), "remote-client.json")
	if err := readJSON(path, &id); err != nil {
		return key.NodePrivate{}, err
	}
	if id.NodeKey.IsZero() {
		id.NodeKey = key.NewNode()
		if err := writeJSON(path, id); err != nil {
			return key.NodePrivate{}, err
		}
	}
	return id.NodeKey, nil
}

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
		return nil, fmt.Errorf("rex: not a tailcat address: %w", err)
	}
	k, err := remoteClientKey()
	if err != nil {
		return nil, err
	}
	tc := &tailcat.Client{Server: a, Key: k, Logf: func(string, ...any) {}}
	conn, err := tc.DialTCPPort(ctx, RemotePort)
	if err != nil {
		tc.Close()
		return nil, fmt.Errorf("rex: connecting to the remote server (is this machine's key allowed there?): %w", err)
	}
	c := newClient(conn, func() (net.Conn, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return tc.DialTCPPort(ctx, RemotePort)
	})
	c.closer = tc
	return c, nil
}
