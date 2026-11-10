package rexapi

import (
	"context"
	"net"
	"net/http"
	"time"

	"gorex/internal/rex"
)

// tailcatListener takes the connections remote clients make to Rex's port
// on the session server's tailcat address: the server lets in its
// allowlist and its network only, and hands each over the local socket,
// its client named first (rex.AcceptTailcat). The gateway keeps no
// tailcat of its own, and keeps one accept waiting while it serves.
type tailcatListener struct {
	accept func(context.Context) (net.Conn, rex.RemoteClient, error)
	ctx    context.Context
	cancel context.CancelFunc
}

func newTailcatListener() *tailcatListener {
	ctx, cancel := context.WithCancel(context.Background())
	return &tailcatListener{accept: rex.AcceptTailcat, ctx: ctx, cancel: cancel}
}

// Accept waits for a connection, asking again a second after the server
// failed to answer, as while it restarts.
func (l *tailcatListener) Accept() (net.Conn, error) {
	for {
		c, peer, err := l.accept(l.ctx)
		if l.ctx.Err() != nil {
			if c != nil {
				c.Close()
			}
			return nil, net.ErrClosed
		}
		if err == nil {
			return &tailcatConn{Conn: c, peer: peer}, nil
		}
		select {
		case <-l.ctx.Done():
		case <-time.After(retryEvery):
		}
	}
}

func (l *tailcatListener) Close() error {
	l.cancel()
	return nil
}

func (l *tailcatListener) Addr() net.Addr { return &net.UnixAddr{Net: "unix", Name: "tailcat"} }

// tailcatConn is a connection of the client peer, over tailcat.
type tailcatConn struct {
	net.Conn
	peer rex.RemoteClient
}

// tailcatPrincipal identifies a caller over tailcat: the device the
// session server named on the local socket, which is this user's only.
// Its key stays out: Rex's principal has no member for it, and clients
// that decode one strictly would refuse it.
func tailcatPrincipal(r *http.Request) (map[string]any, bool) {
	c, _ := r.Context().Value(connKey{}).(net.Conn)
	tc, ok := underlying(c).(*tailcatConn)
	if !ok {
		return nil, false
	}
	return map[string]any{
		"kind": "device", "transport": transportTailcat, "display": tc.peer.Name, "path": []string{"tailcat"},
	}, true
}
