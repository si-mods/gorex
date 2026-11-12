//go:build darwin || linux || windows

package rex

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tailscale/tailcat"
	"tailscale.com/derp/derpserver"
	"tailscale.com/feature"
	_ "tailscale.com/feature/condregister/useproxy" // so that its hook is set before init wraps it
	"tailscale.com/net/stunserver"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

// Reachable at: the machine as its own relay (docs/host-links.md, "2.
// Path"). The server runs a DERP relay on an address of this machine and
// a second tailcat server, of the same keys, homed on it: a tailcat node
// listens through one relay only, so each address has its own. The second
// address names that relay with its certificate pinned, and its STUN.

// The relay's connection limits: from one source, and in all.
const (
	ownRelayPerSource = 8
	ownRelayTotal     = 64
)

// ownRelayPort is the relay's port; tests take any.
var ownRelayPort = RemotePort

// NormalizeReachable checks the address of Reachable at, one IP address
// of this machine, and returns it in its canonical form; "" is off.
func NormalizeReachable(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	ip, err := netip.ParseAddr(s)
	if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
		return "", errors.New("rex: Reachable at is one IP address of this machine, as 192.0.2.10, or off")
	}
	return ip.Unmap().String(), nil
}

// ownLink is the relay of Reachable at and the tailcat server homed on it.
type ownLink struct {
	relay   *ownRelay
	ts      *tailcat.Server
	ln, api net.Listener
	addr    tailcat.Addr
	conns   map[net.Conn]string // as remote.conns
}

func (o *ownLink) close() {
	if o.ln != nil {
		o.ln.Close()
		o.api.Close()
	}
	for c := range o.conns {
		c.Close()
	}
	if o.ts != nil {
		o.ts.Close()
	}
	o.relay.close()
}

// ownStartLocked starts the relay of Reachable at and its tailcat
// server in the background when remote access is on and the setting set,
// retrying while it fails; a failure leaves the first address as it is.
func (s *Server) ownStartLocked() {
	r := &s.rem
	r.ownGen++
	r.ownErr = ""
	if !r.cfg.Enabled || r.cfg.ReachableAt == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.ownCancel = cancel
	gen, cfg := r.ownGen, r.cfg
	ownRelayHost.Store(cfg.ReachableAt)
	go func() {
		for {
			o, err := s.ownStart(ctx, cfg)
			r.mu.Lock()
			if r.ownGen != gen {
				r.mu.Unlock()
				if o != nil {
					o.close()
				}
				return
			}
			if err == nil {
				r.own, r.ownErr = o, ""
				r.mu.Unlock()
				log.Printf("rex: reachable at %s", cfg.ReachableAt)
				errs := make(chan error, 2)
				go func() { errs <- s.remoteAccept(o.ts, o.api) }()
				go func() { errs <- s.remoteAccept(o.ts, o.ln) }()
				aerr := <-errs
				r.mu.Lock()
				if r.ownGen != gen {
					r.mu.Unlock()
					return
				}
				r.own, r.ownErr = nil, scrub(aerr.Error())
				r.mu.Unlock()
				o.close()
			} else {
				r.ownErr = scrub(err.Error())
				r.mu.Unlock()
				log.Printf("rex: reachable at: %s", scrub(err.Error()))
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(remoteRetry):
			}
		}
	}()
}

// ownStopLocked stops the relay; the function it returns closes it and
// its connections, to call without the lock.
func (s *Server) ownStopLocked() func() {
	r := &s.rem
	r.ownGen++
	if r.ownCancel != nil {
		r.ownCancel()
		r.ownCancel = nil
	}
	o := r.own
	r.own = nil
	return func() {
		if o != nil {
			o.close()
		}
	}
}

