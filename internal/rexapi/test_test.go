package rexapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/coder/websocket"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// fixture is a GoRex server, a gateway over it, and the HTTP server that
// serves it.
type fixture struct {
	t    *testing.T
	g    *Gateway
	desk *rex.Client
	srv  *httptest.Server
}

// startFixture starts a GoRex server in a directory of its own, connects
// a gateway to it, and serves the gateway on an HTTP test server.
func startFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	go rex.Serve()
	for i := 0; i < 500; i++ {
		if _, err := os.Stat(rex.SocketPath()); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	desk, err := rex.ConnectSocket(rex.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	// The gateway holds the blocks' size locks through its connection, so
	// the server must keep it alive.
	c, err := rex.ConnectSocketKeepalive(rex.SocketPath())
	if err != nil {
		desk.Shutdown()
		t.Fatal(err)
	}
	g, err := New(Options{Client: c})
	if err != nil {
		c.Close()
		desk.Shutdown()
		t.Fatal(err)
	}
	srv := httptest.NewServer(g.Handler())
	f := &fixture{t: t, g: g, desk: desk, srv: srv}
	t.Cleanup(func() {
		srv.Close()
		g.Close()
		desk.Shutdown()
	})
	return f
}

// setLayout gives the GoRex server a layout.
func (f *fixture) setLayout(store layout.Store) {
	f.t.Helper()
	raw, err := json.Marshal(store)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.desk.SetLayout(raw); err != nil {
		f.t.Fatal(err)
	}
}

// dial opens a WebSocket to a path on the gateway.
func (f *fixture) dial(path string, subprotocols ...string) *websocket.Conn {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, f.srv.URL+path, &websocket.DialOptions{Subprotocols: subprotocols})
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { ws.CloseNow() })
	return ws
}

// testConn is a control connection driven as a Rex client drives one.
type testConn struct {
	t       *testing.T
	ws      *websocket.Conn
	msgs    chan map[string]any
	pending []map[string]any
	// welcome is the server's answer to the connection's hello.
	welcome map[string]any
}

// dialControl opens a control connection.
func (f *fixture) dialControl() *testConn {
	f.t.Helper()
	ws := f.dial("/api/control", controlProtocol)
	ws.SetReadLimit(maxMessage + maxMessage)
	c := &testConn{t: f.t, ws: ws, msgs: make(chan map[string]any, 256)}
	go func() {
		defer close(c.msgs)
		for {
			_, data, err := c.ws.Read(context.Background())
			if err != nil {
				return
			}
			var m map[string]any
			if err := json.Unmarshal(data, &m); err != nil {
				c.t.Errorf("not JSON: %q", data)
				return
			}
			c.msgs <- m
		}
	}()
	return c
}

// send sends one JSON message.
func (c *testConn) send(v any) {
	c.t.Helper()
	data, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, data); err != nil {
		c.t.Fatal(err)
	}
}

// next returns the next message, failing after a while.
func (c *testConn) next() map[string]any {
	c.t.Helper()
	if len(c.pending) > 0 {
		m := c.pending[0]
		c.pending = c.pending[1:]
		return m
	}
	return c.read()
}

// read returns the next message from the connection.
func (c *testConn) read() map[string]any {
	c.t.Helper()
	select {
	case m, ok := <-c.msgs:
		if !ok {
			c.t.Fatal("the connection ended")
		}
		return m
	case <-time.After(10 * time.Second):
		c.t.Fatal("timed out waiting for a message")
		return nil
	}
}

// nextType returns the next message of a type, skipping the others.
func (c *testConn) nextType(typ string) map[string]any {
	c.t.Helper()
	for {
		if m := c.next(); m["type"] == typ {
			return m
		}
	}
}

// hello sends a hello and returns the welcome.
func (c *testConn) hello(revision int) map[string]any {
	c.t.Helper()
	c.send(map[string]any{
		"type": "hello", "api_revision": revision,
		"client": map[string]any{"kind": "app", "platform": "ios", "name": "Rex.app", "version": "1.6.0"},
	})
	c.welcome = c.nextType("welcome")
	return c.welcome
}

// request sends a request and returns its response.
func (c *testConn) request(id, method string, payload any) map[string]any {
	c.t.Helper()
	req := map[string]any{"type": "request", "id": id, "method": method}
	if payload != nil {
		req["payload"] = payload
	}
	c.send(req)
	for {
		m := c.read()
		if m["type"] == "response" && m["id"] == id {
			return m
		}
		c.pending = append(c.pending, m)
	}
}

