//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
)

// scrollback is about how many bytes of output a session keeps above its
// screen, for the windows that attach to it.
const scrollback = 8 << 20

// ErrSessionEnded is input to a session whose program has ended.
var ErrSessionEnded = errors.New("rex: the session has ended")

// session is a pseudo-terminal of the server and its shell.
type session struct {
	id      string
	shell   string
	created time.Time
	p       *pty
	dir     string
	// owner is the key of the remote client that created the session, ""
	// for one created locally; set before the session is listed.
	owner string
	// changed tells the server's subscribers that what the list shows of
	// the session changed; sampler looks, as the session prints, whether
	// its title or foreground program did, from seen.
	changed func()
	sampler *throttle
	seen    SessionInfo

	mu sync.Mutex
	// vt is the session's screen, kept by the terminal emulator of the
	// app's terminals without a view: a window attaching gets a snapshot
	// of it at its size, as the session shows it now.
	vt         *terminal.Terminal
	vtIn       *io.PipeWriter
	graphics   graphics
	output     uint64
	bells      atomic.Int64
	clients    map[*attached]struct{}
	lastOutput time.Time
	lastInput  time.Time
	resync     *time.Timer
	// status is what the program reported of itself; busy, that the last
	// info found a program in the foreground.
	status programStatus
	busy   bool
	// reaped tells that the process ended, with code; exited, that what
	// it printed was read too.
	reaped bool
	exited bool
	code   int
	// screen and title are what the session showed as it exited, its
	// emulator closed.
	screen     []byte
	title      string
	cols, rows int
	done       chan struct{}
	// reading is closed when the goroutine reading the terminal returns,
	// and readEnd when the terminal's output ends: a pause returns before.
	reading, readEnd chan struct{}
	// hold keeps the terminal open while the server hands it to another
	// executable (upgrade).
	hold sync.RWMutex
	// sizeLock is the owner holding the session's size (lockSize), through
	// the control connection sizeLockConn, until sizeLockUntil, its lease:
	// while it holds it, the windows' resizes are ignored.
	sizeLock      string
	sizeLockConn  net.Conn
	sizeLockUntil time.Time
	sizeLockTimer *time.Timer
	// typist is the client that last typed, whose terminal answers the
	// program's queries (authorityLocked); attaches counts the attaches,
	// for the newest.
	typist   *attached
	attaches uint64
	// batchEnd, while the output is inside a synchronized update (mode
	// 2026), closes as it ends; tail is the end of the output read last,
	// which may hold the start of the mode.
	batchEnd chan struct{}
	tail     []byte
}

// attached is a connection attached to a session, whose output a
// goroutine writes so that a slow client never holds up the others.
type attached struct {
	conn net.Conn
	// first is the snapshot it gets first, out what the session printed
	// since.
	first []byte
	out   chan []byte
	once  sync.Once
	// peer is the remote client's key, "" for a local window; queued is how
	// many bytes wait in out; remote tells a remote connection, whose writes
	// have a deadline.
	peer   string
	since  time.Time
	queued atomic.Int64
	remote bool
	// seq orders the attaches of a session; via is the control connection
	// it attached through, which may hold the size lock (lockSize).
	seq uint64
	via net.Conn
}

// The most chunks of output that may wait for a client, and bytes for a
// remote one: one further behind is dropped, and attaches again to a
// snapshot. Without a bound, a client that stopped reading would hold
// all the session printed since.
const (
	maxQueue       = 2048
	maxQueuedBytes = 8 << 20
)

func (a *attached) send(p []byte) bool {
	// The bytes are capped for remote clients only: a local one dropped
	// closes its pane, and reads what it is sent quick enough.
	if a.queued.Add(int64(len(p))) > maxQueuedBytes && a.remote {
		a.queued.Add(-int64(len(p)))
		return false
	}
	select {
	case a.out <- p:
		return true
	default:
		a.queued.Add(-int64(len(p)))
		return false // too far behind: drop it, it attaches again
	}
}

func (a *attached) finish() { a.once.Do(func() { close(a.out) }) }

// writeChunk is the most the writer writes under one deadline: a remote
// client is dropped once it takes nothing for writeTimeout, not when a
// snapshot takes it longer than that on a slow network.
const writeChunk = 32 << 10

