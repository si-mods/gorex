//go:build js && wasm

package main

// HTTP and WebSockets to a host's gateway over its pooled tailcat client
// (docs/WEB-006-tailcat.md): plain HTTP/1.1 inside WireGuard — no TLS and no
// bakedroots — as Rex Go's gateway stream is plain. The engine sends no
// Origin: the connection's device key is the trust, as it is for a phone.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall/js"
	"time"

	"gorex/internal/pairaddr"
	"gorex/web/jsbridge"
)

// gateway is the scheme and name requests to a host's gateway take: the
// dialer decides the host, so the name only keeps one connection pool to
// one address.
const gateway = "http://gorex.tailcat.invalid:7297"

// transport returns the cached HTTP transport of a host's address: its own
// dialer, so net/http never sends a request through the browser's fetch
// (net/http/roundtrip_js.go), and one connection pool per address.
func (n *node) transport(addr string) (*http.Transport, error) {
	if err := checkAddr(addr); err != nil {
		return nil, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if rt := n.transports[addr]; rt != nil {
		return rt, nil
	}
	rt := &http.Transport{
		DialContext: func(ctx context.Context, network, a string) (net.Conn, error) {
			return n.dial(ctx, addr, pairaddr.RexAPIPort)
		},
		ForceAttemptHTTP2:     false,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}
	n.transports[addr] = rt
	return rt, nil
}

// jsRequest is request(address, method, path, headers, body, timeoutMs):
// a promise of the answer as the page reads it (jsbridge.Do).
func (n *node) jsRequest(args []js.Value) any {
	if len(args) != 6 {
		return jsbridge.Promise(func() (any, error) {
			return nil, errors.New("usage: request(address, method, path, headers, body, timeoutMs)")
		})
	}
	addr, method, path := args[0].String(), args[1].String(), args[2].String()
	headers := map[string]string{}
	if h := args[3]; h.Type() == js.TypeString && h.String() != "" {
		json.Unmarshal([]byte(h.String()), &headers)
	}
	var body []byte
	if b := args[4]; b.Truthy() {
		body = make([]byte, b.Get("byteLength").Int())
		js.CopyBytesToGo(body, b)
	}
	timeout := time.Duration(args[5].Int()) * time.Millisecond
	return jsbridge.Promise(func() (any, error) {
		if !strings.HasPrefix(path, "/") {
			return nil, errors.New("the path starts with /")
		}
		rt, err := n.transport(addr)
		if err != nil {
			return nil, err
		}
		return jsbridge.Do(rt, method, gateway+path, headers, body, timeout)
	})
}

// jsSocket is socket(address, path, protocol, emit): a WebSocket to the
// gateway, with wsclient over the tailcat connection (jsbridge.Socket).
func (n *node) jsSocket(args []js.Value) any {
	if len(args) != 4 {
		panic("usage: socket(address, path, protocol, emit)")
	}
	addr, path, protocol, emit := args[0].String(), args[1].String(), args[2].String(), args[3]
	if err := checkAddr(addr); err != nil {
		return jsbridge.Socket(func(context.Context) (net.Conn, error) {
			return nil, err
		}, "tailcat", path, "", protocol, emit)
	}
	return jsbridge.Socket(func(ctx context.Context) (net.Conn, error) {
		return n.dial(ctx, addr, pairaddr.RexAPIPort)
	}, "tailcat", path, "", protocol, emit)
}
