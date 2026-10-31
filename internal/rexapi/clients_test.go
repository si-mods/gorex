package rexapi

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// serverEvent waits for a server event of a type and returns its payload.
func serverEvent(t *testing.T, c *testConn, typ string) map[string]any {
	t.Helper()
	for {
		m := c.next()
		if m["type"] != "event" {
			continue
		}
		payload := m["payload"].(map[string]any)
		if payload["type"] != typ {
			continue
		}
		if body, ok := payload["payload"].(map[string]any); ok {
			return body
		}
		return map[string]any{}
	}
}

// TestClientLabel: a client names itself, and the server tells the
// clients that hear of everything.
func TestClientLabel(t *testing.T) {
	f := startFixture(t)
	f.setLayout(layout.Store{Sessions: []layout.Workspace{{ID: "w", Name: "work",
		Layout: layout.Layout{Tabs: []layout.Tab{{Name: "t", Root: pane("s1")}}}}}})
	c := f.dialControl()
	c.hello(apiRevision)
	client := welcomeID(t, c.welcome)
	obs := f.dialControl()
	obs.hello(apiRevision)
	callChecked(t, obs, "session.attach_all", map[string]any{"event_scope": "all"})

	callChecked(t, c, "client.set_label", map[string]any{"label": "  studio  "})
	ev := serverEvent(t, obs, "client_changed")
	info := ev["client"].(map[string]any)["info"].(map[string]any)
	if info["label"] != "studio" {
		t.Errorf("label: %v", info)
	}
	res := callChecked(t, c, "client.list", nil)
	label := ""
	for _, raw := range res["clients"].([]any) {
		cl := raw.(map[string]any)
		if cl["client_id"] == client {
			label = cl["info"].(map[string]any)["label"].(string)
		}
	}
	if label != "studio" {
		t.Errorf("client.list label %q", label)
	}
	// Labels are bounded and belong to the caller.
	if e := failure(t, c.request("long", "client.set_label", map[string]any{"label": strings.Repeat("a", 65)})); e["code"] != "bad_request" {
		t.Errorf("long label: %v", e)
	}
	if e := failure(t, c.request("id", "client.set_label", map[string]any{"label": "x", "client_id": client})); e["code"] != "bad_request" {
		t.Errorf("client_id: %v", e)
	}
	// An empty label removes it.
	callChecked(t, c, "client.set_label", map[string]any{"label": "   "})
	if ev := serverEvent(t, obs, "client_changed"); ev["client"].(map[string]any)["info"] == nil {
		t.Errorf("label not removed: %v", ev)
	}
}

// TestClientKeymapAndScripts: GoRex has no configuration file nor Lua,
// and says so.
func TestClientKeymapAndScripts(t *testing.T) {
	f := startFixture(t)
	c := f.dialControl()
	c.hello(apiRevision)
	status := callChecked(t, c, "server.status", nil)
	keymap := callChecked(t, c, "client.keymap", nil)
	config := keymap["config"].(map[string]any)
	if config["generation"] != status["config"].(map[string]any)["generation"] {
		t.Errorf("generation: %v, want %v", config["generation"], status["config"])
	}
	if config["instance_id"] != status["instance_id"] || config["path"] == "" || config["exists"] != false {
		t.Errorf("config: %v", config)
	}
	if len(keymap["bindings"].([]any)) != 0 || keymap["diagnostics"] == nil {
		t.Errorf("keymap: %v", keymap)
	}
	// An empty list of scripts unloads what there was.
	callChecked(t, c, "client.set_script", map[string]any{"scripts": []any{}})
	// GoRex runs no Lua.
	if e := failure(t, c.request("lua", "client.set_script", map[string]any{
		"scripts": []any{map[string]any{"name": "init.lua", "source": "return 1"}},
	})); e["code"] != "not_supported" {
		t.Errorf("scripts: %v", e)
	}
	if e := failure(t, c.request("sess", "client.set_script", map[string]any{
		"scripts": []any{}, "session_id": "x",
	})); e["code"] != "bad_request" {
		t.Errorf("scripts with a session: %v", e)
	}
}

// TestClientAction: a block's action is its method.
func TestClientAction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's shell is a Unix one")
	}
	f := startFixture(t)
	c, sid, block := rawSession(t, f)
	res := callChecked(t, c, "client.action", map[string]any{
		"action": creatorName + ".write", "session_id": sid, "block_id": block,
		"args": map[string]any{"data": "hello"},
	})
	written := res["result"].(map[string]any)["bytes_written"]
	if written != float64(5) || len(res["client_actions"].([]any)) != 0 {
		t.Errorf("action: %v", res)
	}
	waitFor(t, "the typed text", func() bool {
		return strings.Contains(screenText(t, c, sid, block), "hello")
	})
	// Names GoRex does not know, and the origin only rex.invoke may send.
	if e := failure(t, c.request("nope", "client.action", map[string]any{"action": "pane.split"})); e["code"] != "not_found" {
		t.Errorf("pane.split: %v", e)
	}
	if e := failure(t, c.request("script", "client.action", map[string]any{
		"action": creatorName + ".write", "origin": "script",
	})); e["code"] != "bad_request" {
		t.Errorf("origin script: %v", e)
	}
	if e := failure(t, c.request("server", "client.action", map[string]any{
		"action": creatorName + ".write", "server": "other",
	})); e["code"] != "not_supported" {
		t.Errorf("server: %v", e)
	}
}

