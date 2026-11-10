package rexapi

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"gorex/internal/rex"
)

// tailcatListener takes the connections remote clients make to Rex's port
// on the session server's tailcat address: the server lets in only the
// clients that may command its layout, and dials the gateway's socket
// (rex.GatewaySocketPath) for each, with a line that names the client
// first. The gateway keeps no tailcat of its own. The socket is this
// user's only, as rexapi.sock.
type tailcatListener struct{ net.Listener }

// Accept takes the next connection and reads the line that names its
// client; one that sends no such line is dropped.
func (l tailcatListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		r := bufio.NewReader(c)
		line, err := r.ReadBytes('\n')
		c.SetReadDeadline(time.Time{})
		var peer rex.RemoteClient
		if err != nil || json.Unmarshal(line, &peer) != nil || peer.PublicKey == "" {
			c.Close()
			continue
		}
		return &tailcatConn{Conn: c, r: r, peer: peer}, nil
	}
}

// tailcatConn is a connection of the client peer, over tailcat.
type tailcatConn struct {
	net.Conn
	r    *bufio.Reader // what follows the line
	peer rex.RemoteClient
}

func (c *tailcatConn) Read(b []byte) (int, error) { return c.r.Read(b) }

// tailcatPrincipal identifies a caller over tailcat: the device the
// session server named on the gateway's socket, which is this user's only.
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
