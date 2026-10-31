package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
)

// Workspace is what Rex and the interface call a session: tabs of their
// own, shown in one window at a time, named with a random label as
// "icy-ridge". The code calls it a workspace, as a session there is a
// terminal of the session server (Pane.SID).
type Workspace struct {
	ID   string
	Name string
	// Color is the space's color: a preset id from spacePalette, a custom
	// "#rrggbb", or "" for the default look.
	Color string
	// layout is its tabs while no window shows it; app is the window that
	// shows it, whose tabs are then its own.
	layout savedLayout
	app    *App
}

// status is the most urgent status of the workspace's sessions, from the
// window that shows it or, hidden, from the last session list.
func (st *store) status(w *Workspace) *rex.ProgramStatus {
	if w == nil {
		return nil
	}
	if w.app != nil {
		var all []rex.ProgramStatus
		for _, t := range w.app.tabs {
			for _, p := range t.panes() {
				if s := p.status(); s != nil {
					all = append(all, *s)
				}
			}
		}
		return rex.MostUrgent(all, time.Time{})
	}
	var sids []string
	for _, t := range w.layout.Tabs {
		savedSIDs(t.Root, &sids)
	}
	var best *rex.ProgramStatus
	for _, sid := range sids {
		s := rex.MostUrgent(st.infos[sid].Statuses, st.seen[sid])
		if s != nil && (best == nil || rex.StatusRank(s.State) > rex.StatusRank(best.State) ||
			rex.StatusRank(s.State) == rex.StatusRank(best.State) && s.Updated.After(best.Updated)) {
			best = s
		}
	}
	return best
}

// recordSeen notes that a session's status was seen.
func (st *store) recordSeen(sid string, at time.Time) {
	if sid == "" || at.IsZero() {
		return
	}
	if st.seen == nil {
		st.seen = map[string]time.Time{}
	}
	if at.After(st.seen[sid]) {
		st.seen[sid] = at
	}
}

func savedSIDs(n *savedNode, out *[]string) {
	if n == nil {
		return
	}
	if n.SID != "" {
		*out = append(*out, n.SID)
	}
	savedSIDs(n.A, out)
	savedSIDs(n.B, out)
}

// tabCount is how many tabs the workspace has.
func (w *Workspace) tabCount() int {
	if w.app != nil {
		return len(w.app.tabs)
	}
	return len(w.layout.Tabs)
}

// store owns the workspaces of the app, the one process GoRex runs as, and
// its windows, each of which shows one: it saves them all, one layout at a
// time, so that no window writes over another's.
type store struct {
	client *rex.Client
	hello  rex.Hello
	errMsg func(locales.Messages) string
	hosts  []*hostConn
	// hostsUp tells that the hosts were read and are connected to.
	hostsUp bool

	workspaces []*Workspace
	// infos are this machine's sessions, as the windows last polled them,
	// for the status of workspaces no window shows; seen is when each
	// session's done or error record was seen.
	infos map[string]rex.SessionInfo
	seen  map[string]time.Time
	// apps are the open windows, the most recently focused first.
	apps []*App
	// lastClosed is the workspace of the last window, once it closed: it
	// opens again with the app, or as the app is activated (macOS).
	lastClosed *Workspace

	started, quitting, debugging bool
	// debugWin is the window, from 1 by place, the debug hook's lines
	// target; 0 for the one focused last.
	debugWin int
	last     string // the layout sent last
	// noAccel are the menu shortcuts a keymap binding takes over, and
	// recording takes every accelerator while the keyboard records.
	noAccel   map[string]bool
	recording bool
	writer    layoutWriter
}

// The layout the server keeps, opaque to it: every workspace, and which
// of them the windows showed, in the order of their places.
type savedStore = layout.Store

// savedWorkspace is a workspace as the layout keeps it.
type savedWorkspace = layout.Workspace

// layoutVersion is the version of savedStore; a layout without one is the
// tabs of a GoRex of one window.
const layoutVersion = 2

