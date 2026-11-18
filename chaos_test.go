package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// The scenarios below break the app on purpose and check that the
// invariants of docs/invariants.md hold. docs/chaos.md names each one,
// the invariant it attacks and the evidence it leaves. Their screenshots
// go to GOREX_SHOTS when it names a directory; `chaos.sh` sets it.

// TestChaosConnectionLoss drops the app's connection to the server while
// a prompt shows, and checks that the window comes back on the same
// session, with the screen it had (docs/invariants.md 1 and 3).
func TestChaosConnectionLoss(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	waitFor(t, tt, "the shell", func() bool {
		return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt)
	})
	if err := a.client.Input(p.SID, []byte("echo chaos-marker\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the marker", func() bool {
		return strings.Contains(p.term.Text(), "chaos-marker")
	})
	sid, st := p.SID, a.st
	old := st.client
	old.Close()
	<-old.Closed() // its reader ends after Close returns
	st.openNew()
	t.Cleanup(func() { st.client.Shutdown() })
	if a.client != st.client {
		t.Fatal("the window kept the closed connection")
	}
	// The pane is new: the window rebuilt it on the new connection, and
	// its terminal holds the screen the session had. No PNG: the window
	// of the test repaints no pane it rebound, so the shot would show the
	// harness, not this state.
	waitFor(t, tt, "the marker after the reconnect", func() bool {
		for _, tab := range a.tabs {
			for _, pn := range tab.panes() {
				if pn.SID != sid || pn.term == nil || pn.stream == nil || pn.stream.ClientID() == "" {
					continue
				}
				return strings.Contains(pn.term.Text(), "chaos-marker")
			}
		}
		return false
	})
	quit(st)
}

// TestChaosResizeStorm changes the window's size without pause, then
// checks that the pane settles on one size, its own, and that the session
// takes it (docs/invariants.md 4).
func TestChaosResizeStorm(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	waitFor(t, tt, "the pane's stream", func() bool {
		return p.stream != nil && p.stream.ClientID() != ""
	})
	sizes := [][2]int{{1280, 800}, {560, 400}, {900, 560}, {420, 300}, {1000, 620}}
	for i := range 40 {
		s := sizes[i%len(sizes)]
		tt.SetSize(s[0], s[1])
		tt.Frame()
	}
	tt.SetSize(900, 560)
	waitFor(t, tt, "the terminal to follow the last size", func() bool {
		cols, rows := p.term.Size()
		askedCols, askedRows := p.askedSize()
		return cols != 0 && cols == askedCols && rows == askedRows
	})
	cols, rows := p.term.Size()
	waitSessionSize(t, tt, a.client, p.SID, fmt.Sprintf("%dx%d", cols, rows))
	chaosShot(t, tt, "chaos-resize-storm-dark.png")
}

// TestChaosBurstOutput runs a program that writes lines as fast as the
// pty takes them, and checks that the last line reaches the pane, which
// means the burst was relayed in order and the app kept drawing
// (docs/invariants.md 3 and 5).
func TestChaosBurstOutput(t *testing.T) {
	a, _ := newTestApp(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(exe)
	if err != nil {
		t.Skip(err)
	}
	dir, err := os.MkdirTemp("", "gorex-burst")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	name := "spew"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	helper := filepath.Join(dir, name)
	if err := os.WriteFile(helper, src, 0o755); err != nil {
		t.Fatal(err)
	}
	const lines = 20000
	// The helper is a copy of this binary, which floods as TestHelperSpew:
	// a copy of a system binary may not run on macOS.
	in, err := a.client.Create(rex.CreateOptions{
		Command: []string{helper, "-test.run=^TestHelperSpew$"},
		Dir:     dir,
		Env:     []string{fmt.Sprintf("GOREX_HELPER_LINES=%d", lines)},
	})
	if err != nil {
		t.Fatal(err)
	}
	l := layout.Layout{Tabs: []layout.Tab{{Root: &layout.Node{SID: in.ID, Dir: dir}}}}
	b := &App{client: a.client}
	if !b.reconcile(l) {
		t.Fatal("nothing restored")
	}
	p := b.tab().panes()[0]
	tt := ui.NewTester(b.view, 1000, 620)
	last := fmt.Sprintf("line %05d", lines)
	start := time.Now()
	waitFor(t, tt, "the last line", func() bool {
		return p.term != nil && strings.Contains(p.term.Text(), last)
	})
	t.Logf("%d lines reached the pane in %s", lines, time.Since(start))
	chaosShot(t, tt, "chaos-burst-output-dark.png")
	// The flood waits to be killed, so that its pane stays for the shot.
	if err := a.client.Kill(in.ID); err != nil {
		t.Fatal(err)
	}
	waitForThat(t, func() bool {
		for _, x := range infos(t, a.client) {
			if x.ID == in.ID {
				return false
			}
		}
		return true
	}, "the flood to end")
	os.RemoveAll(dir)
}

// TestHelperSpew is the flooding program of TestChaosBurstOutput: it
// writes GOREX_HELPER_LINES lines, then waits to be killed. It is no test
// otherwise.
func TestHelperSpew(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("GOREX_HELPER_LINES"))
	if err != nil || n <= 0 {
		return
	}
	for i := 1; i <= n; i++ {
		fmt.Printf("line %05d\n", i)
	}
	time.Sleep(time.Minute)
}

// chaosShot writes the frame to GOREX_SHOTS when it names a directory, in
// dark, where a person compares it with the last run's.
func chaosShot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("GOREX_SHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tt.SetDark(true)
	for range 2 {
		tt.Frame()
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}
