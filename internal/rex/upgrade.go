//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A server upgrades in place: it hands the new executable what the
// sessions need of the kernel, their shells and terminals, and the rest of
// a session, its screen and counters, in a file the new executable reads
// (Resume). How it runs the executable depends on the system (handOver).

// handoffVersion is the version of the handoff's format: a build reads
// the handoffs of the builds before it.
const handoffVersion = 1

// handoff is what an upgrading server hands the executable it runs.
type handoff struct {
	Version int `json:"version"`
	// Listener and Lock are the descriptors of the listener and of the
	// lock file, which stays locked; on Windows the new executable
	// listens again, and takes the lock once this server exits.
	Listener int `json:"listener"`
	Lock     int `json:"lock"`
	// Ready, on Windows, is the pipe the new executable writes to once it
	// serves, after which this server exits.
	Ready          uintptr         `json:"ready,omitempty"`
	Started        time.Time       `json:"started"`
	Layout         json.RawMessage `json:"layout,omitempty"`
	LayoutRevision uint64          `json:"layoutRevision,omitempty"`
	Sessions       []sessionState  `json:"sessions"`
}

// sessionState is a session in a handoff.
type sessionState struct {
	ID    string `json:"id"`
	Shell string `json:"shell"`
	// Command tells a session started with a command; a handoff of a build
	// before it has none.
	Command bool      `json:"command,omitempty"`
	Created time.Time `json:"created"`
	PID     int       `json:"pid"`
	// Dir is the directory the session started in; a handoff of a build
	// before it has none.
	Dir string `json:"dir,omitempty"`
	// Owner is the key of the remote client that created the session. A
	// handoff of a build before it has none: its sessions count as created
	// locally.
	Owner string `json:"owner,omitempty"`
	// Master is the descriptor of the terminal, -1 once the session
	// exited. On Windows it is the handle of the console's input, and
	// Console has its others.
	Master  int             `json:"master"`
	Console *consoleHandles `json:"console,omitempty"`
	Cols    int             `json:"cols"`
	Rows    int             `json:"rows"`
	// Screen is the emulator's snapshot, from which the modes are read
	// again, Images the graphics sequences the session replays on attach,
	// and Title its title. A handoff of a build before Images has the
	// replay at the end of Screen.
	Screen     []byte    `json:"screen,omitempty"`
	Images     [][]byte  `json:"images,omitempty"`
	Title      string    `json:"title,omitempty"`
	Output     uint64    `json:"output"`
	Bells      int64     `json:"bells"`
	LastOutput time.Time `json:"lastOutput"`
	LastInput  time.Time `json:"lastInput"`
	// Reaped tells that the process ended, with Code; Exited, that what
	// it printed was read too.
	Reaped bool `json:"reaped,omitempty"`
	Exited bool `json:"exited,omitempty"`
	Code   int  `json:"code,omitempty"`
	// Statuses are the program's status records (OSC 7501).
	Statuses []ProgramStatus `json:"statuses,omitempty"`
	// StatusSeen is when the records were seen till (programStatus.seen).
	StatusSeen time.Time `json:"statusSeen,omitzero"`
}

// consoleHandles are the handles of a pseudo console besides its input,
// on Windows: the end of its output the session reads, its signal pipe,
// whose end closes it, and its process, 0 once waited for.
type consoleHandles struct {
	Out     uintptr `json:"out"`
	Signal  uintptr `json:"signal"`
	Process uintptr `json:"process,omitempty"`
}

func handoffPath() string { return filepath.Join(Dir(), "handoff.json") }