// parseLayout reads the layout the server keeps, a workspace for the
// tabs of an older GoRex. Workspaces without an id, or of an id already
// read, and windows of none, are dropped.
func parseLayout(raw []byte) savedStore {
	var s savedStore
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return savedStore{Version: layoutVersion}
	}
	if s.Version < layoutVersion {
		if len(s.Tabs) == 0 {
			return savedStore{Version: layoutVersion}
		}
		w := savedWorkspace{ID: newWorkspaceID(), Name: workspaceName(nil), Layout: s.Layout}
		return savedStore{Version: layoutVersion, Sessions: []savedWorkspace{w}, Windows: []string{w.ID}}
	}
	out := savedStore{Version: s.Version}
	ids := map[string]bool{}
	for _, w := range s.Sessions {
		if w.ID == "" || ids[w.ID] {
			continue
		}
		ids[w.ID] = true
		if strings.TrimSpace(w.Name) == "" {
			w.Name = workspaceName(nil)
		}
		out.Sessions = append(out.Sessions, w)
	}
	for _, id := range s.Windows {
		if ids[id] && !slices.Contains(out.Windows, id) {
			out.Windows = append(out.Windows, id)
		}
	}
	return out
}

// saved is the layout of every workspace, and of the windows.
func (st *store) saved() savedStore {
	s := savedStore{Version: layoutVersion, Sessions: []savedWorkspace{}, Windows: st.windowIDs()}
	for _, w := range st.workspaces {
		l := w.layout
		if w.app != nil {
			l = w.app.snapshot()
		}
		s.Sessions = append(s.Sessions, savedWorkspace{ID: w.ID, Name: w.Name, Color: w.Color, Layout: l})
		if len(s.Windows) > 0 && w.ID == s.Windows[0] {
			s.Layout = l
		}
	}
	return s
}

// windowIDs are the workspaces of the windows, in the order of their
// places; that of the last window once it closed.
func (st *store) windowIDs() []string {
	apps := slices.Clone(st.apps)
	slices.SortFunc(apps, func(x, y *App) int { return x.slot - y.slot })
	ids := []string{}
	for _, a := range apps {
		if a.ws != nil {
			ids = append(ids, a.ws.ID)
		}
	}
	if len(ids) == 0 && st.lastClosed != nil && slices.Contains(st.workspaces, st.lastClosed) {
		ids = append(ids, st.lastClosed.ID)
	}
	return ids
}

// save sends the layout to the server, unless it is the one sent last.
func (st *store) save() {
	if st.client == nil {
		return
	}
	b, err := json.Marshal(st.saved())
	if err != nil {
		log.Printf("gorex: saving the layout: %v", err)
		return
	}
	if string(b) == st.last {
		return
	}
	st.last = string(b)
	st.writer.write(st.client, b)
}

// saveNow saves, and waits a second at most for the server to keep it.
func (st *store) saveNow() {
	st.save()
	st.writer.wait(time.Second)
}

// layoutWriter sends layouts to the server one at a time, in the order
// written: one waiting to be sent gives way to a newer one.
type layoutWriter struct {
	mu      sync.Mutex
	next    []byte
	client  *rex.Client
	running bool
	idle    chan struct{} // closed once nothing waits
}

// write sends b through c, after the layout being sent, if any.
func (w *layoutWriter) write(c *rex.Client, b []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.next, w.client = b, c
	if w.running {
		return
	}
	w.running, w.idle = true, make(chan struct{})
	go w.run()
}

// run sends the layout waiting until none waits.
func (w *layoutWriter) run() {
	for {
		w.mu.Lock()
		b, c := w.next, w.client
		w.next = nil
		if b == nil {
			w.running = false
			close(w.idle)
			w.mu.Unlock()
			return
		}
		w.mu.Unlock()
		if err := c.SetLayout(b); err != nil {
			log.Printf("gorex: saving the layout: %v", err)
		}
	}
}

// wait waits until what was written is sent, for d at most.
func (w *layoutWriter) wait(d time.Duration) {
	w.mu.Lock()
	idle, running := w.idle, w.running
	w.mu.Unlock()
	if !running {
		return
	}
	select {
	case <-idle:
	case <-time.After(d):
	}
}

