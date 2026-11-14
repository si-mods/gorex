package tailnet

// Rex Go as a remote client of GoRex machines, over their tailcat address,
// as a GoRex desktop is (docs/ios-tailcat.md). The proxy (proxy.go) dials
// a host as <id>.tailcat.invalid; the app keeps the client key in its
// Keychain and passes it in, Go never writes it.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorex/internal/tailnode"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// tailcatDomain ends the proxy's name of a tailcat host. .invalid never
// resolves, so the name can't reach anything else.
const tailcatDomain = ".tailcat.invalid"

// remotePort is the session server's port on its tailcat address, where
// it pairs (internal/rex RemotePort).
const remotePort = 7464

var (
	tcMu  sync.Mutex
	tcKey key.NodePrivate
	// tcHosts is each host's tailcat address, by its id.
	tcHosts = map[string]string{}
	// tcClients is the tailcat client of each address: one per machine, as
	// two of one key take each other's packets (internal/rex, #212). One
	// closes once it had no connection for tailcatIdle.
	tcClients   = map[string]*pooledTailcat{}
	tailcatIdle = 3 * time.Minute
)

// NewTailcatKey makes a client key, for the app to keep in its Keychain
// and pass to SetTailcatKey.
func NewTailcatKey() string {
	b, _ := key.NewNode().MarshalText()
	return string(b)
}

// SetTailcatKey sets the client key ("privkey:" and hex) the tailcat
// hosts know this phone by. A new key drops the clients of the old one.
func SetTailcatKey(privateKey string) error {
	var k key.NodePrivate
	if err := k.UnmarshalText([]byte(privateKey)); err != nil || k.IsZero() {
		return errors.New("invalid tailcat key")
	}
	tcMu.Lock()
	if k.Equal(tcKey) {
		tcMu.Unlock()
		return nil
	}
	tcKey = k
	old := tcClients
	tcClients = map[string]*pooledTailcat{}
	tcMu.Unlock()
	for _, p := range old {
		go p.tc.Close() // a stuck bind would block the caller
	}
	return nil
}

// TailcatKey is the client key's public half, "nodekey:" and hex, as a
// machine's allowlist shows it; "" before SetTailcatKey.
func TailcatKey() string {
	tcMu.Lock()
	defer tcMu.Unlock()
	if tcKey.IsZero() {
		return ""
	}
	return tcKey.Public().String()
}

type tailcatHost struct {
	ID      string `json:"id"`
	Address string `json:"address"`
}

// SetTailcatHosts sets the hosts the proxy reaches over tailcat, as a JSON
// list of {"id", "address"}: the proxy dials <id>.tailcat.invalid at that
// address. An id is a DNS label. The clients of addresses no longer listed
// close.
func SetTailcatHosts(hostsJSON string) error {
	var hosts []tailcatHost
	if err := json.Unmarshal([]byte(hostsJSON), &hosts); err != nil {
		return err
	}
	m := map[string]string{}
	for _, h := range hosts {
		if !tailnode.ValidHostname(h.ID) {
			return errors.New("invalid tailcat host id")
		}
		if _, err := tailcat.ParseAddr(tailcat.Addr(h.Address)); err != nil {
			// Not the parser's error: it could quote the address.
			return errors.New("not a tailcat address")
		}
		m[strings.ToLower(h.ID)] = h.Address
	}
	tcMu.Lock()
	defer tcMu.Unlock()
	tcHosts = m
	// The clients of addresses no host has go.
	listed := map[string]bool{}
	for _, a := range m {
		listed[a] = true
	}
	for a, p := range tcClients {
		if !listed[a] {
			delete(tcClients, a)
			go p.tc.Close()
		}
	}
	return nil
}

// listed tells whether a host has the address addr.
func listed(addr string) bool {
	tcMu.Lock()
	defer tcMu.Unlock()
	for _, a := range tcHosts {
		if a == addr {
			return true
		}
	}
	return false
}

