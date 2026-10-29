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
	journalStart  = "start"
	journalLayout = "layout"
	journalStop   = "stop"
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
	// names by id alone.
	Sessions []journalSession `json:"sessions,omitempty"`
}

// journalSession is a session as a stop record keeps it.
type journalSession struct {
	ID    string `json:"id"`
	Shell string `json:"shell"`
	Dir   string `json:"dir"`
}

// journal appends to layout.jsonl.
type journal struct {
	mu sync.Mutex
	f  *os.File
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
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Printf("rex: the layout journal: %v", err)
		return j
	}
	j.f = f
	return j
}

// record appends e, with the time it came.
func (j *journal) record(e journalEntry) {
	if j == nil || j.f == nil {
		return
	}
	e.At = time.Now()
	b, err := json.Marshal(e)
	if err != nil {
		log.Printf("rex: the layout journal: %v", err)
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, err := j.f.Write(append(b, '\n')); err != nil {
		log.Printf("rex: the layout journal: %v", err)
	}
}

// started records that a run starts.
func (j *journal) started() {
	j.record(journalEntry{Event: journalStart, PID: os.Getpid()})
}

// layout records a layout the run keeps.
func (j *journal) layout(rev uint64, l json.RawMessage) {
	j.record(journalEntry{Event: journalLayout, Rev: rev, Layout: l})
}

// stopped records that a run stopped, why, and the sessions it held.
func (j *journal) stopped(reason string, sessions []journalSession) {
	j.record(journalEntry{Event: journalStop, Reason: reason, Sessions: sessions})
}

// close closes the journal.
func (j *journal) close() {
	if j == nil || j.f == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.f.Close()
	j.f = nil
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
	return entries[len(entries)-1].Event == journalStop
}

// lastLayout is the layout of the last entry that kept one, for a run whose
// layout.json is gone or cut short.
func lastLayout(entries []journalEntry) json.RawMessage {
	for i := len(entries) - 1; i >= 0; i-- {
		if e := entries[i]; e.Event == journalLayout && len(e.Layout) > 0 && json.Valid(e.Layout) {
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
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		log.Printf("rex: compacting the layout journal: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("rex: compacting the layout journal: %v", err)
	}
}

// restoreSessions starts the sessions a run lost. l is the layout of the run
// before this one, which did not stop: the sessions of its panes start again
// at the directories the panes were in, with the ids the layout names, so
// that the app attaches to the sessions it had.
func (s *Server) restoreSessions(l json.RawMessage) {
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
				if s.restorePane(n) {
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

// restorePane starts the session of node n, when the server does not have it:
// it reports whether it did.
func (s *Server) restorePane(n *layout.Node) bool {
	s.mu.Lock()
	_, live := s.sessions[n.SID]
	s.mu.Unlock()
	if live {
		return false
	}
	o := CreateOptions{Dir: n.Dir, Cols: n.Cols, Rows: n.Rows}
	ss, err := newSession(n.SID, o, "", s.sessionsChanged)
	if err != nil {
		log.Printf("rex: session %s of the layout is lost: %v", n.SID, err)
		return false
	}
	s.mu.Lock()
	s.sessions[ss.id] = ss
	s.order = append(s.order, ss.id)
	s.mu.Unlock()
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
