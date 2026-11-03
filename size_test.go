package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
)

// TestPaneFollowsAnotherClientsSize: a pane lays the program's output out
// at the size of the session, which another client's terminal sizes while
// it is the authority, not at the pane's. At the pane's size the program's
// next frame, moving the cursor from where it thinks it is, lands
// elsewhere, and what it draws in place covers rows with rows.
func TestPaneFollowsAnotherClientsSize(t *testing.T) {
	a, tt := newTestApp(t)
	tt.Frame()
	p := a.tab().Focus
	if p == nil || p.term == nil {
		t.Fatal("no pane")
	}
	waitFor(t, tt, "the pane's stream", func() bool {
		return p.stream != nil && p.stream.ClientID() != ""
	})
	refresh(a)
	tt.Frame()
	ownCols, ownRows := p.term.Size()
	if ownCols == 0 || p.info.Cols != ownCols || p.info.Rows != ownRows {
		t.Fatalf("the pane's terminal is %dx%d, the session %dx%d", ownCols, ownRows, p.info.Cols, p.info.Rows)
	}

	// A window of another machine shows the session, and the user there
	// takes its size.
	other := a.client.Stream(p.SID, 200, 60)
	defer other.Close()
	if err := other.Resize(200, 60); err != nil {
		t.Fatal(err)
	}
	other.SetFocused(true)
	waitSessionSize(t, tt, a.client, p.SID, "200x60")
	refresh(a)
	waitFor(t, tt, "the pane to take the session's size", func() bool {
		cols, rows := p.term.Size()
		return cols == 200 && rows == 60
	})

	// And it shows the session's screen, not what it laid out at the
	// pane's size.
	res, err := a.client.Capture(p.SID, 0)
	if err != nil {
		t.Fatal(err)
	}
	want, err := renderScreen(res.Screen, res.Cols, res.Rows)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the session's screen", func() bool { return p.term.Text() == want })
}

// TestPaneKeepsItsSizeAsTheAuthority: with no other client attached, the
// pane's terminal follows the pane, as it did before the session's screen
// was what a pane shows, and the session takes the pane's size.
func TestPaneKeepsItsSizeAsTheAuthority(t *testing.T) {
	a, tt := newTestApp(t)
	tt.Frame()
	p := a.tab().Focus
	if p == nil || p.term == nil {
		t.Fatal("no pane")
	}
	waitFor(t, tt, "the pane's stream", func() bool {
		return p.stream != nil && p.stream.ClientID() != ""
	})
	refresh(a)
	tt.Frame()
	before, _ := p.term.Size()
	tt.SetSize(700, 500)
	waitFor(t, tt, "the terminal to follow the pane", func() bool {
		cols, rows := p.term.Size()
		askedCols, askedRows := p.askedSize()
		return cols != before && cols == askedCols && rows == askedRows
	})
	cols, rows := p.term.Size()
	waitSessionSize(t, tt, a.client, p.SID, fmt.Sprintf("%dx%d", cols, rows))
}

// TestPaneReturnsToItsSize: the pane takes the session's size back once
// its own stream is the authority again, which typing there makes it.
func TestPaneReturnsToItsSize(t *testing.T) {
	a, tt := newTestApp(t)
	tt.Frame()
	p := a.tab().Focus
	if p == nil || p.term == nil {
		t.Fatal("no pane")
	}
	waitFor(t, tt, "the pane's stream", func() bool {
		return p.stream != nil && p.stream.ClientID() != ""
	})
	refresh(a)
	tt.Frame()
	ownCols, ownRows := p.askedSize()
	other := a.client.Stream(p.SID, 200, 60)
	defer other.Close()
	if err := other.Resize(200, 60); err != nil {
		t.Fatal(err)
	}
	other.SetFocused(true)
	waitSessionSize(t, tt, a.client, p.SID, "200x60")
	refresh(a)
	waitFor(t, tt, "the pane to take the session's size", func() bool {
		cols, rows := p.term.Size()
		return cols == 200 && rows == 60
	})

	// The user types here: this pane's stream sizes the session again,
	// and the pane shows its own size, with the screen the program drew
	// for it, not what it laid out at the size of another.
	a.focusReq = p
	a.tab().setFocus(p)
	tt.Frame()
	tt.Key(0, ui.KeyX)
	waitFor(t, tt, "the pane to take its size back", func() bool {
		cols, rows := p.term.Size()
		return cols == ownCols && rows == ownRows
	})
	waitSessionSize(t, tt, a.client, p.SID, fmt.Sprintf("%dx%d", ownCols, ownRows))
	waitFor(t, tt, "the session's screen at the pane's size", func() bool {
		res, err := a.client.Capture(p.SID, 0)
		if err != nil || res.Cols != ownCols || res.Rows != ownRows {
			return false
		}
		want, err := renderScreen(res.Screen, res.Cols, res.Rows)
		return err == nil && p.term.Text() == want
	})
}

