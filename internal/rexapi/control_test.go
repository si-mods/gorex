package rexapi

import (
	"context"
	"fmt"
	"runtime"
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
	f := startFixture(t)
	ws := f.dial("/api/control", "rex.control.v0")
	wsWrite(t, ws, websocket.MessageText, []byte(`{"type":"hello","api_revision":7}`))
	expectClose(t, ws, 1002)
}

func TestHelloNeedsFiveSeconds(t *testing.T) {
	f := startFixture(t)
	f.g.helloWait = 80 * time.Millisecond
	ws := f.dial("/api/control", controlProtocol)
	expectClose(t, ws, -1)
}

func TestHelloSize(t *testing.T) {
	f := startFixture(t)
	ws := f.dial("/api/control", controlProtocol)
	hello := `{"type":"hello","api_revision":7,"client":{"name":"` + strings.Repeat("x", maxHello) + `"}}`
	wsWrite(t, ws, websocket.MessageText, []byte(hello))
	expectClose(t, ws, 1002)
}

func TestHelloBinary(t *testing.T) {
	f := startFixture(t)
	ws := f.dial("/api/control", controlProtocol)
	wsWrite(t, ws, websocket.MessageBinary, []byte(`{"type":"hello"}`))
	expectClose(t, ws, 1003)
}

func TestHelloInvalidUTF8(t *testing.T) {
	f := startFixture(t)
	ws := f.dial("/api/control", controlProtocol)
	wsWrite(t, ws, websocket.MessageText, []byte{0xff, 0xfe})
	expectClose(t, ws, 1007)
}

func TestNoCommonRevision(t *testing.T) {
	f := startFixture(t)
	ws := f.dial("/api/control", controlProtocol)
	newer := apiRevision + 1
	wsWrite(t, ws, websocket.MessageText, []byte(fmt.Sprintf(
		`{"type":"hello","api_revision":%d,"api_min_revision":%d}`, newer, newer)))
	expectClose(t, ws, 1008)
}

func TestRequestErrors(t *testing.T) {
	f := startFixture(t)
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
	f := startFixture(t)
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
	f := startFixture(t)
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

// TestClientCloseEndsWriter checks that a connection the client ends,
// with a close frame or without, stops its writer and keepalive.
func TestClientCloseEndsWriter(t *testing.T) {
	f := startFixture(t)
	f.setLayout(standardLayout())
	for _, abrupt := range []bool{false, true} {
		c := f.dialControl()
		c.hello(apiRevision)
		waitFor(t, "the writer to start", func() bool { return controlLoops() > 0 })
		if abrupt {
			c.ws.CloseNow()
		} else {
			c.ws.Close(websocket.StatusNormalClosure, "")
		}
		waitFor(t, "the writer and keepalive to end", func() bool { return controlLoops() == 0 })
	}
}

// controlLoops counts the goroutines running a control connection's
// writer or keepalive.
func controlLoops() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	return strings.Count(string(buf), "(*control).writeLoop") + strings.Count(string(buf), "(*control).keepalive")
}

// TestStalledClientHoldsNoOne checks that dropping a client whose events
// back up does not hold up another client's requests while its close
// handshake waits.
func TestStalledClientHoldsNoOne(t *testing.T) {
	f := startFixture(t)
	f.setLayout(standardLayout())
	other := f.dialControl()
	other.hello(apiRevision)
	result(t, other.request("l1", "session.list", nil))
	// This client reads nothing, so it never answers a close frame.
	openControl(t, f)
	var stalled *control
	waitFor(t, "the second connection", func() bool {
		f.g.mu.Lock()
		defer f.g.mu.Unlock()
		for c := range f.g.conns {
			if c.rec.id != welcomeID(t, other.welcome) {
				stalled = c
			}
		}
		return stalled != nil
	})
	stalled.rec.mu.Lock()
	stalled.rec.all = true
	stalled.rec.mu.Unlock()
	stalled.mu.Lock()
	stalled.queued = eventBacklog
	stalled.mu.Unlock()
	f.setLayout(layoutWithTabName(t, "work", "renamed"))
	go f.g.refresh()
	select {
	case <-stalled.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stalled client was not dropped")
	}
	start := time.Now()
	result(t, other.request("l2", "session.list", nil))
	if d := time.Since(start); d > time.Second {
		t.Errorf("a request waited %v for another client's close", d)
	}
}

