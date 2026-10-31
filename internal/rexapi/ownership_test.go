package rexapi

import (
	"bytes"
	"runtime"
	"testing"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// gotEvent is an event of a session, as a connection receives it.
type gotEvent struct {
	Session string
	Event   map[string]any
}

// nextSessionEvent reads until an event of a session arrives, failing
// after a while.
func (c *testConn) nextSessionEvent() gotEvent {
	c.t.Helper()
	for {
		m := c.next()
		if m["type"] != "event" {
			continue
		}
		payload := m["payload"].(map[string]any)
		if payload["type"] != "session_event" {
			continue
		}
		body := payload["payload"].(map[string]any)
		ev, _ := body["event"].(map[string]any)
		if ev == nil {
			continue
		}
		return gotEvent{Session: body["session_id"].(string), Event: ev}
	}
}

// waitEvent waits for a session event of a type, and returns its payload.
// It reads the connection in order, so a response must not be waited for
// on the same connection meanwhile.
func waitEvent(t *testing.T, c *testConn, typ string) map[string]any {
	t.Helper()
	for {
		ev := c.nextSessionEvent()
		if ev.Event["type"] != typ {
			continue
		}
		if p, ok := ev.Event["payload"].(map[string]any); ok {
			return p
		}
		return map[string]any{}
	}
}

// ownerOfBlock returns the owner session.list_blocks reports for a block.
func ownerOfBlock(t *testing.T, c *testConn, sid, block string) string {
	t.Helper()
	res := callChecked(t, c, "session.list_blocks", map[string]any{"session_id": sid})
	for _, raw := range res["blocks"].([]any) {
		b := raw.(map[string]any)
		if b["block_id"] == block {
			if owner, ok := b["owner"].(string); ok {
				return owner
			}
			return ""
		}
	}
	t.Fatalf("no block %s", block)
	return ""
}

// sessionInfoOf returns the server's record of a session.
func sessionInfoOf(t *testing.T, f *fixture, sid string) rex.SessionInfo {
	t.Helper()
	list, err := f.g.opt.Client.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range list {
		if info.ID == sid {
			return info
		}
	}
	t.Fatalf("no session %s", sid)
	return rex.SessionInfo{}
}

// TestOwnership: owning a block takes its size, resizing takes effect at
// once, and giving it up passes it to the next client in line.
func TestOwnership(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	f := startFixture(t)
	c, sid, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-c", "sleep 60"}, Cols: 80, Rows: 24})
	goSID := f.firstSID(t, sid)
	// The block's stream, for the resize message.
	ticket := dataTicket(t, c, sid, block)
	ws := f.dial("/api/data", terminalProtocol)
	ws.SetReadLimit(maxMessage)
	_, nonce := dataHello(t, ws, ticket)
	// A connection that hears of every event.
	obs := f.dialControl()
	obs.hello(apiRevision)
	callChecked(t, obs, "session.attach_all", map[string]any{"event_scope": "all"})

	c1 := welcomeID(t, c.welcome)
	res := callChecked(t, c, "session.own_block", map[string]any{
		"session_id": sid, "block_ids": []string{block},
	})
	claim := res["blocks"].([]any)[0].(map[string]any)
	if claim["owner"] != c1 {
		t.Fatalf("owner: %v", claim)
	}
	if info := sessionInfoOf(t, f, goSID); info.SizeLock != c1 {
		t.Fatalf("size lock %q, want %q", info.SizeLock, c1)
	}
	if ev := waitEvent(t, obs, "block_owner_changed"); ev["owner"] != c1 || ev["reason"] != "take" {
		t.Errorf("owner event: %v", ev)
	}
	if owner := ownerOfBlock(t, c, sid, block); owner != c1 {
		t.Errorf("view owner %q", owner)
	}

	// A size applies at once to the owner.
	blockCall(t, c, sid, block, "resize", map[string]any{"columns": 100, "rows": 30})
	waitFor(t, "the new size", func() bool {
		info := sessionInfoOf(t, f, goSID)
		return info.Cols == 100 && info.Rows == 30
	})
	size := blockCall(t, c, sid, block, "size", nil)
	if size["columns"] != float64(100) || size["rows"] != float64(30) || size["owner"] != c1 {
		t.Errorf("size: %v", size)
	}
	desired := size["desired"].([]any)
	if len(desired) != 1 || desired[0].(map[string]any)["client_id"] != c1 ||
		desired[0].(map[string]any)["columns"] != float64(100) {
		t.Errorf("desired: %v", desired)
	}
	// The stream gets a resize message with the owner.
	got := readUntil(t, ws, `"cols":100`)
	marker := `"owner":"` + c1 + `"`
	if !bytes.Contains(got, []byte(marker)) {
		t.Errorf("no owner in the resize message: %q", got[len(got)-200:])
	}
	_ = nonce

	// A second client asks for the block, and its size waits.
	c2conn := f.dialControl()
	c2conn.hello(apiRevision)
	c2 := welcomeID(t, c2conn.welcome)
	callChecked(t, c2conn, "session.attach", map[string]any{"session_id": sid})
	res = callChecked(t, c2conn, "session.own_block", map[string]any{
		"session_id": sid, "block_ids": []string{block}, "mode": "request",
	})
	if claim := res["blocks"].([]any)[0].(map[string]any); claim["owner"] != c1 {
		t.Fatalf("a request took the block: %v", claim)
	}
	blockCall(t, c2conn, sid, block, "resize", map[string]any{"columns": 120, "rows": 40})
	time.Sleep(100 * time.Millisecond)
	if info := sessionInfoOf(t, f, goSID); info.Cols != 100 || info.Rows != 30 {
		t.Errorf("a request-mode size applied: %dx%d", info.Cols, info.Rows)
	}

	// Giving it up passes it to the client in line, with its size.
	res = callChecked(t, c, "session.disown_block", map[string]any{
		"session_id": sid, "block_ids": []string{block},
	})
	if claim := res["blocks"].([]any)[0].(map[string]any); claim["owner"] != c2 {
		t.Fatalf("owner after disown: %v", claim)
	}
	if ev := waitEvent(t, obs, "block_owner_changed"); ev["owner"] != c2 || ev["previous"] != c1 || ev["reason"] != "disown" {
		t.Errorf("disown event: %v", ev)
	}
	waitFor(t, "the queued size", func() bool {
		info := sessionInfoOf(t, f, goSID)
		return info.Cols == 120 && info.Rows == 40
	})
	// Detaching gives it up too.
	callChecked(t, c2conn, "session.detach", map[string]any{"session_id": sid})
	waitFor(t, "the release", func() bool {
		return sessionInfoOf(t, f, goSID).SizeLock == ""
	})
	if owner := ownerOfBlock(t, c, sid, block); owner != "" {
		t.Errorf("owner after detach: %q", owner)
	}
}

// TestOwnershipReconnect: a client gets its block back when it reconnects
// within the grace.
func TestOwnershipReconnect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	f := startFixture(t)
	c, sid, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-c", "sleep 60"}})
	goSID := f.firstSID(t, sid)
	w := c.welcome
	client := welcomeID(t, w)
	token := welcomeToken(t, w)
	callChecked(t, c, "session.own_block", map[string]any{"session_id": sid, "block_ids": []string{block}})
	if owner := ownerOfBlock(t, c, sid, block); owner != client {
		t.Fatalf("owner %q", owner)
	}
	// The connection drops without a close frame: the client stays, waiting.
	c.ws.CloseNow()
	waitFor(t, "the released block", func() bool {
		if info := sessionInfoOf(t, f, goSID); info.SizeLock != "" {
			return false
		}
		return true
	})
	// A new connection continues the client, and gets the block back.
	c2 := f.dialControl()
	c2.send(map[string]any{
		"type": "hello", "api_revision": apiRevision, "reconnect_token": token,
		"client": map[string]any{"kind": "app", "platform": "ios", "name": "Rex.app"},
	})
	welcome := c2.nextType("welcome")
	if welcome["client"].(map[string]any)["id"] != client {
		t.Fatalf("resumed as %v", welcome["client"])
	}
	waitFor(t, "the restored block", func() bool {
		return ownerOfBlock(t, c2, sid, block) == client
	})
	if info := sessionInfoOf(t, f, goSID); info.SizeLock != client {
		t.Errorf("size lock %q after restore", info.SizeLock)
	}
}

