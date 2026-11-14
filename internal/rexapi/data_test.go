package rexapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// dataSession creates a GoRex session with a command and a layout that
// shows it, and returns the session ID, block ID and GoRex SID.
func dataSession(t *testing.T, f *fixture, command []string) (string, string, string) {
	t.Helper()
	info, err := f.desk.Create(rex.CreateOptions{Command: command, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	f.setLayout(layout.Store{Sessions: []layout.Workspace{{
		ID: "data", Name: "data",
		Layout: layout.Layout{Tabs: []layout.Tab{{Name: "term", Root: pane(info.ID)}}},
	}}})
	return sessionID("data"), blockID(info.ID), info.ID
}

// inputSession starts an interactive shell, which the tests type into to
// make output after the stream is open.
func inputSession(t *testing.T, f *fixture) (string, string, string) {
	t.Helper()
	return dataSession(t, f, nil)
}

// typedText is a command line as the session's shell takes it.
func typedText(command string) []byte {
	if runtime.GOOS == "windows" {
		return []byte(command + "\r\n")
	}
	return []byte(command + "\n")
}

// dataTicket asks for a data connection ticket.
func dataTicket(t *testing.T, c *testConn, session, block string) []byte {
	t.Helper()
	res := result(t, c.request("ticket", "data.connect", map[string]any{
		"session_id": session, "block_id": block, "data_ref_id": "terminal",
		"subprotocol": terminalProtocol,
	}))
	raw, err := base64.RawURLEncoding.DecodeString(res["ticket"].(string))
	if err != nil || len(raw) != 16 {
		t.Fatalf("ticket %q: %v", res["ticket"], err)
	}
	if res["expires_in_ms"] != float64(60000) {
		t.Errorf("expires_in_ms %v", res["expires_in_ms"])
	}
	return raw
}

// dataHello sends a HELLO and returns the WELCOME's token and nonce.
func dataHello(t *testing.T, ws *websocket.Conn, ticket []byte) (token []byte, nonce uint64) {
	t.Helper()
	wsWrite(t, ws, websocket.MessageBinary, append([]byte{0x01}, ticket...))
	typ, w, err := wsRead(t, ws)
	if err != nil || typ != websocket.MessageBinary || len(w) != 32 || w[0] != 0x81 {
		t.Fatalf("welcome: %v %v", w, err)
	}
	if ms := binary.LittleEndian.Uint32(w[20:24]); ms != 60000 {
		t.Errorf("welcome keepalive %d", ms)
	}
	nonce = binary.LittleEndian.Uint64(w[24:32])
	if nonce == 0 {
		t.Error("channel nonce is zero")
	}
	return append([]byte(nil), w[1:17]...), nonce
}

// readData reads one binary message of a stream.
func readData(t *testing.T, ws *websocket.Conn) []byte {
	t.Helper()
	typ, p, err := wsRead(t, ws)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if typ != websocket.MessageBinary {
		t.Fatalf("stream message of type %v", typ)
	}
	if len(p) == 0 || len(p) > maxChunk {
		t.Fatalf("stream message of %d bytes", len(p))
	}
	return p
}

// readUntil reads the stream until it holds marker, and returns every
// byte read.
func readUntil(t *testing.T, ws *websocket.Conn, marker string) []byte {
	t.Helper()
	var got []byte
	deadline := time.Now().Add(10 * time.Second)
	for !bytes.Contains(got, []byte(marker)) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in %d bytes: %q", marker, len(got), got)
		}
		got = append(got, readData(t, ws)...)
	}
	return got
}