// writer writes the client what it gets until a write fails, then closes
// the connection at once: the attach's read fails with it, and drops the
// client, though the session prints nothing more.
func (a *attached) writer() {
	defer func() {
		a.conn.Close()
		for range a.out {
		}
	}()
	first := a.first
	a.first = nil
	if a.write(first) != nil {
		return
	}
	for p := range a.out {
		err := a.write(p)
		a.queued.Add(-int64(len(p)))
		if err != nil {
			return
		}
	}
}

func (a *attached) write(p []byte) error {
	for len(p) > 0 {
		n := min(len(p), writeChunk)
		if a.remote {
			a.conn.SetWriteDeadline(time.Now().Add(writeTimeout.get()))
		}
		if _, err := a.conn.Write(p[:n]); err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}

// maxSize is the most columns or rows a terminal may have: the sizes a
// client sends, remote ones included, are clamped to 1..maxSize.
const maxSize = 1000

func clampSize(n int) int { return min(max(n, 1), maxSize) }

func newSession(id string, o CreateOptions, owner string, changed func()) (*session, error) {
	path, argv, name, err := shellCommand(o.Command)
	if err != nil {
		return nil, err
	}
	cols, rows := o.Cols, o.Rows
	if cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	cols, rows = clampSize(cols), clampSize(rows)
	dir := o.Dir
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	env := sessionEnv(id, o.Env, owner == "")
	p, err := startPTY(path, argv, dir, env, cols, rows)
	if err != nil {
		return nil, err
	}
	s := &session{
		id: id, shell: name, created: time.Now(), p: p, dir: dir,
		clients: map[*attached]struct{}{}, changed: changed,
		cols: cols, rows: rows, done: make(chan struct{}),
	}
	if err := s.newVT(); err != nil {
		p.hangup()
		return nil, err
	}
	s.run(p.wait)
	return s, nil
}

// newVT gives the session its emulator, at the session's size.
func (s *session) newVT() error {
	// The emulator's Conn reads nothing, and what it would answer programs
	// is dropped: the windows' terminals answer them.
	pr, pw := io.Pipe()
	vt, err := terminal.New(terminal.Options{Conn: discard{pr}, Scrollback: scrollback, OnBell: func() { s.bells.Add(1) }})
	if err != nil {
		return err
	}
	vt.Resize(s.cols, s.rows)
	s.vt, s.vtIn = vt, pw
	return nil
}

// run reads the terminal, and waits for its process to end: wait waits
// for it and returns its exit code.
func (s *session) run(wait func() int) {
	s.readEnd = make(chan struct{})
	s.sampler = &throttle{every: sampleEvery, f: s.sample}
	s.startRead()
	go func() {
		code := wait()
		s.mu.Lock()
		s.reaped, s.code = true, code
		s.mu.Unlock()
		// What it printed last is still to read; then hang up whatever
		// else holds the terminal, as terminal apps do.
		select {
		case <-s.readEnd:
		case <-time.After(150 * time.Millisecond):
			s.hold.RLock()
			s.p.close()
			s.hold.RUnlock()
			<-s.readEnd
		}
		s.hold.RLock()
		defer s.hold.RUnlock()
		s.mu.Lock()
		s.screen = append(s.vt.Snapshot(), s.graphics.replay()...)
		s.title = s.vt.Title()
		s.exited = true
		s.clearSizeLockLocked()
		s.status.drop()
		for a := range s.clients {
			a.finish()
		}
		clear(s.clients)
		s.notify()
		s.mu.Unlock()
		close(s.done)
		s.vtIn.Close()
		s.vt.Close()
	}()
}

// starterMarkers are the variables that belong to the process that started
// the server (an agent session, another Rex), not to a fresh terminal. Only
// runtime and session markers are listed; user configuration such as
// CLAUDE_CONFIG_DIR or CLAUDE_CODE_USE_BEDROCK stays.
var starterMarkers = map[string]bool{
	"CLAUDECODE": true, "CLAUDE_PID": true, "CLAUDE_EFFORT": true,
	"CLAUDE_CODE_ENTRYPOINT": true, "CLAUDE_CODE_CHILD_SESSION": true,
	"CLAUDE_CODE_SESSION_ID": true, "CLAUDE_CODE_SESSION_ATTENDED": true,
	"CLAUDE_CODE_BRIDGE_SESSION_ID": true, "CLAUDE_CODE_MESSAGING_TOKEN": true,
	"CLAUDE_CODE_MESSAGING_SOCKET": true, "CLAUDE_CODE_EXECPATH": true,
	"CLAUDE_PROJECT_DIR": true, "CLAUDE_ENV_FILE": true, "AI_AGENT": true,
	"CODEX_SANDBOX": true, "CODEX_SANDBOX_NETWORK_DISABLED": true, "CODEX_THREAD_ID": true,
	"REX_SESSION": true, "REX_BLOCK": true, "REX_SERVER": true, "REX_BIN_DIR": true,
}

func starterMarker(k string) bool { return starterMarkers[k] }

// serverEnv returns the environment for a server process: the current one
// without starter markers, so a secret never lives in the server.
func serverEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !starterMarker(k) {
			env = append(env, kv)
		}
	}
	return env
}

