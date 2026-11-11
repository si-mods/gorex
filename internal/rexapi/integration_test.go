package rexapi

import (
	"context"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestRexCLIIntegration runs the Rex CLI against a gateway when the
// machine has one. CI has no Rex, so it is skipped there.
func TestRexCLIIntegration(t *testing.T) {
	rex, err := exec.LookPath("rex")
	if err != nil {
		t.Skip("no rex on PATH")
	}
	f := startFixture(t)
	f.setLayout(standardLayout())
	for _, args := range [][]string{
		{"--autostart=false", "-S", f.srv.URL, "api", "call", "server.version"},
		{"--autostart=false", "-S", f.srv.URL, "ls"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, rex, args...)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Errorf("%s: %v\n%s", strings.Join(args, " "), err, out)
			continue
		}
		if len(strings.TrimSpace(string(out))) == 0 {
			t.Errorf("%s printed nothing", strings.Join(args, " "))
		}
	}
}

// TestOriginRefused checks that a request with an Origin header, which
// a browser sends and a native client does not, is answered with status
// 403 and a JSON error on every endpoint, and that the same requests
// without it go on.
func TestOriginRefused(t *testing.T) {
	f := startFixture(t)
	f.setLayout(standardLayout())
	const origin = "https://example.com"
	for _, path := range []string{"/api/control", "/api/data", "/api/upload", "/api/spaces", "/api/help", "/llms.txt", "/elsewhere"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			req, _ := http.NewRequest(method, f.srv.URL+path, strings.NewReader("x"))
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("Origin", origin)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), `"error":`) {
				t.Errorf("%s %s: %v %s", method, path, resp.Status, body)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for path, protocol := range map[string]string{"/api/control": controlProtocol, "/api/data": terminalProtocol} {
		ws, resp, err := websocket.Dial(ctx, f.srv.URL+path, &websocket.DialOptions{
			Subprotocols: []string{protocol},
			HTTPHeader:   http.Header{"Origin": {origin}},
		})
		if err == nil {
			ws.CloseNow()
			t.Errorf("%s: a WebSocket with an Origin was accepted", path)
		} else if resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: %v", path, err)
		}
	}

	// Without an Origin, the same connections and upload go on.
	f.dialControl().hello(apiRevision)
	f.dial("/api/data", terminalProtocol)
	if status, res := postUpload(t, f, "block:nope", "a.txt", "application/octet-stream", []byte("x")); status != http.StatusNotFound {
		t.Errorf("upload without Origin: %d %v", status, res)
	}
}

// TestClientLostState checks that a dropped client is listed as lost
// until it reconnects.
func TestClientLostState(t *testing.T) {
	f := startFixture(t)
	f.setLayout(standardLayout())
	c := f.dialControl()
	w := c.hello(apiRevision)
	id := welcomeID(t, w)
	idToken := welcomeToken(t, w)
	ids := sessionIDs(t, c)
	callChecked(t, c, "session.attach", map[string]any{"session_id": ids["work"]})
	c.ws.CloseNow()
	waitFor(t, "the client to be lost", func() bool {
		f.g.mu.Lock()
		rec := f.g.clients[id]
		f.g.mu.Unlock()
		if rec == nil {
			return false
		}
		lost, _ := rec.state()
		return lost
	})

	other := f.dialControl()
	other.hello(apiRevision)
	res := callChecked(t, other, "client.list", nil)
	var seen map[string]any
	for _, raw := range res["clients"].([]any) {
		cl := raw.(map[string]any)
		if cl["client_id"] == id {
			seen = cl
		}
	}
	if seen == nil || seen["connection_state"] != "lost" || seen["connection_lost_at"] == nil {
		t.Fatalf("lost client: %v", seen)
	}
	// It can come back with its token.
	back := f.dialControl()
	back.send(map[string]any{"type": "hello", "api_revision": apiRevision, "reconnect_token": idToken})
	w2 := back.nextType("welcome")
	if cl := w2["client"].(map[string]any); cl["resumed"] != true || cl["id"] != id {
		t.Errorf("resume: %v", w2)
	}
}

// TestKeepalivePing checks that the server answers a client's ping.
func TestKeepalivePing(t *testing.T) {
	f := startFixture(t)
	c := f.dialControl()
	c.hello(apiRevision)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.ws.Ping(ctx); err != nil {
		t.Errorf("ping: %v", err)
	}
}
