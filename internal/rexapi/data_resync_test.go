package rexapi

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// TestFloodHelper is the program of a flooding session, when the tests
// start this binary for one (floodCommand); else it does nothing.
// "forever" prints lines with no end; a number n prints "flood-ready",
// waits for a line, prints n bytes of lines, then "flood-done", and waits.
func TestFloodHelper(t *testing.T) {
	mode := os.Getenv("GOREX_TEST_FLOOD")
	if mode == "" {
		t.Skip("not a flood")
	}
	line := bytes.Repeat([]byte("y"), 63)
	line = append(line, '\n')
	chunk := bytes.Repeat(line, 512)
	if mode == "forever" {
		for {
			if _, err := os.Stdout.Write(chunk); err != nil {
				os.Exit(0)
			}
		}
	}
	n, _ := strconv.Atoi(mode)
	os.Stdout.WriteString("flood-ready\n")
	in := bufio.NewReader(os.Stdin)
	in.ReadString('\n')
	for n > 0 {
		k := min(n, len(chunk))
		os.Stdout.Write(chunk[:k])
		n -= k
	}
	os.Stdout.WriteString("\nflood-done\n")
	for {
		if _, err := in.ReadString('\n'); err != nil {
			os.Exit(0)
		}
	}
}

// floodSession starts a session that runs TestFloodHelper in mode, in a
// layout that shows it, and returns the session ID, block ID and SID.
func floodSession(t *testing.T, f *fixture, mode string) (string, string, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.desk.Create(rex.CreateOptions{
		Command: []string{exe, "-test.run=^TestFloodHelper$"},
		Env:     []string{"GOREX_TEST_FLOOD=" + mode},
		Cols:    80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.setLayout(layout.Store{Sessions: []layout.Workspace{{
		ID: "data", Name: "data",
		Layout: layout.Layout{Tabs: []layout.Tab{{Name: "term", Root: pane(info.ID)}}},
	}}})
	return sessionID("data"), blockID(info.ID), info.ID
}

// attachProxy stands between the gateway and the GoRex server, and
// follows the attach connections: refuse, when set, says which to close
// at once, by their index from 0; delay, when set, how long to hold one
// before it reaches the server.
type attachProxy struct {
	refuse func(i int) bool
	delay  func(i int) time.Duration
	mu     sync.Mutex
	n      int              // the attaches seen
	conns  map[int]net.Conn // the proxy's connections to the server
	ended  map[int]chan struct{}
	// serverEnded counts the attaches the server ended first, as it ends
	// those of a client far behind.
	serverEnded atomic.Int32
}

// attaches counts the attaches the gateway asked for.
func (p *attachProxy) attaches() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

// drop closes attach i on the server's side, as the server drops a
// client.
func (p *attachProxy) drop(i int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.conns[i].Close()
}

// endedCh closes once the gateway closed attach i.
func (p *attachProxy) endedCh(i int) chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ended == nil {
		p.ended = map[int]chan struct{}{}
	}
	if p.ended[i] == nil {
		p.ended[i] = make(chan struct{})
	}
	return p.ended[i]
}

// proxyFixture is startFixture with the gateway's connections to the
// server through p.
func proxyFixture(t *testing.T, p *attachProxy) *fixture {
	t.Helper()
	return startFixtureVia(t, func(socket string) string {
		// A short path: a Unix socket's is limited.
		dir, err := os.MkdirTemp("", "rxp")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		path := filepath.Join(dir, "p.sock")
		ln, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go p.serve(c, socket)
			}
		}()
		return path
	})
}

