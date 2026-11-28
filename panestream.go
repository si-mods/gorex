package main

import (
	"errors"
	"io"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"gorex/internal/rex"
)

// screenReset clears a terminal attached again to its session, whose
// snapshot of the screen and scrollback follows.
const screenReset = "\x1bc\x1b[H\x1b[2J\x1b[3J"

// attachGrace is how long a pane of this machine waits for the view to ask
// for its size before it attaches at the one it has, as rex.Stream waited
// for its first Resize: the box of a pane made by a split is laid out a
// moment after its session, and the program must draw for that box.
const attachGrace = 400 * time.Millisecond

// maxQueuedInput bounds what waits for a stream: typed at a pane whose
// server never answers, or while the screen was lost, it would hold every
// key.
const maxQueuedInput = 64 << 10

// queuedInputTTL is how long after the screen was lost what is typed still
// waits for the stream: past it, the keys go to a program the user has
// stopped seeing, and are dropped instead.
const queuedInputTTL = 10 * time.Second

// streamNoteDelay is how long a pane's stream is lost before the pane
// says so: a stream that resumed at once shows nothing.
const streamNoteDelay = time.Second

// paneStream is the Conn of a pane's terminal: its session on this
// machine's server or on a host, attached again whenever its connection
// comes back, until the session ends. What is typed while it is detached
// waits for it, in order, up to maxQueuedInput and queuedInputTTL.
type paneStream struct {
	// client returns the client to attach through: this machine's, or the
	// host's, nil while it has none.
	client func() *rex.Client
	sid    string
	// name is the host's label, or "local" for this machine, for the log.
	name string
	// OnData is told the size of what the session prints, and onChange that
	// the stream detached or attached again.
	OnData   func(n int)
	onChange func()

	sizeMu     sync.Mutex // sendSize, one at a time
	mu         sync.Mutex
	cur        *rex.Stream
	fresh      bool // cur printed nothing yet
	cols, rows int
	// focused tells that the user looks at the pane, whose session the
	// server sizes for it; cur is told as it changes.
	focused bool
	// present, when set, tells whether the user is at this machine: each
	// stream attached again claims the focus only then.
	present  func() bool
	closed   bool
	detached bool
	// detachedAt is when the stream was lost, zero while attached: a
	// stream lost a moment shows no note, and takes what is typed.
	detachedAt time.Time
	// attachedAt is when the stream attached, for the log of its loss.
	attachedAt time.Time
	// lostAt is when the stream was last lost, for the log of its return.
	lostAt time.Time

	gone     bool // the host no longer lists the session to this machine
	attaches int  // the streams that printed
	fails    int  // the streams lost since one printed
	pending  []byte
	// lastData is when the stream last brought bytes and inRead the
	// Reads waiting on it now: a starved stream with a Read blocked in
	// it starves on the server; one with none starves under the pane
	// that stopped reading. seenOut is the session's output as the
	// watch last counted it, and starved when its report last logged.
	lastData time.Time
	inRead   atomic.Int32
	seenOut  uint64
	starved  time.Time
	// queued is what was typed while the stream is away, or before the
	// first one attached, which it writes in order once it has one.
	queued []byte
	// made is when the pane was made, zero for a pane of a host: a pane
	// of this machine attaches at the size its box takes, which the view
	// asks within attachGrace of it, not at the one its session had. It
	// attaches as the view asks, once it does: the session takes that size
	// now, as a split's child session is made at its parent's.
	made time.Time
	// asked is closed as the view asks for a size the first time.
	asked     chan struct{}
	askedOnce sync.Once
}

func newHostStream(h *hostConn, sid string, cols, rows int) *paneStream {
	s := &paneStream{client: h.current, name: h.label(), sid: sid, cols: cols, rows: rows}
	go s.watch()
	return s
}

// newLocalStream is newHostStream for a session of this machine: it
// attaches through the app's client, and attaches again when the stream
// is lost, as a host's does, so that a pane whose connection dropped
// takes what is typed again instead of showing a screen that goes on.
func newLocalStream(c *rex.Client, sid string, cols, rows int) *paneStream {
	s := &paneStream{client: func() *rex.Client { return c }, name: "local", sid: sid, cols: cols, rows: rows, made: time.Now(), asked: make(chan struct{})}
	go s.watch()
	return s
}

// DetachedFor tells how long the stream has been lost, zero while it is
// attached: the pane says so (streamNoteDelay) once it has been a while.
func (s *paneStream) DetachedFor() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.detached {
		return 0
	}
	return time.Since(s.detachedAt)
}

func (s *paneStream) changed() {
	if s.onChange != nil {
		s.onChange()
	}
}