// load reads the workspaces the server keeps, and returns those of the
// windows open last.
func (st *store) load() []*Workspace {
	raw, err := st.client.Layout()
	if err != nil {
		log.Print(err)
	}
	s := parseLayout(raw)
	st.workspaces = nil
	for _, sw := range s.Sessions {
		st.workspaces = append(st.workspaces, &Workspace{ID: sw.ID, Name: sw.Name, Color: sw.Color, layout: sw.Layout})
	}
	var open []*Workspace
	for _, id := range s.Windows {
		open = append(open, st.byID(id))
	}
	return open
}

// byID returns the workspace of an id, nil when none.
func (st *store) byID(id string) *Workspace {
	for _, w := range st.workspaces {
		if w.ID == id {
			return w
		}
	}
	return nil
}

// namedSIDs are the sessions of this machine the workspaces' layouts name,
// but w's.
func (st *store) namedSIDs(w *Workspace) map[string]bool {
	out := map[string]bool{}
	var walk func(n *savedNode)
	walk = func(n *savedNode) {
		if n == nil {
			return
		}
		if n.Host == "" && n.SID != "" {
			out[n.SID] = true
		}
		walk(n.A)
		walk(n.B)
	}
	for _, o := range st.workspaces {
		if o == w {
			continue
		}
		tabs := o.layout.Tabs
		if o.app != nil {
			tabs = o.app.snapshot().Tabs
		}
		for _, t := range tabs {
			walk(t.Root)
		}
	}
	return out
}

// hostSIDs are the sessions of host h the layouts of the workspaces no
// window shows name.
func (st *store) hostSIDs(h *hostConn) map[string]bool {
	out := map[string]bool{}
	var walk func(n *savedNode)
	walk = func(n *savedNode) {
		if n == nil {
			return
		}
		if n.Host == h.ID && n.SID != "" {
			out[n.SID] = true
		}
		walk(n.A)
		walk(n.B)
	}
	for _, w := range st.workspaces {
		if w.app == nil {
			for _, t := range w.layout.Tabs {
				walk(t.Root)
			}
		}
	}
	return out
}

// addWorkspace makes a workspace of a new name and color, last.
func (st *store) addWorkspace() *Workspace {
	w := &Workspace{ID: newWorkspaceID(), Name: workspaceName(st.taken), Color: st.nextSpaceColor()}
	st.workspaces = append(st.workspaces, w)
	return w
}

// workspaceByID returns the workspace with the ID, nil for none.
func (st *store) workspaceByID(id string) *Workspace {
	for _, w := range st.workspaces {
		if w.ID == id {
			return w
		}
	}
	return nil
}

// taken tells whether a workspace has the name.
func (st *store) taken(name string) bool {
	return slices.ContainsFunc(st.workspaces, func(w *Workspace) bool { return strings.EqualFold(w.Name, name) })
}

// remove forgets a workspace, and the window that shows it shows none.
func (st *store) remove(w *Workspace) {
	st.workspaces = slices.DeleteFunc(st.workspaces, func(o *Workspace) bool { return o == w })
	if w.app != nil {
		w.app.ws = nil
		w.app = nil
	}
	if st.lastClosed == w {
		st.lastClosed = nil
	}
}

// hiddenAfter returns the first workspace no window shows, after w in
// their order, else before it; nil when every one shows.
func (st *store) hiddenAfter(w *Workspace) *Workspace {
	i := slices.Index(st.workspaces, w)
	n := len(st.workspaces)
	for d := 1; d <= n; d++ {
		o := st.workspaces[((i+d)%n+n)%n]
		if o != w && o.app == nil {
			return o
		}
	}
	return nil
}

// raise puts a first among the windows, as the one focused last.
func (st *store) raise(a *App) {
	if i := slices.Index(st.apps, a); i > 0 {
		st.apps = slices.Insert(slices.Delete(st.apps, i, i+1), 0, a)
	}
}

// appOf returns the window's app, or the window focused last's for a
// window of none, as Settings; nil without windows.
func (st *store) appOf(win *mygo.Window) *App {
	for _, a := range st.apps {
		if a.win == win && win != nil {
			return a
		}
	}
	if len(st.apps) > 0 {
		return st.apps[0]
	}
	return nil
}

