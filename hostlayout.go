package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// The window shows the hosts' layouts (docs/MOD-002-projects.md, "The desktop
// window"): each project's tabs, splits, floats and browser panes live in
// the layout of the project's host, this machine's in its own. A host's
// workspaces are the store's as this machine's are (Workspace.host): the
// app takes the host's layout as it changes, and sends what it changes of
// it as the host's commands, through an outbox of the host's.
//
// In the app a pane of a host names it (layout.Node.Host is the host's
// ID), as before; in the host's own layout it names none. hostForm and
// appForm turn one into the other.

// hostSync is the layout of a host as the app follows it.
type hostSync struct {
	// base is the host's layout as the app knows it, in the app's form:
	// the one it took last, with the changes it sent since.
	base savedStore
	out  outbox
	// got tells that base was taken from the host, this run or from the
	// copy kept of it (cached); live that the host's server of the
	// connection of now gave it.
	got, live bool
	// conn is the connection the app follows the layout through
	// (followHost), the host's connection of then.
	conn *rex.Client
	// old tells a host of before projects (v0.38.0 to v0.40.x): its
	// sessions show as tabs with no project, one a session, and nothing
	// the app arranges goes there.
	old bool
}

// hostSyncOf is h's layout as the app follows it, made the first time.
func hostSyncOf(h *hostConn) *hostSync {
	if h.lay == nil {
		h.lay = &hostSync{}
	}
	return h.lay
}

// serverMade tells whether the server of host h, nil for this machine,
// makes the changes of its layout itself, new sessions among them, as the
// app asks: this machine's, and a host's of projects (rex.CapabilityProjects).
func serverMade(h *hostConn) bool {
	return h == nil || h.lay != nil && !h.lay.old && h.lay.conn != nil && h.lay.conn == h.current()
}

// hostForm is s, a host's layout in the app's form, as the host keeps it:
// its panes name no host.
func hostForm(s savedStore, h *hostConn) savedStore {
	out := copyStore(s)
	eachLeaf(&out, func(n *layout.Node) {
		if n.Host == h.ID {
			n.Host = ""
		}
	})
	return out
}

// appForm is s, the layout host h keeps, in the app's form: its sessions
// name it.
func appForm(s savedStore, h *hostConn) savedStore {
	out := copyStore(s)
	eachLeaf(&out, func(n *layout.Node) {
		if n.SID != "" && n.Host == "" {
			n.Host = h.ID
		}
	})
	// What only this machine's own window takes is not a client's.
	out.Teleports, out.Kills, out.Adopt, out.Show = nil, nil, nil, ""
	return out
}

// copyStore is a copy of s that shares no node with it.
func copyStore(s savedStore) savedStore {
	b, _ := json.Marshal(s)
	var out savedStore
	json.Unmarshal(b, &out)
	return out
}

// eachLeaf calls fn with every pane of every tab of s.
func eachLeaf(s *savedStore, fn func(n *layout.Node)) {
	for i := range s.Sessions {
		for _, t := range s.Sessions[i].Tabs {
			for _, r := range t.Roots() {
				for _, n := range layout.Leaves(r) {
					fn(n)
				}
			}
		}
	}
}

// followHost has the app follow the layout of host h's server through c,
// its connection of now: the host's workspaces take it as it changes
// (takeHost). A host of before projects shows its sessions instead
// (takeOld, as its list comes). Call it on the main thread.
func (st *store) followHost(h *hostConn, c *rex.Client, hello rex.Hello, update func(func())) {
	l := hostSyncOf(h)
	l.old, l.conn = !hello.Can(rex.CapabilityProjects), c
	st.retryAdoptions(h)
	l.live = l.old // a host of projects is live once its layout came (takeHost)
	if l.old {
		l.out.use(nil, update, nil)
		return
	}
	l.out.use(c, update, func(ls rex.LayoutState) bool { return st.takeHost(h, c, ls) })
	l.out.follow(c)
}

// lostHost notes that host h's connection c went: the app shows the
// host's workspaces as it last took them, greyed, until it connects
// again. Call it on the main thread.
func (st *store) lostHost(h *hostConn, c *rex.Client) {
	if l := h.lay; l != nil && h.current() != c {
		l.live = false
	}
}

// takeHost shows host h's layout ls, of connection c: the host's
// workspaces take it, and the windows that show one of them its tabs
// (App.reconcile). As takeLayout, it does not while a gesture runs or
// while the app has changes of its own to send first. The tab a window
// shows of a workspace is its own (docs/MOD-002-projects.md): the host's active
// tab moves no window.
func (st *store) takeHost(h *hostConn, c *rex.Client, ls rex.LayoutState) bool {
	if h.current() != c || !slices.Contains(st.hosts, h) {
		return true // of a connection gone, or a host removed
	}
	for _, a := range st.apps {
		if a.gesturing() {
			a.saveDue = true
			return false
		}
	}
	if slices.ContainsFunc(st.apps, func(a *App) bool { return a.saveDue }) {
		st.save()
	}
	l := hostSyncOf(h)
	if l.out.pending() {
		return false
	}
	s := appForm(layout.Parse(ls.Layout), h)
	shown := st.withShownActive(h, s)
	same := l.got && l.live && sameArrangement(shown, st.savedOn(h))
	first := !l.live
	l.base, l.got, l.live = s, true, true
	if first {
		st.sendSpaceRenames(h)
	}
	if !same {
		st.reconcileOn(h, shown)
	} else {
		st.takeFacts(h, s)
	}
	st.cacheHost(h)
	st.sendAdoptions()
	st.redraw()
	return true
}

