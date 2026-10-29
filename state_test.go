package main

import (
	"testing"
	"time"
)

// TestSessionAlive checks the answer a pane's end rests on: the server has a
// session, or it does not, or it answers nothing. A stream that ends while
// the server still has its session — a server that crashed, or one
// restarting — leaves the pane open, so that it attaches to the session the
// server starts in its place.
func TestSessionAlive(t *testing.T) {
	a, _ := newTestApp(t)
	p := a.tab().Focus
	if p.SID == "" {
		t.Fatal("the pane has no session")
	}
	if !sessionAlive(a.client, p.SID) {
		t.Error("a session the server has counts gone")
	}
	if sessionAlive(a.client, "no such session") {
		t.Error("a session the server does not have counts alive")
	}
	if err := a.client.Kill(p.SID); err != nil {
		t.Fatal(err)
	}
	waitForThat(t, func() bool { return !sessionAlive(a.client, p.SID) }, "the killed session to be gone")

	// A connection that went away answers nothing, which ends nothing: the
	// server that comes back starts its sessions again.
	c := a.client
	c.Close()
	if !sessionAlive(c, p.SID) {
		t.Error("a server gone counts its sessions gone")
	}
}

// waitForThat runs until cond holds, or fails after a few seconds. Unlike
// waitFor it draws no frame: what it waits for is what the server says.
func waitForThat(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
