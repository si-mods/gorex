package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
)

func TestProgressLine(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	line := func() (ui.AccessInfo, bool) {
		for range 5 {
			tt.Advance(time.Second)
			tt.Frame()
		}
		return tt.AccessByRole(ui.RoleProgress, "")
	}
	if _, ok := line(); ok {
		t.Fatal("a line without progress")
	}
	// The program's report, through the terminal.
	p.term.Feed([]byte("\x1b]9;4;1;40\x1b\\"))
	l, ok := line()
	if !ok {
		t.Fatal("no line for a report of 40%")
	}
	p.term.Feed([]byte("\x1b]9;4;3\x1b\\"))
	full, ok := line()
	if !ok {
		t.Fatal("no line while the program cannot tell")
	}
	if r := l.Bounds.W / full.Bounds.W; r < 0.38 || r > 0.42 {
		t.Errorf("40%% is %.2f of the line (%v, %v)", r, l.Bounds, full.Bounds)
	}
	if full.Bounds.H != 2 {
		t.Errorf("the line is %v high", full.Bounds.H)
	}
	// A record's percent shows while the program works, under its report.
	p.info.Statuses = []rex.ProgramStatus{{State: "working", Progress: 70, Updated: time.Now()}}
	if v, _, _ := p.shownProgress(); v != -1 {
		t.Errorf("the record went over the report: %v", v)
	}
	p.term.Feed([]byte("\x1b]9;4;0\x1b\\"))
	if _, ok := line(); !ok {
		t.Fatal("no line for the record's 70%")
	}
	if v, failed, _ := p.shownProgress(); v != 0.7 || failed {
		t.Errorf("record's progress = %v, failed %v", v, failed)
	}
	p.info.Statuses[0].State = "done"
	if _, ok := line(); ok {
		t.Error("a line once the program is done")
	}
	p.term.Feed([]byte("\x1b]9;4;2\x1b\\"))
	line()
	if v, failed, ok := p.shownProgress(); v != -1 || !failed || !ok {
		t.Errorf("an error shows %v, failed %v, ok %v", v, failed, ok)
	}
}