// stream returns the stream of the moment, attaching a new one through
// its client once it has one; io.EOF once closed.
func (s *paneStream) stream() (*rex.Stream, error) {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, io.EOF
		}
		if s.cur != nil {
			st := s.cur
			s.mu.Unlock()
			return st, nil
		}
		cols, rows, fails, present := s.cols, s.rows, s.fails, s.present
		made := s.made
		s.mu.Unlock()
		// The first stream of a pane of this machine attaches as the view
		// asks for the size of the box it lays out, or after a moment, as
		// rex.Stream did: the program draws for that box, not for the size
		// its session had when the pane was made.
		if !made.IsZero() && fails == 0 {
			s.mu.Lock()
			s.made = time.Time{}
			s.mu.Unlock()
			if d := time.Until(made.Add(attachGrace)); d > 0 {
				select {
				case <-s.asked:
				case <-time.After(d):
				}
			}
			s.mu.Lock()
			cols, rows = s.cols, s.rows
			s.mu.Unlock()
		}
		c := s.client()
		if c == nil || fails > 0 {
			time.Sleep(min(250*time.Millisecond<<min(fails, 3), 2*time.Second))
			if c == nil {
				continue
			}
		}
		st := c.Stream(s.sid, cols, rows)
		// Before it attaches: a session is claimed again on each new
		// stream only while the user is at this machine.
		st.SetPresent(present)
		st.Resize(cols, rows) // attaches now
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			st.Close()
			return nil, io.EOF
		}
		s.cur, s.fresh = st, true
		queued := s.queued
		s.queued = nil
		s.mu.Unlock()
		if len(queued) > 0 {
			st.Write(queued)
		}
		// A Resize while it attached, over a slow network as the view
		// first lays out, only noted the size: the session takes it now.
		// Left at the old size, its console draws for a screen of other
		// rows than the window's, and lines land on each other.
		s.sendSize()
		s.mu.Lock()
		focused := s.focused
		s.mu.Unlock()
		if focused {
			st.SetFocused(true)
		}
		return st, nil
	}
}

func (s *paneStream) Read(p []byte) (int, error) {
	s.inRead.Add(1)
	defer s.inRead.Add(-1)
	for {
		s.mu.Lock()
		if len(s.pending) > 0 {
			n := copy(p, s.pending)
			s.pending = s.pending[n:]
			s.lastData = time.Now()
			s.mu.Unlock()
			return n, nil
		}
		s.mu.Unlock()
		st, err := s.stream()
		if err != nil {
			return 0, err
		}
		n, err := st.Read(p)
		if n > 0 {
			s.mu.Lock()
			s.lastData = time.Now()
			first := s.fresh && s.cur == st
			var away time.Duration
			back := false
			if first {
				s.fresh, s.detached, s.fails = false, false, 0
				s.detachedAt = time.Time{}
				s.attaches++
				now := time.Now()
				// Back from a loss, even one before the first attach.
				if !s.lostAt.IsZero() {
					away, back = now.Sub(s.lostAt).Round(time.Millisecond), true
					s.lostAt = time.Time{}
				}
				s.attachedAt = now
				// Attached again: its snapshot replaces what shows.
				if s.attaches > 1 {
					s.pending = append([]byte(screenReset), p[:n]...)
					n = copy(p, s.pending)
					s.pending = s.pending[n:]
				}
			}
			s.mu.Unlock()
			if back {
				log.Printf("gorex: pane stream back: host=%s sid=%s away=%s", s.name, s.sid, away)
			}
			if first {
				s.changed()
			}
			if s.OnData != nil {
				s.OnData(n)
			}
			return n, nil
		}
		if err == nil {
			continue
		}
		if s.lost(st, err) {
			return 0, io.EOF
		}
	}
}

// lost drops a stream that ended, and tells whether its session ended
// too, rather than the connection.
func (s *paneStream) lost(st *rex.Stream, err error) bool {
	st.Close()
	s.mu.Lock()
	if s.cur == st {
		s.cur = nil
	}
	closed, was := s.closed, s.detached
	after := "never attached"
	if !s.attachedAt.IsZero() {
		after = time.Since(s.attachedAt).Round(time.Millisecond).String()
	}
	s.detached, s.detachedAt = true, time.Now()
	if !was {
		s.lostAt = s.detachedAt
	}
	s.fails++
	s.mu.Unlock()
	if closed {
		return true
	}
	if !was {
		log.Printf("gorex: pane stream lost: host=%s sid=%s after=%s: %v", s.name, s.sid, after, err)
		s.changed()
		// The note comes due a moment later, when the pane draws again.
		time.AfterFunc(streamNoteDelay, func() {
			s.mu.Lock()
			show := s.detached && !s.closed
			s.mu.Unlock()
			if show {
				s.changed()
			}
		})
	}
	if err.Error() != "no such session" {
		c := s.client()
		if c == nil {
			return false
		}
		infos, lerr := c.List()
		if lerr != nil {
			return false
		}
		for _, in := range infos {
			if in.ID == s.sid {
				return in.Exited
			}
		}
	}
	// Ended, or no longer shared with this machine: not for it to end.
	s.mu.Lock()
	s.gone = true
	s.mu.Unlock()
	return true
}

// Gone tells that the session ended without a trace, or that the host no
// longer shares it with this machine.
func (s *paneStream) Gone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gone
}