// upgrade runs exe, an absolute path, in the server's place, handing it
// the sessions. It returns only when that fails, and the server goes on.
func (s *Server) upgrade(exe string) error {
	if !filepath.IsAbs(exe) {
		return fmt.Errorf("rex: %q is not an absolute path", exe)
	}
	ul, ok := s.ln.(*net.UnixListener)
	if !ok || s.lock == nil || !canUpgrade() {
		return errors.New("rex: this server cannot upgrade")
	}
	if err := probe(exe); err != nil {
		return err
	}
	s.ops.Lock()
	defer s.ops.Unlock()
	// A connection accepted now would close as exe runs: leave them to
	// it, in the listener's queue.
	s.acceptMu.Lock()
	defer s.acceptMu.Unlock()
	ul.SetDeadline(time.Unix(1, 0))
	// The listener of a failed upgrade may be another (Windows).
	defer func() { s.ln.(*net.UnixListener).SetDeadline(time.Time{}) }()
	s.remoteClose()
	defer s.remoteLoad()
	s.mu.Lock()
	all := make([]*session, 0, len(s.order))
	for _, id := range s.order {
		all = append(all, s.sessions[id])
	}
	h := handoff{Version: handoffVersion, Started: s.started, Layout: s.layout, LayoutRevision: s.layoutRev}
	s.mu.Unlock()
	for _, ss := range all {
		if err := ss.pause(); err != nil {
			return err
		}
		defer ss.resume()
	}
	for _, ss := range all {
		h.Sessions = append(h.Sessions, ss.state())
	}
	return s.handOver(exe, &h)
}

// stopForUpgrade records that this run stops, just before exe runs: the
// sessions go on in the process this run hands them to, so this run stops
// with a record, not as one that crashed, and no layout comes after it.
func (s *Server) stopForUpgrade(exe string) {
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
	s.layoutFile.Lock()
	s.journal.stopped("upgrade", s.inventory())
	s.layoutFile.Unlock()
	log.Printf("rex: upgrading to %s", exe)
}

// goOn undoes stopForUpgrade once exe failed: this run goes on with its
// sessions, keeping its layouts, and is one that did not stop.
func (s *Server) goOn() {
	s.mu.Lock()
	s.stopping = false
	s.mu.Unlock()
	s.journal.started()
}

// probe checks that exe reads this server's handoffs before it runs in
// its place: one that failed to, an older build's or another program,
// would end the sessions.
func probe(exe string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-server", "-handoff-version")
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("rex: %s does not take over a server: %w", exe, err)
	}
	if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err != nil || v < handoffVersion {
		return fmt.Errorf("rex: %s does not read this server's sessions", exe)
	}
	return nil
}

// pause stops reading the terminal, leaving what it has not read in the
// kernel, and detaches the windows, for an upgrade; resume reads again.
func (s *session) pause() error {
	s.hold.Lock()
	s.mu.Lock()
	reading := s.reading
	for a := range s.clients {
		a.finish()
	}
	clear(s.clients)
	s.mu.Unlock()
	if err := s.p.stopRead(reading); err != nil {
		s.hold.Unlock()
		return fmt.Errorf("rex: pausing session %s: %w", s.id, err)
	}
	return nil
}

func (s *session) resume() {
	select {
	case <-s.readEnd:
	default:
		s.p.readAgain()
		s.startRead()
	}
	s.hold.Unlock()
}

// state describes a paused session for a handoff.
func (s *session) state() sessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := sessionState{
		ID: s.id, Shell: s.shell, Command: s.command, Created: s.created, PID: s.p.pid(), Dir: s.dir, Master: -1, Owner: s.owner,
		Cols: s.cols, Rows: s.rows,
		Output: s.output, Bells: s.bells.Load(), LastOutput: s.lastOutput, LastInput: s.lastInput,
		Reaped: s.reaped, Exited: s.exited, Code: s.code,
		Statuses: s.status.list(), StatusSeen: s.status.seen, Images: s.graphics.sequences(),
	}
	st.Screen, st.Title = s.view()
	st.Screen = bytes.TrimSuffix(st.Screen, s.graphics.replay())
	select {
	case <-s.readEnd:
		// Its output ended: once its process did too, it exited.
		st.Exited = st.Exited || st.Reaped
	default:
	}
	if !st.Exited && !s.p.handoff(&st) {
		st.Exited, st.Code = true, -1
	}
	return st
}

