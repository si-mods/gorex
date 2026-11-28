//go:build js && wasm

// Package jsbridge carries what the web client's engines (web/tailnet,
// web/tailcat) share across the js boundary: a promise that settles on a
// goroutine, a streamed response body, an HTTP request with its answer as
// the page reads it, and a WebSocket over a connection the engine dialed.
package jsbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"gorex/web/wsclient"
)

// Promise runs f on a goroutine and settles a JS promise with its result,
// or rejects it with its error's text.
func Promise(f func() (any, error)) js.Value {
	var handler js.Func
	handler = js.FuncOf(func(this js.Value, args []js.Value) any {
		resolve, reject := args[0], args[1]
		go func() {
			defer handler.Release()
			if v, err := f(); err != nil {
				reject.Invoke(js.Global().Get("Error").New(err.Error()))
			} else {
				resolve.Invoke(v)
			}
		}()
		return nil
	})
	return js.Global().Get("Promise").New(handler)
}

// Do is an HTTP request whose answer is the object the page reads:
// {status, statusText, headers, body}, headers as JSON pairs and body a
// reader of read() (a promise of a Uint8Array, or null at the end) and
// cancel(). The timeout covers the answer's headers only.
func Do(rt *http.Transport, method, rawurl string, headers map[string]string, body []byte, timeout time.Duration) (any, error) {
	ctx, cancel := context.WithCancel(context.Background())
	var timer *time.Timer
	if timeout > 0 {
		timer = time.AfterFunc(timeout, cancel)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawurl, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := rt.RoundTrip(req)
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
		"body":       BodyReader(res.Body, cancel),
	}, nil
}

// BodyReader hands a response's body to the page in chunks.
func BodyReader(body io.ReadCloser, cancel func()) map[string]any {
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
			return Promise(func() (any, error) {
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

// Socket is a WebSocket to a gateway over a connection dial opens: it
// returns {send(data), close(code, reason)} at once, and emits ("open",
// protocol), ("message", string or Uint8Array) and, once, ("close", code,
// reason) as a browser's WebSocket does: 1006 when it never opened or
// broke, with the gateway's refusal as the reason.
func Socket(dial func(ctx context.Context) (net.Conn, error), host, path, origin, protocol string, emit js.Value) map[string]any {
	s := &socket{wake: make(chan struct{}, 1)}
	var closeOnce sync.Once
	closed := func(code int, reason string) {
		closeOnce.Do(func() { emit.Invoke("close", code, reason) })
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		c, err := dial(ctx)
		cancel()
		if err != nil {
			closed(1006, err.Error())
			return
		}
		ws, err := wsclient.Handshake(c, host, path, origin, protocol)
		if err != nil {
			c.Close()
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
