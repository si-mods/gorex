package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"gorex/internal/rex"
)

// TestPaneStopped keeps the pane of a session whose stream ended while the
// server still has it — the crash — and leaves the pane of one that exited
// to the server, which takes it out of the layout, with no window's help:
// the window follows.
func TestPaneStopped(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	waitFor(t, tt, "the shell", func() bool { return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt) })
	a.newTab(testShell.dir)
	followApp(t, a)
	caughtUp(t, a, tt)

	// The server still has the session: the pane and its tab stay.
	a.paneStopped(p)
	time.Sleep(200 * time.Millisecond)
	if p.closed || len(a.tabs) != 2 {
		t.Fatal("the pane of a session the server has ended")
	}

	// The shell exits: the server takes its pane out, and the window
	// follows; the other tab stays.
	if err := a.client.Input(p.SID, []byte("exit\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the exited pane to go", func() bool { return p.closed && len(a.tabs) == 1 })
	if slices.Contains(liveSIDs(t, a.client), p.SID) {
		t.Error("the exited session is still listed")
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
