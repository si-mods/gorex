//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorex/internal/layout"
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

func TestShareLayoutGate(t *testing.T) {
	s := &Server{layout: []byte(`{"tabs":[]}`), rem: remote{cfg: RemoteConfig{ShareSessions: true}}}
	req := Request{ID: 1, Op: "getLayout"}
	if res := s.request(req, "peer", nil); res.Error == "" {
		t.Fatal("layout shared while off")
	}
	s.rem.cfg.ShareLayout = true
	res := s.request(req, "peer", nil)
	if res.Error != "" || !strings.Contains(string(res.Data), "tabs") {
		t.Fatalf("shared layout: %q %q", res.Error, res.Data)
	}
}

func TestClientShareSessions(t *testing.T) {
	s := &Server{rem: remote{cfg: RemoteConfig{Allowed: []RemoteClient{
		{PublicKey: "a", ShareSessions: true, ShareLayout: true},
		{PublicKey: "b"},
	}}}}
	ss := &session{owner: "c"}
	if !s.remoteShareLayout("a") || s.remoteShareLayout("b") {
		t.Error("per-client layout sharing")
	}
	if !s.mayUse("a", ss) {
		t.Error("a client that shares them does not see another's session")
	}
	if s.mayUse("b", ss) || s.mayUse("d", ss) {
		t.Error("a client that does not share them sees another's session")
	}
}

func TestFilterLayout(t *testing.T) {
	raw := []byte(`{"version":2,"sessions":[{"id":"w","name":"w","active":0,"tabs":[{"root":{"a":{"sid":"a"},"b":{"sid":"b"}},"focus":0},{"root":{"sid":"h","host":"other"}}]}],"windows":["w"]}`)
	s := &Server{
		sessions: map[string]*session{"a": {id: "a", owner: "p"}, "b": {id: "b", owner: "other"}},
		rem:      remote{cfg: RemoteConfig{Allowed: []RemoteClient{{PublicKey: "p"}}}},
	}
	got, err := s.filterLayout(raw, "p")
	if err != nil {
		t.Fatal(err)
	}
	var st layout.Store
	if err := json.Unmarshal(got, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Sessions) != 1 || len(st.Sessions[0].Tabs) != 2 {
		t.Fatalf("filtered %s", got)
	}
	root := st.Sessions[0].Tabs[0].Root
	if root == nil || root.A == nil || root.A.SID != "a" || root.B == nil || !root.B.Unavailable {
		t.Fatalf("panes %+v", root)
	}
	if !st.Sessions[0].Tabs[1].Root.Unavailable {
		t.Fatalf("placeholder %+v", st.Sessions[0].Tabs[1].Root)
	}
	if bytes.Contains(got, []byte(`"sid":"b"`)) || bytes.Contains(got, []byte(`"host":"other"`)) {
		t.Fatalf("leaked %s", got)
	}
}

func TestFilterLayoutFocusAcrossBrowser(t *testing.T) {
	// A tab of a terminal, a browser pane and a terminal, focused on the
	// last: the browser leaves the peer's layout, and the focus follows
	// the terminal to its new place.
	raw := []byte(`{"version":2,"sessions":[{"id":"w","name":"w","tabs":[{"root":{"a":{"sid":"a"},"b":{"a":{"url":"https://example.com/"},"b":{"sid":"b"}}},"focus":2}]}],"windows":["w"]}`)
	s := &Server{
		sessions: map[string]*session{"a": {id: "a", owner: "p"}, "b": {id: "b", owner: "p"}},
		rem:      remote{cfg: RemoteConfig{Allowed: []RemoteClient{{PublicKey: "p"}}}},
	}
	got, err := s.filterLayout(raw, "p")
	if err != nil {
		t.Fatal(err)
	}
	var st layout.Store
	if err := json.Unmarshal(got, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Sessions) != 1 || len(st.Sessions[0].Tabs) != 1 {
		t.Fatalf("filtered %s", got)
	}
	tab := st.Sessions[0].Tabs[0]
	if tab.Root == nil || tab.Root.A == nil || tab.Root.A.SID != "a" || tab.Root.B == nil || tab.Root.B.SID != "b" {
		t.Fatalf("panes %+v", tab.Root)
	}
	if tab.Focus != 1 {
		t.Errorf("focus %d, want the second terminal", tab.Focus)
	}
	if bytes.Contains(got, []byte("example.com")) {
		t.Fatalf("leaked the browser pane: %s", got)
	}
}