// withShownActive is s with the active tab of each of its workspaces a
// window shows that window's own, when s has it: a copy.
func (st *store) withShownActive(h *hostConn, s savedStore) savedStore {
	out := copyStore(s)
	for i := range out.Sessions {
		w := st.byID(out.Sessions[i].ID)
		if w == nil || w.host != h || w.app == nil {
			continue
		}
		if t := w.app.tab(); t != nil {
			if j := slices.IndexFunc(out.Sessions[i].Tabs, func(x savedTab) bool { return x.ID == t.UID }); j >= 0 {
				out.Sessions[i].Active = j
			}
		}
	}
	return out
}

// takeFacts takes what the host's layout says of its workspaces that is
// not their arrangement: their space and last use.
func (st *store) takeFacts(h *hostConn, s savedStore) {
	for _, sw := range s.Sessions {
		if w := st.byID(sw.ID); w != nil && w.host == h {
			w.Space, w.Used, w.Dir = sw.Space, sw.Used, sw.Dir
		}
	}
}

// savedOn is the layout of host h's workspaces as the app has them, in
// the app's form; nil, this machine's, is saved.
func (st *store) savedOn(h *hostConn) savedStore {
	if h == nil {
		return st.saved()
	}
	base := hostSyncOf(h).base
	s := savedStore{Version: layout.Version, Sessions: []savedWorkspace{}, Windows: base.Windows, Spaces: base.Spaces}
	for _, w := range st.workspaces {
		if w.host == h {
			s.Sessions = append(s.Sessions, savedWorkspace{ID: w.ID, Name: w.Name, Color: w.Color, Dir: w.Dir, Space: w.Space, Used: w.Used, Layout: w.savedLayout()})
		}
	}
	return s
}

// queueHosts queues what the app changed of each connected host's layout,
// to send, as queue does this machine's. The active tab of a workspace is
// the window's own: the host's stays as it was.
func (st *store) queueHosts() {
	for _, h := range st.hosts {
		l := h.lay
		if l == nil || !l.got || !l.live || l.old || h.current() == nil {
			continue
		}
		now := keepActive(st.savedOn(h), l.base)
		batch := diffLayout(hostForm(l.base, h), hostForm(now, h))
		// A tab with a pane the client may not see, as a note of the
		// host's, the host does not let a client arrange: it stays as
		// the host has it.
		locked := lockedTabs(l.base)
		batch = slices.DeleteFunc(batch, func(c rex.LayoutCommand) bool {
			return c.Op == "windows.set" || c.Op == "tab.set" && c.TabLayout != nil && locked[c.TabLayout.ID]
		})
		if len(batch) > 0 {
			l.out.add(rex.LayoutCommand{Op: "layout.apply", Batch: batch}, nil)
			l.out.kick()
		}
		l.base = now
	}
}

// keepActive is now with the active tab of each workspace base has as
// base has it, by its ID.
func keepActive(now, base savedStore) savedStore {
	for i := range now.Sessions {
		bw := base.Space(now.Sessions[i].ID)
		if bw == nil || bw.Active < 0 || bw.Active >= len(bw.Tabs) {
			continue
		}
		id := bw.Tabs[bw.Active].ID
		if j := slices.IndexFunc(now.Sessions[i].Tabs, func(t savedTab) bool { return t.ID == id }); j >= 0 {
			now.Sessions[i].Active = j
		}
	}
	return now
}

// lockedTabs are the tabs of s, by ID, with a pane the host does not let
// its client see.
func lockedTabs(s savedStore) map[string]bool {
	out := map[string]bool{}
	for _, w := range s.Sessions {
		for _, t := range w.Tabs {
			for _, r := range t.Roots() {
				for _, n := range layout.Leaves(r) {
					if n.Unavailable {
						out[t.ID] = true
					}
				}
			}
		}
	}
	return out
}

// sendOn asks the server of host h, nil for this machine, for a change it
// makes, after the app's own changes; done runs on the main thread with
// its answer, once the windows show the host's layout after it. A host
// not connected answers at once that it is not.
func (st *store) sendOn(h *hostConn, c rex.LayoutCommand, done func(rex.LayoutCommand)) {
	if h == nil {
		st.send(c, done)
		return
	}
	l := h.lay
	if l == nil || l.conn == nil || l.conn != h.current() {
		if done != nil {
			done(rex.LayoutCommand{Code: "failed", Error: "rex: " + h.label() + " is not connected"})
		}
		return
	}
	if l.old {
		// A host of before projects makes the command, of its own
		// layout, but the app follows only its sessions (takeOld).
		cl := h.current()
		update := h.update
		go func() {
			res, err := cl.Command(c)
			if err != nil {
				res = rex.LayoutCommand{Code: "failed", Error: err.Error()}
			}
			if done != nil && update != nil {
				update(func() { done(res) })
			}
		}()
		return
	}
	st.queue()
	l.out.add(c, done)
	l.out.kick()
}

