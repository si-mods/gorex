//go:build darwin || linux || windows

package rex

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// streamServer is a test server with a session of 80x24, and a client of
// it whose attaches dial through dial.
func streamServer(t *testing.T, dial func() (net.Conn, error)) (*Server, *Client, string) {
	t.Helper()
	s, _ := testServer(t)
	a, b := net.Pipe()
	go s.handle(b, false)
	c := newClient(a, dial)
	t.Cleanup(func() {
		for _, in := range must(c.List()) {
			c.Kill(in.ID)
		}
		c.Close()
	})
	cmd, dir := testShell()
	info, err := c.Create(CreateOptions{Command: cmd, Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	return s, c, info.ID
}

func must[T any](v T, _ error) T { return v }

// sessionSize returns the size the server gave a session.
func sessionSize(t *testing.T, c *Client, sid string) string {
	t.Helper()
	infos, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range infos {
		if in.ID == sid {
			return fmt.Sprintf("%dx%d", in.Cols, in.Rows)
		}
	}
	t.Fatalf("no session %s", sid)
	return ""
}

// TestAttachSizeBeforeAnswer checks that the session has the attach's
// size once the server answers it: a resize the client sends then, on its
// control connection, is not overwritten by the attach's older size.
func TestAttachSizeBeforeAnswer(t *testing.T) {
	s, c, sid := streamServer(t, nil)
	for i := range 20 {
		a, b := net.Pipe()
		go s.handle(b, false)
		size := [2]int{100 + i, 30 + i}
		line, _ := json.Marshal(Attach{Op: "attach", SID: sid, Cols: size[0], Rows: size[1]})
		go a.Write(append(line, '\n'))
		if err := readOK(a); err != nil {
			t.Fatal(err)
		}
		if got, want := sessionSize(t, c, sid), fmt.Sprintf("%dx%d", size[0], size[1]); got != want {
			t.Fatalf("answered attach %d: session %s, want %s", i, got, want)
		}
		a.Close()
	}
}

// TestStreamAttachesAtLatestSize resizes a stream while its attach dials,
// slowly, as over a network: it attaches at the size the terminal has
// then, not the one it had when it started.
func TestStreamAttachesAtLatestSize(t *testing.T) {
	gate := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(gate) }) })
	var dialing atomic.Int32
	var s *Server
	s, c, sid := streamServer(t, func() (net.Conn, error) {
		dialing.Add(1)
		<-gate
		a, b := net.Pipe()
		go s.handle(b, false)
		return a, nil
	})
	st := c.Stream(sid, 80, 24)
	t.Cleanup(func() { st.Close() })
	var wg sync.WaitGroup
	wg.Go(func() { st.Resize(90, 20) }) // attaches
	for dialing.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	wg.Go(func() { st.Resize(121, 41) }) // waits for the attach
	for cols, rows := st.Size(); cols != 121 || rows != 41; cols, rows = st.Size() {
		time.Sleep(time.Millisecond)
	}
	once.Do(func() { close(gate) })
	wg.Wait()
	if got := sessionSize(t, c, sid); got != "121x41" {
		t.Fatalf("session %s, want 121x41", got)
	}
}

// TestStreamLastSizeWins resizes an attached stream from many goroutines
// at once: the session ends at the size the stream ends at.
func TestStreamLastSizeWins(t *testing.T) {
	var s *Server
	s, c, sid := streamServer(t, func() (net.Conn, error) {
		a, b := net.Pipe()
		go s.handle(b, false)
		return a, nil
	})
	st := c.Stream(sid, 80, 24)
	t.Cleanup(func() { st.Close() })
	st.Resize(80, 24)
	for round := range 10 {
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Go(func() { st.Resize(60+round+i, 20+i) })
		}
		wg.Wait()
		cols, rows := st.Size()
		if got, want := sessionSize(t, c, sid), fmt.Sprintf("%dx%d", cols, rows); got != want {
			t.Fatalf("round %d: session %s, stream %s", round, got, want)
		}
	}
}
