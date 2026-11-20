package rexapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// TestReadsDoNotReadTheLayout: a read answers from the published model
// and costs no layout read over the socket — the per-request reread the
// model's event-driven build replaced.
func TestReadsDoNotReadTheLayout(t *testing.T) {
	// The facts timer reads the layout a while after a session's change;
	// keep it out of the count.
	setFor(t, &factsWait, time.Hour)
	f := startFixture(t)
	f.setLayout(emptyStore())
	c := f.dialControl()
	c.hello(apiRevision)
	waitFor(t, "the first publish", func() bool { return f.g.ready.Load() })
	before := f.g.layoutReads.Load()
	callChecked(t, c, "session.list", nil)
	callChecked(t, c, "session.list", nil)
	callChecked(t, c, "session.list", nil)
	if got := f.g.layoutReads.Load() - before; got != 0 {
		t.Errorf("three reads cost %d layout reads over the socket", got)
	}
	if runtime.GOOS == "windows" {
		return // the session's program is a Unix one
	}
	c1, sid, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-c", "sleep 60"}, Cols: 80, Rows: 24})
	before = f.g.layoutReads.Load()
	callChecked(t, c1, "session.view", map[string]any{"session_id": sid})
	callChecked(t, c1, "session.list_blocks", map[string]any{"session_id": sid})
	callChecked(t, c1, "session.describe_block", map[string]any{"session_id": sid, "block_id": block})
	if got := f.g.layoutReads.Load() - before; got != 0 {
		t.Errorf("a view, a block list and a block's description cost %d layout reads over the socket", got)
	}
}

// setFor sets a package setting for the test, and gives the old value
// back when it ends. Set it before the fixture starts: the cleanups run
// in reverse, so the gateway stops before the value goes back.
func setFor[T any](t *testing.T, v *T, to T) {
	t.Helper()
	old := *v
	*v = to
	t.Cleanup(func() { *v = old })
}

// proxiedFixture is startFixture with the gateway's own connection to the
// server through a proxy: up sees each line the gateway sends before it
// passes, and may hold it; a line down returns false for never reaches
// the gateway.
func proxiedFixture(t *testing.T, up func([]byte), down func([]byte) bool) *fixture {
	t.Helper()
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
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s, err := net.Dial("unix", socket)
			if err != nil {
				c.Close()
				return
			}
			go filterLines(s, c, func(line []byte) bool { up(line); return true })
			go filterLines(c, s, down)
		}()
		return path
	})
}

