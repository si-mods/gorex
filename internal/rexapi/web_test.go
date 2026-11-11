package rexapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestNormalizeOrigin checks how origins are read: scheme and host in
// lower case, the default port left out, and anything but an origin
// refused.
func TestNormalizeOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"https://GoRex.Example.com":     "https://gorex.example.com",
		"HTTPS://gorex.example.com:443": "https://gorex.example.com",
		"http://studio:80":              "http://studio",
		"http://studio:443":             "http://studio:443",
		"https://studio:80":             "https://studio:80",
		"http://127.0.0.1:5173":         "http://127.0.0.1:5173",
		"http://127.0.0.1:5173/":        "http://127.0.0.1:5173",
		"http://[::1]:7297":             "http://[::1]:7297",
		"http://[0:0::1]":               "http://[::1]",
		" https://web.gorex.dev ":       "https://web.gorex.dev",
	} {
		got, err := NormalizeOrigin(in)
		if err != nil || got != want {
			t.Errorf("%q: %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "null", "gorex.example.com", "ftp://gorex.example.com", "file:///tmp/x",
		"https://*.example.com", "https://example.com/app", "https://example.com/?q", "https://example.com#x",
		"https://user@example.com", "https://example.com:", "https://example.com:0", "https://example.com:65536",
		"https://example.com:08080", "https://exa mple.com", "https://[::1%25eth0]", "https://", "%zz",
	} {
		if got, err := NormalizeOrigin(in); err == nil {
			t.Errorf("%q: %q, want an error", in, got)
		}
	}
}

// trustOrigins writes web.json in a new directory with origins, and
// returns the gateway's list read from it.
func trustOrigins(t *testing.T, origins ...string) *webOrigins {
	t.Helper()
	dir := t.TempDir()
	if err := SaveWeb(dir, WebConfig{Origins: origins}); err != nil {
		t.Fatal(err)
	}
	return newWebOrigins(dir)
}

// TestWebOrigins checks the list: none without a state directory or a
// file, the origins in it as browsers send them, and a change picked up.
func TestWebOrigins(t *testing.T) {
	var none *webOrigins
	if none.allowed("https://web.gorex.dev") {
		t.Error("a gateway without a state directory trusted a page")
	}
	empty := newWebOrigins(t.TempDir())
	if empty.allowed("https://web.gorex.dev") {
		t.Error("a gateway without web.json trusted a page")
	}
	o := trustOrigins(t, "https://web.gorex.dev", "http://127.0.0.1:5173")
	for origin, want := range map[string]bool{
		"https://web.gorex.dev":      true,
		"https://WEB.gorex.dev:443":  true,
		"http://127.0.0.1:5173":      true,
		"http://web.gorex.dev":       false, // another scheme
		"https://web.gorex.dev:8443": false,
		"http://127.0.0.1:5174":      false,
		"http://localhost:5173":      false,
		"https://evil.gorex.dev":     false,
		"null":                       false,
	} {
		if got := o.allowed(origin); got != want {
			t.Errorf("%q: %v, want %v", origin, got, want)
		}
	}
	// A change to the file is read within a second.
	if err := SaveWeb(o.dir, WebConfig{Origins: []string{"https://other.example"}}); err != nil {
		t.Fatal(err)
	}
	o.mu.Lock()
	o.checked = time.Time{}
	o.mu.Unlock()
	if o.allowed("https://web.gorex.dev") || !o.allowed("https://other.example") {
		t.Error("web.json's change was not picked up")
	}
}

// TestOriginRefused checks that the gateway refuses a page it does not
// trust on every endpoint, before the access rule, and serves native
// clients and a trusted page, with CORS headers for that page only.
func TestOriginRefused(t *testing.T) {
	allowed := 0
	g := &Gateway{
		opt: Options{Access: AccessFunc(func(*http.Request) error { allowed++; return nil })},
		web: trustOrigins(t, "https://web.gorex.dev"),
	}
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()
	do := func(method, path, origin string) *http.Response {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	for _, origin := range []string{"http://evil.example", "null", srv.URL, "http://web.gorex.dev"} {
		for _, path := range []string{"/api/control", "/api/data", "/api/upload", "/api/spaces", "/api/help", "/llms.txt", "/"} {
			res := do("GET", path, origin)
			if res.StatusCode != http.StatusForbidden {
				t.Errorf("%s from %s: %d, want 403", path, origin, res.StatusCode)
			}
			if res.Header.Get("Access-Control-Allow-Origin") != "" {
				t.Errorf("%s from %s: CORS headers on a refusal", path, origin)
			}
		}
		if res := do("OPTIONS", "/api/upload", origin); res.StatusCode != http.StatusForbidden {
			t.Errorf("preflight from %s: %d, want 403", origin, res.StatusCode)
		}
	}
	if allowed != 0 {
		t.Errorf("the access rule was asked %d times for refused requests", allowed)
	}
	if res := do("GET", "/llms.txt", ""); res.StatusCode != http.StatusOK || res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("/llms.txt without an Origin: %d %v", res.StatusCode, res.Header)
	}
	res := do("GET", "/llms.txt", "https://web.gorex.dev")
	if res.StatusCode != http.StatusOK || res.Header.Get("Access-Control-Allow-Origin") != "https://web.gorex.dev" ||
		!slices.Contains(res.Header.Values("Vary"), "Origin") {
		t.Errorf("/llms.txt from a trusted page: %d %v", res.StatusCode, res.Header)
	}
	if allowed != 2 {
		t.Errorf("the access rule was asked %d times, want 2", allowed)
	}
	// The web client is not served: the page is hosted on its own.
	if res := do("GET", "/", ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("/: %d, want 404", res.StatusCode)
	}
}

// TestOriginAccessRule checks that a trusted page still meets the access
// rule, and can read its refusal.
func TestOriginAccessRule(t *testing.T) {
	g := &Gateway{
		opt: Options{Access: AccessFunc(func(*http.Request) error { return errString("not in the networks allowed") })},
		web: trustOrigins(t, "https://web.gorex.dev"),
	}
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/api/control", nil)
	req.Header.Set("Origin", "https://web.gorex.dev")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || res.Header.Get("Access-Control-Allow-Origin") != "https://web.gorex.dev" {
		t.Errorf("a trusted page the access rule refuses: %d %v", res.StatusCode, res.Header)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// TestUploadCORS checks what a browser does before and for an upload
// from a trusted page: the preflight is answered with the methods and
// header an upload needs, and the POST reaches the upload's handler with
// the page's origin allowed.
func TestUploadCORS(t *testing.T) {
	g := &Gateway{web: trustOrigins(t, "http://127.0.0.1:5173")}
	srv := httptest.NewServer(g.Handler())
	defer srv.Close()
	pre, _ := http.NewRequest("OPTIONS", srv.URL+"/api/upload?block_id=b&name=x", nil)
	pre.Header.Set("Origin", "http://127.0.0.1:5173")
	pre.Header.Set("Access-Control-Request-Method", "POST")
	pre.Header.Set("Access-Control-Request-Headers", "content-type")
	res, err := http.DefaultClient.Do(pre)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	h := res.Header
	if res.StatusCode != http.StatusNoContent || h.Get("Access-Control-Allow-Origin") != "http://127.0.0.1:5173" ||
		!strings.Contains(h.Get("Access-Control-Allow-Methods"), "POST") ||
		!strings.EqualFold(h.Get("Access-Control-Allow-Headers"), "Content-Type") {
		t.Fatalf("preflight: %d %v", res.StatusCode, h)
	}
	post, _ := http.NewRequest("POST", srv.URL+"/api/upload?block_id=b&name=x", bytes.NewReader([]byte("bytes")))
	post.Header.Set("Origin", "http://127.0.0.1:5173")
	post.Header.Set("Content-Type", "application/octet-stream")
	res, err = http.DefaultClient.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	// This gateway has no GoRex server to take the file: the handler
	// answers, and the page can read it.
	if res.StatusCode == http.StatusForbidden || res.Header.Get("Access-Control-Allow-Origin") != "http://127.0.0.1:5173" {
		t.Fatalf("upload from a trusted page: %d %v", res.StatusCode, res.Header)
	}
}

// TestControlOrigin checks the WebSocket endpoints end to end: a control
// connection opens from a native client and a trusted page, and not from
// another page, the gateway's own address included.
func TestControlOrigin(t *testing.T) {
	f := startFixture(t)
	f.g.web = trustOrigins(t, "https://web.gorex.dev")
	dial := func(path, proto, origin string) (*websocket.Conn, *http.Response, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		h := http.Header{}
		if origin != "" {
			h.Set("Origin", origin)
		}
		return websocket.Dial(ctx, f.srv.URL+path, &websocket.DialOptions{Subprotocols: []string{proto}, HTTPHeader: h})
	}
	for _, origin := range []string{"", "https://web.gorex.dev", "https://WEB.gorex.dev:443"} {
		ws, _, err := dial("/api/control", controlProtocol, origin)
		if err != nil {
			t.Fatalf("origin %q: %v", origin, err)
		}
		c := newTestConn(t, ws)
		if w := c.hello(10); w["type"] != "welcome" {
			t.Fatalf("origin %q: %v", origin, w)
		}
		ws.CloseNow()
	}
	// The data endpoint upgrades for a trusted page (its HELLO decides
	// the rest).
	if ws, _, err := dial("/api/data", "rex.data.terminal.v1", "https://web.gorex.dev"); err != nil {
		t.Fatalf("data from a trusted page: %v", err)
	} else {
		ws.CloseNow()
	}
	for _, path := range []string{"/api/control", "/api/data"} {
		for _, origin := range []string{f.srv.URL, "https://evil.example", "null"} {
			ws, res, err := dial(path, controlProtocol, origin)
			if err == nil {
				ws.CloseNow()
				t.Fatalf("%s: a page of %s connected", path, origin)
			}
			if res == nil || res.StatusCode != http.StatusForbidden {
				t.Fatalf("%s from %s: %v, %v", path, origin, res, err)
			}
		}
	}
}

// TestWebCommand trusts and distrusts origins from the command line, with
// no gateway: web.json changes, for the gateway to follow when it runs.
func TestWebCommand(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	t.Setenv("GOREX_REXAPI_SOCKET", shortSocket(t))
	quiet(t)
	dir := TailnetDir()
	for _, step := range []struct {
		args []string
		code int
		want []string
	}{
		{[]string{"status"}, 0, nil},
		{[]string{"allow", "https://Web.GoRex.dev:443/"}, 0, []string{"https://web.gorex.dev"}},
		{[]string{"allow", "http://127.0.0.1:5173"}, 0, []string{"https://web.gorex.dev", "http://127.0.0.1:5173"}},
		{[]string{"allow", "https://web.gorex.dev"}, 0, []string{"https://web.gorex.dev", "http://127.0.0.1:5173"}},
		{[]string{"allow", "https://*.gorex.dev"}, 2, []string{"https://web.gorex.dev", "http://127.0.0.1:5173"}},
		{[]string{"allow", "https://web.gorex.dev/app"}, 2, []string{"https://web.gorex.dev", "http://127.0.0.1:5173"}},
		{[]string{"allow", "null"}, 2, []string{"https://web.gorex.dev", "http://127.0.0.1:5173"}},
		{[]string{"deny", "HTTPS://web.gorex.dev"}, 0, []string{"http://127.0.0.1:5173"}},
		{[]string{"deny", "https://not.there"}, 0, []string{"http://127.0.0.1:5173"}},
		{[]string{"allow"}, 2, []string{"http://127.0.0.1:5173"}},
		{[]string{"sideways"}, 2, []string{"http://127.0.0.1:5173"}},
		{nil, 2, []string{"http://127.0.0.1:5173"}},
	} {
		if code := webMain(step.args); code != step.code {
			t.Fatalf("web %v: exit %d, want %d", step.args, code, step.code)
		}
		c, err := LoadWeb(dir)
		if err != nil || !slices.Equal(c.Origins, step.want) {
			t.Fatalf("after web %v: %v, %v; want %v", step.args, c.Origins, err, step.want)
		}
	}
	// The gateway's list reads what the command wrote.
	if o := newWebOrigins(dir); !o.allowed("http://127.0.0.1:5173") || o.allowed("https://web.gorex.dev") {
		t.Error("the gateway's list does not follow web.json")
	}
	if _, err := os.Stat(webConfPath(dir)); err != nil {
		t.Error(err)
	}
}