// result returns a response's payload, failing when it is an error.
func result(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	if e, ok := m["error"]; ok {
		t.Fatalf("response error: %v", e)
	}
	p, ok := m["payload"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", m)
	}
	return p
}

// failure returns a response's error object.
func failure(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	e, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error: %v", m)
	}
	return e
}

// welcomeID returns a welcome's client ID.
func welcomeID(t *testing.T, w map[string]any) string {
	t.Helper()
	c, ok := w["client"].(map[string]any)
	if !ok {
		t.Fatalf("no client in welcome: %v", w)
	}
	return c["id"].(string)
}

// welcomeToken returns a welcome's reconnect token.
func welcomeToken(t *testing.T, w map[string]any) string {
	t.Helper()
	c := w["client"].(map[string]any)
	return c["reconnect_token"].(string)
}

// standardLayout is a layout of two workspaces, the first with tabs of
// splits and the second with one.
func standardLayout() layout.Store {
	return layout.Store{Sessions: []layout.Workspace{
		{
			ID: "work", Name: "work",
			Layout: layout.Layout{Active: 0, Tabs: []layout.Tab{
				{Name: "editor", Root: split(pane("s1"), pane("s2"), false, 0.5), Focus: 0},
				{Name: "logs", Root: pane("s3"), Focus: 0},
			}},
		},
		{
			ID: "side", Name: "side",
			Layout: layout.Layout{Active: 0, Tabs: []layout.Tab{{Name: "shell", Root: pane("s9")}}},
		},
	}}
}

// layoutWithTabName returns the standard layout with a workspace's first
// tab renamed.
func layoutWithTabName(t *testing.T, workspace, name string) layout.Store {
	t.Helper()
	store := standardLayout()
	for i := range store.Sessions {
		if store.Sessions[i].ID == workspace {
			store.Sessions[i].Tabs[0].Name = name
		}
	}
	return store
}

// sessionIDs returns the sessions a session.list result names, by label.
func sessionIDs(t *testing.T, c *testConn) map[string]string {
	t.Helper()
	res := result(t, c.request("ids", "session.list", nil))
	out := map[string]string{}
	for _, raw := range res["sessions"].([]any) {
		s := raw.(map[string]any)
		out[s["label"].(string)] = s["session_id"].(string)
	}
	return out
}

// exitCommand is a command that prints a marker and exits, for a stream
// that must see the program end.
func exitCommand(marker string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe", "/c", "echo " + marker}
	}
	return []string{"/bin/sh", "-c", "echo " + marker}
}

// shellCommand is a command that prints a marker and stays alive a
// while, so that a data stream has output.
func shellCommand(marker string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe", "/c", "echo " + marker + " & ping -n 5 127.0.0.1 >nul"}
	}
	return []string{"/bin/sh", "-c", "echo " + marker + "; sleep 5"}
}

// waitFor polls until cond is true.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// wsWrite sends a binary or text message with a deadline.
func wsWrite(t *testing.T, ws *websocket.Conn, typ websocket.MessageType, p []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ws.Write(ctx, typ, p); err != nil {
		t.Fatal(err)
	}
}

// wsRead reads one message with a deadline.
func wsRead(t *testing.T, ws *websocket.Conn) (websocket.MessageType, []byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return ws.Read(ctx)
}

// closeStatus returns the close status of a read error.
func closeStatus(err error) websocket.StatusCode { return websocket.CloseStatus(err) }

// openControl opens a control connection and sends a hello, without a
// reader goroutine, for tests that expect the server to close it.
func openControl(t *testing.T, f *fixture) *websocket.Conn {
	t.Helper()
	ws := f.dial("/api/control", controlProtocol)
	hello, _ := json.Marshal(map[string]any{
		"type": "hello", "api_revision": apiRevision,
		"client": map[string]any{"kind": "cli", "name": "rex"},
	})
	wsWrite(t, ws, websocket.MessageText, hello)
	typ, data, err := wsRead(t, ws)
	if err != nil || typ != websocket.MessageText {
		t.Fatalf("welcome: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil || m["type"] != "welcome" {
		t.Fatalf("welcome: %s", data)
	}
	return ws
}
