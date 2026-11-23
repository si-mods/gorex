//go:build js && wasm

package main

// HTTPS and WebSockets to a GoRex gateway over the node's network stack
// (docs/WEB-002-tailscale.md, Engine): TCP to a peer's tailnet address on
// port 443, TLS in Go with the LetsEncrypt roots of bakedroots (js/wasm
// has no system roots), then HTTP/1.1. The host is always a peer's
// MagicDNS name, sent as Host and as the TLS server name, and the page's
// own origin goes as Origin, so the gateway checks it as a browser's.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall/js"
	"time"

	"gorex/web/jsbridge"
	"tailscale.com/net/bakedroots"
)

// dialTLS opens TLS to a peer's port 443 by its MagicDNS name.
func (n *node) dialTLS(ctx context.Context, host string) (*tls.Conn, error) {
	ip, ok := n.peerAddr(host)
	if !ok {
		return nil, fmt.Errorf("%s is not a device of this tailnet", host)
	}
	raw, err := n.dialer.UserDial(ctx, "tcp", net.JoinHostPort(ip.String(), "443"))
	if err != nil {
		return nil, err
	}
	tc := tls.Client(raw, &tls.Config{
		ServerName: strings.TrimSuffix(host, "."),
		RootCAs:    bakedroots.Get(),
		NextProtos: []string{"http/1.1"},
		MinVersion: tls.VersionTLS12,
	})
	if err := tc.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return tc, nil
}

// client is the node's HTTP transport: its own dialer, so net/http never
// sends a request through the browser's fetch (net/http/roundtrip_js.go).
func (n *node) client() *http.Transport {
	n.transportOnce.Do(func() {
		n.transport = &http.Transport{
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				return n.dialTLS(ctx, host)
			},
			DialContext: func(context.Context, string, string) (net.Conn, error) {
				return nil, errors.New("the tailnet engine speaks only https")
			},
			ForceAttemptHTTP2:     false,
			TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       60 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
		}
	})
	return n.transport
}

// jsRequest is request(host, method, path, headers, body, origin, timeoutMs):
// a promise of the answer as the page reads it (jsbridge.Do). The timeout
// covers the answer's headers only.
func (n *node) jsRequest(args []js.Value) any {
	if len(args) != 7 {
		return jsbridge.Promise(func() (any, error) {
			return nil, errors.New("usage: request(host, method, path, headers, body, origin, timeoutMs)")
		})
	}
	host, method, path := strings.ToLower(args[0].String()), args[1].String(), args[2].String()
	headers := map[string]string{}
	if h := args[3]; h.Type() == js.TypeString && h.String() != "" {
		json.Unmarshal([]byte(h.String()), &headers)
	}
	var body []byte
	if b := args[4]; b.Truthy() {
		body = make([]byte, b.Get("byteLength").Int())
		js.CopyBytesToGo(body, b)
	}
	origin := args[5].String()
	timeout := time.Duration(args[6].Int()) * time.Millisecond
	return jsbridge.Promise(func() (any, error) {
		if !strings.HasPrefix(path, "/") {
			return nil, errors.New("the path starts with /")
		}
		if origin != "" {
			headers["Origin"] = origin
		}
		return jsbridge.Do(n.client(), method, "https://"+host+path, headers, body, timeout)
	})
}

// jsSocket is socket(host, path, protocol, origin, emit): a WebSocket to a
// gateway over TLS (jsbridge.Socket).
func (n *node) jsSocket(args []js.Value) any {
	if len(args) != 5 {
		panic("usage: socket(host, path, protocol, origin, emit)")
	}
	host, path, protocol, origin, emit := strings.ToLower(args[0].String()), args[1].String(), args[2].String(), args[3].String(), args[4]
	return jsbridge.Socket(func(ctx context.Context) (net.Conn, error) {
		return n.dialTLS(ctx, host)
	}, host, path, origin, protocol, emit)
}
