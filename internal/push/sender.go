package push

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"

	"gorex/internal/rex"
)

const (
	// StaleAfter is how far past now an update's stale date lies: a host
	// that stops lets the activity show as paused (NET-003, Push).
	StaleAfter = 3 * time.Minute
	// CoalesceEvery is the least time between two coalesced updates of
	// one device.
	CoalesceEvery = 2 * time.Second
	// postWait is how long a post to the relay may take.
	postWait = 10 * time.Second
)

// The priorities a push goes to APNs with: updates that wait for the
// coalescing window at 5, a record that starts needing the user —
// blocked, done or error — at 10, at once.
const (
	PriorityCoalesced = 5
	PriorityAlert     = 10
)

// registration is a device bound to a control connection: the device key
// it pushes to, the terminal on the phone's screen, what it sent last,
// and the update coalescing waits to send.
type registration struct {
	device    string
	sessionID string
	blockID   string
	// last is the state sent last, sentAt when.
	last   ContentState
	sentAt time.Time
	// urgent is the record the last change's MostUrgent named, as
	// "id\x00state": a new urgent record of a state that needs the user
	// goes at once.
	urgent string
	// due tells that a coalesced send waits on the timer; pending is the
	// state it carries.
	due        bool
	pending    ContentState
	hasPending bool
}

// Sender pushes content states to registered devices through the relay.
// With no relay URL it sends nothing. Its methods are safe for
// concurrent use.
type Sender struct {
	client *Client
	// relay is the URL the register method answers with.
	relay string
	key   *Keypair
	// keyErr is why the push key failed to load, when it did.
	keyErr error
	log    func(format string, args ...any)
	// now and after are a test's clock; the defaults are the system's.
	now   func() time.Time
	after func(d time.Duration, f func())

	mu   sync.Mutex
	regs map[any]*registration
}

// NewSender makes a sender to the relay at relay, the push key of dir.
// A key that cannot load is logged and turns the sender into no-ops that
// report it on register.
func NewSender(relay, dir string, logf func(format string, args ...any)) *Sender {
	s := &Sender{
		relay: relay,
		log:   logf,
		now:   time.Now,
		after: func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		regs:  map[any]*registration{},
	}
	if s.log == nil {
		s.log = func(string, ...any) {}
	}
	key, err := LoadOrCreate(dir)
	if err != nil {
		s.log("push: %v", err)
		s.keyErr = err
	} else {
		s.key = key
	}
	if relay != "" && s.key != nil {
		s.client = &Client{URL: strings.TrimRight(relay, "/"), Key: s.key}
	}
	return s
}

// Relay is the URL register answers with, "" when none is set.
func (s *Sender) Relay() string {
	if s == nil {
		return ""
	}
	return s.relay
}

// Register binds conn to device, targeting the terminal of sessionID and
// blockID — "" for both when none — and returns the host's public key
// for the register method's result. Registering again moves the binding
// to the new target.
func (s *Sender) Register(conn any, device, sessionID, blockID string) (string, error) {
	if s == nil {
		return "", errNoSender
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return "", s.keyErr
	}
	s.regs[conn] = &registration{device: device, sessionID: sessionID, blockID: blockID}
	return s.key.PublicB64(), nil
}

// errNoSender answers register on a gateway without a push sender.
var errNoSender = errors.New("push: the gateway pushes nothing")

// Unregister drops conn's binding, pushing nothing.
func (s *Sender) Unregister(conn any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.regs, conn)
	s.mu.Unlock()
}

// Changed tells the sender that the block's program status changed: each
// device registered for it gets the new state, at priority 10 at once
// when a blocked, done or error record newly tops the rest, else
// coalesced at priority 5.
func (s *Sender) Changed(blockID string, info *rex.SessionInfo, windowTitle, blockProgram, blockLabel, sessionLabel string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for conn, r := range s.regs {
		if r.blockID != blockID {
			continue
		}
		s.changed(conn, r, StateFor(info, windowTitle, blockProgram, blockLabel, sessionLabel), rex.MostUrgent(info.Statuses, info.StatusSeen))
	}
}

// changed decides what one registration does with a new state. Its
// caller holds s.mu.
func (s *Sender) changed(conn any, r *registration, st ContentState, u *rex.ProgramStatus) {
	urgent := ""
	if u != nil {
		urgent = u.ID + "\x00" + u.State
	}
	alert := u != nil && urgent != r.urgent &&
		(u.State == "blocked" || u.State == "done" || u.State == "error")
	r.urgent = urgent
	if alert {
		r.due, r.hasPending = false, false
		s.send(r, st, PriorityAlert)
		return
	}
	if !r.hasPending && reflect.DeepEqual(st, r.last) {
		return
	}
	r.pending, r.hasPending = st, true
	if r.due {
		return
	}
	if wait := CoalesceEvery - s.now().Sub(r.sentAt); wait > 0 {
		r.due = true
		s.after(wait, func() { s.flush(conn, r) })
		return
	}
	r.hasPending = false
	s.send(r, st, PriorityCoalesced)
}

// flush sends the state a coalesced update waited with, unless the
// binding went or a push since carried it already.
func (s *Sender) flush(conn any, r *registration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.regs[conn] != r || !r.due {
		return
	}
	r.due = false
	if !r.hasPending {
		return
	}
	r.hasPending = false
	if !reflect.DeepEqual(r.pending, r.last) {
		s.send(r, r.pending, PriorityCoalesced)
	}
}

// send posts the update, its caller holding s.mu.
func (s *Sender) send(r *registration, st ContentState, priority int) {
	r.last = st
	r.sentAt = s.now()
	if s.client == nil {
		return
	}
	s.post(PushRequest{
		Device:       r.device,
		Event:        "update",
		ContentState: st,
		StaleDate:    r.sentAt.Add(StaleAfter).Unix(),
		Priority:     priority,
	})
}

// post sends a request to the relay, aside, and logs a failure.
func (s *Sender) post(req PushRequest) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), postWait)
		defer cancel()
		if err := s.client.Push(ctx, req); err != nil {
			s.log("push: %v", err)
		}
	}()
}

// Detached drops conn's binding after a last update that tells the
// activity the stream is gone: connected false, stale now.
func (s *Sender) Detached(conn any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.regs[conn]
	delete(s.regs, conn)
	if r == nil || s.client == nil {
		return
	}
	st := r.last
	st.Connected = false
	s.post(PushRequest{
		Device:       r.device,
		Event:        "update",
		ContentState: st,
		StaleDate:    s.now().Unix(),
		Priority:     PriorityCoalesced,
	})
}