// sessionEnv returns the environment of a session's shell: the server's,
// without what other terminals set, and what terminal apps set. A local
// session gets the gorex shim on PATH; one a remote client made does not,
// so its shell cannot reach this machine's local socket as a local client.
func sessionEnv(id string, extra []string, local bool) []string {
	var env []string
	hasLang := false
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case k == "PATH" && local:
			continue // added below, with the gorex directory first
		case k == "TERM", k == "COLORTERM", k == "TERM_PROGRAM", k == "TERM_PROGRAM_VERSION",
			k == "TERM_SESSION_ID", k == "TMUX", k == "TMUX_PANE", k == "STY", k == "WINDOWID",
			k == "COLUMNS", k == "LINES", k == "SHLVL", k == "OLDPWD", k == "PWD", k == "_",
			strings.HasPrefix(k, "ITERM_"), strings.HasPrefix(k, "GHOSTTY_"), strings.HasPrefix(k, "KITTY_"),
			strings.HasPrefix(k, "VSCODE_"), strings.HasPrefix(k, "WEZTERM_"), strings.HasPrefix(k, "ALACRITTY_"),
			strings.HasPrefix(k, "GOREX_"), strings.HasPrefix(k, "MYGO_"),
			starterMarker(k):
			continue
		case k == "LANG" || k == "LC_ALL" || k == "LC_CTYPE":
			hasLang = true
		}
		env = append(env, kv)
	}
	env = append(env,
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"TERM_PROGRAM=GoRex",
		"TERM_PROGRAM_VERSION=0.2.0",
		"GOREX_SESSION="+id,
	)
	if local {
		bin := CLIDir()
		path := bin
		if p := os.Getenv("PATH"); p != "" {
			path += string(os.PathListSeparator) + p
		}
		env = append(env, "GOREX_BIN_DIR="+bin, "GOREX_SERVER="+SocketPath(), "PATH="+path)
	}
	if !hasLang {
		env = append(env, "LANG=en_US.UTF-8")
	}
	return append(env, extra...)
}

// startRead starts reading the terminal, until its output ends or a pause.
func (s *session) startRead() {
	reading := make(chan struct{})
	s.mu.Lock()
	s.reading = reading
	s.mu.Unlock()
	go func() {
		defer close(reading)
		s.read()
	}()
}

func (s *session) read() {
	buf := make([]byte, 64<<10)
	for {
		n, err := s.p.read(buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			cwd := s.dir
			if bytes.Contains(data, []byte("t=f")) {
				cwd = s.imageDir()
			}
			data, graphics := s.graphics.filter(data, cwd)
			s.mu.Lock()
			s.graphics.record(graphics)
			s.vt.Feed(data)
			s.batch(data)
			beforeStatus := s.status.updates
			queries := s.status.feed(data)
			statusChanged := s.status.updates != beforeStatus
			s.output += uint64(n)
			s.lastOutput = time.Now()
			s.sampler.ask()
			for a := range s.clients {
				if !a.send(data) {
					a.finish()
					delete(s.clients, a)
				}
			}
			s.mu.Unlock()
			if statusChanged {
				s.notify()
			}
			// The server answers, as the windows' terminals do not.
			for range queries {
				s.p.write(statusReply)
			}
		}
		if err != nil {
			s.mu.Lock()
			s.endBatchLocked()
			s.mu.Unlock()
			// A deadline is a pause: what the terminal has stays unread.
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				close(s.readEnd)
			}
			return
		}
	}
}

