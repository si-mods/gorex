//go:build darwin || linux

package rex

import (
	"net"
	"sync"
	"testing"
	"time"
)

// settleFor shortens resizeSettle for a test, and restores it after.
func settleFor(t *testing.T, d time.Duration) {
	t.Helper()
	old := resizeSettle.set(d)
	t.Cleanup(func() { resizeSettle.set(old) })
}

// watchResizes counts the sizes the terminal takes; it returns the calls
// so far.
func watchResizes(t *testing.T) func() [][2]int {
	t.Helper()
	var mu sync.Mutex
	var calls [][2]int
	resized = func(cols, rows int) {
		mu.Lock()
		calls = append(calls, [2]int{cols, rows})
		mu.Unlock()
	}
	t.Cleanup(func() { resized = nil })
	return func() [][2]int {
		mu.Lock()
		defer mu.Unlock()
		return append([][2]int(nil), calls...)
	}
}

// newResizeSession starts a shell to resize, at 80×24.
func newResizeSession(t *testing.T) *session {
	t.Helper()
	s, err := newSession("resize", CreateOptions{Command: []string{"/bin/sh"}, Cols: 80, Rows: 24}, "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.kill)
	return s
}

func appliedSize(t *testing.T, s *session) (int, int) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows
}

// TestSessionResizeSettles: a drag resizes the terminal once, at its last
// size, not once per step.
func TestSessionResizeSettles(t *testing.T) {
	settleFor(t, 50*time.Millisecond)
	s := newResizeSession(t)
	calls := watchResizes(t)
	for i := 1; i <= 10; i++ {
		s.resize(80+i, 24+i)
	}
	if cols, rows := appliedSize(t, s); cols != 80 || rows != 24 {
		t.Fatalf("size applied before it settled: %d×%d", cols, rows)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(calls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// Nothing more may follow.
	time.Sleep(100 * time.Millisecond)
	if got := calls(); len(got) != 1 || got[0] != [2]int{90, 34} {
		t.Fatalf("resizes %v, want one 90×34", got)
	}
	if cols, rows := appliedSize(t, s); cols != 90 || rows != 34 {
		t.Fatalf("size %d×%d, want 90×34", cols, rows)
	}
}

// TestSessionResizeNowCancelsPending: an explicit size is taken at once,
// and the size of the drag it interrupts never reaches the terminal.
func TestSessionResizeNowCancelsPending(t *testing.T) {
	settleFor(t, 5*time.Second)
	s := newResizeSession(t)
	calls := watchResizes(t)
	s.resize(100, 30)
	s.resizeNow(120, 40)
	if got := calls(); len(got) != 1 || got[0] != [2]int{120, 40} {
		t.Fatalf("resizes %v, want 120×40 at once", got)
	}
	if cols, rows := appliedSize(t, s); cols != 120 || rows != 40 {
		t.Fatalf("size %d×%d, want 120×40", cols, rows)
	}
	s.resize(100, 30)
	conn, peer := net.Pipe()
	defer conn.Close()
	defer peer.Close()
	s.lockSize("owner", 60, 20, conn)
	if got := calls(); len(got) != 2 || got[1] != [2]int{60, 20} {
		t.Fatalf("resizes %v, want 60×20 at once", got)
	}
	s.resize(100, 30)
	if !s.lockedResize("owner", 70, 25) {
		t.Fatal("lockedResize lost the lock")
	}
	if got := calls(); len(got) != 3 || got[2] != [2]int{70, 25} {
		t.Fatalf("resizes %v, want 70×25 at once", got)
	}
	s.unlockSize("owner", 50, 15)
	if got := calls(); len(got) != 4 || got[3] != [2]int{50, 15} {
		t.Fatalf("resizes %v, want 50×15 at once", got)
	}
	// The drag's pending size was dropped by the lock, and never lands.
	time.Sleep(50 * time.Millisecond)
	if got := calls(); len(got) != 4 {
		t.Fatalf("resizes %v, want four", got)
	}
}

// TestSessionResizeAfterKill: a size still waiting when the session ends
// never reaches a closed terminal.
func TestSessionResizeAfterKill(t *testing.T) {
	settleFor(t, 200*time.Millisecond)
	s := newResizeSession(t)
	calls := watchResizes(t)
	s.resize(100, 30)
	s.kill()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not end")
	}
	time.Sleep(400 * time.Millisecond)
	if got := calls(); len(got) != 0 {
		t.Fatalf("resizes after the kill: %v", got)
	}
	// Nor is a new one taken.
	s.resize(90, 28)
	time.Sleep(400 * time.Millisecond)
	if got := calls(); len(got) != 0 {
		t.Fatalf("resizes after the kill: %v", got)
	}
}
