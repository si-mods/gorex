//go:build js && wasm

package main

// HTTPS and WebSockets to a GoRex gateway over the node's network stack
// (docs/web-tailscale.md, Engine): TCP to a peer's tailnet address on
// port 443, TLS in Go with the LetsEncrypt roots of bakedroots (js/wasm
// has no system roots), then HTTP/1.1. The host is always a peer's
// MagicDNS name, sent as Host and as the TLS server name, and the page's
// own origin goes as Origin, so the gateway checks it as a browser's.

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"gorex/web/tailnet/wsclient"
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
// a promise of {status, statusText, headers, body}, headers as JSON pairs
// and body a reader of read() (a promise of a Uint8Array, or null at the
// end) and cancel(). The timeout covers the answer's headers only.
func (n *node) jsRequest(args []js.Value) any {
	if len(args) != 7 {
		return promise(func() (any, error) {
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
	return promise(func() (any, error) {
		if !strings.HasPrefix(path, "/") {
			return nil, errors.New("the path starts with /")
		}
		ctx, cancel := context.WithCancel(context.Background())
		var timer *time.Timer
		if timeout > 0 {
			timer = time.AfterFunc(timeout, cancel)
		}
		req, err := http.NewRequestWithContext(ctx, method, "https://"+host+path, bytes.NewReader(body))
		if err != nil {
			cancel()
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := n.client().RoundTrip(req)
		if timer != nil && !timer.Stop() && err == nil {
			// The timeout came as the headers did.
			res.Body.Close()
			err = context.DeadlineExceeded
		}
		if err != nil {
			cancel()
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, errors.New("timed out")
			}
			return nil, err
		}
		var pairs [][2]string
		for k, vs := range res.Header {
			for _, v := range vs {
				pairs = append(pairs, [2]string{strings.ToLower(k), v})
			}
		}
		hj, _ := json.Marshal(pairs)
		return map[string]any{
			"status":     res.StatusCode,
			"statusText": strings.TrimSpace(strings.TrimPrefix(res.Status, strconv.Itoa(res.StatusCode))),
			"headers":    string(hj),
			"body":       bodyReader(res.Body, cancel),
		}, nil
	})
}

// bodyReader hands a response's body to the page in chunks.
func bodyReader(body io.ReadCloser, cancel func()) map[string]any {
	var mu sync.Mutex
	done := false
	finish := func() {
		if !done {
			done = true
			body.Close()
			cancel()
		}
	}
	return map[string]any{
		"read": js.FuncOf(func(this js.Value, args []js.Value) any {
			return promise(func() (any, error) {
				mu.Lock()
				defer mu.Unlock()
				if done {
					return nil, nil
				}
				buf := make([]byte, 64<<10)
				k, err := body.Read(buf)
				if k > 0 {
					a := js.Global().Get("Uint8Array").New(k)
					js.CopyBytesToJS(a, buf[:k])
					return a, nil
				}
				if err == io.EOF {
					finish()
					return nil, nil
				}
				if err != nil {
					finish()
					return nil, err
				}
				return js.Global().Get("Uint8Array").New(0), nil
			})
		}),
		"cancel": js.FuncOf(func(this js.Value, args []js.Value) any {
			go func() {
				mu.Lock()
				defer mu.Unlock()
				finish()
			}()
			return nil
		}),
	}
}

// jsSocket is socket(host, path, protocol, origin, emit): a WebSocket to a
// gateway. It returns {send(data), close(code, reason)} at once, and emits
// ("open", protocol), ("message", string or Uint8Array) and, once, ("close",
// code, reason) as a browser's WebSocket does: 1006 when it never opened
// or broke, with the gateway's refusal as the reason.
func (n *node) jsSocket(args []js.Value) any {
	if len(args) != 5 {
		panic("usage: socket(host, path, protocol, origin, emit)")
	}
	host, path, protocol, origin, emit := strings.ToLower(args[0].String()), args[1].String(), args[2].String(), args[3].String(), args[4]
	s := &socket{wake: make(chan struct{}, 1)}
	var closeOnce sync.Once
	closed := func(code int, reason string) {
		closeOnce.Do(func() { emit.Invoke("close", code, reason) })
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		tc, err := n.dialTLS(ctx, host)
		cancel()
		if err != nil {
			closed(1006, err.Error())
			return
		}
		ws, err := wsclient.Handshake(tc, host, path, origin, protocol)
		if err != nil {
			tc.Close()
			closed(1006, err.Error())
			return
		}
		if !s.opened(ws) {
			// Closed by the page while it opened.
			ws.Close(1000, "")
			ws.Abort()
			closed(1000, "")
			return
		}
		emit.Invoke("open", ws.Protocol)
		go s.writer()
		for {
			kind, data, err := ws.Read()
			if err != nil {
				var ce *wsclient.CloseError
				if errors.As(err, &ce) {
					closed(ce.Code, ce.Reason)
				} else {
					closed(1006, err.Error())
				}
				s.end()
				return
			}
			if kind == wsclient.Text {
				emit.Invoke("message", string(data))
			} else {
				a := js.Global().Get("Uint8Array").New(len(data))
				js.CopyBytesToJS(a, data)
				emit.Invoke("message", a)
			}
		}
	}()
	return map[string]any{
		"send": js.FuncOf(func(this js.Value, args []js.Value) any {
			v := args[0]
			if v.Type() == js.TypeString {
				s.queue(wsclient.Text, []byte(v.String()))
			} else {
				b := make([]byte, v.Get("byteLength").Int())
				js.CopyBytesToGo(b, v)
				s.queue(wsclient.Binary, b)
			}
			return nil
		}),
		"close": js.FuncOf(func(this js.Value, args []js.Value) any {
			code, reason := 1000, ""
			if len(args) > 0 && args[0].Type() == js.TypeNumber {
				code = args[0].Int()
			}
			if len(args) > 1 && args[1].Type() == js.TypeString {
				reason = args[1].String()
			}
			s.close(code, reason)
			return nil
		}),
	}
}

// socket orders a WebSocket's writes, which the page makes without
// waiting, on one goroutine.
type socket struct {
	mu      sync.Mutex
	ws      *wsclient.Conn
	pending []frame
	closing *frame
	ended   bool
	wake    chan struct{}
}

type frame struct {
	kind int
	data []byte
	code int
}

func (s *socket) opened(ws *wsclient.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing != nil {
		return false
	}
	s.ws = ws
	return true
}

func (s *socket) queue(kind int, b []byte) {
	s.mu.Lock()
	if s.closing == nil && !s.ended {
		s.pending = append(s.pending, frame{kind: kind, data: b})
	}
	s.mu.Unlock()
	s.signal()
}

func (s *socket) close(code int, reason string) {
	s.mu.Lock()
	if s.closing == nil {
		s.closing = &frame{code: code, data: []byte(reason)}
	}
	s.mu.Unlock()
	s.signal()
}

func (s *socket) end() {
	s.mu.Lock()
	s.ended = true
	s.mu.Unlock()
	s.signal()
}

func (s *socket) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *socket) writer() {
	for range s.wake {
		s.mu.Lock()
		ws, out, closing, ended := s.ws, s.pending, s.closing, s.ended
		s.pending = nil
		s.mu.Unlock()
		if ended {
			return
		}
		for _, f := range out {
			if err := ws.Write(f.kind, f.data); err != nil {
				ws.Abort()
				return
			}
		}
		if closing != nil {
			ws.Close(closing.code, string(closing.data))
			// The reader ends when the gateway answers; no answer, no wait.
			time.AfterFunc(3*time.Second, func() { ws.Abort() })
			return
		}
	}
}
