package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorex/internal/rex"
	"gorex/internal/reximp"
	"gorex/internal/rexpeer"
	"gorex/internal/testrex"
)

// TestRexImportCreatesSpaces imports a Rex session into the app: a space,
// a tab and its panes, named as Rex's, then leaves one already imported
// alone.
func TestRexImportCreatesSpaces(t *testing.T) {
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
	// Two sessions, laid out as a Rex server's: one session, one window of
	// two blocks side by side.
	one, err := a.client.Create(rex.CreateOptions{Command: []string{"/bin/sh"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	two, err := a.client.Create(rex.CreateOptions{Command: []string{"/bin/sh"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	layout := fmt.Sprintf(`{"version":2,"sessions":[{"id":"REXSPACE","name":"rexspace","active":0,"tabs":[{"id":"REXTAB","name":"rexwin","root":{"ratio":0.5,"a":{"sid":%q,"name":"left"},"b":{"sid":%q,"name":"right"}},"focus":0}]}],"windows":["REXSPACE"]}`, one.ID, two.ID)
	if err := a.client.SetLayout(json.RawMessage(layout)); err != nil {
		t.Fatal(err)
	}
	api, err := testrex.New(testrex.Options{Socket: rex.SocketPath(), Version: "test"})
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	peer, err := rexpeer.Dial(ctx, sock)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	state := filepath.Join(t.TempDir(), "rex-import.json")
	res, err := reximp.Run(ctx, a.client, peer, reximp.Options{
		Shell:     true,
		StatePath: state,
		RexSocket: sock,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The Detached session is skipped: it holds no window.
	if len(res.Imported) != 1 || len(res.Skipped) != 1 {
		t.Fatalf("imported %+v, skipped %+v", res.Imported, res.Skipped)
	}
	imp := res.Imported[0]
	if imp.Label != "rexspace" || imp.Windows != 1 || imp.Blocks != 2 {
		t.Fatalf("imported %+v", imp)
	}
	var ws *Workspace
	for _, w := range a.st.workspaces {
		if w.Name == "rexspace" {
			ws = w
		}
	}
	if ws == nil || ws.app == nil || len(ws.app.tabs) != 1 {
		t.Fatalf("the space: %+v", ws)
	}
	tab := ws.app.tabs[0]
	if tab.Name != "rexwin" || len(tab.panes()) != 2 {
		t.Fatalf("the tab: %+v", tab)
	}
	names := map[string]bool{}
	for _, p := range tab.panes() {
		names[p.Name] = true
	}
	if !names["left"] || !names["right"] {
		t.Fatalf("the panes: %v", names)
	}
	// A session the record names is left alone: the import writes the
	// record after each session, so a later run skips it.
	now, err := peer.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var already string
	for _, s := range now {
		if s.Label != "rexspace" && s.Label != "Detached" {
			already = s.SessionID
		}
	}
	if already == "" {
		t.Fatalf("no session left to test the record with: %v", now)
	}
	record := map[string]any{"sessions": map[string]any{already: map[string]string{"label": "kept", "space": "SPACE"}}}
	b, _ := json.Marshal(record)
	if err := os.WriteFile(state, b, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = reximp.Run(ctx, a.client, peer, reximp.Options{
		Sessions:  []string{already},
		Shell:     true,
		StatePath: state,
		RexSocket: sock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Imported) != 0 || len(res.Skipped) != 1 || res.Skipped[0].Session != already {
		t.Fatalf("the second run: %+v %+v", res.Imported, res.Skipped)
	}
}