// expectReject reads a REJECT and checks its reason and close status.
func expectReject(t *testing.T, ws *websocket.Conn, reason byte, status websocket.StatusCode) {
	t.Helper()
	typ, p, err := wsRead(t, ws)
	if err != nil || typ != websocket.MessageBinary || len(p) != 2 || p[0] != 0x82 || p[1] != reason {
		t.Fatalf("reject: %v %v", p, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := ws.Read(ctx); err != nil {
			if got := websocket.CloseStatus(err); got != status {
				t.Errorf("close %v, want %v (%v)", got, status, err)
			}
			return
		}
	}
}

// TestDataConnection checks a terminal stream: the Ghostty snapshot the
// server takes, the resize message after it, and the program's output.
func TestDataConnection(t *testing.T) {
	f := startFixture(t)
	session, block, sid := inputSession(t, f)

	c := f.dialControl()
	c.hello(apiRevision)
	callChecked(t, c, "session.attach", map[string]any{"session_id": session})
	ticket := dataTicket(t, c, session, block)

	ws := f.dial("/api/data", terminalProtocol)
	ws.SetReadLimit(maxMessage)
	token, nonce := dataHello(t, ws, ticket)
	if len(token) != 16 {
		t.Errorf("token %d bytes", len(token))
	}
	if err := f.desk.Input(sid, typedText("echo rexapi-data")); err != nil {
		t.Fatal(err)
	}
	got := readUntil(t, ws, "rexapi-data")
	if !bytes.HasPrefix(got, []byte("GHOSTSNP")) {
		t.Fatalf("stream starts %q", got[:min(len(got), 16)])
	}
	if v := binary.LittleEndian.Uint16(got[8:10]); v != 1 {
		t.Errorf("snapshot version %d, want 1", v)
	}
	resize := "\x1b_rex1;" + strconv.FormatUint(nonce, 10) + ";"
	if !bytes.Contains(got, []byte(`"type":"resize"`)) {
		t.Errorf("no resize message: %q", got)
	}
	// The resize comes after the snapshot and before the output.
	i, j, k := bytes.Index(got, []byte("GHOSTSNP")), bytes.Index(got, []byte(resize)), bytes.Index(got, []byte("rexapi-data"))
	if i < 0 || j < i || k < j {
		t.Errorf("output at %d, resize at %d, snapshot at %d", k, j, i)
	}
}

// TestDataChildExited checks that a program that ends sends child_exited
// and leaves the stream open.
func TestDataChildExited(t *testing.T) {
	f := startFixture(t)
	session, block, _ := dataSession(t, f, exitCommand("byebye"))
	c := f.dialControl()
	c.hello(apiRevision)
	callChecked(t, c, "session.attach", map[string]any{"session_id": session})
	ticket := dataTicket(t, c, session, block)
	ws := f.dial("/api/data", terminalProtocol)
	ws.SetReadLimit(maxMessage)
	dataHello(t, ws, ticket)
	var got []byte
	deadline := time.Now().Add(15 * time.Second)
	for !bytes.Contains(got, []byte(`"child_exited"`)) {
		if time.Now().After(deadline) {
			t.Fatalf("no child_exited: %q", got)
		}
		got = append(got, readData(t, ws)...)
	}
}

// TestDataHandshake checks the REJECTs of a data connection.
func TestDataHandshake(t *testing.T) {
	f := startFixture(t)
	session, block, _ := dataSession(t, f, shellCommand("handshake"))
	c := f.dialControl()
	c.hello(apiRevision)
	callChecked(t, c, "session.attach", map[string]any{"session_id": session})
	ticket := dataTicket(t, c, session, block)

	t.Run("bad ticket", func(t *testing.T) {
		ws := f.dial("/api/data", terminalProtocol)
		wsWrite(t, ws, websocket.MessageBinary, append([]byte{0x01}, randomBytes(16)...))
		expectReject(t, ws, rejectTicket, 1008)
	})
	t.Run("malformed", func(t *testing.T) {
		ws := f.dial("/api/data", terminalProtocol)
		wsWrite(t, ws, websocket.MessageBinary, []byte{0x01, 2, 3})
		expectReject(t, ws, rejectMalformed, 1002)
	})
	t.Run("unknown message", func(t *testing.T) {
		ws := f.dial("/api/data", terminalProtocol)
		wsWrite(t, ws, websocket.MessageBinary, []byte{0x09})
		expectReject(t, ws, rejectMalformed, 1002)
	})
	t.Run("wrong subprotocol", func(t *testing.T) {
		ws := f.dial("/api/data", "rex.data.other.v1")
		wsWrite(t, ws, websocket.MessageBinary, append([]byte{0x01}, ticket...))
		expectReject(t, ws, rejectProtocol, 1008)
	})
}

// TestDataNoGhostty checks that a gateway over a server that does not take
// Ghostty snapshots says the data provider is unavailable.
func TestDataNoGhostty(t *testing.T) {
	f := startFixture(t)
	f.g.hello.Capabilities = nil
	session, block, _ := dataSession(t, f, shellCommand("nodata"))
	c := f.dialControl()
	c.hello(apiRevision)
	callChecked(t, c, "session.attach", map[string]any{"session_id": session})
	ticket := dataTicket(t, c, session, block)
	ws := f.dial("/api/data", terminalProtocol)
	wsWrite(t, ws, websocket.MessageBinary, append([]byte{0x01}, ticket...))
	typ, w, err := wsRead(t, ws)
	if err != nil || typ != websocket.MessageBinary || len(w) != 32 || w[0] != 0x81 {
		t.Fatalf("welcome: %v %v", w, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := ws.Read(ctx); err != nil {
			if got := websocket.CloseStatus(err); got != 1008 {
				t.Errorf("close %v, want 1008 (%v)", got, err)
			}
			return
		}
	}
}

// TestDataResume checks a resume with the exact count and one with too
// high a count.
func TestDataResume(t *testing.T) {
	f := startFixture(t)
	session, block, sid := inputSession(t, f)
	c := f.dialControl()
	c.hello(apiRevision)
	callChecked(t, c, "session.attach", map[string]any{"session_id": session})
	ticket := dataTicket(t, c, session, block)

	ws := f.dial("/api/data", terminalProtocol)
	ws.SetReadLimit(maxMessage)
	token, nonce := dataHello(t, ws, ticket)
	if err := f.desk.Input(sid, typedText("echo resume-live")); err != nil {
		t.Fatal(err)
	}
	got := readUntil(t, ws, "resume-live")
	count := uint64(len(got))
	ws.CloseNow()

	// The exact count continues the same stream: the same nonce, a new
	// token, and bytes not sent before.
	resume := append([]byte{0x02}, token...)
	resume = binary.LittleEndian.AppendUint64(resume, count)
	ws = f.dial("/api/data", terminalProtocol)
	ws.SetReadLimit(maxMessage)
	wsWrite(t, ws, websocket.MessageBinary, resume)
	typ, w, err := wsRead(t, ws)
	if err != nil || typ != websocket.MessageBinary || len(w) != 32 || w[0] != 0x81 {
		t.Fatalf("resume welcome: %v %v", w, err)
	}
	if gotNonce := binary.LittleEndian.Uint64(w[24:32]); gotNonce != nonce {
		t.Errorf("nonce %d, want %d", gotNonce, nonce)
	}
	if bytes.Equal(w[1:17], token) {
		t.Error("the resume kept the old token")
	}
	token = append([]byte(nil), w[1:17]...)
	if err := f.desk.Input(sid, typedText("echo resumed-again")); err != nil {
		t.Fatal(err)
	}
	resumed := readUntil(t, ws, "resumed-again")
	if bytes.HasPrefix(resumed, []byte("GHOSTSNP")) {
		t.Error("the resume sent the snapshot again")
	}
	ws.CloseNow()

	// A count past what the server sent is refused, and ends the stream.
	tooHigh := append([]byte{0x02}, token...)
	tooHigh = binary.LittleEndian.AppendUint64(tooHigh, 1<<40)
	bad := f.dial("/api/data", terminalProtocol)
	wsWrite(t, bad, websocket.MessageBinary, tooHigh)
	expectReject(t, bad, rejectMalformed, 1002)
}

// TestBootstrapDataTickets checks the tickets session.bootstrap hands out
// for the blocks it returns: they open the block's data connection as
// data.connect's do, they work once, and a detach or a bad request leaves
// none behind.
func TestBootstrapDataTickets(t *testing.T) {
	f := startFixture(t)
	session, block, _ := dataSession(t, f, nil)
	c := f.dialControl()
	c.hello(apiRevision)

	terminals := []map[string]any{{
		"creator_name": creatorName, "data_ref_id": dataTerminal, "subprotocol": terminalProtocol,
	}}
	res := result(t, c.request("boot", "session.bootstrap", map[string]any{
		"event_scope":        "session",
		"attach_session_ids": []string{session},
		"data_tickets":       terminals,
	}))
	tickets, ok := res["data_tickets"].([]any)
	if !ok || len(tickets) != 1 {
		t.Fatalf("data_tickets: %v", res["data_tickets"])
	}
	got := tickets[0].(map[string]any)
	if got["session_id"] != session || got["block_id"] != block {
		t.Errorf("ticket names %v, want %s/%s", got, session, block)
	}
	if got["data_ref_id"] != dataTerminal || got["subprotocol"] != terminalProtocol {
		t.Errorf("ticket opens %v/%v", got["data_ref_id"], got["subprotocol"])
	}
	if got["expires_in_ms"] != float64(60000) {
		t.Errorf("expires_in_ms %v", got["expires_in_ms"])
	}
	raw, err := base64.RawURLEncoding.DecodeString(got["ticket"].(string))
	if err != nil || len(raw) != 16 {
		t.Fatalf("ticket %q: %v", got["ticket"], err)
	}

	// The ticket opens a data connection, as one from data.connect does.
	ws := f.dial("/api/data", terminalProtocol)
	ws.SetReadLimit(maxMessage)
	dataHello(t, ws, raw)

	// A ticket works once: the same bytes are refused.
	again := f.dial("/api/data", terminalProtocol)
	wsWrite(t, again, websocket.MessageBinary, append([]byte{0x01}, raw...))
	expectReject(t, again, rejectTicket, 1008)

	// A detach throws away the tickets the connection did not spend.
	res = result(t, c.request("boot2", "session.bootstrap", map[string]any{
		"event_scope":        "session",
		"attach_session_ids": []string{session},
		"data_tickets":       terminals,
	}))
	unspent := res["data_tickets"].([]any)[0].(map[string]any)["ticket"].(string)
	result(t, c.request("detach", "session.detach", map[string]any{"session_id": session}))
	dropped, err := base64.RawURLEncoding.DecodeString(unspent)
	if err != nil {
		t.Fatal(err)
	}
	stale := f.dial("/api/data", terminalProtocol)
	wsWrite(t, stale, websocket.MessageBinary, append([]byte{0x01}, dropped...))
	expectReject(t, stale, rejectTicket, 1008)

	// A kind no block matches issues nothing, and the key is left out.
	res = result(t, c.request("nomatch", "session.bootstrap", map[string]any{
		"event_scope":        "session",
		"attach_session_ids": []string{session},
		"data_tickets": []map[string]any{{
			"creator_name": "com.superlogical.note", "data_ref_id": dataTerminal,
		}},
	}))
	if v, ok := res["data_tickets"]; ok {
		t.Errorf("data_tickets for no block: %v", v)
	}

	// A request the server rejects is answered before anything attaches:
	// the connection has no attachment to open a data connection with.
	rejected := f.dialControl()
	rejected.hello(apiRevision)
	tooMany := make([]map[string]any, 9)
	for i := range tooMany {
		tooMany[i] = map[string]any{"creator_name": creatorName, "data_ref_id": dataTerminal}
	}
	for _, bad := range []struct {
		id      string
		kinds   any
		problem string
	}{
		{"many", tooMany, "more than 8 kinds"},
		{"noref", []map[string]any{{"creator_name": creatorName}}, "no data_ref_id"},
		{"badsub", []map[string]any{{
			"creator_name": creatorName, "data_ref_id": dataTerminal, "subprotocol": "bad proto!",
		}}, "a malformed subprotocol"},
		{"unknown", "not an array", "data_tickets that is not an array"},
	} {
		e := failure(t, rejected.request(bad.id, "session.bootstrap", map[string]any{
			"event_scope":        "session",
			"attach_session_ids": []string{session},
			"data_tickets":       bad.kinds,
		}))
		if e["code"] != "bad_request" {
			t.Errorf("%s: %v", bad.problem, e)
			continue
		}
		e = failure(t, rejected.request(bad.id+"connect", "data.connect", map[string]any{
			"session_id": session, "block_id": block, "data_ref_id": dataTerminal,
		}))
		if e["code"] != "conflict" {
			t.Errorf("%s: the connection attached anyway: %v", bad.problem, e)
		}
	}
}

// TestStreamResumeWindow feeds a stream far more than it keeps, and
// checks that it keeps between resumeBytes and twice that, that a resume
// from the oldest byte it promises gets every byte from there, and that
// one from before it is refused.
func TestStreamResumeWindow(t *testing.T) {
	old := resumeBytes
	resumeBytes = 4 << 10
	defer func() { resumeBytes = old }()

	s := &stream{g: &Gateway{stokens: map[string]*stream{}}, done: make(chan struct{})}
	var sent []byte
	for i := 0; len(sent) < 10*resumeBytes; i++ {
		p := make([]byte, 1+i*37%1000)
		for j := range p {
			p[j] = byte(len(sent) + j)
		}
		sent = append(sent, p...)
		s.appendBytes(p)
		s.sent = s.total // a writer took it all
		if n := len(s.buf); n < min(len(sent), resumeBytes) || n > 2*resumeBytes {
			t.Fatalf("after %d bytes the stream keeps %d", len(sent), n)
		}
	}
	oldest := len(sent) - resumeBytes
	if s.base != int64(oldest) {
		t.Fatalf("base %d, want %d", s.base, oldest)
	}

	conns := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		conns <- ws
	}))
	defer srv.Close()
	client, _, err := websocket.Dial(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()

	ws := <-conns
	defer ws.CloseNow()
	call := &resumeCall{ws: ws, count: uint64(oldest), reply: make(chan error, 1)}
	go s.resumeStream(call)
	if err := <-call.reply; err != nil {
		t.Fatalf("resume from the oldest byte: %v", err)
	}
	if _, w, err := wsRead(t, client); err != nil || len(w) != 32 {
		t.Fatalf("welcome: %v %v", w, err)
	}
	var got []byte
	for len(got) < resumeBytes {
		_, p, err := wsRead(t, client)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, p...)
	}
	if !bytes.Equal(got, sent[oldest:]) {
		t.Error("the resume sent other bytes than the stream's newest")
	}

	// The refusal ends the stream; the client leaves first so that its
	// close does not wait for an answer.
	client.CloseNow()
	call = &resumeCall{count: uint64(oldest - 1), reply: make(chan error, 1)}
	s.resumeStream(call)
	if r := rejectReason(<-call.reply); r != rejectUnavailable {
		t.Errorf("resume from before the oldest byte: reason 0x%02x, want 0x%02x", r, rejectUnavailable)
	}
}

// BenchmarkStreamAppend appends small chunks to a stream that keeps all
// it may.
func BenchmarkStreamAppend(b *testing.B) {
	s := &stream{}
	chunk := make([]byte, 4<<10)
	for s.total < 2*int64(resumeBytes) {
		s.appendBytes(chunk)
		s.sent = s.total
	}
	b.SetBytes(int64(len(chunk)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.appendBytes(chunk)
		s.sent = s.total
	}
}
