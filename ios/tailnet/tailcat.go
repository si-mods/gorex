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
	// two of one key take each other's packets (internal/rex, #212).
	tcClients = map[string]*tailcat.Client{}
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
	tcClients = map[string]*tailcat.Client{}
	tcMu.Unlock()
	for _, c := range old {
		c.Close()
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
	tcHosts = m
	unused := pruneLocked()
	tcMu.Unlock()
	for _, c := range unused {
		c.Close()
	}
	return nil
}

// pruneLocked takes out the clients of addresses no host has, for the
// caller to close without the lock.
func pruneLocked() []*tailcat.Client {
	used := map[string]bool{}
	for _, a := range tcHosts {
		used[a] = true
	}
	var unused []*tailcat.Client
	for a, c := range tcClients {
		if !used[a] {
			delete(tcClients, a)
			unused = append(unused, c)
		}
	}
	return unused
}

// tailcatClient is the client of addr, made the first time.
func tailcatClient(addr string) (*tailcat.Client, error) {
	tcMu.Lock()
	defer tcMu.Unlock()
	if tcKey.IsZero() {
		return nil, errors.New("no tailcat key")
	}
	c := tcClients[addr]
	if c == nil {
		c = &tailcat.Client{Server: tailcat.Addr(addr), Key: tcKey, Logf: func(string, ...any) {}} // it logs keys
		tcClients[addr] = c
	}
	return c, nil
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
// connects to DERP and shakes hands. tailcatTry bounds a try before the
// server is met again (internal/rex has the same).
var (
	tailcatDial = 15 * time.Second
	tailcatTry  = 3 * time.Second
)

// dialTailcat connects to port on the tailcat host id.
func dialTailcat(ctx context.Context, id string, port uint16) (net.Conn, error) {
	tcMu.Lock()
	addr, ok := tcHosts[id]
	tcMu.Unlock()
	if !ok {
		return nil, errors.New("not a tailcat host: " + id)
	}
	c, err := tailcatClient(addr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tailcatDial)
	defer cancel()
	try, done := context.WithTimeout(ctx, tailcatTry)
	conn, err := c.DialTCPPort(try, port)
	done()
	if err != nil && ctx.Err() == nil {
		// A machine whose server restarted forgot this client, and its
		// port changed: meet the server again (Ping), forget the old path,
		// and try once more. Else the client waits for WireGuard's own
		// rekey, 15 seconds; internal/rex makes a new client instead.
		if _, err = c.Ping(ctx); err == nil {
			forgetPath(c, addr)
			conn, err = c.DialTCPPort(ctx, port)
		}
	}
	return conn, err
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
	c, err := tailcatClient(address)
	if err != nil {
		return "", err
	}
	// A client no host uses goes once the request is answered.
	defer func() {
		tcMu.Lock()
		unused := pruneLocked()
		tcMu.Unlock()
		for _, c := range unused {
			c.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), pairWait)
	defer cancel()
	conn, err := c.DialTCPPort(ctx, remotePort)
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
