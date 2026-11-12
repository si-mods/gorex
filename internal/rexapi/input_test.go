package rexapi

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// sessionWith starts a GoRex session of the opts, makes it the only pane
// of a fresh layout, and returns a connection attached to it.
func (f *fixture) sessionWith(t *testing.T, o rex.CreateOptions) (*testConn, string, string) {
	t.Helper()
	info, err := f.desk.Create(o)
	if err != nil {
		t.Fatal(err)
	}
	store := layout.Store{Sessions: []layout.Workspace{{
		ID: "w", Name: "work",
		Layout: layout.Layout{Active: 0, Tabs: []layout.Tab{{
			Name: "tab", Root: &layout.Node{SID: info.ID},
		}}},
	}}}
	f.setLayout(store)
	c := f.dialControl()
	c.hello(apiRevision)
	sid := sessionID("w")
	callChecked(t, c, "session.attach", map[string]any{"session_id": sid})
	return c, sid, blockID(info.ID)
}

// keyPayload is a key event of client.key.
func keyPayload(name, text, mods string, codepoint int) map[string]any {
	return map[string]any{
		"key": name, "mods": mods, "consumed_mods": "",
		"text": text, "unshifted_codepoint": codepoint,
		"composing": false, "action": "press",
	}
}

// typeText sends text to a block one key at a time, as a keyboard does.
func typeText(t *testing.T, c *testConn, sid, block, text string) {
	t.Helper()
	for _, r := range text {
		var name string
		switch {
		case r >= 'a' && r <= 'z':
			name = "key_" + string(r)
		case r >= '0' && r <= '9':
			name = "digit_" + string(r)
		case r == ' ':
			name = "space"
		case r == '-':
			name = "minus"
		default:
			t.Fatalf("no key for %q", r)
		}
		key := keyPayload(name, string(r), "", int(r))
		res := result(t, c.request("k"+name+string(r)+block, "client.key", map[string]any{
			"session_id": sid, "block_id": block, "key": key,
		}))
		if res["handled"] != false {
			t.Fatalf("handled: %v", res)
		}
	}
}

// screenText returns a block's screen as plain text.
func screenText(t *testing.T, c *testConn, sid, block string) string {
	t.Helper()
	res := result(t, c.request("fmt", "com.superlogical.terminal.format", map[string]any{
		"session_id": sid, "block_id": block, "args": map[string]any{},
	}))
	return res["content"].(string)
}

// TestClientKeyTypes: keys sent to a block reach its program in order.
func TestClientKeyTypes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's shell is a Unix one")
	}
	f := startFixture(t)
	c, sid, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-i"}})
	typeText(t, c, sid, block, "echo hi")
	res := result(t, c.request("enter", "client.key", map[string]any{
		"session_id": sid, "block_id": block, "key": keyPayload("enter", "", "", 0),
	}))
	if res["handled"] != false {
		t.Fatalf("handled: %v", res)
	}
	waitFor(t, "the shell's output", func() bool {
		return strings.Contains(screenText(t, c, sid, block), "hi")
	})
}

// TestClientKeyFocusesAndValidates: a key without a block goes to the
// session's focused block, and bad arguments are bad_request.
func TestClientKeyFocusesAndValidates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's shell is a Unix one")
	}
	f := startFixture(t)
	c, sid, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-i"}})
	callChecked(t, c, "client.key", map[string]any{
		"session_id": sid, "key": keyPayload("key_x", "x", "", 'x'),
	})
	// A key that no Lua binding takes still succeeds when the session has
	// no focused block.
	f2 := startFixture(t)
	f2.setLayout(layout.Store{Sessions: []layout.Workspace{{ID: "e", Name: "empty"}}})
	c2 := f2.dialControl()
	c2.hello(apiRevision)
	callChecked(t, c2, "session.attach", map[string]any{"session_id": sessionID("e")})
	res := result(t, c2.request("nofocus", "client.key", map[string]any{
		"session_id": sessionID("e"), "key": keyPayload("key_a", "a", "", 'a'),
	}))
	if res["handled"] != false {
		t.Errorf("handled: %v", res)
	}
	for _, tc := range []struct {
		payload map[string]any
		code    string
	}{
		{map[string]any{"session_id": sid}, "bad_request"},
		{map[string]any{"session_id": sid, "key": map[string]any{"key": "key_a"}}, "bad_request"},
		{map[string]any{"session_id": sid, "key": keyPayload("key_a", "a", "hyper", 'a')}, "bad_request"},
		{map[string]any{"session_id": sid, "key": keyPayload("key_a", "a", "", 'a'), "prefix": []string{" "}}, "bad_request"},
		{map[string]any{"session_id": sid, "block_id": "block:nope", "key": keyPayload("key_a", "a", "", 'a')}, "not_found"},
		{map[string]any{"session_id": "session:nope", "key": keyPayload("key_a", "a", "", 'a')}, "session_not_found"},
	} {
		e := failure(t, c.request("bad"+tc.code, "client.key", tc.payload))
		if e["code"] != tc.code {
			t.Errorf("%v: %v, want %s", tc.payload, e, tc.code)
		}
	}
	// A connection that did not attach cannot type.
	other := f.dialControl()
	other.hello(apiRevision)
	if e := failure(t, other.request("x", "client.key", map[string]any{
		"session_id": sid, "key": keyPayload("key_a", "a", "", 'a'),
	})); e["code"] != "conflict" {
		t.Errorf("unattached key: %v", e)
	}
	_ = block
}