// attach attaches a connection: it gets a snapshot of the session's
// screen and scrollback, at the size the server gave the session for it
// (resize), then what the session prints, and what it sends is typed,
// typed first, what the server read past the attach's line.
// may, checked as it joins, tells whether peer may still use the session:
// detach drops it later when it may not. via is the control connection
// it attached through (Attach.Conn), if any; o.History and o.Framed shape
// the snapshot.
func (s *session) attach(conn net.Conn, peer string, o Attach, via net.Conn, may func() bool, typed []byte) {
	a := &attached{conn: conn, via: via, peer: peer, since: time.Now(), out: make(chan []byte, maxQueue), remote: peer != ""}
	s.mu.Lock()
	s.waitBatchLocked()
	if !may() {
		s.mu.Unlock()
		conn.Close()
		return
	}
	snap, title := s.view()
	cut := false
	if o.History > 0 {
		snap, cut = trimSnapshot(snap, s.rows, o.History)
	}
	if title != "" {
		snap = append([]byte("\x1b]2;"+title+"\x07"), snap...)
	}
	if o.Framed {
		head, _ := json.Marshal(SnapshotHeader{Snapshot: len(snap), Cols: s.cols, Rows: s.rows, Truncated: cut})
		snap = append(append(head, '\n'), snap...)
	}
	a.first = snap
	exited := s.exited
	if exited {
		a.finish()
	} else {
		s.attaches++
		a.seq = s.attaches
		s.clients[a] = struct{}{}
	}
	s.mu.Unlock()
	go a.writer()
	if exited {
		return
	}
	if len(typed) > 0 {
		s.input(a, typed)
	}
	buf := make([]byte, 32<<10)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			s.input(a, buf[:n])
		}
		if err != nil {
			break
		}
	}
	s.mu.Lock()
	if _, ok := s.clients[a]; ok {
		delete(s.clients, a)
		a.finish()
	}
	if s.typist == a {
		s.typist = nil
	}
	s.mu.Unlock()
}

// detach drops the windows attached for a peer that drop tells.
func (s *session) detach(drop func(peer string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for a := range s.clients {
		if drop(a.peer) {
			a.conn.Close() // reads no more of what it types
			a.finish()
			delete(s.clients, a)
		}
	}
}

// input types what client a sends.
func (s *session) input(a *attached, p []byte) {
	if p := s.admit(a, p); len(p) > 0 {
		s.p.write(p)
	}
}

// inputProgram types what a client that is not attached sends, without
// the terminal's answers a program would read as its own.
func (s *session) inputProgram(p []byte) error {
	p = dropReplies(p)
	s.mu.Lock()
	if s.exited {
		s.mu.Unlock()
		return ErrSessionEnded
	}
	if len(p) == 0 {
		s.mu.Unlock()
		return nil
	}
	s.lastInput = time.Now()
	s.mu.Unlock()
	s.p.write(p)
	return nil
}

// admit returns what of p, which client a sends, the program gets. Every
// terminal attached answers the program's queries, and the program would
// read each answer once per client: only the authority's answers reach
// it (authorityLocked).
// What the server types itself, a nil client's, goes as it is.
func (s *session) admit(a *attached, p []byte) []byte {
	typed := dropReplies(p)
	s.mu.Lock()
	defer s.mu.Unlock()
	if a != nil && len(typed) > 0 {
		s.typist = a
	}
	if a != nil && s.authorityLocked() != a {
		p = typed
	}
	if s.exited || len(p) == 0 {
		return nil
	}
	s.lastInput = time.Now()
	return p
}

// authorityLocked returns the client whose terminal answers the program's
// queries: one attached through the control connection that holds the
// size, whose size the program draws for, else the client that typed
// last, else the one attached last; s.mu is held.
func (s *session) authorityLocked() *attached {
	var newest *attached
	for a := range s.clients {
		if s.sizeLock != "" && a.via != nil && a.via == s.sizeLockConn {
			return a
		}
		if newest == nil || a.seq > newest.seq {
			newest = a
		}
	}
	if _, ok := s.clients[s.typist]; ok {
		return s.typist
	}
	return newest
}

// dropReplies returns p without the terminal's answers to queries in it:
// reports of the cursor (CPR), the device (DA, DSR), modes (DECRQM), the
// window's size and the keyboard protocol's flags, and whole control
// strings (OSC, DCS, APC), as colors, XTVERSION and DECRQSS answer. Only
// whole sequences go; any other byte stays.
// ponytail: what a user types that has an answer's shape goes too, on a
// client other than the authority, as Shift+F3 (CSI 1;2R, a CPR's);
// telling them apart needs the client to mark its answers.
func dropReplies(p []byte) []byte {
	if bytes.IndexByte(p, 0x1b) < 0 {
		return p
	}
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); {
		if n := replyLen(p[i:]); n > 0 {
			i += n
			continue
		}
		out = append(out, p[i])
		i++
	}
	return out
}

