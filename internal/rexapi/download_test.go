package rexapi

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"tailscale.com/client/tailscale/apitype"

	"gorex/internal/rex"
)

// downloadShell is a session's command that stays until it is killed.
func downloadShell() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe"}
	}
	return []string{"/bin/sh"}
}

// lingeringDir is a directory for a session to start in, removed when
// the test ends as far as it can be: on Windows the shell may hold it a
// while after it is killed.
func lingeringDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gorex-dl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for range 50 {
			if os.RemoveAll(dir) == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	return dir
}

// downloadSession starts a session in a directory of its own and returns
// a connection attached to it, its session and block, and the directory.
func downloadSession(t *testing.T, f *fixture) (*testConn, string, string, string) {
	t.Helper()
	dir := lingeringDir(t)
	c, sid, block := f.sessionWith(t, rex.CreateOptions{Command: downloadShell(), Dir: dir, Cols: 80, Rows: 24})
	return c, sid, block, dir
}

// downloadBegin calls com.superlogical.terminal.download_begin for a
// block's path, and returns the response.
func downloadBegin(c *testConn, session, block, path string) map[string]any {
	c.t.Helper()
	return c.request("download_begin", "com.superlogical.terminal.download_begin", map[string]any{
		"session_id": session, "block_id": block, "args": map[string]any{"path": path},
	})
}

// getDownload fetches a ticket's file, from a page of origin when set.
func getDownload(t *testing.T, f *fixture, ticket, origin string) (*http.Response, []byte, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.srv.URL+"/api/download?ticket="+url.QueryEscape(ticket), nil)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	return res, body, err
}

// TestDownloadFeature: the welcome lists gorex.download only when the
// server sends files, and download_begin is not_supported without.
func TestDownloadFeature(t *testing.T) {
	f := startFixture(t)
	c, sid, block, _ := downloadSession(t, f)
	if features, _ := c.welcome["features"].([]any); !slices.Contains(features, any(featureDownload)) {
		t.Fatalf("welcome features: %v", c.welcome["features"])
	}
	f.g.hello.Capabilities = slices.DeleteFunc(slices.Clone(f.g.hello.Capabilities), func(s string) bool {
		return s == rex.CapabilityFileTransfer
	})
	c2 := f.dialControl()
	c2.hello(apiRevision)
	if features, _ := c2.welcome["features"].([]any); slices.Contains(features, any(featureDownload)) {
		t.Errorf("welcome features without file transfer: %v", c2.welcome["features"])
	}
	if e := failure(t, downloadBegin(c, sid, block, "a.txt")); e["code"] != "not_supported" {
		t.Errorf("download_begin without file transfer: %v", e)
	}
}

// TestDownloadPaths: download_begin takes a path as file.get does,
// absolute, from ~/ and relative to the session's directory, and maps
// what it can't send to codes.
func TestDownloadPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	f := startFixture(t)
	c, sid, block, dir := downloadSession(t, f)
	files := map[string]string{
		filepath.Join(dir, "abs.txt"):   "absolute",
		filepath.Join(dir, "rel.txt"):   "relative",
		filepath.Join(home, "home.txt"): "home",
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for path, want := range map[string]string{
		filepath.Join(dir, "abs.txt"): "absolute",
		"rel.txt":                     "relative",
		"~/home.txt":                  "home",
	} {
		res := result(t, downloadBegin(c, sid, block, path))
		if res["size"] != float64(len(want)) || res["type"] != "text/plain" || res["name"] == "" {
			t.Errorf("%s: %v", path, res)
		}
		_, body, err := getDownload(t, f, res["ticket"].(string), "")
		if err != nil || string(body) != want {
			t.Errorf("%s: %q %v, want %q", path, body, err, want)
		}
	}

	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := maxDownloadFileBytes
	maxDownloadFileBytes = 4
	defer func() { maxDownloadFileBytes = old }()
	for path, code := range map[string]string{
		"sub":                            "is_folder",
		filepath.Join(dir, "sub"):        "is_folder",
		"missing.txt":                    "no_file",
		filepath.Join(dir, "no", "such"): "no_file",
		"rel\x00.txt":                    "internal",
		"abs.txt":                        "too_large",
	} {
		if e := failure(t, downloadBegin(c, sid, block, path)); e["code"] != code {
			t.Errorf("%q: %v, want %s", path, e, code)
		}
	}
	if e := failure(t, downloadBegin(c, sid, "block:nope", "abs.txt")); e["code"] != "not_found" {
		t.Errorf("no block: %v", e)
	}
	if e := failure(t, downloadBegin(c, sid, block, "")); e["code"] != "bad_request" {
		t.Errorf("no path: %v", e)
	}
	// The good tickets were used, and no failure keeps one.
	if n := openDownloadCount(f.g); n != 0 {
		t.Errorf("%d downloads left open", n)
	}
}

