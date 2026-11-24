//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"tailscale.com/types/key"
)

// sessionOf returns the server's session sid.
func sessionOf(t testing.TB, s *Server, sid string) *session {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.sessions[sid]
	if ss == nil {
		t.Fatalf("no session %s", sid)
	}
	return ss
}

// flood makes the session print about total bytes of colored, numbered
// lines, as its program would, in reads of 4 KiB that cut escape
// sequences anywhere.
func flood(ss *session, total int) {
	var b strings.Builder
	for i := 0; b.Len() < total; i++ {
		fmt.Fprintf(&b, "\x1b[3%dmline %07d\x1b[0m %s\r\n", i%8, i, strings.Repeat("x", i%50))
	}
	out := []byte(b.String())
	for len(out) > 0 {
		n := min(len(out), 4<<10)
		ss.emit(append([]byte(nil), out[:n]...), nil, n)
		out = out[n:]
	}
}

// screenOf returns the last rows lines of a terminal's text: its screen.
func screenOf(text string, rows int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	return strings.Join(lines[max(len(lines)-rows, 0):], "\n")
}

// viewer is a client attached to a session that feeds what it reads into
// a terminal of its own, as a window does.
type viewer struct {
	conn net.Conn
	term *terminal.Terminal
	// start lets a viewer made stalled read.
	start chan struct{}
	once  sync.Once
	err   chan error
}

func attachViewer(t *testing.T, s *Server, sid, peer string, stalled bool) *viewer {
	t.Helper()
	c := pipe(s, peer)
	t.Cleanup(func() { c.Close() })
	conn, err := c.AttachConn(Attach{SID: sid, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	v := &viewer{conn: conn, term: headless(t), start: make(chan struct{}), err: make(chan error, 1)}
	v.term.Resize(80, 24)
	if !stalled {
		v.read()
	}
	go func() {
		<-v.start
		buf := make([]byte, 32<<10)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				v.term.Feed(append([]byte(nil), buf[:n]...))
			}
			if err != nil {
				v.err <- err
				return
			}
		}
	}()
	return v
}

func (v *viewer) read() { v.once.Do(func() { close(v.start) }) }