// replyLen returns the length of the answer to a query that p starts
// with, 0 when it starts with none.
func replyLen(p []byte) int {
	if len(p) < 3 || p[0] != 0x1b {
		return 0
	}
	switch p[1] {
	case ']', 'P', '_':
		// A control string: keys send none, but OSC 5522 (Kitty's
		// clipboard) is a paste in a window, which only it sends. It ends
		// with ST, or BEL for an OSC.
		if bytes.HasPrefix(p, []byte("\x1b]5522;")) {
			return 0
		}
		for i := 2; i < len(p); i++ {
			switch {
			case p[i] == 0x07 && p[1] == ']':
				return i + 1
			case p[i] == 0x1b:
				if i+1 < len(p) && p[i+1] == '\\' {
					return i + 2
				}
				return 0
			}
		}
	case '[':
		i, mark := 2, byte(0)
		if p[i] == '?' || p[i] == '>' {
			mark = p[i]
			i++
		}
		for i < len(p) && (p[i] >= '0' && p[i] <= '9' || p[i] == ';') {
			i++
		}
		if i == len(p) {
			return 0
		}
		switch f := p[i]; {
		case f == '$':
			if i+1 < len(p) && p[i+1] == 'y' { // DECRPM
				return i + 2
			}
		case f == 'R', f == 'n', // CPR, DECXCPR, DSR
			f == 'c' && mark != 0,   // DA1, DA2
			f == 't' && mark == 0,   // the window's size
			f == 'u' && mark == '?': // the keyboard protocol's flags
			return i + 1
		}
	}
	return 0
}

// sizeLease is how long a size lock lasts unless renewed.
var sizeLease = newDuration(30 * time.Second)

// resize sets the size of the terminal: not while an owner holds the size
// (lockSize).
func (s *session) resize(cols, rows int) {
	s.mu.Lock()
	pty := s.sizeLock == "" && s.resizeLocked(cols, rows)
	cols, rows = s.cols, s.rows
	s.mu.Unlock()
	if pty {
		s.p.resize(cols, rows)
	}
}

// lockSize gives the session's size to owner, through the control
// connection conn, at cols×rows when given: until it unlocks, or conn
// closes (releaseSize), only its resizes size the session. The lock ends
// sizeLease after it was taken or renewed last.
func (s *session) lockSize(owner string, cols, rows int, conn net.Conn) {
	s.mu.Lock()
	s.clearSizeLockLocked()
	s.sizeLock, s.sizeLockConn = owner, conn
	s.renewLocked()
	s.notify()
	pty := cols > 0 && rows > 0 && s.resizeLocked(cols, rows)
	cols, rows = s.cols, s.rows
	s.mu.Unlock()
	if pty {
		s.p.resize(cols, rows)
	}
}

// lockedResize resizes the session for owner, and reports whether it
// holds the size.
func (s *session) lockedResize(owner string, cols, rows int) bool {
	s.mu.Lock()
	held := owner != "" && s.sizeLock == owner
	pty := held && s.resizeLocked(cols, rows)
	cols, rows = s.cols, s.rows
	s.mu.Unlock()
	if pty {
		s.p.resize(cols, rows)
	}
	return held
}

// unlockSize gives the size back to the windows, at cols×rows when given,
// the size of the window unlocking. With an owner, it unlocks only that
// owner's lock.
func (s *session) unlockSize(owner string, cols, rows int) {
	s.mu.Lock()
	pty := false
	if owner == "" || s.sizeLock == owner {
		s.clearSizeLockLocked()
		pty = cols > 0 && rows > 0 && s.resizeLocked(cols, rows)
	}
	cols, rows = s.cols, s.rows
	s.mu.Unlock()
	if pty {
		s.p.resize(cols, rows)
	}
}