// TestDownloadStream: GET /api/download streams the file once, with the
// headers of an attachment, and the exposed headers for a trusted page.
func TestDownloadStream(t *testing.T) {
	f := startFixture(t)
	f.g.web = trustOrigins(t, "https://web.gorex.dev")
	c, sid, block, dir := downloadSession(t, f)
	name := "photo ü \"x\".png"
	if runtime.GOOS == "windows" {
		name = "photo ü x.png" // Windows names have no quote
	}
	data := bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 300_000)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC)
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	res := result(t, downloadBegin(c, sid, block, p))
	if res["name"] != name || res["size"] != float64(len(data)) || res["mtime"] != "2026-11-12T09:30:00Z" || res["type"] != "image/png" {
		t.Fatalf("download_begin: %v", res)
	}
	ticket := res["ticket"].(string)
	resp, body, err := getDownload(t, f, ticket, "https://web.gorex.dev")
	if err != nil || resp.StatusCode != http.StatusOK || !bytes.Equal(body, data) {
		t.Fatalf("download: %d, %d bytes, %v", resp.StatusCode, len(body), err)
	}
	h := resp.Header
	for k, want := range map[string]string{
		"Content-Type":                  "application/octet-stream",
		"Cache-Control":                 "no-store",
		"X-Content-Type-Options":        "nosniff",
		"Last-Modified":                 "Thu, 12 Nov 2026 09:30:00 GMT",
		"Access-Control-Allow-Origin":   "https://web.gorex.dev",
		"Access-Control-Expose-Headers": "Content-Disposition, Content-Length",
	} {
		if got := h.Get(k); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
	}
	if resp.ContentLength != int64(len(data)) {
		t.Errorf("Content-Length %d", resp.ContentLength)
	}
	cd := h.Get("Content-Disposition")
	typ, params, err := mime.ParseMediaType(cd)
	if err != nil || typ != "attachment" || params["filename"] != name {
		t.Errorf("Content-Disposition %q: %s %v %v", cd, typ, params, err)
	}
	if !strings.Contains(cd, "filename=") || !strings.Contains(cd, "filename*=utf-8''") {
		t.Errorf("Content-Disposition %q has no ASCII fallback and UTF-8 name", cd)
	}
	for _, r := range cd {
		if r > 0x7e {
			t.Errorf("Content-Disposition %q is not ASCII", cd)
			break
		}
	}
	// A ticket is good once.
	if resp, _, _ := getDownload(t, f, ticket, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("second use: %d", resp.StatusCode)
	}
	if resp, _, _ := getDownload(t, f, "nope", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown ticket: %d", resp.StatusCode)
	}
	// Without an Origin, a navigation, it is served with no CORS header.
	res = result(t, downloadBegin(c, sid, block, p))
	resp, body, err = getDownload(t, f, res["ticket"].(string), "")
	if err != nil || resp.StatusCode != http.StatusOK || !bytes.Equal(body, data) || resp.Header.Get("Access-Control-Expose-Headers") != "" {
		t.Errorf("download without Origin: %d %d bytes %v %v", resp.StatusCode, len(body), err, resp.Header)
	}
}

