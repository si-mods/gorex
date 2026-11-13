package share

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	ws, _, err := websocket.Dial(t.Context(), wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {base}}})
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
