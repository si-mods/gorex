package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorex/internal/rex"

	"github.com/egoist/mygo/ui"
)

// sessionSizeOf returns the size the server last gave the session.
func sessionSizeOf(t *testing.T, c *rex.Client, sid string) string {
	t.Helper()
	infos, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range infos {
		if in.ID == sid {
			return fmt.Sprintf("%dx%d", in.Cols, in.Rows)
		}
	}
	t.Fatalf("no session %s", sid)
	return ""
}

// waitSessionSize runs frames until the session has the size want.
func waitSessionSize(t *testing.T, tt *ui.Tester, c *rex.Client, sid, want string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; {
		if got := sessionSizeOf(t, c, sid); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s, want %s", sessionSizeOf(t, c, sid), want)
		}
		time.Sleep(20 * time.Millisecond)
		tt.Frame()
	}
}

// TestFocusFollowsWindow: the panes shown with the window's focus claim
// the session's size there, and only as the focus they claim changes: a
// device that types meanwhile keeps the size until the window's focus
// moves.
func TestFocusFollowsWindow(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	waitFor(t, tt, "the shell", func() bool { return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt) })
	// A device of another machine types into the pane's session: the
	// session follows it.
	// A device that keeps its connection alive, as another machine's does:
	// its attach is not one of the app's windows here.
	c2, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c2.Close() })
	if _, err := c2.Hello(); err != nil {
		t.Fatal(err)
	}
	pc, err := c2.AttachConn(rex.Attach{SID: p.SID, Cols: 130, Rows: 44})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if _, err := pc.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	waitSessionSize(t, tt, a.client, p.SID, "130x44")
	// The active tab changes: the pane shown claims the size, at once.
	cols, rows := p.term.Size()
	paneSize := fmt.Sprintf("%dx%d", cols, rows)
	tt.Frame()
	a.newTab(os.TempDir())
	tt.Frame()
	a.selectTab(0)
	tt.Frame()
	waitSessionSize(t, tt, a.client, p.SID, paneSize)
	// The device types again: the frames that follow do not claim the
	// size back, the panes' focus did not change.
	if _, err := pc.Write([]byte("y")); err != nil {
		t.Fatal(err)
	}
	waitSessionSize(t, tt, a.client, p.SID, "130x44")
	for range 5 {
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	if got := sessionSizeOf(t, a.client, p.SID); got != "130x44" {
		t.Fatalf("frames claimed the size again: %s", got)
	}
	// Switching the tab away and back claims it again.
	a.selectTab(1)
	tt.Frame()
	a.selectTab(0)
	tt.Frame()
	waitSessionSize(t, tt, a.client, p.SID, paneSize)
}

// TestFocusNeedsPresence: a window that kept the focus while the user was
// away from the machine claims no session's size; the user coming back
// claims it again, though the focus did not change.
func TestFocusNeedsPresence(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	waitFor(t, tt, "the shell", func() bool { return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt) })
	cols, rows := p.term.Size()
	paneSize := fmt.Sprintf("%dx%d", cols, rows)
	// The user leaves the machine: no input comes to it.
	testPresent.Store(false)
	waitFor(t, tt, "the user to leave", func() bool { return !a.focusPresent })
	// A device of another machine types into the pane's session: the
	// session follows it.
	c2, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c2.Close() })
	if _, err := c2.Hello(); err != nil {
		t.Fatal(err)
	}
	pc, err := c2.AttachConn(rex.Attach{SID: p.SID, Cols: 130, Rows: 44})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if _, err := pc.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	waitSessionSize(t, tt, a.client, p.SID, "130x44")
	// The window kept the focus, but nobody is at the machine: the frames
	// claim nothing.
	for range 5 {
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	if got := sessionSizeOf(t, a.client, p.SID); got != "130x44" {
		t.Fatalf("an absent user's window sized the session to %s", got)
	}
	// The user comes back: the frames claim the size again.
	testPresent.Store(true)
	waitSessionSize(t, tt, a.client, p.SID, paneSize)
}
