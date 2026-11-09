package rexapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestWebAssetsAndAccess(t *testing.T) {
	f, base := publicFixture(t)
	_, token := pairedDevice(t, f.g.public)
	for _, path := range []string{"/", "/web/app.js", "/web/core.js", "/web/app.css", "/web/vendor/xterm.js", "/web/vendor/addon-fit.js", "/web/vendor/addon-unicode11.js"} {
		response, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || len(body) == 0 {
			t.Fatalf("asset %s: %d", path, response.StatusCode)
		}
		if response.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("asset missing nosniff")
		}
		if path == "/" && (!bytes.Contains(body, []byte("pair-form")) || !strings.Contains(response.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'")) {
			t.Fatal("login shell missing or not protected")
		}
	}
	for _, path := range []string{"/api/web/session", "/api/control", "/api/data", "/web/../api/help"} {
		response, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatalf("anonymous API %s: %d", path, response.StatusCode)
		}
	}
	for _, path := range []string{"/web/package.json", "/web/core.test.js", "/web/vendor/"} {
		request, _ := http.NewRequest("GET", base+path, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatalf("development file exposed at %s: %d", path, response.StatusCode)
		}
	}
}

func TestWebLogoutRevokesOnlyThisBrowser(t *testing.T) {
	f, base := publicFixture(t)
	device, token := pairedDevice(t, f.g.public)
	other, _ := pairedDevice(t, f.g.public)
	ws := publicDial(t, base, "/api/control", token, controlProtocol)
	c := newTestConn(t, ws)
	c.hello(apiRevision)
	request, _ := http.NewRequest("POST", base+"/api/access/logout", strings.NewReader("{}"))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Origin", "https://terminal.example")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("logout: %d", response.StatusCode)
	}
	if cookies := response.Cookies(); len(cookies) != 1 || cookies[0].Name != deviceCookie || cookies[0].MaxAge != -1 {
		t.Fatal("logout did not clear cookie")
	}
	if f.g.public.validDevice(device.ID) || !f.g.public.validDevice(other.ID) {
		t.Fatal("logout revoked the wrong devices")
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-c.msgs:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("logged-out control remained connected")
		}
	}
}

func vtTicket(t *testing.T, c *testConn, space, block string) []byte {
	t.Helper()
	result := result(t, c.request("vt-ticket", "data.connect", map[string]any{"session_id": space, "block_id": block, "data_ref_id": "terminal", "subprotocol": terminalVTProtocol}))
	// The browser's HELLO uses the ticket's raw 16 bytes.
	raw, err := base64.RawURLEncoding.DecodeString(result["ticket"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func readVTUntil(t *testing.T, ws *websocket.Conn, rendered *uint64, needle string) []byte {
	t.Helper()
	var out []byte
	for !bytes.Contains(out, []byte(needle)) {
		data := readData(t, ws)
		out = append(out, data...)
		*rendered += uint64(len(data))
		ack := binary.LittleEndian.AppendUint64(nil, *rendered)
		wsWrite(t, ws, websocket.MessageBinary, ack)
		if len(out) > 16<<20 {
			t.Fatal("stream did not reach expected output")
		}
	}
	return out
}

func TestWebVTStreamReattachesAndResumes(t *testing.T) {
	f, base := publicFixture(t)
	space, block, sid := inputSession(t, f)
	_, token := pairedDevice(t, f.g.public)
	c := newTestConn(t, publicDial(t, base, "/api/control", token, controlProtocol))
	c.hello(apiRevision)
	callChecked(t, c, "session.attach", map[string]any{"session_id": space})
	if err := f.desk.Input(sid, typedText("echo BEFORE_BROWSER")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "preexisting terminal output", func() bool { return strings.Contains(screenText(t, c, space, block), "BEFORE_BROWSER") })
	ws := publicDial(t, base, "/api/data", token, terminalVTProtocol)
	ws.SetReadLimit(maxMessage)
	streamToken, _ := dataHello(t, ws, vtTicket(t, c, space, block))
	var received uint64
	snapshot := readVTUntil(t, ws, &received, `"type":"resize"`)
	if bytes.Contains(snapshot, []byte("GHOSTSNP")) || !bytes.Contains(snapshot, []byte(`"type":"snapshot"`)) || !bytes.Contains(snapshot, []byte("BEFORE_BROWSER")) {
		t.Fatal("browser did not receive a VT snapshot of the existing session")
	}
	ws.CloseNow()
	s := f.g.streamFor(string(streamToken))
	if s == nil {
		t.Fatal("stream not retained for resume")
	}
	waitFor(t, "dropped browser stream", func() bool { return !s.active() })
	if err := f.desk.Input(sid, typedText("echo OFFLINE_OUTPUT")); err != nil {
		t.Fatal(err)
	}
	back := publicDial(t, base, "/api/data", token, terminalVTProtocol)
	back.SetReadLimit(maxMessage)
	hello := append([]byte{2}, streamToken...)
	hello = binary.LittleEndian.AppendUint64(hello, received)
	wsWrite(t, back, websocket.MessageBinary, hello)
	welcome := readData(t, back)
	if len(welcome) != 32 || welcome[0] != 0x81 {
		t.Fatal("VT resume was rejected")
	}
	var connectionReceived uint64
	pending := readVTUntil(t, back, &connectionReceived, "OFFLINE_OUTPUT")
	if bytes.Contains(pending, []byte(`"type":"snapshot"`)) {
		t.Fatal("resume replayed the snapshot instead of continuing output")
	}
	// Refreshing creates a new browser client and restores the same process.
	c2 := newTestConn(t, publicDial(t, base, "/api/control", token, controlProtocol))
	c2.hello(apiRevision)
	callChecked(t, c2, "session.attach", map[string]any{"session_id": space})
	fresh := publicDial(t, base, "/api/data", token, terminalVTProtocol)
	fresh.SetReadLimit(maxMessage)
	dataHello(t, fresh, vtTicket(t, c2, space, block))
	connectionReceived = 0
	restored := readVTUntil(t, fresh, &connectionReceived, `"type":"resize"`)
	if !bytes.Contains(restored, []byte("BEFORE_BROWSER")) || !bytes.Contains(restored, []byte("OFFLINE_OUTPUT")) {
		t.Fatal("fresh browser lost the existing terminal history")
	}
}

func TestRenderWindowBoundsAndCancellation(t *testing.T) {
	f := newRenderFlow()
	if err := f.reserve(context.Background(), renderWindow); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := f.reserve(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unacknowledged data exceeded window: %v", err)
	}
	if f.ack(renderWindow + 1) {
		t.Fatal("accepted acknowledgement for unsent data")
	}
	if !f.ack(65536) {
		t.Fatal("render acknowledgement rejected")
	}
	if err := f.reserve(context.Background(), 65536); err != nil {
		t.Fatal(err)
	}
	if f.ack(1) {
		t.Fatal("accepted a backwards acknowledgement")
	}
	f.close()
	if err := f.reserve(context.Background(), 1); err == nil {
		t.Fatal("closed flow accepted output")
	}
}
