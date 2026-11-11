//go:build darwin || linux || windows

package rex

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"
)

// subscriber is a control connection that subscribed: pending are the
// kinds of change not written to it yet, and transfers the last state of
// each transfer not written, when it asked for them (wantTransfers);
// wake tells its writer of them. A burst of changes is written as one of
// each kind.
type subscriber struct {
	remote        bool
	pending       map[string]bool
	wantTransfers bool
	transfers     []Transfer
	wake          chan struct{}
	done          chan struct{}
}

// subscribe tells write, a control connection's, of the changes to come,
// and of the transfers when transfers, until unsubscribe.
func (s *Server) subscribe(write func([]byte), remote bool, transfers bool) *subscriber {
	sub := &subscriber{remote: remote, pending: map[string]bool{}, wantTransfers: transfers, wake: make(chan struct{}, 1), done: make(chan struct{})}
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
			pending, transfers := sub.pending, sub.transfers
			sub.pending, sub.transfers = map[string]bool{}, nil
			s.subMu.Unlock()
			// The sessions first: a new tab's layout names a new session.
			for _, kind := range []string{"sessions", "layout"} {
				if !pending[kind] {
					continue
				}
				if sub.remote {
					if kind == "sessions" || !s.remoteShareLayout() {
						continue
					}
				}
				ev := Event{Kind: kind}
				if kind == "layout" {
					ev.Revision = s.layoutRevision()
				} else {
					ev.Sessions = s.eventSessions()
				}
				b, _ := json.Marshal(ev)
				write(b)
			}
			// An ended session's transfer is told its last state too.
			for _, t := range transfers {
				b, _ := json.Marshal(Event{Kind: "transfer", Transfer: &t})
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
	if kind == "sessions" {
		s.refreshSoon()
	}
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

// transferred tells the subscribers that asked of t, a transfer's state
// now: one not told of its last state yet gets this one
// instead.
func (s *Server) transferred(t Transfer) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for sub := range s.subs {
		if !sub.wantTransfers {
			continue
		}
		same := func(o Transfer) bool { return o.SID == t.SID && o.Direction == t.Direction && o.ID == t.ID }
		if i := slices.IndexFunc(sub.transfers, same); i >= 0 {
			sub.transfers[i] = t
		} else {
			sub.transfers = append(sub.transfers, t)
		}
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

// eventSessions is what an Event says of the sessions: their IDs and the
// status records that answered the status protocol.
func (s *Server) eventSessions() []EventSession {
	all := s.all()
	slices.SortFunc(all, func(a, b *session) int { return strings.Compare(a.id, b.id) })
	out := make([]EventSession, 0, len(all))
	for _, ss := range all {
		in := s.info(ss)
		if len(in.Statuses) == 0 {
			out = append(out, EventSession{ID: in.ID})
			continue
		}
		out = append(out, EventSession{ID: in.ID, Status: in.Status, Statuses: in.Statuses})
	}
	return out
}

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
