package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"runtime"
	"slices"
	"strings"
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
	// Color is the space's color: a preset id from layout.Palette, a custom
	// "#rrggbb", or "" for the default look.
	Color string
	// layout is its tabs while no window shows it; app is the window that
	// shows it, whose tabs are then its own.
	layout savedLayout
	app    *App
	// kept are the live tabs of the window that last showed it, which
	// shows them again as they were; nil when none.
	kept *keptTabs
}

// keptTabs are a window's tabs of a space it went away from.
type keptTabs struct {
	app    *App
	tabs   []*Tab
	active int
}

// unkeep closes the panes a window kept of the space, which then shows
// from its layout.
func (w *Workspace) unkeep() {
	if k := w.kept; k != nil {
		w.kept = nil
		closePanes(k.tabs)
	}
}

// unkeepAll closes the panes window a keeps of the spaces it went away
// from: their callbacks are a's.
func (st *store) unkeepAll(a *App) {
	for _, w := range st.workspaces {
		if w.kept != nil && w.kept.app == a {
			w.unkeep()
		}
	}
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
			for _, p := range t.allPanes() {
				if s := p.status(); s != nil {
					all = append(all, *s)
				}
			}
		}
		return rex.MostUrgent(all, time.Time{})
	}
	var all []rex.ProgramStatus
	st.eachHost(func(h *hostConn) {
		if s := st.hiddenStatus(w, h); s != nil {
			all = append(all, *s)
		}
	})
	return rex.MostUrgent(all, time.Time{})
}

// eachHost calls fn with this machine, nil, then each host.
func (st *store) eachHost(fn func(h *hostConn)) {
	fn(nil)
	for _, h := range st.hosts {
		fn(h)
	}
}

// hiddenStatus is the most urgent status of the sessions of host h, nil
// for this machine, in the layout of a workspace no window shows: from
// the machine's last session list, none while a host is not connected.
func (st *store) hiddenStatus(w *Workspace, h *hostConn) *rex.ProgramStatus {
	infos, id, ok := st.listOf(h)
	if !ok {
		return nil
	}
	var all []rex.ProgramStatus
	st.visitSIDs(w, id, func(sid string) {
		if s := rex.MostUrgent(infos[sid].Statuses, st.seen[sid]); s != nil {
			all = append(all, *s)
		}
	})
	return rex.MostUrgent(all, time.Time{})
}

// listOf is the last session list of host h, nil for this machine, and
// the id the layouts name the host by; not ok while a host is not
// connected, whose list is old.
func (st *store) listOf(h *hostConn) (infos map[string]rex.SessionInfo, id string, ok bool) {
	if h == nil {
		return st.infos, "", true
	}
	if h.state != hostConnected {
		return nil, "", false
	}
	return h.infos, h.ID, true
}

// statusOn is the most urgent status of the workspace's sessions on host
// h, from the window that shows it or, hidden, from the host's last
// session list.
func (st *store) statusOn(w *Workspace, h *hostConn) *rex.ProgramStatus {
	if w.app == nil {
		return st.hiddenStatus(w, h)
	}
	var all []rex.ProgramStatus
	for _, t := range w.app.tabsOn(h) {
		for _, p := range t.allPanes() {
			if s := p.status(); s != nil {
				all = append(all, *s)
			}
		}
	}
	return rex.MostUrgent(all, time.Time{})
}

