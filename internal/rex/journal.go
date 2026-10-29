//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gorex/internal/layout"
)

// The server keeps the layout the app sends in layout.json, and every
// layout it kept in layout.jsonl beside it: a journal, appended to and never
// rewritten, whose entries tell a run's start, the layouts it kept and its
// stop. A run that ends without a stop record — a crash, a kill, a reboot —
// leaves its last layout in the journal, and the server that comes next
// starts the sessions of its panes again: the same ids, at the directories
// the panes were in, so that the windows attach to the sessions they had as
// if nothing happened.

const (
	// journalLimit is the size past which the journal keeps only its last
	// entries; journalKeep is how many it keeps.
	journalLimit = 1 << 20
	journalKeep  = 200
	// The kinds of entry the journal has.
	eventStart   = "start"
	eventLayout  = "layout"
	eventSession = "session"
	eventStop    = "stop"
)

// journalEntry is a line of the journal.
type journalEntry struct {
	At     time.Time       `json:"at"`
	Event  string          `json:"event"`
	PID    int             `json:"pid,omitempty"`
	Reason string          `json:"reason,omitempty"`
	Rev    uint64          `json:"rev,omitempty"`
	Layout json.RawMessage `json:"layout,omitempty"`
	// Sessions are the ones a run held when it stopped, which the layout
	// names by id alone, and the one a session entry adds.
	Sessions []journalSession `json:"sessions,omitempty"`
	Session  *journalSession  `json:"session,omitempty"`
}

// journalSession is a session as a stop record keeps it.
type journalSession struct {
	ID    string `json:"id"`
	Shell string `json:"shell"`
	Dir   string `json:"dir"`
	// Owner is the key of the remote client that created the session, "" for
	// this machine's.
	Owner string `json:"owner,omitempty"`
}

// journal appends to layout.jsonl.
type journal struct {
	mu     sync.Mutex
	f      *os.File
	closed bool
	// ended tells that this run's stop is its last entry: nothing recorded
	// after it may say the run did not stop.
	ended bool
	// n is what this run appended since the journal was last compacted.
	n int64
}

// journalPath is the path of the journal.
func journalPath() string { return filepath.Join(Dir(), "layout.jsonl") }

// openJournal opens the journal for appending, compacting it first when it
// grew past journalLimit. One that cannot open drops what it records, which
// costs a run its recovery, not its sessions.
func openJournal() *journal {
	j := &journal{}
	path := journalPath()
	if st, err := os.Stat(path); err == nil && st.Size() > journalLimit {
		compactJournal(path)
	}
	// A line a run cut short takes the next entry with it: end it first.
	var last [1]byte
	if f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0o600); err == nil {
		if st, err := f.Stat(); err == nil && st.Size() > 0 {
			if _, err := f.ReadAt(last[:], st.Size()-1); err == nil && last[0] != '\n' && last[0] != 0 {
				f.Write([]byte{'\n'})
			}
		}
		f.Chmod(0o600)
		j.f = f
		return j
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Printf("rex: the layout journal: %v", err)
		return j
	}
	j.f = f
	return j
}

// record appends e, with the time it came. Past journalLimit what this run
// appended is compacted away, keeping the journal of the last runs short
// enough to read whole.
func (j *journal) record(e journalEntry) {
	if j == nil {
		return
	}
	e.At = time.Now()
	b, err := json.Marshal(e)
	if err != nil {
		log.Printf("rex: the layout journal: %v", err)
		return
	}
	b = append(b, '\n')
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed || j.f == nil {
		return
	}
	if e.Event == eventStop {
		j.ended = true
	} else if j.ended {
		return
	}
	n, err := j.f.Write(b)
	if err != nil {
		log.Printf("rex: the layout journal: %v", err)
		return
	}
	j.n += int64(n)
	if j.n < journalLimit {
		return
	}
	// Compaction keeps the last entries, which say how the runs ended.
	j.f.Close()
	j.f = nil
	compactJournal(journalPath())
	if f, err := os.OpenFile(journalPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		j.f, j.n = f, 0
	}
}

// started records that a run starts.
func (j *journal) started() {
	j.record(journalEntry{Event: eventStart, PID: os.Getpid()})
}

// layout records a layout the run keeps.
func (j *journal) layout(rev uint64, l json.RawMessage) {
	j.record(journalEntry{Event: eventLayout, Rev: rev, Layout: l})
}

// session records a session a run started, with the client that owns it, so
// that one started again after a crash stays that client's.
func (j *journal) session(id, owner, shell, dir string) {
	j.record(journalEntry{Event: eventSession, Session: &journalSession{ID: id, Owner: owner, Shell: shell, Dir: dir}})
}

// stopped records that a run stopped, why, and the sessions it held.
func (j *journal) stopped(reason string, sessions []journalSession) {
	j.record(journalEntry{Event: eventStop, Reason: reason, Sessions: sessions})
}

