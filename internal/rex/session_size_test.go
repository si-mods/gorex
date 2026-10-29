//go:build darwin || linux

package rex

import (
	"net"
	"testing"
	"time"
)

// addAttached adds a client to s, as an attach would, at cols×rows when
// given, through the control connection via.
func addAttached(s *session, seq uint64, via net.Conn, cols, rows int) *attached {
	a := &attached{conn: via, out: make(chan []byte, maxQueue), seq: seq, via: via, cols: cols, rows: rows}
	s.mu.Lock()
	s.clients[a] = struct{}{}
	s.mu.Unlock()
	return a
}

// waitApplied waits for the session to take a size.
func waitApplied(t *testing.T, s *session, cols, rows int) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); ; {
		if c, r := appliedSize(t, s); c == cols && r == rows {
			return
		}
		if time.Now().After(deadline) {
			c, r := appliedSize(t, s)
			t.Fatalf("session %dx%d, want %dx%d", c, r, cols, rows)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// settled waits for a size still waiting to settle, and any resize it
// applies, to land.
func settled() { time.Sleep(3 * resizeSettle.get()) }

// TestSizeFollowsTypist: the session's size follows the client whose
// terminal answers the program's queries, not the window that resized
// last.
func TestSizeFollowsTypist(t *testing.T) {
	settleFor(t, 30*time.Millisecond)
	s := newResizeSession(t)
	deskVia, phoneVia := net.Pipe()
	defer deskVia.Close()
	defer phoneVia.Close()
	desk := addAttached(s, 1, deskVia, 100, 30)
	s.resizeForAttach(100, 30)
	waitApplied(t, s, 100, 30)
	// The desktop types: it answers the program's queries.
	s.admit(desk, []byte("ls\r"))
	// A viewer attaching does not resize the session under the typist.
	s.resizeForAttach(140, 50)
	phone := addAttached(s, 2, phoneVia, 140, 50)
	if c, r := appliedSize(t, s); c != 100 || r != 30 {
		t.Fatalf("a viewer attaching resized the session to %dx%d", c, r)
	}
	// The viewer's resize is only recorded, not taken.
	s.resizeFrom(phoneVia, "", 150, 55)
	settled()
	if c, r := appliedSize(t, s); c != 100 || r != 30 {
		t.Fatalf("the viewer's resize sized the session to %dx%d", c, r)
	}
	// The typist's resize is taken once it settled.
	s.resizeFrom(deskVia, "", 110, 32)
	waitApplied(t, s, 110, 32)
	// The viewer typing makes it the authority, at its size at once.
	s.admit(phone, []byte("x"))
	if c, r := appliedSize(t, s); c != 150 || r != 55 {
		t.Fatalf("typing did not follow the typist's size: %dx%d", c, r)
	}
	// The viewer leaving gives the size back to the typist at once.
	s.leave(phone)
	if c, r := appliedSize(t, s); c != 110 || r != 32 {
		t.Fatalf("leaving did not give the size back: %dx%d", c, r)
	}
}

// TestFocusReportKeepsSize: a terminal reporting that its window gained
// focus did not type, and does not take the size.
func TestFocusReportKeepsSize(t *testing.T) {
	settleFor(t, 30*time.Millisecond)
	s := newResizeSession(t)
	deskVia, phoneVia := net.Pipe()
	defer deskVia.Close()
	defer phoneVia.Close()
	desk := addAttached(s, 1, deskVia, 100, 30)
	s.resizeForAttach(100, 30)
	waitApplied(t, s, 100, 30)
	s.admit(desk, []byte("ls\r"))
	phone := addAttached(s, 2, phoneVia, 150, 55)
	s.admit(phone, []byte("\x1b[I"))
	if c, r := appliedSize(t, s); c != 100 || r != 30 {
		t.Fatalf("a focus report resized the session to %dx%d", c, r)
	}
	s.mu.Lock()
	authority := s.authorityLocked()
	s.mu.Unlock()
	if authority != desk {
		t.Fatal("a focus report took the authority")
	}
}

// TestResizeWithoutViaStillApplies: a resize from a client that attached
// with no control connection, as the CLI's, sizes the session as before.
func TestResizeWithoutViaStillApplies(t *testing.T) {
	settleFor(t, 30*time.Millisecond)
	s := newResizeSession(t)
	s.resizeFrom(nil, "", 90, 28)
	waitApplied(t, s, 90, 28)
}

// TestLocalWindowResizeIsRecorded: the app's windows on this machine
// resize with no control connection; their size is recorded on their
// attach all the same, so that taking the size back by typing takes the
// window's size of now, not the one it attached at.
func TestLocalWindowResizeIsRecorded(t *testing.T) {
	settleFor(t, 30*time.Millisecond)
	s := newResizeSession(t)
	_, phoneVia := net.Pipe()
	defer phoneVia.Close()
	desk := addAttached(s, 1, nil, 100, 30)
	s.resizeForAttach(100, 30)
	waitApplied(t, s, 100, 30)
	s.admit(desk, []byte("ls\r"))
	s.resizeFrom(nil, "", 87, 30)
	waitApplied(t, s, 87, 30)
	phone := addAttached(s, 2, phoneVia, 155, 40)
	phone.peer = "nodekey:phone"
	s.admit(phone, []byte("x"))
	waitApplied(t, s, 155, 40)
	// The window resizes while the phone types: recorded, not taken.
	s.resizeFrom(nil, "", 90, 33)
	settled()
	if c, r := appliedSize(t, s); c != 155 || r != 40 {
		t.Fatalf("a window that is not the authority resized the session to %dx%d", c, r)
	}
	s.admit(desk, []byte("y"))
	if c, r := appliedSize(t, s); c != 90 || r != 33 {
		t.Fatalf("typing took %dx%d, not the window's 90x33", c, r)
	}
}

// TestSizeLockBeatsTypist: the client holding the size lock keeps the
// session's size, whoever types.
func TestSizeLockBeatsTypist(t *testing.T) {
	settleFor(t, 30*time.Millisecond)
	s := newResizeSession(t)
	ownerVia, phoneVia := net.Pipe()
	defer ownerVia.Close()
	defer phoneVia.Close()
	addAttached(s, 1, ownerVia, 60, 20)
	s.lockSize("owner", 60, 20, ownerVia)
	waitApplied(t, s, 60, 20)
	phone := addAttached(s, 2, phoneVia, 150, 55)
	s.admit(phone, []byte("x"))
	if c, r := appliedSize(t, s); c != 60 || r != 20 {
		t.Fatalf("typing resized a locked session to %dx%d", c, r)
	}
}

// TestResizeFollowsTypistOverServer drives the same through the server:
// two clients attached over kept control connections, each resizing its
// own.
func TestResizeFollowsTypistOverServer(t *testing.T) {
	settleFor(t, 100*time.Millisecond)
	s, c, sid := streamServer(t, nil)
	hello := func() *Client {
		t.Helper()
		pc := pipe(s, "")
		pc.keepalive = true
		if _, err := pc.Hello(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { pc.Close() })
		return pc
	}
	desk, phone := hello(), hello()
	dc, err := desk.AttachConn(Attach{SID: sid, Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close()
	waitSize(t, c, sid, "100x30")
	// The desktop types, once the session has its input.
	if _, err := dc.Write([]byte("ls\r")); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); ; {
		s.mu.Lock()
		ss := s.sessions[sid]
		s.mu.Unlock()
		ss.mu.Lock()
		typed := ss.typist != nil
		ss.mu.Unlock()
		if typed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the desktop's typing never reached the session")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The phone attaches as a viewer: the size stays the typist's.
	pc, err := phone.AttachConn(Attach{SID: sid, Cols: 140, Rows: 50})
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionSize(t, c, sid); got != "100x30" {
		t.Fatalf("a viewer attaching resized the session to %s", got)
	}
	// Its resize is recorded, not taken.
	if err := phone.Resize(sid, 150, 55); err != nil {
		t.Fatal(err)
	}
	settled()
	if got := sessionSize(t, c, sid); got != "100x30" {
		t.Fatalf("the viewer's resize sized the session to %s", got)
	}
	// The typist's resize is taken.
	if err := desk.Resize(sid, 110, 32); err != nil {
		t.Fatal(err)
	}
	waitSize(t, c, sid, "110x32")
	// The viewer typing makes it the authority, at its size; leaving
	// gives the size back to the typist.
	if _, err := pc.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	waitSize(t, c, sid, "150x55")
	pc.Close()
	waitSize(t, c, sid, "110x32")
}