// needsOn tells whether the workspace's sessions on host h need the user,
// as a folded host's tabs tell: by a pane's attention or status, or,
// hidden, by its sessions' last status (statusOn).
func (st *store) needsOn(w *Workspace, h *hostConn) bool {
	if w.app == nil {
		return needsUser(st.statusOn(w, h))
	}
	for _, t := range w.app.tabsOn(h) {
		if slices.ContainsFunc(t.allPanes(), func(p *Pane) bool { return p.needsYou() > 0 }) {
			return true
		}
	}
	return false
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
	// hostsUp tells that the hosts were read and are connected to;
	// hostsLost that hosts.json would not read.
	hostsUp, hostsLost bool
	// killsTaken are the kills of the server's layout the app took, until
	// the layout no longer has them (takeKills).
	killsTaken map[layout.Kill]bool

	workspaces []*Workspace
	// badge is the count the app icon shows.
	badge int
	// jumped is the pane the jump to the pane that needs you went to last.
	jumped jumpMark
	// infos are this machine's sessions, as the windows last polled them,
	// for the status of workspaces no window shows; seen is when each
	// session's done or error record was seen.
	infos map[string]rex.SessionInfo
	seen  map[string]time.Time
	// shown close the notifications shown of each session, by notifyKey,
	// till its pane is seen; clicked is the ID of the notification the
	// user clicked last that found no pane yet, as of clickedAt.
	shown     map[string][]func()
	clicked   string
	clickedAt time.Time
	// apps are the open windows, the most recently focused first.
	apps []*App
	// lastClosed is the workspace of the last window, once it closed: it
	// opens again with the app, or as the app is activated (macOS).
	lastClosed *Workspace

	started, quitting, debugging bool
	// debugWin is the window, from 1 by place, the debug hook's lines
	// target; 0 for the one focused last.
	debugWin int
	// noAccel are the menu shortcuts a keymap binding takes over, and
	// recording takes every accelerator while the keyboard records.
	noAccel   map[string]bool
	recording bool
	// base is the server's layout, as the app knows it: the one it took
	// last, with the changes it sent since (out). The app sends what it
	// changes of it, and nothing while readOnly, its server one of before
	// the server made the layout's changes (rex.CapabilityLayoutServer).
	base     savedStore
	out      outbox
	readOnly bool
}

// The layout the server keeps, opaque to it: every workspace, and which
// of them the windows showed, in the order of their places.
type savedStore = layout.Store

// savedWorkspace is a workspace as the layout keeps it.
type savedWorkspace = layout.Workspace

