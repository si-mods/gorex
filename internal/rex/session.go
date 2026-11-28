//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime"
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
	id string
	// typed, when set, is told of what a client typed: the server notes
	// the use of the session's project (Server.used).
	typed func()
	shell string
	// command tells a session started with a command, as gorex new --
	// ping, whose process shell names, rather than the user's shell.
	command bool
	created time.Time
	p       *pty
	dir     string
	// home is the home directory of the user the session runs as, where
	// it starts when no directory is named, and what ~ means to it.
	home string
	// runAs is the user the session runs as, when it is not the server's:
	// what the server writes for it is that user's.
	runAs *sessionUser
	// owner is the key of the remote client that created the session, ""
	// for one created locally; set before the session is listed.
	owner string
	// delegatedFrom and delegatedAgent record peer-delegation origin
	// metadata (docs/MOD-016-peer-delegation.md).
	delegatedFrom  string
	delegatedAgent string
	// createdBy, lastCLIInputAt, agentLabel, agentActive, parentSession,
	// and tookOverAt track CLI and delegated actor state under mu;
	// onTakeover notifies subscribers after mu is released
	// (docs/DSK-016-agent-pane-highlight.md).
	createdBy      string
	lastCLIInputAt time.Time
	agentLabel     string
	agentActive    bool
	parentSession  string
	tookOverAt     time.Time
	onTakeover     func(TakeoverEvent)
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
	vt   *terminal.Terminal
	vtIn *io.PipeWriter
	// vtAnswers is whether the emulator's answers to the output fed last
	// reach the program (answerLocked).
	vtAnswers bool

	graphics   graphics
	output     uint64
	bells      atomic.Int64
	clients    map[*attached]struct{}
	lastOutput time.Time
	lastInput  time.Time
	// modes are the modes the program set, read from its output: the
	// bracketed paste mode a paste the server writes follows.
	modes decModes
	// writeMu makes one writer at a time of the pty: a paste, which a
	// client sends in pieces, is never split by what is typed meanwhile.
	writeMu sync.Mutex
	resync  *time.Timer
	// pendingResize applies the windows' last requested size once it
	// settled (resize); pendingCols and pendingRows are that size.
	pendingResize *time.Timer
	pendingCols   int
	pendingRows   int
	// status is what the program reported of itself; icon is its custom
	// icon (OSC 7502); ran, that a look found a program in the foreground.
	status programStatus
	icon   programIcon
	ran    bool
	// reaped tells that the process ended, with code; exited, that what
	// it printed was read too.
	reaped bool
	exited bool
	code   int
	// screen and title are what the session showed as it exited, its
	// emulator closed; ghostty is that screen in Ghostty's binary
	// snapshot format, which an attach asking for one sends; text is the
	// same screen as plain text, for a block query after the program
	// ended.
	screen     []byte
	ghostty    []byte
	text       string
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
	// typist is the client that last typed or told of the user's focus,
	// whose terminal answers the program's queries (authorityLocked);
	// attaches counts the attaches, for the newest.
	typist   *attached
	attaches uint64
	// batchEnd, while the output is inside a synchronized update (mode
	// 2026), closes as it ends; tail is the end of the output read last,
	// which may hold the start of the mode.
	batchEnd chan struct{}
	tail     []byte
	hasTheme atomic.Bool
}

// setTheme applies the host's active terminal colors to s.vt.
func (s *session) setTheme(th *terminal.Theme) {
	if s.vt == nil || th == nil {
		return
	}
	s.vt.SetTheme(th, th)
	s.hasTheme.Store(true)
}

// attached is a connection attached to a session, whose output a
// goroutine writes so that a slow client never holds up the others.
type attached struct {
	conn net.Conn
	// first is the snapshot it gets first, pending what the session
	// printed since.
	first []byte
	// s is the session, whose screen a client fallen behind gets again
	// (resync); nil in tests that run no writer.
	s *session
	// mu guards what follows, up to peer; the session's mu, when both are
	// held, is taken first.
	mu sync.Mutex
	// pending is the output the writer did not take yet, queued its
	// bytes; behind tells that the client fell too far behind: its output
	// is dropped until the writer sends it the screen again. closed tells
	// that no more output comes (finish): the writer writes what is
	// pending and closes; dead, that the writer is gone.
	pending              [][]byte
	queued               int
	behind, closed, dead bool
	// wake tells the writer that pending, behind or closed changed.
	wake chan struct{}
	// resynced is when the writer last sent the screen again; resyncs and
	// dropped count the resyncs and bytes not logged yet, logged when the
	// last log was (noteBehindLocked).
	resynced, logged time.Time
	resyncs, dropped int
	// peer is the remote client's key, "" for a local window; remote tells
	// a remote connection.
	peer   string
	since  time.Time
	remote bool
	// seq orders the attaches of a session; via is the control connection
	// it attached through, which may hold the size lock (lockSize).
	seq uint64
	via net.Conn
	// cols and rows are the size its terminal has, which the session
	// follows once this client is the authority (authorityLocked).
	cols, rows int
	// client is the id its attach gave (Attach.Client), "" for none,
	// which SessionInfo.SizedBy names the authority by.
	client string
	// mute tells that its terminal answers no query (Attach.Mute).
	mute bool
}

