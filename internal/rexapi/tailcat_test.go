package rexapi

import (
	"context"
	"errors"
	"net"
	"net/http"
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
	streams := make(chan net.Conn)
	l := newTailcatListener()
	fails := 1
	l.accept = func(ctx context.Context) (net.Conn, rex.RemoteClient, error) {
		if fails > 0 {
			// A server that does not answer at first is asked again.
			fails--
			return nil, rex.RemoteClient{}, errors.New("unknown op")
		}
		select {
		case c := <-streams:
			return c, phone, nil
		case <-ctx.Done():
			return nil, rex.RemoteClient{}, ctx.Err()
		}
	}
	tl := newTrackedListener(l)
	done := make(chan error, 1)
	go func() { done <- f.g.serveOn(tl, transportTailcat) }()

	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		a, b := net.Pipe()
		select {
		case streams <- b:
			return a, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
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
	tl.Close()
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
