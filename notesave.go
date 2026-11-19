package main

import (
	"log"
	"os"
	"sync"
	"time"
)

// noteSaveDelay is how long a note waits after its last edit before its
// file is written.
const noteSaveDelay = 400 * time.Millisecond

// noteSaver writes notes' files off the UI thread. An edit only records the
// text and restarts a timer; when the timer fires, the latest text is
// written. Each note has its own lock for the disk work, so its writes keep
// their order and a flush waits for one that runs.
type noteSaver struct {
	// after starts a timer that calls f once after d; tests inject their
	// own. write puts data in the file of the note; tests count its calls.
	after func(d time.Duration, f func()) (stop func())
	write func(path string, data []byte) error

	mu    sync.Mutex
	notes map[string]*noteQueue
}

// noteQueue is the unwritten text of one note.
type noteQueue struct {
	io    sync.Mutex // held while the file is written
	path  string
	text  string
	dirty bool
	stop  func() // stops the timer, nil when none runs
}

func newNoteSaver() *noteSaver {
	return &noteSaver{
		after: func(d time.Duration, f func()) func() {
			t := time.AfterFunc(d, f)
			return func() { t.Stop() }
		},
		write: func(path string, data []byte) error {
			if err := os.MkdirAll(noteDir(), 0o700); err != nil {
				return err
			}
			return writeFileAtomic(path, data, 0o600)
		},
		notes: map[string]*noteQueue{},
	}
}

// notesDisk is the saver of the app's notes.
var notesDisk = newNoteSaver()

// schedule records text as the latest of the note id and writes it after
// the delay with no later edit.
func (s *noteSaver) schedule(id, path, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.notes[id]
	if q == nil {
		q = &noteQueue{path: path}
		s.notes[id] = q
	}
	q.text, q.dirty = text, true
	if q.stop != nil {
		q.stop()
	}
	q.stop = s.after(noteSaveDelay, func() { s.flush(id) })
}

// flush writes the latest text of the note id now, if it is unwritten, and
// returns when it is on disk.
func (s *noteSaver) flush(id string) {
	s.mu.Lock()
	q := s.notes[id]
	s.mu.Unlock()
	if q == nil {
		return
	}
	q.io.Lock()
	defer q.io.Unlock()
	s.mu.Lock()
	text, dirty := q.text, q.dirty
	q.dirty = false
	if q.stop != nil {
		q.stop()
		q.stop = nil
	}
	s.mu.Unlock()
	if !dirty {
		return
	}
	if err := s.write(q.path, []byte(text)); err != nil {
		log.Printf("note pane: %v", err)
	}
}

// flushAll writes every unwritten note, for the app's quit.
func (s *noteSaver) flushAll() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.notes))
	for id := range s.notes {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.flush(id)
	}
}

// drop forgets the unwritten text of the note id, and returns when a write
// that runs is done, so that the caller can delete the file for good.
func (s *noteSaver) drop(id string) {
	s.mu.Lock()
	q := s.notes[id]
	delete(s.notes, id)
	if q != nil {
		q.dirty = false
		if q.stop != nil {
			q.stop()
			q.stop = nil
		}
	}
	s.mu.Unlock()
	if q != nil {
		q.wait()
	}
}

// wait returns when a write of the note that runs is done.
func (q *noteQueue) wait() {
	q.io.Lock()
	defer q.io.Unlock()
}
