package share

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestShareAdmissionStaticAndProxy(t *testing.T) {
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<h1>GoRex</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(webDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "assets", "app.js"), []byte("console.log('ok');"), 0o644); err != nil {
		t.Fatal(err)
	}

	var gotOrigin string
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrigin = r.Header.Get("Origin")
		gotPath = r.URL.Path
		if r.URL.Path == "/api/control" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			defer c.CloseNow()
			mt, msg, err := c.Read(r.Context())
			if err == nil {
				_ = c.Write(r.Context(), mt, append([]byte("echo:"), msg...))
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	pendingCh := make(chan Device, 1)
	srv, err := Start(Config{
		Addr:      "127.0.0.1:0",
		WebDir:    webDir,
		PhoneName: "Test iPhone",
		OnPending: func(d Device) {
			select {
			case pendingCh <- d:
			default:
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	if !srv.Alive() {
		t.Fatal("expected server Alive() == true")
	}

	hostsJSON, _ := json.Marshal([]Host{
		{ID: "mac1", Name: "MacBook", Icon: "laptop", URL: upstream.URL, Transport: "system"},
	})
	if err := srv.SetHosts(string(hostsJSON)); err != nil {
		t.Fatal(err)
	}

	base := "http://" + srv.Addr()

	// 1. Before admission: / returns 403 admission page; /share/hosts and /h/mac1/api/help return 403.
	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "Allow this computer on your iPhone") {
		t.Fatalf("expected 403 admission page, got %d: %s", resp.StatusCode, body)
	}

	d := <-pendingCh
	if d.IP != "127.0.0.1" || d.State != "pending" {
		t.Fatalf("unexpected pending device: %+v", d)
	}

	for _, p := range []string{"/share/hosts", "/h/mac1/api/help"} {
		r, err := http.Get(base + p)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusForbidden {
			t.Fatalf("%s before admission: got %d, want 403", p, r.StatusCode)
		}
	}

	// 2. Admit 127.0.0.1.
	srv.Admit("127.0.0.1")

	r, err := http.Get(base + "/share/state")
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		State string `json:"state"`
	}
	_ = json.NewDecoder(r.Body).Decode(&st)
	r.Body.Close()
	if st.State != "admitted" {
		t.Fatalf("state = %q, want admitted", st.State)
	}

	// Static files work.
	r, err = http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusOK || string(b) != "<h1>GoRex</h1>" {
		t.Fatalf("GET / = %d %q", r.StatusCode, b)
	}

	// /share/hosts returns public host list.
	r, err = http.Get(base + "/share/hosts")
	if err != nil {
		t.Fatal(err)
	}
	var hl struct {
		Phone string `json:"phone"`
		Hosts []Host `json:"hosts"`
	}
	_ = json.NewDecoder(r.Body).Decode(&hl)
	r.Body.Close()
	if hl.Phone != "Test iPhone" || len(hl.Hosts) != 1 || hl.Hosts[0].ID != "mac1" || hl.Hosts[0].URL != "" {
		t.Fatalf("unexpected /share/hosts: %+v", hl)
	}

	// 3. Proxy strips /h/mac1 and drops same-origin Origin header.
	req, _ := http.NewRequest("GET", base+"/h/mac1/api/help", nil)
	req.Header.Set("Origin", base)
	r, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusOK || gotPath != "/api/help" || gotOrigin != "" {
		t.Fatalf("proxy HTTP: status=%d path=%q origin=%q", r.StatusCode, gotPath, gotOrigin)
	}

	// Different Origin is refused.
	req, _ = http.NewRequest("GET", base+"/h/mac1/api/help", nil)
	req.Header.Set("Origin", "http://evil.example")
	r, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin: got %d, want 403", r.StatusCode)
	}

	// 4. WebSocket upgrade works over /h/mac1/api/control.
	wsURL := "ws://" + srv.Addr() + "/h/mac1/api/control"
	ws, resp, err := websocket.Dial(t.Context(), wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {base}}})
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(t.Context(), websocket.MessageText, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	_, msg, err := ws.Read(t.Context())
	ws.CloseNow()
	if err != nil || string(msg) != "echo:hi" {
		t.Fatalf("ws echo = %q err=%v", msg, err)
	}

	// 5. Revoke removes admission.
	srv.Revoke("127.0.0.1")
	r, err = http.Get(base + "/share/hosts")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Fatalf("after revoke: got %d, want 403", r.StatusCode)
	}

	srv.Stop()
	if srv.Alive() {
		t.Fatal("expected Alive() == false after Stop")
	}
}

// TestShareProxySendsLANToken adds the phone's LAN token to every request
// it forwards, HTTP and WebSocket alike, in place of any the browser
// sends, and never lets the browser see it.
func TestShareProxySendsLANToken(t *testing.T) {
	const token = "test-token-0123456789abcdefghijklmnopqrstuv"
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<h1>GoRex</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var auths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.URL.Path+" "+r.Header.Get("Authorization"))
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "no token", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/api/control" {
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			defer c.CloseNow()
			mt, msg, err := c.Read(r.Context())
			if err == nil {
				_ = c.Write(r.Context(), mt, append([]byte("echo:"), msg...))
			}
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	srv, err := Start(Config{Addr: "127.0.0.1:0", WebDir: webDir, PhoneName: "Test iPhone"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	hostsJSON, _ := json.Marshal([]Host{
		{ID: "lan", Name: "Desk", URL: upstream.URL, Transport: "system", Token: token},
	})
	if err := srv.SetHosts(string(hostsJSON)); err != nil {
		t.Fatal(err)
	}
	srv.Admit("127.0.0.1")
	base := "http://" + srv.Addr()

	get := func(path string, header ...string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var hdr strings.Builder
		for k, v := range resp.Header {
			hdr.WriteString(k + ": " + strings.Join(v, ",") + "\n")
		}
		return resp.StatusCode, hdr.String() + string(b)
	}

	if code, body := get("/h/lan/api/help"); code != 200 {
		t.Fatalf("proxied with the phone's token: %d %s", code, body)
	}
	// A token the browser sends is replaced by the phone's.
	if code, _ := get("/h/lan/api/help", "Authorization", "Bearer browser-token"); code != 200 {
		t.Errorf("a browser's own Authorization reached the host: %d", code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/h/lan/api/control", nil)
	if err != nil {
		t.Fatalf("WebSocket through the proxy: %v", err)
	}
	defer ws.CloseNow()
	if err := ws.Write(ctx, websocket.MessageText, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if _, msg, err := ws.Read(ctx); err != nil || string(msg) != "echo:hi" {
		t.Fatalf("WebSocket echo: %q, %v", msg, err)
	}

	mu.Lock()
	for _, a := range auths {
		if !strings.HasSuffix(a, " Bearer "+token) {
			t.Errorf("a forwarded request without the phone's token: %q", a)
		}
	}
	mu.Unlock()
	for _, p := range []string{"/", "/share/hosts", "/share/state", "/h/lan/api/help"} {
		if _, body := get(p); strings.Contains(body, token) {
			t.Errorf("%s shows the token to the browser: %s", p, body)
		}
	}
}