// releaseSize unlocks the lock taken through conn, which closed.
func (s *session) releaseSize(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sizeLock != "" && s.sizeLockConn == conn {
		s.clearSizeLockLocked()
	}
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
		s.mu.Lock()
		defer s.mu.Unlock()
		// A renewal may have come as the timer fired.
		if !s.sizeLockUntil.IsZero() && !time.Now().Before(s.sizeLockUntil) {
			s.clearSizeLockLocked()
		}
	})
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

// batchWait is the longest an attach waits for a synchronized update to
// end before it takes its snapshot.
const batchWait = 100 * time.Millisecond

var syncOn, syncOff = []byte("\x1b[?2026h"), []byte("\x1b[?2026l")

// batch follows the synchronized updates (mode 2026) in the output p, a
// program drawing a frame that windows show once it ends; s.mu is held.
func (s *session) batch(p []byte) {
	// A mode the read before cut in two is in seam.
	k := len(syncOn) - 1
	seam := append(append([]byte(nil), s.tail...), p[:min(len(p), k)]...)
	on, off := bytes.LastIndex(p, syncOn), bytes.LastIndex(p, syncOff)
	if on < 0 && off < 0 {
		on, off = bytes.LastIndex(seam, syncOn), bytes.LastIndex(seam, syncOff)
	}
	switch {
	case on > off && s.batchEnd == nil:
		s.batchEnd = make(chan struct{})
	case off > on:
		s.endBatchLocked()
	}
	if len(p) < k {
		s.tail = seam[max(len(seam)-k, 0):]
	} else {
		s.tail = append(s.tail[:0], p[len(p)-k:]...)
	}
}

// endBatchLocked ends the synchronized update the output is in, if any:
// it ended, or the output stopped; s.mu is held.
func (s *session) endBatchLocked() {
	if s.batchEnd != nil {
		close(s.batchEnd)
		s.batchEnd = nil
	}
}

// waitBatchLocked waits for the synchronized update the output is in, if
// any, to end, at most batchWait; s.mu is held. A snapshot of its middle
// holds half a frame, which the window attaching would show.
func (s *session) waitBatchLocked() {
	timeout := time.After(batchWait)
	for s.batchEnd != nil {
		end := s.batchEnd
		s.mu.Unlock()
		select {
		case <-end:
		case <-timeout:
			s.mu.Lock()
			return
		}
		s.mu.Lock()
	}
}

// resyncDelay is how long after the last resize the screen of a
// full-screen program is sent again.
const resyncDelay = 150 * time.Millisecond

// resyncScreen sends the windows the screen of a full-screen program as
// the session has it. A window resizes its terminal as it draws, and the
// session as the request reaches it, while the program's output is on its
// way: what the program drew for one size may land in a screen of
// another, which full-screen programs, redrawing only what changed, do
// not mend. Shells' scrollback reflows alike in both and needs nothing.
func (s *session) resyncScreen() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exited || len(s.clients) == 0 {
		return
	}
	snap, _ := s.view()
	if !bytes.Contains(snap, []byte("\x1b[?1049h")) {
		return
	}
	// Clear the alternate screen the window shows, then draw it anew.
	msg := append([]byte("\x1b[?1049h\x1b[H\x1b[2J"), snap...)
	for a := range s.clients {
		if !a.send(msg) {
			a.finish()
			delete(s.clients, a)
		}
	}
}

// kill hangs up the session and waits a little for it to end.
func (s *session) kill() {
	s.mu.Lock()
	exited := s.exited
	s.mu.Unlock()
	if !exited {
		s.p.hangup()
	}
	select {
	case <-s.done:
	case <-time.After(time.Second):
		s.p.kill()
	}
}

// capture returns the session's screen as an attach would send it, cut to
// about history bytes of scrollback when history is positive.
func (s *session) capture(history int) CaptureResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, title := s.view()
	cut := false
	if history > 0 {
		snap, cut = trimSnapshot(snap, s.rows, history)
	}
	return CaptureResult{Screen: snap, Title: title, Cols: s.cols, Rows: s.rows, Truncated: cut}
}

// view returns the session's screen, as escape sequences, and its
// title; s.mu is held.
func (s *session) view() ([]byte, string) {
	if s.exited {
		return s.screen, s.title
	}
	return append(s.vt.Snapshot(), s.graphics.replay()...), s.vt.Title()
}

// imageDir resolves a file a program names relative to where it runs: its
// foreground process's directory, else the session's start.
func (s *session) imageDir() string {
	if pid := s.p.foreground(); pid > 0 {
		if in := inspect(pid); in.dir != "" {
			return in.dir
		}
	}
	return s.dir
}