func (p *attachProxy) serve(c net.Conn, socket string) {
	br := bufio.NewReader(c)
	line, err := br.ReadBytes('\n')
	if err != nil {
		c.Close()
		return
	}
	i := -1
	if bytes.Contains(line, []byte(`"op":"attach"`)) {
		p.mu.Lock()
		i = p.n
		p.n++
		refuse := p.refuse != nil && p.refuse(i)
		var wait time.Duration
		if p.delay != nil {
			wait = p.delay(i)
		}
		p.mu.Unlock()
		if refuse {
			c.Close()
			return
		}
		time.Sleep(wait)
	}
	s, err := net.Dial("unix", socket)
	if err != nil {
		c.Close()
		return
	}
	var ended chan struct{}
	if i >= 0 {
		ended = p.endedCh(i)
		p.mu.Lock()
		if p.conns == nil {
			p.conns = map[int]net.Conn{}
		}
		p.conns[i] = s
		p.mu.Unlock()
	}
	var gatewayEnded atomic.Bool
	go func() {
		defer c.Close()
		buf := make([]byte, 64<<10)
		for {
			n, err := s.Read(buf)
			if n > 0 {
				if _, werr := c.Write(buf[:n]); werr != nil {
					return // the gateway closed it
				}
			}
			if err != nil {
				if i >= 0 && !gatewayEnded.Load() {
					p.serverEnded.Add(1)
				}
				return
			}
		}
	}()
	s.Write(line)
	io.Copy(s, br)
	gatewayEnded.Store(true)
	if ended != nil {
		close(ended)
	}
	s.Close()
}

// openData opens a block's data stream, and returns it with its token and
// nonce.
func openData(t *testing.T, f *fixture, session, block string) (*websocket.Conn, []byte, uint64) {
	t.Helper()
	c := f.dialControl()
	c.hello(apiRevision)
	callChecked(t, c, "session.attach", map[string]any{"session_id": session})
	ticket := dataTicket(t, c, session, block)
	ws := f.dial("/api/data", terminalProtocol)
	ws.SetReadLimit(maxMessage)
	token, nonce := dataHello(t, ws, ticket)
	return ws, token, nonce
}

// resumeData resumes a stream from byte count, and returns the new
// connection and token.
func resumeData(t *testing.T, f *fixture, token []byte, count int) (*websocket.Conn, []byte) {
	t.Helper()
	msg := append([]byte{0x02}, token...)
	msg = binary.LittleEndian.AppendUint64(msg, uint64(count))
	ws := f.dial("/api/data", terminalProtocol)
	ws.SetReadLimit(maxMessage)
	wsWrite(t, ws, websocket.MessageBinary, msg)
	typ, w, err := wsRead(t, ws)
	if err != nil || typ != websocket.MessageBinary || len(w) != 32 || w[0] != 0x81 {
		t.Fatalf("resume welcome: %v %v", w, err)
	}
	return ws, append([]byte(nil), w[1:17]...)
}

// resyncAt checks that got holds a resync, in its order: the reset, the
// size, terminal_reset, then the snapshot; and returns where it starts.
func resyncAt(t *testing.T, got []byte, nonce uint64) int {
	t.Helper()
	apc := "\x1b_rex1;" + strconv.FormatUint(nonce, 10) + ";"
	i := bytes.Index(got, []byte(resyncReset))
	if i < 0 {
		t.Fatal("no resync")
	}
	// The size, then terminal_reset, right after the reset.
	j := bytes.Index(got[i:], []byte(apc+`{"cell_height_px":0`))
	k := bytes.Index(got[i:], []byte(apc+`{"type":"terminal_reset"}`))
	if j != len(resyncReset) || k < j {
		t.Fatalf("no resync: reset at %d, resize at %d, terminal_reset at %d", i, j, k)
	}
	return i
}

// TestDataResync checks that a stream goes on when the GoRex server ends
// its attach while the session runs: the stream attaches again and sends
// a reset, the size, terminal_reset, and the new snapshot, then the
// output, and does not close with 1011.
func TestDataResync(t *testing.T) {
	p := &attachProxy{}
	f := proxyFixture(t, p)
	session, block, sid := inputSession(t, f)
	ws, _, nonce := openData(t, f, session, block)
	if err := f.desk.Input(sid, typedText("echo before-drop")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, ws, "before-drop")

	p.drop(0)
	if err := f.desk.Input(sid, typedText("echo after-drop")); err != nil {
		t.Fatal(err)
	}
	got := readUntil(t, ws, "after-drop")
	if at := resyncAt(t, got, nonce); at > bytes.LastIndex(got, []byte("after-drop")) {
		t.Errorf("the resync came after the output: %q", got)
	}
	if n := p.attaches(); n != 2 {
		t.Errorf("%d attaches, want 2", n)
	}
}

// TestDataResyncFails checks that a stream whose attach ended closes with
// 1011 when it cannot attach again, while the session runs.
func TestDataResyncFails(t *testing.T) {
	p := &attachProxy{refuse: func(i int) bool { return i > 0 }}
	f := proxyFixture(t, p)
	session, block, sid := inputSession(t, f)
	ws, _, _ := openData(t, f, session, block)
	if err := f.desk.Input(sid, typedText("echo before-drop")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, ws, "before-drop")
	p.drop(0)
	expectClose(t, ws, websocket.StatusInternalError)
}