// close closes the journal.
func (j *journal) close() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.closed = true
	if j.f != nil {
		j.f.Close()
		j.f = nil
	}
}

// readJournal reads the entries the journal keeps, ignoring a line a run cut
// short.
func readJournal() []journalEntry {
	b, err := os.ReadFile(journalPath())
	if err != nil {
		return nil
	}
	var out []journalEntry
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e journalEntry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		out = append(out, e)
	}
	return out
}

// journalStopped tells whether the run before this one stopped cleanly: the
// last entry it left is a stop, not a layout it kept or a start. A journal
// without entries is one of a run before the journal, and counts as stopped
// only when there is nothing to restore.
func journalStopped(entries []journalEntry) bool {
	if len(entries) == 0 {
		return false
	}
	return entries[len(entries)-1].Event == eventStop
}

// lastLayout is the layout of the last entry that kept one, for a run whose
// layout.json is gone or cut short.
func lastLayout(entries []journalEntry) json.RawMessage {
	for i := len(entries) - 1; i >= 0; i-- {
		if e := entries[i]; e.Event == eventLayout && len(e.Layout) > 0 && json.Valid(e.Layout) {
			return e.Layout
		}
	}
	return nil
}

// compactJournal keeps the last journalKeep entries of the journal, which is
// every entry of the runs still able to tell how they ended.
func compactJournal(path string) {
	entries := readJournal()
	if len(entries) > journalKeep {
		entries = entries[len(entries)-journalKeep:]
	}
	var b bytes.Buffer
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			continue
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	tmp := path + ".tmp"
	os.Remove(tmp)
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		log.Printf("rex: compacting the layout journal: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("rex: compacting the layout journal: %v", err)
	}
}

// journalOwners are the owners the journal recorded, the newest of a session
// last: a session started again after a crash stays its client's.
func journalOwners(entries []journalEntry) map[string]string {
	out := map[string]string{}
	for _, e := range entries {
		if e.Event == eventSession && e.Session != nil && e.Session.ID != "" {
			out[e.Session.ID] = e.Session.Owner
		}
	}
	return out
}

// restoreSessions starts the sessions a run lost. l is the layout of the run
// before this one, which did not stop: the sessions of its panes start again
// at the directories the panes were in, with the ids the layout names, so
// that the app attaches to the sessions it had. owners is the journal's.
func (s *Server) restoreSessions(l json.RawMessage, owners map[string]string) {
	if len(l) == 0 {
		return
	}
	st := layout.Store{}
	if json.Unmarshal(l, &st) != nil {
		return
	}
	if st.Version < 2 {
		st.Sessions = []layout.Workspace{{Layout: st.Layout}}
	}
	want := 0
	var walk func(n *layout.Node)
	walk = func(n *layout.Node) {
		if n == nil {
			return
		}
		if n.A == nil || n.B == nil {
			// A pane: of this machine, with a session the layout names.
			if n.SID != "" && n.Host == "" {
				if s.restorePane(n, owners[n.SID]) {
					want++
				}
			}
			return
		}
		walk(n.A)
		walk(n.B)
	}
	for _, w := range st.Sessions {
		for _, t := range w.Layout.Tabs {
			walk(t.Root)
		}
	}
	if want > 0 {
		log.Printf("rex: resumed %d sessions of the layout the last run kept", want)
	}
}

// restoreMost is the most sessions a run's layout starts again: a layout of
// many panes starts no army of shells before the server answers a client.
var restoreMost = 32

// restorePane starts the session of node n, when the server does not have it:
// it reports whether it did.
func (s *Server) restorePane(n *layout.Node, owner string) bool {
	s.mu.Lock()
	_, live := s.sessions[n.SID]
	more := len(s.sessions) < restoreMost
	s.mu.Unlock()
	if live {
		return false
	}
	if !more {
		log.Printf("rex: not resuming session %s: %d sessions already", n.SID, restoreMost)
		return false
	}
	o := CreateOptions{Dir: n.Dir, Cols: n.Cols, Rows: n.Rows}
	ss, err := newSession(n.SID, o, owner, s.sessionsChanged)
	if err != nil {
		log.Printf("rex: session %s of the layout is lost: %v", n.SID, err)
		return false
	}
	ss.owner = owner
	s.mu.Lock()
	s.sessions[ss.id] = ss
	s.order = append(s.order, ss.id)
	s.mu.Unlock()
	s.journal.session(ss.id, owner, ss.shell, ss.dir)
	s.notify("sessions")
	return true
}

// inventory is the sessions the server holds now, as a stop record keeps
// them.
func (s *Server) inventory() []journalSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]journalSession, 0, len(s.order))
	for _, id := range s.order {
		ss := s.sessions[id]
		if ss == nil {
			continue
		}
		out = append(out, journalSession{ID: ss.id, Shell: ss.shell, Dir: ss.dir})
	}
	return out
}
