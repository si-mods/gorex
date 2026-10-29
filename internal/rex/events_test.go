//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"encoding/json"
	"net"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestThrottle asks a throttle many times quickly: it runs once per
// period, and once after the last ask.
func TestThrottle(t *testing.T) {
	var runs atomic.Int32
	th := &throttle{every: 100 * time.Millisecond, f: func() { runs.Add(1) }}
	for range 50 {
		th.ask()
		time.Sleep(10 * time.Millisecond)
	}
	asked := time.Now()
	time.Sleep(150 * time.Millisecond)
	// 500 ms of asks: the first at once, then one per 100 ms.
	if n := runs.Load(); n < 5 || n > 7 {
		t.Errorf("%d runs in %v", n, time.Since(asked)+500*time.Millisecond)
	}
	n := runs.Load()
	time.Sleep(150 * time.Millisecond)
	if runs.Load() != n {
		t.Error("ran with no ask")
	}
}

// TestSubscribe checks that a connection that subscribed is told of each
// change, and one that did not of none.
func TestSubscribe(t *testing.T) {
	defer sizeLease.set(sizeLease.set(300 * time.Millisecond))
	s, c, sid := streamServer(t, nil)
	sub := pipe(s, "")
	defer sub.Close()
	sub.keepalive = true
	if _, err := sub.Hello(); err != nil {
		t.Fatal(err)
	}
	if err := sub.Subscribe(); err != nil {
		t.Fatal(err)
	}
	// An old client: a raw connection that never subscribes.
	old, b := net.Pipe()
	go s.handle(b, "")
	defer old.Close()
	r := bufio.NewReader(old)
	old.Write([]byte(`{"id":1,"op":"list"}` + "\n"))
	if line, err := r.ReadBytes('\n'); err != nil || !strings.Contains(string(line), `"id":1`) {
		t.Fatalf("old client's list: %q %v", line, err)
	}

	// quiet waits for the changes going on to end, and forgets them.
	quiet := func() {
		t.Helper()
		for {
			select {
			case <-sub.Changed():
				continue
			case <-time.After(sampleEvery + 200*time.Millisecond):
			}
			sub.Changes()
			return
		}
	}
	expect := func(what, kind string) {
		t.Helper()
		timeout := time.After(3 * time.Second)
		for {
			select {
			case <-sub.Changed():
			case <-timeout:
				t.Errorf("%s: no %s event", what, kind)
				return
			}
			if layout, sessions := sub.Changes(); kind == "layout" && layout || kind == "sessions" && sessions {
				return
			}
		}
	}
	none := func(what string) {
		t.Helper()
		select {
		case <-sub.Changed():
			l, ss := sub.Changes()
			t.Errorf("%s: told of a change: layout %v, sessions %v", what, l, ss)
		case <-time.After(200 * time.Millisecond):
		}
	}

	quiet()
	cmd, dir := testShell()
	other, err := c.Create(CreateOptions{Command: cmd, Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	expect("create", "sessions")
	quiet()
	c.SetLayout(json.RawMessage(`{"tabs":[]}`))
	expect("setLayout", "layout")
	c.SetLayout(json.RawMessage(`{"tabs":[]}`))
	none("the same layout again")
	c.Resize(sid, 90, 30)
	expect("resize", "sessions")
	c.Resize(sid, 90, 30)
	none("the same size again")
	if err := sub.LockSize(sid, "phone", 70, 20); err != nil {
		t.Fatal(err)
	}
	expect("lockSize", "sessions")
	sub.UnlockSize(sid, "phone", 0, 0)
	expect("unlockSize", "sessions")
	sub.LockSize(sid, "phone", 70, 20)
	expect("lockSize", "sessions")
	quiet() // its lease ends meanwhile
	if in := sessionInfo(t, c, sid); in.SizeLock != "" {
		t.Fatalf("lease did not end: %+v", in)
	}

	st := sub.Stream(sid, 90, 30)
	defer st.Close()
	st.Resize(90, 30)
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := st.Read(buf); err != nil {
				return
			}
		}
	}()
	quiet()
	title, run := "printf '\\033]2;gorex-t\\007'\n", "while :; do echo t; sleep 0.2; done\n"
	if runtime.GOOS == "windows" {
		title, run = "title gorex-t\r", "ping -n 30 127.0.0.1\r"
	}
	st.Write([]byte(title))
	for deadline := time.Now().Add(5 * time.Second); sessionInfo(t, c, sid).Title != "gorex-t"; {
		expect("title", "sessions")
		if time.Now().After(deadline) {
			t.Fatalf("title %q", sessionInfo(t, c, sid).Title)
		}
	}
	quiet()
	st.Write([]byte(run))
	for deadline := time.Now().Add(5 * time.Second); sessionInfo(t, c, sid).Idle; {
		expect("a program", "sessions")
		if time.Now().After(deadline) {
			t.Fatalf("program %+v", sessionInfo(t, c, sid))
		}
	}

	c.Kill(sid)
	expect("kill", "sessions")
	quiet()
	exit := "exit\n"
	if runtime.GOOS == "windows" {
		exit = "exit\r"
	}
	st2 := sub.Stream(other.ID, 80, 24)
	defer st2.Close()
	st2.Resize(80, 24)
	quiet()
	st2.Write([]byte(exit))
	expect("exit", "sessions")

	old.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if line, err := r.ReadBytes('\n'); err == nil {
		t.Errorf("an old client got %q", line)
	}
}

func sessionInfo(t *testing.T, c *Client, sid string) SessionInfo {
	t.Helper()
	for _, in := range must(c.List()) {
		if in.ID == sid {
			return in
		}
	}
	t.Fatalf("no session %s", sid)
	return SessionInfo{}
}

// TestEventStream checks that a client reads the changes themselves, not
// only that one came.
func TestEventStream(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	if _, err := c.Hello(); err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe(); err != nil {
		t.Fatal(err)
	}
	if err := c.SetLayoutAt([]byte(`{"tabs":[]}`), 0); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-c.Events():
		if ev.Kind != "layout" || ev.Revision == 0 {
			t.Fatalf("event %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event")
	}
}

func TestStatusEvent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("printf")
	}
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	if _, err := c.Hello(); err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe(); err != nil {
		t.Fatal(err)
	}
	in, err := c.Create(CreateOptions{Command: []string{"/bin/sh"}, Dir: "/tmp", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	st := c.Stream(in.ID, 80, 24)
	defer st.Close()
	st.Write([]byte("printf '\\033]7501;state=blocked:kind=question\\007'\n"))
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-c.Events():
			if ev.Kind != "sessions" {
				continue
			}
			for _, es := range ev.Sessions {
				if es.ID == in.ID && es.Status != nil && es.Status.State == "blocked" {
					return
				}
			}
		case <-deadline:
			in := sessionInfo(t, c, in.ID)
			t.Fatalf("no blocked event; statuses %+v", in.Statuses)
		}
	}
}
