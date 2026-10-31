package rexapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// expectClose reads until the connection ends, and checks the close
// status. A status of -1 means the server closed without a close frame.
func expectClose(t *testing.T, ws *websocket.Conn, want websocket.StatusCode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := ws.Read(ctx); err != nil {
			if got := websocket.CloseStatus(err); got != want {
				t.Errorf("close status %v, want %v (%v)", got, want, err)
			}
			return
		}
	}
}

func TestControlSubprotocol(t *testing.T) {
	f := startFixture(t, nil)
	ws := f.dial("/api/control", "rex.control.v0")
	wsWrite(t, ws, websocket.MessageText, []byte(`{"type":"hello","api_revision":7}`))
	expectClose(t, ws, 1002)
}

func TestHelloNeedsFiveSeconds(t *testing.T) {
	f := startFixture(t, nil)
	f.g.helloWait = 80 * time.Millisecond
	ws := f.dial("/api/control", controlProtocol)
	expectClose(t, ws, -1)
}

func TestHelloSize(t *testing.T) {
	f := startFixture(t, nil)
	ws := f.dial("/api/control", controlProtocol)
	hello := `{"type":"hello","api_revision":7,"client":{"name":"` + strings.Repeat("x", maxHello) + `"}}`
	wsWrite(t, ws, websocket.MessageText, []byte(hello))
	expectClose(t, ws, 1002)
}

func TestHelloBinary(t *testing.T) {
	f := startFixture(t, nil)
	ws := f.dial("/api/control", controlProtocol)
	wsWrite(t, ws, websocket.MessageBinary, []byte(`{"type":"hello"}`))
	expectClose(t, ws, 1003)
}

func TestHelloInvalidUTF8(t *testing.T) {
	f := startFixture(t, nil)
	ws := f.dial("/api/control", controlProtocol)
	wsWrite(t, ws, websocket.MessageText, []byte{0xff, 0xfe})
	expectClose(t, ws, 1007)
}

func TestNoCommonRevision(t *testing.T) {
	f := startFixture(t, nil)
	ws := f.dial("/api/control", controlProtocol)
	wsWrite(t, ws, websocket.MessageText, []byte(`{"type":"hello","api_revision":9,"api_min_revision":8}`))
	expectClose(t, ws, 1008)
}

func TestRequestErrors(t *testing.T) {
	f := startFixture(t, nil)
	f.setLayout(standardLayout())
	t.Run("id", func(t *testing.T) {
		ws := openControl(t, f)
		wsWrite(t, ws, websocket.MessageText, []byte(`{"type":"request","id":7,"method":"server.version"}`))
		expectClose(t, ws, 1002)
	})
	t.Run("method", func(t *testing.T) {
		ws := openControl(t, f)
		wsWrite(t, ws, websocket.MessageText, []byte(`{"type":"request","id":"x"}`))
		expectClose(t, ws, 1002)
	})
	t.Run("binary", func(t *testing.T) {
		ws := openControl(t, f)
		wsWrite(t, ws, websocket.MessageBinary, []byte(`{}`))
		expectClose(t, ws, 1003)
	})
	t.Run("payload", func(t *testing.T) {
		c := f.dialControl()
		c.hello(apiRevision)
		e := failure(t, c.request("p", "session.attach", "not an object"))
		if e["code"] != "bad_request" {
			t.Errorf("payload: %v", e)
		}
	})
	t.Run("unknown type is skipped", func(t *testing.T) {
		c := f.dialControl()
		c.hello(apiRevision)
		c.send(map[string]any{"type": "future", "payload": map[string]any{}})
		result(t, c.request("v", "server.version", nil))
	})
	t.Run("too large", func(t *testing.T) {
		ws := openControl(t, f)
		big := `{"type":"request","id":"big","method":"server.version","payload":{"x":"` +
			strings.Repeat("x", maxMessage) + `"}}`
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		ws.Write(ctx, websocket.MessageText, []byte(big))
		cancel()
		expectClose(t, ws, 1009)
	})
}

func TestDuplicateRequestID(t *testing.T) {
	f := startFixture(t, nil)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	f.g.hold = func(id string) {
		if id == "dup" {
			started <- struct{}{}
			<-release
		}
	}
	ws := openControl(t, f)
	wsWrite(t, ws, websocket.MessageText, []byte(`{"type":"request","id":"dup","method":"server.version"}`))
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the held request did not start")
	}
	wsWrite(t, ws, websocket.MessageText, []byte(`{"type":"request","id":"dup","method":"server.version"}`))
	expectClose(t, ws, 1002)
	close(release)
}

// TestReconnect checks that an abnormal end keeps the client for a
// reconnect, and that a close frame does not.
func TestReconnect(t *testing.T) {
	f := startFixture(t, nil)
	f.setLayout(standardLayout())

	c1 := f.dialControl()
	w1 := c1.hello(apiRevision)
	id1, token1 := welcomeID(t, w1), welcomeToken(t, w1)
	ids := sessionIDs(t, c1)
	c1.request("a", "session.attach", map[string]any{"session_id": ids["work"]})
	c1.ws.Close(websocket.StatusNormalClosure, "")
	time.Sleep(50 * time.Millisecond)

	c2 := f.dialControl()
	c2.send(map[string]any{"type": "hello", "api_revision": apiRevision, "reconnect_token": token1,
		"client": map[string]any{"kind": "cli", "name": "rex"}})
	w2 := c2.nextType("welcome")
	if c := w2["client"].(map[string]any); c["resumed"] != nil || c["id"] == id1 {
		t.Errorf("a clean close resumed: %v", w2)
	}

	c3 := f.dialControl()
	w3 := c3.hello(apiRevision)
	id3, token3 := welcomeID(t, w3), welcomeToken(t, w3)
	c3.request("a", "session.attach", map[string]any{"session_id": ids["work"]})
	c3.ws.CloseNow()
	waitFor(t, "the client to be kept", func() bool {
		f.g.mu.Lock()
		rec := f.g.clients[id3]
		f.g.mu.Unlock()
		if rec == nil {
			return false
		}
		lost, _ := rec.state()
		return lost
	})

	c4 := f.dialControl()
	c4.send(map[string]any{"type": "hello", "api_revision": apiRevision, "reconnect_token": token3})
	w4 := c4.nextType("welcome")
	cl := w4["client"].(map[string]any)
	if cl["resumed"] != true || cl["id"] != id3 {
		t.Errorf("an abnormal end did not resume: %v", w4)
	}
	// The old token stopped working, and the client kept its attachment.
	c5 := f.dialControl()
	c5.send(map[string]any{"type": "hello", "api_revision": apiRevision, "reconnect_token": token3})
	w5 := c5.nextType("welcome")
	if cl := w5["client"].(map[string]any); cl["resumed"] == true || cl["id"] == id3 {
		t.Errorf("the old token resumed twice: %v", w5)
	}
	result(t, c4.request("v", "session.view", map[string]any{"session_id": ids["work"]}))

	// Events start again at seq 1 on the new connection.
	f.setLayout(layoutWithTabName(t, "work", "renamed"))
	ev := c4.nextType("event")
	if ev["seq"] != float64(1) {
		t.Errorf("first event after a reconnect: %v", ev)
	}
}