// openDownloadCount counts the gateway's open tickets.
func openDownloadCount(g *Gateway) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.downloads)
}

// TestDownloadLapse: a ticket unused lapses, and one of a control
// connection that closed is gone: both answer 404, their gets closed.
func TestDownloadLapse(t *testing.T) {
	f := startFixture(t)
	c, sid, block, dir := downloadSession(t, f)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := downloadWait
	downloadWait = 200 * time.Millisecond
	lapsed := result(t, downloadBegin(c, sid, block, "a.txt"))["ticket"].(string)
	downloadWait = old
	waitFor(t, "the ticket to lapse", func() bool { return openDownloadCount(f.g) == 0 })
	if resp, _, _ := getDownload(t, f, lapsed, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("lapsed ticket: %d", resp.StatusCode)
	}

	kept := result(t, downloadBegin(c, sid, block, "a.txt"))["ticket"].(string)
	if openDownloadCount(f.g) != 1 {
		t.Fatalf("%d open", openDownloadCount(f.g))
	}
	c.ws.Close(1000, "")
	waitFor(t, "the connection's tickets to go", func() bool { return openDownloadCount(f.g) == 0 })
	if resp, _, _ := getDownload(t, f, kept, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("ticket of a closed connection: %d", resp.StatusCode)
	}
}

// TestDownloadLimit: a control connection holds at most four tickets.
func TestDownloadLimit(t *testing.T) {
	f := startFixture(t)
	c, sid, block, dir := downloadSession(t, f)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	var tickets []string
	for range maxDownloadsPerControl {
		tickets = append(tickets, result(t, downloadBegin(c, sid, block, "a.txt"))["ticket"].(string))
	}
	if e := failure(t, downloadBegin(c, sid, block, "a.txt")); e["code"] != "conflict" {
		t.Errorf("a fifth ticket: %v", e)
	}
	// Using one makes room for another.
	if resp, body, err := getDownload(t, f, tickets[0], ""); err != nil || resp.StatusCode != 200 || string(body) != "abc" {
		t.Fatalf("download: %d %q %v", resp.StatusCode, body, err)
	}
	result(t, downloadBegin(c, sid, block, "a.txt"))
}

// TestDownloadShort: a file cut short as it is sent aborts the response,
// so that the client never takes a part for the whole file.
func TestDownloadShort(t *testing.T) {
	f := startFixture(t)
	c, sid, block, dir := downloadSession(t, f)
	p := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), 16<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	ticket := result(t, downloadBegin(c, sid, block, "big.bin"))["ticket"].(string)
	if err := os.Truncate(p, 1<<20); err != nil {
		t.Fatal(err)
	}
	resp, body, err := getDownload(t, f, ticket, "")
	if resp.StatusCode != http.StatusOK || err == nil || !errors.Is(err, io.ErrUnexpectedEOF) || len(body) >= 16<<20 {
		t.Errorf("short download: %d, %d bytes, %v", resp.StatusCode, len(body), err)
	}
}