// waitScreen waits for the viewer to show the session's screen, and fails
// if its stream ends meanwhile, or a moment after.
func (v *viewer) waitScreen(t *testing.T, what string, ss *session) {
	t.Helper()
	server := func() string {
		ss.mu.Lock()
		defer ss.mu.Unlock()
		return screenOf(ss.vt.Text(), 24)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case err := <-v.err:
			t.Fatalf("%s: the stream ended: %v", what, err)
		default:
		}
		if got := screenOf(v.term.Text(), 24); got == server() {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("%s shows\n%s\nwant\n%s", what, got, server())
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case err := <-v.err:
		t.Fatalf("%s: the stream ended: %v", what, err)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestLaggingClientResyncs is a session printing far more than a client
// may fall behind, while one client reads nothing and another reads
// along: neither is dropped, and once the slow one reads, both show the
// session's screen. A client is sent the screen again, on its stream,
// rather than disconnected for falling behind.
func TestLaggingClientResyncs(t *testing.T) {
	for name, peer := range map[string]string{"local": "", "remote": key.NewNode().Public().String()} {
		t.Run(name, func(t *testing.T) {
			s, _ := testServer(t)
			c := pipe(s, "")
			defer c.Close()
			info, err := c.Create(CreateOptions{Command: silentCommand(), Cols: 80, Rows: 24})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Kill(info.ID)
			ss := sessionOf(t, s, info.ID)
			slow := attachViewer(t, s, info.ID, peer, true)
			fast := attachViewer(t, s, info.ID, peer, false)
			flood(ss, 12<<20)
			slow.read()
			fast.waitScreen(t, "the fast client", ss)
			slow.waitScreen(t, "the slow client", ss)
			if n := attachedTo(s, info.ID); n != 2 {
				t.Fatalf("%d clients attached, want 2", n)
			}
		})
	}
}

// TestDeadClientDropped is a client whose connection takes nothing for
// the write timeout, or is closed: it is dropped, as before.
func TestDeadClientDropped(t *testing.T) {
	defer SetTimeoutsForTest(300*time.Millisecond, 2*time.Second)()
	for name, peer := range map[string]string{"stalled remote": key.NewNode().Public().String(), "closed local": ""} {
		t.Run(name, func(t *testing.T) {
			s, _ := testServer(t)
			c := pipe(s, "")
			defer c.Close()
			info, err := c.Create(CreateOptions{Command: silentCommand(), Cols: 80, Rows: 24})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Kill(info.ID)
			ss := sessionOf(t, s, info.ID)
			v := attachViewer(t, s, info.ID, peer, true)
			if peer == "" {
				v.conn.Close()
			}
			flood(ss, 64<<10)
			if !waitUntil(5*time.Second, func() bool { return attachedTo(s, info.ID) == 0 }) {
				t.Fatal("the dead client stays attached")
			}
			if peer != "" {
				v.read()
				select {
				case err := <-v.err:
					if err != io.EOF && !strings.Contains(err.Error(), "closed") {
						t.Logf("stream ended: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("the dropped client's stream stays open")
				}
			}
		})
	}
}

// styledFill returns 32 MiB of rows that change color at every cell, the
// costliest scrollback to render as escape sequences.
func styledFill() []byte {
	var b []byte
	for i := 0; len(b) < 32<<20; i++ {
		for j := range 290 {
			b = fmt.Appendf(b, "\x1b[38;5;%dm%c", (i+j)%256, 'a'+j%26)
		}
		b = append(b, "\r\n"...)
	}
	return b
}

// styledSession returns a session of 300x50 whose scrollback styledFill
// filled.
func styledSession(t testing.TB) *session {
	t.Helper()
	s, _ := testServer(t)
	c := pipe(s, "")
	t.Cleanup(func() { c.Close() })
	info, err := c.Create(CreateOptions{Command: silentCommand(), Cols: 300, Rows: 50})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Kill(info.ID) })
	ss := sessionOf(t, s, info.ID)
	for b := styledFill(); len(b) > 0; {
		n := min(len(b), 64<<10)
		ss.emit(b[:n], nil, n)
		b = b[n:]
	}
	return ss
}

// TestAttachRendersOffLock checks that an attach's snapshot, rendered
// off the session's lock from the binary snapshot, is byte for byte what
// snapshotLocked renders under it, header and all.
func TestAttachRendersOffLock(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	info, err := c.Create(CreateOptions{Command: silentCommand(), Cols: 300, Rows: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Kill(info.ID)
	ss := sessionOf(t, s, info.ID)
	for b := styledFill(); len(b) > 0; {
		n := min(len(b), 64<<10)
		ss.emit(b[:n], nil, n)
		b = b[n:]
	}
	ss.mu.Lock()
	want := ss.snapshotLocked(Attach{Framed: true, History: resyncHistory})
	ss.mu.Unlock()
	conn, err := c.AttachConn(Attach{SID: info.ID, Framed: true, History: resyncHistory, Mute: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReaderSize(conn, 64<<10)
	line, err := br.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var head SnapshotHeader
	if err := json.Unmarshal(line, &head); err != nil {
		t.Fatal(err)
	}
	if !head.Truncated {
		t.Fatalf("a snapshot of a full scrollback is not truncated")
	}
	got := make([]byte, head.Snapshot)
	if _, err := io.ReadFull(br, got); err != nil {
		t.Fatal(err)
	}
	if got := append(line, got...); string(got) != string(want) {
		t.Fatalf("the attach rendered %d bytes off the lock, unlike the %d under it", len(got), len(want))
	}
}

// TestAttachOffLockKeepsOutput checks that output a session prints while
// an attach's snapshot renders off its lock reaches the client after the
// snapshot, not lost nor out of order.
func TestAttachOffLockKeepsOutput(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	info, err := c.Create(CreateOptions{Command: silentCommand(), Cols: 300, Rows: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Kill(info.ID)
	ss := sessionOf(t, s, info.ID)
	for b := styledFill(); len(b) > 0; {
		n := min(len(b), 64<<10)
		ss.emit(b[:n], nil, n)
		b = b[n:]
	}
	conn, err := c.AttachConn(Attach{SID: info.ID, Framed: true, History: resyncHistory, Mute: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	marker := []byte("\x1b[31mMARK-attach-output\x1b[0m\r\n")
	ss.emit(marker, nil, len(marker))
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReaderSize(conn, 64<<10)
	line, err := br.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var head SnapshotHeader
	if err := json.Unmarshal(line, &head); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, br, int64(head.Snapshot)); err != nil {
		t.Fatal(err)
	}
	if _, err := br.ReadBytes('K'); err != nil {
		t.Fatalf("the output printed after the attach never came: %v", err)
	}
}

// TestResyncRendersAsAttach checks that the screen a resync renders off
// the session's lock, from its binary snapshot, is the one an attach
// with resyncHistory gets, bounded however large the scrollback.
func TestResyncRendersAsAttach(t *testing.T) {
	ss := styledSession(t)
	ss.mu.Lock()
	want := ss.snapshotLocked(Attach{History: resyncHistory})
	bin, err := ss.vt.GhosttySnapshot()
	title, graphics, rows := ss.vt.Title(), ss.graphics.replay(), ss.rows
	full := len(ss.vt.Snapshot())
	ss.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	got, err := renderResync(bin, title, graphics, rows)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("the resync renders %d bytes, unlike the attach's %d", len(got), len(want))
	}
	if len(got) > resyncHistory+64<<10 || full < 4*resyncHistory {
		t.Fatalf("resync of %d bytes from a scrollback of %d", len(got), full)
	}
}

// BenchmarkResyncLocked is the work a resync does under the session's
// lock, against BenchmarkSnapshotLocked, an attach's, which renders the
// whole scrollback.
func BenchmarkResyncLocked(b *testing.B) {
	ss := styledSession(b)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for b.Loop() {
		ss.vt.GhosttySnapshot()
		ss.vt.Title()
		ss.graphics.replay()
	}
}

func BenchmarkSnapshotLocked(b *testing.B) {
	ss := styledSession(b)
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for b.Loop() {
		ss.snapshotLocked(Attach{History: resyncHistory})
	}
}

// streamReader reads an attach's stream into a buffer.
type streamReader struct {
	mu  sync.Mutex
	got []byte
}

func readStream(conn net.Conn) *streamReader {
	r := &streamReader{}
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := conn.Read(buf)
			r.mu.Lock()
			r.got = append(r.got, buf[:n]...)
			r.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return r
}

// afterReset returns what the stream brought after its last resync's
// reset, and how many resets it brought.
func (r *streamReader) afterReset() (string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	got := string(r.got)
	i := strings.LastIndex(got, resyncReset)
	if i < 0 {
		return "", 0
	}
	return got[i+len(resyncReset):], strings.Count(got, resyncReset)
}

// TestResyncClientInBand is a window that asks for the session's screen
// again (Client.Resync), as one that laid its terminal out at another
// size does: the screen comes on its stream holding the output before
// the request; output printed meanwhile comes once, in the screen or
// after it; output printed once the screen came follows it. A request
// at another size than the session's, or for the stream of another
// peer, is refused.
func TestResyncClientInBand(t *testing.T) {
	for name, peer := range map[string]string{"local": "", "remote": key.NewNode().Public().String()} {
		t.Run(name, func(t *testing.T) {
			s, _ := testServer(t)
			c := pipe(s, "")
			defer c.Close()
			info, err := c.Create(CreateOptions{Command: silentCommand(), Cols: 80, Rows: 24})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Kill(info.ID)
			ss := sessionOf(t, s, info.ID)
			w := pipe(s, peer)
			defer w.Close()
			conn, err := w.AttachConn(Attach{SID: info.ID, Cols: 80, Rows: 24, Client: "w1"})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			r := readStream(conn)
			emit := func(s string) { ss.emit([]byte(s+"\r\n"), nil, len(s)+2) }
			emit("before the resync")

			if err := w.Resync(info.ID, "w1", 81, 24); err == nil || err.Error() != ErrResyncSize.Error() {
				t.Fatalf("a resync at another size: %v, want %v", err, ErrResyncSize)
			}
			other := pipe(s, key.NewNode().Public().String())
			defer other.Close()
			if err := other.Resync(info.ID, "w1", 80, 24); err == nil {
				t.Fatal("another peer resynced the stream")
			}
			if err := w.Resync(info.ID, "w1", 80, 24); err != nil {
				t.Fatal(err)
			}
			emit("meanwhile")
			var tail string
			if !waitUntil(5*time.Second, func() bool {
				tail, _ = r.afterReset()
				return strings.Contains(tail, "meanwhile")
			}) {
				t.Fatalf("no resync on the stream: %q", tail)
			}
			// The screen came: what prints now must follow it.
			emit("after the screen")
			if !waitUntil(5*time.Second, func() bool {
				tail, _ = r.afterReset()
				return strings.Contains(tail, "after the screen")
			}) {
				t.Fatalf("the output after the screen did not follow it: %q", tail)
			}
			b, m, a := strings.Index(tail, "before the resync"), strings.Index(tail, "meanwhile"), strings.Index(tail, "after the screen")
			if b < 0 || b > m || m > a {
				t.Fatalf("after the reset the stream brings %q: want the screen with the output before the request, then the rest in order", tail)
			}
			for _, s := range []string{"before the resync", "meanwhile", "after the screen"} {
				if n := strings.Count(tail, s); n != 1 {
					t.Fatalf("%q comes %d times after the reset: %q", s, n, tail)
				}
			}
		})
	}
}

// TestResyncReachesTheLiveAttach is a stream that attached again with
// its id while the server still holds its old attach, whose connection
// is dead: each resync reaches the live one, and with no live one the
// request fails, so that the window falls back to a capture.
func TestResyncReachesTheLiveAttach(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	info, err := c.Create(CreateOptions{Command: silentCommand(), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Kill(info.ID)
	ss := sessionOf(t, s, info.ID)
	attach := func() net.Conn {
		conn, err := c.AttachConn(Attach{SID: info.ID, Cols: 80, Rows: 24, Client: "w1"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	attach()
	ss.mu.Lock()
	var old *attached
	for a := range ss.clients {
		old = a
	}
	ss.mu.Unlock()
	live := readStream(attach())
	// The old connection is half-open: the server has not dropped it, but
	// its writer is gone.
	old.mu.Lock()
	old.dead = true
	old.mu.Unlock()

	for i := 1; i <= 10; i++ {
		if err := c.Resync(info.ID, "w1", 80, 24); err != nil {
			t.Fatal(err)
		}
		if !waitUntil(5*time.Second, func() bool {
			_, n := live.afterReset()
			return n >= i
		}) {
			t.Fatalf("resync %d did not reach the live attach", i)
		}
	}

	live.mu.Lock()
	live.got = nil
	live.mu.Unlock()
	ss.mu.Lock()
	for a := range ss.clients {
		if a != old {
			a.mu.Lock()
			a.dead = true
			a.mu.Unlock()
		}
	}
	ss.mu.Unlock()
	if err := c.Resync(info.ID, "w1", 80, 24); err == nil {
		t.Fatal("a resync with no live attach succeeded: the window would not fall back")
	}
}