// filterLines copies the lines from r to w that keep returns true for.
func filterLines(w, r net.Conn, keep func([]byte) bool) {
	defer w.Close()
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && keep(line) {
			if _, werr := w.Write(line); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// TestReadBeforeFirstPublish: until the first publish, a read answers
// "not ready" (internal), never a wrong session_not_found; the first
// publish then lets it through. The proxy holds the gateway's first
// layout read, so no publish can happen while the test reads.
func TestReadBeforeFirstPublish(t *testing.T) {
	setFor(t, &commandWait, 100*time.Millisecond)
	release := make(chan struct{})
	f := proxiedFixture(t, func(line []byte) {
		if bytes.Contains(line, []byte(`"op":"getLayout"`)) {
			<-release
		}
	}, func([]byte) bool { return true })
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	c := f.dialControl()
	c.hello(apiRevision)
	e := failure(t, c.request("list", "session.list", nil))
	if e["code"] != "internal" || !strings.Contains(e["message"].(string), "not ready") {
		t.Errorf("session.list before the first publish: %v", e)
	}
	e = failure(t, c.request("attach", "session.attach", map[string]any{"session_id": sessionID("w")}))
	if e["code"] != "internal" || !strings.Contains(e["message"].(string), "not ready") {
		t.Errorf("session.attach before the first publish: %v", e)
	}
	if f.g.ready.Load() {
		t.Fatal("the gateway published with its layout read held")
	}
	close(release)
	waitFor(t, "the first publish", func() bool { return f.g.ready.Load() })
	callChecked(t, c, "session.list", nil)
}

// TestReadAfterConnectionGone: once the client connection to the GoRex
// server is gone, a read gets the 500 a failed layout read gave, not the
// last model, which no event can bring up to date any more.
func TestReadAfterConnectionGone(t *testing.T) {
	f := startFixture(t)
	f.setLayout(emptyStore())
	c := f.dialControl()
	c.hello(apiRevision)
	callChecked(t, c, "session.list", nil)
	f.g.opt.Client.Close()
	<-f.g.opt.Client.Closed()
	e := failure(t, c.request("list", "session.list", nil))
	if e["code"] != "internal" || !strings.Contains(e["message"].(string), "the GoRex server is gone") {
		t.Errorf("session.list after the connection went: %v", e)
	}
}

// TestWaiterSkipsSettle: a registered waiter wakes on the layout event,
// not after settle. settle, the facts timer and the safety tick are all
// far longer than the wait, so only the skip can publish in time.
func TestWaiterSkipsSettle(t *testing.T) {
	setFor(t, &settle, time.Hour)
	setFor(t, &factsWait, time.Hour)
	setFor(t, &safetyEvery, time.Hour)
	f := startFixture(t)
	waitFor(t, "the first publish", func() bool { return f.g.ready.Load() })
	shows := func(m *model) bool { return m.sessionOfSpace("w") != nil }
	if shows(f.g.modelNow()) {
		t.Fatal("the space is there before the test made it")
	}
	got := make(chan bool, 1)
	go func() {
		_, ok := f.g.waitModel(shows, time.Now().Add(5*time.Second))
		got <- ok
	}()
	waitFor(t, "the waiter", func() bool { return f.g.waiters.Load() == 1 })
	store := layout.Store{Sessions: []layout.Workspace{{ID: "w", Name: "work"}}}
	raw, err := json.Marshal(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.desk.SetLayout(raw); err != nil {
		t.Fatal(err)
	}
	if !<-got {
		t.Fatal("the waiter did not see the layout change before its deadline")
	}
}

// TestFactsTimerReadsTheLayout: a sessions event arms the facts timer,
// and its read picks up a change of the layout no layout event told of,
// as none tells of Server.refresh's stamp. The proxy drops the layout
// events, and the safety tick is far off, so only the facts read can
// show it.
func TestFactsTimerReadsTheLayout(t *testing.T) {
	setFor(t, &factsWait, 50*time.Millisecond)
	setFor(t, &safetyEvery, time.Hour)
	var drop atomic.Bool
	f := proxiedFixture(t, func([]byte) {}, func(line []byte) bool {
		return !drop.Load() || !bytes.Contains(line, []byte(`"event":"layout"`))
	})
	// The gateway must hold a layout read: with none, any publish reads
	// it. Give it one after the first publish, and wait for the read its
	// layout event asks for, so no read is still due when the drop starts.
	waitFor(t, "the first publish", func() bool { return f.g.ready.Load() })
	reads := f.g.layoutReads.Load()
	raw, err := json.Marshal(layout.Store{Sessions: []layout.Workspace{{ID: "w", Name: "work"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.desk.SetLayout(raw); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the read of the layout event", func() bool {
		f.g.refreshMu.Lock()
		defer f.g.refreshMu.Unlock()
		return f.g.layoutReads.Load() > reads && f.g.raw != nil
	})
	drop.Store(true)
	if f.g.factsReads.Load() != 0 {
		t.Fatal("the facts timer read with no sessions event")
	}
	// A new session is a sessions event, and the server places it in the
	// layout: a change whose layout event the proxy drops.
	info, err := f.desk.Create(rex.CreateOptions{Command: shellCommand("facts"), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the facts read to show the new session's place", func() bool {
		f.g.refreshMu.Lock()
		defer f.g.refreshMu.Unlock()
		return bytes.Contains(f.g.raw, []byte(info.ID))
	})
	if f.g.factsReads.Load() == 0 {
		t.Error("the layout read showed the change with no facts read")
	}
}

// TestOwnerSeenAtOnce: a gateway-local change publishes before the write
// answers, so another client's next read sees it — no event announces an
// owner, and no publish may lag behind the reply.
func TestOwnerSeenAtOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	f := startFixture(t)
	c1, sid, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-c", "sleep 60"}, Cols: 80, Rows: 24})
	c2 := f.dialControl()
	c2.hello(apiRevision)
	callChecked(t, c2, "session.attach", map[string]any{"session_id": sid})
	name := welcomeID(t, c1.welcome)
	callChecked(t, c1, "session.own_block", map[string]any{
		"session_id": sid, "block_ids": []string{block},
	})
	view := viewOf(t, c2, sid)
	var owner string
	for _, w := range view["windows"].([]any) {
		for _, l := range w.(map[string]any)["layers"].([]any) {
			for _, b := range l.(map[string]any)["blocks"].([]any) {
				owner, _ = b.(map[string]any)["owner"].(string)
			}
		}
	}
	if owner != name {
		t.Fatalf("the other client sees owner %q, want %q", owner, name)
	}
}
