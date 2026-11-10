package rexapi

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"gorex/internal/rex"
)

// TestTailcatGateway serves the gateway on streams as the session server
// hands them over from its tailcat address: a client is the device the
// server named, and gets Rex's API, sessions from the real server
// included, but not the administering methods.
func TestTailcatGateway(t *testing.T) {
	f := startFixture(t)
	f.setLayout(standardLayout())
	phone := rex.RemoteClient{Name: "phone", PublicKey: "nodekey:" + strings.Repeat("ab", 32)}
	sock := shortSocket(t)
	ln, err := listenUnix(sock)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.g.serveOn(tailcatListener{ln}, transportTailcat) }()

	// A connection that names no client is dropped; the next is served.
	if junk, err := net.Dial("unix", sock); err == nil {
		junk.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
		junk.SetReadDeadline(time.Now().Add(5 * time.Second))
		if b, _ := io.ReadAll(junk); len(b) != 0 {
			t.Errorf("a stream that names no client got %q", b)
		}
		junk.Close()
	}

	// The session server dials the socket and names the client first.
	line, _ := json.Marshal(phone)
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, "unix", sock)
		if err == nil {
			_, err = c.Write(append(line, '\n'))
		}
		return c, err
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://tailcat/api/control", &websocket.DialOptions{
		HTTPClient: client, Subprotocols: []string{controlProtocol},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	c := newTestConn(t, ws)
	c.hello(apiRevision)
	who := callChecked(t, c, "server.whoami", nil)
	p, _ := who["principal"].(map[string]any)
	if p["kind"] != "device" || p["transport"] != "tailcat" || p["display"] != "phone" || p["device"] != nil {
		t.Errorf("principal: %v", p)
	}
	if path, _ := p["path"].([]any); len(path) != 1 || path[0] != "tailcat" {
		t.Errorf("path: %v", p["path"])
	}
	if ids := sessionIDs(t, c); ids["work"] == "" || ids["side"] == "" {
		t.Errorf("sessions: %v", ids)
	}
	if e := failure(t, c.request("d", "server.tailscale.disable", nil)); e["status"] != float64(403) {
		t.Errorf("administering over tailcat: %v", e)
	}

	// Closing the listener closes its connections.
	ln.Close()
	if _, _, err := ws.Read(ctx); err == nil {
		t.Error("the tailcat connection stayed open")
	}
	if err := <-done; err != nil {
		t.Errorf("serving: %v", err)
	}
}

// TestTailcatRefusesOthers: the tailcat handler lets in only the streams
// of its listener.
func TestTailcatRefusesOthers(t *testing.T) {
	f := startFixture(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go f.g.serveOn(ln, transportTailcat)
	defer ln.Close()
	resp, err := http.Get("http://" + ln.Addr().String() + "/api/help")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("a plain TCP caller: %d", resp.StatusCode)
	}
}

// TestWantedReadsItsDataDir: remote access on, in the data directory that
// holds the gateway's state, wants the gateway; another's does not count,
// and a change is seen.
func TestWantedReadsItsDataDir(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	t.Setenv("GOREX_DIR", other)
	dir := filepath.Join(root, "rexapi")
	write := func(path, s string) {
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(other, "remote.json"), `{"enabled":true}`)
	if Wanted(dir) {
		t.Error("wanted for another data directory's remote access")
	}
	write(filepath.Join(root, "remote.json"), `{"enabled":true,"allowed":[]}`)
	if !Wanted(dir) {
		t.Error("not wanted with remote access on")
	}
	write(filepath.Join(root, "remote.json"), `{"enabled":false}`)
	if Wanted(dir) {
		t.Error("wanted with remote access off")
	}
}