// ownStart starts the relay on cfg.ReachableAt and the tailcat server of
// cfg's keys homed on it, listening on both ports.
func (s *Server) ownStart(ctx context.Context, cfg RemoteConfig) (*ownLink, error) {
	ip, err := netip.ParseAddr(cfg.ReachableAt)
	if err != nil {
		return nil, err
	}
	self := cfg.NodeKey.Public()
	relay, err := startOwnRelay(ip, func(k key.NodePublic) bool {
		r := &s.rem
		r.mu.Lock()
		defer r.mu.Unlock()
		// Pairing by address (next) admits any key here while the window
		// is open.
		return k == self || r.allowedLocked(k.String())
	})
	if err != nil {
		return nil, err
	}
	o := &ownLink{relay: relay, conns: map[net.Conn]string{}}
	// No AllowedClients: remoteAccept checks each peer, as on the first.
	o.ts = &tailcat.Server{Key: cfg.NodeKey, PresharedKey: cfg.PresharedKey, Region: relay.region,
		Logf: func(string, ...any) {}}
	if o.ln, o.addr, _, err = remoteListen(ctx, o.ts, 0); err == nil {
		if o.api, err = remoteAPIListen(o.ts); err != nil {
			o.ln.Close()
			o.ln = nil
		}
	}
	if err != nil {
		o.close()
		return nil, err
	}
	return o, nil
}

// ownRelay is a DERP relay on an address of this machine, for the keys
// admit takes only.
type ownRelay struct {
	d      *derpserver.Server
	hs     *http.Server
	ln     *limitListener
	verify *http.Server // on the loopback, for d's admission
	region *tailcfg.DERPRegion
	stun   context.CancelFunc // stops the STUN server, nil when there is none
}

func startOwnRelay(ip netip.Addr, admit func(key.NodePublic) bool) (*ownRelay, error) {
	if !localAddr(ip) {
		return nil, fmt.Errorf("rex: %s is not an address of this machine", ip)
	}
	cert, pin, err := ownRelayCert()
	if err != nil {
		return nil, err
	}
	// derpserver asks a URL whether to admit a key: one on the loopback,
	// with a path no other process knows.
	vln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	secret := make([]byte, 16)
	rand.Read(secret)
	vpath := "/" + hex.EncodeToString(secret)
	o := &ownRelay{d: derpserver.New(key.NewNode(), func(string, ...any) {})}
	o.verify = &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req tailcfg.DERPAdmitClientRequest
		if r.URL.Path != vpath || json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req) != nil {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(tailcfg.DERPAdmitClientResponse{Allow: admit(req.NodePublic)})
	})}
	go o.verify.Serve(vln)
	o.d.SetVerifyClientURL("http://" + vln.Addr().String() + vpath)
	o.d.SetVerifyClientURLFailOpen(false)

	tln, err := net.Listen("tcp", netip.AddrPortFrom(ip, uint16(ownRelayPort)).String())
	if err != nil {
		o.close()
		return nil, fmt.Errorf("rex: listening on %s: %w", netip.AddrPortFrom(ip, uint16(ownRelayPort)), err)
	}
	o.ln = &limitListener{Listener: tln, per: map[netip.Addr]int{}, conns: map[*limitConn]bool{}}
	mux := http.NewServeMux()
	h := derpserver.Handler(o.d)
	mux.Handle("/derp", h)
	mux.Handle("/derp/", h) // probe and latency-check, which netcheck asks without STUN
	o.hs = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}},
		// DERP upgrades an HTTP/1.1 connection: no HTTP/2.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
		ErrorLog:     log.New(io.Discard, "", 0), // handshakes of a wrong pin, or of strangers
	}
	go o.hs.ServeTLS(o.ln, "", "")
	port := tln.Addr().(*net.TCPAddr).Port
	node := &tailcfg.DERPNode{
		Name: "900a", RegionID: 900, HostName: ip.String(), IPv4: ip.String(), IPv6: "none",
		DERPPort: port, STUNPort: -1, CertName: "sha256-raw:" + pin,
	}
	if ip.Is6() {
		node.IPv4, node.IPv6 = "none", ip.String()
	}
	// STUN on the same port, UDP: with none, the tailcat server waits 3 s
	// for netcheck before it uses the relay. Without it, it still works.
	sctx, stop := context.WithCancel(context.Background())
	if su := stunserver.New(sctx); su.Listen(netip.AddrPortFrom(ip, uint16(port)).String()) == nil {
		go su.Serve()
		node.STUNPort, o.stun = port, stop
	} else {
		stop()
	}
	o.region = &tailcfg.DERPRegion{RegionID: 900, RegionCode: "own", Nodes: []*tailcfg.DERPNode{node}}
	return o, nil
}

// dropClients closes the relay's connections: the keys still admitted
// connect again; one revoked is asked again, and refused.
func (o *ownRelay) dropClients() { o.ln.closeAll() }

func (o *ownRelay) close() {
	if o.hs != nil {
		o.hs.Close()
		o.ln.closeAll()
	}
	if o.stun != nil {
		o.stun()
	}
	o.verify.Close()
	o.d.Close()
}

