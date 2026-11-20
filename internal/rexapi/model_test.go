package rexapi

import (
	"runtime"
	"testing"

	"gorex/internal/rex"
)

// TestReadsDoNotReadTheLayout: a read answers from the published model
// and costs no layout read over the socket — the per-request reread the
// model's event-driven build replaced.
func TestReadsDoNotReadTheLayout(t *testing.T) {
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
