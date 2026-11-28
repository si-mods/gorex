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
	// StartMinGap is the least time between two start pushes to one
	// device (NET-003, Push-to-start).
	StartMinGap = 5 * time.Minute
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
	// phoneHost and hostName are the phone's id and name for the host,
	// as gorex.push.register carries them at version 2: a start push's
	// attributes need them. Empty, a register of version 1, the device
	// takes no start push.
	phoneHost string
	hostName  string
	// liveActivities is the register call's flag of the phone's Live
	// Activities being on: a blocked record's urgent push then carries
	// an alert, the local notification it replaces.
	liveActivities bool
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
	// hostName is this host's name, a start push's attributes' fallback
	// when the register carried none.
	hostName string
	// keyErr is why the push key failed to load, when it did.
	keyErr error
	log    func(format string, args ...any)
	// now and after are a test's clock; the defaults are the system's.
	now   func() time.Time
	after func(d time.Duration, f func())

	mu   sync.Mutex
	regs map[any]*registration
	// starts is each device's session and block a start push went out
	// for while it stayed blocked — device, session and block joined by
	// a NUL: none goes twice for one session and block until it unblocks.
	// lastStart is when each device's last start went.
	starts    map[string]struct{}
	lastStart map[string]time.Time
}

// NewSender makes a sender to the relay at relay, the push key of dir,
// for the host named name. A key that cannot load is logged and turns
// the sender into no-ops that report it on register.
func NewSender(relay, dir, name string, logf func(format string, args ...any)) *Sender {
	s := &Sender{
		relay:     relay,
		hostName:  name,
		log:       logf,
		now:       time.Now,
		after:     func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		regs:      map[any]*registration{},
		starts:    map[string]struct{}{},
		lastStart: map[string]time.Time{},
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
// to the new target. phoneHost and hostName are the phone's id and name
// for the host, which a start push's attributes need (version 2 of
// gorex.push.register; "" before it).
func (s *Sender) Register(conn any, device, sessionID, blockID string, liveActivities bool, phoneHost, hostName string) (string, error) {
	if s == nil {
		return "", errNoSender
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return "", s.keyErr
	}
	s.regs[conn] = &registration{device: device, sessionID: sessionID, blockID: blockID,
		liveActivities: liveActivities, phoneHost: phoneHost, hostName: hostName}
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
// coalesced at priority 5. A device registered with no target — its
// phone suspended or on no terminal — gets a start push for the block
// instead, when the new top record is a blocked one (maybeStart).
func (s *Sender) Changed(blockID, sessionID string, info *rex.SessionInfo, windowTitle, blockProgram, blockLabel, sessionLabel string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := StateFor(info, windowTitle, blockProgram, blockLabel, sessionLabel)
	u := rex.MostUrgent(info.Statuses, info.StatusSeen)
	for conn, r := range s.regs {
		if u == nil || u.State != "blocked" {
			// The block unblocked: a start push for it may go again
			// (maybeStart), also after sendStart retargeted the
			// registration to it.
			delete(s.starts, r.device+"\x00"+sessionID+"\x00"+blockID)
		}
		if r.blockID == blockID {
			s.changed(conn, r, st, u)
			continue
		}
		s.maybeStart(r, sessionID, blockID, st, u)
	}
}

// changed decides what one registration does with a new state. Its
// caller holds s.mu.
func (s *Sender) changed(conn any, r *registration, st ContentState, u *rex.ProgramStatus) {
	urgent := urgentKey(u)
	alert := u != nil && urgent != r.urgent &&
		(u.State == "blocked" || u.State == "done" || u.State == "error")
	r.urgent = urgent
	if alert {
		r.due, r.hasPending = false, false
		var a *Alert
		if u.State == "blocked" && r.liveActivities {
			a = alertFor(st, u)
		}
		s.send(r, st, PriorityAlert, a)
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
	s.send(r, st, PriorityCoalesced, nil)
}

// urgentKey names the record a registration's last urgent push was for,
// as "id\x00state"; "" for none.
func urgentKey(u *rex.ProgramStatus) string {
	if u == nil {
		return ""
	}
	return u.ID + "\x00" + u.State
}

// maybeStart decides whether a change of a block the device did not
// register for starts its Live Activity by push (NET-003, Push-to-start,
// and IOS-006's Start): only a device with no target, whose register
// carried its host id (version 2) and Live Activities on, takes one —
// a register without them keeps phase 2 — and only a blocked record,
// once for the session and block while it stays blocked, and at most one
// for the device each StartMinGap. Its caller holds s.mu.
func (s *Sender) maybeStart(r *registration, sessionID, blockID string, st ContentState, u *rex.ProgramStatus) {
	if r.blockID != "" {
		return // the phone's terminal is elsewhere: no start
	}
	if u == nil || u.State != "blocked" {
		return // Changed re-arms the dedupe on this path
	}
	key := r.device + "\x00" + sessionID + "\x00" + blockID
	if _, sent := s.starts[key]; sent {
		return
	}
	if r.phoneHost == "" || !r.liveActivities {
		return
	}
	if now := s.now(); now.Sub(s.lastStart[r.device]) < StartMinGap {
		return
	} else {
		s.lastStart[r.device] = now
	}
	s.starts[key] = struct{}{}
	s.sendStart(r, sessionID, blockID, st, u)
}

// sendStart posts the start push that begins the device's activity of
// the blocked block remotely, then retargets the registration to it: the
// token the woken app binds for the new activity (input-push-token)
// takes this block's updates then. Its caller holds s.mu.
func (s *Sender) sendStart(r *registration, sessionID, blockID string, st ContentState, u *rex.ProgramStatus) {
	hostName := r.hostName
	if hostName == "" {
		hostName = s.hostName
	}
	if s.client != nil {
		s.post(PushRequest{
			Device:         r.device,
			Event:          "start",
			AttributesType: AttributesTypeTerminalActivity,
			Attributes: &Attributes{
				Host:     r.phoneHost,
				HostName: cleanText(hostName),
				Session:  sessionID,
				Block:    blockID,
			},
			ContentState:   st,
			StaleDate:      s.now().Add(StaleAfter).Unix(),
			Alert:          alertFor(st, u),
			InputPushToken: true,
			Priority:       PriorityAlert,
		})
	}
	r.sessionID, r.blockID = sessionID, blockID
	r.urgent = urgentKey(u)
	r.last, r.sentAt = st, s.now()
}

// alertFor is a blocked record's push alert: "<app> needs input", the
// title of the local notification it replaces (IOS-006, Notifications),
// named by the state's program or title, and the record's message as
// the body.
func alertFor(st ContentState, u *rex.ProgramStatus) *Alert {
	name := cleanText(st.Program)
	if name == "" {
		name = cleanText(st.Title)
	}
	return &Alert{
		Title: cut(strings.TrimSpace(name+" needs input"), MaxText, MaxTextBytes),
		Body:  cut(statusText(u.Message()), MaxText, MaxTextBytes),
	}
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
		s.send(r, r.pending, PriorityCoalesced, nil)
	}
}

// send posts the update, its caller holding s.mu. alert, when not nil,
// is the push's alert: it lights the phone's screen.
func (s *Sender) send(r *registration, st ContentState, priority int, alert *Alert) {
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
		Alert:        alert,
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