// localAddr tells whether ip is an address of an interface of this machine.
func localAddr(ip netip.Addr) bool {
	as, _ := net.InterfaceAddrs()
	for _, a := range as {
		if n, ok := a.(*net.IPNet); ok {
			if a, ok := netip.AddrFromSlice(n.IP); ok && a.Unmap() == ip {
				return true
			}
		}
	}
	return false
}

// ownRelayCert returns the relay's certificate and its pin, the hex
// SHA-256 of its DER, made the first time and kept in
// Dir()/remote-relay.json. It names no address, so that it outlives a
// change of Reachable at; the pin is what clients check.
func ownRelayCert() (tls.Certificate, string, error) {
	ownCertMu.Lock()
	defer ownCertMu.Unlock()
	var kept struct {
		Cert []byte `json:"cert"`
		Key  []byte `json:"key"`
	}
	path := filepath.Join(Dir(), "remote-relay.json")
	if err := readJSON(path, &kept); err != nil {
		return tls.Certificate{}, "", err
	}
	if len(kept.Cert) == 0 {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject:      pkix.Name{CommonName: "gorex"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().AddDate(100, 0, 0), // clients check expiry even of a pinned certificate
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		if kept.Cert, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k); err != nil {
			return tls.Certificate{}, "", err
		}
		if kept.Key, err = x509.MarshalPKCS8PrivateKey(k); err != nil {
			return tls.Certificate{}, "", err
		}
		if err := writeJSON(path, kept); err != nil {
			return tls.Certificate{}, "", err
		}
	}
	k, err := x509.ParsePKCS8PrivateKey(kept.Key)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("rex: reading %s: %w", filepath.Base(path), err)
	}
	sum := sha256.Sum256(kept.Cert)
	return tls.Certificate{Certificate: [][]byte{kept.Cert}, PrivateKey: k}, hex.EncodeToString(sum[:]), nil
}

var ownCertMu sync.Mutex

// limitListener accepts at most ownRelayPerSource connections from one
// address and ownRelayTotal in all, closing the others at once.
type limitListener struct {
	net.Listener
	mu    sync.Mutex
	per   map[netip.Addr]int
	conns map[*limitConn]bool
}

type limitConn struct {
	net.Conn
	l    *limitListener
	src  netip.Addr
	once sync.Once
}

func (l *limitListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		ap, _ := netip.ParseAddrPort(c.RemoteAddr().String())
		src := ap.Addr().Unmap()
		l.mu.Lock()
		if len(l.conns) >= ownRelayTotal || l.per[src] >= ownRelayPerSource {
			l.mu.Unlock()
			c.Close()
			continue
		}
		lc := &limitConn{Conn: c, l: l, src: src}
		l.per[src]++
		l.conns[lc] = true
		l.mu.Unlock()
		return lc, nil
	}
}

func (c *limitConn) Close() error {
	c.once.Do(func() {
		c.l.mu.Lock()
		delete(c.l.conns, c)
		if c.l.per[c.src]--; c.l.per[c.src] <= 0 {
			delete(c.l.per, c.src)
		}
		c.l.mu.Unlock()
	})
	return c.Conn.Close()
}

func (l *limitListener) closeAll() {
	l.mu.Lock()
	cs := make([]*limitConn, 0, len(l.conns))
	for c := range l.conns {
		cs = append(cs, c)
	}
	l.mu.Unlock()
	for _, c := range cs {
		c.Close()
	}
}

// ownRelayHost is the address of Reachable at, which the relay's client
// in this process dials without the environment's proxy.
var ownRelayHost atomic.Value // string

func init() {
	next, ok := feature.HookProxyFromEnvironment.GetOk()
	if !ok {
		return
	}
	// The hook is set once only: a new one in its place, set before any
	// dial, as inits run first.
	var h feature.Hook[func(*http.Request) (*url.URL, error)]
	h.Set(noProxyToOwnRelay(next))
	feature.HookProxyFromEnvironment = h
}

// noProxyToOwnRelay is next, but for a dial to this machine's own relay,
// which no proxy should carry.
func noProxyToOwnRelay(next func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	return func(r *http.Request) (*url.URL, error) {
		if h, _ := ownRelayHost.Load().(string); h != "" && r.URL.Hostname() == h {
			return nil, nil
		}
		return next(r)
	}
}