// saved is the layout of every workspace, and of the windows.
func (st *store) saved() savedStore {
	s := savedStore{Version: layout.Version, Sessions: []savedWorkspace{}, Windows: st.windowIDs()}
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

// save sends the server what the app changed of the layout since the
// server's (base), as commands (diffLayout), after those it sent before.
func (st *store) save() {
	st.queue()
	st.out.kick()
}

// queue queues what the app changed of the layout, to send.
func (st *store) queue() {
	for _, a := range st.apps {
		a.saveDue, a.lastSave = false, time.Now()
	}
	if st.client == nil {
		return
	}
	now := st.saved()
	if st.readOnly {
		// The server takes no change of the app's: the window shows the
		// server's layout again, and says why.
		if !sameArrangement(now, st.base) {
			for _, a := range st.apps {
				a.err, a.errMsg = "", locales.Messages.ServerLayoutReadOnly
			}
			st.out.refresh()
		}
		return
	}
	if batch := diffLayout(st.base, now); len(batch) > 0 {
		for _, c := range batch {
			if c.TabLayout != nil {
				logFocus(st.base, *c.TabLayout)
			}
		}
		st.out.add(rex.LayoutCommand{Op: "layout.apply", Batch: batch}, nil)
	}
	st.base = now
	// What the browser panes show the server has now.
	for _, a := range st.apps {
		for _, t := range a.tabs {
			for _, p := range t.allPanes() {
				if b := p.browser; b != nil {
					b.synced = b.url
				}
			}
		}
	}
}

// logFocus logs a change of a tab's focus the app sends, for the focus
// that moved by itself, once a second, in a window left alone (P3's e2e).
func logFocus(base savedStore, t layout.Tab) {
	for _, w := range base.Sessions {
		for _, b := range w.Tabs {
			if b.ID == t.ID && (b.Focus != t.Focus || b.FocusFloat != t.FocusFloat) {
				log.Printf("gorex: tab %s focus %d/%d to %d/%d", t.ID, b.Focus, b.FocusFloat, t.Focus, t.FocusFloat)
			}
		}
	}
}

// saveNow saves, and waits a second at most for the server to make it.
func (st *store) saveNow() {
	st.save()
	st.out.wait(time.Second)
}

// send asks the server for a change it makes, as a new session, after the
// app's own changes; done runs on the main thread with its answer, once
// the windows show the server's layout after it.
func (st *store) send(c rex.LayoutCommand, done func(rex.LayoutCommand)) {
	if st.readOnly || st.client == nil {
		if done != nil {
			res := rex.LayoutCommand{Code: "read_only", Error: locales.ServerLayoutReadOnly()}
			if st.client == nil {
				res = rex.LayoutCommand{Code: "failed", Error: "rex: connection to the server lost"}
			}
			done(res)
		}
		return
	}
	st.queue()
	st.out.add(c, done)
	st.out.kick()
}

// use makes c the connection the app sends its changes through, and
// follows the layout of, a server whose revisions are its own; update
// runs a change on the main thread, nil to run it at once, as tests do.
func (st *store) use(c *rex.Client, update func(func())) {
	st.out.use(c, update, st.takeLayout)
}

// follow has the app follow the layout of c's server: on each change, the
// app's own as the server made it and another client's alike, the
// workspaces take it, and the windows show it (takeLayout).
func (st *store) follow(c *rex.Client) {
	if err := c.Subscribe(); err != nil {
		log.Printf("gorex: following the layout: %v", err)
		return
	}
	st.out.follow(c)
	for ev := range c.Events() {
		if ev.Kind == "layout" {
			st.out.tell(c, ev.Revision)
		}
	}
}

// takeLayout shows the server's layout ls: the workspaces take it, and
// the windows show their spaces' tabs of it, keeping the tabs and panes
// they have of it (App.reconcile). It does not while a gesture runs, or
// while the app has changes of its own to send, which go first: false
// then. Call it on the main thread.
func (st *store) takeLayout(ls rex.LayoutState) bool {
	for _, a := range st.apps {
		if a.gesturing() {
			a.saveDue = true // a save once it ends, and a take after it
			return false
		}
	}
	if slices.ContainsFunc(st.apps, func(a *App) bool { return a.saveDue }) {
		st.save()
	}
	// Changes queued since the read go first, built on the tabs as the
	// app knew them: the server refuses one another client's change came
	// before, and the app reads again after them.
	if st.out.pending() {
		return false
	}
	s := layout.Parse(ls.Layout)
	// What the app shows already, as its own change the server made, it
	// need not take again.
	same := sameArrangement(s, st.saved())
	st.base = s
	if !same {
		st.reconcile(s)
	}
	if len(st.apps) > 0 {
		st.takeKills(st.apps[0])
	}
	st.takeShow()
	return true
}

// takeKills ends the sessions of other hosts the server's layout names
// (layout.Store.Kills), as the app ends those its user closes (kill): it
// keeps each to end, saves the hosts once, and ends each host's through
// its client. It tells the server it took those it saved, and those of a
// host the app does not have, whose sessions go on there as a removed
// host's do, unless hosts.json would not read. A kill it took, it takes
// no more while a layout still has it, as an older one may. Call it on
// the main thread, with a window of the store.
func (st *store) takeKills(a *App) {
	kills := st.base.Kills
	for k := range st.killsTaken {
		if !slices.Contains(kills, k) {
			delete(st.killsTaken, k)
		}
	}
	if len(kills) == 0 || !st.hostsUp {
		return
	}
	var taken, kept []layout.Kill
	sids := map[*hostConn][]string{}
	for _, k := range kills {
		switch h := a.hostByID(k.Host); {
		case st.killsTaken[k]:
		case h != nil:
			sids[h] = append(sids[h], k.SID)
			kept = append(kept, k)
		case !st.hostsLost:
			taken = append(taken, k)
		}
	}
	if len(sids) > 0 {
		var run []func()
		for h, ids := range sids {
			if c := h.keepKills(ids); c != nil {
				run = append(run, func() { h.runKills(c) })
			}
		}
		if a.saveHosts() == nil {
			taken = append(taken, kept...)
		}
		for _, f := range run {
			go f()
		}
	}
	if len(taken) == 0 {
		return
	}
	if st.killsTaken == nil {
		st.killsTaken = map[layout.Kill]bool{}
	}
	for _, k := range taken {
		st.killsTaken[k] = true
	}
	st.out.add(rex.LayoutCommand{Op: "kills.taken", Kills: taken}, nil)
	st.out.kick()
}

// takeShow shows the space the server's layout asks a window to show
// (layout.Store.Show), of a client's tab.create with the focus, in the
// window focused last, and tells the server it did (show.taken). With no
// window it waits for one; one asked longer ago than showFor it drops, as
// the user moved on. Call it on the main thread.
func (st *store) takeShow() {
	id := st.base.Show
	if id == "" || len(st.apps) == 0 {
		return
	}
	if w := st.byID(id); w != nil && time.Since(st.base.ShowAt) < showFor {
		st.apps[0].showWorkspace(w)
	}
	st.out.add(rex.LayoutCommand{Op: "show.taken", Space: id}, nil)
	st.out.kick()
}

// showFor is how long a client's request to show a space stands.
const showFor = 30 * time.Second

// reconcile makes the workspaces those of s, in its order, keeping the
// workspace of each id it has: the windows show their spaces' tabs of s
// (App.reconcile), and the spaces s.Windows says, by their places. A
// space s no longer has goes; a window that showed it, or whose space s
// left with no tabs, shows another no window does, or closes.
func (st *store) reconcile(s savedStore) {
	var next, gone []*Workspace
	var homeless []*App
	for _, sw := range s.Sessions {
		w := st.byID(sw.ID)
		if w == nil {
			w = &Workspace{ID: sw.ID}
		}
		w.Name, w.Color = sw.Name, sw.Color
		if a := w.app; a != nil {
			if !a.reconcile(sw.Layout) {
				// Emptied by another client: the space stays, as Rex
				// keeps a session of no windows, and the window goes on.
				a.leave()
				homeless = append(homeless, a)
			}
		} else {
			if w.kept != nil && !sameLayout(w.layout, sw.Layout) {
				w.unkeep()
			}
			w.layout = sw.Layout
		}
		next = append(next, w)
	}
	for _, w := range st.workspaces {
		if !slices.Contains(next, w) {
			gone = append(gone, w)
		}
	}
	st.workspaces = next
	for _, w := range gone {
		if a := w.app; a != nil {
			closePanes(a.leave())
			homeless = append(homeless, a)
		}
		st.remove(w)
	}
	// The windows show the spaces the layout says, by their places.
	apps := slices.Clone(st.apps)
	slices.SortFunc(apps, func(x, y *App) int { return x.slot - y.slot })
	for i, a := range apps {
		if i >= len(s.Windows) {
			break
		}
		if w := st.byID(s.Windows[i]); w != nil && w != a.ws && w.app == nil && a.ws != nil {
			a.park()
			a.show(w)
			homeless = slices.DeleteFunc(homeless, func(o *App) bool { return o == a })
		}
	}
	st.rehome(homeless, false)
}

// rehome shows in each window a space no window shows, with tabs, once
// the space it showed went or emptied; a window with none to show closes,
// or, with fresh, shows a new space.
func (st *store) rehome(apps []*App, fresh bool) {
	for _, a := range apps {
		var w *Workspace
		for _, o := range st.workspaces {
			if o.app == nil && o.tabCount() > 0 {
				w = o
				break
			}
		}
		switch {
		case w != nil:
		case fresh:
			w = st.addWorkspace()
		default:
			if a.win != nil {
				a.win.Close()
			}
			continue
		}
		a.show(w)
	}
}

// sameLayout tells whether two layouts are the same arrangement, as the
// server keeps one (layout.Tab.Arrangement).
func sameLayout(x, y savedLayout) bool {
	if x.Active != y.Active || len(x.Tabs) != len(y.Tabs) {
		return false
	}
	for i := range x.Tabs {
		if !bytes.Equal(x.Tabs[i].Arrangement(), y.Tabs[i].Arrangement()) {
			return false
		}
	}
	return true
}

// load reads the workspaces the server keeps, and returns those of the
// windows open last.
func (st *store) load() []*Workspace {
	raw, err := st.read()
	if err != nil {
		log.Print(err)
	}
	return st.loaded(raw)
}

// read reads the layout the server keeps, as the one the app shows from
// now on.
func (st *store) read() ([]byte, error) {
	ls, err := st.client.LayoutState()
	if err != nil {
		return st.client.Layout() // a server of before revisions
	}
	st.out.took(ls.Revision, ls.Layout)
	st.base = layout.Parse(ls.Layout)
	return ls.Layout, nil
}

// loaded takes the workspaces of layout raw, and returns those of the
// windows open last.
func (st *store) loaded(raw []byte) []*Workspace {
	s := layout.Parse(raw)
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

// reload reads the workspaces the server keeps again, for a window to
// open while none is: the server makes the clients' changes itself
// meanwhile, which the layout the app has does not show, nor must write
// over. One it cannot read leaves the workspaces as they are, and the window
// unopened, as it would save them over the server's.
func (st *store) reload() error {
	raw, err := st.read()
	if err != nil {
		return err
	}
	last := st.lastClosed
	st.loaded(raw)
	st.lastClosed = nil
	if last != nil {
		st.lastClosed = st.byID(last.ID)
	}
	return nil
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
				for _, n := range t.Roots() {
					walk(n)
				}
			}
		}
	}
	return out
}

