//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"encoding/json"
	"io"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
)

// renderResync returns what snapshotLocked returns for an attach with
// resyncHistory, from bin, the session's Ghostty binary snapshot, and its
// title, graphics and rows: bin restores in a terminal no program runs,
// whose escape sequences are the session's.
func renderResync(bin []byte, title string, graphics []byte, rows int) ([]byte, error) {
	snap, _, err := renderSnapshot(bin, title, graphics, rows, resyncHistory)
	return snap, err
}

// renderSnapshot returns what snapshotLocked's escape-sequence path
// returns for an attach with history, rendered off the session's lock
// from bin, the session's Ghostty binary snapshot, and its title,
// graphics and rows; cut is what trimSnapshot reports.
func renderSnapshot(bin []byte, title string, graphics []byte, rows, history int) (snap []byte, cut bool, err error) {
	pr, pw := io.Pipe()
	defer pw.Close()
	term, err := terminal.New(terminal.Options{Conn: quietConn{pr}, Scrollback: scrollback})
	if err != nil {
		return nil, false, err
	}
	defer term.Close()
	if err := term.RestoreGhosttySnapshot(bin); err != nil {
		return nil, false, err
	}
	snap = append(term.Snapshot(), graphics...)
	if history > 0 {
		snap, cut = trimSnapshot(snap, rows, history)
	}
	if title != "" {
		snap = append([]byte("\x1b]2;"+title+"\x07"), snap...)
	}
	return snap, cut, nil
}

// attachSnapshot returns what snapshotLocked returns for o, rendered off
// the session's lock from bin, the Ghostty binary snapshot taken at the
// attach: it is nil when the render fails, and the attach takes the
// snapshot under the lock instead.
func attachSnapshot(o Attach, bin []byte, title string, graphics []byte, cols, rows int) []byte {
	snap, cut, err := renderSnapshot(bin, title, graphics, rows, o.History)
	if err != nil {
		return nil
	}
	if !o.Framed {
		return snap
	}
	head, _ := json.Marshal(SnapshotHeader{Snapshot: len(snap), Cols: cols, Rows: rows, Truncated: cut})
	return append(append(head, '\n'), snap...)
}

// quietConn connects a terminal no program runs: what it answers goes
// nowhere.
type quietConn struct{ r *io.PipeReader }

func (c quietConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c quietConn) Write(p []byte) (int, error) { return len(p), nil }
func (c quietConn) Close() error                { return c.r.Close() }

// answererLocked returns the client whose terminal answers the program's
// queries: the authority, unless it is mute; nil when there is none, and
// the server's emulator answers, at the session's size. s.mu is held.
func (s *session) answererLocked() *attached {
	if a := s.authorityLocked(); a != nil && !a.mute {
		return a
	}
	return nil
}

