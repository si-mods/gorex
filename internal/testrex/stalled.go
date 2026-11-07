package testrex

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Stalled is a Unix socket that takes connections and answers none, as a
// wedged server does. It closes them as the test ends.
type Stalled struct {
	Path string

	mu          sync.Mutex
	dials, open int
	conns       []net.Conn
}

// NewStalled listens on a Stalled socket for the test.
func NewStalled(t testing.TB) *Stalled {
	t.Helper()
	// A Unix socket's path is short on macOS: the test's own directory is
	// too long for it.
	dir, err := os.MkdirTemp("", "rx")
	if err != nil {
		t.Fatal(err)
	}
	s := &Stalled{Path: filepath.Join(dir, "s.sock")}
	ln, err := net.Listen("unix", s.Path)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.dials++
			s.open++
			s.conns = append(s.conns, c)
			s.mu.Unlock()
			go func() {
				var b [512]byte
				for {
					if _, err := c.Read(b[:]); err != nil {
						break
					}
				}
				s.mu.Lock()
				s.open--
				s.mu.Unlock()
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		s.mu.Lock()
		for _, c := range s.conns {
			c.Close()
		}
		s.mu.Unlock()
		os.RemoveAll(dir)
	})
	return s
}

// Counts tells how many connections came, and how many are still open:
// a client that gave up has closed its own.
func (s *Stalled) Counts() (dials, open int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dials, s.open
}
