//go:build darwin || linux || windows

package rex

import (
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

// styledSession returns a session of 300x50 whose scrollback is full of
// rows that change color at every cell, the costliest to render as
// escape sequences.
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
	var b []byte
	for i := 0; len(b) < 32<<20; i++ {
		for j := range 290 {
			b = fmt.Appendf(b, "\x1b[38;5;%dm%c", (i+j)%256, 'a'+j%26)
		}
		b = append(b, "\r\n"...)
	}
	for len(b) > 0 {
		n := min(len(b), 64<<10)
		ss.emit(b[:n], nil, n)
		b = b[n:]
	}
	return ss
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

// TestResyncClientInBand is a window that asks for the session's screen
// again (Client.Resync), as one that laid its terminal out at another
// size does: the screen comes on its stream after the output before it
// and before the output after, so it holds the one and the other follows
// it once. A request at another size than the session's, or for the
// stream of another peer, is refused.
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
			var mu sync.Mutex
			var got []byte
			go func() {
				buf := make([]byte, 32<<10)
				for {
					n, err := conn.Read(buf)
					mu.Lock()
					got = append(got, buf[:n]...)
					mu.Unlock()
					if err != nil {
						return
					}
				}
			}()
			before := []byte("before the resync\r\n")
			ss.emit(before, nil, len(before))

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
			after := []byte("after the resync\r\n")
			ss.emit(after, nil, len(after))

			var tail string
			if !waitUntil(5*time.Second, func() bool {
				mu.Lock()
				defer mu.Unlock()
				i := strings.LastIndex(string(got), resyncReset)
				if i < 0 {
					return false
				}
				tail = string(got[i+len(resyncReset):])
				return strings.Contains(tail, "after the resync")
			}) {
				mu.Lock()
				defer mu.Unlock()
				t.Fatalf("no resync followed by the output after it: %q", got)
			}
			b, a := strings.Index(tail, "before the resync"), strings.Index(tail, "after the resync")
			if b < 0 || b > a || strings.Count(tail, "after the resync") != 1 {
				t.Fatalf("after the reset the stream brings %q: want the screen, with the output before it, then the output after it once", tail)
			}
		})
	}
}