// answerLocked tells the emulator's Conn, in order with what the emulator
// writes, whether its answers to the output fed next reach the program:
// as the output is fed, not as the answers are written, so that a client
// attaching or leaving in between makes no answer twice or none. s.mu is
// held.
func (s *session) answerLocked() {
	if on := s.answererLocked() == nil; on != s.vtAnswers {
		s.vtAnswers = on
		mark := vtOff
		if on {
			mark = vtOn
		}
		s.vt.Send(mark)
	}
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

// HasHumanInputBytes reports whether p carries genuine human keyboard or paste
// input rather than terminal query replies (DA, CPR, DSR, DECRPM, XTWINOPS,
// Kitty flags, OSC/DCS/APC), clipboard read refusals (OSC 5522 EPERM/DONE),
// window focus reports (CSI I, CSI O), mouse/wheel/motion reports (SGR, URXVT,
// X10), or Kitty keyboard protocol key-release and modifier-only events.
func HasHumanInputBytes(p []byte) bool {
	if len(p) == 0 {
		return false
	}
	if bytes.IndexByte(p, 0x1b) < 0 {
		return true
	}
	for i := 0; i < len(p); {
		if n := nonHumanInputLen(p[i:]); n > 0 {
			i += n
			continue
		}
		return true
	}
	return false
}

func nonHumanInputLen(p []byte) int {
	if len(p) < 3 || p[0] != 0x1b {
		return 0
	}
	if bytes.HasPrefix(p, []byte("\x1b[I")) || bytes.HasPrefix(p, []byte("\x1b[O")) {
		return 3
	}
	if n := replyLen(p); n > 0 {
		return n
	}
	switch p[1] {
	case ']':
		// OSC 5522 is left untouched by replyLen so clipboard paste packets reach
		// the program. Only a paste offer (type=paste) or granted paste payload
		// (type=read:status=OK / status=DATA) is human input; policy denials such
		// as status=EPERM and trailing status=DONE frames are terminal replies.
		if bytes.HasPrefix(p, []byte("\x1b]5522;")) {
			if isKittyPasteInput(p) {
				return 0
			}
			return controlStringLen(p)
		}
	case '^', 'X': // PM, SOS control strings
		return controlStringLen(p)
	case '[':
		if n := mouseReportLen(p); n > 0 {
			return n
		}
		if n := kittyNonTypingKeyLen(p); n > 0 {
			return n
		}
	}
	return 0
}

func isKittyPasteInput(p []byte) bool {
	return bytes.HasPrefix(p, []byte("\x1b]5522;type=paste")) ||
		bytes.HasPrefix(p, []byte("\x1b]5522;type=read:status=DATA")) ||
		bytes.HasPrefix(p, []byte("\x1b]5522;type=read:status=OK"))
}

func controlStringLen(p []byte) int {
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
	return 0
}

// mouseReportLen matches SGR (CSI < Cb;Cx;Cy M/m), URXVT (CSI Cb;Cx;Cy M), and
// X10/normal (CSI M cb cx cy) pointer, click, drag, and wheel reports.
func mouseReportLen(p []byte) int {
	if len(p) < 6 || p[0] != 0x1b || p[1] != '[' {
		return 0
	}
	if p[2] == '<' {
		j := 3
		digits := false
		for j < len(p) && (p[j] >= '0' && p[j] <= '9' || p[j] == ';') {
			if p[j] >= '0' && p[j] <= '9' {
				digits = true
			}
			j++
		}
		if digits && j < len(p) && (p[j] == 'M' || p[j] == 'm') {
			return j + 1
		}
		return 0
	}
	if p[2] == 'M' && p[3] >= 0x20 && p[4] >= 0x21 && p[5] >= 0x21 {
		return 6
	}
	// URXVT mouse: ESC [ Cb ; Cx ; Cy M (at least two semicolons).
	j, semis, digits := 2, 0, false
	for j < len(p) && (p[j] >= '0' && p[j] <= '9' || p[j] == ';') {
		if p[j] == ';' {
			semis++
		} else {
			digits = true
		}
		j++
	}
	if digits && semis >= 2 && j < len(p) && p[j] == 'M' {
		return j + 1
	}
	return 0
}

// kittyNonTypingKeyLen matches Kitty keyboard protocol key-release sequences
// (event type :3 before the final byte, as CSI 97;1:3u or CSI 1;1:3A) and
// modifier-only key events (codes 57441..57454 ending in 'u').
func kittyNonTypingKeyLen(p []byte) int {
	if len(p) < 4 || p[0] != 0x1b || p[1] != '[' {
		return 0
	}
	code, hasCode := 0, false
	j := 2
	for j < len(p) && p[j] >= '0' && p[j] <= '9' {
		code = code*10 + int(p[j]-'0')
		hasCode = true
		j++
	}
	for j < len(p) && (p[j] >= '0' && p[j] <= '9' || p[j] == ';' || p[j] == ':') {
		j++
	}
	if j >= len(p) {
		return 0
	}
	f := p[j]
	if f != 'u' && f != '~' && (f < 'A' || f > 'Z') {
		return 0
	}
	params := p[2:j]
	// Check for Kitty event-type ":3" (release) in the modifier parameter (2nd field).
	if semi := bytes.IndexByte(params, ';'); semi >= 0 {
		rest := params[semi+1:]
		if end := bytes.IndexByte(rest, ';'); end >= 0 {
			rest = rest[:end]
		}
		if colon := bytes.IndexByte(rest, ':'); colon >= 0 && bytes.Equal(rest[colon+1:], []byte("3")) {
			return j + 1
		}
	}
	if f == 'u' && hasCode && code >= 57441 && code <= 57454 {
		return j + 1
	}
	return 0
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
	s.sized(func() bool {
		if s.exited || len(s.clients) == 0 {
			return false
		}
		snap, _ := s.view()
		if !bytes.Contains(snap, []byte("\x1b[?1049h")) {
			return false
		}
		// Clear the alternate screen the window shows, then draw it anew.
		msg := append([]byte("\x1b[?1049h\x1b[H\x1b[2J"), snap...)
		for a := range s.clients {
			a.send(msg)
		}
		return false
	})
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

// snapshotLocked returns what a client attaching with o gets first: the
// session's screen and scrollback, as the escape sequences that replay
// them, or for a "ghostty" attach as Ghostty's binary snapshot, followed
// by the title and Kitty graphics the escape sequences carry. A framed
// one puts a SnapshotHeader line before it, Format and all. s.mu is held.
func (s *session) snapshotLocked(o Attach) []byte {
	if o.Snapshot == SnapshotGhostty {
		if snap, after, ok := s.ghosttyView(); ok {
			var first []byte
			if o.Framed {
				head, _ := json.Marshal(SnapshotHeader{Snapshot: len(snap), Cols: s.cols, Rows: s.rows, Format: SnapshotGhostty})
				first = make([]byte, 0, len(head)+1+len(snap)+len(after))
				first = append(append(append(first, head...), '\n'), snap...)
			} else {
				first = append(make([]byte, 0, len(snap)+len(after)), snap...)
			}
			return append(first, after...)
		}
		// With no binary snapshot to send, the escape sequences go as
		// they do for a client that asked for none: Format stays empty,
		// which tells the client which it got.
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
	return snap
}

// ghosttyView returns the session's screen in Ghostty's binary snapshot
// format, and the title and Kitty graphics an attach sends after it, which
// that format does not carry; ok is false when the session has none, as an
// exited one whose terminal took none. s.mu is held.
func (s *session) ghosttyView() (snap, after []byte, ok bool) {
	title := s.title
	if s.exited {
		if s.ghostty == nil {
			return nil, nil, false
		}
		snap = s.ghostty
	} else {
		b, err := s.vt.GhosttySnapshot()
		if err != nil {
			return nil, nil, false
		}
		snap, title = b, s.vt.Title()
	}
	if title != "" {
		after = append(after, "\x1b]2;"+title+"\x07"...)
	}
	return snap, append(after, s.graphics.replay()...), true
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

// vtConn is the Conn of a session's emulator: it reads from a pipe that
// closes with the session, and types what the emulator answers between
// vtOn and vtOff (answerLocked), else drops it. Colors it does not
// answer: the emulator's theme is not the windows'.
type vtConn struct {
	r  *io.PipeReader
	s  *session
	on bool
	// rest is the start of an answer, whose end a later write brings.
	rest []byte
}

// vtOn and vtOff mark where the emulator's answers start and stop
// reaching the program; no answer starts with NUL. Each is a write of
// its own: Send queues it apart from the emulator's answers.
var vtOn, vtOff = []byte("\x00on"), []byte("\x00off")

func (c *vtConn) Read(p []byte) (int, error) { return c.r.Read(p) }
func (c *vtConn) Close() error               { return c.r.Close() }

// Write is called on the emulator's input goroutine with what it
// answered, which may hold several answers, or part of one (the binding
// writes what libghostty-vt writes, and cuts a long reply in pieces): it
// takes each answer whole.
func (c *vtConn) Write(p []byte) (int, error) {
	if bytes.Equal(p, vtOn) || bytes.Equal(p, vtOff) {
		c.on = bytes.Equal(p, vtOn)
		return len(p), nil
	}
	c.rest = append(c.rest, p...)
	var out []byte
	for len(c.rest) > 0 {
		n := seqLen(c.rest)
		if n == 0 {
			break
		}
		if c.on && (!colorReply(c.rest[:n]) || (c.s != nil && c.s.hasTheme.Load())) {
			out = append(out, c.rest[:n]...)
		}
		c.rest = c.rest[n:]
	}
	if len(out) > 0 && c.s != nil {
		c.s.write(out)
	}
	return len(p), nil
}

// seqLen returns the length of the escape sequence p starts with, or of
// the text up to the next one; 0 when p holds only part of a sequence.
func seqLen(p []byte) int {
	if p[0] != 0x1b {
		if i := bytes.IndexByte(p, 0x1b); i >= 0 {
			return i
		}
		return len(p)
	}
	if len(p) < 2 {
		return 0
	}
	switch p[1] {
	case ']', 'P', '_', '^', 'X':
		// A control string ends with ST, or BEL for an OSC.
		for i := 2; i < len(p); i++ {
			if p[i] == 0x07 && p[1] == ']' {
				return i + 1
			}
			if p[i] == 0x1b && i+1 < len(p) && p[i+1] == '\\' {
				return i + 2
			}
		}
		return 0
	case '[':
		for i := 2; i < len(p); i++ {
			if p[i] >= 0x40 && p[i] <= 0x7e {
				return i + 1
			}
		}
		return 0
	}
	return 2
}

// colorReply tells an answer about colors: a palette's or a theme's (OSC),
// or the color scheme's (CSI ? 997).
func colorReply(p []byte) bool {
	return bytes.HasPrefix(p, []byte("\x1b]")) || bytes.HasPrefix(p, []byte("\x1b[?997;"))
}
