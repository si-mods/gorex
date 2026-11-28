package rex

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// Stream is a session attached to, for a terminal's Conn: it attaches
// once it knows the size of the screen, from the first Resize, or after
// a moment at the size it had last.
type Stream struct {
	c        *Client
	sid      string
	attachMu sync.Mutex // one attach at a time
	sizeMu   sync.Mutex // the sizes the server is told, in order: the last is the latest
	mu       sync.Mutex
	cond     *sync.Cond
	conn     net.Conn
	dialed   net.Conn // attaching
	err      error
	closed   bool
	// cols and rows are the terminal's size; sentCols and sentRows the
	// size the server was told last.
	cols, rows         int
	sentCols, sentRows int
	// focused tells that the user looks at the terminal, whose session
	// the server sizes for it (Focus).
	focused bool
	// Present, when set, tells whether the user is at this machine: the
	// stream claims the focus only then, so an unattended machine whose
	// window kept the focus takes no session's size. nil counts as
	// present. Set it with SetPresent, under mu.
	Present func() bool
	// id is what the attach gives as Attach.Client (ClientID), made
	// once: the session names its authority by it (SessionInfo.SizedBy),
	// which tells the window whether the size the program draws for is
	// its own.
	id string

	// OnData, when set, is called with the size of what the session
	// prints, from the reading goroutine.
	OnData func(n int)
}

// newClientID returns an id an attach gives as Attach.Client, by which
// the session names the client whose terminal sizes it
// (SessionInfo.SizedBy).
func newClientID() string { return "s" + rand.Text()[:10] }

// Stream returns a stream of the session sid, which attaches lazily.
func (c *Client) Stream(sid string, cols, rows int) *Stream {
	s := &Stream{c: c, sid: sid, cols: cols, rows: rows}
	s.cond = sync.NewCond(&s.mu)
	time.AfterFunc(400*time.Millisecond, s.attach)
	return s
}

// attach attaches to the session at the terminal's size, once, then
// tells it a size the terminal took meanwhile.
func (s *Stream) attach() {
	s.attachOnce()
	s.sync()
}

// SetFocused tells the server whether the user looks at the session: its
// size follows the screen the user's focus is on.
func (s *Stream) SetFocused(on bool) {
	s.mu.Lock()
	s.focused = on
	s.mu.Unlock()
	s.sendFocus()
}

// SetPresent sets Present under the stream's mutex, which the attaching
// goroutine reads: a stream told its machine's presence right after it is
// made is race-free.
func (s *Stream) SetPresent(present func() bool) {
	s.mu.Lock()
	s.Present = present
	s.mu.Unlock()
}

// sendFocus tells the server that the user looks at the session, once
// attached, and only while the user is at this machine: a window that
// kept the focus of an unattended machine claims nothing. A server that
// does not know the request answers an error, which stays untold.
func (s *Stream) sendFocus() {
	s.mu.Lock()
	conn, focused, present := s.conn, s.focused, s.Present
	s.mu.Unlock()
	if conn != nil && focused && (present == nil || present()) {
		go s.c.Focus(s.sid)
	}
}