// A terminal stream carries the session's screen, not every byte it
// printed: a client that falls behind gets the screen again, on the same
// connection, rather than the output it missed, and is never dropped for
// it. Only a connection that takes nothing for writeTimeout, or fails a
// write, is closed.
const (
	// maxQueuedBytes is the most output that may wait for a client, local
	// or remote, before it is dropped for the screen again: as
	// internal/rexapi's collapseBytes, about a second of a connection of
	// 2 MiB/s, past which the screen is worth more than the old output.
	// It counts bytes, not chunks, whose size follows the terminal's
	// reads rather than the load. One chunk always queues on an empty
	// queue, as an image a read rewrote may be larger.
	maxQueuedBytes = 2 << 20
	// resyncHistory is the most scrollback, in bytes, of the screen sent
	// again: an eighth of maxQueuedBytes, as internal/rexapi's, so that a
	// resync sends far less than the output it drops. It is not counted
	// against maxQueuedBytes, so it never makes another resync itself.
	resyncHistory = 256 << 10
	// resyncEvery is the least time between a client's resyncs: one that
	// keeps falling behind gets the screen once a second at most, not a
	// snapshot per drop.
	resyncEvery = time.Second
	// resyncLogEvery is the least time between a client's log lines of
	// resyncs, which count those between.
	resyncLogEvery = 10 * time.Second
)

// resyncReset is what a resync sends before the session's screen: CAN and
// ST end an escape sequence the dropped output may have cut, then the
// terminal resets and clears as a GoRex window attaching again does
// (screenReset in package main), so that the screen after is a new
// attach's.
const resyncReset = "\x18\x1b\\" + "\x1bc\x1b[H\x1b[2J\x1b[3J"

// send queues p for the client, s.mu held: once more than maxQueuedBytes
// wait, the queue is dropped and the writer sends the screen again.
func (a *attached) send(p []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case a.dead || a.closed:
		return
	case a.behind:
		a.dropped += len(p)
		return
	case a.queued > 0 && a.queued+len(p) > maxQueuedBytes:
		a.noteBehindLocked(a.queued + len(p))
		a.pending, a.queued, a.behind = nil, 0, true
	default:
		a.pending = append(a.pending, p)
		a.queued += len(p)
	}
	a.wakeLocked()
}

// noteBehindLocked logs that the client fell behind, dropping n bytes,
// once per resyncLogEvery with the counts between; a.mu is held.
func (a *attached) noteBehindLocked(n int) {
	a.resyncs++
	a.dropped += n
	if time.Since(a.logged) < resyncLogEvery {
		return
	}
	who, sid := "local", ""
	if a.peer != "" {
		k := strings.TrimPrefix(a.peer, "nodekey:")
		who = "peer " + k[:min(len(k), 8)]
	}
	if a.s != nil {
		sid = a.s.id
	}
	log.Printf("rex: session %s: %s client fell behind %d times, dropped %d bytes: it is sent the screen again", sid, who, a.resyncs, a.dropped)
	a.logged, a.resyncs, a.dropped = time.Now(), 0, 0
}

