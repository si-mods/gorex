package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	"gorex/internal/rex"
	"gorex/internal/rexapi"
)

// rexWS is a control connection of Rex's API, driven by a test.
type rexWS struct {
	t  *testing.T
	ws *websocket.Conn
	n  int
}

// rexHello opens the API's control connection and greets it.
func rexHello(t *testing.T, sock string) *rexWS {
	t.Helper()
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("unix", sock)
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://localhost/api/control", &websocket.DialOptions{
		Subprotocols: []string{"rex.control.v1"},
		HTTPClient:   hc,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	ws.Write(ctx, websocket.MessageText, []byte(`{"type":"hello","api_revision":7,"client":{"kind":"other","name":"test"}}`))
	if _, data, err := ws.Read(ctx); err != nil {
		t.Fatal(err)
	} else {
		var w map[string]any
		json.Unmarshal(data, &w)
		if w["type"] != "welcome" {
			t.Fatalf("welcome %v", w)
		}
	}
	return &rexWS{t: t, ws: ws}
}

// call runs a method and returns its payload, failing on an error.
func (c *rexWS) call(method string, payload any) map[string]any {
	c.t.Helper()
	c.n++
	id := string(rune('a' + c.n))
	req := map[string]any{"type": "request", "id": id, "method": method}
	if payload != nil {
		req["payload"] = payload
	}
	data, _ := json.Marshal(req)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, data); err != nil {
		c.t.Fatal(err)
	}
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			c.t.Fatalf("%s: %v", method, err)
		}
		var m map[string]any
		json.Unmarshal(data, &m)
		if m["type"] != "response" || m["id"] != id {
			continue
		}
		if e, has := m["error"]; has {
			c.t.Fatalf("%s: %v", method, e)
		}
		out, _ := m["payload"].(map[string]any)
		return out
	}
}

// TestRexAPICreatesSessions drives the app as a Rex client does: it makes
// a session with a window and a block that runs a command, sees it in the
// view, and destroys it.
func TestRexAPICreatesSessions(t *testing.T) {
	a, tt := newTestApp(t)
	go a.st.takeCommands(a.client, a.post)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				tt.Frame()
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()
	api, err := rexapi.New(rexapi.Options{Socket: rex.SocketPath(), Version: "gorex test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	sock := filepath.Join(filepath.Dir(rex.SocketPath()), "api.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: api.Handler()}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	c := rexHello(t, sock)
	created := c.call("session.create", map[string]any{
		"label": "rexmade",
		"initial_windows": []any{map[string]any{
			"window_label": "editor",
			"layout": map[string]any{"block": map[string]any{
				"flavor":  rexapi.FlavorShell,
				"options": map[string]any{"cwd": "/tmp", "command": []string{"/bin/sh", "-c", "echo rexmade"}},
			}},
		}},
	})
	sid, _ := created["session_id"].(string)
	if sid == "" {
		t.Fatalf("session.create %v", created)
	}
	windows := created["initial_windows"].([]any)
	if len(windows) != 1 || len(windows[0].(map[string]any)["block_ids"].([]any)) != 1 {
		t.Fatalf("initial_windows %v", windows)
	}
	// The session is the app's space, shown in its window.
	found := false
	for _, w := range a.st.workspaces {
		if w.Name == "rexmade" && w.app != nil && len(w.app.tabs) == 1 && w.app.tabs[0].Name == "editor" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the space is not in the app: %+v", a.st.workspaces)
	}
	c.call("session.attach", map[string]any{"session_id": sid})
	view := c.call("session.view", map[string]any{"session_id": sid})
	if got := view["label"]; got != "rexmade" {
		t.Errorf("label %v", got)
	}
	if ws := view["windows"].([]any); len(ws) != 1 || ws[0].(map[string]any)["label"] != "editor" {
		t.Errorf("windows %v", ws)
	}
	c.call("session.destroy", map[string]any{"session_id": sid})
	deadline := time.Now().Add(5 * time.Second)
	for {
		list := c.call("session.list", nil)["sessions"].([]any)
		gone := true
		for _, s := range list {
			if s.(map[string]any)["session_id"] == sid {
				gone = false
			}
		}
		if gone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the session stays: %v", list)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