// TestClientPerformAndReply: GoRex's clients don't perform each other's
// actions, and nothing waits for a reply.
func TestClientPerformAndReply(t *testing.T) {
	f := startFixture(t)
	c := f.dialControl()
	c.hello(apiRevision)
	client := welcomeID(t, c.welcome)
	c2 := f.dialControl()
	c2.hello(apiRevision)
	if e := failure(t, c.request("missing", "client.perform", map[string]any{
		"client_id": "client:01a191c5-96af-7b1f-b589-a7e9a714fc66", "action": "x",
	})); e["code"] != "not_found" {
		t.Errorf("unknown client: %v", e)
	}
	if e := failure(t, c.request("known", "client.perform", map[string]any{
		"client_id": client, "action": "x",
	})); e["code"] != "not_supported" {
		t.Errorf("perform: %v", e)
	}
	if e := failure(t, c.request("neg", "client.perform", map[string]any{
		"client_id": client, "action": "x", "timeout_ms": -1,
	})); e["code"] != "bad_request" {
		t.Errorf("negative timeout: %v", e)
	}
	if e := failure(t, c.request("reply", "client.reply", map[string]any{
		"call_id": "nope", "result": nil,
	})); e["code"] != "not_found" {
		t.Errorf("reply: %v", e)
	}
	if e := failure(t, c.request("both", "client.reply", map[string]any{
		"call_id": "nope", "result": 1, "error": map[string]any{"code": "x", "message": "y"},
	})); e["code"] != "bad_request" {
		t.Errorf("result and error: %v", e)
	}
	_ = c2
}

// TestConfigReload: a reload counts the generation and tells every client
// to fetch its keymap again.
func TestConfigReload(t *testing.T) {
	f := startFixture(t)
	c := f.dialControl()
	c.hello(apiRevision)
	obs := f.dialControl()
	obs.hello(apiRevision)
	callChecked(t, obs, "session.attach_all", map[string]any{"event_scope": "all"})
	before := callChecked(t, c, "server.status", nil)["config"].(map[string]any)["generation"].(float64)
	res := callChecked(t, c, "server.config_reload", nil)
	gen := res["generation"].(float64)
	if gen != before+1 {
		t.Errorf("generation %v, want %v", gen, before+1)
	}
	ev := serverEvent(t, obs, "keymap_changed")
	if ev["generation"] != gen {
		t.Errorf("keymap_changed: %v", ev)
	}
	keymap := callChecked(t, c, "client.keymap", nil)
	if keymap["config"].(map[string]any)["generation"] != gen {
		t.Errorf("keymap generation: %v", keymap["config"])
	}
}

// TestHosts: GoRex keeps no host list.
func TestHosts(t *testing.T) {
	f := startFixture(t)
	c := f.dialControl()
	c.hello(apiRevision)
	res := callChecked(t, c, "hosts.list", nil)
	if len(res["hosts"].([]any)) != 0 || res["hash"] == "" {
		t.Errorf("hosts: %v", res)
	}
	again := callChecked(t, c, "hosts.list", nil)
	if again["hash"] != res["hash"] {
		t.Error("the hash of the empty list changed")
	}
	if e := failure(t, c.request("put", "hosts.put", map[string]any{
		"host": map[string]any{"label": "studio", "endpoints": []string{"studio.ts.net"}},
	})); e["code"] != "not_supported" {
		t.Errorf("hosts.put: %v", e)
	}
	if e := failure(t, c.request("rm", "hosts.remove", map[string]any{"id": "host:x"})); e["code"] != "not_supported" {
		t.Errorf("hosts.remove: %v", e)
	}
}

