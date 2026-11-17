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
func sessionOf(t *testing.T, s *Server, sid string) *session {
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
