//go:build darwin || linux || windows

package rex

import (
	"encoding/json"
	"sync"
	"time"
)

// subscriber is a control connection that subscribed: pending are the
// kinds of change not written to it yet; wake tells its writer of them. A
// burst of changes is written as one of each kind.
type subscriber struct {
	pending map[string]bool
	wake    chan struct{}
	done    chan struct{}
}

// subscribe tells write, a control connection's, of the changes to come,
// until unsubscribe.
func (s *Server) subscribe(write func([]byte)) *subscriber {
	sub := &subscriber{pending: map[string]bool{}, wake: make(chan struct{}, 1), done: make(chan struct{})}
	s.subMu.Lock()
	if s.subs == nil {
		s.subs = map[*subscriber]struct{}{}
	}
	s.subs[sub] = struct{}{}
	s.subMu.Unlock()
	go func() {
		for {
			select {
			case <-sub.wake:
			case <-sub.done:
				return
			}
			s.subMu.Lock()
			pending := sub.pending
			sub.pending = map[string]bool{}
			s.subMu.Unlock()
			// The sessions first: a new tab's layout names a new session.
			for _, kind := range []string{"sessions", "layout"} {
				if !pending[kind] {
					continue
				}
				ev := Event{Kind: kind}
				if kind == "layout" {
					ev.Revision = s.layoutRevision()
				}
				b, _ := json.Marshal(ev)
				write(b)
			}
		}
	}()
	return sub
}

func (s *Server) unsubscribe(sub *subscriber) {
	s.subMu.Lock()
	delete(s.subs, sub)
	s.subMu.Unlock()
	close(sub.done)
}

// notify tells the subscribers of a change of kind, "layout" or
// "sessions". It never waits for them: sessions call it as they print.
func (s *Server) notify(kind string) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for sub := range s.subs {
		sub.pending[kind] = true
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

func (s *Server) sessionsChanged() { s.notify("sessions") }

// layoutRevision returns the revision of the layout the server keeps.
func (s *Server) layoutRevision() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.layoutRev
}

// throttle runs f at most once every every, and after each ask: one
// asked again while it waits runs once, when its turn comes.
type throttle struct {
	every time.Duration
	f     func()
	mu    sync.Mutex
	last  time.Time
	due   bool
}

func (t *throttle) ask() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.due {
		return
	}
	t.due = true
	time.AfterFunc(max(time.Until(t.last.Add(t.every)), 0), func() {
		t.mu.Lock()
		t.due, t.last = false, time.Now()
		t.mu.Unlock()
		t.f()
	})
}
