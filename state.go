package main

import (
	"cmp"
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"gorex/internal/config"
	"gorex/internal/rex"
	"gorex/locales"
)

// Tab is a tab of the window: panes in a tree of splits.
type Tab struct {
	ID   int
	Name string // the name the user gave it, "" to follow its panes
	Root *Node
	// Focus is the pane with the focus, and Zoom the pane that fills the
	// tab, when one does.
	Focus *Pane
	Zoom  *Pane
	// recent lists the panes by when they had the focus last, the latest
	// last: closing a pane gives the focus back to the one before.
	recent []*Pane
}

// Node is a pane, or a split of two nodes.
type Node struct {
	ID     int
	Pane   *Pane
	Parent *Node
	// Vertical stacks A above B; otherwise A is left of B. Ratio is A's
	// share of the room.
	Vertical bool
	Ratio    float32
	A, B     *Node
}

// Pane is a terminal attached to a session of the server, or of a remote
// host's.
type Pane struct {
	ID  int
	SID string
	// Name is the label the user gave the pane, "" to follow its program.
	Name   string
	Tab    *Tab
	Node   *Node
	term   *terminal.Terminal
	stream *rex.Stream
	// host is the remote host the session lives on, nil for this machine;
	// remote is then the terminal's Conn.
	host   *hostConn
	remote *hostStream

	// info is what the server last said of the session.
	info rex.SessionInfo
	// title is the last title the program set.
	title string
	// lastData is when the session last printed, in Unix nanoseconds.
	lastData atomic.Int64
	// attention marks a pane that rang the bell or whose program finished
	// while it was out of sight; seen clears it.
	attention bool
	// seen is when the program's last record the user saw was updated:
	// done and error records till then no longer ask for attention.
	// notified is when the pane's last status notification was shown.
	seen, notified time.Time
	// bounds is where the pane's card was in the last frame.
	bounds ui.Rect
	closed bool
	// restored tells that the session was made anew for a pane of a
	// saved layout whose session was gone.
	restored bool
	startDir string
	// seenFunc tells the window's store which session status was seen.
	seenFunc func(sid string, at time.Time)
}

// App is the state of a window of GoRex's, which shows a workspace.
type App struct {
	win *mygo.Window
	// st is the app's store of workspaces, ws the one the window shows,
	// and slot the window's place among the windows. stop closes with the
	// window.
	st     *store
	ws     *Workspace
	slot   int
	stop   chan struct{}
	client *rex.Client
	hello  rex.Hello
	// keys matches key presses to actions; it runs before the terminal
	// sees a key a binding takes. luaConfig owns the custom actions the
	// keymap and palette use.
	keys      *keyDispatcher
	luaConfig *config.Config
	custom    []*command
	err       string
	// errMsg is a message of the catalog, said when drawn, which wins
	// over err, a message of the server.
	errMsg func(locales.Messages) string

	tabs   []*Tab
	active int
	nextID int
	// hosts are the remote hosts saved, in their order.
	hosts []*hostConn

	// focusReq asks the view to give the keyboard focus to a pane's
	// terminal.
	focusReq *Pane

	// The command palette, the host's popover and the tab being renamed.
	paletteOpen   bool
	paletteQuery  string
	paletteSel    int
	hostOpen      bool
	renaming      *Tab
	renameText    string
	renameFocus   bool
	paneRenaming  *Pane
	paneNameText  string
	paneNameFocus bool
	// findPane is the pane whose find bar shows, findText its query, and
	// findStatus how its search goes; lastFind reopens the bar.
	findPane   *Pane
	findText   string
	lastFind   string
	findStatus terminal.SearchStatus
	findFocus  bool
	saveDue    bool
	lastSave   time.Time
	quitting   bool
	focusedWin bool
	title      string

	// The workspace's popover, the field that renames it, Close
	// Session's question, and the palette listing the workspaces.
	wsOpen          bool
	wsRenaming      bool
	wsRenameText    string
	wsRenameFocus   bool
	wsConfirm       bool
	paletteSessions bool

	// The sidebar: the tab last scrolled into view, and a drag of its
	// edge, from the width it began at.
	revealed  *Tab
	edgeDrag  bool
	edgeFrom  float32
	edgeMoved float32

	// posted are changes to make in the next frame, from terminals of a
	// view without a window, as in tests.
	postMu sync.Mutex
	posted []func()
	// services keeps redraw requests available to menu callbacks.
	services ui.Services
}

func (a *App) post(fn func()) {
	a.postMu.Lock()
	a.posted = append(a.posted, fn)
	a.postMu.Unlock()
}

// later changes the tree of tabs and panes once the frame building now
// is built, as closing what it is building would pull it from under it.
func (a *App) later(c *ui.Context, fn func()) {
	a.post(fn)
	c.Invalidate()
}