// firstSID returns the GoRex session of a block, from the gateway's model.
func (f *fixture) firstSID(t *testing.T, sid string) string {
	t.Helper()
	m, err := f.g.current()
	if err != nil {
		t.Fatal(err)
	}
	s := m.byID[sid]
	if s == nil || len(s.blocks) == 0 {
		t.Fatalf("no block in %s", sid)
	}
	return s.blocks[0].sid
}

// TestSizeWithoutOwner: a size applies at once while no client owns the
// block.
func TestSizeWithoutOwner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	f := startFixture(t)
	c, sid, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-c", "sleep 60"}, Cols: 80, Rows: 24})
	goSID := f.firstSID(t, sid)
	blockCall(t, c, sid, block, "resize", map[string]any{"columns": 90, "rows": 20})
	waitFor(t, "the new size", func() bool {
		info := sessionInfoOf(t, f, goSID)
		return info.Cols == 90 && info.Rows == 20
	})
	size := blockCall(t, c, sid, block, "size", nil)
	if size["owner"] != nil {
		t.Errorf("owner: %v", size["owner"])
	}
	if e := blockFailure(t, c, sid, block, "resize", map[string]any{"columns": 0, "rows": 10}); e["code"] != "bad_request" {
		t.Errorf("zero columns: %v", e)
	}
}

// TestSessionListClients: the clients attached to a session, as the
// session reports them.
func TestSessionListClients(t *testing.T) {
	f := startFixture(t)
	f.setLayout(layout.Store{Sessions: []layout.Workspace{{ID: "w", Name: "work",
		Layout: layout.Layout{Tabs: []layout.Tab{{Name: "t", Root: pane("s1")}}}}}})
	c := f.dialControl()
	c.hello(apiRevision)
	sid := sessionID("w")
	callChecked(t, c, "session.attach", map[string]any{"session_id": sid})
	res := callChecked(t, c, "session.list_clients", map[string]any{"session_id": sid})
	clients := res["clients"].([]any)
	if len(clients) != 1 {
		t.Fatalf("clients: %v", clients)
	}
	client := clients[0].(map[string]any)
	if client["client_id"] == "" || client["principal"] == nil || client["connected_at"] == "" {
		t.Errorf("client: %v", client)
	}
}