// tailcatTarget is the host id and port of a proxy target under
// tailcatDomain.
func tailcatTarget(addr string) (id string, port uint16, ok bool) {
	host, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, false
	}
	id, ok = strings.CutSuffix(strings.ToLower(strings.TrimSuffix(host, ".")), tailcatDomain)
	pn, err := strconv.ParseUint(p, 10, 16)
	if !ok || err != nil || !tailnode.ValidHostname(id) {
		return "", 0, false
	}
	return id, uint16(pn), true
}

// tailcatDial bounds a dial of a tailcat host, longer than the five
// seconds the proxy's SOCKS server gives one: a client's first dial
// connects to DERP and shakes hands. tailcatTry bounds a try of a client
// that connected before, which fails when its server restarted
// (internal/rex has the same).
var (
	tailcatDial = 15 * time.Second
	tailcatTry  = 3 * time.Second
)

// pooledTailcat is the client of an address, as internal/rex's.
type pooledTailcat struct {
	tc    *tailcat.Client
	conns int  // open, or being dialed
	used  bool // it connected once
	idle  *time.Timer
}

// dialTailcat connects to port on the tailcat host id.
func dialTailcat(ctx context.Context, id string, port uint16) (net.Conn, error) {
	tcMu.Lock()
	addr, ok := tcHosts[id]
	tcMu.Unlock()
	if !ok {
		return nil, errors.New("not a tailcat host: " + id)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tailcatDial)
	defer cancel()
	return dialAddr(ctx, addr, port)
}

// dialAddr connects to port at addr with the client of addr, as
// internal/rex's dialTailcat: a client that connected before and fails
// now may have a server that restarted and forgot it, so it registers
// again (Ping) and tries once more; then, when no other connection uses
// it, a new client replaces it. A client other connections use is never
// closed here: one slow dial says little of them.
func dialAddr(ctx context.Context, addr string, port uint16) (net.Conn, error) {
	tcMu.Lock()
	if tcKey.IsZero() {
		tcMu.Unlock()
		return nil, errors.New("no tailcat key")
	}
	p := tcClients[addr]
	if p == nil {
		p = newPooledLocked(addr)
	}
	if p.idle != nil {
		p.idle.Stop()
		p.idle = nil
	}
	p.conns++
	used := p.used
	tcMu.Unlock()

	var c net.Conn
	var err error
	if used {
		c, err = p.try(ctx, port)
		if err != nil && ctx.Err() == nil {
			pctx, cancel := context.WithTimeout(ctx, tailcatTry)
			_, err = p.tc.Ping(pctx)
			cancel()
			if err == nil {
				c, err = p.try(ctx, port)
			}
		}
		if err != nil && ctx.Err() == nil && p.drop(addr) {
			old := p
			tcMu.Lock()
			if p = tcClients[addr]; p == nil {
				p = newPooledLocked(addr)
			}
			p.conns++
			tcMu.Unlock()
			go old.tc.Close()
			old.release(addr)
			c, err = p.tc.DialTCPPort(ctx, port)
		}
	} else {
		c, err = p.tc.DialTCPPort(ctx, port)
	}
	if err != nil {
		if p.drop(addr) {
			go p.tc.Close()
		}
		p.release(addr)
		return nil, err
	}
	tcMu.Lock()
	p.used = true
	tcMu.Unlock()
	return &tailcatConn{Conn: c, p: p, addr: addr}, nil
}

// newPooledLocked makes the client of addr. tcMu must be held, and tcKey
// set.
func newPooledLocked(addr string) *pooledTailcat {
	p := &pooledTailcat{tc: &tailcat.Client{Server: tailcat.Addr(addr), Key: tcKey, Logf: func(string, ...any) {}}} // it logs keys
	tcClients[addr] = p
	return p
}

// try dials within tailcatTry.
func (p *pooledTailcat) try(ctx context.Context, port uint16) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, tailcatTry)
	defer cancel()
	return p.tc.DialTCPPort(ctx, port)
}