// rawSession starts a raw program that prints the modes the test sends
// keys for, and echoes them back with escapes visible.
func rawSession(t *testing.T, f *fixture) (*testConn, string, string) {
	t.Helper()
	script := `printf '\033[?1h\033[?1000h\033[?1006h\033[?1004h\033[?2004h'; stty raw -echo; exec cat -v`
	return f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-c", script}})
}

// TestBlockEventInput: the block's input events are encoded as the
// program's modes ask.
func TestBlockEventInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	f := startFixture(t)
	c, sid, block := rawSession(t, f)
	waitFor(t, "the program's modes", func() bool {
		st := result(t, c.request("m", "com.superlogical.terminal.modes", map[string]any{
			"session_id": sid, "block_id": block, "args": map[string]any{},
		}))
		on := map[string]bool{}
		for _, raw := range st["modes"].([]any) {
			m := raw.(map[string]any)
			on[m["name"].(string)] = m["set"].(bool)
		}
		return on["application_cursor_keys"] && on["bracketed_paste"] && on["focus_event"] && on["mouse_click"]
	})
	send := func(event map[string]any) {
		result(t, c.request("ev", "block.event", map[string]any{
			"session_id": sid, "block_id": block, "event": event,
		}))
	}
	// Application cursor keys: the up arrow is ESC O A.
	send(map[string]any{"type": "key", "payload": keyPayload("arrow_up", "", "", 0)})
	waitFor(t, "the up arrow", func() bool {
		return strings.Contains(screenText(t, c, sid, block), "^[OA")
	})
	// A bracketed paste.
	send(map[string]any{"type": "paste", "payload": map[string]any{"text": "foo"}})
	waitFor(t, "the paste", func() bool {
		return strings.Contains(screenText(t, c, sid, block), "^[[200~foo^[[201~")
	})
	// A mouse press in cell 2,4, report 1-based.
	send(map[string]any{"type": "mouse", "payload": map[string]any{
		"action": "press", "button": "left", "mods": "", "column": 2, "row": 4,
	}})
	waitFor(t, "the mouse report", func() bool {
		return strings.Contains(screenText(t, c, sid, block), "^[[<0;3;5M")
	})
	// The window's focus.
	send(map[string]any{"type": "focus", "payload": map[string]any{"action": "in"}})
	waitFor(t, "the focus report", func() bool {
		return strings.Contains(screenText(t, c, sid, block), "^[[I")
	})
	// The wheel.
	send(map[string]any{"type": "wheel", "payload": map[string]any{
		"direction": "up", "mods": "", "column": 2, "row": 4,
	}})
	waitFor(t, "the wheel report", func() bool {
		return strings.Contains(screenText(t, c, sid, block), "^[[<64;3;5M")
	})
	// Server-only event types are turned away.
	for _, typ := range []string{"title_changed", "bell", "child_exited", ""} {
		e := failure(t, c.request("bad"+typ, "block.event", map[string]any{
			"session_id": sid, "block_id": block,
			"event": map[string]any{"type": typ},
		}))
		if e["code"] != "bad_request" {
			t.Errorf("event %q: %v", typ, e)
		}
	}
}

// TestTerminalWrite types text through the block method.
func TestTerminalWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's shell is a Unix one")
	}
	f := startFixture(t)
	c, sid, block := rawSession(t, f)
	res := result(t, c.request("w", "com.superlogical.terminal.write", map[string]any{
		"session_id": sid, "block_id": block, "args": map[string]any{"data": "héllo\n"},
	}))
	if res["bytes_written"] != float64(len("héllo\n")) {
		t.Errorf("bytes_written: %v", res)
	}
	// GNU cat -v shows the é's bytes as M-CM-); BSD cat, in a UTF-8
	// locale, as é.
	waitFor(t, "the typed text", func() bool {
		s := screenText(t, c, sid, block)
		return strings.Contains(s, "héllo") || strings.Contains(s, "hM-CM-)llo")
	})
	// block.method does the same by a short name.
	res = result(t, c.request("w2", "block.method", map[string]any{
		"session_id": sid, "block_id": block, "method": "write",
		"args": map[string]any{"data": "bye"},
	}))
	if res["bytes_written"] != float64(3) {
		t.Errorf("block.method bytes_written: %v", res)
	}
}

