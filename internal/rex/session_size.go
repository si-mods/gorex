//go:build darwin || linux || windows

package rex

import (
	"net"
	"sync/atomic"
	"time"
)

// followAuthorityLocked takes the size of the new authority when it
// changed from prev, s.mu held: the session's size follows the client
// whose terminal answers the program's queries, which draws for it. It
// reports whether to resize the terminal to the session's size, which the
// caller does once it let go of s.mu.
func (s *session) followAuthorityLocked(prev *attached) bool {
	if s.exited || s.sizeLock != "" {
		return false
	}
	a := s.authorityLocked()
	if a == nil || a == prev || a.cols == 0 || a.rows == 0 {
		return false
	}
	s.clearPendingResizeLocked()
	return s.resizeLocked(a.cols, a.rows)
}

// authorityLocked returns the client whose size the program draws for,
// whose terminal answers its queries unless it is mute (answererLocked):
// one attached through the control connection that holds the size, else
// the client that typed last, else the one attached last that gave a
// size, else the one attached last; s.mu is held. A client that gave no
// size, as the rexapi gateway's stream, has none for the program to draw
// for: were it the authority, the session would keep whatever size it
// had, and the windows' resizes would be ignored.
func (s *session) authorityLocked() *attached {
	var newest, sized *attached
	for a := range s.clients {
		if s.sizeLock != "" && a.via != nil && a.via == s.sizeLockConn {
			return a
		}
		if newest == nil || a.seq > newest.seq {
			newest = a
		}
		if a.cols > 0 && (sized == nil || a.seq > sized.seq) {
			sized = a
		}
	}
	if _, ok := s.clients[s.typist]; ok {
		return s.typist
	}
	if sized != nil {
		return sized
	}
	return newest
}

// sizeLease is how long a size lock lasts unless renewed.
var sizeLease = newDuration(30 * time.Second)

// resizeSettle is how long a size the windows asked for must hold before
// the session takes it. Dragging a window's edge resizes it every step,
// and each step reflows the emulator and sends SIGWINCH: a program that
// redraws its prompt on the signal (zsh with a right prompt) leaves a
// stale copy per step, as the emulator's reflow does upstream
// (libghostty-vt), so the size waits for the drag to settle.
var resizeSettle = newDuration(150 * time.Millisecond)

// ResizeSettle is how long a size the windows ask for must hold before
// the session takes it: a window's own terminal takes a size it asked
// for no sooner, or it lays out at another size what the program drew
// for the one the session still has.
func ResizeSettle() time.Duration { return resizeSettle.get() }

// resize takes the size the windows ask for once it settled
// (resizeSettle): not while an owner holds the size (lockSize). An
// explicit size (resizeNow, lockSize, lockedResize, unlockSize) is taken
// at once and drops a size still waiting.
func (s *session) resize(cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resizeLaterLocked(cols, rows)
}

// resizeLaterLocked takes a size once it settled (resizeSettle), s.mu
// held: not while an owner holds the size (lockSize), and not when it is
// the session's already.
func (s *session) resizeLaterLocked(cols, rows int) {
	cols, rows = clampSize(cols), clampSize(rows)
	if s.sizeLock != "" || s.exited {
		return
	}
	if cols == s.cols && rows == s.rows && s.pendingResize == nil {
		return
	}
	s.pendingCols, s.pendingRows = cols, rows
	settle := resizeSettle.get()
	if s.pendingResize == nil {
		s.pendingResize = time.AfterFunc(settle, s.applyResize)
	} else {
		s.pendingResize.Reset(settle)
	}
}

// resizeFrom records the size cols×rows of the clients that asked for
// it, and takes it once it settled (resize) when the authority is one of
// them: the size of the client whose terminal answers the program's
// queries is the session's (authorityLocked). The clients that asked are
// those attached through the control connection via; with none, as the
// app's windows on this machine, those of peer attached without one. A
// request of no client attached resizes the session as before.
func (s *session) resizeFrom(via net.Conn, peer string, cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cols, rows = clampSize(cols), clampSize(rows)
	matched := false
	for a := range s.clients {
		if askedBy(a, via, peer) {
			a.cols, a.rows = cols, rows
			matched = true
		}
	}
	if matched {
		if a := s.authorityLocked(); a == nil || !askedBy(a, via, peer) {
			return
		}
	}
	s.resizeLaterLocked(cols, rows)
}