// drop takes p out of the pool when the dial under way is its only
// connection, and reports whether it did.
func (p *pooledTailcat) drop(addr string) bool {
	tcMu.Lock()
	defer tcMu.Unlock()
	if p.conns != 1 || tcClients[addr] != p {
		return false
	}
	delete(tcClients, addr)
	return true
}

// release counts a connection of p closed, and closes p after tailcatIdle
// without one.
func (p *pooledTailcat) release(addr string) {
	tcMu.Lock()
	defer tcMu.Unlock()
	p.conns--
	if p.conns > 0 || tcClients[addr] != p {
		return
	}
	var t *time.Timer
	t = time.AfterFunc(tailcatIdle, func() {
		tcMu.Lock()
		idle := p.idle == t && p.conns == 0 && tcClients[addr] == p
		if idle {
			delete(tcClients, addr)
		}
		tcMu.Unlock()
		if idle {
			p.tc.Close()
		}
	})
	p.idle = t
}

// tailcatConn is a connection of a pooled client, which it releases on
// close.
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

// Forward half-close to the underlying conn so callers checking
// nettype.HalfCloser (such as tailscale.com/net/socks5) propagate EOF.
func (c *tailcatConn) CloseRead() error {
	if hr, ok := c.Conn.(interface{ CloseRead() error }); ok {
		return hr.CloseRead()
	}
	return errors.ErrUnsupported
}

func (c *tailcatConn) CloseWrite() error {
	if hw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return hw.CloseWrite()
	}
	return errors.ErrUnsupported
}

// The session server's wire (internal/rex, proto.go), as far as pairing
// needs it; internal/rex is the server, too big for the app.
type pairRequest struct {
	ID     int64      `json:"id"`
	Op     string     `json:"op"`
	Remote pairClient `json:"remote"`
}

type pairClient struct {
	Name string `json:"name"`
}

type pairResponse struct {
	Error string `json:"error"`
	Data  struct {
		Fingerprint string `json:"fingerprint"`
	} `json:"data"`
}

// refusedMessage is what a machine says to a client it does not allow,
// while it is not pairing (internal/rex).
const refusedMessage = "rex: this machine is not allowed here"

// pairWait bounds a pairing request, the connection included.
var pairWait = 30 * time.Second

// TailcatPair asks the machine at the tailcat address to allow this
// phone's key by name, as a GoRex desktop does (remote.pair), and returns
// the fingerprint the machine shows next to the request.
func TailcatPair(address, name string) (string, error) {
	if _, err := tailcat.ParseAddr(tailcat.Addr(address)); err != nil {
		return "", errors.New("not a tailcat address")
	}
	ctx, cancel := context.WithTimeout(context.Background(), pairWait)
	defer cancel()
	var conn net.Conn
	var err error
	if listed(address) {
		conn, err = dialAddr(ctx, address, remotePort)
	} else {
		// A machine not listed yet gets a client of its own, for this
		// request only.
		tcMu.Lock()
		k := tcKey
		tcMu.Unlock()
		if k.IsZero() {
			return "", errors.New("no tailcat key")
		}
		c := &tailcat.Client{Server: tailcat.Addr(address), Key: k, Logf: func(string, ...any) {}}
		defer func() { go c.Close() }()
		conn, err = c.DialTCPPort(ctx, remotePort)
	}
	if err != nil {
		return "", err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	b, _ := json.Marshal(pairRequest{ID: 1, Op: "remote.pair", Remote: pairClient{Name: name}})
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return "", err
	}
	line, err := bufio.NewReaderSize(conn, 4096).ReadSlice('\n')
	if err != nil {
		return "", err
	}
	var res pairResponse
	if err := json.Unmarshal(line, &res); err != nil {
		return "", errors.New("the machine answered something else")
	}
	switch {
	case strings.Contains(res.Error, refusedMessage):
		return "", errors.New("the machine is not pairing now: start pairing there first")
	case res.Error != "":
		return "", errors.New(res.Error)
	}
	return res.Data.Fingerprint, nil
}
