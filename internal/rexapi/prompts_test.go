package rexapi

import (
	"strings"
	"testing"
	"time"
)

// promptClients starts a session, the client that runs the action, and a
// second app that may answer.
func promptClients(t *testing.T, f *fixture) (*testConn, *testConn, string) {
	t.Helper()
	f.setLayout(emptyStore())
	c := f.dialControl()
	c.hello(apiRevision)
	created := callChecked(t, c, "session.create", map[string]any{
		"label": "prompts",
		"initial_windows": []any{
			map[string]any{"window_label": "one", "layout": blockLayout()},
		},
	})
	sid := created["session_id"].(string)
	callChecked(t, c, "session.attach", map[string]any{"session_id": sid})
	other := f.dialControl()
	other.hello(apiRevision)
	// The welcome goes out before the gateway lists the connection, and a
	// question goes only to the connections it lists: wait for both.
	waitFor(t, "both connections in the gateway", func() bool {
		f.g.mu.Lock()
		defer f.g.mu.Unlock()
		return len(f.g.conns) == 2
	})
	return c, other, sid
}

// take returns the first message that match accepts: one that an earlier
// wait set aside, or else the next one from the connection that matches.
// It sets aside the messages it skips, because a later wait may need
// them. The responses to two requests on one connection arrive in either
// order: an action can end, and its response go out, before the response
// to the client.reply that answered its prompt.
func take(c *testConn, match func(map[string]any) bool) map[string]any {
	c.t.Helper()
	for i, m := range c.pending {
		if match(m) {
			c.pending = append(c.pending[:i:i], c.pending[i+1:]...)
			return m
		}
	}
	for {
		m := c.read()
		if match(m) {
			return m
		}
		c.pending = append(c.pending, m)
	}
}

// readServerEvent waits for a server event of a type and returns its
// payload.
func readServerEvent(t *testing.T, c *testConn, typ string) map[string]any {
	t.Helper()
	m := take(c, func(m map[string]any) bool {
		body, _ := m["payload"].(map[string]any)
		return m["type"] == "event" && body["type"] == typ
	})
	return m["payload"].(map[string]any)["payload"].(map[string]any)
}

// readResponse waits for the response with an ID.
func readResponse(t *testing.T, c *testConn, id string) map[string]any {
	t.Helper()
	return take(c, func(m map[string]any) bool {
		return m["type"] == "response" && m["id"] == id
	})
}

// TestReadResponseKeepsOthers: the action's response can arrive before
// the response to the client.reply that answered its prompt (#1133), and
// both waits still find theirs.
func TestReadResponseKeepsOthers(t *testing.T) {
	c := &testConn{t: t, msgs: make(chan map[string]any, 2)}
	c.msgs <- map[string]any{"type": "response", "id": "run"}
	c.msgs <- map[string]any{"type": "response", "id": "reply"}
	if m := readResponse(t, c, "reply"); m["id"] != "reply" {
		t.Fatalf("reply = %v", m)
	}
	if m := readResponse(t, c, "run"); m["id"] != "run" {
		t.Fatalf("run = %v", m)
	}
}

// TestPromptInputFromAnotherApp: the first answer wins, the other app
// closes, and the action uses the answer.
func TestPromptInputFromAnotherApp(t *testing.T) {
	writeConfig(t, `
gorex.action{name = "rename", run = function(ctx, args)
	local name = gorex.ui.input{title = "Rename"}
	if name then gorex.invoke("tab.rename", {label = name}) end
end}
`)
	f := startFixture(t)
	c, other, sid := promptClients(t, f)
	c.send(map[string]any{"type": "request", "id": "run", "method": "client.action",
		"payload": map[string]any{"action": "rename", "session_id": sid}})
	mine := readServerEvent(t, c, "client_prompt_requested")
	theirs := readServerEvent(t, other, "client_prompt_requested")
	if mine["action"] != "rename" {
		t.Fatalf("event = %v", mine)
	}
	prompt, _ := theirs["prompt"].(map[string]any)
	if prompt["kind"] != "input" || prompt["title"] != "Rename" {
		t.Fatalf("prompt = %v", prompt)
	}
	// Another connection cannot answer this copy.
	other.send(map[string]any{"type": "request", "id": "wrong", "method": "client.reply",
		"payload": map[string]any{"call_id": mine["call_id"], "result": map[string]any{"text": "no"}}})
	if e := failure(t, readResponse(t, other, "wrong")); e["code"] != "not_found" {
		t.Fatalf("the wrong connection answered: %v", e)
	}
	other.send(map[string]any{"type": "request", "id": "reply", "method": "client.reply",
		"payload": map[string]any{"call_id": theirs["call_id"], "result": map[string]any{"text": "work"}}})
	result(t, readResponse(t, other, "reply"))
	ended := readServerEvent(t, c, "client_prompt_ended")
	if ended["reason"] != "answered" {
		t.Fatalf("ended = %v", ended)
	}
	result(t, readResponse(t, c, "run"))
	window := viewOf(t, c, sid)["windows"].([]any)[0].(map[string]any)
	if window["label"] != "work" {
		t.Fatalf("label = %v", window["label"])
	}
}

// TestPromptTimesOut: an unanswered prompt ends the action.
func TestPromptTimesOut(t *testing.T) {
	writeConfig(t, `
gorex.action{name = "ask", run = function(ctx, args) gorex.ui.input{title = "Name", timeout_ms = 100} end}
`)
	f := startFixture(t)
	c, _, sid := promptClients(t, f)
	c.send(map[string]any{"type": "request", "id": "run", "method": "client.action",
		"payload": map[string]any{"action": "ask", "session_id": sid}})
	readServerEvent(t, c, "client_prompt_requested")
	res := readResponse(t, c, "run")
	e := failure(t, res)
	if !strings.Contains(e["message"].(string), "timed out") {
		t.Fatalf("error = %v", e)
	}
}

