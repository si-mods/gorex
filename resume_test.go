package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
		// A quoted program is a string to PowerShell; & runs it.
		{shell: `C:\Program Files\PowerShell\7\pwsh.exe`, args: []string{`C:\Users\u\.local\bin\claude.exe`, "--resume", "8d4ae62f"}, want: `& 'C:\Users\u\.local\bin\claude.exe' --resume 8d4ae62f`, ok: true},
		{shell: "powershell", args: []string{"my tool"}, want: `& 'my tool'`, ok: true},
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
// TestAgentContinue checks the agents whose last conversation a restored
// pane continues, by the names their programs go by.
func TestAgentContinue(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"claude", "claude --continue"},
		{"Claude.exe", "claude --continue"},
		{"/opt/homebrew/bin/omp", "omp --continue"},
		{"pi", "pi --continue"},
		{"codex", "codex resume --last"},
		{"node", ""},
		{"npx", ""},
		{"", ""},
	} {
		if got := continueCommand(c.name); got != c.want {
			t.Errorf("continueCommand(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSnapshotSavesProgram(t *testing.T) {
	a, _ := newTestApp(t)
	p := a.tab().Focus
	p.info.Program, p.info.Args, p.info.Idle = "claude", []string{"claude", "--resume"}, false
	p.info.AgentSession, p.info.AgentConfigDir = "5d454f1b", "/c"
	n := a.savedTab(a.tab()).Root
	if n.Prog != "claude" || len(n.Args) != 2 || n.Args[1] != "--resume" || n.AgentSession != "5d454f1b" || n.AgentConfigDir != "/c" {
		t.Errorf("saved %q %q %q %q", n.Prog, n.Args, n.AgentSession, n.AgentConfigDir)
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
	if p.resume.cont != "claude --continue" {
		t.Errorf("continue command %q", p.resume.cont)
	}
	tt := ui.NewTester(b.view, 1000, 620)
	waitFor(t, tt, "the strip of the last run", func() bool {
		return strings.Contains(strings.Join(tt.Texts(), "\n"), locales.ResumeRun())
	})
	b.runResume(p, p.resume.cmd)
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

// TestResumeHintSession checks that a restored Claude Code pane continues
// the conversation the layout kept, and the last one without it.
func TestResumeHintSession(t *testing.T) {
	const id = "5d454f1b-0c2e-4b8a-9f00-0123456789ab"
	for _, c := range []struct {
		shell string
		node  savedNode
		want  string
	}{
		{"bash", savedNode{Prog: "claude", Args: []string{`C:\bin\claude.exe`}, AgentSession: id}, "claude --resume " + id},
		{"bash", savedNode{Prog: "claude", AgentSession: id, AgentConfigDir: "/c"}, "CLAUDE_CONFIG_DIR=/c claude --resume " + id},
		{"bash", savedNode{Prog: "claude"}, "claude --continue"},
		// A config dir cmd cannot take leaves the last conversation.
		{"cmd", savedNode{Prog: "claude", AgentSession: id, AgentConfigDir: "%x%"}, "claude --continue"},
		{"bash", savedNode{Prog: "codex", AgentSession: id}, "codex resume --last"},
		// The layout is a file anyone may write: what is no UUID is not typed.
		{"bash", savedNode{Prog: "claude", AgentSession: "x; touch pwned"}, "claude --continue"},
		{"pwsh", savedNode{Prog: "claude", AgentSession: "--add"}, "claude --continue"},
		{"pwsh", savedNode{Prog: "claude", AgentSession: id, AgentConfigDir: "x’; Write-Output INJECTED; ’"},
			"$env:CLAUDE_CONFIG_DIR='x’’; Write-Output INJECTED; ’’'; claude --resume " + id},
		{"bash", savedNode{Prog: "claude", AgentSession: id, AgentConfigDir: "x'; touch pwned; '"},
			`CLAUDE_CONFIG_DIR='x'\''; touch pwned; '\''' claude --resume ` + id},
	} {
		h := resumeHintFor(c.shell, &c.node)
		if h == nil || h.cont != c.want {
			t.Errorf("resumeHintFor(%q, %+v) = %+v, want cont %q", c.shell, c.node, h, c.want)
		}
	}
}

// TestClaudeSessionRecorded runs a stand-in for Claude Code, a copy of a
// program that waits named claude, with a config dir and the file Claude
// Code keeps there: the session server finds its conversation, the layout
// keeps it, and a pane restored from that layout resumes it.
func TestClaudeSessionRecorded(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	waitFor(t, tt, "the shell", func() bool {
		return p.info.Idle && p.term != nil && strings.Contains(p.term.Text(), testShell.prompt)
	})
	bin, config := t.TempDir(), t.TempDir()
	src, fake, run := "/bin/sleep", filepath.Join(bin, "claude"), ""
	if runtime.GOOS == "windows" {
		src, fake = filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE"), filepath.Join(bin, "claude.exe")
		run = `set "CLAUDE_CONFIG_DIR=` + config + `" && "` + fake + `" -n 30 127.0.0.1 >nul`
	} else {
		run = "CLAUDE_CONFIG_DIR='" + config + "' '" + fake + "' 30"
	}
	b, err := os.ReadFile(src)
	if err != nil {
		t.Skip(err)
	}
	if err := os.WriteFile(fake, b, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := a.client.Input(p.SID, []byte(run+"\r")); err != nil {
		t.Fatal(err)
	}
	var fg int
	var cwd string
	waitForThat(t, func() bool {
		for _, in := range infos(t, a.client) {
			if in.ID == p.SID && in.Program == "claude" {
				fg, cwd = in.Foreground, in.Dir
				return true
			}
		}
		return false
	}, "claude in the foreground")
	// Ends the stand-in before its directory goes: Windows keeps a running
	// program's file.
	t.Cleanup(func() {
		if pr, err := os.FindProcess(fg); err == nil {
			pr.Kill()
		}
		for i := 0; i < 50 && os.Remove(fake) != nil; i++ {
			time.Sleep(20 * time.Millisecond)
		}
	})
	const id = "5d454f1b-0c2e-4b8a-9f00-0123456789ab"
	os.MkdirAll(filepath.Join(config, "sessions"), 0o755)
	file := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":%q}`, fg, id, cwd)
	if err := os.WriteFile(filepath.Join(config, "sessions", fmt.Sprint(fg)+".json"), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForThat(t, func() bool {
		refresh(a)
		return p.info.AgentSession == id
	}, "the conversation in the session's info")
	n := a.savedTab(a.tab()).Root
	if n.AgentSession != id || n.AgentConfigDir != config {
		t.Fatalf("the layout kept %q in %q", n.AgentSession, n.AgentConfigDir)
	}
	n.SID = "gone"
	r := &App{client: a.client}
	if !r.restore(layout.Layout{Tabs: []layout.Tab{{Root: n}}}, nil) {
		t.Fatal("nothing restored")
	}
	h := r.tab().panes()[0].resume
	want, _ := rex.ClaudeResume(testShell.path, id, config)
	if h == nil || h.cont != want || !strings.Contains(want, id) {
		t.Errorf("restored with %+v, want continue %q", h, want)
	}
}