// attachOnce attaches, once; a stream that attached or failed stays so.
func (s *Stream) attachOnce() {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	// A window focused while the stream attached, after the attach said
	// whether it was, claims once attached. Only then: a claim each time
	// attachOnce runs would take the size back from a client that took it
	// since.
	late := false
	defer func() {
		if late {
			s.sendFocus()
		}
	}()
	s.mu.Lock()
	if s.conn != nil || s.err != nil || s.closed {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	conn, err := s.c.dial()
	var cols, rows int
	focused := false
	if err == nil {
		// Closed with the client's connection, or the stream, while it
		// attaches too: a network gone answers nothing.
		connID, ok := s.c.track(conn)
		s.mu.Lock()
		s.dialed = conn
		closed := s.closed
		s.mu.Unlock()
		if !ok {
			err = errors.New("rex: connection to the server lost")
		} else if closed {
			err = io.EOF
		}
		// The size of now, not of before the dial, which may be slow: the
		// server gives it to the session before it answers.
		s.sizeMu.Lock()
		defer s.sizeMu.Unlock()
		s.mu.Lock()
		cols, rows = s.cols, s.rows
		var present func() bool
		focused, present = s.focused, s.Present
		if s.id == "" {
			s.id = newClientID()
		}
		id := s.id
		s.mu.Unlock()
		// An attach claims the focus only while the user is at this
		// machine: a stream made and focused on an unattended one leaves
		// the session's size where it is.
		if present != nil && !present() {
			focused = false
		}
		if err == nil {
			conn.SetDeadline(time.Now().Add(callTimeout.get()))
			b, _ := json.Marshal(Attach{Op: "attach", SID: s.sid, Cols: cols, Rows: rows, Conn: connID, Focused: focused, Client: id})
			if _, err = conn.Write(append(b, '\n')); err == nil {
				err = readOK(conn)
			}
			conn.SetDeadline(time.Time{})
		}
		if err != nil {
			conn.Close()
			s.c.untrack(conn)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dialed = nil
	if s.closed {
		if err == nil {
			conn.Close()
			s.c.untrack(conn)
		}
		return
	}
	if err != nil {
		s.err = err
	} else {
		s.conn, s.sentCols, s.sentRows = conn, cols, rows
		late = s.focused && !focused
	}
	s.cond.Broadcast()
}

// sync tells the server the terminal's size, once attached, when it was
// told another. One at a time, each reading the size as it goes: the last
// one tells the latest size.
func (s *Stream) sync() error {
	s.sizeMu.Lock()
	defer s.sizeMu.Unlock()
	s.mu.Lock()
	if s.conn == nil || s.cols == s.sentCols && s.rows == s.sentRows {
		s.mu.Unlock()
		return nil
	}
	cols, rows := s.cols, s.rows
	s.sentCols, s.sentRows = cols, rows
	s.mu.Unlock()
	return s.c.Resize(s.sid, cols, rows)
}

// readOK reads the server's answer to an attach, a byte at a time so as
// to read nothing past it.
func readOK(conn net.Conn) error {
	var line []byte
	b := make([]byte, 1)
	for {
		if _, err := conn.Read(b); err != nil {
			return err
		}
		if b[0] == '\n' {
			break
		}
		line = append(line, b[0])
		if len(line) > 4096 {
			return errors.New("rex: bad answer")
		}
	}
	var res struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(line, &res); err != nil {
		return err
	}
	if !res.OK {
		return errors.New(res.Error)
	}
	return nil
}

func (s *Stream) wait() (net.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.conn == nil && s.err == nil && !s.closed {
		s.cond.Wait()
	}
	if s.closed {
		return nil, io.EOF
	}
	return s.conn, s.err
}

func (s *Stream) Read(p []byte) (int, error) {
	conn, err := s.wait()
	if err != nil {
		return 0, err
	}
	n, err := conn.Read(p)
	if n > 0 && s.OnData != nil {
		s.OnData(n)
	}
	return n, err
}

func (s *Stream) Write(p []byte) (int, error) {
	conn, err := s.wait()
	if err != nil {
		return 0, err
	}
	return conn.Write(p)
}

// Resize tells the session the size of the screen, attaching first.
func (s *Stream) Resize(cols, rows int) error {
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	s.mu.Unlock()
	s.attachOnce()
	return s.sync()
}

// Close detaches; the session goes on.
func (s *Stream) Close() error {
	s.mu.Lock()
	s.closed = true
	conn, dialed := s.conn, s.dialed
	s.cond.Broadcast()
	s.mu.Unlock()
	if dialed != nil {
		dialed.Close()
	}
	if conn != nil {
		s.c.untrack(conn)
		return conn.Close()
	}
	return nil
}

// Size returns the size the session was last given.
func (s *Stream) Size() (cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows
}

// ClientID returns the id the stream's attach gives (Attach.Client), ""
// until it attaches: the session names the client whose terminal sizes it
// (SessionInfo.SizedBy) by that id, which tells whether the size the
// program draws for is this stream's.
func (s *Stream) ClientID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}
