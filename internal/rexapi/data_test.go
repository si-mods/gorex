package rexapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// fakeSnapshot is a Snapshotter that records what it was given and returns
// a recognizable GHOSTSNP.
type fakeSnapshot struct {
	vt   []byte
	cols int
	rows int
}

func (f *fakeSnapshot) Snapshot(vt []byte, cols, rows int) ([]byte, error) {
	f.vt, f.cols, f.rows = append([]byte(nil), vt...), cols, rows
	out := []byte("GHOSTSNP\x00\x01fake snapshot")
	return out, nil
}

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

// TestDataConnection checks a terminal stream: its snapshot, the resize
// message after it, and the program's output.
func TestDataConnection(t *testing.T) {
	snap := &fakeSnapshot{}
	f := startFixture(t, snap)
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
		t.Errorf("stream starts %q", got[:min(len(got), 16)])
	}
	resize := "\x1b_rex1;" + strconv.FormatUint(nonce, 10) + ";"
	if !bytes.Contains(got, []byte(resize)) || !bytes.Contains(got, []byte(`"type":"resize"`)) {
		t.Errorf("no resize after the snapshot: %q", got)
	}
	if snap.cols != 80 || snap.rows != 24 {
		t.Errorf("snapshot size %dx%d", snap.cols, snap.rows)
	}
	if len(snap.vt) == 0 {
		t.Error("the snapshotter got no VT bytes")
	}
	// The resize comes after the snapshot and before the output.
	if i, j := bytes.Index(got, []byte("GHOSTSNP")), bytes.Index(got, []byte(resize)); i < 0 || j < i {
		t.Errorf("resize at %d, snapshot at %d", j, i)
	}
}

// TestDataChildExited checks that a program that ends sends child_exited
// and leaves the stream open.
func TestDataChildExited(t *testing.T) {
	f := startFixture(t, &fakeSnapshot{})
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
	f := startFixture(t, &fakeSnapshot{})
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

// TestDataNoSnapshotter checks that a gateway without a Snapshotter says
// the data provider is unavailable.
func TestDataNoSnapshotter(t *testing.T) {
	f := startFixture(t, nil)
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
	f := startFixture(t, &fakeSnapshot{})
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
	next := readData(t, ws)
	if bytes.HasPrefix(next, []byte("GHOSTSNP")) {
		t.Error("the resume sent the snapshot again")
	}
	if err := f.desk.Input(sid, typedText("echo resumed-again")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, ws, "resumed-again")
	ws.CloseNow()

	// A count past what the server sent is refused, and ends the stream.
	tooHigh := append([]byte{0x02}, token...)
	tooHigh = binary.LittleEndian.AppendUint64(tooHigh, 1<<40)
	bad := f.dial("/api/data", terminalProtocol)
	wsWrite(t, bad, websocket.MessageBinary, tooHigh)
	expectReject(t, bad, rejectMalformed, 1002)
}
