package main

import (
	"strings"
	"testing"
	"time"

	"gorex/internal/rex"
)

// TestSessionAlive checks the answer a pane's end rests on: the server has a
// session, or it does not, or it answers nothing.
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
	// server that comes back starts its sessions again. A connection of its
	// own, so that the app's stays the one its windows hold.
	gone, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	gone.Close()
	if !sessionAlive(gone, p.SID) {
		t.Error("a server gone counts its sessions gone")
	}
}

// TestPaneStopped keeps the tab of a pane whose stream ended while the server
// still has its session — the crash — and ends the one whose session the
// server no longer has, as one that exited.
func TestPaneStopped(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	waitFor(t, tt, "the shell", func() bool { return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt) })
	now := func(fn func()) { fn() }

	// The server still has the session: the pane and its tab stay.
	a.paneStopped(p, now)
	time.Sleep(200 * time.Millisecond)
	if p.closed || len(a.tab().panes()) != 1 {
		t.Fatal("the pane of a session the server has ended")
	}

	// The session is gone: the pane ends, and the tab with it.
	if err := a.client.Kill(p.SID); err != nil {
		t.Fatal(err)
	}
	a.paneStopped(p, now)
	waitForThat(t, func() bool { return p.closed }, "the pane of a session gone to end")
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

// TestPaneLabelTitle checks what names a pane: a shell by its name at the
// prompt, and another program by the title it sets, even when it is the
// program the session started with.
func TestPaneLabelTitle(t *testing.T) {
	shell := &Pane{info: rex.SessionInfo{Program: "zsh", Shell: "zsh", Idle: true}, title: "~/repos/gorex"}
	if name, _ := shell.label(); name != "zsh" {
		t.Errorf("a shell is named %q", name)
	}
	mirror := &Pane{info: rex.SessionInfo{Program: "rex-mirror", Shell: "rex-mirror", Idle: true}, title: "Build the site"}
	if name, _ := mirror.label(); name != "Build the site" {
		t.Errorf("a mirror is named %q", name)
	}
	unnamed := &Pane{info: rex.SessionInfo{Program: "rex-mirror", Shell: "rex-mirror", Idle: true}}
	if name, _ := unnamed.label(); name != "rex-mirror" {
		t.Errorf("a mirror without a title is named %q", name)
	}
}

// TestPaneProgram checks how a pane's program shows: what it reported,
// else a shell's own tile, else the executable.
func TestPaneProgram(t *testing.T) {
	shell := &Pane{info: rex.SessionInfo{Program: "zsh", Shell: "zsh", Idle: true}}
	if got := shell.program().Name; got != "zsh" {
		t.Errorf("a shell shows as %q", got)
	}
	mirror := &Pane{info: rex.SessionInfo{
		Program: "rex-mirror", Shell: "rex-mirror", Idle: true,
		Status: &rex.ProgramStatus{State: "working", App: "omp"},
	}}
	if got := mirror.program().Name; got != "omp" {
		t.Errorf("a mirror of omp shows as %q", got)
	}
	claude := &Pane{info: rex.SessionInfo{
		Program: "rex-mirror", Shell: "rex-mirror", Idle: true,
		Status: &rex.ProgramStatus{State: "done", App: "claude-code"},
	}}
	if got := claude.program().Name; got != "Claude Code" {
		t.Errorf("a mirror of Claude Code shows as %q", got)
	}
}
