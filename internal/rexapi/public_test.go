package rexapi

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"gorex/internal/rex"
)

func publicFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f := startFixture(t)
	p, err := newPublicAccess(f.g, filepath.Join(rex.Dir(), "public-test"))
	if err != nil {
		t.Fatal(err)
	}
	f.g.public = p
	p.apply(PublicConfig{Enabled: true, URL: "https://terminal.example", Listen: "127.0.0.1:0"})
	if p.listener == nil {
		t.Fatalf("no public listener: %+v", p.status)
	}
	return f, "http://" + p.listener.Addr().String()
}

func pairedDevice(t *testing.T, p *publicAccess) (publicDevice, string) {
	t.Helper()
	code, _, err := p.pairCode()
	if err != nil {
		t.Fatal(err)
	}
	d, token, err := p.redeem(code, "test device")
	if err != nil {
		t.Fatal(err)
	}
	return d, token
}

func publicDial(t *testing.T, base, path, token, protocol string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, base+path, &websocket.DialOptions{
		Subprotocols: []string{protocol}, HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	return ws
}

func TestPublicConfig(t *testing.T) {
	good := PublicConfig{Enabled: true, URL: "https://terminal.example", Listen: "127.0.0.1:7298"}
	if err := good.check(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*PublicConfig){
		func(c *PublicConfig) { c.URL = "http://terminal.example" },
		func(c *PublicConfig) { c.URL = "https://user:password@terminal.example" },
		func(c *PublicConfig) { c.URL = "https://terminal.example/path" },
		func(c *PublicConfig) { c.URL = "https://terminal.example?token=secret" },
		func(c *PublicConfig) { c.Listen = "0.0.0.0:7298" },
		func(c *PublicConfig) { c.Listen = "localhost:7298" },
		func(c *PublicConfig) { c.Cert = "cert.pem" },
	} {
		bad := good
		change(&bad)
		if err := bad.check(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	dir := t.TempDir()
	if Wanted(dir) {
		t.Fatal("gateway wanted before configuration")
	}
	if err := SavePublic(dir, good); err != nil {
		t.Fatal(err)
	}
	if !Wanted(dir) {
		t.Fatal("public access did not start gateway supervision")
	}
	good.URL = "https://TERMINAL.example:443/"
	if err := SavePublic(dir, good); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPublic(dir)
	if err != nil || loaded.URL != "https://terminal.example" {
		t.Fatalf("origin not normalized: %q, %v", loaded.URL, err)
	}
}

func TestPublicPairing(t *testing.T) {
	f, base := publicFixture(t)
	p := f.g.public
	code, _, err := p.pairCode()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"code": code, "name": "phone"})
	r, _ := http.NewRequest(http.MethodPost, base+"/api/access/pair", bytes.NewReader(body))
	r.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("cross-origin pair: %d", resp.StatusCode)
	}
	r, _ = http.NewRequest(http.MethodPost, base+"/api/access/pair", bytes.NewReader(body))
	r.Header.Set("Origin", "https://terminal.example")
	resp, err = http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var answer struct {
		ID    string `json:"device_id"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || answer.ID == "" || answer.Token == "" {
		t.Fatalf("pair status %d", resp.StatusCode)
	}
	cookies := resp.Cookies()
	if len(cookies) != 1 || cookies[0].Name != deviceCookie || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("pairing did not set a secure HttpOnly SameSite cookie")
	}
	if _, _, err := p.redeem(code, "replay"); err == nil {
		t.Fatal("pairing code replay succeeded")
	}
	state, err := os.ReadFile(filepath.Join(p.dir, "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(state, []byte(answer.Token)) || bytes.Contains(state, []byte(code)) {
		t.Fatal("plaintext credential was saved")
	}
	info, _ := os.Stat(filepath.Join(p.dir, "devices.json"))
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("credential file permissions %o", info.Mode().Perm())
	}
	loaded, err := newPublicAccess(f.g, p.dir)
	if err != nil || !loaded.validDevice(answer.ID) {
		t.Fatalf("credential not persistent: %v", err)
	}
	for _, path := range []string{"/api/help", "/api/control", "/api/data", "/api/upload", "/api/spaces"} {
		r, _ := http.NewRequest(http.MethodGet, base+path, nil)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Errorf("anonymous %s: %d", path, resp.StatusCode)
		}
	}
	for _, cookie := range []bool{false, true} {
		r, _ := http.NewRequest(http.MethodGet, base+"/api/help", nil)
		if cookie {
			r.AddCookie(cookies[0])
		} else {
			r.Header.Set("Authorization", "Bearer "+answer.Token)
		}
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("authenticated request: %d", resp.StatusCode)
		}
	}
	r, _ = http.NewRequest(http.MethodGet, base+"/api/access/devices", nil)
	r.Header.Set("Authorization", "Bearer "+answer.Token)
	resp, err = http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("remote device administration allowed")
	}
}

func TestPublicPairCodeSingleUseAndExpiry(t *testing.T) {
	p, err := newPublicAccess(nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := p.pairCode()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _, err := p.redeem(code, "phone"); results <- err }()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("%d redemptions of one code", success)
	}
	code, _, _ = p.pairCode()
	p.state.Pairings[0].Expires = time.Now().Add(-time.Second)
	if _, _, err := p.redeem(code, "expired"); err == nil {
		t.Fatal("expired code worked")
	}
}

func TestPublicTokensBoundToDeviceAndRevocation(t *testing.T) {
	f, base := publicFixture(t)
	session, block, sid := inputSession(t, f)
	device, token := pairedDevice(t, f.g.public)
	_, otherToken := pairedDevice(t, f.g.public)
	c := newTestConn(t, publicDial(t, base, "/api/control", token, controlProtocol))
	c.hello(apiRevision)
	callChecked(t, c, "session.attach", map[string]any{"session_id": session})
	ticket := dataTicket(t, c, session, block)
	other := publicDial(t, base, "/api/data", otherToken, terminalProtocol)
	wsWrite(t, other, websocket.MessageBinary, append([]byte{0x01}, ticket...))
	_, rejection, err := wsRead(t, other)
	if err != nil || !bytes.Equal(rejection, []byte{0x82, rejectTicket}) {
		t.Fatalf("another device used ticket: %x, %v", rejection, err)
	}
	ws := publicDial(t, base, "/api/data", token, terminalProtocol)
	streamToken, _ := dataHello(t, ws, dataTicket(t, c, session, block))
	readData(t, ws) // snapshot
	other = publicDial(t, base, "/api/data", otherToken, terminalProtocol)
	resume := append([]byte{0x02}, streamToken...)
	resume = binary.LittleEndian.AppendUint64(resume, 0)
	wsWrite(t, other, websocket.MessageBinary, resume)
	_, rejection, err = wsRead(t, other)
	if err != nil || !bytes.Equal(rejection, []byte{0x82, rejectTicket}) {
		t.Fatalf("another device resumed stream: %x, %v", rejection, err)
	}
	if err := f.g.public.revoke(device.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := wsRead(t, ws); err == nil {
		t.Fatal("revoked data stream stayed open")
	}
	closed := false
	deadline := time.After(5 * time.Second)
	for !closed {
		select {
		case _, ok := <-c.msgs:
			closed = !ok
		case <-deadline:
			t.Fatal("revoked control stayed open")
		}
	}
	// Revocation ends access, not the shell. It can still accept input locally.
	if err := f.desk.Input(sid, typedText("echo after-revoke")); err != nil {
		t.Fatalf("revocation ended session: %v", err)
	}
	r, _ := http.NewRequest(http.MethodGet, base+"/api/help", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("revoked credential still admitted")
	}
	// A different device still reaches the same running terminal.
	c2 := newTestConn(t, publicDial(t, base, "/api/control", otherToken, controlProtocol))
	c2.hello(apiRevision)
	callChecked(t, c2, "session.attach", map[string]any{"session_id": session})
	w2 := publicDial(t, base, "/api/data", otherToken, terminalProtocol)
	dataHello(t, w2, dataTicket(t, c2, session, block))
	if !bytes.HasPrefix(readData(t, w2), []byte("GHOSTSNP")) {
		t.Fatal("original session did not reattach")
	}
}

func TestPublicControlResumeIdentity(t *testing.T) {
	f, base := publicFixture(t)
	_, token := pairedDevice(t, f.g.public)
	_, otherToken := pairedDevice(t, f.g.public)
	c := newTestConn(t, publicDial(t, base, "/api/control", token, controlProtocol))
	c.hello(apiRevision)
	callChecked(t, c, "server.whoami", nil)
	callChecked(t, c, "client.list", nil)
	callChecked(t, c, "session.attach_all", map[string]any{"event_scope": "all"})
	client := c.welcome["client"].(map[string]any)
	id, reconnect := client["id"].(string), client["reconnect_token"].(string)
	c.ws.CloseNow()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.g.mu.Lock()
		rec := f.g.clients[id]
		f.g.mu.Unlock()
		if rec == nil {
			t.Fatal("client state was not retained for reconnect")
		}
		rec.mu.Lock()
		lost := rec.conn == nil
		rec.mu.Unlock()
		if lost {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control did not disconnect")
		}
		time.Sleep(time.Millisecond)
	}
	other := newTestConn(t, publicDial(t, base, "/api/control", otherToken, controlProtocol))
	other.send(map[string]any{"type": "hello", "api_revision": apiRevision, "reconnect_token": reconnect})
	answer := other.next()
	if answer["client"].(map[string]any)["resumed"] == true {
		t.Fatal("another device resumed control")
	}
	back := newTestConn(t, publicDial(t, base, "/api/control", token, controlProtocol))
	back.send(map[string]any{"type": "hello", "api_revision": apiRevision, "reconnect_token": reconnect})
	answer = back.next()
	if answer["client"].(map[string]any)["id"] != id || answer["client"].(map[string]any)["resumed"] != true {
		t.Fatal("own device could not resume control")
	}
}

func TestPublicTLSAndLocalAdministration(t *testing.T) {
	f, _ := publicFixture(t)
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	cert := seed.TLS.Certificates[0]
	seed.Close()
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0600)
	os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600)
	p := f.g.public
	p.apply(PublicConfig{Enabled: true, URL: "https://terminal.example", Listen: "127.0.0.1:0", Cert: certPath, Key: keyPath})
	if p.listener == nil {
		t.Fatal(p.status.Error)
	}
	_, token := pairedDevice(t, p)
	r, _ := http.NewRequest(http.MethodGet, "https://"+p.listener.Addr().String()+"/api/help", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	resp, err := seed.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.TLS == nil {
		t.Fatal("TLS listener did not authenticate")
	}
	admin := httptest.NewServer(f.g.HandlerFor(transportUnix))
	defer admin.Close()
	resp, err = http.Get(admin.URL + "/api/access/devices")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || bytes.Contains(b, []byte(token)) || bytes.Contains(b, []byte("hash")) {
		t.Fatal("device listing leaked credential or failed")
	}
	// Stopping the listener also closes every hijacked connection and keeps credentials.
	p.apply(PublicConfig{})
	if !p.validDevice(p.state.Devices[0].ID) {
		t.Fatal("turning off listener removed device authorization")
	}
}

func TestPublicCLIConfiguration(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	quiet(t)
	if code := publicMain([]string{"on", "--url", "https://terminal.example"}); code != 0 {
		t.Fatalf("on: %d", code)
	}
	if !Wanted(TailnetDir()) {
		t.Fatal("gateway not enabled")
	}
	if code := publicMain([]string{"on", "--url", "https://terminal.example", "--listen", "0.0.0.0:7298"}); code != 2 {
		t.Fatal("plaintext public interface allowed")
	}
	if code := publicMain([]string{"on", "--url", "https://terminal.example", "--cert", "cert.pem"}); code != 2 {
		t.Fatal("TLS certificate without private key allowed")
	}
	if code := publicMain([]string{"off"}); code != 0 {
		t.Fatalf("off: %d", code)
	}
	if Wanted(TailnetDir()) {
		t.Fatal("gateway still wanted after off")
	}
}

// The actual gateway reads configuration, serves local-only administration,
// and retains device authorization when the gateway restarts.
func TestPublicGatewayLifecycle(t *testing.T) {
	startFixture(t)
	t.Setenv("GOREX_REXAPI_SOCKET", shortSocket(t))
	dir := t.TempDir()
	conf := PublicConfig{Enabled: true, URL: "https://terminal.example", Listen: "127.0.0.1:0"}
	if err := SavePublic(dir, conf); err != nil {
		t.Fatal(err)
	}
	var token, deviceID string
	for iteration := range 2 {
		func() {
			c, err := rex.ConnectSocketKeepalive(rex.SocketPath())
			if err != nil {
				t.Fatal(err)
			}
			os.Remove(filepath.Join(dir, "public-status.json"))
			stop, done := make(chan struct{}), make(chan error, 1)
			go func() { done <- run(c, runOptions{socket: SocketPath(), dir: dir, stop: stop}) }()
			defer func() {
				close(stop)
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			var status LANStatus
			waitFor(t, "public gateway listener", func() bool {
				b, _ := os.ReadFile(filepath.Join(dir, "public-status.json"))
				status = LANStatus{}
				json.Unmarshal(b, &status)
				return status.Listening != "" && gatewayRuns()
			})
			base := "http://" + status.Listening
			if iteration == 0 {
				raw, err := callPublicAdmin(http.MethodPost, "/api/access/pairing", nil)
				if err != nil {
					t.Fatal(err)
				}
				var pair struct {
					Code string `json:"code"`
				}
				json.Unmarshal(raw, &pair)
				body, _ := json.Marshal(map[string]string{"code": pair.Code, "name": "phone"})
				resp, err := http.Post(base+"/api/access/pair", "application/json", bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				var answer struct {
					Token string `json:"token"`
					ID    string `json:"device_id"`
				}
				err = json.NewDecoder(resp.Body).Decode(&answer)
				resp.Body.Close()
				if err != nil || resp.StatusCode != 200 {
					t.Fatalf("pair: %d, %v", resp.StatusCode, err)
				}
				token, deviceID = answer.Token, answer.ID
			}
			r, _ := http.NewRequest(http.MethodGet, base+"/api/help", nil)
			r.Header.Set("Authorization", "Bearer "+token)
			resp, err := http.DefaultClient.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("authorization lost after restart: %d", resp.StatusCode)
			}
			if iteration == 1 {
				if _, err := callPublicAdmin(http.MethodPost, "/api/access/revoke", map[string]string{"id": deviceID}); err != nil {
					t.Fatal(err)
				}
				resp, err := http.DefaultClient.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != 401 {
					t.Fatal("revocation through local socket failed")
				}
				conf.Enabled = false
				if err := SavePublic(dir, conf); err != nil {
					t.Fatal(err)
				}
				waitFor(t, "public listener off", func() bool {
					b, _ := os.ReadFile(filepath.Join(dir, "public-status.json"))
					status = LANStatus{}
					json.Unmarshal(b, &status)
					return status.Listening == ""
				})
			}
		}()
	}
}
