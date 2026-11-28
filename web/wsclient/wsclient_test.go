package wsclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// dial opens a client over plain TCP to the test server, as the engine does
// over TLS on the netstack.
func dial(t *testing.T, srv *httptest.Server, origin, protocol string) *Conn {
	t.Helper()
	addr := strings.TrimPrefix(srv.URL, "http://")
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := Handshake(c, addr, "/ws", origin, protocol)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestMessagesAndCloseCode(t *testing.T) {
	gotOrigin := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrigin <- r.Header.Get("Origin")
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"rex.data.terminal.v1"}, InsecureSkipVerify: true})
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		// An echo of the client's message, a fragmented one, a ping, then 4000.
		typ, b, err := c.Read(ctx)
		if err != nil {
			return
		}
		c.Write(ctx, typ, b)
		w2, _ := c.Writer(ctx, websocket.MessageText)
		w2.Write([]byte("hello, "))
		w2.Write([]byte("world"))
		w2.Close()
		go func() { c.Ping(ctx) }()
		c.Write(ctx, websocket.MessageBinary, []byte{1, 2, 3})
		c.Close(4000, "block closed")
	}))
	defer srv.Close()

	ws := dial(t, srv, "http://127.0.0.1:5173", "rex.data.terminal.v1")
	if ws.Protocol != "rex.data.terminal.v1" {
		t.Fatalf("protocol %q", ws.Protocol)
	}
	if o := <-gotOrigin; o != "http://127.0.0.1:5173" {
		t.Fatalf("origin %q", o)
	}
	if err := ws.Write(Binary, []byte{0x01, 0xff}); err != nil {
		t.Fatal(err)
	}
	kind, b, err := ws.Read()
	if err != nil || kind != Binary || string(b) != "\x01\xff" {
		t.Fatalf("echo: %d %q %v", kind, b, err)
	}
	kind, b, err = ws.Read()
	if err != nil || kind != Text || string(b) != "hello, world" {
		t.Fatalf("fragmented: %d %q %v", kind, b, err)
	}
	kind, b, err = ws.Read()
	if err != nil || kind != Binary || string(b) != "\x01\x02\x03" {
		t.Fatalf("binary: %d %q %v", kind, b, err)
	}
	_, _, err = ws.Read()
	var ce *CloseError
	if !errors.As(err, &ce) || ce.Code != 4000 || ce.Reason != "block closed" {
		t.Fatalf("close: %v", err)
	}
}

func TestLargeMessage(t *testing.T) {
	big := strings.Repeat("x", 200_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		c.SetReadLimit(1 << 20)
		typ, b, err := c.Read(r.Context())
		if err != nil {
			return
		}
		c.Write(r.Context(), typ, b)
		c.Close(websocket.StatusNormalClosure, "")
	}))
	defer srv.Close()
	ws := dial(t, srv, "", "")
	if err := ws.Write(Text, []byte(big)); err != nil {
		t.Fatal(err)
	}
	kind, b, err := ws.Read()
	if err != nil || kind != Text || string(b) != big {
		t.Fatalf("echo of %d bytes: %d, %d bytes, %v", len(big), kind, len(b), err)
	}
}

func TestClientClose(t *testing.T) {
	code := make(chan websocket.StatusCode, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		_, _, err = c.Read(r.Context())
		code <- websocket.CloseStatus(err)
	}))
	defer srv.Close()
	ws := dial(t, srv, "", "")
	if err := ws.Close(1000, ""); err != nil {
		t.Fatal(err)
	}
	if c := <-code; c != websocket.StatusNormalClosure {
		t.Fatalf("server saw %d", c)
	}
	_, _, err := ws.Read()
	var ce *CloseError
	if !errors.As(err, &ce) || ce.Code != 1000 {
		t.Fatalf("after close: %v", err)
	}
	if err := ws.Write(Text, []byte("late")); err == nil {
		t.Fatal("a write after close went")
	}
}

func TestRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"this GoRex gateway does not trust web pages from https://x.example"}`))
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Handshake(c, addr, "/ws", "https://x.example", "rex.control.v1")
	var re *RefusedError
	if !errors.As(err, &re) || re.Status != 403 || !strings.Contains(re.Body, "does not trust") {
		t.Fatalf("refusal: %v", err)
	}
}
