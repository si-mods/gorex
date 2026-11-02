package main

import (
	"fmt"
	"testing"

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
	// and the pane shows its own size.
	if _, err := p.stream.Write([]byte(" ")); err != nil {
		t.Fatal(err)
	}
	waitSessionSize(t, tt, a.client, p.SID, fmt.Sprintf("%dx%d", ownCols, ownRows))
	refresh(a)
	waitFor(t, tt, "the pane to take its size back", func() bool {
		cols, rows := p.term.Size()
		return cols == ownCols && rows == ownRows
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
}
