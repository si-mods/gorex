package main

import (
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
)

// TestResumeCommand checks the quoting of the command a restored pane runs
// again, by the shell it types it into.
func TestResumeCommand(t *testing.T) {
	for _, c := range []struct {
		shell string
		prog  string
		args  []string
		want  string
		ok    bool
	}{
		{shell: "bash", args: []string{"claude"}, want: "claude", ok: true},
		{shell: "/bin/zsh", args: []string{"claude", "--model", "a b"}, want: `claude --model 'a b'`, ok: true},
		{shell: "bash", args: []string{"say", "it's"}, want: `say 'it'\''s'`, ok: true},
		{shell: "fish", args: []string{`a'b`, `c\d`}, want: `'a\'b' 'c\\d'`, ok: true},
		{shell: "pwsh", args: []string{"say", "it's"}, want: `say 'it''s'`, ok: true},
		{shell: "pwsh.exe", args: []string{"node", "x y"}, want: `node 'x y'`, ok: true},
		{shell: "cmd", args: []string{"node", "x y"}, want: `node "x y"`, ok: true},
		{shell: "cmd.exe", args: []string{"node", "100%"}, ok: false},
		{shell: "cmd.exe", args: []string{`node`, `a"b`}, ok: false},
		{shell: "nu", args: []string{"npm", "run", "dev"}, ok: false},
		{shell: "", args: []string{"npm"}, ok: false},
		// The program's name stands in for argv when the inspection found no
		// arguments, and no program at all gets no command.
		{shell: "bash", prog: "claude", want: "claude", ok: true},
		{shell: "bash", ok: false},
		// An empty argument stays one.
		{shell: "bash", args: []string{"printf", "", "x"}, want: `printf '' x`, ok: true},
		{shell: "cmd", args: []string{"printf", "", "x"}, want: `printf "" x`, ok: true},
	} {
		got, ok := resumeCommand(c.shell, c.prog, c.args)
		if ok != c.ok || got != c.want {
			t.Errorf("resumeCommand(%q, %q, %q) = %q, %v; want %q, %v", c.shell, c.prog, c.args, got, ok, c.want, c.ok)
		}
	}
}

// TestSnapshotSavesProgram checks that the layout keeps what ran in a pane,
// not the shell at its prompt, and nothing for a host's pane.
func TestSnapshotSavesProgram(t *testing.T) {
	a, _ := newTestApp(t)
	p := a.tab().Focus
	p.info.Program, p.info.Args, p.info.Idle = "claude", []string{"claude", "--resume"}, false
	n := a.savedTab(a.tab()).Root
	if n.Prog != "claude" || len(n.Args) != 2 || n.Args[1] != "--resume" {
		t.Errorf("saved %q %q", n.Prog, n.Args)
	}
	p.info.Program, p.info.Args, p.info.Idle = testShell.path, nil, true
	if n := a.savedTab(a.tab()).Root; n.Prog != "" || n.Args != nil {
		t.Errorf("a shell at its prompt saved %q %q", n.Prog, n.Args)
	}
	p.info.Program, p.info.Args, p.info.Idle = "ssh", []string{"ssh", "box"}, false
	p.host = &hostConn{savedHost: savedHost{ID: "h"}}
	if n := a.savedTab(a.tab()).Root; n.Prog != "" {
		t.Errorf("a host's pane saved %q", n.Prog)
	}
}

// TestRestoreOffersRun checks that a pane whose session the layout lost
// offers the command it ran, and that one click types it into the new
// session.
func TestRestoreOffersRun(t *testing.T) {
	a, _ := newTestApp(t)
	l := layout.Layout{Tabs: []layout.Tab{{Root: &layout.Node{
		SID: "gone", Dir: os.TempDir(), Prog: "claude", Args: []string{"claude", "-p"},
	}}}}
	b := &App{client: a.client}
	if !b.restore(l, nil) {
		t.Fatal("nothing restored")
	}
	p := b.tab().panes()[0]
	if !p.restored || p.SID == "gone" {
		t.Fatalf("pane restored %v with session %q", p.restored, p.SID)
	}
	if p.resume == nil || p.resume.cmd == "" {
		t.Fatalf("pane restored with no resume, or %+v", p.resume)
	}
	tt := ui.NewTester(b.view, 1000, 620)
	waitFor(t, tt, "the strip of the last run", func() bool {
		return strings.Contains(strings.Join(tt.Texts(), "\n"), locales.ResumeRun())
	})
	b.runResume(p)
	if p.resume != nil {
		t.Error("the strip stays after running")
	}
	waitForThat(t, func() bool {
		for _, in := range infos(t, a.client) {
			if in.ID == p.SID {
				return !in.LastInput.IsZero()
			}
		}
		return false
	}, "the command to reach the session")
}

// infos reads the sessions of c.
func infos(t *testing.T, c *rex.Client) []rex.SessionInfo {
	t.Helper()
	ins, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	return ins
}

// TestForegroundProgramRecorded checks the inspection the strip rests on:
// a program running in a pane's session reaches the pane's info, and the
// layout keeps it.
func TestForegroundProgramRecorded(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	waitFor(t, tt, "the shell", func() bool {
		return p.info.Idle && p.term != nil && strings.Contains(p.term.Text(), testShell.prompt)
	})
	if err := a.client.Input(p.SID, []byte(testShell.run+"\r")); err != nil {
		t.Fatal(err)
	}
	waitForThat(t, func() bool {
		for _, in := range infos(t, a.client) {
			if in.ID == p.SID {
				return !in.Idle && in.Program != ""
			}
		}
		return false
	}, "the program in the foreground")
	refresh(a)
	n := a.savedTab(a.tab()).Root
	if n.Prog == "" || len(n.Args) == 0 {
		t.Fatalf("the layout kept %q %q", n.Prog, n.Args)
	}
	if n.Prog != p.info.Program {
		t.Errorf("kept %q, the session runs %q", n.Prog, p.info.Program)
	}
}

// TestRestoreOffersRunOfResumedSession checks the session the server starts
// again after a reboot: the pane attaches to the new shell, and offers the
// program the layout saved, since that one ended with the run.
func TestRestoreOffersRunOfResumedSession(t *testing.T) {
	a, tt := newTestApp(t)
	p0 := a.tab().Focus
	waitFor(t, tt, "the shell", func() bool {
		return p0.term != nil && strings.Contains(p0.term.Text(), testShell.prompt)
	})
	refresh(a)
	if !p0.info.Idle {
		t.Fatalf("the shell is %+v", p0.info)
	}
	l := layout.Layout{Tabs: []layout.Tab{{Root: &layout.Node{
		SID: p0.SID, Dir: os.TempDir(), Prog: "claude", Args: []string{"claude"},
	}}}}
	b := &App{client: a.client}
	if !b.restore(l, nil) {
		t.Fatal("nothing restored")
	}
	p := b.tab().panes()[0]
	if p.SID != p0.SID {
		t.Fatalf("pane attached to %q, not the session %q", p.SID, p0.SID)
	}
	if p.resume == nil || p.resume.cmd == "" {
		t.Fatalf("no strip on the resumed session, %+v", p.resume)
	}
}