// TestDataResyncNoOutput checks that a stream closes with 1011, rather
// than attach again and again, when an attach ends with no output after
// its snapshot.
func TestDataResyncNoOutput(t *testing.T) {
	p := &attachProxy{}
	f := proxyFixture(t, p)
	session, block, sid := inputSession(t, f)
	ws, _, _ := openData(t, f, session, block)
	if err := f.desk.Input(sid, typedText("echo before-drop")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, ws, "before-drop")
	settled(t, f, sid) // the shell printed its last prompt
	p.drop(0)
	waitFor(t, "the second attach", func() bool { return p.attaches() == 2 })
	time.Sleep(300 * time.Millisecond) // its snapshot read
	p.drop(1)
	expectClose(t, ws, websocket.StatusInternalError)
	if n := p.attaches(); n != 2 {
		t.Errorf("%d attaches, want 2", n)
	}
}

// TestDataCollapse checks a stream that falls far behind while its
// client is away: it drops the output past collapseBytes, and the resume
// gets the bytes kept, then a resync with the newest screen, not the
// output in between; a resume after that continues at the same count.
func TestDataCollapse(t *testing.T) {
	const flood = 4 << 20

	p := &attachProxy{}
	f := proxyFixture(t, p)
	f.g.collapseBytes = 64 << 10
	session, block, sid := floodSession(t, f, strconv.Itoa(flood))
	ws, token, nonce := openData(t, f, session, block)
	count := len(readUntil(t, ws, "flood-ready"))
	ws.CloseNow()

	if err := f.desk.Input(sid, typedText("")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the flood", func() bool {
		list, err := f.desk.List()
		if err != nil {
			return false
		}
		for _, in := range list {
			if in.ID == sid {
				return in.Output > flood
			}
		}
		return false
	})
	time.Sleep(500 * time.Millisecond)

	ws, token = resumeData(t, f, token, count)
	got := readUntil(t, ws, "flood-done")
	resyncAt(t, got, nonce)
	if len(got) > flood/2 {
		t.Errorf("the resume sent %d bytes of a flood of %d", len(got), flood)
	}
	count += len(got)
	ws.CloseNow()

	// The count stays the stream's own across the drop.
	ws, _ = resumeData(t, f, token, count)
	if err := f.desk.Input(sid, typedText("after-collapse")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, ws, "after-collapse")
	if n := p.serverEnded.Load(); n != 0 {
		t.Errorf("the server ended %d attaches", n)
	}
}

// TestDataSlowReader checks that a client that stops reading a flooding
// pane for a while does not make the GoRex server drop the gateway's
// attach, and that its stream stays open.
func TestDataSlowReader(t *testing.T) {
	p := &attachProxy{}
	f := proxyFixture(t, p)
	session, block, _ := floodSession(t, f, "forever")
	ws, _, _ := openData(t, f, session, block)
	readData(t, ws)
	time.Sleep(5 * time.Second)
	got, resyncs := 0, 0
	end := time.Now().Add(2 * time.Second)
	for time.Now().Before(end) {
		b := readData(t, ws)
		got += len(b)
		resyncs += strings.Count(string(b), resyncReset)
	}
	t.Logf("read %d bytes after the pause, %d resyncs, %d attaches", got, resyncs, p.attaches())
	// One resync at a time, once the connection took the one before.
	if resyncs < 1 {
		t.Error("no resync: the stream did not fall behind")
	}
	if resyncs > 4 {
		t.Errorf("%d resyncs in 2 s", resyncs)
	}
	if n := p.serverEnded.Load(); n != 0 {
		t.Errorf("the server ended %d attaches", n)
	}
}

// output is how many bytes session sid printed.
func output(t *testing.T, f *fixture, sid string) uint64 {
	t.Helper()
	list, err := f.desk.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range list {
		if in.ID == sid {
			return in.Output
		}
	}
	t.Fatalf("no session %s", sid)
	return 0
}

