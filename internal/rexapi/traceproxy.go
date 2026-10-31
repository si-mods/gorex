package rexapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/coder/websocket"
)

// proxyTarget, set by GOREX_REXAPI_PROXY to a Rex server's http URL, makes
// the gateway a recording relay instead: every request goes to that
// server, and with tracing on, every WebSocket message both ways is
// logged. It is for learning what a Rex client says to a real Rex server,
// to compare with what the gateway answers. Never set it for real use.
var proxyTarget = os.Getenv("GOREX_REXAPI_PROXY")

// relayHandler returns the relay, or nil without a target.
func relayHandler() http.Handler {
	if proxyTarget == "" {
		return nil
	}
	target, err := url.Parse(proxyTarget)
	if err != nil {
		logf("GOREX_REXAPI_PROXY: %v", err)
		return nil
	}
	plain := httputil.NewSingleHostReverseProxy(target)
	return traceRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			plain.ServeHTTP(w, r)
			return
		}
		relayWebSocket(w, r, target)
	}))
}

// relayWebSocket joins a client's WebSocket to the target's, with the
// same subprotocols, and logs each message.
func relayWebSocket(w http.ResponseWriter, r *http.Request, target *url.URL) {
	offered := offeredProtocols(r)
	u := *target
	u.Scheme = strings.Replace(u.Scheme, "http", "ws", 1)
	u.Path = r.URL.Path
	u.RawQuery = r.URL.RawQuery
	ctx := context.Background()
	up, resp, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{Subprotocols: offered, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		tracef("relay dial %s: %v (%v)", u.String(), err, resp)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	up.SetReadLimit(64 << 20)
	down, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{up.Subprotocol()}, InsecureSkipVerify: true,
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		up.CloseNow()
		return
	}
	down.SetReadLimit(64 << 20)
	name := r.URL.Path
	pump := func(from, to *websocket.Conn, dir string) {
		for {
			typ, data, err := from.Read(ctx)
			if err != nil {
				tracef("relay %s %s ended: %v (status %d)", name, dir, err, websocket.CloseStatus(err))
				status := websocket.CloseStatus(err)
				if status < 0 {
					to.CloseNow()
				} else {
					to.Close(status, "")
				}
				return
			}
			if typ == websocket.MessageText {
				tracef("relay %s %s %s", name, dir, clip(data))
			} else {
				tracef("relay %s %s binary %d bytes: %s", name, dir, len(data), fmt.Sprintf("%x", data[:min(len(data), 48)]))
			}
			if err := to.Write(ctx, typ, data); err != nil {
				tracef("relay %s %s write: %v", name, dir, err)
				from.CloseNow()
				return
			}
		}
	}
	go pump(up, down, "S->C")
	pump(down, up, "C->S")
}