// takeOld shows the sessions of host h, of before projects, as its tabs
// with no project: a tab a session, in the order they were made. Call it
// on the main thread with the host's last list.
func (st *store) takeOld(h *hostConn, byID map[string]rex.SessionInfo) {
	l := hostSyncOf(h)
	if !l.old || !l.live {
		return
	}
	w := savedWorkspace{ID: "old:" + h.ID, Name: layout.NoProjectName, Layout: layout.Layout{Tabs: []savedTab{}}}
	for _, in := range sortedInfos(byID) {
		if in.Exited {
			continue
		}
		w.Tabs = append(w.Tabs, savedTab{ID: "old:" + in.ID, Root: &layout.Node{SID: in.ID, Host: h.ID, Dir: in.Dir}})
	}
	s := savedStore{Version: layout.Version, Sessions: []savedWorkspace{w}}
	shown := st.withShownActive(h, s)
	if l.got && sameArrangement(shown, st.savedOn(h)) {
		return
	}
	l.base, l.got = s, true
	st.reconcileOn(h, shown)
	st.redraw()
}

// hostWorkspaces are host h's workspaces, nil for this machine's, in the
// layout's order.
func (st *store) hostWorkspaces(h *hostConn) []*Workspace {
	var out []*Workspace
	for _, w := range st.workspaces {
		if w.host == h {
			out = append(out, w)
		}
	}
	return out
}

// noProjectOf is host h's workspace of tabs with no project, nil for
// none yet.
func (st *store) noProjectOf(h *hostConn) *Workspace {
	for _, w := range st.workspaces {
		if w.host == h && w.Dir == "" {
			return w
		}
	}
	return nil
}

// The copy kept of each host's layout, to show the host while it is away
// (docs/MOD-002-projects.md, "Offline hosts"): its projects and their tabs as the
// app last took them, shown greyed, never changed or opened.

// hostCachePath is the file of the copies, beside the hosts.
func hostCachePath() string { return filepath.Join(rex.Dir(), "hosts-layout.json") }

// cacheHost keeps the copy of host h's layout as the app took it last.
func (st *store) cacheHost(h *hostConn) {
	l := h.lay
	if l == nil || !l.got || l.old {
		return
	}
	all := readHostCache()
	all[h.ID] = hostForm(l.base, h)
	writeHostCache(all)
}

// forgetHostCache drops the copy of a host removed.
func forgetHostCache(id string) {
	all := readHostCache()
	if _, ok := all[id]; ok {
		delete(all, id)
		writeHostCache(all)
	}
}

func readHostCache() map[string]savedStore {
	all := map[string]savedStore{}
	if b, err := os.ReadFile(hostCachePath()); err == nil {
		if err := json.Unmarshal(b, &all); err != nil {
			log.Printf("gorex: reading the hosts' layouts: %v", err)
		}
	}
	return all
}

func writeHostCache(all map[string]savedStore) {
	b, _ := json.Marshal(all)
	if err := writeFileAtomic(hostCachePath(), b, 0o600); err != nil {
		log.Printf("gorex: keeping the hosts' layouts: %v", err)
	}
}

// loadHostCache shows each host not connected yet from the copy kept of
// its layout: greyed, until it connects.
func (st *store) loadHostCache() {
	all := readHostCache()
	for _, h := range st.hosts {
		s, ok := all[h.ID]
		l := hostSyncOf(h)
		if !ok || l.got {
			continue
		}
		l.base, l.got = appForm(s, h), true
		st.reconcileOn(h, l.base)
	}
}

// offline tells that the workspace is a host's not connected: a copy of
// what it last showed, which nothing changes or opens.
func (w *Workspace) offline() bool {
	return w.host != nil && (w.host.lay == nil || !w.host.lay.live || w.host.current() == nil)
}

// sortedWorkspaces are ws by last use, the latest first, as Codex lists
// projects (docs/MOD-002-projects.md): the same use keeps their order.
func sortedWorkspaces(ws []*Workspace) []*Workspace {
	out := slices.Clone(ws)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Used.After(out[j].Used) })
	return out
}

// workspaceOfTab is host h's workspace with the tab of ID id, nil for
// none.
func (st *store) workspaceOfTab(h *hostConn, id string) *Workspace {
	for _, w := range st.workspaces {
		if w.host != h || id == "" {
			continue
		}
		if slices.ContainsFunc(w.savedLayout().Tabs, func(t savedTab) bool { return t.ID == id }) {
			return w
		}
	}
	return nil
}