// TestBlockCreatorList: the terminal creator with its methods and events.
func TestBlockCreatorList(t *testing.T) {
	f := startFixture(t)
	f.setLayout(layout.Store{Sessions: []layout.Workspace{{ID: "w", Name: "work",
		Layout: layout.Layout{Tabs: []layout.Tab{{Name: "t", Root: pane("s1")}}}}}})
	c := f.dialControl()
	c.hello(apiRevision)
	res := callChecked(t, c, "block_creator.list", nil)
	creators := res["creators"].([]any)
	if len(creators) != 1 {
		t.Fatalf("creators: %v", creators)
	}
	creator := creators[0].(map[string]any)
	if creator["name"] != creatorName || creator["host_id"] == "" {
		t.Errorf("creator: %v", creator)
	}
	flavor := creator["flavors"].([]any)[0].(map[string]any)
	if flavor["flavor"] != flavorShell || flavor["name"] != "shell" {
		t.Errorf("flavor: %v", flavor)
	}
	api := creator["api"].(map[string]any)
	methods := api["methods"].(map[string]any)
	for _, name := range []string{"write", "format", "resize", "size", "modes", "title"} {
		if _, ok := methods[name]; !ok {
			t.Errorf("no method %s in %v", name, methods)
		}
	}
	events := api["events"].(map[string]any)
	for _, name := range []string{"bell", "title_changed", "program_status_changed", "child_exited"} {
		if _, ok := events[name]; !ok {
			t.Errorf("no event %s in %v", name, events)
		}
	}
}

// TestBlockEvents: the events a terminal sends reach the connections that
// hear of them.
func TestBlockEvents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	f := startFixture(t)
	script := `sleep 1; printf '\033]0;Event Title\007'; printf '\a'; printf '\033]7501;state=working:app=cargo\033\\'; sleep 60`
	c, sid, _ := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-c", script}})
	obs := f.dialControl()
	obs.hello(apiRevision)
	callChecked(t, obs, "session.attach_all", map[string]any{"event_scope": "all"})
	seen := map[string]bool{}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && len(seen) < 3 {
		ev := waitBlockEvent(t, obs)
		seen[ev["name"].(string)] = true
		if ev["name"] == "title_changed" {
			if ev["payload"].(map[string]any)["title"] != "Event Title" {
				t.Errorf("title payload: %v", ev["payload"])
			}
		}
	}
	for _, name := range []string{"title_changed", "bell", "program_status_changed"} {
		if !seen[name] {
			t.Errorf("no %s event: %v", name, seen)
		}
	}
	_ = sid
	_ = c
}

// TestBlockChildExited: a program that ends sends child_exited.
func TestBlockChildExited(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	f := startFixture(t)
	_, _, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-c", "sleep 1; exit 3"}})
	obs := f.dialControl()
	obs.hello(apiRevision)
	callChecked(t, obs, "session.attach_all", map[string]any{"event_scope": "all"})
	ev := waitBlockEventNamed(t, obs, "child_exited")
	if ev["block_id"] != block {
		t.Errorf("child_exited block: %v", ev)
	}
	if payload, ok := ev["payload"].(map[string]any); ok {
		if payload["exit_code"] != float64(3) {
			t.Errorf("exit code: %v", payload)
		}
	}
}

// waitBlockEvent reads sessions' events until a block's own arrives.
func waitBlockEvent(t *testing.T, c *testConn) map[string]any {
	t.Helper()
	for {
		ev := c.nextSessionEvent()
		if ev.Event["type"] != "block_event" {
			continue
		}
		return ev.Event["payload"].(map[string]any)
	}
}

// waitBlockEventNamed reads until a block event of a name arrives.
func waitBlockEventNamed(t *testing.T, c *testConn, name string) map[string]any {
	t.Helper()
	for {
		ev := waitBlockEvent(t, c)
		if ev["name"] == name {
			return ev
		}
	}
}

// TestClientEvents: a client that hears of everything sees the others
// come, go, and lose their connection.
func TestClientEvents(t *testing.T) {
	old := reconnectWait
	reconnectWait = 300 * time.Millisecond
	f := startFixture(t)
	// The gateway took its own copy as it was made.
	reconnectWait = old
	f.setLayout(layout.Store{Sessions: []layout.Workspace{{ID: "w", Name: "work",
		Layout: layout.Layout{Tabs: []layout.Tab{{Name: "t", Root: pane("s1")}}}}}})
	obs := f.dialControl()
	obs.hello(apiRevision)
	callChecked(t, obs, "session.attach_all", map[string]any{"event_scope": "all"})

	c2 := f.dialControl()
	c2.hello(apiRevision)
	id := welcomeID(t, c2.welcome)
	joined := serverEvent(t, obs, "client_joined")
	if joined["client"].(map[string]any)["client_id"] != id {
		t.Errorf("client_joined: %v", joined)
	}
	callChecked(t, c2, "session.attach", map[string]any{"session_id": sessionID("w")})
	// A connection that drops is told of, then the client leaves once it
	// does not come back.
	c2.ws.CloseNow()
	if ev := waitEvent(t, obs, "client_connection_changed"); ev["client_id"] != id || ev["connection_state"] != "lost" {
		t.Errorf("connection changed: %v", ev)
	}
	if ev := serverEvent(t, obs, "client_left"); ev["client_id"] != id {
		t.Errorf("client_left: %v", ev)
	}
}