// runPosted makes the changes posted since the last frame.
func (a *App) runPosted() {
	a.postMu.Lock()
	fns := a.posted
	a.posted = nil
	a.postMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func (a *App) id() int {
	a.nextID++
	return a.nextID
}

func (a *App) tab() *Tab {
	if a.active >= 0 && a.active < len(a.tabs) {
		return a.tabs[a.active]
	}
	return nil
}

// panes returns the panes of a node in order, left to right and top to
// bottom.
func (n *Node) panes() []*Pane {
	if n == nil {
		return nil
	}
	if n.Pane != nil {
		return []*Pane{n.Pane}
	}
	return append(n.A.panes(), n.B.panes()...)
}

func (t *Tab) panes() []*Pane { return t.Root.panes() }

// setFocus gives a pane the focus of its tab.
func (t *Tab) setFocus(p *Pane) {
	if p == nil {
		return
	}
	t.Focus = p
	p.see()
	t.recent = slices.DeleteFunc(t.recent, func(q *Pane) bool { return q == p })
	t.recent = append(t.recent, p)
}

// clientOf returns the client of a host's server, this machine's for
// nil; nil while the host is not connected.
func (a *App) clientOf(h *hostConn) *rex.Client {
	if h == nil {
		return a.client
	}
	return h.current()
}

// killOn ends a session of a host, or of this machine for nil.
func (a *App) killOn(h *hostConn, sid string) {
	switch {
	case sid == "":
	case h != nil:
		h.kill(sid)
	case a.client != nil:
		c := a.client
		go c.Kill(sid)
	}
}

// offline tells, on the error banner, that a host is not connected, and
// reports whether it is not.
func (a *App) offline(h *hostConn) bool {
	if h == nil || h.current() != nil {
		return false
	}
	name := h.Name
	a.err, a.errMsg = "", func(m locales.Messages) string { return m.HostsNotConnectedTo(name) }
	return true
}

// newPane makes a pane with a new session in dir, on host h.
func (a *App) newPane(h *hostConn, dir string, cols, rows int) *Pane {
	if cols <= 0 {
		cols, rows = 80, 24
	}
	var info rex.SessionInfo
	id := a.id()
	env := []string{"GOREX_PANE=" + strconv.Itoa(id)}
	if h != nil {
		env = append(env, "GOREX_HOST="+h.Name)
	} else if a.hello.Host.Name != "" {
		env = append(env, "GOREX_CLIENT="+a.hello.Host.Name)
	}
	err := errors.New("rex: connection to the server lost")
	if c := a.clientOf(h); c != nil {
		info, err = c.Create(rex.CreateOptions{Dir: dir, Cols: cols, Rows: rows, Env: env})
	}
	if err != nil {
		log.Printf("creating a session: %v", err)
		a.err, a.errMsg = err.Error(), nil
		info = rex.SessionInfo{ID: "", Exited: true}
	}
	p := &Pane{ID: id, SID: info.ID, info: info, startDir: dir, host: h}
	if p.info.Dir == "" {
		p.info.Dir = dir
	}
	a.attach(p, cols, rows)
	return p
}

// attach makes the pane's terminal, attached to its session.
func (a *App) attach(p *Pane, cols, rows int) {
	if p.SID == "" {
		opts := termOptions()
		opts.Conn = nopConn{}
		term, err := terminal.New(opts)
		if err == nil {
			term.Feed([]byte("\x1b[31m" + locales.ErrorSessionStart(a.err) + "\x1b[0m\r\n"))
			p.term = term
		}
		return
	}
	// The terminal's callbacks change the state on the main thread, in
	// the window that made the pane, as long as it is open.
	win := a.win
	update := func(fn func()) {
		if win != nil {
			win.Update(fn)
		} else {
			a.post(fn)
		}
	}
	onData := func(int) { p.lastData.Store(time.Now().UnixNano()) }
	if a.st != nil {
		p.seenFunc = func(sid string, at time.Time) { a.st.recordSeen(sid, at) }
	}
	opts := termOptions()
	if p.host != nil {
		p.remote = newHostStream(p.host, p.SID, cols, rows)
		p.remote.OnData = onData
		p.remote.onChange = func() { update(func() {}) }
		opts.Conn = p.remote
	} else {
		p.stream = a.client.Stream(p.SID, cols, rows)
		p.stream.OnData = onData
		opts.Conn = p.stream
	}
	// A paste offers the bytes the clipboard holds here, an image's among
	// them: a program that reads the clipboard itself (Kitty's paste
	// events) takes them even over SSH or on a host, where a path of this
	// machine means nothing. The terminal then reads every format the
	// clipboard holds, as it does in a pane of this machine. Who types last
	// takes the session's size back from a device that holds it (SizeLock):
	// the session gets the pane's size again.
	opts.OnPaste = func(d transfer.Data) bool {
		if p.host == nil {
			a.takeSize(p)
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
		if p.host == nil && mods&ui.Super == 0 {
			a.takeSize(p)
		}
		return a.dispatchKey(mods, key)
	}
	opts.OnTitle = func(title string) {
		update(func() { p.title = title })
	}
	opts.OnBell = func() {
		update(func() { a.ring(p) })
	}
	opts.OnNotify = func(title, body string) {
		if title == "" {
			title = "GoRex"
		}
		mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body}).Show()
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
	opts.OnExit = func(int) {
		update(func() {
			if p.closed || a.quitting {
				return
			}
			// The session of a host ended, or is gone: the window keeps a
			// tab of this machine's rather than closing with its last.
			if p.host != nil && len(a.tabs) == 1 && len(a.tabs[0].panes()) == 1 {
				home, _ := os.UserHomeDir()
				a.newTabOn(nil, home)
			}
			// A session the host no longer lists, as one it stopped sharing,
			// is not this machine's to end.
			a.dropPane(p, p.remote == nil || !p.remote.Gone())
		})
	}
	term, err := terminal.New(opts)
	if err != nil {
		log.Printf("terminal: %v", err)
		a.err, a.errMsg = err.Error(), nil
		return
	}
	// Until a view shows it, the terminal has the session's size, so that
	// attaching changes nothing in the session.
	term.Resize(cols, rows)
	p.term = term
}

// takeSize takes the size of the pane's session back from the device that
// holds it, at the pane's size. The terminal's OnKey and OnPaste call it on
// the main thread, which apply sets p.info on.
func (a *App) takeSize(p *Pane) {
	if p.info.SizeLock == "" || p.term == nil {
		return
	}
	p.info.SizeLock = ""
	cols, rows := p.term.Size()
	client, sid := a.client, p.SID
	go func() {
		if err := client.UnlockSize(sid, "", cols, rows); err != nil {
			log.Printf("taking the size back: %v", err)
		}
	}()
}

// nopConn is the Conn of a terminal that only shows what the app feeds it.
type nopConn struct{}

func (nopConn) Read([]byte) (int, error)    { select {} }
func (nopConn) Write(p []byte) (int, error) { return len(p), nil }
func (nopConn) Close() error                { return nil }

func (a *App) ring(p *Pane) {
	if t := a.tab(); t == nil || t.Focus != p || p.Tab != t || !a.focusedWin {
		p.attention = true
	}
}

// newTab opens a tab with a shell in dir, after the active tab, on the
// active tab's host.
func (a *App) newTab(dir string) *Tab { return a.newTabOn(a.currentHost(), dir) }

// newTabOn opens a tab with a shell in dir on host h, nil for this
// machine, after the active tab (newTabAfter), and shows it.
func (a *App) newTabOn(h *hostConn, dir string) *Tab {
	t := a.newTabAfter(h, dir, a.tab())
	if t != nil {
		a.active = slices.Index(a.tabs, t)
		a.focusReq = t.Focus
	}
	return t
}

// newTabAfter opens a tab with a shell in dir on host h: after tab after,
// or last of the host's tabs when after is another host's. It opens none
// while the host is not connected. The active tab stays.
func (a *App) newTabAfter(h *hostConn, dir string, after *Tab) *Tab {
	if a.offline(h) {
		return nil
	}
	t := &Tab{ID: a.id()}
	p := a.newPane(h, dir, 0, 0)
	t.Root = &Node{ID: a.id(), Pane: p}
	p.Tab, p.Node = t, t.Root
	t.setFocus(p)
	at := len(a.tabs)
	if i := slices.Index(a.tabs, after); i >= 0 && after.host() == h {
		at = i + 1
	}
	cur := a.tab()
	a.tabs = slices.Insert(a.tabs, at, t)
	if cur != nil {
		a.active = slices.Index(a.tabs, cur)
	}
	a.groupTabs()
	a.changed()
	return t
}

// currentHost is the host of the focused pane, nil for this machine.
func (a *App) currentHost() *hostConn {
	if t := a.tab(); t != nil && t.Focus != nil {
		return t.Focus.host
	}
	return nil
}

// currentDir returns the directory new panes open in: the focused pane's,
// else the home directory, which a host's server picks itself for "".
func (a *App) currentDir() string {
	if t := a.tab(); t != nil && t.Focus != nil {
		if d := t.Focus.info.Dir; d != "" {
			return d
		}
		if t.Focus.host != nil {
			return ""
		}
	}
	home, _ := os.UserHomeDir()
	return home
}

// split splits the focused pane, the new pane right of it or below it.
func (a *App) split(vertical bool) { a.splitAt(vertical, "") }

// splitAt splits the focused pane in dir, the current directory when "".
func (a *App) splitAt(vertical bool, dir string) {
	t := a.tab()
	if t == nil || t.Focus == nil {
		return
	}
	if dir == "" {
		dir = a.currentDir()
	}
	if p := a.splitPane(t.Focus, vertical, dir); p != nil {
		a.focusReq = p
	}
}

// splitPane splits pane old, the new pane, in dir, right of it or below
// it, with the focus of its tab; none while its host is not connected.
func (a *App) splitPane(old *Pane, vertical bool, dir string) *Pane {
	if a.offline(old.host) {
		return nil
	}
	t := old.Tab
	t.Zoom = nil
	n := old.Node
	cols, rows := 80, 24
	if old.term != nil {
		cols, rows = old.term.Size()
		if vertical {
			rows = max(rows/2, 2)
		} else {
			cols = max(cols/2, 10)
		}
	}
	p := a.newPane(old.host, dir, cols, rows)
	p.Tab = t
	left := &Node{ID: a.id(), Pane: old, Parent: n}
	right := &Node{ID: a.id(), Pane: p, Parent: n}
	old.Node, p.Node = left, right
	n.Pane, n.A, n.B, n.Vertical, n.Ratio = nil, left, right, vertical, 0.5
	t.setFocus(p)
	a.changed()
	return p
}

// closePane closes a pane and ends its session.
func (a *App) closePane(p *Pane) { a.dropPane(p, true) }

// dropPane closes a pane, and ends its session when kill is set.
func (a *App) dropPane(p *Pane, kill bool) {
	if p.closed {
		return
	}
	p.closed = true
	if a.findPane == p {
		a.closeFind()
	}
	if p.term != nil {
		p.term.Close()
	}
	if kill {
		a.killOn(p.host, p.SID)
	}
	t := p.Tab
	if t.Zoom == p {
		t.Zoom = nil
	}
	t.recent = slices.DeleteFunc(t.recent, func(q *Pane) bool { return q == p })
	n := p.Node
	if n.Parent == nil {
		a.removeTab(t)
		return
	}
	parent := n.Parent
	other := parent.A
	if other == n {
		other = parent.B
	}
	// The other side takes the parent's place.
	parent.Pane, parent.A, parent.B, parent.Vertical, parent.Ratio = other.Pane, other.A, other.B, other.Vertical, other.Ratio
	if parent.Pane != nil {
		parent.Pane.Node = parent
	} else {
		parent.A.Parent, parent.B.Parent = parent, parent
	}
	if t.Focus == p {
		next := (*Pane)(nil)
		if k := len(t.recent); k > 0 {
			next = t.recent[k-1]
		} else if ps := t.panes(); len(ps) > 0 {
			next = ps[0]
		}
		t.Focus = nil
		t.setFocus(next)
	}
	if a.tab() == t {
		a.focusReq = t.Focus
	}
	a.changed()
}

func (a *App) removeTab(t *Tab) {
	i := slices.Index(a.tabs, t)
	if i < 0 {
		return
	}
	a.tabs = slices.Delete(a.tabs, i, i+1)
	if a.active > i || a.active >= len(a.tabs) {
		a.active = max(a.active-1, 0)
	}
	if a.renaming == t {
		a.renaming = nil
	}
	if len(a.tabs) == 0 {
		a.changed()
		if a.emptied() {
			return
		}
		a.save()
		if a.win != nil {
			a.win.Close()
		}
		return
	}
	if nt := a.tab(); nt != nil {
		a.focusReq = nt.Focus
	}
	a.changed()
}

// closeTab closes a tab and ends the sessions of its panes.
func (a *App) closeTab(t *Tab) {
	for _, p := range t.panes() {
		p.closed = true
		if p.term != nil {
			p.term.Close()
		}
		a.killOn(p.host, p.SID)
	}
	a.removeTab(t)
}

func (a *App) selectTab(i int) {
	if i < 0 || i >= len(a.tabs) {
		return
	}
	a.active = i
	t := a.tabs[i]
	if t.Focus != nil {
		t.Focus.see()
	}
	a.focusReq = t.Focus
	a.changed()
}

// moveFocus gives the focus to the pane next to the focused one, toward
// dx, dy.
func (a *App) moveFocus(dx, dy int) {
	t := a.tab()
	if t == nil || t.Focus == nil || t.Zoom != nil {
		return
	}
	from := t.Focus.bounds
	cx, cy := from.X+from.W/2, from.Y+from.H/2
	var best *Pane
	bestScore := float32(1e9)
	for _, p := range t.panes() {
		if p == t.Focus {
			continue
		}
		r := p.bounds
		var dist, overlap float32
		switch {
		case dx > 0:
			dist = r.X - (from.X + from.W)
			overlap = min(r.Y+r.H, from.Y+from.H) - max(r.Y, from.Y)
		case dx < 0:
			dist = from.X - (r.X + r.W)
			overlap = min(r.Y+r.H, from.Y+from.H) - max(r.Y, from.Y)
		case dy > 0:
			dist = r.Y - (from.Y + from.H)
			overlap = min(r.X+r.W, from.X+from.W) - max(r.X, from.X)
		default:
			dist = from.Y - (r.Y + r.H)
			overlap = min(r.X+r.W, from.X+from.W) - max(r.X, from.X)
		}
		if dist < -4 || overlap <= 0 {
			continue
		}
		// The nearest, then the one most in line with the focused pane's
		// center.
		px, py := r.X+r.W/2, r.Y+r.H/2
		off := px - cx
		if dx != 0 {
			off = py - cy
		}
		score := dist*4 + abs(off)
		if score < bestScore {
			best, bestScore = p, score
		}
	}
	if best != nil {
		t.setFocus(best)
		a.focusReq = best
	}
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// resizeFocused moves the nearest divider of the focused pane on the
// axis of dx, dy.
func (a *App) resizeFocused(dx, dy int) { a.resizeFocusedBy(dx, dy, 5) }

// resizeFocusedBy moves the nearest matching divider by amount percent.
func (a *App) resizeFocusedBy(dx, dy int, amount float32) {
	t := a.tab()
	if t == nil || t.Focus == nil {
		return
	}
	if amount <= 0 || float32IsBad(amount) {
		amount = 5
	}
	vertical := dy != 0
	for n := t.Focus.Node; n.Parent != nil; n = n.Parent {
		p := n.Parent
		if p.Vertical != vertical {
			continue
		}
		step := amount / 100
		if dx < 0 || dy < 0 {
			step = -step
		}
		p.Ratio = min(max(p.Ratio+step, 0.1), 0.9)
		a.changed()
		return
	}
}

// setSplitRatio sets the nearest split of the focused pane.
func (a *App) setSplitRatio(ratio float32) {
	t := a.tab()
	if t == nil || t.Focus == nil || t.Focus.Node.Parent == nil || float32IsBad(ratio) {
		return
	}
	t.Focus.Node.Parent.Ratio = min(max(ratio, 0.1), 0.9)
	a.changed()
}

// resizePane moves the divider on p's direction edge by amount, a share
// of the split as herdr's pane.resize has it. It reports whether the
// split moved.
func (a *App) resizePane(p *Pane, direction string, amount float32) (bool, string) {
	if p == nil || p.Node == nil {
		return false, "unchanged"
	}
	vertical := direction == "up" || direction == "down"
	if direction != "left" && direction != "right" && !vertical {
		return false, "unchanged"
	}
	amount = abs(amount)
	if amount == 0 || float32IsBad(amount) {
		amount = 0.05
	}
	amount = min(amount, 0.5)
	if direction == "left" || direction == "up" {
		amount = -amount
	}
	// The split a pane's direction edge belongs to is the one that has
	// the pane on that side: A's right edge, or B's left edge. Walk out
	// to it, keeping the nearest same-axis split for an outer edge, which
	// moves that one as herdr does.
	var nearest *Node
	grows := direction == "right" || direction == "down"
	for n := p.Node; n.Parent != nil; n = n.Parent {
		split := n.Parent
		if split.Vertical != vertical {
			continue
		}
		if nearest == nil {
			nearest = split
		}
		if (split.A == n) == grows {
			return a.shiftSplit(split, amount)
		}
	}
	if nearest != nil {
		return a.shiftSplit(nearest, amount)
	}
	return false, "unchanged"
}

// shiftSplit moves split's ratio by amount, within the bounds the
// desktop's own divider drags use.
func (a *App) shiftSplit(split *Node, amount float32) (bool, string) {
	old := split.Ratio
	split.Ratio = min(max(old+amount, 0.1), 0.9)
	if split.Ratio == old {
		return false, "unchanged"
	}
	a.changed()
	return true, ""
}

// swapPanes puts p and q in each other's places, in their tab, and gives
// the focus to p as herdr's pane.swap does.
func (a *App) swapPanes(p, q *Pane) {
	if p == nil || q == nil || p == q || p.Tab == nil || p.Tab != q.Tab || p.Node == nil || q.Node == nil {
		return
	}
	pn, qn := p.Node, q.Node
	pn.Pane, qn.Pane = q, p
	p.Node, q.Node = qn, pn
	if p.Tab.Zoom == q {
		p.Tab.Zoom = p
	}
	p.Tab.setFocus(p)
	a.changed()
}

// equalize gives every split of the tab equal shares, by the panes on
// either side.
func (a *App) equalize() {
	t := a.tab()
	if t == nil {
		return
	}
	var walk func(n *Node) (cols, rows int)
	walk = func(n *Node) (int, int) {
		if n.Pane != nil {
			return 1, 1
		}
		ac, ar := walk(n.A)
		bc, br := walk(n.B)
		if n.Vertical {
			n.Ratio = float32(ar) / float32(ar+br)
			return max(ac, bc), ar + br
		}
		n.Ratio = float32(ac) / float32(ac+bc)
		return ac + bc, max(ar, br)
	}
	walk(t.Root)
	a.changed()
}

func (a *App) toggleZoom() { _ = a.zoomFocused("toggle") }

// zoomFocused makes the focused pane fill its tab, mode "on"; gives its
// room back, "off"; or toggles, "" or "toggle".
func (a *App) zoomFocused(mode string) error {
	t := a.tab()
	if t == nil || t.Focus == nil {
		return nil
	}
	on := t.Zoom == nil
	switch mode {
	case "on":
		on = true
	case "off":
		on = false
	}
	a.zoom(t.Focus, on)
	a.focusReq = t.Focus
	return nil
}

// zoom makes pane p fill its tab, with its focus, when the tab has
// another, or gives every pane its room back.
func (a *App) zoom(p *Pane, on bool) {
	t := p.Tab
	t.Zoom = nil
	if on && len(t.panes()) > 1 {
		t.setFocus(p)
		t.Zoom = p
	}
	a.changed()
}

// label returns how a pane names itself: its program, or the title the
// program set, and the directory it works in.
func (p *Pane) label() (name, detail string) {
	in := p.info
	if p.Name != "" {
		return p.Name, shortDir(in.Dir)
	}
	prog := programOf(in.Program)
	dir := shortDir(in.Dir)
	if in.Program == "" {
		if in.Shell == "" {
			return "Shell", shortDir(p.startDir)
		}
		return in.Shell, shortDir(p.startDir)
	}
	if in.Idle || prog.Shell {
		name := in.Program
		if name == "" {
			name = in.Shell
		}
		return name, dir
	}
	if t := strings.TrimSpace(p.title); t != "" && !titleOfShell(t, in) {
		return t, ""
	}
	name = prog.Name
	if name == "ssh" || in.Program == "ssh" {
		if host := sshHost(in.Args); host != "" {
			return "SSH", host
		}
	}
	return name, dir
}

// titleOfShell tells a title a shell set, naming the program or the
// directory, from one a program set of its own.
func titleOfShell(title string, in rex.SessionInfo) bool {
	l := strings.ToLower(title)
	for _, w := range []string{strings.ToLower(in.Program), strings.ToLower(in.Shell)} {
		// Windows's consoles title themselves with the shell's file:
		// "C:\WINDOWS\system32\cmd.exe - ping".
		if w != "" && (strings.HasPrefix(l, w) || strings.Contains(l, `\`+w+".exe")) {
			return true
		}
	}
	if home, _ := os.UserHomeDir(); strings.Contains(title, "~") || (home != "" && strings.Contains(title, home)) {
		return true
	}
	return strings.Contains(title, "@") && strings.Contains(title, ":")
}

func sshHost(args []string) string {
	for i := 1; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			if len(a) == 2 && strings.Contains("bcDEeFIiJLlmOopQRSWw", a[1:]) {
				i++
			}
			continue
		}
		return a
	}
	return ""
}

// shortDir shows a directory as shells do, from ~, and only its last
// parts when it is deep.
func shortDir(d string) string {
	if d == "" {
		return ""
	}
	sep := string(filepath.Separator)
	if home, _ := os.UserHomeDir(); home != "" {
		if samePath(d, home) {
			return "~"
		}
		if len(d) > len(home) && samePath(d[:len(home)], home) && d[len(home)] == filepath.Separator {
			d = "~" + d[len(home):]
		}
	}
	parts := strings.Split(d, sep)
	if len(parts) > 5 || len(d) > 48 && len(parts) > 3 {
		return "…" + sep + strings.Join(parts[len(parts)-2:], sep)
	}
	return d
}

// samePath reports whether two paths are the same, without case on
// Windows.
func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// tabLabel names a tab: its name, or its focused pane's.
func (t *Tab) label() (name, detail string) {
	if t.Name != "" {
		return t.Name, ""
	}
	if t.Focus == nil {
		return "Shell", ""
	}
	return t.Focus.label()
}

// The saved layout: the tabs, their splits and the sessions of their
// panes, which the server keeps, and the app restores when it starts.
type savedLayout struct {
	Active int        `json:"active"`
	Tabs   []savedTab `json:"tabs"`
}

type savedTab struct {
	Name  string     `json:"name,omitempty"`
	Root  *savedNode `json:"root"`
	Focus int        `json:"focus"`
	Zoom  bool       `json:"zoom,omitempty"`
}

type savedNode struct {
	SID  string `json:"sid,omitempty"`
	Name string `json:"name,omitempty"`
	// Host is the id of the remote host of the session, "" for this
	// machine's.
	Host     string     `json:"host,omitempty"`
	Dir      string     `json:"dir,omitempty"`
	Cols     int        `json:"cols,omitempty"`
	Rows     int        `json:"rows,omitempty"`
	Vertical bool       `json:"vertical,omitempty"`
	Ratio    float32    `json:"ratio,omitempty"`
	A        *savedNode `json:"a,omitempty"`
	B        *savedNode `json:"b,omitempty"`
}

func (a *App) snapshot() savedLayout {
	l := savedLayout{Active: a.active, Tabs: []savedTab{}}
	for _, t := range a.tabs {
		l.Tabs = append(l.Tabs, a.savedTab(t))
	}
	return l
}

// savedTab is a tab as the layout keeps it.
func (a *App) savedTab(t *Tab) savedTab {
	var save func(n *Node) *savedNode
	save = func(n *Node) *savedNode {
		if p := n.Pane; p != nil {
			s := &savedNode{SID: p.SID, Name: p.Name, Dir: p.info.Dir}
			if p.host != nil {
				s.Host = p.host.ID
			}
			if s.Dir == "" {
				s.Dir = p.startDir
			}
			if p.term != nil {
				s.Cols, s.Rows = p.term.Size()
			}
			return s
		}
		return &savedNode{Vertical: n.Vertical, Ratio: n.Ratio, A: save(n.A), B: save(n.B)}
	}
	st := savedTab{Name: t.Name, Root: save(t.Root), Zoom: t.Zoom != nil}
	st.Focus = max(slices.Index(t.panes(), t.Focus), 0)
	return st
}

// changed notes that the layout changed, to save it soon.
func (a *App) changed() { a.saveDue = true }

// save sends the layout of every workspace to the server, which keeps it
// on disk.
func (a *App) save() {
	a.saveDue = false
	a.lastSave = time.Now()
	if a.st != nil {
		a.st.save()
	}
}

// restore opens the tabs of a saved layout, attaching to the sessions
// the server still has and starting new ones in place of those it lost.
// With others, sessions of this machine that neither the layout nor
// others name get tabs too, rather than running unseen. It reports
// whether it opened a tab.
func (a *App) restore(l savedLayout, others map[string]bool) bool {
	if a.client == nil || len(l.Tabs) == 0 && others == nil {
		return false
	}
	live := map[string]rex.SessionInfo{}
	if infos, err := a.client.List(); err == nil {
		for _, in := range infos {
			if !in.Exited {
				live[in.ID] = in
			}
		}
	}
	used := map[string]bool{}
	// A pane of a host attaches once the host connects, and closes then
	// if its session is gone; one of a host no longer saved goes now.
	var load func(s *savedNode, t *Tab, parent *Node) *Node
	load = func(s *savedNode, t *Tab, parent *Node) *Node {
		n := &Node{ID: a.id(), Parent: parent}
		if s.A == nil || s.B == nil {
			var p *Pane
			if s.Host != "" {
				h := a.hostByID(s.Host)
				if h == nil || s.SID == "" {
					return nil
				}
				p = &Pane{ID: a.id(), SID: s.SID, Name: s.Name, host: h, info: rex.SessionInfo{ID: s.SID, Dir: s.Dir}, startDir: s.Dir}
				a.attach(p, s.Cols, s.Rows)
			} else if in, ok := live[s.SID]; ok && !used[s.SID] {
				used[s.SID] = true
				p = &Pane{ID: a.id(), SID: s.SID, Name: s.Name, info: in, startDir: s.Dir}
				a.attach(p, in.Cols, in.Rows)
			} else {
				dir := s.Dir
				if st, err := os.Stat(dir); err != nil || !st.IsDir() {
					dir, _ = os.UserHomeDir()
				}
				p = a.newPane(nil, dir, s.Cols, s.Rows)
				p.Name, p.restored = s.Name, true
			}
			p.Tab, p.Node = t, n
			n.Pane = p
			return n
		}
		n.Vertical, n.Ratio = s.Vertical, min(max(s.Ratio, 0.05), 0.95)
		n.A, n.B = load(s.A, t, n), load(s.B, t, n)
		// The side left takes the place of a split whose other side went.
		switch {
		case n.A == nil && n.B == nil:
			return nil
		case n.A == nil:
			n.B.Parent = parent
			return n.B
		case n.B == nil:
			n.A.Parent = parent
			return n.A
		}
		return n
	}
	for _, st := range l.Tabs {
		if st.Root == nil {
			continue
		}
		t := &Tab{ID: a.id(), Name: st.Name}
		if t.Root = load(st.Root, t, nil); t.Root == nil {
			continue
		}
		ps := t.panes()
		for _, p := range ps {
			t.setFocus(p)
		}
		t.setFocus(ps[min(max(st.Focus, 0), len(ps)-1)])
		if st.Zoom && len(ps) > 1 {
			t.Zoom = t.Focus
		}
		a.tabs = append(a.tabs, t)
	}
	// Sessions no tab shows, as those of an app that crashed, get a tab
	// of their own.
	for _, in := range sortedInfos(live) {
		if others != nil && !used[in.ID] && !others[in.ID] {
			a.tabs = append(a.tabs, a.sessionTab(nil, in))
		}
	}
	if len(a.tabs) == 0 {
		return false
	}
	a.active = min(max(l.Active, 0), len(a.tabs)-1)
	a.groupTabs()
	a.focusReq = a.tab().Focus
	return true
}

// sessionTab makes a tab of one pane, attached to session in of host h,
// nil for this machine, at the session's size.
func (a *App) sessionTab(h *hostConn, in rex.SessionInfo) *Tab {
	t := &Tab{ID: a.id()}
	p := &Pane{ID: a.id(), SID: in.ID, info: in, startDir: in.Dir, host: h}
	a.attach(p, in.Cols, in.Rows)
	t.Root = &Node{ID: a.id(), Pane: p}
	p.Tab, p.Node = t, t.Root
	t.setFocus(p)
	return t
}

func sortedInfos(m map[string]rex.SessionInfo) []rex.SessionInfo {
	out := make([]rex.SessionInfo, 0, len(m))
	for _, in := range m {
		out = append(out, in)
	}
	slices.SortFunc(out, func(a, b rex.SessionInfo) int { return a.Created.Compare(b.Created) })
	return out
}

// poll asks the server what runs in each session, every half second,
// until the window closes. Every half minute, less often, it asks after
// the network this machine joined, if any, whose members become hosts.
func (a *App) poll(win *mygo.Window, client *rex.Client) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	rounds := 0
	for {
		select {
		case <-a.stop:
			return
		case <-client.Closed():
			win.Update(func() {
				// Not once the window took a new connection.
				if !a.quitting && a.client == client {
					a.errMsg = locales.Messages.ErrorConnectionLost
				}
			})
			return
		case <-tick.C:
		}
		rounds++
		if rounds%30 == 1 {
			if st, err := client.RemoteStatus(); err == nil && st.Network != nil {
				win.Update(func() {
					if a.win == win {
						a.reconcileNetwork(st.Network)
					}
				})
			}
		}
		infos, err := client.List()
		if err != nil {
			continue
		}
		byID := make(map[string]rex.SessionInfo, len(infos))
		for _, in := range infos {
			byID[in.ID] = in
		}
		win.Update(func() {
			if a.win == win {
				if a.st != nil {
					a.st.infos = byID
				}
				a.apply(nil, byID)
			}
		})
	}
}

// apply takes what the server of host h, nil for this machine's, said of
// its sessions.
func (a *App) apply(h *hostConn, byID map[string]rex.SessionInfo) {
	if h != nil {
		a.adopt(h, byID)
	}
	for ti, t := range a.tabs {
		for _, p := range t.panes() {
			in, ok := byID[p.SID]
			if !ok || p.host != h {
				continue
			}
			was := p.info
			p.info = in
			// While another device holds the session's size, the pane's
			// resizes are ignored, and the session keeps that size; once
			// it lets go, the pane gives the session its own.
			if was.SizeLock != "" && in.SizeLock == "" && p.stream != nil {
				go func(st *rex.Stream) {
					if err := st.ResendSize(); err != nil {
						log.Printf("resizing a session given back: %v", err)
					}
				}(p.stream)
			}
			seen := ti == a.active && t.Focus == p && a.focusedWin
			if seen {
				p.see()
			} else if r := entered(was, in); r != nil && time.Since(p.notified) > 10*time.Second {
				p.notified = time.Now()
				a.notifyStatus(h, t, p, r)
			}
			// A program that ran a while and finished out of sight
			// asks for attention. One that reports its status was
			// notified of it.
			if !was.Idle && was.Program != "" && in.Idle && was.Program != in.Program {
				if !seen {
					p.attention = true
				}
				statuses := len(was.Statuses) > 0 || len(in.Statuses) > 0
				if !a.focusedWin && time.Since(was.LastInput) > 8*time.Second && !statuses {
					prog := programOf(was.Program)
					n := mygo.NewNotification(mygo.NotificationOptions{
						Title: locales.NotifyFinished(prog.Name),
						Body:  locales.NotifyIn(shortDir(in.Dir)),
					})
					n.Show()
				}
			}
			if was.Dir != in.Dir {
				a.changed()
			}
		}
	}
	if a.saveDue && time.Since(a.lastSave) > time.Second {
		a.save()
	}
}

// see marks what the pane shows as seen: its attention, and the done and
// error records of its program.
func (p *Pane) see() {
	p.attention = false
	for _, r := range p.info.Statuses {
		if r.Updated.After(p.seen) {
			p.seen = r.Updated
		}
	}
	if p.seenFunc != nil {
		p.seenFunc(p.SID, p.seen)
	}
}

// status is the record of the pane's program that most needs the user,
// nil for none.
func (p *Pane) status() *rex.ProgramStatus { return rex.MostUrgent(p.info.Statuses, p.seen) }

// entered returns the most urgent record that went blocked, done or error
// between two infos of a session, nil for none. An info of a pane not
// polled yet has no output: its records are not news.
func entered(was, in rex.SessionInfo) *rex.ProgramStatus {
	if was.LastOutput.IsZero() {
		return nil
	}
	var news []rex.ProgramStatus
	for _, r := range in.Statuses {
		if rex.StatusRank(r.State) < rex.StatusRank("done") {
			continue
		}
		i := slices.IndexFunc(was.Statuses, func(o rex.ProgramStatus) bool { return o.ID == r.ID })
		if i < 0 || was.Statuses[i].State != r.State {
			news = append(news, r)
		}
	}
	return rex.MostUrgent(news, time.Time{})
}

// notifyStatus tells, with a notification, that a program of pane p, on
// host h, reported r.
func (a *App) notifyStatus(h *hostConn, t *Tab, p *Pane, r *rex.ProgramStatus) {
	name, _ := p.label()
	prog := r.App
	if prog == "" {
		prog = name
	}
	title := locales.NotifyFinished(prog)
	switch r.State {
	case "blocked":
		title = locales.NotifyBlocked(prog)
	case "error":
		title = locales.NotifyFailed(prog)
	}
	src := name
	if tn, _ := t.label(); tn != name {
		src = tn + " · " + name
	}
	if h != nil {
		src = locales.NotifyOnHost(src, a.hostName(h))
	}
	body := src
	if msg := cmp.Or(r.Msg, r.Title); msg != "" {
		body = msg + "\n" + src
	}
	mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body}).Show()
}

// laterService queues a menu action without keeping a build context.
func (a *App) laterService(fn func()) { a.post(fn); a.services.Invalidate() }

// movePaneToTab moves pane p into tab t, beside the pane that has its
// focus. Its session keeps running and its term is not recreated.
func (a *App) movePaneToTab(p *Pane, t *Tab) {
	if p == nil || t == nil || p.Tab == nil || p.Tab == t {
		return
	}
	if t.Root != nil && p.host != t.host() {
		return
	}
	if t.Focus == nil || t.Root == nil {
		src, wasActive := p.Tab, a.tab() == p.Tab
		a.detachPane(p)
		p.Tab, p.Node = t, &Node{ID: a.id(), Pane: p}
		t.Root, t.Zoom = p.Node, nil
		t.setFocus(p)
		a.finishPaneMove(t, p, src, wasActive)
		a.changed()
		return
	}
	a.movePaneToPane(p, t.Focus)
}

// movePaneToPane moves pane p beside target, in target's tab. It works
// within a tab too, detaching p and splitting target's node.
func (a *App) movePaneToPane(p, target *Pane) { a.movePaneBeside(p, target, false, 0.5) }

// movePaneBeside moves pane p beside target in target's tab, in a split
// of the given orientation and ratio, herdr's pane.move; the session
// keeps running.
func (a *App) movePaneBeside(p, target *Pane, vertical bool, ratio float32) {
	if p == nil || target == nil || p == target || p.Tab == nil || p.host != target.host {
		return
	}
	t, src, wasActive := target.Tab, p.Tab, a.tab() == p.Tab
	a.detachPane(p)
	a.placePane(t, p, target, vertical, ratio)
	a.finishPaneMove(t, p, src, wasActive)
	a.changed()
}

// placePane puts p beside target in tab t, or alone when the tab has no
// pane; p is not in a tree.
func (a *App) placePane(t *Tab, p *Pane, target *Pane, vertical bool, ratio float32) {
	if t == nil || p == nil {
		return
	}
	if float32IsBad(ratio) || ratio <= 0 || ratio >= 1 {
		ratio = 0.5
	}
	ratio = min(max(ratio, 0.1), 0.9)
	if t.Root == nil {
		p.Tab, p.Node = t, &Node{ID: a.id(), Pane: p}
		t.Root, t.Zoom = p.Node, nil
		t.setFocus(p)
		return
	}
	if target == nil || target.Node == nil {
		return
	}
	old := target.Node
	sp := &Node{ID: a.id(), Parent: old.Parent, A: old, B: &Node{ID: a.id(), Pane: p}, Vertical: vertical, Ratio: ratio}
	sp.B.Parent = sp
	if old.Parent == nil {
		t.Root = sp
	} else if old.Parent.A == old {
		old.Parent.A = sp
	} else {
		old.Parent.B = sp
	}
	old.Parent = sp
	p.Tab, p.Node = t, sp.B
	t.Zoom = nil
	t.setFocus(p)
}

// movePaneToWindow moves p to target's active tab, as a session target
// attaches again to: its program is not restarted.
func (a *App) movePaneToWindow(p *Pane, target *App) {
	if p == nil || target == nil || target == a || p.Tab == nil {
		return
	}
	var h *hostConn
	if p.host != nil {
		h = target.hostByID(p.host.ID)
		if h == nil {
			return
		}
	}
	t := target.tab()
	if t == nil || t.Root != nil && t.host() != p.host {
		return
	}
	cols, rows := 80, 24
	if p.term != nil {
		cols, rows = p.term.Size()
	}
	src := p.Tab
	if a.findPane == p {
		a.closeFind()
	}
	a.detachPane(p)
	p.closed = true
	if p.term != nil {
		p.term.Close()
	}
	if a.tab() == src && src.Focus != nil {
		a.focusReq = src.Focus
	}
	if len(src.panes()) == 0 {
		a.removeTab(src)
	}
	np := &Pane{ID: target.id(), SID: p.SID, Name: p.Name, info: p.info, title: p.title, startDir: p.startDir, host: h}
	target.attach(np, cols, rows)
	target.placePane(t, np, t.Focus, false, 0.5)
	target.focusReq = np
	target.front()
	target.changed()
}

// movePaneToNewTab moves pane p into a tab of its own, after its tab,
// keeping its session and program.
func (a *App) movePaneToNewTab(p *Pane, label string) bool {
	if p == nil || p.Tab == nil {
		return false
	}
	src, cur := p.Tab, a.tab()
	wasActive := cur == src
	a.detachPane(p)
	t := &Tab{ID: a.id(), Name: paneName(label)}
	t.Root = &Node{ID: a.id(), Pane: p}
	p.Tab, p.Node = t, t.Root
	t.setFocus(p)
	at := len(a.tabs)
	if i := slices.Index(a.tabs, src); i >= 0 {
		at = i + 1
	}
	a.tabs = slices.Insert(a.tabs, at, t)
	if i := slices.Index(a.tabs, cur); cur != nil && i >= 0 {
		a.active = i
	}
	if len(src.panes()) == 0 {
		a.removeTab(src)
		if wasActive {
			a.active = max(slices.Index(a.tabs, t), 0)
		}
	}
	switch {
	case a.tab() == t:
		a.focusReq = t.Focus
	case wasActive && a.tab() == src && src.Focus != nil:
		a.focusReq = src.Focus
	}
	a.groupTabs()
	a.changed()
	return true
}

// finishPaneMove settles focus after p moved to t from src.
func (a *App) finishPaneMove(t *Tab, p *Pane, src *Tab, wasActive bool) {
	if src != t && len(src.panes()) == 0 {
		a.removeTab(src)
		if wasActive {
			if i := slices.Index(a.tabs, t); i >= 0 {
				a.selectTab(i)
			}
		}
	}
	switch {
	case a.tab() == t:
		a.focusReq = p
	case wasActive && a.tab() == src && src.Focus != nil:
		a.focusReq = src.Focus
	}
}

// paneName is the name a user gave a pane: trimmed, without control
// characters, and cut to 64 characters.
func paneName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > 64 {
		return string(r[:64])
	}
	return s
}

// detachPane removes p from its tab's tree, promoting its sibling, and
// leaves the tab empty when p was its only pane.
func (a *App) detachPane(p *Pane) {
	t, n := p.Tab, p.Node
	if t == nil || n == nil {
		return
	}
	if t.Zoom == p {
		t.Zoom = nil
	}
	t.recent = slices.DeleteFunc(t.recent, func(q *Pane) bool { return q == p })
	if n.Parent == nil {
		t.Root, t.Focus = nil, nil
		p.Tab, p.Node = nil, nil
		return
	}
	parent, other := n.Parent, n.Parent.A
	if other == n {
		other = n.Parent.B
	}
	parent.Pane, parent.Vertical, parent.Ratio = other.Pane, other.Vertical, other.Ratio
	parent.A, parent.B = other.A, other.B
	if parent.Pane != nil {
		parent.Pane.Node = parent
	} else {
		parent.A.Parent, parent.B.Parent = parent, parent
	}
	if t.Focus == p {
		t.Focus = nil
		if len(t.recent) > 0 {
			t.setFocus(t.recent[len(t.recent)-1])
		} else if ps := t.panes(); len(ps) > 0 {
			t.setFocus(ps[0])
		}
	}
	p.Tab, p.Node = nil, nil
}