// freeSlot is the first place no window takes.
func (st *store) freeSlot() int {
	for i := 0; ; i++ {
		if !slices.ContainsFunc(st.apps, func(a *App) bool { return a.slot == i }) {
			return i
		}
	}
}

// stateKey names the bounds MyGo keeps of the window in a place: the first
// keeps those GoRex had of its one window.
func stateKey(slot int) string {
	if slot == 0 {
		return "main"
	}
	return fmt.Sprintf("main-%d", slot+1)
}

// redraw draws every window again.
func (st *store) redraw() {
	for _, a := range st.apps {
		if a.win != nil {
			a.win.Update(func() {})
		}
	}
	if w := settingsUI.win; w != nil {
		w.Update(func() {})
	}
}

// connect connects to the session server once, which a build of before
// this one runs in its place; it reports whether it did.
func (st *store) connect() bool {
	if c := st.client; c != nil {
		select {
		case <-c.Closed():
		default:
			return true
		}
	}
	client, err := rex.Connect()
	if err != nil {
		log.Print(err)
		mygo.Dialog.Error(locales.ErrorServerStart(), err.Error())
		mygo.App.Quit()
		return false
	}
	return st.adopt(client)
}

// adopt takes client as the app's connection: the hello, a server a build of
// before this one runs made to run this build's in its place, its layout
// commands, and the windows open shown again on it, attached to their
// sessions.
func (st *store) adopt(client *rex.Client) bool {
	var err error
	st.client, st.errMsg = client, nil
	st.hello, err = client.Hello()
	if err == nil && staleServer(st.hello) {
		// A server a build before this one started runs that build's
		// code: it runs this build's in its place, keeping its sessions.
		// Only for a newer build: two copies of the app would take the
		// server from each other at every launch. One that cannot upgrade
		// is under mygo dev restarted, and on Windows by a newer build
		// once the user agrees: the next server starts its sessions again.
		// Otherwise it is kept.
		var next *rex.Client
		if st.hello.Upgrade {
			if exe, at := rex.Executable(); at.After(st.hello.ExeTime) {
				if next, err = client.Upgrade(exe); err != nil {
					log.Print(err)
					client.Close()
					next, err = rex.Connect()
				}
			}
		} else if mygo.IsDev() {
			// A restart stops the server so that the next one starts the
			// sessions of the layout again (Restart sends restart:true).
			next, err = client.Restart(st.hello.PID)
		} else if runtime.GOOS == "windows" && st.hello.Can(rex.CapabilityShutdownRestart) {
			// Windows servers cannot upgrade in place: a newer build
			// restarts one that brings its sessions back, once the user
			// agrees to end the programs in its panes.
			if _, at := rex.Executable(); at.After(st.hello.ExeTime) {
				res, derr := mygo.Dialog.Message(mygo.MessageOptions{
					Type:          mygo.MessageWarning,
					Message:       locales.ServerRestartAskTitle(),
					Detail:        locales.ServerRestartAskDetail(),
					Buttons:       []string{locales.ServerRestartAskRestart(), locales.ServerRestartAskLater()},
					DefaultButton: 0,
					CancelButton:  1,
				})
				if derr == nil && res.Button == 0 {
					next, err = client.Restart(st.hello.PID)
				}
			}
		}
		if err != nil {
			log.Print(err)
			mygo.Dialog.Error(locales.ErrorServerRestart(), err.Error())
			mygo.App.Quit()
			return false
		}
		if next != nil {
			st.client = next
			st.hello, err = next.Hello()
		}
	}
	if err == nil && st.hello.Version != rex.ProtocolVersion {
		st.errMsg = locales.Messages.ErrorServerVersion
	}
	go st.takeCommands(st.client, func(fn func()) { go mygo.RunOnMain(fn) })
	// The windows open take the new connection, attached again to their
	// sessions, as the old one closed.
	for _, a := range st.apps {
		a.client, a.hello, a.errMsg = st.client, st.hello, st.errMsg
		if w := a.ws; w != nil {
			a.detach()
			a.show(w, nil)
		}
		if a.win != nil {
			go a.poll(a.win, st.client)
		}
	}
	go st.watch(st.client)
	return true
}

