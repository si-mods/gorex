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
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"gorex/internal/pairaddr"
	"gorex/internal/tailcatpool"
	"gorex/web/jsbridge"

	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

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

// node is the page's tailcat device: its key, a shared tailcatpool.Pool so
// restarted hosts retire stale clients automatically, and one HTTP transport
// per host address. Two clients of one key take each other's packets, so one
// page holds one key (web/src/tailcat.ts's tab lock), and one address keeps
// one pooled client.
type node struct {
	priv key.NodePrivate
	pool *tailcatpool.Pool

	mu         sync.Mutex
	transports map[string]*http.Transport
}

func newNode(config js.Value) (*node, error) {
	n := &node{transports: map[string]*http.Transport{}}
	if k := config.Get("key"); k.Type() == js.TypeString && k.String() != "" {
		if err := n.priv.UnmarshalText([]byte(k.String())); err != nil || n.priv.IsZero() {
			return nil, errors.New("invalid tailcat key")
		}
	} else {
		n.priv = key.NewNode()
	}
	n.pool = &tailcatpool.Pool{
		LogPrefix: "tailcat",
		KeyFn:     func() (key.NodePrivate, bool) { return n.priv, !n.priv.IsZero() },
	}
	return n, nil
}

func checkAddr(addr string) error {
	_, err := tailcat.ParseAddr(tailcat.Addr(addr))
	return err
}

// dial connects to port at addr through the pooled tailcat client, retiring a
// warm client when a restarted server forgot it.
func (n *node) dial(ctx context.Context, addr string, port uint16) (net.Conn, error) {
	if err := checkAddr(addr); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, dialWait)
	defer cancel()
	return n.pool.Dial(ctx, addr, port, n.priv)
}

// close drops the HTTP transport and pooled tailcat client of an address, for
// Disconnect and host removal.
func (n *node) close(addr string) {
	n.mu.Lock()
	rt := n.transports[addr]
	delete(n.transports, addr)
	n.mu.Unlock()
	if rt != nil {
		rt.CloseIdleConnections()
	}
	if e := n.pool.Get(addr); e != nil {
		n.pool.Evict(addr, e)
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
		conn, err := n.dial(context.Background(), addr, pairaddr.RemotePort)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		return pairaddr.PairTailcat(conn, name)
	})
}
