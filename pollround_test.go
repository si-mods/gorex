package main

import (
	"testing"
	"time"
)

// pollRounds starts the poll of this machine's server for the windows of
// st and returns round, which runs the poll's next round on the test's
// goroutine and tells whether it drew the windows, and quiet, which runs
// rounds until one leaves the list as it was.
func pollRounds(t *testing.T, a *App) (round func() bool, quiet func()) {
	t.Helper()
	st := a.st
	client, _ := countLists(t)
	stop := make(chan struct{})
	st.polling = stop
	rounds, done := make(chan func()), make(chan struct{})
	go func() {
		defer close(done)
		st.poll(client, stop, func(fn func()) {
			select {
			case rounds <- fn:
			case <-stop:
			}
		})
	}()
	t.Cleanup(func() {
		close(stop)
		<-done
		st.polling = nil
		quit(st)
	})
	posted := func() int {
		a.postMu.Lock()
		defer a.postMu.Unlock()
		return len(a.posted)
	}
	round = func() bool {
		t.Helper()
		select {
		case fn := <-rounds:
			was := posted()
			fn()
			return posted() != was
		case <-time.After(5 * time.Second):
			t.Fatal("no round")
		}
		return false
	}
	quiet = func() {
		t.Helper()
		for i := 0; round(); i++ {
			if i == 20 {
				t.Fatal("every round draws the windows")
			}
		}
	}
	return round, quiet
}

// TestQuietPollLeavesPanes checks that a poll round whose list did not
// change gives no window the list again (#843): the panes keep what they
// hold, which a round of the same list would have written over.
func TestQuietPollLeavesPanes(t *testing.T) {
	a, _ := newTestApp(t)
	b := a.st.openWindow(a.st.newTestSpace())
	round, quiet := pollRounds(t, a)
	if !round() {
		t.Fatal("the first round drew nothing")
	}
	quiet()
	pa, pb := a.tab().Focus, b.tab().Focus
	for range 20 {
		pa.info.Program, pb.info.Program = "mark", "mark"
		if round() {
			continue // the list changed: the windows take it
		}
		if pa.info.Program != "mark" || pb.info.Program != "mark" {
			t.Fatalf("a round of the same list applied it to the windows: %q %q", pa.info.Program, pb.info.Program)
		}
		return
	}
	t.Fatal("no round left the list as it was")
}

// TestQuietPollSavesAndRetries checks that a poll round whose list did
// not change still saves a layout changed a while ago, and still brings
// a notification's clicked pane to the front.
func TestQuietPollSavesAndRetries(t *testing.T) {
	a, _ := newTestApp(t)
	st := a.st
	round, quiet := pollRounds(t, a)
	if !round() {
		t.Fatal("the first round drew nothing")
	}
	quiet()
	p := a.tab().Focus
	for range 20 {
		a.saveDue, a.lastSave = true, time.Now().Add(-2*time.Second)
		st.clicked, st.clickedAt = "gorex||"+p.SID+"|x", time.Now()
		if round() {
			continue
		}
		// The click's focus may change the layout again (saveDue): the
		// save shows in its time.
		if time.Since(a.lastSave) > time.Second {
			t.Error("a round of the same list did not save the changed layout")
		}
		if st.clicked != "" {
			t.Error("a round of the same list did not retry the notification's click")
		}
		return
	}
	t.Fatal("no round left the list as it was")
}
