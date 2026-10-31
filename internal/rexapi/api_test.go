//go:build darwin || linux || windows

package rexapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"gorex/internal/rex"
)

// testServer starts a session server in a short temporary directory, as
// rex's own tests do, and returns it and its client.
func testServer(t *testing.T) (*rex.Client, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "rexapi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("GOREX_DIR", dir)
	go rex.Serve()
	var c *rex.Client
	for range 100 {
		if _, err := os.Stat(rex.SocketPath()); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	c, err = rex.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.Shutdown()
		time.Sleep(50 * time.Millisecond)
	})
	return c, rex.SocketPath()
}

// testAPI starts the Rex API over a server, on a socket of its own.
func testAPI(t *testing.T, c *rex.Client) (*Server, string) {
	t.Helper()
	s, err := New(Options{Socket: rex.SocketPath(), Version: "gorex test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	sock := filepath.Join(filepath.Dir(rex.SocketPath()), "api.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: s.Handler()}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return s, sock
}

// testLayout puts two panes of two sessions in one window of a workspace.
func testLayout(t *testing.T, c *rex.Client, sid1, sid2 string) string {
	t.Helper()
	l := fmt.Sprintf(`{"version":2,"sessions":[{"id":"SPACE1","name":"work","active":0,"tabs":[{"id":"TAB1","name":"editor","root":{"vertical":false,"ratio":0.5,"a":{"sid":%q,"name":"left"},"b":{"sid":%q,"name":"right"}},"focus":1}]}],"windows":["SPACE1"]}`, sid1, sid2)
	if err := c.SetLayout(json.RawMessage(l)); err != nil {
		t.Fatal(err)
	}
	return l
}

// dial opens a WebSocket to the API's socket.
func dial(t *testing.T, sock, path string, subprotocols ...string) *websocket.Conn {
	t.Helper()
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("unix", sock)
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://localhost"+path, &websocket.DialOptions{
		Subprotocols: subprotocols,
		HTTPClient:   hc,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	return ws
}

// hello opens a control connection and reads its welcome.
func hello(t *testing.T, sock string) (*websocket.Conn, map[string]any) {
	t.Helper()
	ws := dial(t, sock, "/api/control", "rex.control.v1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws.Write(ctx, websocket.MessageText, []byte(`{"type":"hello","api_revision":7,"client":{"kind":"other","name":"test"}}`))
	_, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var w map[string]any
	if err := json.Unmarshal(data, &w); err != nil {
		t.Fatal(err)
	}
	return ws, w
}

// rpc sends a request and returns its response, skipping events.
func rpc(t *testing.T, ws *websocket.Conn, id, method string, payload any) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := map[string]any{"type": "request", "id": id, "method": method}
	if payload != nil {
		req["payload"] = payload
	}
	data, _ := json.Marshal(req)
	if err := ws.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		if m["type"] != "response" || m["id"] != id {
			continue
		}
		return m
	}
}

// ok fails the test when a response carries an error.
func ok(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	if e, has := m["error"]; has {
		t.Fatalf("error: %v", e)
	}
	out, _ := m["payload"].(map[string]any)
	return out
}

// uuidShape is what every ID a Rex client parses must look like.
var uuidShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// idShape fails the test unless an ID has the kind and a UUID body.
func idShape(t *testing.T, id, kind string) {
	t.Helper()
	body, found := strings.CutPrefix(id, kind+":")
	if !found || !uuidShape.MatchString(body) {
		t.Errorf("%q is not a %s ID with a UUID", id, kind)
	}
}