// TestHostCheck: over a listener a browser reaches, a request whose Host
// is not the gateway's gets 421, on every path; localhost, an IP
// address, built-in Tailscale's names and a name of web.json pass. The
// local socket and tailcat are not checked.
func TestHostCheck(t *testing.T) {
	f := startFixture(t)
	dir := t.TempDir()
	if err := SaveWeb(dir, WebConfig{Hosts: []string{"gorex.example.com"}}); err != nil {
		t.Fatal(err)
	}
	f.g.web = newWebOrigins(dir)
	node := &fakeNode{state: "Running", hostname: "dev", owner: 7, callers: map[string]*apitype.WhoIsResponse{}, ready: make(chan struct{})}
	f.g.tailnet = newTailnet(f.g, t.TempDir(), nil)
	f.g.tailnet.node = node
	defer func() { f.g.tailnet = nil }()
	do := func(transport, path, host string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = host
		rec := httptest.NewRecorder()
		f.g.HandlerFor(transport).ServeHTTP(rec, req)
		return rec.Code
	}
	for _, path := range []string{"/api/download?ticket=x", "/api/spaces", "/api/control", "/"} {
		for _, transport := range []string{transportTCP, transportLAN, transportTailscale} {
			for _, host := range []string{"evil.example", "evil.example:7297", "gorex.example.com.evil.example", "dev.evil.example", ""} {
				if code := do(transport, path, host); code != http.StatusMisdirectedRequest {
					t.Errorf("%s %s Host %q: %d, want 421", transport, path, host, code)
				}
			}
		}
		for _, host := range []string{
			"localhost", "LOCALHOST:7297", "127.0.0.1", "127.0.0.1:7297", "192.168.1.20:7297", "[::1]:7297", "[fe80::1]",
			"100.64.0.1", "dev.example.ts.net", "DEV.example.ts.net.", "dev", "dev:443", "gorex.example.com", "gorex.example.com:8443",
		} {
			if code := do(transportTCP, path, host); code == http.StatusMisdirectedRequest {
				t.Errorf("%s Host %q: 421", path, host)
			}
		}
		for _, transport := range []string{transportUnix, transportTailcat} {
			if code := do(transport, path, "abc.tailcat.invalid:7297"); code == http.StatusMisdirectedRequest {
				t.Errorf("%s %s: 421", transport, path)
			}
		}
	}
	// With built-in Tailscale off, its names are not the gateway's.
	f.g.tailnet.node = nil
	if code := do(transportTCP, "/api/spaces", "dev.example.ts.net"); code != http.StatusMisdirectedRequest {
		t.Errorf("tailnet name with Tailscale off: %d", code)
	}
}

// TestWebHostCommand adds and removes host names from the command line.
func TestWebHostCommand(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	t.Setenv("GOREX_REXAPI_SOCKET", shortSocket(t))
	quiet(t)
	dir := TailnetDir()
	for _, step := range []struct {
		args []string
		code int
		want []string
	}{
		{[]string{"host", "add", "GoRex.Example.com."}, 0, []string{"gorex.example.com"}},
		{[]string{"host", "add", "gorex.example.com"}, 0, []string{"gorex.example.com"}},
		{[]string{"host", "add", "proxy.example"}, 0, []string{"gorex.example.com", "proxy.example"}},
		{[]string{"host", "add", "*.example"}, 2, []string{"gorex.example.com", "proxy.example"}},
		{[]string{"host", "add", "proxy.example:8443"}, 2, []string{"gorex.example.com", "proxy.example"}},
		{[]string{"host", "add", "https://proxy.example"}, 2, []string{"gorex.example.com", "proxy.example"}},
		{[]string{"host", "remove", "GOREX.example.com"}, 0, []string{"proxy.example"}},
		{[]string{"host", "add"}, 2, []string{"proxy.example"}},
		{[]string{"host", "list", "x"}, 2, []string{"proxy.example"}},
		{[]string{"status"}, 0, []string{"proxy.example"}},
	} {
		if code := webMain(step.args); code != step.code {
			t.Fatalf("web %v: exit %d, want %d", step.args, code, step.code)
		}
		c, err := LoadWeb(dir)
		if err != nil || !slices.Equal(c.Hosts, step.want) {
			t.Fatalf("after web %v: %v, %v; want %v", step.args, c.Hosts, err, step.want)
		}
	}
	if o := newWebOrigins(dir); !o.host("proxy.example") || o.host("gorex.example.com") {
		t.Error("the gateway's list does not follow web.json's hosts")
	}
}

// TestGuessType guesses from the extension, without parameters.
func TestGuessType(t *testing.T) {
	for name, want := range map[string]string{
		"a.PNG": "image/png", "b.jpeg": "image/jpeg", "c.svg": "image/svg+xml", "d.avif": "image/avif",
		"e.txt": "text/plain", "Makefile": "application/octet-stream", "f.unknownext": "application/octet-stream",
	} {
		if got := guessType(name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}
