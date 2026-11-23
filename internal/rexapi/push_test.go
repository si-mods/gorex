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
	script := "while true; do printf '\\033]7501;state=blocked:kind=permission:app=claude:id=watch:msg=" + msg + "\\033\\\\'; sleep 0.4; done"
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
	// The record's id goes with it, for gorex.push.seen to report.
	if rec["id"] != "watch" {
		t.Errorf("record id: %v", rec)
	}
	if stale, _ := req["stale_date"].(float64); stale <= 0 {
		t.Errorf("stale_date: %v", req["stale_date"])
	}
	// The register had live_activities on, so the blocked push alerts.
	if a, _ := req["alert"].(map[string]any); a["title"] != "claude needs input" || a["body"] != "needs input" {
		t.Errorf("alert: %v", req["alert"])
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
	if e := failure(t, c.request("old", "gorex.push.seen", map[string]any{
		"session_id": "session:x", "block_id": "block:x", "record_ids": []string{"r"},
	})); e["code"] != "not_supported" {
		t.Errorf("gorex.push.seen on an old server: %v, want not_supported", e)
	}
}

// TestGorexPushSeen checks gorex.push.seen: the records record_ids names
// are seen, as gorex.see_block marks them — the block's seen_at rises to
// their latest updated_at and a program_status_changed tells every
// client — and an id the block has no record of is skipped.
func TestGorexPushSeen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	// The root record, then build, then tests a second later, so their
	// updated_at differ.
	script := `printf '\033]7501;state=done\007'; printf '\033]7501;state=done:id=build\007'; sleep 1; ` +
		`printf '\033]7501;state=error:id=tests\007'; sleep 60`
	f := startFixture(t)
	c, sid, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-c", script}})
	var status map[string]any
	waitFor(t, "the records", func() bool {
		status = blockCall(t, c, sid, block, "program_status", nil)
		return len(status["records"].([]any)) == 3
	})
	updated := map[string]string{}
	for _, r := range status["records"].([]any) {
		m := r.(map[string]any)
		id, _ := m["id"].(string)
		updated[id] = m["updated_at"].(string)
	}
	for _, id := range []string{"", "build", "tests"} {
		if updated[id] == "" {
			t.Fatalf("no record %s in %v", id, status["records"])
		}
	}
	if _, ok := status["seen_at"]; ok {
		t.Fatalf("seen_at before any client saw the records: %v", status)
	}
	obs := f.dialControl()
	obs.hello(apiRevision)
	callChecked(t, obs, "session.attach_all", map[string]any{"event_scope": "all"})
	// The next program_status_changed whose payload carries seen_at; a
	// report's own event, which has none, may still be in flight.
	seenEvent := func() map[string]any {
		for {
			e := waitBlockEventNamed(t, obs, "program_status_changed")
			if p, _ := e["payload"].(map[string]any); p["seen_at"] != nil {
				return e
			}
		}
	}

	// Only the records named are seen; one the block has not is skipped.
	res := callChecked(t, c, "gorex.push.seen", map[string]any{
		"session_id": sid, "block_id": block, "record_ids": []string{"build", "gone"},
	})
	if len(res) != 0 {
		t.Errorf("result %v, want {}", res)
	}
	if ev := seenEvent(); ev["payload"].(map[string]any)["seen_at"] != updated["build"] || ev["block_id"] != block {
		t.Errorf("program_status_changed %v, want seen_at %s", ev, updated["build"])
	}
	if got := blockCall(t, c, sid, block, "program_status", nil)["seen_at"]; got != updated["build"] {
		t.Errorf("seen_at %v, want %s", got, updated["build"])
	}

	// The error record and the root — the empty id — follow.
	callChecked(t, c, "gorex.push.seen", map[string]any{
		"session_id": sid, "block_id": block, "record_ids": []string{"tests", ""},
	})
	if ev := seenEvent(); ev["payload"].(map[string]any)["seen_at"] != updated["tests"] {
		t.Errorf("program_status_changed %v, want seen_at %s", ev, updated["tests"])
	}

	// A call that names no record the block has changes nothing, and
	// still succeeds.
	callChecked(t, c, "gorex.push.seen", map[string]any{
		"session_id": sid, "block_id": block, "record_ids": []string{"gone"},
	})
	callChecked(t, c, "gorex.push.seen", map[string]any{
		"session_id": sid, "block_id": block, "record_ids": []string{},
	})

	for _, tc := range []struct {
		payload map[string]any
		code    string
	}{
		{map[string]any{"session_id": sid, "block_id": block}, "bad_request"},
		{map[string]any{"session_id": sid, "block_id": block, "record_ids": "build"}, "bad_request"},
		{map[string]any{"session_id": sid, "block_id": block, "record_ids": []string{"build"}, "extra": 1}, "bad_request"},
		{map[string]any{"session_id": sid, "block_id": "block:nope", "record_ids": []string{"build"}}, "not_found"},
	} {
		if e := failure(t, c.request("bad", "gorex.push.seen", tc.payload)); e["code"] != tc.code {
			t.Errorf("%v: %v, want %s", tc.payload, e, tc.code)
		}
	}

	if schemaFor(t, "gorex.push.seen") == nil {
		t.Error("gorex.push.seen has no result schema")
	}
	res2, err := httpGet(f.srv.URL + "/llms.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if body, _ := io.ReadAll(res2.Body); !strings.Contains(string(body), "### gorex.push.seen") {
		t.Error("/llms.txt does not describe gorex.push.seen")
	}
}