// askedBy tells whether a client asked through the control connection
// via, or, with none, as the app's windows on this machine, as peer
// attached without one.
func askedBy(a *attached, via net.Conn, peer string) bool {
	if via != nil {
		return a.via == via
	}
	return a.via == nil && a.peer == peer
}

// focusFrom makes the newest client that asked, among those the user
// looks at now, the authority at once: the session's size follows the
// machine the user's focus is on, not only the one that typed last. The
// clients that asked are those attached through the control connection
// via; with none, those of peer attached without one. A size lock keeps
// its precedence (followAuthorityLocked).
func (s *session) focusFrom(via net.Conn, peer string) {
	s.sized(func() bool {
		prev := s.authorityLocked()
		var focused *attached
		for a := range s.clients {
			if askedBy(a, via, peer) && (focused == nil || a.seq > focused.seq) {
				focused = a
			}
		}
		if focused == nil {
			return false
		}
		s.typist = focused
		return s.followAuthorityLocked(prev)
	})
}

// sized runs f with s.mu held, and resizes the terminal to the session's
// size when f reports to (resizeLocked), once it let go of s.mu.
func (s *session) sized(f func() bool) {
	s.mu.Lock()
	pty := f()
	cols, rows := s.cols, s.rows
	s.mu.Unlock()
	if pty {
		s.resizeTerminal(cols, rows)
	}
}

// resizeTerminal resizes the terminal, the caller having let go of s.mu:
// the console may write as it resizes, which the reader takes s.mu to
// read.
func (s *session) resizeTerminal(cols, rows int) {
	if fn := resized.Load(); fn != nil {
		(*fn)(cols, rows)
	}
	s.p.resize(cols, rows)
}

// resized, set by tests, is told each size the terminal takes, to count
// the resizes SIGWINCH follows.
var resized atomic.Pointer[func(cols, rows int)]

func setResized(fn func(cols, rows int)) {
	if fn == nil {
		resized.Store(nil)
		return
	}
	resized.Store(&fn)
}

// applyResize takes the size the windows asked for last, once it settled.
func (s *session) applyResize() {
	s.sized(func() bool {
		cols, rows := s.pendingCols, s.pendingRows
		if cols == 0 || rows == 0 {
			// An explicit size dropped this one as the timer fired.
			return false
		}
		s.pendingResize = nil
		s.pendingCols, s.pendingRows = 0, 0
		return !s.exited && s.sizeLock == "" && s.resizeLocked(cols, rows)
	})
}

// resizeNow sets the size of the terminal at once, not while an owner
// holds the size (lockSize).
func (s *session) resizeNow(cols, rows int) {
	s.sized(func() bool {
		s.clearPendingResizeLocked()
		return s.sizeLock == "" && s.resizeLocked(cols, rows)
	})
}

// resizeForAttach sets the size of the terminal at once for a window
// attaching, whose screen is replayed at its size before it can resize
// the session itself: unless a client that typed is attached, in which
// case the session keeps its size, the attaching client becoming the
// authority as the newest.
func (s *session) resizeForAttach(cols, rows int) {
	s.sized(func() bool {
		if _, ok := s.clients[s.typist]; ok {
			return false
		}
		s.clearPendingResizeLocked()
		return s.sizeLock == "" && s.resizeLocked(cols, rows)
	})
}

// lockSize gives the session's size to owner, through the control
// connection conn, at cols×rows when given: until it unlocks, or conn
// closes (releaseSize), only its resizes size the session. The lock ends
// sizeLease after it was taken or renewed last.
func (s *session) lockSize(owner string, cols, rows int, conn net.Conn) {
	s.sized(func() bool {
		s.clearPendingResizeLocked()
		s.clearSizeLockLocked()
		s.sizeLock, s.sizeLockConn = owner, conn
		s.renewLocked()
		s.notify()
		return cols > 0 && rows > 0 && s.resizeLocked(cols, rows)
	})
}

