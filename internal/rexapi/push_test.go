package rexapi

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"

	"gorex/internal/rex"
)

// pushRelay records the pushes a test's relay gets.
type pushRelay struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []map[string]any
}

// startPushRelay serves a fake push relay, and points the gateways the
// test starts at it.
func startPushRelay(t *testing.T) *pushRelay {
	f := &pushRelay{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("a push that is not JSON: %s", body)
			return
		}
		req["sig"] = r.Header.Get("X-Gorex-Sig")
		f.mu.Lock()
		f.reqs = append(f.reqs, req)
		f.mu.Unlock()
		w.Write([]byte(`{"sent":1}`))
	}))
	t.Cleanup(f.srv.Close)
	old := pushRelayURL
	pushRelayURL = f.srv.URL
	t.Cleanup(func() { pushRelayURL = old })
	return f
}

// pushes returns the requests the relay got.
func (f *pushRelay) pushes() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.reqs...)
}

// pushDevice is a device key for tests to register.
const pushDevice = "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo="

// TestGorexPush checks gorex.push.register and gorex.push.unregister:
// the welcome lists gorex.push, register answers the host's key and the
// relay, a status change of the block pushes to the relay, unregister
// and the connection's end stop them — the end with a last update,
// connected false.
func TestGorexPush(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	relay := startPushRelay(t)
	msg := base64.StdEncoding.EncodeToString([]byte("needs input"))
	// Reports that keep coming each change the signature, so one lands
	// whenever the registration is in.
	script := "while true; do printf '\\033]7501;state=blocked:kind=permission:app=claude:msg=" + msg + "\\033\\\\'; sleep 0.4; done"
	f := startFixture(t)
	c, sid, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-c", script}})
	if features, _ := c.welcome["features"].([]any); !slices.Contains(features, any(featurePush)) {
		t.Fatalf("the welcome's features %v lack %s", features, featurePush)
	}

	r := callChecked(t, c, "gorex.push.register", map[string]any{
		"device": pushDevice, "session_id": sid, "block_id": block, "live_activities": true,
	})
	if r["relay"] != relay.srv.URL {
		t.Errorf("relay: %v, want %s", r["relay"], relay.srv.URL)
	}
	host, err := base64.StdEncoding.DecodeString(r["host"].(string))
	if err != nil || len(host) == 0 {
		t.Errorf("host: %v", r["host"])
	}

	// The blocked record pushes at once, at priority 10.
	var req map[string]any
	waitFor(t, "the relay's push", func() bool {
		if reqs := relay.pushes(); len(reqs) > 0 {
			req = reqs[len(reqs)-1]
			return true
		}
		return false
	})
	if req["device"] != pushDevice || req["event"] != "update" || req["priority"] != float64(10) || req["sig"] == "" {
		t.Errorf("push: %v", req)
	}
	cs, _ := req["content_state"].(map[string]any)
	rec, _ := cs["record"].(map[string]any)
	if cs["connected"] != true || cs["line"] != "needs input" || cs["title"] != "tab" || cs["program"] != "claude" ||
		rec == nil || rec["state"] != "blocked" || rec["kind"] != "permission" {
		t.Errorf("content_state: %v", cs)
	}
	if stale, _ := req["stale_date"].(float64); stale <= 0 {
		t.Errorf("stale_date: %v", req["stale_date"])
	}

	// Unregister stops the pushes.
	callChecked(t, c, "gorex.push.unregister", nil)

	if schemaFor(t, "gorex.push.register") == nil {
		t.Error("gorex.push.register has no result schema")
	}
	res, err := httpGet(f.srv.URL + "/llms.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if body, _ := io.ReadAll(res.Body); !strings.Contains(string(body), "### gorex.push.register") {
		t.Error("/llms.txt does not describe gorex.push.register")
	}
}

// TestGorexPushArgs checks register's argument errors by their codes.
func TestGorexPushArgs(t *testing.T) {
	f := startFixture(t)
	c := f.dialControl()
	c.hello(apiRevision)
	for _, tc := range []struct {
		payload map[string]any
		code    string
	}{
		{map[string]any{}, "bad_request"},
		{map[string]any{"device": 3}, "bad_request"},
		{map[string]any{"device": "!!! not base64 !!!"}, "bad_request"},
		{map[string]any{"device": pushDevice, "session_id": "session:nope"}, "bad_request"},
		{map[string]any{"device": pushDevice, "session_id": "session:nope", "block_id": "block:x"}, "session_not_found"},
	} {
		if e := failure(t, c.request("bad", "gorex.push.register", tc.payload)); e["code"] != tc.code {
			t.Errorf("%v: %v, want %s", tc.payload, e, tc.code)
		}
	}
}

// TestGorexPushDetached: the end of a registered connection sends a last
// update, connected false, and drops the binding.
func TestGorexPushDetached(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	relay := startPushRelay(t)
	f := startFixture(t)
	c, sid, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-c", "sleep 60"}})
	callChecked(t, c, "gorex.push.register", map[string]any{
		"device": pushDevice, "session_id": sid, "block_id": block,
	})
	c.ws.Close(websocket.StatusNormalClosure, "bye")
	var req map[string]any
	waitFor(t, "the last push", func() bool {
		if reqs := relay.pushes(); len(reqs) > 0 {
			req = reqs[len(reqs)-1]
			return true
		}
		return false
	})
	cs, _ := req["content_state"].(map[string]any)
	if cs["connected"] != false || req["event"] != "update" || req["priority"] != float64(5) {
		t.Errorf("the last push: %v", req)
	}
}

// TestGorexPushOldServer: over a GoRex server that does not push, the
// welcome lacks gorex.push and the methods fail with not_supported.
func TestGorexPushOldServer(t *testing.T) {
	f := startFixture(t)
	f.g.hello.Capabilities = slices.DeleteFunc(slices.Clone(f.g.hello.Capabilities), func(s string) bool {
		return s == rex.CapabilityPush
	})
	c := f.dialControl()
	c.hello(apiRevision)
	if features, _ := c.welcome["features"].([]any); slices.Contains(features, any(featurePush)) {
		t.Errorf("the welcome lists %s without the capability: %v", featurePush, features)
	}
	if e := failure(t, c.request("old", "gorex.push.register", map[string]any{"device": pushDevice})); e["code"] != "not_supported" {
		t.Errorf("gorex.push.register on an old server: %v, want not_supported", e)
	}
	if e := failure(t, c.request("old", "gorex.push.unregister", nil)); e["code"] != "not_supported" {
		t.Errorf("gorex.push.unregister on an old server: %v, want not_supported", e)
	}
}