// wakeLocked wakes the writer; a.mu is held.
func (a *attached) wakeLocked() {
	if a.wake == nil {
		a.wake = make(chan struct{}, 1)
	}
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// finish ends the client's output: the writer writes what is pending,
// then closes the connection.
func (a *attached) finish() {
	a.mu.Lock()
	a.closed = true
	a.wakeLocked()
	a.mu.Unlock()
}

// writeChunk is the most the writer writes under one deadline: a client
// is dropped once it takes nothing for writeTimeout, not when a snapshot
// takes it longer than that on a slow connection.
const writeChunk = 32 << 10

// writer writes the client what it gets until a write fails, then closes
// the connection at once: the attach's read fails with it, and drops the
// client, though the session prints nothing more.
func (a *attached) writer() {
	defer func() {
		a.mu.Lock()
		a.dead, a.pending, a.queued = true, nil, 0
		a.mu.Unlock()
		a.conn.Close()
	}()
	first := a.first
	a.first = nil
	if a.write(first) != nil {
		return
	}
	for {
		p, ok := a.next()
		if !ok || a.write(p) != nil {
			return
		}
	}
}

// next waits for what the writer writes next: the screen again for a
// client fallen behind, else its oldest pending output. It is false
// once the output ended and all of it was taken.
func (a *attached) next() ([]byte, bool) {
	for {
		a.mu.Lock()
		if a.wake == nil {
			a.wake = make(chan struct{}, 1)
		}
		wake := a.wake
		switch {
		case a.behind && !a.closed:
			wait := resyncEvery - time.Since(a.resynced)
			a.mu.Unlock()
			if wait > 0 {
				select {
				case <-time.After(wait):
				case <-wake:
				}
			} else if p := a.resync(); p != nil {
				return p, true
			}
		case len(a.pending) > 0:
			p := a.pending[0]
			a.pending[0] = nil
			a.pending = a.pending[1:]
			a.queued -= len(p)
			a.mu.Unlock()
			return p, true
		case a.closed:
			a.mu.Unlock()
			return nil, false
		default:
			a.mu.Unlock()
			<-wake
		}
	}
}

// resync returns the screen again for a client fallen behind: what ends a
// cut escape sequence and resets its terminal, then the session's screen
// as an attach's snapshot has it, with resyncHistory of scrollback. It
// takes the state under s.mu, as the output is fed and queued, so that it
// holds all the output before and none of what follows, which queues
// after it. nil means there is none to send.
//
// Under s.mu it takes only Ghostty's binary snapshot, the title and the
// graphics: rendering the whole scrollback as escape sequences, which
// the formatter cannot bound, costs some times more, and is done after,
// from the binary snapshot, holding up no output (renderResync). Should
// that fail, the snapshot is taken again under s.mu, as an attach's.
func (a *attached) resync() []byte {
	s := a.s
	s.mu.Lock()
	s.waitBatchLocked()
	a.mu.Lock()
	if !a.behind || a.closed || a.dead {
		a.mu.Unlock()
		s.mu.Unlock()
		return nil
	}
	a.pending, a.queued, a.behind, a.resynced = nil, 0, false, time.Now()
	a.mu.Unlock()
	var bin []byte
	if !s.exited {
		bin, _ = s.vt.GhosttySnapshot()
	}
	if bin == nil {
		snap := s.snapshotLocked(Attach{History: resyncHistory})
		s.mu.Unlock()
		return append([]byte(resyncReset), snap...)
	}
	title, graphics, rows := s.vt.Title(), s.graphics.replay(), s.rows
	s.mu.Unlock()
	if snap, err := renderResync(bin, title, graphics, rows); err == nil {
		return append([]byte(resyncReset), snap...)
	}
	// Drop what queued meanwhile, and take the snapshot as an attach does.
	s.mu.Lock()
	defer s.mu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.dead {
		return nil
	}
	a.pending, a.queued, a.behind = nil, 0, false
	return append([]byte(resyncReset), s.snapshotLocked(Attach{History: resyncHistory})...)
}

// ErrResyncSize is the answer to "resync" while the session has another
// size than the one asked for: the client asks again once it took it.
var ErrResyncSize = errors.New("rex: the session has another size")

// resyncClient sends the session's screen at cols×rows again to the
// client whose attach gave id, of peer, in band, as to a client fallen
// behind (resync): the screen holds all the output before it on that
// stream and none of what follows. A window that laid its terminal out
// for another size asks for it. A screen it captured on its own
// connection would not be in step with its stream: output the stream
// brought before the capture's answer, the reset before that screen
// would wipe, and output the capture holds, the stream would bring again.
func (s *session) resyncClient(id, peer string, cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cols != cols || s.rows != rows {
		return ErrResyncSize
	}
	// A stream that attached again gives the same id: its old attach,
	// whose connection the server has not seen fail yet, may still be
	// here. Each live one gets the screen; with none, the window falls
	// back to a capture.
	live := false
	for a := range s.clients {
		if id == "" || a.client != id || a.peer != peer {
			continue
		}
		a.mu.Lock()
		if !a.closed && !a.dead {
			a.pending, a.queued, a.behind, a.resynced = nil, 0, true, time.Time{}
			a.wakeLocked()
			live = true
		}
		a.mu.Unlock()
	}
	if !live {
		return errors.New("rex: no such client")
	}
	return nil
}

func (a *attached) write(p []byte) error {
	for len(p) > 0 {
		n := min(len(p), writeChunk)
		// A local window stalls as a remote peer does: its reader
		// wedged, a write waits forever, the client attached and the
		// pane a screen that goes on no more.
		a.conn.SetWriteDeadline(time.Now().Add(writeTimeout.get()))
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

// sessionDir is where a session starts: dir, when it is a directory, else
// home, as a layout whose directory is gone starts at home too.
func sessionDir(dir, home string) string {
	if dir != "" {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir
		}
	}
	if home != "" {
		return home
	}
	home, _ = os.UserHomeDir()
	return home
}

// checkFolder fails, its error starting with FolderMissing, unless dir is
// a directory the server can read: where a session of a project must
// start (CreateOptions.DirStrict).
func checkFolder(dir string) error {
	if dir == "" {
		return fmt.Errorf("%s: no folder given", FolderMissing)
	}
	st, err := os.Stat(dir)
	if err == nil && !st.IsDir() {
		return fmt.Errorf("%s: %s is not a folder", FolderMissing, dir)
	}
	if err == nil {
		var f *os.File
		if f, err = os.Open(dir); err == nil {
			f.Close()
			return nil
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s: the folder %s is missing", FolderMissing, dir)
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return fmt.Errorf("%s: the folder %s cannot be opened: %v", FolderMissing, dir, err)
}

func newSession(id string, o CreateOptions, owner string, changed func(), sp spawner) (*session, error) {
	if o.DirStrict {
		if err := checkFolder(o.Dir); err != nil {
			return nil, err
		}
	}
	path, argv, name, err := shellCommand(o.Command, sp.shell())
	if err != nil {
		return nil, err
	}
	cols, rows := o.Cols, o.Rows
	if cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	cols, rows = clampSize(cols), clampSize(rows)
	home := sp.home()
	dir := sessionDir(o.Dir, home)
	env := sessionEnv(id, o.Env, owner == "", sp.user)
	p, err := startPTY(sessionSpawn{path: path, argv: argv, dir: dir, env: env, cols: cols, rows: rows, user: sp.user, exe: sp.exe})
	if err != nil {
		return nil, err
	}
	now := time.Now()
	createdBy := normalizeCreatedBy(o.CreatedBy)
	var lastCLI time.Time
	var agentLabel, parentSession string
	var agentActive bool
	if createdBy != ActorHuman {
		def := ActorCLI
		if createdBy == ActorDelegate {
			def = ActorDelegate
		}
		agentLabel = cmp.Or(NormalizeAgentName(o.Agent), def)
		if o.ParentSession != "" && o.ParentSession != id {
			parentSession = o.ParentSession
		}
		agentActive = true
		if o.StampInput || len(o.Command) > 0 {
			lastCLI = now
		}
	}
	s := &session{
		id: id, shell: name, command: len(o.Command) > 0, created: now, p: p, dir: dir, home: home,
		runAs:          sp.user,
		createdBy:      createdBy,
		lastCLIInputAt: lastCLI,
		agentLabel:     agentLabel,
		agentActive:    agentActive,
		parentSession:  parentSession,
		clients:        map[*attached]struct{}{}, changed: changed,
		cols: cols, rows: rows, done: make(chan struct{}),
	}
	s.graphics.home = home
	if err := s.newVT(); err != nil {
		p.hangup()
		return nil, err
	}
	s.vt.Feed(o.prelude)
	s.run(p.wait)
	return s, nil
}

// newVT gives the session its emulator, at the session's size.
func (s *session) newVT() error {
	// The emulator's Conn reads nothing, and what it answers programs is
	// typed only while no client's terminal answers them.
	pr, pw := io.Pipe()
	vt, err := terminal.New(terminal.Options{Conn: &vtConn{r: pr, s: s}, Scrollback: scrollback, OptionAsAlt: true, OnBell: func() { s.bells.Add(1); s.notify() }})
	if err != nil {
		return err
	}
	vt.Resize(s.cols, s.rows)
	s.vt, s.vtIn, s.vtAnswers = vt, pw, false
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
		// When it cannot be taken, the escape sequences stay the only form
		// the session's screen survives in.
		s.ghostty, _ = s.vt.GhosttySnapshot()
		s.text = s.vt.Text()
		s.title = s.vt.Title()
		s.exited = true
		s.clearPendingResizeLocked()
		s.clearSizeLockLocked()
		s.status.drop()
		s.icon.drop()
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
	"OMPCODE": true, "PI_SESSION_FILE": true, "PI_SESSION_ID": true, "PI_TOOL_CALL_ID": true,
	"__PI_NATIVE_VARIANT_CACHE": true,
}

// rexMarkers are the starterMarkers that Rex itself sets. A Rex session
// has them, but it is not an agent's, so they are no proof of an agent.
var rexMarkers = map[string]bool{
	"REX_SESSION": true, "REX_BLOCK": true, "REX_SERVER": true, "REX_BIN_DIR": true,
}

func starterMarker(k string) bool { return starterMarkers[k] }

// agentMarker reports a marker that only an agent sets.
func agentMarker(k string) bool { return starterMarkers[k] && !rexMarkers[k] }

// anyValue in agentEnvVars means the variable goes whatever its value.
var anyValue []string

// agentEnvVars are the variables an agent's tool shell sets, with the
// values it sets, so its commands never wait for a person: no color, no
// pager, no editor, no prompt. A terminal of a person needs none of them.
// cleanEnv drops one only when an agent started this process and the value
// is the agent's, so a user's own EDITOR=nvim or PAGER=less stays. User
// configuration such as npm_config_cache stays too. Names are upper case;
// see foldName.
var agentEnvVars = map[string][]string{
	"NO_COLOR": anyValue, "CI": {"true", "1"}, "AGENT": {"1"},
	"PAGER": {"cat"}, "GIT_PAGER": {"cat"}, "GH_PAGER": {"cat"}, "MANPAGER": {"cat"},
	"BAT_PAGER": {"cat"}, "DELTA_PAGER": {"cat"}, "GLAB_PAGER": {"cat"},
	"HOMEBREW_PAGER": {"cat"}, "MYSQL_PAGER": {"cat"}, "PSQL_PAGER": {"cat"},
	"SYSTEMD_PAGER": {"cat"}, "AWS_PAGER": {""},
	"EDITOR": {"true"}, "VISUAL": {"true"}, "GIT_EDITOR": {"true"}, "GIT_SEQUENCE_EDITOR": {"true"},
	"GIT_TERMINAL_PROMPT": {"0"}, "GCM_INTERACTIVE": {"never"}, "GH_PROMPT_DISABLED": {"1"},
	"GPG_TTY": {"not a tty"}, "SSH_ASKPASS": {"false"}, "DEBIAN_FRONTEND": {"noninteractive"},
	"CLOUDSDK_CORE_DISABLE_PROMPTS": {"1"}, "COMPOSER_NO_INTERACTION": {"1"},
	"TF_INPUT": {"0"}, "TF_IN_AUTOMATION": {"1"}, "PIP_NO_INPUT": {"1"},
	"PIP_DISABLE_PIP_VERSION_CHECK": {"1"}, "NPM_CONFIG_YES": {"true"}, "NPM_CONFIG_PROGRESS": {"false"},
	"NPM_CONFIG_AUDIT": {"false"}, "NPM_CONFIG_FUND": {"false"}, "NPM_CONFIG_UPDATE_NOTIFIER": {"false"},
	"YARN_ENABLE_PROGRESS_BARS": {"0"}, "YARN_ENABLE_TELEMETRY": {"0"},
	"PNPM_DISABLE_SELF_UPDATE_CHECK": {"true"}, "PNPM_UPDATE_NOTIFIER": {"false"},
	"CARGO_TERM_PROGRESS_WHEN": {"never"},
}

// foldName upper-cases the names that match without case: all on Windows,
// and npm_config_* everywhere, as npm reads both cases on every platform.
func foldName(k string) string {
	if runtime.GOOS == "windows" || strings.HasPrefix(strings.ToLower(k), "npm_config_") {
		return strings.ToUpper(k)
	}
	return k
}

// cleanEnv returns environ without starter markers. When an agent started
// this process (an agent marker is present, or AGENT=1), it also drops the
// agent's non-interactive variables that still have the agent's value. It
// must run where the markers are still visible, so the server gets the
// cleaned environment and its sessions inherit it. A server cleaned
// before AGENT=1 stayed has no markers, but AGENT=1 still marks it, so an
// upgrade cleans it too.
func cleanEnv(environ []string) []string {
	agent := false
	for _, kv := range environ {
		if k, v, _ := strings.Cut(kv, "="); agentMarker(k) || foldName(k) == "AGENT" && v == "1" {
			agent = true
			break
		}
	}
	var env []string
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if starterMarker(k) {
			continue
		}
		if vals, ok := agentEnvVars[foldName(k)]; agent && ok && (vals == nil || slices.Contains(vals, v)) {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// serverEnv returns the environment for a server process: the current one
// without starter markers, so a secret never lives in the server, and
// without an agent's non-interactive variables.
func serverEnv() []string { return cleanEnv(os.Environ()) }

// sessionEnv returns the environment of a session's shell: the server's,
// without what other terminals set, and what terminal apps set. A local
// session gets the gorex shim on PATH; one a remote client made does not,
// so its shell cannot reach this machine's local socket as a local client,
// and neither does one of another user run (runAs): the server is not its
// to reach either. Such a session's identity is the user's, not the
// server's, which may run as root.
func sessionEnv(id string, extra []string, local bool, runAs *sessionUser) []string {
	if runAs != nil {
		local = false
	}
	var env []string
	hasLang := false
	// The server's environment is clean already (serverEnv). cleanEnv here
	// only guards a server run by hand with AGENT=1 or a marker.
	for _, kv := range cleanEnv(os.Environ()) {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case foldName(k) == "PATH" && local:
			continue // added below, with the gorex directory first; Windows's is Path
		case runAs != nil && (foldName(k) == "HOME" || foldName(k) == "USER" ||
			foldName(k) == "USERNAME" || foldName(k) == "LOGNAME" || foldName(k) == "SHELL" || k == "MAIL"):
			continue // the session's own, below
		case k == "TERM", k == "COLORTERM", k == "TERM_PROGRAM", k == "TERM_PROGRAM_VERSION",
			k == "TERM_SESSION_ID", k == "TMUX", k == "TMUX_PANE", k == "STY", k == "WINDOWID",
			k == "COLUMNS", k == "LINES", k == "SHLVL", k == "OLDPWD", k == "PWD", k == "_",
			strings.HasPrefix(k, "ITERM_"), strings.HasPrefix(k, "GHOSTTY_"), strings.HasPrefix(k, "KITTY_"),
			strings.HasPrefix(k, "VSCODE_"), strings.HasPrefix(k, "WEZTERM_"), strings.HasPrefix(k, "ALACRITTY_"),
			strings.HasPrefix(k, "GOREX_"), strings.HasPrefix(k, "MYGO_"):
			continue
		case k == "LANG" || k == "LC_ALL" || k == "LC_CTYPE":
			hasLang = true
		}
		env = append(env, kv)
	}
	env = append(env,
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		// Every client of a session draws it with Ghostty, and programs
		// pick features by this name, as omp its Kitty image placeholders,
		// and by the version of the Ghostty whose libghostty-vt draws it.
		"TERM_PROGRAM=ghostty",
		"TERM_PROGRAM_VERSION="+terminal.GhosttyVersion,
		"GOREX_SESSION="+id,
	)
	if runAs != nil {
		env = append(env, "HOME="+runAs.home, "USER="+runAs.name, "LOGNAME="+runAs.name)
		if runAs.shell != "" {
			env = append(env, "SHELL="+runAs.shell)
		}
	}
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
	return fillRegistryEnv(append(env, extra...), runAs != nil)
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
			s.emit(data, graphics, n)
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

// emit feeds the session's screen with data, n bytes the terminal printed
// with the graphics it rewrote, and sends it to the clients.
func (s *session) emit(data []byte, graphics []graphicsSeq, n int) {
	s.mu.Lock()
	beforeStatus := s.status.updates
	queries := s.status.feed(data)
	statusChanged := s.status.updates != beforeStatus
	beforeIcon := s.icon.updates
	iconQueries := s.icon.feed(data)
	iconChanged := s.icon.updates != beforeIcon
	if queries > 0 || iconQueries > 0 {
		s.mu.Unlock()
		// Answer before feeding the emulator or clients so the reply reaches
		// the program ahead of any trailing DA1 (CSI c) sentinel.
		for range queries {
			s.write(statusReply)
		}
		for range iconQueries {
			s.write(iconReply)
		}
		s.mu.Lock()
	}
	s.graphics.record(graphics)
	s.answerLocked()
	s.vt.Feed(data)
	s.modes.feed(data)
	s.batch(data)
	s.output += uint64(n)
	s.lastOutput = time.Now()
	s.sampler.ask()
	for a := range s.clients {
		a.send(data)
	}
	s.mu.Unlock()
	if statusChanged || iconChanged {
		s.notify()
	}
}

// attach attaches a connection: it is answered once it is one of the
// session's clients, whose resizes on its control connection are then
// its own (resizeFrom); it gets a snapshot of the session's screen and
// scrollback, at the size the server gave the session for it
// (resizeForAttach), then what the session prints, and what it sends is
// typed, typed first, what the server read past the attach's line. via
// is the control connection it attached through (Attach.Conn), if any;
// o.History, o.Framed and o.Snapshot shape the snapshot.
func (s *session) attach(conn net.Conn, peer string, o Attach, via net.Conn, typed []byte) {
	a := &attached{conn: conn, s: s, via: via, peer: peer, since: time.Now(), remote: peer != "", client: o.Client, mute: o.Mute}
	if o.Cols > 0 && o.Rows > 0 {
		a.cols, a.rows = o.Cols, o.Rows
	}
	s.mu.Lock()
	s.waitBatchLocked()
	// An attach of escape sequences renders the scrollback off the lock,
	// from the binary snapshot, as a resync does (attachSnapshot): the
	// render describes the screen at this point, and output after it
	// reaches the client through its queue. A session that exited or
	// whose binary snapshot fails renders under the lock.
	var bin []byte
	if o.Snapshot != SnapshotGhostty && !s.exited {
		bin, _ = s.vt.GhosttySnapshot()
	}
	if bin == nil {
		a.first = s.snapshotLocked(o)
	}
	prev := s.authorityLocked()
	exited := s.exited
	if exited {
		a.finish()
	} else {
		s.attaches++
		a.seq = s.attaches
		s.clients[a] = struct{}{}
		if o.Focused {
			s.typist = a
		}
	}
	pty := o.Focused && !exited && s.followAuthorityLocked(prev)
	cols, rows := s.cols, s.rows
	var title string
	var graphics []byte
	if bin != nil {
		title, graphics = s.vt.Title(), s.graphics.replay()
	}
	s.mu.Unlock()
	if bin != nil {
		if a.first = attachSnapshot(o, bin, title, graphics, cols, rows); a.first == nil {
			// The render failed: drop what queued meanwhile and take the
			// snapshot under the lock, as a resync's fallback does.
			s.mu.Lock()
			a.mu.Lock()
			a.pending, a.queued = nil, 0
			a.mu.Unlock()
			a.first = s.snapshotLocked(o)
			s.mu.Unlock()
		}
	}
	conn.Write([]byte(`{"ok":true}` + "\n"))
	go a.writer()
	if pty {
		s.resizeTerminal(cols, rows)
	}
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
	s.leave(a)
}

// leave drops a client that is gone, and lets the session's size follow
// the authority that is left: the typist, or the newest attaching.
func (s *session) leave(a *attached) {
	s.sized(func() bool { return s.dropLocked(a) })
}

// dropLocked drops client a, s.mu held, as leave does. It reports whether to resize the terminal to the
// session's size, which the caller does once it let go of s.mu.
func (s *session) dropLocked(a *attached) bool {
	prev := s.authorityLocked()
	if _, ok := s.clients[a]; ok {
		delete(s.clients, a)
		a.finish()
	}
	if s.typist == a {
		s.typist = nil
	}
	return s.followAuthorityLocked(prev)
}

// input types what client a sends.
func (s *session) input(a *attached, p []byte) {
	if p := s.admit(a, p); len(p) > 0 {
		s.write(p)
		if a != nil && s.typed != nil && len(dropFocusReports(dropReplies(p))) > 0 {
			s.typed()
		}
	}
}

func normalizeCreatedBy(v string) string {
	switch v {
	case ActorCLI, ActorDelegate:
		return v
	default:
		return ActorHuman
	}
}

// inputProgram types what a client that is not attached sends, without
// the terminal's answers a program would read as its own.
func (s *session) inputProgram(p []byte) error {
	return s.inputProgramWithActor(p, "", "", "", "", false)
}

// inputFromActor types p on behalf of actor ("cli", "delegate", or "human"),
// stamping lastCLIInputAt for non-human actors or clearing the active agent
// mark and emitting a takeover event when a human UI action sent p.
func (s *session) inputFromActor(p []byte, actor, agent, parentSession, via string) error {
	return s.inputProgramWithActor(p, actor, agent, parentSession, via, true)
}

func (s *session) inputProgramWithActor(p []byte, actor, agent, parentSession, via string, trackActor bool) error {
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
	now := time.Now()
	s.lastInput = now
	var takeover TakeoverEvent
	var tookOver bool
	if trackActor {
		if actor == ActorHuman {
			takeover, tookOver = s.takeOverLocked(cmp.Or(via, "paste"), now)
		} else {
			def := ActorCLI
			if actor == ActorDelegate {
				def = ActorDelegate
			}
			if !now.After(s.lastCLIInputAt) {
				now = s.lastCLIInputAt.Add(time.Nanosecond)
			}
			s.lastCLIInputAt = now
			s.tookOverAt = time.Time{}
			s.agentLabel = cmp.Or(NormalizeAgentName(agent), s.agentLabel, def)
			if parentSession != "" && parentSession != s.id {
				s.parentSession = parentSession
			}
			s.agentActive = true
		}
	}
	s.mu.Unlock()
	if tookOver {
		s.emitTakeover(takeover)
	} else if trackActor && actor != ActorHuman && s.changed != nil {
		s.changed()
	}
	s.write(p)
	if s.typed != nil {
		s.typed()
	}
	return nil
}

// takeOverLocked transitions agentActive from true to false on human UI input
// while s.mu is held, returning the edge-triggered TakeoverEvent.
func (s *session) takeOverLocked(via string, now time.Time) (TakeoverEvent, bool) {
	if !s.agentActive || s.exited {
		return TakeoverEvent{}, false
	}
	s.agentActive = false
	s.tookOverAt = now
	return TakeoverEvent{
		SID:            s.id,
		Agent:          cmp.Or(s.agentLabel, s.delegatedAgent, ActorCLI),
		CreatedBy:      cmp.Or(s.createdBy, ActorHuman),
		ParentSession:  s.parentSession,
		Via:            via,
		LastCLIInputAt: s.lastCLIInputAt,
		At:             now,
	}, true
}

func (s *session) emitTakeover(ev TakeoverEvent) {
	if s.onTakeover != nil {
		s.onTakeover(ev)
	}
}

// clearAgent clears the session's active agent mark when a person types in a
// UI client, emitting a takeover event on the true -> false edge.
func (s *session) clearAgent(via string) {
	s.mu.Lock()
	ev, ok := s.takeOverLocked(via, time.Now())
	s.mu.Unlock()
	if ok {
		s.emitTakeover(ev)
	}
}

// write types p into the program, one writer at a time: a writer's p
// lands whole, and what another writes lands between its writes.
func (s *session) write(p []byte) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.p.write(p)
}

// paste writes text to the program as the terminal's paste keys would:
// bracketed when the program asked for bracketed pastes, else with its
// newlines as returns. A console that drops the terminal's paste event, a
// ConPTY on Windows, gets a paste this way.
func (s *session) paste(text string) error {
	return s.pasteFromActor(text, ActorCLI, "", "")
}

// pasteFromActor writes text as a paste and updates actor bookkeeping.
func (s *session) pasteFromActor(text, actor, agent, parentSession string) error {
	if text == "" {
		return nil
	}
	return s.inputFromActor(encodePaste(text, s.bracketedPaste()), actor, agent, parentSession, "paste")
}

// bracketedPaste reports whether the foreground program has enabled DEC
// private mode 2004 (bracketed paste).
func (s *session) bracketedPaste() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modes.bracketed
}

// plainText returns the session's rendered screen text without ANSI escape
// sequences, using the live terminal emulator when running or the saved exit
// text after exit.
func (s *session) plainText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exited {
		if s.text != "" {
			return strings.TrimSpace(s.text)
		}
		return stripANSI(string(s.screen))
	}
	if s.vt != nil {
		return strings.TrimSpace(s.vt.Text())
	}
	return stripANSI(string(s.screen))
}

// admit returns what of p, which client a sends, the program gets. Every
// terminal attached answers the program's queries, and the program would
// read each answer once per client: only one client's answers reach
// it (answererLocked).
// What the server types itself, a nil client's, goes as it is.
func (s *session) admit(a *attached, p []byte) []byte {
	typed := dropReplies(p)
	var takeover TakeoverEvent
	var tookOver bool
	s.sized(func() bool {
		prev := s.authorityLocked()
		now := time.Now()
		if a != nil && len(dropFocusReports(typed)) > 0 {
			s.typist = a
			takeover, tookOver = s.takeOverLocked("pty", now)
		}
		if a != nil && s.answererLocked() != a {
			p = typed
		}
		pty := a != nil && s.followAuthorityLocked(prev)
		if s.exited || len(p) == 0 {
			p = nil
		} else {
			s.lastInput = now
		}
		return pty
	})
	if tookOver {
		s.emitTakeover(takeover)
	}
	return p
}

// dropFocusReports returns p without the reports of the terminal's window
// gaining or losing focus (CSI I, CSI O): a terminal sends them as its
// window changes, without the user typing.
func dropFocusReports(p []byte) []byte {
	if bytes.IndexByte(p, 0x1b) < 0 {
		return p
	}
	p = bytes.ReplaceAll(p, []byte("\x1b[I"), nil)
	return bytes.ReplaceAll(p, []byte("\x1b[O"), nil)
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

// info describes the session and what runs in its foreground.
func (s *session) info() SessionInfo {
	s.mu.Lock()
	title := s.title
	if !s.exited {
		title = s.vt.Title()
	}
	in := SessionInfo{
		ID: s.id, Shell: s.shell, Command: s.command, Created: s.created, Owner: s.owner,
		DelegatedFrom: s.delegatedFrom, DelegatedAgent: s.delegatedAgent,
		AgentActor: AgentActor{
			CreatedBy:      cmp.Or(s.createdBy, ActorHuman),
			LastCLIInputAt: s.lastCLIInputAt,
			Agent:          s.agentLabel,
			Active:         s.agentActive && !s.exited,
			ParentSession:  s.parentSession,
			TookOverAt:     s.tookOverAt,
		},
		Title: title, LastOutput: s.lastOutput, LastInput: s.lastInput,
		Output: s.output, Bells: int(s.bells.Load()), Exited: s.exited, ExitCode: s.code,
		Attached: len(s.clients), Cols: s.cols, Rows: s.rows,
		SizeLock: s.sizeLock,
	}
	// The authority's id, and none while a device holds the size: no
	// attached terminal's size is the session's then.
	if a := s.authorityLocked(); a != nil && s.sizeLock == "" {
		in.SizedBy = a.client
	}
	s.mu.Unlock()
	in.PID = s.p.pid()
	in.ConPTY = s.p.conpty()
	if !in.Exited {
		s.foreground(&in)
	}
	s.mu.Lock()
	// What it saw is the latest look, as a conversation Claude Code
	// names only after it started, which no output tells.
	if !in.Exited {
		s.see(SessionInfo{Title: in.Title, PID: in.PID, Foreground: in.Foreground, Idle: in.Idle, Program: in.Program, Args: in.Args,
			Dir: in.Dir, AgentSession: in.AgentSession, AgentConfigDir: in.AgentConfigDir, agentFile: in.agentFile})
	}
	in.Statuses, in.StatusSeen, in.ProgramIcon = s.status.list(), s.status.seen, s.icon.get()
	s.mu.Unlock()
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
	in.Foreground = fg
	in.Idle = fg == in.PID
	p := inspect(fg)
	in.Program, in.Args, in.Dir = p.programName(), p.args, p.dir
	if in.Program == "" {
		in.Program = s.shell
	}
	if in.Dir == "" && !in.Idle {
		in.Dir = inspect(in.PID).dir
	}
	switch {
	case in.Idle:
	case strings.EqualFold(in.Program, "claude"):
		in.AgentSession, in.AgentConfigDir = claudeSession(fg, p.dir, s.home)
	case strings.EqualFold(in.Program, "omp"):
		in.AgentSession, in.AgentConfigDir, in.agentFile = ompSession(procEnvOf(fg), p.dir, p.args, s.p.tty(), s.home)
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
	s.see(now)
	s.mu.Unlock()
}

// see keeps what a look at the foreground saw, a sample's or an info's,
// which the layout's facts are stamped from (facts), and tells the
// subscribers when it changed. s.mu is held.
func (s *session) see(now SessionInfo) {
	was := s.seen
	s.seen = now
	s.ran = s.ran || !now.Idle
	// A program that went back to the shell is no longer working nor
	// blocked. Only the change counts: a session whose process is the
	// program is idle all along.
	if now.Idle && was.PID != 0 && !was.Idle {
		s.status.drop()
		s.icon.drop()
	}
	if now.Title != was.Title || now.Program != was.Program || now.Dir != was.Dir || now.Idle != was.Idle || !slices.Equal(now.Args, was.Args) ||
		now.AgentSession != was.AgentSession || now.AgentConfigDir != was.AgentConfigDir {
		s.notify()
	}
}

// facts are what the session's last look saw run in its foreground (see),
// with its size, whether it exited and whether a program ran (ran);
// sampled is false before a look, which its first output asks for.
func (s *session) facts() (in SessionInfo, sampled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in = s.seen
	in.Cols, in.Rows, in.Exited, in.ran = s.cols, s.rows, s.exited, s.ran
	return in, s.seen.PID != 0
}

// seeStatus tells that a client showed the user the session's status
// records updated till at, and tells the subscribers when that is news.
func (s *session) seeStatus(at time.Time) {
	s.mu.Lock()
	changed := s.status.see(at)
	s.mu.Unlock()
	if changed {
		s.notify()
	}
}

// notify tells the server's subscribers that the session changed.
func (s *session) notify() {
	if s.changed != nil {
		s.changed()
	}
}
