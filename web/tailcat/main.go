//go:build js && wasm

// The web client's tailcat engine (docs/WEB-006-tailcat.md): a tailcat
// client compiled to WebAssembly that runs in the page, one
// tailcat.Client for each host's address, and HTTP and WebSockets to the
// host's gateway over it. A browser has no UDP, so every path is relayed:
// under GOOS=js, tailscale.com's derphttp reaches DERP over WebSocket by
// itself, and the browser checks the relay's certificate chain.
//
// It sets one global, gorexTailcat(config), which makes a node and
// returns its calls (see newNode). The page (web/src/tailcat.ts) loads
// the module only when a tailcat host needs it. Desktop GoRex never
// builds or imports it.
package main

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"gorex/web/jsbridge"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// rexAPIPort is the gateway's port on the host's tailcat address
// (internal/rexapi, as Rex Go dials it).
const rexAPIPort = 7297

// remotePort is the session server's port on the tailcat address, where
// a client not allowed yet asks to pair (internal/rex RemotePort).
const remotePort = 7464

// dialWait bounds one dial of a client: the DERP connection and the
// handshake it carries.
const dialWait = 15 * time.Second

func main() {
	js.Global().Set("gorexTailcat", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) != 1 {
			panic("usage: gorexTailcat(config)")
		}
		n, err := newNode(args[0])
		if err != nil {
			panic(err.Error())
		}
		return n.api()
	}))
	// Keep the runtime alive for the calls to come.
	select {}
}

// node is the page's tailcat device: its key and a client for each host's
// address. Two clients of one key take each other's packets, so one page
// holds one key (web/src/tailcat.ts's tab lock), and one address keeps
// one client.
type node struct {
	priv key.NodePrivate

	mu      sync.Mutex
	clients map[tailcat.Addr]*tailcat.Client
}

func newNode(config js.Value) (*node, error) {
	n := &node{clients: map[tailcat.Addr]*tailcat.Client{}}
	if k := config.Get("key"); k.Type() == js.TypeString && k.String() != "" {
		if err := n.priv.UnmarshalText([]byte(k.String())); err != nil || n.priv.IsZero() {
			return nil, errors.New("invalid tailcat key")
		}
	} else {
		n.priv = key.NewNode()
	}
	return n, nil
}

// client returns the tailcat client of an address, made on first use.
// Callers dial it; a client starts lazily (tailcat's ensureStarted).
func (n *node) client(addr string) (*tailcat.Client, error) {
	a := tailcat.Addr(addr)
	if _, err := tailcat.ParseAddr(a); err != nil {
		return nil, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	c := n.clients[a]
	if c == nil {
		c = tailcat.NewClient(a)
		c.Key = n.priv
		n.clients[a] = c
	}
	return c, nil
}

// close drops the client of an address, for Disconnect and host removal.
func (n *node) close(addr string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	a := tailcat.Addr(addr)
	if c := n.clients[a]; c != nil {
		delete(n.clients, a)
		go c.Close() // a stuck bind would block the caller
	}
}

// api is the node's calls, as the page sees them:
//
//	key()                        the device public key, "nodekey:" and hex
//	privkey()                    the device private key's text, for the page to keep
//	check(address)               what is wrong with an address, or {ok: true}
//	pair(address, name): Promise one remote.pair to RemotePort; the fingerprint
//	request(...): Promise        HTTP to the gateway (conn.go)
//	socket(...): object          a WebSocket to the gateway (conn.go)
//	close(address)               drops the address's client
func (n *node) api() map[string]any {
	fn := func(f func(args []js.Value) any) js.Func {
		return js.FuncOf(func(this js.Value, args []js.Value) any { return f(args) })
	}
	return map[string]any{
		"key":     fn(func([]js.Value) any { return n.priv.Public().String() }),
		"privkey": fn(func([]js.Value) any { b, _ := n.priv.MarshalText(); return string(b) }),
		"check":   fn(n.jsCheck),
		"pair":    fn(n.jsPair),
		"request": fn(n.jsRequest),
		"socket":  fn(n.jsSocket),
		"close":   fn(func(args []js.Value) any { n.close(args[0].String()); return nil }),
	}
}

// jsCheck is check(address): whether the engine can use the address, and
// why not when it cannot. {ok: true} parses; a region that names the
// machine's own relay gets {ownRelay: true}, because a browser cannot
// pin its certificate; a parse failure gets {error}. Every answer that
// parses carries relay, the first node's DERP base URL, for the page's
// admission probe (docs/NET-004-public-relay.md).
func (n *node) jsCheck(args []js.Value) any {
	ci, err := tailcat.ParseAddr(tailcat.Addr(args[0].String()))
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	var relay string
	for _, r := range ci.Region {
		for _, nd := range r.Nodes {
			if relay == "" {
				relay = derpBase(nd.HostName, nd.DERPPort)
			}
			if strings.HasPrefix(nd.CertName, "sha256-raw:") {
				return map[string]any{"ownRelay": true, "relay": relay}
			}
		}
	}
	return map[string]any{"ok": true, "relay": relay}
}

// derpBase is a DERP node's base URL, as its HTTP endpoints share it:
// https on the node's host name, the default port unwritten, a literal
// IPv6 host in brackets. "" for a node without a host name.
func derpBase(hostName string, port int) string {
	if hostName == "" {
		return ""
	}
	if port == 0 || port == 443 {
		return "https://" + hostName
	}
	return "https://" + net.JoinHostPort(hostName, strconv.Itoa(port))
}

// jsPair is pair(address, name): a promise of the fingerprint the server
// shows for this key's pairing request, or its refusal.
func (n *node) jsPair(args []js.Value) any {
	addr, name := args[0].String(), args[1].String()
	return jsbridge.Promise(func() (any, error) {
		c, err := n.client(addr)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), dialWait)
		defer cancel()
		conn, err := c.DialTCPPort(ctx, remotePort)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		return pairOn(conn, name)
	})
}
