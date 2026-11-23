//go:build js && wasm

package main

// HTTP and WebSockets to a host's gateway over its tailcat client, and
// the one-line request that pairs (docs/WEB-006-tailcat.md): plain HTTP/1.1
// inside WireGuard — no TLS and no bakedroots — as Rex Go's gateway
// stream is plain. The engine sends no Origin: the connection's device
// key is the trust, as it is for a phone.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall/js"
	"time"

	"gorex/web/jsbridge"
)

// gateway is the scheme and name requests to a host's gateway take: the
// dialer decides the host, so the name only keeps one connection pool to
// one address.
const gateway = "http://gorex.tailcat.invalid:7297"

// client is the HTTP transport of a host's address: its own dialer, so
// net/http never sends a request through the browser's fetch
// (net/http/roundtrip_js.go).
func (n *node) transport(addr string) (*http.Transport, error) {
	c, err := n.client(addr)
	if err != nil {
		return nil, err
	}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, a string) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, dialWait)
			defer cancel()
			return c.DialTCPPort(ctx, rexAPIPort)
		},
		ForceAttemptHTTP2:     false,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}, nil
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
	c, err := n.client(addr)
	if err != nil {
		return jsbridge.Socket(func(context.Context) (net.Conn, error) {
			return nil, err
		}, "tailcat", path, "", protocol, emit)
	}
	return jsbridge.Socket(func(ctx context.Context) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, dialWait)
		defer cancel()
		return c.DialTCPPort(ctx, rexAPIPort)
	}, "tailcat", path, "", protocol, emit)
}

// pairOn writes the one remote.pair request the pairing window serves and
// reads its one answer (internal/rex pairConn): the fingerprint of this
// key as the server shows it, or the server's refusal.
func pairOn(conn net.Conn, name string) (any, error) {
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	req, _ := json.Marshal(map[string]any{"id": 1, "op": "remote.pair", "remote": map[string]string{"name": name}})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return nil, err
	}
	line, err := bufio.NewReaderSize(conn, 4096).ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var res struct {
		Error string          `json:"error"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(line, &res); err != nil {
		return nil, fmt.Errorf("not an answer: %w", err)
	}
	if res.Error != "" {
		return nil, errors.New(res.Error)
	}
	var data struct {
		Fingerprint string `json:"fingerprint"`
	}
	json.Unmarshal(res.Data, &data)
	return data.Fingerprint, nil
}
