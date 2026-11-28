package main

import (
	"cmp"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
)

// Tab is a tab of the window: panes in a tree of splits.
type Tab struct {
	ID int
	// UID is the tab's lasting ID, layout.Tab.ID: makeTab gives one to
	// every tab made, reconcile keeps the saved one, and clients name the
	// tab by it. ID is only this window's handle for it.
	UID  string
	Name string // the name the user gave it, "" to follow its panes
	Root *Node
	// Focus is the pane with the focus, and Zoom the pane that fills the
	// tab, when one does.
	Focus *Pane
	Zoom  *Pane
	// Floats are the panes over the tab's tiled ones, the lowest first.
	Floats []*Float
	// hiddenLast is the float float.toggle hid last, to bring the same one
	// back.
	hiddenLast *Float
	// recent lists the panes by when they had the focus last, the latest
	// last: closing a pane gives the focus back to the one before.
	recent []*Pane
	// laid is the layout of the panes the window showed last, and settle
	// when the panes are done moving from the one before.
	laid   string
	settle time.Time
	// canvas makes the tab an infinite canvas (canvas.go): no tiled
	// panes, every pane a float on it; nil for a tab of tiled panes.
	canvas *layout.Canvas
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
// host's, or a browser that shows a page.
type Pane struct {
	ID  int
	SID string
	// Name is the label the user gave the pane, "" to follow its program.
	Name string
	Tab  *Tab
	Node *Node
	// float is the float the pane is, nil for a tiled pane.
	float *Float
	term  *terminal.Terminal
	// stream is the Conn of a pane of this machine, nil for a pane of a
	// host, whose is remote; it attaches again when its connection drops.
	stream *paneStream
	// ext is the pane's extension payload, nil for a terminal: a
	// browser, a note or a pane of an extension pack (docs/EXT-001).
	ext paneExt
	// host is the remote host the session lives on, nil for this machine;
	// remote is then the terminal's Conn.
	host   *hostConn
	remote *paneStream

	// info is what the server last said of the session.
	info rex.SessionInfo
	// title is the last title the program set.
	title string
	// attention marks a pane that rang the bell or whose program finished
	// while it was out of sight; seen clears it. asked counts the times it
	// came to ask for attention (askAttention), and popped is the last of
	// those its dots popped for, at popAt.
	attention     bool
	asked, popped uint64
	popAt         time.Time
	// seen is when the program's last done or error record the user saw
	// in this pane was updated: those till then, or till the server's
	// info.StatusSeen, which every client's panes raise, no longer ask
	// for attention. notified is when the pane's last status
	// notification was shown, and told when its program's own last one
	// was.
	seen, notified, told time.Time
	// bounds is where the pane's card rests in the layout, and termSize
	// the size of its terminal when the pane was last at rest.
	bounds   ui.Rect
	termSize ui.Rect
	closed   bool
	// resume is what the pane ran when the layout was saved, for a pane
	// whose session the server lost: the strip that runs it again, gone
	// once the user runs it or dismisses it.
	resume   *resumeHint
	startDir string
	// teleport is the teleport of the pane under way, nil for none, and
	// teleportErr the banner its last failure showed.
	teleport    *teleportRun
	teleportErr string
	// seenFunc tells the server and the window's store that the pane was
	// seen. seeing tells that a request telling the server is under way,
	// and seenTold is the seen time, in Unix nanoseconds, the server last
	// took (tellSeen).
	seenFunc func()
	seeing   atomic.Bool
	seenTold atomic.Int64
	// focusClaimed tells whether the session was last told that the user
	// looks at this pane, through focusConn: the session's size follows
	// the pane shown with the window's focus. A pane attached again tells
	// its new stream.
	focusClaimed bool
	focusConn    any
	// followed tells that the pane's terminal last took a size its program
	// drew for another client (screenSize), which its own size's return
	// has to be mended from too.
	followed bool
	// held counts the frames in which the pane's terminal may still take
	// the size a divider drag gave it: the take mends its screen (held's
	// resyncPane), which output on the way as the session resized may
	// have laid out at a size the program did not draw for. A hold that
	// ends at the size it began takes no size, and asks for no screen.
	held int
	// waitCols and waitRows are the size the pane's view last asked the
	// session for while its terminal still waits for it (sizeWait),
	// waitAt is when the pane asked for it, waitMend tells that a
	// divider drag's hold ended inside the wait, whose screen the take
	// mends then (held), and waitTimer pokes a frame when the wait ends.
	waitCols, waitRows int
	waitAt             time.Time
	waitMend           bool
	waitTimer          *time.Timer
	// agentCleared and agentClearedAt hold local human takeover until the
	// next server poll confirms it or stamps newer CLI input (agenthighlight.go).
	agentCleared   bool
	agentClearedAt time.Time
}

// end lets the pane go from its window: its terminal closes. Its
// transfers go on, as its session does, moved or detached.
func (p *Pane) end() {
	p.closed = true
	if p.waitTimer != nil {
		p.waitTimer.Stop()
	}
	if p.term != nil {
		p.term.Close()
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

// floatPanes returns the tab's floats' panes, the lowest first.
func (t *Tab) floatPanes() []*Pane {
	out := make([]*Pane, 0, len(t.Floats))
	for _, f := range t.Floats {
		if f.Pane != nil {
			out = append(out, f.Pane)
		}
	}
	return out
}

// allPanes returns the tab's tiled panes in tree order, then its floats'.
func (t *Tab) allPanes() []*Pane {
	return append(t.panes(), t.floatPanes()...)
}

// tiledTarget returns the pane a change to the tab's tiled panes goes to:
// the focused one when it is tiled, else the latest tiled one, else the
// first. It is nil for a tab without tiled panes.
func (t *Tab) tiledTarget() *Pane {
	if t.Focus != nil && t.Focus.float == nil && t.Focus.Node != nil {
		return t.Focus
	}
	if p := t.latestTiled(); p != nil {
		return p
	}
	if ps := t.panes(); len(ps) > 0 {
		return ps[0]
	}
	return nil
}

// removeFloat drops f from t's floats: its pane no longer floats, keeps no
// memory as the one hidden last, and leaves t.recent.
func (t *Tab) removeFloat(f *Float) {
	if f == nil {
		return
	}
	t.Floats = slices.DeleteFunc(t.Floats, func(x *Float) bool { return x == f })
	if t.hiddenLast == f {
		t.hiddenLast = nil
	}
	if f.Pane == nil {
		return
	}
	f.Pane.float = nil
	t.recent = slices.DeleteFunc(t.recent, func(q *Pane) bool { return q == f.Pane })
}

// latestTiled returns the pane of the latest of t.recent that is tiled, nil
// for none.
func (t *Tab) latestTiled() *Pane {
	for i := len(t.recent) - 1; i >= 0; i-- {
		if q := t.recent[i]; q.float == nil && q.Node != nil {
			return q
		}
	}
	return nil
}

// nextFocus returns the pane the focus goes to when skip leaves: the latest
// in t.recent that is in sight, else the first tiled pane.
func (t *Tab) nextFocus(skip *Pane) *Pane {
	for i := len(t.recent) - 1; i >= 0; i-- {
		q := t.recent[i]
		if q == skip || q.Node == nil || q.float != nil && q.float.Hidden {
			continue
		}
		return q
	}
	if ps := t.allPanes(); len(ps) > 0 {
		return ps[0]
	}
	return nil
}

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

// recentBehind puts p in t.recent just before the focus, as the pane that
// had the focus before it; the focus stays.
func (t *Tab) recentBehind(p *Pane) {
	t.recent = slices.DeleteFunc(t.recent, func(q *Pane) bool { return q == p })
	i := slices.Index(t.recent, t.Focus)
	if i < 0 {
		i = len(t.recent)
	}
	t.recent = slices.Insert(t.recent, i, p)
}

// newTab opens a tab with a shell in dir, after the active tab, on the
// active tab's host.
func (a *App) newTab(dir string) *Tab { return a.newTabOn(a.currentHost(), dir) }

// newTabOn opens a tab with a shell in dir on host h, nil for this
// machine, after the active tab, and shows it.
func (a *App) newTabOn(h *hostConn, dir string) *Tab { return a.newTabOnIn(h, dir, false) }

// newTabOnIn is newTabOn of a tab of a host that starts in dir or not at
// all when strict (rex.CreateOptions.DirStrict).
func (a *App) newTabOnIn(h *hostConn, dir string, strict bool) *Tab {
	t := a.newTabAfterIn(h, dir, strict, a.tab(), nil)
	if t != nil {
		a.active = slices.Index(a.tabs, t)
		a.focusReq = t.Focus
	}
	return t
}

// newTabAfterIn opens a tab with a shell in dir on host h, nil for this
// machine, after tab after: in the window's workspace when it is h's, else
// in h's tabs with no project, which the window then shows. It opens none
// while the host is not connected. The tab's first pane runs command
// instead of the login shell. The host's server makes it, the active tab,
// and it returns once the window shows it, as tests have it, else nil. It
// starts in dir; in the folder of the project when dir is "", or fails
// (strict is the server's then).
func (a *App) newTabAfterIn(h *hostConn, dir string, strict bool, after *Tab, command []string) *Tab {
	if a.offline(h) {
		return nil
	}
	w := a.ws
	if w == nil || w.host != h {
		w = nil
		if a.st != nil {
			w = a.st.noProjectOf(h)
		}
		after = nil
	}
	c := rex.LayoutCommand{Op: "tab.create", Dir: dir, Command: command, Focus: true}
	if w != nil {
		c.Space = w.ID
	} else if h == nil {
		w = a.st.addWorkspace()
		c.Space = w.ID
		a.st.queue()
	}
	if p := after.localPane(); p != nil {
		c.After = p.SID
	}
	_ = strict
	var t *Tab
	a.askOn(h, c, func(res rex.LayoutCommand) {
		if res.Code == rex.FolderMissing {
			log.Printf("no new tab: %s", res.Error)
			a.folderMissing(dir)
		}
		if res.Code != "" {
			return
		}
		if w == nil && a.st != nil {
			w = a.st.workspaceOfTab(h, res.Tab)
		}
		if w != nil && w != a.ws && a.st != nil && slices.Contains(a.st.workspaces, w) {
			a.showWorkspace(w)
		}
		if t = a.tabByUID(res.Tab); t != nil && a.ws == w {
			a.active = slices.Index(a.tabs, t)
			a.focusReq = t.Focus
		}
	})
	return t
}

// newPaneTab opens a tab of the one pane p, which has no session, after
// the active tab, and shows it, with the keyboard on p.
func (a *App) newPaneTab(p *Pane) *Tab {
	t := a.makeTab("")
	t.Root = &Node{ID: a.id(), Pane: p}
	p.Tab, p.Node = t, t.Root
	t.setFocus(p)
	at := len(a.tabs)
	if cur := a.tab(); cur != nil {
		at = slices.Index(a.tabs, cur) + 1
	}
	a.tabs = slices.Insert(a.tabs, at, t)
	a.active = slices.Index(a.tabs, t)
	a.focusReq = p
	a.groupTabs()
	a.changed()
	return t
}

// newProjectTab is New Tab: a tab of the window's workspace, on its host;
// in a project's folder, which its server starts it in or fails, saying
// so, and starts none elsewhere (docs/MOD-002-projects.md, "New tabs and panes").
// Of the tabs with no project, it starts where the focused pane is.
func (a *App) newProjectTab() *Tab {
	return a.newProjectTabWithCommand(nil)
}

// newProjectTabWithCommand opens a tab in the window's workspace running
// command (nil for the login shell).
func (a *App) newProjectTabWithCommand(command []string) *Tab {
	w := a.ws
	if w == nil {
		return a.newTabAfterIn(a.currentHost(), a.currentDir(), false, a.tab(), command)
	}
	if w.offline() {
		a.offline(w.host)
		return nil
	}
	dir := a.currentDir()
	if w.Dir != "" {
		dir = ""
	}
	t := a.newTabAfterIn(w.host, dir, false, a.tab(), command)
	if t != nil {
		a.active = slices.Index(a.tabs, t)
		a.focusReq = t.Focus
	}
	return t
}

// currentHost is the host of the window's workspace, nil for this
// machine: a tab's panes are all of it.
func (a *App) currentHost() *hostConn {
	if a.ws != nil {
		return a.ws.host
	}
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
	return a.splitPaneRun(old, vertical, dir, nil)
}

// splitPaneRun is splitPane for a pane that runs command instead of the
// login shell. One of this machine the server makes; it returns once the
// window shows it, as tests have it, else nil. One of a host shows as the
// host made its session, unless old closed meanwhile.
func (a *App) splitPaneRun(old *Pane, vertical bool, dir string, command []string) *Pane {
	if a.offline(old.host) {
		return nil
	}
	if serverMade(old.host) {
		return a.askPane(old, rex.LayoutCommand{Op: "pane.split", Vertical: vertical, Dir: dir, Command: command}, a.revealNew)
	}
	// A float splits the tab's tiled pane, not its own standalone node;
	// on a canvas the new box is the float's size, beside it.
	free := old.float != nil && old.Tab.canvas != nil
	if old.float != nil && !free {
		old = old.Tab.tiledTarget()
		if old == nil {
			return nil
		}
	}
	cols, rows := old.paneSize()
	switch {
	case cols <= 0 || rows <= 0:
		cols, rows = 80, 24
	case free:
	case vertical:
		rows = max(rows/2, 2)
	default:
		cols = max(cols/2, 10)
	}
	a.newPaneRun(old.host, dir, cols, rows, command, func(p *Pane) bool {
		if old.closed {
			return false
		}
		a.splitWith(old, p, vertical)
		if a.tab() == old.Tab {
			a.focusReq = p
		}
		return true
	})
	return nil
}

// splitLocal splits the focused pane with the pane make returns, right of
// it or below it. A tab's panes are on one host, so a remote pane takes
// none.
func (a *App) splitLocal(vertical bool, make func() *Pane) {
	t := a.tab()
	if t == nil || t.Focus == nil || t.Focus.host != nil {
		return
	}
	old := t.Focus
	// A float splits the tab's tiled pane, not its own standalone node.
	if old.float != nil && t.canvas == nil {
		old = t.tiledTarget()
		if old == nil {
			return
		}
	}
	p := make()
	a.splitWith(old, p, vertical)
	a.focusReq = p
}

// splitWith puts pane p beside old, right of it or below it, with the
// focus of its tab. A float as old splits the tab's tiled pane instead.
func (a *App) splitWith(old, p *Pane, vertical bool) {
	t := old.Tab
	if old.float != nil && t.canvas != nil {
		a.placeOnCanvas(t, old.float, p, vertical)
		t.setFocus(p)
		a.changed()
		return
	}
	if old.float != nil {
		if old = t.tiledTarget(); old == nil {
			return
		}
	}
	a.splitNode(t, old.Node, p, true, vertical, 0.5)
	t.setFocus(p)
	a.changed()
}

// ask asks the server of the window's workspace's host for a change of
// its layout (askOn).
func (a *App) ask(c rex.LayoutCommand, done func(rex.LayoutCommand)) {
	a.askOn(a.currentHost(), c, done)
}

// askOn asks the server of host h, nil for this machine, for a change of
// the layout that may start a session, after the window's own changes
// (store.sendOn): done runs with its answer once the window shows it,
// made or not (Code). One it does not make shows its error.
func (a *App) askOn(h *hostConn, c rex.LayoutCommand, done func(rex.LayoutCommand)) {
	if a.st == nil {
		if done != nil {
			done(rex.LayoutCommand{Code: "desktop_unavailable", Error: "no session server"})
		}
		return
	}
	a.st.sendOn(h, c, func(res rex.LayoutCommand) {
		switch res.Code {
		case "":
		case "read_only":
			a.err, a.errMsg = "", locales.Messages.ServerLayoutReadOnly
		default:
			a.err, a.errMsg = res.Error, nil
		}
		if done != nil {
			done(res)
		}
	})
}

// askPane asks for a pane.split of c by pane at, its session's or, of no
// session, its tab's focused one, in the window's space; done runs with
// the new pane once the window shows it. It returns the pane then, as
// tests have it, else nil.
func (a *App) askPane(at *Pane, c rex.LayoutCommand, done func(*Pane)) *Pane {
	if at == nil || at.Tab == nil {
		return nil
	}
	// The space is one of the layout the command goes to: the window's,
	// when it shows that host's, which a pane of another host's in this
	// machine's layout is not (docs/MOD-002-projects.md, "The desktop window").
	// None otherwise, and the server finds the pane in any of its spaces.
	if a.ws != nil && a.ws.host == at.host {
		c.Space = a.ws.ID
	}
	if at.SID != "" {
		c.SID = at.SID
	} else {
		at.Tab.setFocus(at)
		c.Tab = at.Tab.UID
	}
	var p *Pane
	h := at.host
	a.askOn(h, c, func(res rex.LayoutCommand) {
		if p = a.paneOn(h, res.New); p != nil && done != nil {
			done(p)
		}
	})
	return p
}

// revealNew pans a canvas to show its new pane p, which gets the keyboard.
func (a *App) revealNew(p *Pane) {
	if p.float != nil {
		a.revealFloat(p.Tab, p.float)
	}
	if a.tab() == p.Tab {
		a.focusReq = p
	}
}

// localPane is the first pane of this machine's sessions in t, nil for
// none or no tab.
func (t *Tab) localPane() *Pane {
	if t == nil {
		return nil
	}
	for _, p := range t.allPanes() {
		if p.SID != "" && !p.closed {
			return p
		}
	}
	return nil
}

// gesturing tells that a gesture moves the window's panes, a divider's or
// a float's drag (a thrown float rests where its glide ends already): what it changes stays the window's until it ends, then
// saves once, and the server's changes wait for it.
func (a *App) gesturing() bool {
	if a.dividerHeld || a.dividerNow {
		return true
	}
	for _, t := range a.tabs {
		for _, f := range t.Floats {
			if f.dragging {
				return true
			}
		}
	}
	return false
}

// closePane closes a pane and ends its session.
func (a *App) closePane(p *Pane) { a.dropPane(p, true) }

// paneStopped decides what a terminal that stopped reading ends: a session
// of a host ends its pane. One of this machine's ends nothing: the server
// takes the pane out of the layout once its session ended, and a stream of
// a server that went away — a crash, a kill, a restart — keeps its pane, to
// attach to the session the server starts in its place.
func (a *App) paneStopped(p *Pane) {
	if p.host != nil || p.SID == "" {
		a.endPane(p)
	}
}

// endPane ends the pane of a terminal that stopped reading, and its tab with
// it when it was the last. The window keeps a tab of this machine's rather
// than closing with its last.
func (a *App) endPane(p *Pane) {
	if p.closed || a.quitting {
		return
	}
	if w := a.keeper(p.Tab); w != nil {
		a.endKept(w, p)
		return
	}
	if p.host != nil && len(a.tabs) == 1 && len(a.tabs[0].allPanes()) == 1 && a.ws != nil {
		// The pane goes once the new tab shows, which keeps the window,
		// or once the tab could not be made.
		home, _ := os.UserHomeDir()
		a.ask(rex.LayoutCommand{Op: "tab.create", Space: a.ws.ID, Dir: home, Focus: true}, func(rex.LayoutCommand) {
			a.dropPane(p, p.remote == nil || !p.remote.Gone())
		})
		return
	}
	// A session the host no longer lists, as one it stopped sharing, is not
	// this machine's to end.
	a.dropPane(p, p.remote == nil || !p.remote.Gone())
}

// endKept ends a pane of the tabs the window keeps of space w, out of
// sight: its tab goes with its last pane, and the space's layout follows.
// A space whose last pane ends goes, as one closed empty.
func (a *App) endKept(w *Workspace, p *Pane) {
	k := w.kept
	shown := k.tabs[min(k.active, len(k.tabs)-1)]
	a.dropPane(p, p.remote == nil || !p.remote.Gone())
	k.tabs = slices.DeleteFunc(k.tabs, func(t *Tab) bool { return t.Root == nil && len(t.Floats) == 0 })
	k.active = max(slices.Index(k.tabs, shown), 0)
	w.layout = a.layoutOf(k.tabs, k.active)
	if len(k.tabs) == 0 {
		w.kept = nil
		a.st.remove(w)
		a.st.save()
	}
	a.changed()
}

// disposePane lets a pane go from the window: its terminal and web view
// close, and its find bar, label field, zoom and place in its tab's order
// of use go with it. kill ends its session and deletes its note, for a
// pane closed rather than moved or gone from the layout.
func (a *App) disposePane(p *Pane, kill bool) {
	p.closed = true
	if a.findPane == p {
		a.closeFind()
	}
	if a.paneRenaming == p {
		a.paneRenaming, a.paneNameFocus = nil, false
	}
	p.end()
	if p.ext != nil {
		p.ext.destroy(kill)
	}
	if kill {
		killOn(p.host, p.SID)
	}
	t := p.Tab
	if t.Zoom == p {
		t.Zoom = nil
	}
	t.recent = slices.DeleteFunc(t.recent, func(q *Pane) bool { return q == p })
}

// dropPane closes a pane, and ends its session when kill is set.
func (a *App) dropPane(p *Pane, kill bool) {
	if p.closed {
		return
	}
	a.disposePane(p, kill)
	t := p.Tab
	a.detachPane(p)
	if len(t.allPanes()) == 0 {
		a.removeTab(t)
		return
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
	chosen := a.active == i
	a.tabs = slices.Delete(a.tabs, i, i+1)
	if a.active > i || a.active >= len(a.tabs) {
		a.active = max(a.active-1, 0)
	}
	// The chosen tab gives way to the next tab of its host, or the one
	// before: one of another host's would show in that host's fold.
	if chosen {
		same := func(o *Tab) bool { return o.host() == t.host() }
		if j := slices.IndexFunc(a.tabs[i:], same); j >= 0 {
			a.active = i + j
		} else {
			for j, o := range slices.Backward(a.tabs[:i]) {
				if same(o) {
					a.active = j
					break
				}
			}
		}
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

// makeTab makes a tab, with the lasting ID id, or a new one when id is ""
// or another tab of the layout has it already, as a tab of an older
// layout, or one copied, would.
func (a *App) makeTab(id string) *Tab {
	if id == "" || a.tabUIDTaken(id) {
		id = layout.NewTabID()
	}
	return &Tab{ID: a.id(), UID: id}
}

// tabUIDTaken reports whether a tab of any window has the lasting ID id.
// One of another window whose space the layout being taken moved it from
// is not (store.placed): that window lets it go as it takes the layout.
func (a *App) tabUIDTaken(id string) bool {
	if slices.ContainsFunc(a.tabs, func(t *Tab) bool { return t.UID == id }) {
		return true
	}
	if a.st == nil {
		return false
	}
	for _, w := range a.st.workspaces {
		if to, ok := a.st.placed[id]; ok && to != w.ID {
			continue
		}
		if w.app != nil && w.app != a {
			if slices.ContainsFunc(w.app.tabs, func(t *Tab) bool { return t.UID == id }) {
				return true
			}
		}
	}
	return false
}

// tabByUID returns this window's tab of lasting ID id, or nil.
func (a *App) tabByUID(id string) *Tab {
	for _, t := range a.tabs {
		if t.UID == id {
			return t
		}
	}
	return nil
}

// closeTab closes a tab and ends the sessions of its panes.
func (a *App) closeTab(t *Tab) {
	for _, p := range t.allPanes() {
		a.disposePane(p, true)
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
	cands := t.panes()
	if t.canvas != nil {
		cands = t.shownFloats()
	}
	for _, p := range cands {
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
	if f := t.Focus.float; f != nil {
		step := amount / 100
		a.resizeFloat(f, float32(dx)*step, float32(dy)*step)
		return
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

// swapPanes puts p and q in each other's places, in their tab, and gives
// the focus to p as pane.swap does.
func (a *App) swapPanes(p, q *Pane) {
	if p == nil || q == nil || p == q || p.Tab == nil || p.Tab != q.Tab || p.Node == nil || q.Node == nil ||
		p.float != nil || q.float != nil {
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
	equalizeTree(t.Root)
	a.changed()
}

// equalizeTree gives every pane of the tree of root an even share.
func equalizeTree(root *Node) {
	if root == nil {
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
	walk(root)
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
	if f := t.Focus.float; f != nil {
		on = !f.Zoom
	}
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
	if f := p.float; f != nil {
		f.Zoom = on
		t.setFocus(p)
		a.changed()
		return
	}
	t.Zoom = nil
	if on && len(t.panes()) > 1 {
		t.setFocus(p)
		t.Zoom = p
	}
	a.changed()
}

// program is how a pane's program shows: what it reported through the
// status protocol when it did, else a shell at its prompt, else the
// executable.
func (p *Pane) program() program {
	if p.ext != nil {
		return p.ext.program()
	}
	var name string
	switch {
	case p.info.Status != nil && p.info.Status.App != "":
		name = p.info.ProgramName()
	case p.info.Idle || p.info.Program == "":
		name = p.info.Shell
	default:
		name = p.info.ProgramName()
	}
	prog := programOf(name)
	if ic := resolveCustomIcon(name, p.info.Args, p.info.ProgramIcon, appearanceDark()); ic != nil {
		return programWithIcon(prog, ic)
	}
	return prog
}

// label returns how a pane names itself: its program, or the title the
// program set, and the directory it works in.
func (p *Pane) label() (name, detail string) {
	if p.ext != nil {
		return p.ext.label(p)
	}
	in := p.info
	if p.Name != "" {
		return p.Name, shortDir(in.Dir)
	}
	prog := programOf(in.ProgramName())
	dir := shortDir(in.Dir)
	if in.Program == "" {
		if in.Shell == "" {
			return "Shell", shortDir(p.startDir)
		}
		return in.Shell, shortDir(p.startDir)
	}
	// A shell at its prompt is the shell, whatever a program that ended
	// there left: its title, and its done or error status, which stays
	// until seen and would name the program (ProgramName).
	if prog.Shell || in.Idle && programOf(in.Program).Shell {
		name := in.Program
		if name == "" {
			name = in.Shell
		}
		return name, dir
	}
	// A program that is not a shell names its pane by the title it sets,
	// even while it is the one the session started with, as a mirror of a
	// Rex block does: the one its terminal here read, else the one the
	// server keeps of the session, for a pane whose terminal has not read
	// one yet, as one of a tab not shown.
	if t := strings.TrimSpace(cmp.Or(p.title, in.Title)); t != "" && !titleOfShell(t, in) {
		return t, ""
	}
	if in.Idle {
		name := in.Program
		if name == "" {
			name = in.Shell
		}
		return name, dir
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
		if w == "" {
			continue
		}
		// Windows's consoles title themselves with the shell's file:
		// "cmd.exe", "C:\WINDOWS\system32\cmd.exe", or "- ping" while a
		// child runs. A program's own title that starts with its name
		// says more: "devin.exe: learn the design system".
		if l == w || l == w+".exe" ||
			strings.HasPrefix(l, w+" - ") || strings.HasPrefix(l, w+".exe - ") ||
			strings.Contains(l, `\`+w+".exe") {
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

// poll asks this machine's server what runs in each session, every half
// second, for every window, until stop closes or the connection does;
// update runs a change on the main thread. The windows draw again as the
// list changes (its ETag). Less often it refreshes the remote host lists.
func (st *store) poll(client *rex.Client, stop chan struct{}, update func(func())) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	rounds, etag := 0, ""
	for {
		select {
		case <-stop:
			return
		case <-client.Closed():
			update(func() {
				for _, a := range st.apps {
					// Not once the window took a new connection.
					if !a.quitting && a.client == client {
						a.errMsg = locales.Messages.ErrorConnectionLost
					}
				}
				st.redraw()
			})
			return
		case <-tick.C:
		}
		rounds++
		var tailnet *rex.TailnetStatus
		if rounds%30 == 1 {
			if s, err := client.RemoteStatus(); err == nil {
				tailnet = s.Tailnet
			}
		}
		infos, next, changed, err := client.ListSince(etag)
		if err != nil {
			continue
		}
		etag = next
		byID := make(map[string]rex.SessionInfo, len(infos))
		for _, in := range infos {
			byID[in.ID] = in
		}
		update(func() {
			// A list on its way as the last window closed, or the
			// connection was replaced.
			if st.polling != stop {
				return
			}
			if tailnet != nil {
				st.apps[0].reconcileTailnet(tailnet)
			}
			if changed {
				st.infos = byID
			}
			st.pollRound(changed || tailnet != nil)
			if changed || tailnet != nil {
				st.redraw()
			}
		})
	}
}

// take takes into the window's panes what the server of host h, nil for
// this machine's, said of its sessions (apply).
func (a *App) take(h *hostConn, byID map[string]rex.SessionInfo) {
	var events []map[string]string
	for _, t := range a.tabs {
		for _, p := range t.allPanes() {
			in, ok := byID[p.SID]
			if !ok || p.host != h {
				continue
			}
			was := p.info
			p.info = in
			p.reconcileAgent(in)
			// A strip naming what the pane ran has served its purpose once
			// something runs there again.
			if p.resume != nil && !in.Idle {
				p.resume = nil
			}
			events = append(events, statusEvents(p, was.Statuses, in.Statuses)...)
			// While another device holds the session's size, the pane's
			// resizes are ignored, and the session keeps that size; once
			// it lets go, the pane gives the session its own. A server
			// of now takes its authority's size itself as the lock
			// ends; one before does not, and outlives the app: a
			// Windows server never upgrades in place.
			if was.SizeLock != "" && in.SizeLock == "" && p.stream != nil {
				go p.stream.sendSize()
			}
			// The layout keeps a host's pane's directory, which this
			// machine's server does not stamp: a change saves it.
			if h != nil && was.Dir != in.Dir {
				a.changed()
			}
			seen := a.shows(p)
			if seen {
				p.see()
			} else if r := entered(was, in); r != nil && p.notifyDue(false) {
				a.notifyStatus(p, r)
			}
			// A program that ran a while and finished out of sight
			// asks for attention. One that reports its status was
			// notified of it.
			if !was.Idle && was.Program != "" && in.Idle && was.Program != in.Program {
				if !seen {
					p.askAttention()
				}
				statuses := len(was.Statuses) > 0 || len(in.Statuses) > 0
				if !a.focusedWin && time.Since(was.LastInput) > 8*time.Second && !statuses && p.notifyDue(false) {
					prog := programOf(was.ProgramName())
					a.notifyPane(p, prog.Name, prog, locales.NotifyFinished(prog.Name), locales.NotifyIn(shortDir(in.Dir)))
				}
			}
		}
	}
	a.syncSizes()
	if a.luaConfig != nil {
		for _, ev := range events {
			a.luaConfig.Emit("program_status_changed", ev)
		}
	}
	a.saveIfDue()
}

// statusEvents are the program status changes of a pane, for the Lua
// event. A record gone says clear.
func statusEvents(p *Pane, was, now []rex.ProgramStatus) []map[string]string {
	if len(was) == 0 && len(now) == 0 {
		return nil
	}
	old := make(map[string]rex.ProgramStatus, len(was))
	for _, r := range was {
		old[r.ID] = r
	}
	var out []map[string]string
	add := func(r rex.ProgramStatus, state string) {
		out = append(out, map[string]string{
			"session": p.SID, "pane": p.labelName(), "state": state,
			"id": r.ID, "kind": r.Kind, "title": r.Title, "msg": r.Msg, "last_msg": r.LastMsg,
			"app": r.App, "progress": strconv.Itoa(r.Progress),
			"program": p.info.Program, "dir": p.info.Dir,
		})
	}
	for _, r := range now {
		if o, ok := old[r.ID]; !ok || o.State != r.State || o.Kind != r.Kind ||
			o.Title != r.Title || o.Msg != r.Msg || o.App != r.App || o.Progress != r.Progress {
			add(r, r.State)
		}
		delete(old, r.ID)
	}
	for _, r := range old {
		add(r, "clear")
	}
	return out
}

// see marks what the pane shows as seen: its attention, and the done and
// error records of its program.
func (p *Pane) see() {
	p.attention = false
	for _, r := range p.info.Statuses {
		if (r.State == "done" || r.State == "error") && r.Updated.After(p.seen) {
			p.seen = r.Updated
		}
	}
	if p.seenFunc != nil {
		p.seenFunc()
	}
}

// tellSeen tells the pane's server, when it keeps what was seen
// (rex.CapabilityStatusSeen), what the pane saw that it does not know
// yet: one request at a time, as see runs each frame, and a failed one
// again a second later at the earliest. A server before it, which knows
// nothing of it, leaves the pane's and the store's own seen to stand in.
func (a *App) tellSeen(p *Pane) {
	told := p.info.StatusSeen
	if t := time.Unix(0, p.seenTold.Load()); t.After(told) {
		told = t
	}
	h := p.host
	if !p.seen.After(told) || !a.keepsSeen(h) {
		return
	}
	c := a.clientOf(h)
	if c == nil || !p.seeing.CompareAndSwap(false, true) {
		return
	}
	sid, at := p.SID, p.seen
	go func() {
		if err := c.SeeStatus(sid, at); err != nil {
			time.Sleep(time.Second)
		} else {
			p.seenTold.Store(at.UnixNano())
		}
		p.seeing.Store(false)
	}()
}

// keepsSeen tells whether the server of host h, nil for this machine's,
// keeps when its sessions' status was seen.
func (a *App) keepsSeen(h *hostConn) bool {
	if h != nil {
		return h.hello.Can(rex.CapabilityStatusSeen)
	}
	return a.st != nil && a.st.hello.Can(rex.CapabilityStatusSeen)
}

// status is the record of the pane's program that most needs the user,
// nil for none.
func (p *Pane) status() *rex.ProgramStatus {
	seen := p.seen
	if p.info.StatusSeen.After(seen) {
		seen = p.info.StatusSeen
	}
	return rex.MostUrgent(p.info.Statuses, seen)
}

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
