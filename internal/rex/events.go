//go:build darwin || linux || windows

package rex

import (
	"encoding/json"
	"strconv"
	"sync"
	"time"
)

// subscriber is a control connection that subscribed: pending are the
// changes not written to it yet, by kind, each with its ETag; wake tells
// its writer of them. A burst of changes is written as one of each kind.
type subscriber struct {
	pending map[string]string
	wake    chan struct{}
	done    chan struct{}
}

// subscribe tells write, a control connection's, of the changes to come,
// until unsubscribe.
func (s *Server) subscribe(write func([]byte)) *subscriber {
	sub := &subscriber{pending: map[string]string{}, wake: make(chan struct{}, 1), done: make(chan struct{})}
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
			sub.pending = map[string]string{}
			s.subMu.Unlock()
			// The sessions first: a new tab's layout names a new session.
			for _, kind := range []string{"sessions", "layout"} {
				if tag, ok := pending[kind]; ok {
					b, _ := json.Marshal(struct {
						Event string `json:"event"`
						ETag  string `json:"etag"`
					}{kind, tag})
					write(b)
				}
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
	s.changes++
	tag := strconv.FormatUint(s.changes, 10)
	for sub := range s.subs {
		sub.pending[kind] = tag
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

func (s *Server) sessionsChanged() { s.notify("sessions") }

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
