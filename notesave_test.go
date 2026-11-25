package main

import (
	"os"
	"sync"
	"testing"
	"time"
)

// fakeNoteClock is a timer the test fires by hand.
type fakeNoteClock struct {
	mu   sync.Mutex
	fns  map[int]func()
	next int
}

func (c *fakeNoteClock) after(_ time.Duration, f func()) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	id := c.next
	c.fns[id] = f
	return func() {
		c.mu.Lock()
		delete(c.fns, id)
		c.mu.Unlock()
	}
}

// fire runs the timers that are set.
func (c *fakeNoteClock) fire() {
	c.mu.Lock()
	var run []func()
	for id, f := range c.fns {
		run = append(run, f)
		delete(c.fns, id)
	}
	c.mu.Unlock()
	for _, f := range run {
		f()
	}
}

// useNoteSaver swaps the app's note saver for one with a fake clock and a
// counting writer.
func useNoteSaver(t *testing.T) (*fakeNoteClock, *int) {
	t.Helper()
	old := notesDisk
	t.Cleanup(func() { notesDisk = old })
	s := newNoteSaver()
	clock := &fakeNoteClock{fns: map[int]func(){}}
	s.after = clock.after
	real := s.write
	writes := new(int)
	s.write = func(path string, data []byte) error {
		*writes++
		return real(path, data)
	}
	notesDisk = s
	return clock, writes
}

func TestNoteSaveDebounced(t *testing.T) {
	a, _ := newTestApp(t)
	clock, writes := useNoteSaver(t)
	n := a.newNotePane(newNoteID()).ext.(*notePane)
	for _, text := range []string{"h", "he", "hel", "hell", "hello"} {
		n.text = text
		n.persist() // the view calls it every frame
		n.persist()
	}
	if *writes != 0 {
		t.Fatalf("%d writes while typing, want none", *writes)
	}
	if _, err := os.Stat(n.path()); err == nil {
		t.Fatal("the file is written before the delay")
	}
	clock.fire()
	if *writes != 1 {
		t.Fatalf("%d writes after the delay, want 1", *writes)
	}
	if b, _ := os.ReadFile(n.path()); string(b) != "hello" {
		t.Errorf("file %q, want hello", b)
	}
}

func TestNoteFlushOnClose(t *testing.T) {
	a, _ := newTestApp(t)
	useNoteSaver(t)
	p := a.newNotePane(newNoteID())
	noteOf(p).text = "draft"
	noteOf(p).persist()
	noteOf(p).text = "draft two"
	noteOf(p).persist()
	// A note that moves is made again from its file: it has the latest text.
	moved := a.newNotePane(noteOf(p).id)
	if noteOf(moved).text != "draft two" {
		t.Errorf("moved note %q, want the latest text", noteOf(moved).text)
	}
	// The app's quit writes what waits.
	noteOf(moved).text = "last"
	noteOf(moved).persist()
	notesDisk.flushAll()
	if b, _ := os.ReadFile(noteOf(moved).path()); string(b) != "last" {
		t.Errorf("file %q after quit, want last", b)
	}
}

func TestNoteDestroyDropsPendingWrite(t *testing.T) {
	a, _ := newTestApp(t)
	clock, writes := useNoteSaver(t)
	n := a.newNotePane(newNoteID()).ext.(*notePane)
	n.text = "gone"
	n.persist()
	n.destroy(true)
	clock.fire()
	if *writes != 0 {
		t.Errorf("%d writes after the note was deleted", *writes)
	}
	if _, err := os.Stat(n.path()); err == nil {
		t.Error("the file of a deleted note is back")
	}
}
