package rexapi

import (
	"context"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/tailscale/peercred"
)

// The transports a client reaches the gateway by, as Rex names them in a
// principal.
const (
	transportUnix      = "unix"
	transportTCP       = "tcp"
	transportTailscale = "tailscale"
)

type connKey struct{}

type principalKey struct{}

// trackedListener is a listener that remembers the connections it
// accepted, so that closing it closes them too, the WebSockets an HTTP
// server let go of among them.
type trackedListener struct {
	net.Listener
	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
}

func newTrackedListener(ln net.Listener) *trackedListener {
	return &trackedListener{Listener: ln, conns: map[net.Conn]struct{}{}}
}

func (l *trackedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		c.Close()
		return nil, net.ErrClosed
	}
	tc := &trackedConn{Conn: c, l: l}
	l.conns[tc] = struct{}{}
	return tc, nil
}

func (l *trackedListener) Close() error {
	err := l.Listener.Close()
	l.mu.Lock()
	l.closed = true
	conns := l.conns
	l.conns = map[net.Conn]struct{}{}
	l.mu.Unlock()
	for c := range conns {
		c.(*trackedConn).Conn.Close()
	}
	return err
}

type trackedConn struct {
	net.Conn
	l    *trackedListener
	once sync.Once
}

func (c *trackedConn) Close() error {
	c.once.Do(func() {
		c.l.mu.Lock()
		delete(c.l.conns, c)
		c.l.mu.Unlock()
	})
	return c.Conn.Close()
}

// underlying returns the connection a tracked one wraps, which peer
// credentials are read from.
func underlying(c net.Conn) net.Conn {
	if tc, ok := c.(*trackedConn); ok {
		return tc.Conn
	}
	return c
}

// serveOn serves the API on ln, whose clients come by transport, until
// ln closes.
func (g *Gateway) serveOn(ln net.Listener, transport string) error {
	srv := &http.Server{
		Handler:           g.HandlerFor(transport),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, connKey{}, c)
		},
		ErrorLog: nil,
	}
	err := srv.Serve(ln)
	if err == http.ErrServerClosed || isClosed(err) {
		return nil
	}
	return err
}

func isClosed(err error) bool {
	ne, ok := err.(*net.OpError)
	return err == net.ErrClosed || ok && ne.Err != nil && ne.Err.Error() == net.ErrClosed.Error()
}

// HandlerFor returns the gateway's endpoints for clients that come by
// transport: each request is identified as Rex identifies it, and one
// the transport's rules refuse is answered with 403.
func (g *Gateway) HandlerFor(transport string) http.Handler {
	inner := g.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		switch transport {
		case transportUnix:
			var ok bool
			if p, ok = peerPrincipal(r); !ok {
				writeRefusal(w, "this socket lets in only the user the server runs as")
				return
			}
		case transportTailscale:
			if g.tailnet == nil {
				writeRefusal(w, "built-in Tailscale is off")
				return
			}
			var err error
			if p, err = g.tailnet.identify(r.Context(), r.RemoteAddr); err != nil {
				writeRefusal(w, err.Error())
				return
			}
		default:
			inner.ServeHTTP(w, r)
			return
		}
		inner.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}

// peerPrincipal identifies a caller on the Unix socket by its
// credentials, and lets in only the user the gateway runs as. Where the
// system gives no credentials, as on Windows, the socket's own
// permissions are the boundary.
func peerPrincipal(r *http.Request) (map[string]any, bool) {
	p := map[string]any{"kind": "peer", "transport": transportUnix, "display": "local"}
	c, _ := r.Context().Value(connKey{}).(net.Conn)
	if c == nil {
		return p, true
	}
	creds, err := peercred.Get(underlying(c))
	if err != nil {
		return p, true
	}
	if uid, ok := creds.UserID(); ok {
		if uid != strconv.Itoa(os.Getuid()) {
			return nil, false
		}
		if n, err := strconv.Atoi(uid); err == nil {
			p["uid"] = n
		}
		p["display"] = "uid " + uid
	}
	if pid, ok := creds.PID(); ok {
		p["pid"] = pid
	}
	return p, true
}

// transportOf tells how the client of a request reached the gateway.
func transportOf(r *http.Request) string {
	if p, ok := r.Context().Value(principalKey{}).(map[string]any); ok {
		if t, ok := p["transport"].(string); ok {
			return t
		}
	}
	return transportTCP
}