func TestControlAndSessions(t *testing.T) {
	c, _ := testServer(t)
	a, err := c.Create(rex.CreateOptions{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Create(rex.CreateOptions{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	testLayout(t, c, a.ID, b.ID)
	_, sock := testAPI(t, c)

	ws, w := hello(t, sock)
	if w["protocol"] != "rex.control.v1" || w["type"] != "welcome" {
		t.Fatalf("welcome %v", w)
	}
	client := w["client"].(map[string]any)
	idShape(t, client["id"].(string), "client")

	list := ok(t, rpc(t, ws, "1", "session.list", nil))
	sessions := list["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("sessions %v", sessions)
	}
	s := sessions[0].(map[string]any)
	if s["label"] != "work" {
		t.Errorf("label %v", s["label"])
	}
	sid := s["session_id"].(string)
	idShape(t, sid, "session")

	att := ok(t, rpc(t, ws, "2", "session.attach", map[string]any{"session_id": sid}))
	v := att["view"].(map[string]any)
	windows := v["windows"].([]any)
	if len(windows) != 1 {
		t.Fatalf("windows %v", windows)
	}
	win := windows[0].(map[string]any)
	idShape(t, win["window_id"].(string), "window")
	if want := "window:" + uuidLike("window", "TAB1"); win["window_id"] != want {
		t.Errorf("window %v, want %v", win["window_id"], want)
	}
	if win["label"] != "editor" {
		t.Errorf("window label %v", win["label"])
	}
	layer := win["layers"].([]any)[0].(map[string]any)
	layout := layer["layout"].(map[string]any)
	split, _ := layout["split"].(map[string]any)
	if split == nil || split["direction"] != "horizontal" {
		t.Fatalf("layout %v", layout)
	}
	idShape(t, split["split_id"].(string), "split")
	left := split["before"].(map[string]any)
	right := split["after"].(map[string]any)
	if !strings.HasPrefix(right["block_id"].(string), "block:") {
		t.Errorf("block %v", right)
	}
	if rect := right["rect"].([]any); rect[0].(float64) != 0.5 {
		t.Errorf("after rect %v", rect)
	}
	if win["focused_block_id"] != right["block_id"] {
		t.Errorf("focused %v, want %v", win["focused_block_id"], right["block_id"])
	}

	blocks := ok(t, rpc(t, ws, "3", "session.list_blocks", map[string]any{"session_id": sid}))["blocks"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("blocks %v", blocks)
	}
	bid := left["block_id"].(string)
	target := ok(t, rpc(t, ws, "4", "target.resolve", map[string]any{
		"kinds": []string{"block"}, "queries": []string{"left"},
		"scope": map[string]any{"session": sid},
	}))["results"].([]any)[0].(map[string]any)
	if target["status"] != "resolved" {
		t.Fatalf("resolve %v", target)
	}

	d := ok(t, rpc(t, ws, "5", "session.describe_block", map[string]any{"session_id": sid, "block_id": bid}))
	if d["block"].(map[string]any)["placement"] != "placed" {
		t.Errorf("describe %v", d["block"])
	}
	if m := rpc(t, ws, "6", "session.nonsense", nil); m["error"] == nil {
		t.Error("a method the server does not have succeeded")
	}
	if m := rpc(t, ws, "7", "hosts.put", map[string]any{"host": map[string]any{}}); m["error"] == nil {
		t.Error("hosts.put succeeded")
	}
}

func TestBlockMethodsAndKeys(t *testing.T) {
	c, _ := testServer(t)
	a, err := c.Create(rex.CreateOptions{Command: []string{"/bin/sh"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Create(rex.CreateOptions{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	testLayout(t, c, a.ID, b.ID)
	_, sock := testAPI(t, c)
	ws, _ := hello(t, sock)
	sid := "session:" + uuidLike("session", "SPACE1")
	ok(t, rpc(t, ws, "1", "session.attach", map[string]any{"session_id": sid}))
	bid := "block:" + uuidLike("block", a.ID)

	res := ok(t, rpc(t, ws, "2", CreatorName+".write", map[string]any{
		"session_id": sid, "block_id": bid, "args": map[string]any{"data": "echo bonjour\n"},
	}))
	if res["bytes_written"].(float64) != float64(len("echo bonjour\n")) {
		t.Errorf("write %v", res)
	}
	var content string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res = ok(t, rpc(t, ws, "3", "block.method", map[string]any{
			"session_id": sid, "block_id": bid, "method": "format",
			"args": map[string]any{"format": "text", "trim": true},
		}))
		content, _ = res["content"].(string)
		if strings.Contains(content, "bonjour") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(content, "bonjour") {
		t.Fatalf("the screen: %q", content)
	}
	// The key events the CLI sends type into the shell.
	key := map[string]any{"key": "enter", "mods": "", "text": "", "action": "press"}
	ok(t, rpc(t, ws, "4", "client.key", map[string]any{"session_id": sid, "block_id": bid, "key": key}))
	if m := ok(t, rpc(t, ws, "5", "com.superlogical.terminal.size", map[string]any{
		"session_id": sid, "block_id": bid, "args": map[string]any{},
	})); m["columns"].(float64) <= 0 {
		t.Errorf("size %v", m)
	}
}

func TestEventsFollowTheLayout(t *testing.T) {
	c, _ := testServer(t)
	a, err := c.Create(rex.CreateOptions{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Create(rex.CreateOptions{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	raw := testLayout(t, c, a.ID, b.ID)
	_, sock := testAPI(t, c)
	ws, _ := hello(t, sock)
	sid := "session:" + uuidLike("session", "SPACE1")
	ok(t, rpc(t, ws, "1", "session.attach", map[string]any{"session_id": sid}))

	// Rename the window, as the app would: the layout the server keeps
	// changes, and the connection hears of it.
	renamed := strings.Replace(raw, `"name":"editor"`, `"name":"files"`, 1)
	if err := c.SetLayoutAt(json.RawMessage(renamed), 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("no event: %v", err)
		}
		var m map[string]any
		json.Unmarshal(data, &m)
		if m["type"] != "event" {
			continue
		}
		inner := m["payload"].(map[string]any)["payload"].(map[string]any)
		ev := inner["event"].(map[string]any)
		if ev["type"] == "window_label_changed" {
			if ev["payload"].(map[string]any)["label"] != "files" {
				t.Errorf("label %v", ev["payload"])
			}
			return
		}
	}
}

// TestDataConnection opens a data connection and reads the screen.
func TestDataConnection(t *testing.T) {
	c, _ := testServer(t)
	a, err := c.Create(rex.CreateOptions{Command: []string{"/bin/sh"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Create(rex.CreateOptions{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	testLayout(t, c, a.ID, b.ID)
	_, sock := testAPI(t, c)
	ws, _ := hello(t, sock)
	sid := "session:" + uuidLike("session", "SPACE1")
	ok(t, rpc(t, ws, "1", "session.attach", map[string]any{"session_id": sid}))
	bid := "block:" + uuidLike("block", a.ID)
	if err := c.Input(a.ID, []byte("echo stream-ok\n")); err != nil {
		t.Fatal(err)
	}
	res := ok(t, rpc(t, ws, "2", "data.connect", map[string]any{
		"session_id": sid, "block_id": bid, "data_ref_id": "terminal", "subprotocol": dataProtocol,
	}))
	ticket := res["ticket"].(string)
	data, err := urlDecode(ticket)
	if err != nil {
		t.Fatal(err)
	}
	dws := dial(t, sock, "/api/data", dataProtocol)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := dws.Write(ctx, websocket.MessageBinary, append([]byte{0x01}, data...)); err != nil {
		t.Fatal(err)
	}
	_, welcome, err := dws.Read(ctx)
	if err != nil || len(welcome) != 32 || welcome[0] != 0x81 {
		t.Fatalf("welcome %v %v", welcome, err)
	}
	var out []byte
	for len(out) < 4096 {
		_, msg, err := dws.Read(ctx)
		if err != nil {
			break
		}
		out = append(out, msg...)
	}
	if !strings.Contains(string(out), "stream-ok") {
		t.Fatalf("the stream: %q", out)
	}
}

// urlDecode reads a ticket.
func urlDecode(s string) ([]byte, error) { return urlNoPad().DecodeString(s) }

func TestHelp(t *testing.T) {
	c, _ := testServer(t)
	_, sock := testAPI(t, c)
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("unix", sock)
	}}}
	res, err := hc.Get("http://localhost/api/help")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	var help struct {
		HTTP    map[string]string `json:"http"`
		Methods []struct {
			Name string `json:"name"`
			In   any    `json:"input_schema"`
		} `json:"methods"`
	}
	if err := json.NewDecoder(res.Body).Decode(&help); err != nil {
		t.Fatal(err)
	}
	if help.HTTP["control_path"] != "/api/control" || len(help.Methods) < 20 {
		t.Fatalf("help %v", help)
	}
	for _, m := range help.Methods {
		if m.Name == "session.view" && m.In == nil {
			t.Error("session.view has no schema")
		}
	}
}

// TestWindowIDKeepsTheTab checks that a window keeps its ID when the pane
// that came first closes, and that a tab of a layout without IDs falls
// back to its first pane.
func TestWindowIDKeepsTheTab(t *testing.T) {
	first := `{"sessions":[{"id":"S","name":"w","active":0,"tabs":[{"id":"TAB","root":{"a":{"sid":"aa"},"b":{"sid":"bb"}}}]}]}`
	second := `{"sessions":[{"id":"S","name":"w","active":0,"tabs":[{"id":"TAB","root":{"sid":"bb"}}]}]}`
	m1, err := buildModel(1, json.RawMessage(first), nil)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := buildModel(2, json.RawMessage(second), nil)
	if err != nil {
		t.Fatal(err)
	}
	w1, w2 := m1.sessions[0].Windows[0].ID, m2.sessions[0].Windows[0].ID
	if w1 != w2 {
		t.Errorf("the window is %q, then %q", w1, w2)
	}
	old := `{"sessions":[{"id":"S","name":"w","active":0,"tabs":[{"root":{"sid":"cc"}}]}]}`
	m3, err := buildModel(3, json.RawMessage(old), nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "window:" + uuidLike("window", "cc"); m3.sessions[0].Windows[0].ID != want {
		t.Errorf("a tab without an ID: %q, want %q", m3.sessions[0].Windows[0].ID, want)
	}
}
