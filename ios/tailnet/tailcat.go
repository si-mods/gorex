package tailnet

// Rex Go as a remote client of GoRex machines, over their tailcat address,
// as a GoRex desktop is (docs/IOS-002-tailcat.md). The proxy (proxy.go) dials
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

	"gorex/internal/tailcatpool"
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
	// tcPool holds one tailcat client per machine, shared with internal/rex.
	tcPool = &tailcatpool.Pool{
		LogPrefix: "tailcat",
		KeyFn: func() (key.NodePrivate, bool) {
			tcMu.Lock()
			defer tcMu.Unlock()
			return tcKey, !tcKey.IsZero()
		},
	}
	// tcScopes ends the dials to each address: SetTailcatHosts cancels
	// the scope of an address no host has any more, SetTailcatKey all.
	tcScopes = map[string]tcScope{}
)

// tcScope is the context the dials to an address run in.
type tcScope struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// scopeLocked is the scope of the dials to addr. tcMu must be held.
func scopeLocked(addr string) context.Context {
	s, ok := tcScopes[addr]
	if !ok {
		s.ctx, s.cancel = context.WithCancel(context.Background())
		tcScopes[addr] = s
	}
	return s.ctx
}

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
	for _, s := range tcScopes {
		s.cancel() // dials of the old key
	}
	tcScopes = map[string]tcScope{}
	tcMu.Unlock()
	tcPool.CloseAll()
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
	tcHosts = m
	// The clients of addresses no host has go.
	listed := map[string]bool{}
	for _, a := range m {
		listed[a] = true
	}
	for a, s := range tcScopes {
		if !listed[a] {
			delete(tcScopes, a)
			s.cancel()
		}
	}
	tcMu.Unlock()
	tcPool.KeepOnly(listed)
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
// connects to DERP and shakes hands.
var tailcatDial = 15 * time.Second

// dialTailcat connects to port on the tailcat host id, within
// tailcatDial. A cancel of ctx ends the dial; ctx's deadline does not.
// The proxy's SOCKS server (tailscale.com/net/socks5, handleTCP) gives
// each dial a context of its own, context.Background with five seconds,
// shorter than a client's first dial, which connects to DERP and shakes
// hands. That context is never cancelled when the app gives a connection
// up: the server does not read the app's side while it dials. So a dial
// through the proxy runs on to tailcatDial, unless its host leaves the
// list or the key changes (SetTailcatHosts, SetTailcatKey), which end it.
func dialTailcat(ctx context.Context, id string, port uint16) (net.Conn, error) {
	tcMu.Lock()
	addr, ok := tcHosts[id]
	var scope context.Context
	if ok {
		scope = scopeLocked(addr)
	}
	tcMu.Unlock()
	if !ok {
		return nil, errors.New("not a tailcat host: " + id)
	}
	dctx, cancel := context.WithTimeout(scope, tailcatDial)
	defer cancel()
	stop := context.AfterFunc(ctx, func() {
		if errors.Is(ctx.Err(), context.Canceled) {
			cancel()
		}
	})
	defer stop()
	return dialAddr(dctx, addr, port)
}

// dialAddr connects to port at addr with the pooled client of addr.
func dialAddr(ctx context.Context, addr string, port uint16) (net.Conn, error) {
	tcMu.Lock()
	k := tcKey
	tcMu.Unlock()
	if k.IsZero() {
		return nil, errors.New("no tailcat key")
	}
	return tcPool.Dial(ctx, addr, port, k)
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