// TestPromptNotSupported: a client that cannot show the prompt leaves,
// and the other one answers.
func TestPromptNotSupported(t *testing.T) {
	writeConfig(t, `
gorex.action{name = "ask", run = function(ctx, args) gorex.ui.input{title = "Name"} end}
`)
	f := startFixture(t)
	c, other, sid := promptClients(t, f)
	c.send(map[string]any{"type": "request", "id": "run", "method": "client.action",
		"payload": map[string]any{"action": "ask", "session_id": sid}})
	mine := readServerEvent(t, c, "client_prompt_requested")
	theirs := readServerEvent(t, other, "client_prompt_requested")
	other.send(map[string]any{"type": "request", "id": "reply", "method": "client.reply",
		"payload": map[string]any{"call_id": theirs["call_id"], "error": map[string]any{"code": "not_supported", "message": "no window"}}})
	result(t, readResponse(t, other, "reply"))
	c.send(map[string]any{"type": "request", "id": "reply", "method": "client.reply",
		"payload": map[string]any{"call_id": mine["call_id"], "result": map[string]any{"text": "ok"}}})
	result(t, readResponse(t, c, "reply"))
	result(t, readResponse(t, c, "run"))
}

// TestPromptChecksItems: an ID the prompt did not offer fails, and the
// prompt stays open for a correct answer.
func TestPromptChecksItems(t *testing.T) {
	writeConfig(t, `
gorex.action{name = "pick", run = function(ctx, args)
	gorex.ui.pick{title = "Project", items = {{id = "a", title = "A"}, {id = "b", title = "B"}}}
end}
`)
	f := startFixture(t)
	c, _, sid := promptClients(t, f)
	c.send(map[string]any{"type": "request", "id": "run", "method": "client.action",
		"payload": map[string]any{"action": "pick", "session_id": sid}})
	prompt := readServerEvent(t, c, "client_prompt_requested")
	callID := prompt["call_id"]
	c.send(map[string]any{"type": "request", "id": "bad", "method": "client.reply",
		"payload": map[string]any{"call_id": callID, "result": map[string]any{"ids": []any{"nope"}}}})
	if e := failure(t, readResponse(t, c, "bad")); e["code"] != "bad_request" {
		t.Fatalf("error = %v", e)
	}
	c.send(map[string]any{"type": "request", "id": "good", "method": "client.reply",
		"payload": map[string]any{"call_id": callID, "result": map[string]any{"ids": []any{"a"}}}})
	result(t, readResponse(t, c, "good"))
	result(t, readResponse(t, c, "run"))
}

// TestPromptTimeIsNotActionTime: an action's deadline does not run while
// its question is open.
func TestPromptTimeIsNotActionTime(t *testing.T) {
	writeConfig(t, `
gorex.action{name = "ask", run = function(ctx, args) gorex.ui.input{title = "Name", timeout_ms = 2000} end}
`)
	old := actionDeadline
	actionDeadline = 150 * time.Millisecond
	defer func() { actionDeadline = old }()
	f := startFixture(t)
	c, _, sid := promptClients(t, f)
	c.send(map[string]any{"type": "request", "id": "run", "method": "client.action",
		"payload": map[string]any{"action": "ask", "session_id": sid}})
	prompt := readServerEvent(t, c, "client_prompt_requested")
	time.Sleep(400 * time.Millisecond)
	c.send(map[string]any{"type": "request", "id": "reply", "method": "client.reply",
		"payload": map[string]any{"call_id": prompt["call_id"], "result": map[string]any{"text": "ok"}}})
	result(t, readResponse(t, c, "reply"))
	result(t, readResponse(t, c, "run"))
}

// TestPromptErrorIsShortened: a long refusal message reaches the action
// shortened.
func TestPromptErrorIsShortened(t *testing.T) {
	writeConfig(t, `
gorex.action{name = "ask", run = function(ctx, args) gorex.ui.input{title = "Name"} end}
`)
	f := startFixture(t)
	c, other, sid := promptClients(t, f)
	c.send(map[string]any{"type": "request", "id": "run", "method": "client.action",
		"payload": map[string]any{"action": "ask", "session_id": sid}})
	mine := readServerEvent(t, c, "client_prompt_requested")
	theirs := readServerEvent(t, other, "client_prompt_requested")
	// The second refusal is the one the action sees.
	other.send(map[string]any{"type": "request", "id": "first", "method": "client.reply",
		"payload": map[string]any{"call_id": theirs["call_id"], "error": map[string]any{"code": "not_supported", "message": "no"}}})
	result(t, readResponse(t, other, "first"))
	long := strings.Repeat("x", 5000)
	c.send(map[string]any{"type": "request", "id": "reply", "method": "client.reply",
		"payload": map[string]any{"call_id": mine["call_id"], "error": map[string]any{"code": "not_supported", "message": long}}})
	result(t, readResponse(t, c, "reply"))
	e := failure(t, readResponse(t, c, "run"))
	msg := e["message"].(string)
	longest, run := 0, 0
	for _, r := range msg {
		if r == 'x' {
			run++
			if run > longest {
				longest = run
			}
			continue
		}
		run = 0
	}
	if longest > 4096 || !strings.Contains(msg, "…") {
		t.Fatalf("the refusal kept %d bytes", longest)
	}
}