// watch connects again when the connection to c closes. A server that goes
// away — a crash, a kill, a restart — ends every stream it had, and the
// server that comes next starts the sessions of the run before it again; the
// windows attach to theirs, as they were.
func (st *store) watch(c *rex.Client) {
	if c == nil {
		return
	}
	go func() {
		<-c.Closed()
		for i := range 40 {
			time.Sleep(min(time.Duration(i+1)*250*time.Millisecond, 3*time.Second))
			var stop bool
			mygo.RunOnMain(func() { stop = !st.started || st.quitting || st.client != c })
			if stop {
				return
			}
			// Dialing waits, and a window that waits does not draw.
			client, err := rex.Connect()
			if err != nil {
				log.Print(err)
				continue
			}
			mygo.RunOnMain(func() {
				if st.client != c {
					client.Close()
					stop = true
					return
				}
				stop = st.adopt(client)
			})
			if stop {
				return
			}
		}
	}()
}

// start opens the windows open when the app quit, with their workspaces;
// one at least, of the first workspace or a new one. Sessions of this
// machine no workspace names get tabs in the first.
func (st *store) start() {
	if st.started {
		return
	}
	st.started = true
	if !st.connect() {
		return
	}
	open := slices.DeleteFunc(st.load(), func(w *Workspace) bool { return w == nil })
	if len(open) == 0 {
		if len(st.workspaces) > 0 {
			open = st.workspaces[:1]
		} else {
			open = []*Workspace{st.addWorkspace()}
		}
	}
	for i, w := range open {
		st.openWindow(w, i == 0)
	}
	st.save()
}

// reopen opens a window when none is: of the workspace of the last one,
// else of one no window shows, else of a new one. Before the app started,
// the start, which opens one at least, serves it.
func (st *store) reopen() {
	if !st.started || len(st.apps) > 0 || !st.connect() {
		return
	}
	w := st.lastClosed
	if w == nil || !slices.Contains(st.workspaces, w) {
		w = st.hiddenAfter(nil)
	}
	if w == nil {
		w = st.addWorkspace()
	}
	st.openWindow(w, false)
	st.save()
}

// openNew opens a window of a new workspace, as New Window and New
// Space do without windows; before the app started, the start serves it.
func (st *store) openNew() {
	if st.started && st.connect() {
		st.openWindow(st.addWorkspace(), false)
		st.save()
	}
}

// secondInstance is a launch of the app while it runs: it shows the
// window focused last, or opens one.
func (st *store) secondInstance() {
	if len(st.apps) > 0 {
		st.apps[0].front()
		return
	}
	st.reopen()
}

// newWindow makes the window of a workspace; tests make none.
var newWindow = func(a *App) *mygo.Window {
	opts := mygo.WindowOptions{
		Title:                "GoRex",
		Width:                1000,
		Height:               620,
		MinWidth:             560,
		MinHeight:            340,
		StateKey:             stateKey(a.slot),
		TitleBarStyle:        mygo.TitleBarHidden,
		TrafficLightPosition: &mygo.Point{X: 16, Y: 15},
		BackgroundColor:      "light-dark(#efe1e6, #231e27)",
		Content:              ui.View(a.view),
	}
	if ownControls {
		// The title bar draws the window's controls (windowControls); the
		// hidden title bar keeps the window's top edge, which resizes it.
		opts.HideWindowButtons = true
	}
	return mygo.NewWindow(opts)
}