// trimSnapshot cuts the oldest rows of snap, the snapshot of a screen of
// rows rows, to about limit bytes, and reports whether it cut. What comes
// before the first cursor home, the modes and tab stops, and the screen's
// rows stay: only scrollback goes, whole rows, each of which sets its own
// style. A snapshot of the alternate screen has none.
func trimSnapshot(snap []byte, rows, limit int) ([]byte, bool) {
	home := bytes.Index(snap, []byte("\x1b[H"))
	if len(snap) <= limit || home < 0 {
		return snap, false
	}
	head, body := snap[:home+3], snap[home+3:]
	var breaks []int // where each row but the first starts
	for i := 0; ; {
		j := bytes.Index(body[i:], []byte("\r\n"))
		if j < 0 {
			break
		}
		i += j + 2
		breaks = append(breaks, i)
	}
	// The last rows rows are the screen's.
	n := len(breaks) + 1 - rows
	if n <= 0 {
		return snap, false
	}
	cut := 0
	for _, b := range breaks[:n] {
		cut = b
		if b >= len(snap)-limit {
			break
		}
	}
	return append(head[:len(head):len(head)], body[cut:]...), true
}

// info describes the session and what runs in its foreground.
func (s *session) info() SessionInfo {
	s.mu.Lock()
	title := s.title
	if !s.exited {
		title = s.vt.Title()
	}
	in := SessionInfo{
		ID: s.id, Shell: s.shell, Created: s.created, Owner: s.owner,
		Title: title, LastOutput: s.lastOutput, LastInput: s.lastInput,
		Output: s.output, Bells: int(s.bells.Load()), Exited: s.exited, ExitCode: s.code,
		Attached: len(s.clients), Cols: s.cols, Rows: s.rows,
		SizeLock: s.sizeLock,
	}
	s.mu.Unlock()
	in.PID = s.p.pid()
	if !in.Exited {
		s.foreground(&in)
	}
	s.mu.Lock()
	// A program that went back to the shell is no longer working nor
	// blocked. Only the change counts: a session whose process is the
	// program is idle all along.
	beforeStatus := s.status.updates
	if in.Idle && s.busy {
		s.status.drop()
	}
	statusChanged := s.status.updates != beforeStatus
	s.busy = !in.Exited && !in.Idle
	in.Statuses = s.status.list()
	s.mu.Unlock()
	if statusChanged {
		s.notify()
	}
	in.Status = MostUrgent(in.Statuses, time.Time{})
	return in
}

// foreground fills in what runs in the foreground of the session, whose
// process is in.PID.
func (s *session) foreground(in *SessionInfo) {
	fg := s.p.foreground()
	if fg <= 0 {
		fg = in.PID
	}
	in.Idle = fg == in.PID
	p := inspect(fg)
	in.Program, in.Args, in.Dir = p.programName(), p.args, p.dir
	if in.Program == "" {
		in.Program = s.shell
	}
	if in.Dir == "" && !in.Idle {
		in.Dir = inspect(in.PID).dir
	}
}

// sampleEvery is how often at most a session that prints looks whether
// its title or foreground program changed: the system tells of neither.
const sampleEvery = 500 * time.Millisecond

// sample tells the subscribers when the session's title or foreground
// program changed since it looked last.
func (s *session) sample() {
	s.mu.Lock()
	if s.exited {
		s.mu.Unlock()
		return
	}
	now := SessionInfo{Title: s.vt.Title()}
	s.mu.Unlock()
	now.PID = s.p.pid()
	s.foreground(&now)
	s.mu.Lock()
	defer s.mu.Unlock()
	was := s.seen
	s.seen = now
	if now.Title != was.Title || now.Program != was.Program || now.Dir != was.Dir || now.Idle != was.Idle || !slices.Equal(now.Args, was.Args) {
		s.notify()
	}
}

// notify tells the server's subscribers that the session changed.
func (s *session) notify() {
	if s.changed != nil {
		s.changed()
	}
}

// discard is the Conn of a session's emulator: it reads from a pipe that
// closes with the session, and drops what it writes.
type discard struct{ r *io.PipeReader }

func (d discard) Read(p []byte) (int, error)  { return d.r.Read(p) }
func (d discard) Write(p []byte) (int, error) { return len(p), nil }
func (d discard) Close() error                { return d.r.Close() }