// lockedResize resizes the session for owner, and reports whether it
// holds the size.
func (s *session) lockedResize(owner string, cols, rows int) (held bool) {
	s.sized(func() bool {
		s.clearPendingResizeLocked()
		held = owner != "" && s.sizeLock == owner
		return held && s.resizeLocked(cols, rows)
	})
	return held
}

// unlockSize gives the size back to the windows, at cols×rows when given,
// the size of the window unlocking, else the authority's. With an owner,
// it unlocks only that owner's lock.
func (s *session) unlockSize(owner string, cols, rows int) {
	s.sized(func() bool {
		if owner != "" && s.sizeLock != owner {
			return false
		}
		// With a size, it is the window's own and is taken at once,
		// replacing a size still waiting. Without one, the session takes
		// the authority's (endSizeLockLocked).
		if cols > 0 && rows > 0 {
			s.clearSizeLockLocked()
			s.clearPendingResizeLocked()
			return s.resizeLocked(cols, rows)
		}
		return s.endSizeLockLocked()
	})
}

// releaseSize unlocks the lock taken through conn, which closed.
func (s *session) releaseSize(conn net.Conn) {
	s.sized(func() bool { return s.sizeLock != "" && s.sizeLockConn == conn && s.endSizeLockLocked() })
}

// renewSize renews owner's lock, and reports whether it holds it.
func (s *session) renewSize(owner string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner == "" || s.sizeLock != owner {
		return false
	}
	s.renewLocked()
	return true
}

func (s *session) renewLocked() {
	lease := sizeLease.get()
	s.sizeLockUntil = time.Now().Add(lease)
	if s.sizeLockTimer != nil {
		s.sizeLockTimer.Reset(lease)
		return
	}
	s.sizeLockTimer = time.AfterFunc(lease, func() {
		// A renewal may have come as the timer fired.
		s.sized(func() bool {
			return !s.sizeLockUntil.IsZero() && !time.Now().Before(s.sizeLockUntil) && s.endSizeLockLocked()
		})
	})
}

// clearPendingResizeLocked drops a size still waiting for the drag to
// settle: an explicit one replaces it. s.mu is held.
func (s *session) clearPendingResizeLocked() {
	if s.pendingResize != nil {
		s.pendingResize.Stop()
		s.pendingResize = nil
	}
	s.pendingCols, s.pendingRows = 0, 0
}

// endSizeLockLocked ends the size lock, s.mu held, and, when one held,
// gives the session the size of the authority that is left, as a client
// leaving does (leave): the windows' sizes, ignored while the lock held,
// were recorded (resizeFrom), and none waits, as the lock took none. A
// release of a lock no one holds takes no size, leaving a window's size
// still waiting to land. It reports whether to resize the terminal to
// the session's size, which the caller does once it let go of s.mu.
func (s *session) endSizeLockLocked() bool {
	held := s.sizeLock != ""
	s.clearSizeLockLocked()
	return held && s.followAuthorityLocked(nil)
}

func (s *session) clearSizeLockLocked() {
	if s.sizeLock != "" {
		s.notify()
	}
	if s.sizeLockTimer != nil {
		s.sizeLockTimer.Stop()
		s.sizeLockTimer = nil
	}
	s.sizeLock, s.sizeLockConn, s.sizeLockUntil = "", nil, time.Time{}
}

// resizeLocked sets the size of the session's emulator, s.mu held, and
// reports whether to resize the terminal to it, which the caller does
// once it let go of s.mu: the console may write as it resizes, which the
// reader takes s.mu to read.
func (s *session) resizeLocked(cols, rows int) bool {
	cols, rows = clampSize(cols), clampSize(rows)
	changed := cols != s.cols || rows != s.rows
	s.cols, s.rows = cols, rows
	if changed {
		s.notify()
		s.vt.Resize(cols, rows)
		// Once the program drew its screen at the new size, the windows
		// get it again.
		if s.resync == nil {
			s.resync = time.AfterFunc(resyncDelay, s.resyncScreen)
		} else {
			s.resync.Reset(resyncDelay)
		}
	}
	return changed && !s.exited
}