func (s *paneStream) Write(p []byte) (int, error) {
	typed := len(p)
	s.mu.Lock()
	if st := s.cur; st != nil && !s.detached {
		s.mu.Unlock()
		st.Write(p)
		return typed, nil
	}
	// The stream is lost, or the pane's first one is still on its way:
	// what is typed goes once it attached, in order, as it waited for the
	// attach of old. Dropped, a shell asked to exit as its pane was made
	// would go on, and a key typed the moment a stream dropped would be
	// lost though the pane said it reconnected.
	if s.closed || !s.detachedAt.IsZero() && time.Since(s.detachedAt) > queuedInputTTL {
		// Typed long after the screen was lost: not for a program the user
		// has stopped seeing.
		s.queued = nil
		s.mu.Unlock()
		return typed, nil
	}
	if room := maxQueuedInput - len(s.queued); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		s.queued = append(s.queued, p...)
	}
	s.mu.Unlock()
	return typed, nil
}

// Paste writes text to the session as a paste, as the terminal's paste
// keys would: the host's server brackets it as the program asked.
func (s *paneStream) Paste(text string) error {
	c := s.client()
	if c == nil {
		return errors.New("gorex: the host is away")
	}
	return c.Paste(s.sid, text)
}

// Resize tells the session the size of the screen, when attached; the
// next stream attaches at it.
func (s *paneStream) Resize(cols, rows int) error {
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	asked := s.asked
	s.mu.Unlock()
	if asked != nil {
		s.askedOnce.Do(func() { close(asked) })
	}
	s.sendSize()
	return nil
}

// SetPresent sets what tells whether the user is at this machine, which
// each stream attaching again reads: a pane of this machine claims the
// session's size only then.
func (s *paneStream) SetPresent(present func() bool) {
	s.mu.Lock()
	s.present = present
	s.mu.Unlock()
}

// SetFocused tells the session whether the user looks at the pane, once
// attached; a stream attaching again is told when it does (stream).
func (s *paneStream) SetFocused(on bool) {
	s.mu.Lock()
	s.focused = on
	st := s.cur
	s.mu.Unlock()
	if st != nil {
		st.SetFocused(on)
	}
}

// Size returns the size the pane asked the session for, which its next
// stream attaches at.
func (s *paneStream) Size() (cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows
}

// ClientID returns the id of the stream attached now, "" while none is:
// the server names the client whose terminal sizes the session
// (SessionInfo.SizedBy) by it.
func (s *paneStream) ClientID() string {
	s.mu.Lock()
	st := s.cur
	s.mu.Unlock()
	if st == nil {
		return ""
	}
	return st.ClientID()
}

// sendSize tells the stream of the moment the latest size, one at a time,
// so that an older size never comes last.
func (s *paneStream) sendSize() {
	s.sizeMu.Lock()
	defer s.sizeMu.Unlock()
	s.mu.Lock()
	st, cols, rows := s.cur, s.cols, s.rows
	s.mu.Unlock()
	if st != nil {
		st.Resize(cols, rows)
	}
}

// Close detaches; the session goes on.
func (s *paneStream) Close() error {
	s.mu.Lock()
	s.closed = true
	st := s.cur
	s.cur = nil
	s.mu.Unlock()
	if st != nil {
		st.Close()
	}
	return nil
}

var (
	// streamWatchEvery is how often a live stream is checked for
	// starvation: its session prints while it brings nothing. A stream
	// silent for one check while the session printed in it starves.
	streamWatchEvery = 30 * time.Second
	// streamStarveLogEvery is the least time between the starvation
	// report of a stream, and the goroutine dump with it.
	streamStarveLogEvery = 10 * time.Minute
)

// watch drops a stream that starves: the session's output grows, its
// reads bring nothing, and no error ever comes — a write the server
// cannot finish, or a reader the pane does not run, is silent to it. It
// closes the stream, whose next Read attaches again, the snapshot
// replacing the screen; and it logs a goroutine dump once in a while, so
// that a freeze like #1076 leaves where it wedged in the log.
func (s *paneStream) watch() {
	t := time.NewTicker(streamWatchEvery)
	defer t.Stop()
	for range t.C {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		cur, detached, lastData, seen := s.cur, s.detached, s.lastData, s.seenOut
		s.mu.Unlock()
		if cur == nil || detached || lastData.IsZero() {
			continue
		}
		c := s.client()
		if c == nil {
			continue
		}
		var out uint64
		found := false
		if infos, err := c.List(); err == nil {
			for _, in := range infos {
				if in.ID == s.sid {
					out, found = in.Output, true
					break
				}
			}
		}
		s.mu.Lock()
		s.seenOut = out
		silent := time.Since(lastData)
		starved := found && out > seen && silent > streamWatchEvery
		report := starved && time.Since(s.starved) > streamStarveLogEvery
		if report {
			s.starved = time.Now()
		}
		s.mu.Unlock()
		if !starved {
			continue
		}
		if report {
			log.Printf("gorex: pane stream starved: host=%s sid=%s silent=%s out=%d reads=%d: it is closed to attach again", s.name, s.sid, silent.Round(time.Second), out, s.inRead.Load())
			dump := make([]byte, 1<<20)
			log.Printf("gorex: goroutines of the starved pane stream:\n%s", dump[:runtime.Stack(dump, true)])
		}
		cur.Close()
	}
}