// openWindow opens a window that shows w; with adopt, the sessions of this
// machine no workspace names get tabs in it.
func (st *store) openWindow(w *Workspace, adopt bool) *App {
	a := &App{st: st, client: st.client, hello: st.hello, errMsg: st.errMsg, slot: st.freeSlot(), stop: make(chan struct{}), keys: newKeyDispatcher()}
	a.win = newWindow(a)
	if err := a.loadLuaConfig(); err != nil {
		a.err = err.Error()
	}
	st.apps = slices.Insert(st.apps, 0, a)
	if !st.hostsUp {
		a.loadHosts()
	} else {
		a.hosts = st.hosts
	}
	var others map[string]bool
	if adopt {
		others = st.namedSIDs(w)
	}
	a.show(w, others)
	if win := a.win; win != nil {
		go a.poll(win, st.client)
		win.OnFocus(func() { st.raise(a) })
		win.OnClosed(func() { st.closed(a) })
		if !st.debugging {
			st.debugging = true
			st.debugHook(a)
		}
	}
	return a
}

// closed forgets a window that closed; its workspace goes on, its
// terminals detached. The hosts stay connected for the next window, as
// the app stays without windows on macOS.
func (st *store) closed(a *App) {
	close(a.stop)
	a.quitting = true
	w := a.ws
	a.detach()
	a.win = nil
	st.apps = slices.DeleteFunc(st.apps, func(o *App) bool { return o == a })
	if len(st.apps) == 0 && w != nil {
		st.lastClosed = w
	}
	if !st.quitting {
		st.saveNow()
	}
}

// newWorkspaceID returns a new workspace's id.
func newWorkspaceID() string { return rand.Text()[:12] }

// newTabID returns a tab's own ID, as "K3QV7Z2M9XPA".
func newTabID() string { return rand.Text()[:12] }

// The words of workspaces' names, as Rex's "icy-ridge".
var (
	nameAdjectives = []string{"amber", "brisk", "calm", "coral", "crisp", "dusky", "eager", "fern", "gentle", "golden", "hazy", "icy", "jade", "keen", "lucky", "lunar", "misty", "mossy", "noble", "quiet", "rapid", "rusty", "silent", "sunny", "swift", "tidal", "velvet", "vivid", "windy", "young"}
	nameNouns      = []string{"bay", "brook", "canyon", "cedar", "cliff", "comet", "delta", "dune", "ember", "fjord", "glade", "grove", "harbor", "island", "lagoon", "meadow", "mesa", "orbit", "peak", "pine", "reef", "ridge", "river", "shore", "summit", "tundra", "valley", "willow"}
)

// workspaceName returns a random name, as "icy-ridge", that taken does not
// report taken, if it finds one.
func workspaceName(taken func(string) bool) string {
	pick := func(words []string) string {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
		return words[n.Int64()]
	}
	name := ""
	for range 50 {
		name = pick(nameAdjectives) + "-" + pick(nameNouns)
		if taken == nil || !taken(name) {
			return name
		}
	}
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s-%d", name, i); !taken(n) {
			return n
		}
	}
}

// show shows w in the window: the tabs of its layout, attached to the
// sessions the server has. With others, the sessions of this machine
// neither w nor others name get tabs too. An empty workspace gets a tab.
func (a *App) show(w *Workspace, others map[string]bool) { a.showTabs(w, others, true) }

// showEmpty shows a workspace, opening no tab in one that has none: a
// client that makes the tabs it wants, as a Rex client does when it
// creates a session with no windows.
func (a *App) showEmpty(w *Workspace) { a.showTabs(w, nil, false) }

// showTabs shows a workspace's saved tabs, and opens one in its home
// directory when it has none and fallback asks for it. A space shown
// before keeps its tabs in the layout, for the window that shows it again.
func (a *App) showTabs(w *Workspace, others map[string]bool, fallback bool) {
	if a.ws != nil && a.ws != w {
		a.detach()
	}
	a.ws, w.app = w, a
	a.tabs, a.active, a.focusReq, a.renaming = nil, 0, nil, nil
	if !a.restore(w.layout, others) && fallback {
		home, _ := os.UserHomeDir()
		a.newTabOn(nil, home)
	}
	a.changed()
}