// settled waits until session sid printed nothing for half a second.
func settled(t *testing.T, f *fixture, sid string) {
	t.Helper()
	last, since := output(t, f, sid), time.Now()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		if n := output(t, f, sid); n != last {
			last, since = n, time.Now()
		} else if time.Since(since) >= 500*time.Millisecond {
			return
		}
	}
	t.Fatal("the session kept printing")
}

// TestDataCollapseSlowReader checks a client that reads, slowly, all
// along: the stream drops what the client falls behind by and resyncs,
// and a resume at the client's count then continues the same stream.
func TestDataCollapseSlowReader(t *testing.T) {
	const flood = 16 << 20
	p := &attachProxy{}
	f := proxyFixture(t, p)
	f.g.collapseBytes = 64 << 10
	session, block, sid := floodSession(t, f, strconv.Itoa(flood))
	ws, token, nonce := openData(t, f, session, block)
	got := readUntil(t, ws, "flood-ready")
	if err := f.desk.Input(sid, typedText("")); err != nil {
		t.Fatal(err)
	}
	reset := []byte(`"type":"terminal_reset"`)
	for !bytes.Contains(got, reset) {
		got = append(got, readData(t, ws)...)
		time.Sleep(5 * time.Millisecond) // a slow reader
		if len(got) > flood {
			t.Fatal("no resync in the whole flood")
		}
	}
	ws.CloseNow()
	ws, _ = resumeData(t, f, token, len(got))
	more := readUntil(t, ws, "flood-done")
	if len(got)+len(more) > flood/2 {
		t.Errorf("the client got %d bytes of a flood of %d", len(got)+len(more), flood)
	}
	resyncAt(t, append(got, more...), nonce)
	if n := p.serverEnded.Load(); n != 0 {
		t.Errorf("the server ended %d attaches", n)
	}
}

// TestDataResyncEnds checks that an attach a resync made is closed when
// the stream ends before the attach is ready.
func TestDataResyncEnds(t *testing.T) {
	p := &attachProxy{delay: func(i int) time.Duration {
		if i == 1 {
			return time.Second
		}
		return 0
	}}
	f := proxyFixture(t, p)
	session, block, sid := inputSession(t, f)
	ws, _, _ := openData(t, f, session, block)
	if err := f.desk.Input(sid, typedText("echo before-drop")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, ws, "before-drop")
	p.drop(0)
	waitFor(t, "the second attach", func() bool { return p.attaches() == 2 })
	ws.Close(websocket.StatusNormalClosure, "")
	select {
	case <-p.endedCh(1):
	case <-time.After(10 * time.Second):
		t.Fatal("the gateway kept the attach of a resync after its stream ended")
	}
}

// TestDataResumeDuringFlood checks that a client that comes back to a
// flooding stream gets bytes at once, and a resync soon after.
func TestDataResumeDuringFlood(t *testing.T) {
	p := &attachProxy{}
	f := proxyFixture(t, p)
	session, block, _ := floodSession(t, f, "forever")
	ws, token, nonce := openData(t, f, session, block)
	count := len(readData(t, ws))
	ws.CloseNow()
	time.Sleep(2 * time.Second)

	start := time.Now()
	ws, _ = resumeData(t, f, token, count)
	first := readData(t, ws)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the first bytes came %v after the resume", d)
	}
	got := append([]byte(nil), first...)
	for !bytes.Contains(got, []byte(`"type":"terminal_reset"`)) {
		if time.Since(start) > 5*time.Second {
			t.Fatalf("no resync %v after the resume", time.Since(start))
		}
		got = append(got, readData(t, ws)...)
	}
	resyncAt(t, got, nonce)
	t.Logf("resync %v after the resume", time.Since(start))
}

// TestStreamKeepsUnsent checks that a stream keeps the bytes its writer
// did not take, past what it keeps for a resume, as the start of a large
// snapshot, and keeps only the newest once they are taken.
func TestStreamKeepsUnsent(t *testing.T) {
	s := &stream{}
	chunk := make([]byte, 64<<10)
	for s.total < 3*int64(resumeBytes) {
		s.appendBytes(chunk)
	}
	if start := s.total - int64(len(s.buf)); start != 0 {
		t.Fatalf("the stream dropped %d bytes its writer did not take", start)
	}
	s.sent = s.total
	for range 4 {
		s.appendBytes(chunk)
	}
	if n := len(s.buf); n > 2*resumeBytes {
		t.Errorf("the stream keeps %d bytes once they were sent", n)
	}
}