// TestPaneFollowsALockedSize: a device that holds the session's size
// (lockSize), as a layout sheet does, sizes the program's screen too; the
// pane follows it, since no window's terminal does.
func TestPaneFollowsALockedSize(t *testing.T) {
	a, tt := newTestApp(t)
	tt.Frame()
	p := a.tab().Focus
	if p == nil || p.term == nil {
		t.Fatal("no pane")
	}
	waitFor(t, tt, "the pane's stream", func() bool {
		return p.stream != nil && p.stream.ClientID() != ""
	})
	refresh(a)
	tt.Frame()
	sheet, err := rex.ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sheet.Close() })
	if _, err := sheet.Hello(); err != nil {
		t.Fatal(err)
	}
	if err := sheet.LockSize(p.SID, "sheet", 140, 44); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sheet.UnlockSize(p.SID, "sheet", 0, 0) })
	waitSessionSize(t, tt, a.client, p.SID, "140x44")
	refresh(a)
	waitFor(t, tt, "the pane to take the locked size", func() bool {
		cols, rows := p.term.Size()
		return cols == 140 && rows == 44
	})

	// The device gives the size back without one: the session takes the
	// pane's, and the pane its own, at once.
	ownCols, ownRows := p.askedSize()
	if err := sheet.UnlockSize(p.SID, "sheet", 0, 0); err != nil {
		t.Fatal(err)
	}
	refresh(a)
	waitFor(t, tt, "the pane to take its size back", func() bool {
		cols, rows := p.term.Size()
		return cols == ownCols && rows == ownRows
	})
	waitSessionSize(t, tt, a.client, p.SID, fmt.Sprintf("%dx%d", ownCols, ownRows))
}

// TestPaneShowsTheSessionAfterADrag: a pane's terminal keeps the size it
// had at rest while the pointer drags a divider, and once it takes the
// drag's size the pane shows the session's screen: what the program
// printed for the width before, as a shell redrawing its prompt, reached
// the pane while it still laid the output out at that width, and what
// that left over is not the screen the session shows.
func TestPaneShowsTheSessionAfterADrag(t *testing.T) {
	a, tt := newTestApp(t)
	tt.Frame()
	waitFor(t, tt, "the shell", func() bool {
		p := a.tab().Focus
		return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt)
	})
	settle(tt)
	a.split(false)
	waitFor(t, tt, "2 panes", func() bool { return len(a.tab().panes()) == 2 })
	settle(tt)
	p := a.tab().panes()[0]
	if p.term == nil {
		t.Fatal("no terminal")
	}
	before, _ := p.term.Size()
	d, ok := tt.Find("Divider")
	if !ok {
		t.Fatalf("no divider; texts %q", tt.Texts())
	}
	x, y := d.X+d.W/2, d.Y+d.H/2
	tt.Press(x, y)
	tt.Frame()
	for i := 1; i <= 12; i++ {
		tt.Move(x+float32(i*15), y)
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
	}
	// The program's frames for the grid before, reaching the pane while it
	// still shows that grid: the drag's size takes them away with it.
	p.term.Feed([]byte("stale-write\r\n"))
	tt.Release(x+180, y)
	tt.Frame()
	waitFor(t, tt, "the pane to take the drag's size", func() bool {
		cols, _ := p.term.Size()
		return cols != before
	})
	cols, rows := p.term.Size()
	want := fmt.Sprintf("%dx%d", cols, rows)
	waitSessionSize(t, tt, a.client, p.SID, want)
	waitFor(t, tt, "the session's screen", func() bool {
		res, err := a.client.Capture(p.SID, 0)
		if err != nil || res.Cols != cols || res.Rows != rows {
			return false
		}
		screen, err := renderScreen(res.Screen, res.Cols, res.Rows)
		return err == nil && p.term.Text() == screen
	})
}

// TestDividerHoldEndsAtItsSize: a hold that ends where it began takes no
// size, so the pane asks for no screen after it: the history it has stays
// at the next resize there is, instead of being replaced by the session's
// screen and the last of it.
func TestDividerHoldEndsAtItsSize(t *testing.T) {
	a, tt := newTestApp(t)
	tt.Frame()
	waitFor(t, tt, "the shell", func() bool {
		p := a.tab().Focus
		return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt)
	})
	settle(tt)
	a.split(false)
	waitFor(t, tt, "2 panes", func() bool { return len(a.tab().panes()) == 2 })
	settle(tt)
	p := a.tab().Focus
	if p.term == nil {
		t.Fatal("no terminal")
	}
	// A history longer than a resync replays, which one would cut.
	var history strings.Builder
	for range 3000 {
		history.WriteString("sentinel " + strings.Repeat("x", 40) + "\r\n")
	}
	p.term.Feed([]byte(history.String()))
	tt.Frame()
	if got := p.term.Text(); !strings.Contains(got, "sentinel") {
		t.Fatalf("the terminal did not take the history: %d bytes", len(got))
	}
	d, ok := tt.Find("Divider")
	if !ok {
		t.Fatalf("no divider; texts %q", tt.Texts())
	}
	// A press on the divider, let go where it began.
	x, y := d.X+d.W/2, d.Y+d.H/2
	tt.Press(x, y)
	tt.Frame()
	for range 4 {
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
	}
	tt.Release(x, y)
	tt.Frame()
	settle(tt)
	// A resize it did take, the window's, with no drag behind it.
	beforeC, beforeR := p.term.Size()
	tt.SetSize(1000, 560)
	settle(tt)
	waitFor(t, tt, "the terminal to take the window's size", func() bool {
		c, r := p.term.Size()
		return c != beforeC || r != beforeR
	})
	// A screen mended from the session's waits for a capture, off the
	// frames' clock: give one the time to land if it was asked for.
	for range 30 {
		time.Sleep(50 * time.Millisecond)
		tt.Frame()
	}
	if got := p.term.Text(); !strings.Contains(got, "sentinel") {
		t.Errorf("the history is %d bytes, cut: a hold that took no size mended one", len(got))
	}
}