// TestKeysArriveInOrder: keys sent in a burst without waiting arrive in
// the order they were sent.
func TestKeysArriveInOrder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's shell is a Unix one")
	}
	f := startFixture(t)
	c, sid, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-i"}})
	for i, r := range "order" {
		c.send(map[string]any{
			"type": "request", "id": "o" + string(rune('0'+i)), "method": "client.key",
			"payload": map[string]any{
				"session_id": sid, "block_id": block,
				"key": keyPayload("key_"+string(r), string(r), "", int(r)),
			},
		})
	}
	// The responses come in order too, as ordered methods run one at a
	// time; the last one says the keys were typed.
	for i := 0; i < 5; i++ {
		m := c.nextType("response")
		if m["error"] != nil {
			t.Fatalf("response %d: %v", i, m)
		}
	}
	c.send(map[string]any{
		"type": "request", "id": "ret", "method": "client.key",
		"payload": map[string]any{
			"session_id": sid, "block_id": block, "key": keyPayload("enter", "", "", 0),
		},
	})
	c.nextType("response")
	waitFor(t, "the typed text", func() bool {
		return strings.Contains(screenText(t, c, sid, block), "order")
	})
}

// rawArgs turns a map into a method's arguments.
func rawArgs(t *testing.T, m map[string]any) args {
	t.Helper()
	a := args{}
	for k, v := range m {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		a[k] = raw
	}
	return a
}

// TestWheelDeltas: a revision 11 wheel's deltas become the discrete
// wheel events a program's mouse modes know, and a direction still
// makes the single event it always did.
func TestWheelDeltas(t *testing.T) {
	t.Run("lines", func(t *testing.T) {
		ms, aerr := wheelInput(rawArgs(t, map[string]any{
			"delta_mode": "line", "delta_y": -2, "delta_x": 1, "column": 3, "row": 4,
		}))
		if aerr != nil {
			t.Fatal(aerr)
		}
		if len(ms) != 3 {
			t.Fatalf("%d events, want 3: %v", len(ms), ms)
		}
		for i, m := range ms[:2] {
			if m.Button != "wheel_up" || m.Col != 3 || m.Row != 4 {
				t.Errorf("event %d: %+v", i, m)
			}
		}
		if ms[2].Button != "wheel_right" {
			t.Errorf("delta_x event: %+v", ms[2])
		}
	})
	t.Run("pixels and pages", func(t *testing.T) {
		ms, _ := wheelInput(rawArgs(t, map[string]any{"delta_mode": "pixel", "delta_y": 40}))
		if len(ms) != 1 || ms[0].Button != "wheel_down" {
			t.Errorf("40 pixels: %v", ms)
		}
		ms, _ = wheelInput(rawArgs(t, map[string]any{"delta_mode": "page", "delta_y": 0.5}))
		if len(ms) != 5 || ms[0].Button != "wheel_down" {
			t.Errorf("half a page: %d events", len(ms))
		}
		ms, _ = wheelInput(rawArgs(t, map[string]any{"delta_mode": "line", "delta_y": 0}))
		if len(ms) != 0 {
			t.Errorf("no delta: %v", ms)
		}
	})
	t.Run("a bad mode and unknown fields fail", func(t *testing.T) {
		if _, aerr := wheelInput(rawArgs(t, map[string]any{"delta_mode": "furlong", "delta_y": 1})); aerr == nil {
			t.Error("furlong")
		}
		if _, aerr := wheelInput(rawArgs(t, map[string]any{"direction": "up", "pixel": map[string]any{"x": -1}})); aerr == nil {
			t.Error("a negative pixel")
		}
	})
	t.Run("a huge delta is capped", func(t *testing.T) {
		ms, aerr := wheelInput(rawArgs(t, map[string]any{"delta_mode": "line", "delta_y": 1e9}))
		if aerr != nil {
			t.Fatal(aerr)
		}
		if len(ms) != maxWheelEvents {
			t.Errorf("%d events from delta_y 1e9, want %d", len(ms), maxWheelEvents)
		}
	})
	t.Run("a direction is one event", func(t *testing.T) {
		ms, aerr := wheelInput(rawArgs(t, map[string]any{"direction": "up", "column": 1, "row": 2}))
		if aerr != nil || len(ms) != 1 || ms[0].Button != "up" {
			t.Errorf("direction: %v %v", ms, aerr)
		}
	})
}

// TestMousePixelButtons: revision 11's pixel position and held buttons
// are accepted, and the first held button becomes the event's button.
func TestMousePixelButtons(t *testing.T) {
	m, aerr := mouseInput(rawArgs(t, map[string]any{
		"action": "motion", "buttons": []string{"left"}, "column": 5, "row": 6,
		"pixel": map[string]any{"x": 40, "y": 80},
	}))
	if aerr != nil {
		t.Fatal(aerr)
	}
	if m.Action != "motion" || m.Button != "left" || m.Col != 5 || m.Row != 6 {
		t.Errorf("event: %+v", m)
	}
	if _, aerr := mouseInput(rawArgs(t, map[string]any{
		"action": "motion", "buttons": []string{"wheel_up"},
	})); aerr == nil {
		t.Error("a wheel button among the held buttons")
	}
	if _, aerr := mouseInput(rawArgs(t, map[string]any{
		"action": "press", "pixel": map[string]any{"x": 1, "z": 2},
	})); aerr == nil {
		t.Error("a third axis in the pixel position")
	}
}