// addWorkspace makes a workspace of a new name and color, last.
func (st *store) addWorkspace() *Workspace {
	w := &Workspace{ID: layout.NewSpaceID(), Name: layout.SpaceName(st.taken), Color: st.nextSpaceColor()}
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
	w.unkeep()
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
	// A server of before the server made the layout's changes, as one the
	// user did not restart yet on Windows, shows the layout but takes no
	// change of the app's.
	st.readOnly = err == nil && !st.hello.Can(rex.CapabilityLayoutServer)
	switch {
	case err == nil && st.hello.Version != rex.ProtocolVersion:
		st.errMsg = locales.Messages.ErrorServerVersion
	case st.readOnly:
		st.errMsg = locales.Messages.ServerLayoutReadOnly
	}
	update := func(fn func()) { go mygo.RunOnMain(fn) }
	st.use(st.client, update)
	go st.takeCommands(st.client, update)
	go st.follow(st.client)
	// The windows open take the new connection, attached again to their
	// sessions, as the old one closed, and show the layout of the server,
	// which may have changed meanwhile.
	shown := map[*App]*Workspace{}
	for _, a := range st.apps {
		a.client, a.hello, a.errMsg = st.client, st.hello, st.errMsg
		// Kept panes' streams were the old connection's.
		st.unkeepAll(a)
		if w := a.ws; w != nil {
			shown[a] = w
			a.detach()
		}
		if a.win != nil {
			go a.poll(a.win, st.client)
		}
	}
	// A server with no layout, as one that stopped for good, lost it:
	// the windows keep theirs.
	// takeShow acts on the layout read here, never the last connection's.
	read := false
	if len(shown) > 0 {
		if raw, err := st.read(); err != nil {
			log.Printf("gorex: reading the layout: %v", err)
		} else if len(raw) > 0 && string(raw) != "null" {
			st.reconcile(layout.Parse(raw))
			read = true
		}
	}
	// Each window shows its space again; those whose space the server
	// closed meanwhile take another once every other window has its own.
	var homeless []*App
	for _, a := range st.apps {
		if w, ok := shown[a]; ok {
			if slices.Contains(st.workspaces, w) {
				a.show(w)
			} else {
				homeless = append(homeless, a)
			}
		}
	}
	st.rehome(homeless, true)
	if read {
		st.takeShow()
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
	for _, w := range open {
		st.openWindow(w)
	}
	st.takeShow()
	st.save()
}

// reopen opens a window when none is: of the workspace of the last one,
// else of one no window shows, else of a new one. Before the app started,
// the start, which opens one at least, serves it.
func (st *store) reopen() {
	if !st.started || len(st.apps) > 0 || !st.connect() {
		return
	}
	if err := st.reload(); err != nil {
		log.Printf("gorex: reading the layout: %v", err)
		return
	}
	w := st.lastClosed
	if w == nil || !slices.Contains(st.workspaces, w) {
		w = st.hiddenAfter(nil)
	}
	if w == nil {
		w = st.addWorkspace()
	}
	st.openWindow(w)
	st.takeShow()
	st.save()
}

// openNew opens a window of a new workspace, as New Window and New
// Space do without windows; before the app started, the start serves it.
func (st *store) openNew() {
	if st.started && st.connect() {
		if len(st.apps) == 0 {
			if err := st.reload(); err != nil {
				log.Printf("gorex: reading the layout: %v", err)
				return
			}
		}
		st.openWindow(st.addWorkspace())
		st.takeShow()
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
		Vibrancy:             glassVibrancy(),
		Transparent:          glassTransparent(),
		Content:              ui.View(a.view),
	}
	if ownControls {
		// The title bar draws the window's controls (windowControls); the
		// hidden title bar keeps the window's top edge, which resizes it.
		opts.HideWindowButtons = true
	}
	return mygo.NewWindow(opts)
}

// openWindow opens a window that shows w.
func (st *store) openWindow(w *Workspace) *App {
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
	a.show(w)
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
	st.unkeepAll(a)
	a.win = nil
	st.apps = slices.DeleteFunc(st.apps, func(o *App) bool { return o == a })
	if len(st.apps) == 0 && w != nil {
		st.lastClosed = w
	}
	if !st.quitting {
		st.saveNow()
	}
}

// show shows w in the window: the tabs of its layout, attached to the
// sessions the server has. An empty workspace gets a tab
// in the home directory. A space this window showed before shows its kept
// tabs again, as they were; another window's kept tabs close, and the
// space shows from its layout.
func (a *App) show(w *Workspace) {
	if old := a.ws; old != nil && old != w {
		a.park()
	}
	a.ws, w.app = w, a
	if k := w.kept; k != nil && k.app == a {
		w.kept = nil
		a.tabs, a.active, a.focusReq, a.renaming = k.tabs, k.active, nil, nil
		// The panes' infos are from when they left: take the latest, or
		// the next poll reads what happened away as news.
		for _, t := range k.tabs {
			for _, p := range t.allPanes() {
				in, ok := a.st.infos[p.SID]
				if p.host != nil {
					in, ok = p.host.listed(p.SID)
				}
				if ok {
					p.info = in
				}
			}
		}
		if t := a.tab(); t != nil {
			a.focusReq = t.Focus
		}
		a.changed()
		return
	}
	w.unkeep()
	a.tabs, a.active, a.focusReq, a.renaming = nil, 0, nil, nil
	if !a.reconcile(w.layout) {
		home, _ := os.UserHomeDir()
		a.newTabOn(nil, home)
	}
	a.changed()
}

// detach closes the window's terminals, keeping their sessions, and its
// tabs in its workspace's layout.
func (a *App) detach() { closePanes(a.leave()) }

// park takes the window off its workspace as detach does, but keeps its
// tabs live in the workspace, terminals attached and pages loaded, for the
// window to show them again (showTabs).
func (a *App) park() {
	w, active := a.ws, a.active
	tabs := a.leave()
	if w == nil || len(tabs) == 0 {
		closePanes(tabs)
		return
	}
	for _, t := range tabs {
		for _, p := range t.allPanes() {
			p.setFocused(false)
		}
	}
	w.kept = &keptTabs{app: a, tabs: tabs, active: active}
}

// leave takes the window off its workspace, whose layout keeps its tabs,
// and returns them.
func (a *App) leave() []*Tab {
	tabs := a.tabs
	if a.findPane != nil {
		a.closeFind()
	}
	if w := a.ws; w != nil {
		w.layout = a.snapshot()
		w.app = nil
	}
	a.ws, a.tabs, a.active, a.focusReq, a.renaming = nil, nil, 0, nil, nil
	a.wsOpen, a.wsRenaming, a.wsConfirm = false, false, false
	a.paneRenaming, a.paneNameFocus = nil, false
	return tabs
}

// closePanes closes the tabs' terminals and web views, keeping their
// sessions.
func closePanes(tabs []*Tab) {
	for _, t := range tabs {
		for _, p := range t.allPanes() {
			p.end()
			p.browser.destroy()
		}
	}
}

// keeper is the space whose kept tabs hold t, nil for a tab the window
// shows.
func (a *App) keeper(t *Tab) *Workspace {
	if a.st == nil || slices.Contains(a.tabs, t) {
		return nil
	}
	for _, w := range a.st.workspaces {
		if k := w.kept; k != nil && k.app == a && slices.Contains(k.tabs, t) {
			return w
		}
	}
	return nil
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
	a.park()
	a.show(w)
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
		for _, p := range t.allPanes() {
			p.close()
			p.browser.destroy()
			p.note.destroy()
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
	a.show(next)
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
	a.show(next)
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
	for _, p := range t.allPanes() {
		p.end()
		p.browser.destroy()
	}
	a.removeTab(t)
	w := st.addWorkspace()
	w.layout = l
	st.openWindow(w)
	st.save()
}
