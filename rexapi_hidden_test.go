package main

import (
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"gorex/internal/rex"
	"gorex/internal/rexapi"
)

// TestRexAPIDestroysHiddenSession destroys a session whose space the window
// does not show: its tabs are in the layout, and their sessions end with
// it.
func TestRexAPIDestroysHiddenSession(t *testing.T) {
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
	mk := func(label string) string {
		layout := map[string]any{"block": map[string]any{
			"flavor":  rexapi.FlavorShell,
			"options": map[string]any{"cwd": "/tmp", "command": []string{"/bin/sh", "-c", "sleep 60"}},
		}}
		res := c.call("session.create", map[string]any{
			"label":           label,
			"initial_windows": []any{map[string]any{"window_label": "w-" + label, "layout": layout}},
		})
		sid, _ := res["session_id"].(string)
		if sid == "" {
			t.Fatalf("session.create %v", res)
		}
		return sid
	}
	one := mk("one")
	two := mk("two")
	// The window shows the last space made, so "one" is hidden: its tab is
	// in the layout, not in the window's app.
	var hidden *Workspace
	for _, w := range a.st.workspaces {
		if w.Name == "one" {
			hidden = w
		}
	}
	if hidden == nil || hidden.app != nil {
		t.Fatalf("the space of %q: %+v", one, hidden)
	}
	c.call("session.attach", map[string]any{"session_id": one})
	c.call("session.destroy", map[string]any{"session_id": one})
	for _, s := range c.call("session.list", nil)["sessions"].([]any) {
		if s.(map[string]any)["session_id"] == one {
			t.Fatalf("the hidden session stays: %v", s)
		}
	}
	for _, w := range a.st.workspaces {
		if w.Name == "one" {
			t.Fatalf("the hidden space stays: %+v", w)
		}
	}
	// The session the window shows is still there, and can be destroyed.
	c.call("session.attach", map[string]any{"session_id": two})
	c.call("session.destroy", map[string]any{"session_id": two})
}