func TestFilterLayoutFocusPastUnavailable(t *testing.T) {
	// The focused terminal is after one the peer may not use: its place
	// counts the sessions left, as the peer's own count does.
	raw := []byte(`{"version":2,"sessions":[{"id":"w","name":"w","tabs":[{"root":{"a":{"sid":"a"},"b":{"a":{"sid":"b"},"b":{"sid":"c"}}},"focus":2}]}],"windows":["w"]}`)
	s := &Server{
		sessions: map[string]*session{"a": {id: "a", owner: "p"}, "b": {id: "b", owner: "other"}, "c": {id: "c", owner: "p"}},
		rem:      remote{cfg: RemoteConfig{Allowed: []RemoteClient{{PublicKey: "p"}}}},
	}
	got, err := s.filterLayout(raw, "p")
	if err != nil {
		t.Fatal(err)
	}
	var st layout.Store
	if err := json.Unmarshal(got, &st); err != nil {
		t.Fatal(err)
	}
	tab := st.Sessions[0].Tabs[0]
	if tab.Focus != 1 {
		t.Errorf("focus %d, want the second of the peer's sessions", tab.Focus)
	}
}

// TestFilterLayoutFloats checks that a peer's layout keeps the floats it
// may use, drops those it cannot, and moves the focus over them.
func TestFilterLayoutFloats(t *testing.T) {
	raw := []byte(`{"version":2,"sessions":[{"id":"w","name":"w","tabs":[{"root":{"sid":"a"},"focus":0,"floats":[{"pane":{"sid":"a"},"x":0.1},{"pane":null},{"pane":{"sid":"b"}}],"focusFloat":2}]}],"windows":["w"]}`)
	s := &Server{
		sessions: map[string]*session{"a": {id: "a", owner: "p"}, "b": {id: "b", owner: "other"}},
		rem:      remote{cfg: RemoteConfig{Allowed: []RemoteClient{{PublicKey: "p"}}}},
	}
	got, err := s.filterLayout(raw, "p")
	if err != nil {
		t.Fatal(err)
	}
	var st layout.Store
	if err := json.Unmarshal(got, &st); err != nil {
		t.Fatal(err)
	}
	tab := st.Sessions[0].Tabs[0]
	if len(tab.Floats) != 2 {
		t.Fatalf("floats %+v", tab.Floats)
	}
	if tab.Floats[0].Pane == nil || tab.Floats[0].Pane.SID != "a" || tab.Floats[0].X != 0.1 {
		t.Errorf("first float %+v", tab.Floats[0])
	}
	if !tab.Floats[1].Pane.Unavailable {
		t.Errorf("a pane of another's float is not unavailable: %+v", tab.Floats[1])
	}
	if tab.FocusFloat != 0 {
		t.Errorf("focus float %d, want 0 after the focused float went", tab.FocusFloat)
	}
	if bytes.Contains(got, []byte(`"sid":"b"`)) {
		t.Fatalf("leaked %s", got)
	}
}

func TestFilterLayoutMirror(t *testing.T) {
	raw := []byte(`{"version":2,"sessions":[{"id":"w","name":"w","tabs":[{"root":{"sid":"a"}}]}],"windows":["w"],"tabs":[{"root":{"sid":"a"}},{"root":{"sid":"b"}}]}`)
	s := &Server{
		sessions: map[string]*session{"a": {id: "a", owner: "p"}, "b": {id: "b", owner: "other"}},
		rem:      remote{},
	}
	got, err := s.filterLayout(raw, "p")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte(`"sid":"b"`)) {
		t.Fatalf("mirror leaked: %s", got)
	}
	if !bytes.Contains(got, []byte(`"sid":"a"`)) {
		t.Fatalf("allowed pane lost: %s", got)
	}
}