// detach closes the window's terminals, keeping their sessions, and its
// tabs in its workspace's layout.
func (a *App) detach() {
	if w := a.ws; w != nil {
		w.layout = a.snapshot()
		w.app = nil
	}
	for _, t := range a.tabs {
		for _, p := range t.panes() {
			p.closed = true
			if p.term != nil {
				p.term.Close()
			}
		}
	}
	a.ws, a.tabs, a.active, a.focusReq, a.renaming = nil, nil, 0, nil, nil
	a.wsOpen, a.wsRenaming, a.wsConfirm = false, false, false
}

// front shows the window over the others, and gives it the keyboard.
func (a *App) front() {
	if a.st != nil {
		a.st.raise(a)
	}
	if w := a.win; w != nil {
		if w.IsMinimized() {
			w.Restore()
		}
		w.Show()
		w.Focus()
	}
}

// showWorkspace shows w in the window, or focuses the window that shows
// it already.
func (a *App) showWorkspace(w *Workspace) {
	if a.st == nil || w == nil || w == a.ws {
		return
	}
	if w.app != nil {
		w.app.front()
		return
	}
	a.detach()
	a.show(w, nil)
	a.st.save()
}

// showSpace shows the nth space, from 1; 9 is the last, as showTab's
// numbers the tabs. A number no space has does nothing.
func (a *App) showSpace(n int) {
	if a.st == nil || n < 1 || n > 9 {
		return
	}
	homes := a.st.workspaces
	if n == 9 || n > len(homes) {
		n = len(homes)
	}
	if n < 1 {
		return
	}
	a.showWorkspace(homes[n-1])
}

// newWorkspace shows a new workspace in the window.
func (a *App) newWorkspace() {
	if a.st != nil {
		a.showWorkspace(a.st.addWorkspace())
	}
}

// newWindowCmd opens a window of a new workspace.
func (a *App) newWindowCmd() {
	if a.st != nil {
		a.st.openNew()
	}
}

// cycleWorkspace shows the next workspace that no other window shows,
// toward d.
func (a *App) cycleWorkspace(d int) {
	if a.st == nil {
		return
	}
	ws := a.st.workspaces
	i, n := slices.Index(ws, a.ws), len(ws)
	for k := 1; k < n; k++ {
		if o := ws[((i+d*k)%n+n)%n]; o.app == nil {
			a.showWorkspace(o)
			return
		}
	}
}

// renameWorkspace names the window's workspace; an empty name keeps the
// one it has.
func (a *App) renameWorkspace(name string) {
	if name = strings.TrimSpace(name); name != "" && a.ws != nil {
		a.ws.Name = name
		a.changed()
	}
}

// closeWorkspace ends the sessions of the window's workspace and forgets
// it; the window shows another, which no window shows, or a new one.
func (a *App) closeWorkspace() {
	st, w := a.st, a.ws
	if st == nil || w == nil {
		return
	}
	for _, t := range a.tabs {
		for _, p := range t.panes() {
			p.closed = true
			if p.term != nil {
				p.term.Close()
			}
			a.killOn(p.host, p.SID)
		}
	}
	a.tabs = nil
	next := st.hiddenAfter(w)
	st.remove(w)
	a.detach()
	if next == nil {
		next = st.addWorkspace()
	}
	a.show(next, nil)
	st.save()
}

// emptied follows the close of the last tab of the window's workspace,
// which goes: the window shows another that no window shows, or closes.
// It reports whether the window stays.
func (a *App) emptied() bool {
	st, w := a.st, a.ws
	if st == nil || w == nil {
		return false
	}
	next := st.hiddenAfter(w)
	st.remove(w)
	if next == nil {
		st.save()
		return false
	}
	a.show(next, nil)
	st.save()
	return true
}

// moveTabToNewWindow moves a tab to a new workspace, in a window of its
// own, its sessions going on. The last tab stays.
func (a *App) moveTabToNewWindow(t *Tab) {
	st := a.st
	if st == nil || t == nil || len(a.tabs) < 2 {
		return
	}
	l := savedLayout{Tabs: []savedTab{a.savedTab(t)}}
	for _, p := range t.panes() {
		p.closed = true
		if p.term != nil {
			p.term.Close()
		}
	}
	a.removeTab(t)
	w := st.addWorkspace()
	w.layout = l
	st.openWindow(w, false)
	st.save()
}