// Resume serves the sessions a server upgrading to this executable
// handed it in the file path, as Serve does.
func Resume(path string) error {
	// The executable may have moved: an upgrade is one.
	if err := EnsureCLI(); err != nil {
		log.Printf("rex: the gorex command: %v", err)
	}
	b, err := os.ReadFile(path)
	os.Remove(path)
	if err != nil {
		return err
	}
	var h handoff
	if err := json.Unmarshal(b, &h); err != nil {
		return fmt.Errorf("rex: reading the handoff: %w", err)
	}
	if h.Version < 1 || h.Version > handoffVersion {
		return fmt.Errorf("rex: a handoff of version %d", h.Version)
	}
	ln, lock, err := resumeListener(h)
	if err != nil {
		return err
	}
	defer lock.Close()
	s := newServer(ln, lock)
	s.journal = openJournal()
	defer s.journal.close()
	s.journal.started()
	s.started, s.layout, s.layoutRev = h.Started, h.Layout, h.LayoutRevision
	if s.layoutRev == 0 {
		s.layoutRev = 1
	}
	for _, st := range h.Sessions {
		ss, err := resumeSession(st, s.sessionsChanged)
		if err != nil {
			log.Printf("rex: session %s is lost: %v", st.ID, err)
			closeHandedOver(st)
			continue
		}
		s.sessions[ss.id] = ss
		s.order = append(s.order, ss.id)
		s.watchExit(ss)
	}
	log.Printf("rex: resumed %d of %d sessions", len(s.order), len(h.Sessions))
	// Those the handoff lost start again, once it listens.
	go s.restoreLost(journalOwners(readJournal()), s.lostPanes())
	tookOver(h)
	return s.serve()
}

// resumeSession makes a session of a handoff's again.
func resumeSession(st sessionState, changed func()) (*session, error) {
	s := &session{
		id: st.ID, shell: st.Shell, command: st.Command, created: st.Created, dir: st.Dir, owner: st.Owner, changed: changed,
		clients: map[*attached]struct{}{},
		output:  st.Output, lastOutput: st.LastOutput, lastInput: st.LastInput,
		cols: clampSize(st.Cols), rows: clampSize(st.Rows), done: make(chan struct{}),
	}
	if err := s.newVT(); err != nil {
		return nil, err
	}
	for _, r := range st.Statuses {
		if s.status.records == nil {
			s.status.records = map[string]*statusRecord{}
		}
		s.status.updates++
		s.status.records[r.ID] = &statusRecord{r, s.status.updates}
	}
	s.status.seen = st.StatusSeen
	// The images go to the history the attaches replay, not to the
	// emulator, which the snapshot left as it was: a placement moves the
	// cursor.
	s.graphics.restore(st.Images)
	if st.Title != "" {
		s.vt.Feed([]byte("\x1b]2;" + st.Title + "\x07"))
	}
	s.vt.Feed(st.Screen)
	s.modes.feed(st.Screen)
	if st.Exited || st.Master < 0 {
		s.p = exitedPTY(st.PID)
		s.reaped, s.exited, s.code = true, true, st.Code
		s.screen, s.title = append(st.Screen, s.graphics.replay()...), st.Title
		s.ghostty, _ = s.vt.GhosttySnapshot()
		s.text = s.vt.Text()
		s.reading, s.readEnd = make(chan struct{}), make(chan struct{})
		close(s.reading)
		close(s.readEnd)
		close(s.done)
		s.vtIn.Close()
		s.vt.Close()
		s.bells.Store(st.Bells)
		return s, nil
	}
	p, err := resumePTY(st)
	if err != nil {
		s.vtIn.Close()
		s.vt.Close()
		return nil, err
	}
	s.p = p
	s.graphics.cut = true
	s.bells.Store(st.Bells)
	wait := p.wait
	if st.Reaped {
		// The executable before this one waited for it.
		wait = func() int { return st.Code }
	}
	s.run(wait)
	return s, nil
}
