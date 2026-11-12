package rexapi

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// listCount counts the gateway's session list requests, and the answers
// that carried the list rather than "unchanged".
type listCount struct{ asked, sent atomic.Int32 }

// countedFixture is startFixture with the gateway's requests counted, on
// its own connection to the server; attached sessions pass uncounted.
func countedFixture(t *testing.T) (*fixture, *listCount) {
	t.Helper()
	n := &listCount{}
	return startFixtureVia(t, func(socket string) string {
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
			for first := true; ; first = false {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				s, err := net.Dial("unix", socket)
				if err != nil {
					c.Close()
					continue
				}
				if !first {
					go func() { io.Copy(c, s); c.Close() }()
					go func() { io.Copy(s, c); s.Close() }()
					continue
				}
				go copyLines(s, c, func(line []byte) {
					if bytes.Contains(line, []byte(`"op":"list"`)) {
						n.asked.Add(1)
					}
				})
				go copyLines(c, s, func(line []byte) {
					if bytes.Contains(line, []byte(`"etag":`)) && bytes.Contains(line, []byte(`"data":`)) {
						n.sent.Add(1)
					}
				})
			}
		}()
		return path
	}), n
}

// copyLines copies lines from r to w, each seen first.
func copyLines(w, r net.Conn, see func([]byte)) {
	defer w.Close()
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		see(line)
		if _, werr := w.Write(line); err != nil || werr != nil {
			return
		}
	}
}

// shellBlocks starts n shells, each in a tab of its own of workspace
// "w", and returns their GoRex sessions.
func shellBlocks(t *testing.T, f *fixture, n int) []string {
	t.Helper()
	var tabs []layout.Tab
	var sids []string
	for range n {
		info, err := f.desk.Create(rex.CreateOptions{Command: []string{"/bin/sh"}, Cols: 80, Rows: 24})
		if err != nil {
			t.Fatal(err)
		}
		tabs = append(tabs, layout.Tab{Name: "t", Root: pane(info.ID)})
		sids = append(sids, info.ID)
	}
	f.setLayout(layout.Store{Sessions: []layout.Workspace{{ID: "w", Name: "work", Layout: layout.Layout{Tabs: tabs}}}})
	return sids
}

// streamed opens a block's data stream, and collects what it sends.
type streamed struct {
	mu  sync.Mutex
	got []byte
}

func openStream(t *testing.T, f *fixture, c *testConn, block string) *streamed {
	t.Helper()
	ticket := dataTicket(t, c, sessionID("w"), block)
	ws := f.dial("/api/data", terminalProtocol)
	ws.SetReadLimit(maxMessage)
	dataHello(t, ws, ticket)
	s := &streamed{}
	go func() {
		for {
			_, p, err := ws.Read(context.Background())
			if err != nil {
				return
			}
			s.mu.Lock()
			s.got = append(s.got, p...)
			s.mu.Unlock()
		}
	}()
	return s
}

// within waits up to d for the stream to hold marker after from bytes,
// and returns how long it took.
func (s *streamed) within(t *testing.T, from int, marker string, d time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	for time.Since(start) < 5*time.Second {
		s.mu.Lock()
		found := bytes.Contains(s.got[min(from, len(s.got)):], []byte(marker))
		s.mu.Unlock()
		if found {
			took := time.Since(start)
			if took > d {
				t.Errorf("%s took %v, more than %v", marker, took, d)
			}
			return took
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no %s", marker)
	return 0
}

func (s *streamed) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

// TestIdleGatewayListsNothing: with no client and nothing changing, the
// gateway does not ask for the session list.
func TestIdleGatewayListsNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	f, n := countedFixture(t)
	shellBlocks(t, f, 2)
	time.Sleep(time.Second)
	asked := n.asked.Load()
	time.Sleep(4 * 500 * time.Millisecond)
	if got := n.asked.Load() - asked; got != 0 {
		t.Errorf("an idle gateway asked for the session list %d times in 2s", got)
	}
}

// TestOneChangeOneList: with a control client and two data streams, a
// title's change costs a list or two, not one per stream and tick.
func TestOneChangeOneList(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	f, n := countedFixture(t)
	sids := shellBlocks(t, f, 2)
	c := f.dialControl()
	c.hello(apiRevision)
	callChecked(t, c, "session.attach_all", map[string]any{"event_scope": "all"})
	callChecked(t, c, "session.attach", map[string]any{"session_id": sessionID("w")})
	openStream(t, f, c, blockID(sids[0]))
	openStream(t, f, c, blockID(sids[1]))
	time.Sleep(time.Second)
	asked, sent := n.asked.Load(), n.sent.Load()
	if err := f.desk.Input(sids[0], []byte("printf '\\033]0;Busy\\007'\n")); err != nil {
		t.Fatal(err)
	}
	if ev := waitBlockEventNamed(t, c, "title_changed"); ev["payload"].(map[string]any)["title"] != "Busy" {
		t.Errorf("title_changed: %v", ev)
	}
	time.Sleep(time.Second)
	a, s := n.asked.Load()-asked, n.sent.Load()-sent
	t.Logf("a title's change: %d list requests, %d lists sent", a, s)
	if a > 4 || s > 3 {
		t.Errorf("a title's change cost %d list requests, %d lists sent", a, s)
	}
}

// TestStreamFollowsSession: a stream tells of its session's resize and
// exit, and the control connection of a bell, within the half second the
// polls took at most.
func TestStreamFollowsSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	f := startFixture(t)
	sid := shellBlocks(t, f, 1)[0]
	c := f.dialControl()
	c.hello(apiRevision)
	callChecked(t, c, "session.attach_all", map[string]any{"event_scope": "all"})
	callChecked(t, c, "session.attach", map[string]any{"session_id": sessionID("w")})
	s := openStream(t, f, c, blockID(sid))
	time.Sleep(time.Second)

	start := time.Now()
	if err := f.desk.Input(sid, []byte("printf '\\a'\n")); err != nil {
		t.Fatal(err)
	}
	waitBlockEventNamed(t, c, "bell")
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Errorf("the bell took %v", took)
	}

	from := s.len()
	if err := f.desk.Resize(sid, 100, 30); err != nil {
		t.Fatal(err)
	}
	t.Logf("resize after %v", s.within(t, from, `"cols":100`, 500*time.Millisecond))

	from = s.len()
	if err := f.desk.Input(sid, []byte("exit 7\n")); err != nil {
		t.Fatal(err)
	}
	t.Logf("exit after %v", s.within(t, from, `"child_exited"`, 500*time.Millisecond))
}