// TestControlFrames checks the bytes a control connection queues for
// responses and events, which clients parse: key order, omitted fields,
// escaping and the older revisions' shapes.
func TestControlFrames(t *testing.T) {
	rec := &clientRecord{id: "c1", all: true, scope: "all", attached: map[string]bool{}}
	conn := func(rev int) *control {
		return &control{rec: rec, rev: rev, out: make(chan outMsg, 16), done: make(chan struct{})}
	}
	type entry struct {
		Name string `json:"name"`
		Note string `json:"note,omitempty"`
		HTML string `json:"html"`
	}
	payload := map[string]any{"z": 1, "a": entry{Name: "x", HTML: "<b>&"}, "m": nil}
	tailscale := map[string]any{"state": "running", "control_url": "https://c.example"}
	c, old := conn(apiRevision), conn(9)
	c.respond(&request{ID: "r1", seq: 3}, payload, nil)
	c.respond(&request{ID: "r2", Method: "server.version"}, nil, nil)
	c.respond(&request{ID: "r3"}, nil, errMethodNotFound("%s is not a method", "x.y"))
	c.respond(&request{ID: "r4", seq: 1}, strings.Repeat("x", maxMessage), nil)
	old.respond(&request{ID: "r5", Method: "server.status"}, map[string]any{"tailscale": tailscale}, nil)
	chs := []change{
		{session: "S1", typ: "tab_renamed", payload: payload},
		{session: "S1", typ: "session_created"},
		{server: true, typ: "client_joined", payload: map[string]any{"client_id": "c2"}},
		{server: true, typ: "tailscale_status_changed", payload: tailscale},
	}
	c.sendChanges(chs)
	old.sendChanges(chs)
	want := []string{
		`{"type":"response","id":"r1","seq":3,"requester":"c1","payload":{"a":{"name":"x","html":"\u003cb\u003e\u0026"},"m":null,"z":1}}`,
		`{"type":"response","id":"r2","seq":0,"requester":"c1","payload":{}}`,
		`{"type":"response","id":"r3","seq":0,"requester":"c1","error":{"code":"method_not_found","message":"x.y is not a method","status":404}}`,
		`{"type":"response","id":"r4","seq":1,"requester":"c1","error":{"code":"too_large","message":"the result is larger than 1048576 bytes","status":413}}`,
		`{"type":"event","seq":1,"payload":{"type":"session_event","payload":{"session_id":"S1","event":{"type":"tab_renamed","payload":{"a":{"name":"x","html":"\u003cb\u003e\u0026"},"m":null,"z":1}}}}}`,
		`{"type":"event","seq":2,"payload":{"type":"session_event","payload":{"session_id":"S1","event":{"type":"session_created"}}}}`,
		`{"type":"event","seq":3,"payload":{"type":"client_joined","payload":{"client_id":"c2"}}}`,
		`{"type":"event","seq":4,"payload":{"type":"tailscale_status_changed","payload":{"control_url":"https://c.example","state":"running"}}}`,
		`{"type":"response","id":"r5","seq":0,"requester":"c1","payload":{"tailscale":{"state":"running"}}}`,
		`{"type":"event","seq":1,"payload":{"type":"session_event","payload":{"session_id":"S1","event":{"type":"tab_renamed","payload":{"a":{"name":"x","html":"\u003cb\u003e\u0026"},"m":null,"z":1}}}}}`,
		`{"type":"event","seq":2,"payload":{"type":"session_event","payload":{"session_id":"S1","event":{"type":"session_created"}}}}`,
		`{"type":"event","seq":3,"payload":{"type":"client_joined","payload":{"client_id":"c2"}}}`,
		`{"type":"event","seq":4,"payload":{"type":"tailscale_status_changed","payload":{"state":"running"}}}`,
	}
	var got []string
	for _, out := range []chan outMsg{c.out, old.out} {
		for len(out) > 0 {
			got = append(got, string((<-out).data))
		}
	}
	if len(got) != len(want) {
		t.Fatalf("got %d frames, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("frame %d:\n got %s\nwant %s", i, got[i], want[i])
		}
	}
}

// BenchmarkEventFanout sends one changed view's events to many
// connections, as refresh does.
func BenchmarkEventFanout(b *testing.B) {
	rec := &clientRecord{id: "c1", all: true, scope: "all", attached: map[string]bool{}}
	conns := make([]*control, 64)
	for i := range conns {
		conns[i] = &control{rec: rec, rev: apiRevision, out: make(chan outMsg, 16), done: make(chan struct{})}
	}
	b.ReportAllocs()
	for b.Loop() {
		events := []change{
			{session: "S1", typ: "window_label_changed", payload: windowPayload{WindowID: "W1", Label: "editor"}},
			{session: "S1", typ: "session_view_changed", payload: revisionPayload{Revision: 42}},
		}
		for _, c := range conns {
			c.sendChanges(events)
			for len(c.out) > 0 {
				<-c.out
			}
			c.queued = 0
		}
	}
}
