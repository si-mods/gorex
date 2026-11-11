package rexapi

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestSameOrigin checks which Origin headers pass: none, as native
// clients send, and the gateway's own scheme, host and port only.
func TestSameOrigin(t *testing.T) {
	for _, c := range []struct {
		host, origin string
		tls          bool
		want         bool
	}{
		{"192.168.1.5:7297", "", false, true},
		{"192.168.1.5:7297", "http://192.168.1.5:7297", false, true},
		{"192.168.1.5:7297", "HTTP://192.168.1.5:7297", false, true},
		{"studio:80", "http://studio", false, true},
		{"studio", "http://studio:80", false, true},
		{"studio.ts.net", "https://studio.ts.net", true, true},
		{"studio.ts.net", "https://studio.ts.net:443", true, true},
		{"[::1]:7297", "http://[::1]:7297", false, true},
		// Another page, port or scheme.
		{"192.168.1.5:7297", "http://evil.example", false, false},
		{"192.168.1.5:7297", "http://192.168.1.5:8080", false, false},
		{"192.168.1.5:7297", "https://192.168.1.5:7297", false, false},
		{"studio.ts.net", "http://studio.ts.net:443", true, false},
		{"192.168.1.5:7297", "null", false, false},
		{"192.168.1.5:7297", "http://user@192.168.1.5:7297", false, false},
		{"192.168.1.5:7297", "%zz", false, false},
	} {
		r := httptest.NewRequest("GET", "/api/control", nil)
		r.Host = c.host
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if c.tls {
			r.TLS = &tls.ConnectionState{}
		} else {
			r.TLS = nil
		}
		if got := sameOrigin(r); got != c.want {
			t.Errorf("host %q, origin %q, tls %v: %v, want %v", c.host, c.origin, c.tls, got, c.want)
		}
	}
}

// TestCrossOriginRefused checks that the gateway refuses a web page of
// another origin on every endpoint, before the access rule, and still
// serves native clients and its own page.
func TestCrossOriginRefused(t *testing.T) {
	allowed := 0
	g := &Gateway{opt: Options{Access: AccessFunc(func(*http.Request) error { allowed++; return nil })}}
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()
	get := func(path, origin string) int {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for _, path := range []string{"/api/control", "/api/data", "/api/upload", "/api/spaces", "/api/help", "/llms.txt", "/"} {
		if code := get(path, "http://evil.example"); code != http.StatusForbidden {
			t.Errorf("%s from another origin: %d, want 403", path, code)
		}
	}
	if allowed != 0 {
		t.Errorf("the access rule was asked %d times for refused requests", allowed)
	}
	if code := get("/llms.txt", ""); code != http.StatusOK {
		t.Errorf("/llms.txt without an Origin: %d", code)
	}
	if code := get("/llms.txt", srv.URL); code != http.StatusOK {
		t.Errorf("/llms.txt from the gateway's own origin: %d", code)
	}
}

// TestControlOrigin checks the WebSocket endpoints end to end: a control
// connection opens from the gateway's own page and from a native client,
// and not from another page.
func TestControlOrigin(t *testing.T) {
	f := startFixture(t)
	dial := func(origin string) (*websocket.Conn, *http.Response, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		h := http.Header{}
		if origin != "" {
			h.Set("Origin", origin)
		}
		return websocket.Dial(ctx, f.srv.URL+"/api/control", &websocket.DialOptions{
			Subprotocols: []string{controlProtocol}, HTTPHeader: h,
		})
	}
	for _, origin := range []string{"", f.srv.URL} {
		ws, _, err := dial(origin)
		if err != nil {
			t.Fatalf("origin %q: %v", origin, err)
		}
		c := newTestConn(t, ws)
		if w := c.hello(10); w["type"] != "welcome" {
			t.Fatalf("origin %q: %v", origin, w)
		}
		ws.CloseNow()
	}
	ws, res, err := dial(strings.Replace(f.srv.URL, "127.0.0.1", "localhost", 1))
	if err == nil {
		ws.CloseNow()
		t.Fatal("a page of another origin opened a control connection")
	}
	if res == nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("another origin: %v, %v", res, err)
	}
}
