//go:build darwin || linux || windows

package rexpeer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorex/internal/rex"
	"gorex/internal/rexapi"
)

// startGorex starts a session server in a short temporary directory and
// returns it with the Rex API over its socket, as a Rex server would be.
func startGorex(t *testing.T) (*rex.Client, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "rexpeer")
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
	api, err := rexapi.New(rexapi.Options{Socket: rex.SocketPath(), Version: "test"})
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
	return c, sock
}

func TestPeerListsAndMirrors(t *testing.T) {
	c, sock := startGorex(t)
	a, err := c.Create(rex.CreateOptions{Command: []string{"/bin/sh"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Create(rex.CreateOptions{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	layout := fmt.Sprintf(`{"version":2,"sessions":[{"id":"SPACE","name":"work","active":0,"tabs":[{"id":"TAB","name":"editor","root":{"ratio":0.5,"a":{"sid":%q,"name":"left"},"b":{"sid":%q,"name":"right"}},"focus":0}]}],"windows":["SPACE"]}`, a.ID, b.ID)
	if err := c.SetLayout(json.RawMessage(layout)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	peer, err := Dial(ctx, sock)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()

	sessions, err := peer.Sessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].Label != "work" {
		t.Fatalf("sessions %v %v", sessions, err)
	}
	sid := sessions[0].SessionID
	view, err := peer.Attach(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Windows) != 1 || view.Windows[0].Label != "editor" {
		t.Fatalf("view %+v", view)
	}
	var layer *Layer
	for i := range view.Windows[0].Layers {
		if view.Windows[0].Layers[i].Kind == "tiled" {
			layer = &view.Windows[0].Layers[i]
		}
	}
	if layer == nil || layer.Layout.Split == nil {
		t.Fatalf("layer %+v", layer)
	}
	blocks, err := peer.Blocks(ctx, sid)
	if err != nil || len(blocks) != 2 {
		t.Fatalf("blocks %v %v", blocks, err)
	}
	var leftID string
	for _, bl := range blocks {
		if bl.Label == "left" {
			leftID = bl.BlockID
		}
	}
	if leftID == "" {
		leftID = layer.Layout.Split.Before.BlockID
	}
	if err := peer.Write(ctx, sid, leftID, "echo peer-ok\n"); err != nil {
		t.Fatal(err)
	}
	if err := peer.Resize(ctx, sid, leftID, 100, 30); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if dir := peer.Pwd(ctx, sid, leftID); dir == "" {
		t.Log("the shell reported no directory")
	}
	screen, err := peer.Format(ctx, sid, leftID, "text")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(screen, "peer-ok") {
		t.Logf("the screen, before the text is drawn: %q", screen)
	}
	stream, err := peer.Data(ctx, sid, leftID)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.SkipSnapshot(); err != nil {
		t.Fatal(err)
	}
	if stream.Nonce() == 0 {
		t.Error("the stream has no nonce")
	}
	got := make(chan string, 1)
	go func() {
		var out []byte
		buf := make([]byte, 4096)
		for len(out) < 1<<20 {
			n, err := stream.Read(buf)
			if n > 0 {
				out = append(out, buf[:n]...)
				if strings.Contains(string(out), "peer-ok") {
					got <- string(out)
					return
				}
			}
			if err != nil {
				got <- string(out)
				return
			}
		}
		got <- string(out)
	}()
	select {
	case out := <-got:
		if !strings.Contains(out, "peer-ok") {
			t.Fatalf("the stream: %q", out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the stream sent nothing")
	}
}
