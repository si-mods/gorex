package main

import (
	"errors"
	"log"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// clientOf returns the client of a host's server, this machine's for
// nil; nil while the host is not connected.
func (a *App) clientOf(h *hostConn) *rex.Client {
	if h == nil {
		return a.client
	}
	return h.current()
}

// killOn ends a session of a host; one of this machine, for nil, the
// server ends as the layout no longer has it (diffLayout).
func killOn(h *hostConn, sid string) {
	if h != nil && sid != "" {
		h.kill(sid)
	}
}

// offline tells, in an error toast, that a host is not connected, and
// reports whether it is not. The menus and the palette show what needs
// the host disabled meanwhile (command.Host); this stops a key or a
// client that asks for it.
func (a *App) offline(h *hostConn) bool {
	if h == nil || h.current() != nil {
		return false
	}
	a.toastError(locales.HostsNotConnectedTo(a.hostName(h)))
	return true
}

// newPaneRun makes a pane with a new session in dir on host h, that runs
// command instead of the login shell when given, as a Rex client asks for.
// The host answers in its time, up to the client's timeout, and the window
// goes on meanwhile: place puts the pane on the main thread once the host
// made its session, after the panes asked of the host before it, and
// reports whether the pane has a place still; the session of one that has
// not ends. One the host does not make shows its error, and no pane.
func (a *App) newPaneRun(h *hostConn, dir string, cols, rows int, command []string, place func(*Pane) bool) {
	a.newPaneIn(h, rex.CreateOptions{Dir: dir, Cols: cols, Rows: rows, Command: command}, place)
}

// newPaneIn is newPaneRun of the session o describes, which a strict
// directory (rex.CreateOptions.DirStrict) the host does not have fails.
func (a *App) newPaneIn(h *hostConn, o rex.CreateOptions, place func(*Pane) bool) {
	dir, cols, rows, command := o.Dir, o.Cols, o.Rows, o.Command
	if cols <= 0 {
		cols, rows = 80, 24
	}
	id := a.id()
	env := []string{"GOREX_PANE=" + strconv.Itoa(id), "GOREX_HOST=" + h.Name}
	c := a.clientOf(h)
	update := h.update
	if update == nil {
		update = a.updater()
	}
	prev, done := h.creating, make(chan struct{})
	h.creating = done
	go func() {
		defer close(done)
		var info rex.SessionInfo
		err := errors.New("rex: connection to the server lost")
		if c != nil {
			info, err = c.Create(rex.CreateOptions{Command: command, Dir: dir, DirStrict: o.DirStrict, Cols: cols, Rows: rows, Env: env})
		}
		if err == nil {
			// The host's next list may have it before its pane: adopt
			// leaves it for the pane.
			h.expecting(info.ID, true)
		}
		if prev != nil {
			<-prev
		}
		update(func() {
			if err != nil {
				log.Printf("creating a session: %v", err)
				if o.DirStrict && strings.Contains(err.Error(), rex.FolderMissing) {
					a.folderMissing(dir)
					return
				}
				a.toastError(locales.ToastSessionFailed(reasonOf(err)))
				return
			}
			h.expecting(info.ID, false)
			// A window closed, or a host removed, meanwhile: the session
			// goes on there, as their others do.
			if a.quitting || !slices.Contains(a.hosts, h) {
				return
			}
			p := &Pane{ID: id, SID: info.ID, info: info, startDir: dir, host: h}
			if p.info.Dir == "" {
				p.info.Dir = dir
			}
			a.attach(p, cols, rows)
			if !place(p) {
				p.end()
				killOn(h, p.SID)
			}
		})
	}()
}

// attach makes the pane's terminal, attached to its session.
func (a *App) attach(p *Pane, cols, rows int) {
	if p.SID == "" {
		opts := termOptions()
		opts.Conn = nopConn{}
		term, err := newTerm(opts)
		if err == nil {
			term.Feed([]byte("\x1b[31m" + locales.ErrorSessionStart(a.err) + "\x1b[0m\r\n"))
			p.term = term
		}
		return
	}
	// The terminal's callbacks change the state on the main thread, in
	// the window that made the pane, as long as it is open.
	update := a.updater()
	p.seenFunc = func() {
		a.tellSeen(p)
		if a.st != nil {
			a.st.recordSeen(p.SID, p.seen)
			a.st.closeNotifications(notifyKey(p))
		}
	}
	opts := termOptions()
	// The session keeps the terminal the program draws to, and the pane
	// shows what it says: its screen takes the session's size (syncSize),
	// where the program drew its output, not the pane's, and the view
	// only asks the session for the size the pane fits.
	opts.ResizeByConn = true
	onHumanInput := func() {
		update(func() {
			if p.info.AgentActor.Active {
				p.clearAgentLocal()
			}
		})
	}
	if p.host != nil {
		p.remote = newHostStream(p.host, p.SID, cols, rows)
		// A host's streams claim the focus on each attach only while the
		// user is at this machine.
		p.remote.present = userPresent
		p.remote.onChange = func() { update(func() {}) }
		p.remote.onInput = onHumanInput
		opts.Conn = p.remote
	} else {
		// A pane of this machine's session attaches through the app's
		// client, and attaches again when the stream is lost, as a host's
		// does: a stream lost for good leaves a screen that keeps what it
		// last showed and takes nothing typed.
		p.stream = newLocalStream(a.client, p.SID, cols, rows)
		p.stream.SetPresent(userPresent)
		p.stream.onChange = func() { update(func() {}) }
		p.stream.onInput = onHumanInput
		opts.Conn = p.stream
	}
	// A paste offers the bytes the clipboard holds here, an image's among
	// them: a program that reads the clipboard itself (Kitty's paste
	// events) takes them even over SSH or on a host, where a path of this
	// machine means nothing. The terminal then reads every format the
	// clipboard holds, as it does in a pane of this machine. In a pane of a
	// host whose program does not (mode 5522), or a ConPTY, an image goes
	// there as a dropped file does and its path there is pasted: the
	// program, as Claude Code, would read the host's clipboard, which does
	// not hold it (pasteToHost). Who types last takes the session's size
	// back from a device that holds it (SizeLock): the session gets the
	// pane's size again.
	opts.OnPaste = func(d transfer.Data) bool {
		p.clearAgentLocal()
		if p.host == nil {
			a.takeSize(p)
		}
		p.claimSize()
		if a.pasteToHost(p, d) {
			return true
		}
		// A console that drops the terminal's paste event, a ConPTY on
		// Windows, takes the text as a paste the session's server writes:
		// the event is an OSC, which such a console throws away. An image
		// alone has no text to write.
		if p.info.ConPTY {
			text, err := d.Read(transfer.Text)
			if err != nil {
				log.Printf("paste: reading the clipboard's text: %v", err)
			}
			a.pasteText(p, string(text))
			return true
		}
		img, ok := pasteImageData(d)
		if !ok || p.term == nil {
			// A read fails whole when one of the clipboard's
			// representations is larger than a read takes: the text, which
			// a shell or an editor wants, still pastes.
			if len(d.Formats()) == 0 && p.term != nil {
				if text := mygo.Clipboard.ReadText(); text != "" {
					p.term.Paste(text)
					return true
				}
			}
			return false
		}
		p.term.PasteData(img)
		return true
	}
	// A key with a binding runs its action; one without goes to the
	// program. Menu accelerators run before the terminal, so a sequence
	// cannot start with one of those. Command's keys are the app's
	// shortcuts.
	opts.OnKey = func(mods ui.Modifiers, key ui.Key) bool {
		if mods&ui.Super == 0 && !isModifierOnlyKey(key) {
			if p.host == nil {
				a.takeSize(p)
			}
			p.claimSize()
		}
		if a.dispatchKey(mods, key) {
			return true
		}
		if mods&ui.Super == 0 && !isModifierOnlyKey(key) && !isScrollbackKey(mods, key) {
			p.clearAgentLocal()
		}
		return false
	}
	opts.OnTitle = func(title string) {
		update(func() { p.title = title })
	}
	opts.OnBell = func() {
		update(func() { a.ring(p) })
	}
	opts.OnNotify = func(title, body string) {
		update(func() { a.notifyProgram(p, title, body) })
	}
	opts.OnSearch = func(s terminal.SearchStatus) {
		update(func() {
			if a.findPane == p {
				a.findStatus = s
				a.changed()
			}
		})
	}
	opts.OpenLink = func(link string) bool { return a.openLink(p, link) }
	opts.LinkPath = func(link string) bool { return a.isLink(p, link, update) }
	opts.OnExit = func(int) {
		update(func() {
			if p.closed || a.quitting {
				return
			}
			a.paneStopped(p)
		})
	}
	term, err := newTerm(opts)
	if err != nil {
		log.Printf("terminal: %v", err)
		a.toastError(locales.ToastTerminalFailed(reasonOf(err)))
		return
	}
	// Until a view shows it, the terminal has the session's size, so that
	// attaching changes nothing in the session.
	term.Resize(cols, rows)
	p.term = term
}

// pasteText writes a paste's text to the pane's session through its
// server, which brackets it as the program asked: a program whose console
// drops the terminal's paste event (SessionInfo.ConPTY) takes a paste this
// way.
func (a *App) pasteText(p *Pane, text string) {
	if text == "" {
		return
	}
	client, sid, host, remote := a.client, p.SID, p.host, p.remote
	go func() {
		var err error
		switch {
		case host == nil:
			err = client.PasteHuman(sid, text)
		case remote != nil:
			err = remote.Paste(text)
		}
		if err != nil {
			log.Printf("paste: %v", err)
		}
	}()
}

// takeSize takes the size of the pane's session back from the device that
// holds it, at the pane's size. The terminal's OnKey and OnPaste call it on
// the main thread, which apply sets p.info on.
func (a *App) takeSize(p *Pane) {
	if p.info.SizeLock == "" || p.term == nil {
		return
	}
	p.info.SizeLock = ""
	cols, rows := p.askedSize()
	if cols <= 0 || rows <= 0 {
		cols, rows = p.term.Size()
	}
	client, sid := a.client, p.SID
	go func() {
		if err := client.UnlockSize(sid, "", cols, rows); err != nil {
			log.Printf("taking the size back: %v", err)
		}
	}()
}

// claimSize makes the pane the session's size authority at once, as typing
// or a paste does on the server (admit): the pane takes its own size
// without waiting for the next poll of the sessions, and the program, told
// of that size, draws for the grid it now lays its output out at. A claim
// the server turns down is corrected by the next poll.
func (p *Pane) claimSize() {
	if p.term == nil || p.info.SizedBy == p.clientID() {
		return
	}
	cols, rows := p.askedSize()
	if cols <= 0 || rows <= 0 {
		return
	}
	p.info.SizedBy, p.info.Cols, p.info.Rows = p.clientID(), cols, rows
}

// askedSize returns the size the pane asked the session for, its own,
// which the program draws for while this pane's stream is the session's
// authority (SessionInfo.SizedBy).
func (p *Pane) askedSize() (cols, rows int) {
	switch {
	case p.stream != nil:
		return p.stream.Size()
	case p.remote != nil:
		return p.remote.Size()
	}
	return 0, 0
}

// paneSize returns the size of the pane's grid, which a session it opens
// takes: the size it asked its own session for, or its terminal's while
// the stream is not attached yet.
func (p *Pane) paneSize() (cols, rows int) {
	if p == nil {
		return 0, 0
	}
	if cols, rows = p.askedSize(); cols > 0 && rows > 0 {
		return cols, rows
	}
	if p.term != nil {
		return p.term.Size()
	}
	return 0, 0
}

// clientID returns the id the pane's stream attaches with, which
// SessionInfo.SizedBy names the client whose terminal sizes the session
// by.
func (p *Pane) clientID() string {
	switch {
	case p.stream != nil:
		return p.stream.ClientID()
	case p.remote != nil:
		return p.remote.ClientID()
	}
	return ""
}

// screenSize returns the size the pane's terminal takes, and whether it is
// another client's: the session's, while another window's terminal, on
// this machine or another, or a device holding the size (SizeLock), sizes
// it, so that the pane lays the program's output out at the size the
// program drew it for; else the pane's own.
func (p *Pane) screenSize() (cols, rows int, theirs bool) {
	by := p.info.SizedBy
	if (p.info.SizeLock != "" || by != "" && by != p.clientID()) && p.info.Cols > 0 && p.info.Rows > 0 {
		return p.info.Cols, p.info.Rows, true
	}
	cols, rows = p.askedSize()
	return cols, rows, false
}

// syncSizes gives each pane's terminal the size the program in its session
// draws for (screenSize). A terminal that lays the output out at another
// size wraps the lines the program did not, and the program's next frame,
// moving the cursor from where it thinks it is, lands elsewhere: what
// agents' interfaces draw in place then covers rows with rows. It runs
// each frame, on the main thread, and acts only as a size changes.
func (a *App) syncSizes() {
	for _, t := range a.tabs {
		for _, p := range t.allPanes() {
			a.syncSize(p)
		}
	}
}

// syncSize sizes the pane's terminal (syncSizes) and, when the grid it
// takes is not the one the screen was laid out for, the session's screen
// follows it (resyncPane): at another size the program's output wrapped
// where the program did not, and its frames, moving the cursor from where
// it thinks it is, drew over rows, which neither the reflow nor the
// program mends.
func (a *App) syncSize(p *Pane) {
	if p.term == nil || p.closed {
		return
	}
	cols, rows, theirs := p.screenSize()
	if cols <= 0 || rows <= 0 {
		return
	}
	if c, r := p.term.Size(); c == cols && r == rows {
		p.followed = theirs
		p.waitCols, p.waitRows, p.waitMend = 0, 0, false
		return
	}
	if !theirs && a.sizeWait(p, cols, rows) {
		return
	}
	p.term.Resize(cols, rows)
	mend := p.waitMend
	p.waitCols, p.waitRows, p.waitMend = 0, 0, false
	if theirs || p.followed || p.held > 0 || mend {
		a.resyncPane(p, cols, rows)
	}
	p.followed, p.held = theirs, 0
}

// sizeSettle is how long a pane's terminal waits past the last size its
// view asked for before it takes it (sizeWait): the session takes the
// size once it held that long (resizeSettle).
var sizeSettle = rex.ResizeSettle()

// sizeWait tells whether the pane's terminal waits for the session to
// take the size its view asked for. A size the session has settles
// first: the terminal, taking the new one then, lays each byte out at
// the size the program drew it for, while one ahead of the session
// wraps at a width the program did not draw for, and what the reflow
// made of that stays in its scrollback, which a resync's replay of the
// session mends only on the screen. A size the session already shows,
// as an explicit size taken at once (unlockSize), waits for nothing.
func (a *App) sizeWait(p *Pane, cols, rows int) bool {
	if p.waitCols != cols || p.waitRows != rows {
		p.waitCols, p.waitRows, p.waitAt = cols, rows, time.Now()
		p.waitMend = false
	}
	p.waitMend = p.waitMend || p.held > 0
	if p.info.Cols == cols && p.info.Rows == rows {
		return false
	}
	d := time.Until(p.waitAt.Add(sizeSettle))
	if d <= 0 {
		return false
	}
	if p.waitTimer == nil {
		update := a.updater()
		p.waitTimer = time.AfterFunc(d, func() {
			update(func() { p.waitTimer = nil })
		})
	}
	return true
}

// holdGrace is how many frames a pane's terminal may take to follow the
// size a divider drag's hold left it at, once the hold ends: the frame
// the pane's view asks for the drag's size, and the one after it, when
// the terminal takes it (Pane.held).
const holdGrace = 2

// resyncHistory bounds what a resync replays of the scrollback, which it
// clears: the session's screen and the last of it, not a session's whole
// history, which the session would format and the pane feed at once.
const resyncHistory = 64 << 10

// resyncPane makes the pane show its session's screen at cols×rows, the
// size its terminal took, off the UI thread: the session takes a size it
// was asked for once it settled, so the request waits for it. The server
// sends the screen on the pane's stream (rex.Client.Resync), in step with
// the output around it; a server too old for that answers with a capture,
// which the pane feeds, and one for a size the pane no longer has is
// dropped.
func (a *App) resyncPane(p *Pane, cols, rows int) {
	if p.SID == "" || p.closed || p.term == nil {
		return
	}
	sid, update, term, id := p.SID, a.updater(), p.term, p.clientID()
	var client *rex.Client
	if p.host != nil {
		client = p.host.current()
	} else {
		client = a.client
	}
	if client == nil {
		return
	}
	go func() {
		var err error
		for try := 0; id != ""; try++ {
			if err = client.Resync(sid, id, cols, rows); err == nil {
				return
			}
			if err.Error() != rex.ErrResyncSize.Error() {
				break
			}
			if try == 20 {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		var res rex.CaptureResult
		for try := 0; ; try++ {
			if res, err = client.Capture(sid, resyncHistory); err == nil && res.Cols == cols && res.Rows == rows {
				break
			}
			if try == 20 {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		fn := func() {
			// What the pane shows since is another screen's: that size
			// resyncs on its own.
			if p.closed || p.term != term {
				return
			}
			if c, r, _ := p.screenSize(); c != cols || r != rows {
				return
			}
			// The reset clears what the pane laid out for another size,
			// the scrollback and the modes among it, and the session's
			// screen takes its place.
			term.Feed(append([]byte(screenReset), res.Screen...))
		}
		update(fn)
	}()
}

// claimFocus tells every pane whether the user looks at it: the one shown
// with the window's focus, the other panes of the active tab losing it. A
// session's size follows the machine the user's focus is on. It runs each
// frame, and acts only as that changes.
func (a *App) claimFocus() {
	// Whether the user is at this machine, asked at most once a second:
	// this runs each frame, and a window that kept the focus of an
	// unattended machine claims nothing.
	was := a.focusPresent
	if a.focusedWin && time.Since(a.focusSampled) >= time.Second {
		a.focusPresent = userPresent()
		a.focusSampled = time.Now()
	}
	// The user came back to a machine that was idle while its window kept
	// the focus: the panes' focus did not change, so nothing else would
	// claim again, and their claims start over.
	if a.focusPresent && !was {
		for _, t := range a.tabs {
			for _, p := range t.allPanes() {
				p.focusClaimed = false
			}
		}
	}
	for _, t := range a.tabs {
		on := a.focusedWin && t == a.tab()
		for _, p := range t.allPanes() {
			p.setFocused(on)
		}
	}
}

// setFocused tells the pane's session whether the user looks at it, once
// it changed.
func (p *Pane) setFocused(on bool) {
	var conn any
	switch {
	case p.stream != nil:
		conn = p.stream
	case p.remote != nil:
		conn = p.remote
	}
	if on == p.focusClaimed && conn == p.focusConn {
		return
	}
	p.focusClaimed, p.focusConn = on, conn
	switch {
	case p.stream != nil:
		p.stream.SetFocused(on)
	case p.remote != nil:
		p.remote.SetFocused(on)
	}
}

// nopConn is the Conn of a terminal that only shows what the app feeds it.
type nopConn struct{}

func (nopConn) Read([]byte) (int, error)    { select {} }
func (nopConn) Write(p []byte) (int, error) { return len(p), nil }
func (nopConn) Close() error                { return nil }

func (a *App) ring(p *Pane) {
	if !a.shows(p) {
		p.askAttention()
		a.syncBadge()
	}
}

// shows tells that the user looks at pane p: the focused pane of the
// window's tab, while the window has the focus.
func (a *App) shows(p *Pane) bool {
	t := a.tab()
	return t != nil && t.Focus == p && p.Tab == t && a.focusedWin
}

// askAttention marks the pane as asking for attention: its dots pop as it
// comes to.
func (p *Pane) askAttention() {
	if !p.attention {
		p.asked++
	}
	p.attention = true
}
