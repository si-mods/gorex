//go:build darwin || linux || windows

package rex

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gorex/internal/layout"
)

// The server's own changes of the layout, which no client asks for: the
// facts of its panes' sessions, the panes of the sessions that ended, the
// sessions no pane has, and those a run before lost.

// refreshWait is how long after a change of the sessions the server
// refreshes the layout's facts (refresh), and forgets a session that
// exited: a client that polls the sessions sees the exit first.
var refreshWait = newDuration(time.Second)

// refreshSoon refreshes the layout refreshWait from now, unless a refresh
// is to come already: the changes of a second, an animating title's
// included, cost one refresh.
func (s *Server) refreshSoon() {
	if s.refreshDue.CompareAndSwap(false, true) {
		time.AfterFunc(refreshWait.get(), func() {
			s.refreshDue.Store(false)
			s.refresh()
		})
	}
}

// refresh stamps the facts of the sessions on the layout (stampAll), from
// what they sampled, in layout.json but not the journal: the revision
// stays, as the layout's arrangement does, and the next change takes them
// along. It changes nothing while the run stops.
func (s *Server) refresh() {
	s.mu.Lock()
	raw, rev := s.layout, s.layoutRev
	s.mu.Unlock()
	st := layout.Parse(raw)
	if !s.stampAll(&st) {
		return
	}
	l := encodeLayout(&st)
	s.mu.Lock()
	if s.layoutRev != rev || s.stopping {
		s.mu.Unlock()
		return // a change came first, with its own facts
	}
	s.layout = l
	s.mu.Unlock()
	s.writeLayout(rev, false)
}

// watchExit takes the pane of ss out of the layout as soon as its program
// ends (ended).
func (s *Server) watchExit(ss *session) {
	go func() {
		<-ss.done
		s.ended(ss.id)
	}()
}

// ended takes the pane of session sid, which ended, out of the layout at
// once, as pane.close does but ending nothing, so that no crash after it
// starts the session again; a teleport from it is over. It changes nothing
// while the run stops, nor while an upgrade hands the sessions on, which
// holds the change off. A session that exited is forgotten refreshWait
// later, once a client polling the sessions saw it exit.
func (s *Server) ended(sid string) {
	a := &applying{s: s}
	pruned := false
	s.change(a, func(st *layout.Store) LayoutCommand {
		s.mu.Lock()
		stopping := s.stopping
		s.mu.Unlock()
		if stopping {
			return LayoutCommand{Code: "stopping"}
		}
		pruned = prune(st, sid, a)
		st.Teleports = slices.DeleteFunc(st.Teleports, func(t layout.Teleport) bool { return t.From == sid })
		return LayoutCommand{}
	})
	if !pruned {
		return
	}
	time.AfterFunc(refreshWait.get(), func() {
		s.mu.Lock()
		ss := s.sessions[sid]
		s.mu.Unlock()
		if ss != nil {
			s.endAll([]string{sid})
		}
	})
}

// prune takes the pane of session sid out of st, as pane.close does, its
// tab with its last pane, ending nothing; false when st has none.
func prune(st *layout.Store, sid string, a *applying) bool {
	p, ok := find(st, "", sid)
	if ok {
		a.touch(p.tab().ID, p.tab())
		if unlink(p, "") {
			removeTab(p.w, p.ti)
		}
	}
	return ok
}

// place gives the session ss, which a remote client started here, a tab
// of its own (placeTab).
func (s *Server) place(ss *session) {
	a := &applying{s: s}
	s.change(a, func(st *layout.Store) LayoutCommand {
		if _, ok := find(st, "", ss.id); !ok {
			placeTab(st, a, ss)
		}
		return LayoutCommand{}
	})
}

// placeOrphans gives each running session of this machine no pane has a
// tab of its own (placeTab): one a client started with no tab, as gorex
// new, or that the layout lost, as layout.json did. The window shows them
// as it opens, as it did before the server placed sessions.
func (s *Server) placeOrphans() {
	a := &applying{s: s}
	s.change(a, func(st *layout.Store) LayoutCommand {
		named := localSIDs(st)
		s.mu.Lock()
		var orphans []*session
		for _, id := range s.order {
			if ss := s.sessions[id]; ss != nil && named[id] == nil {
				orphans = append(orphans, ss)
			}
		}
		s.mu.Unlock()
		for _, ss := range orphans {
			if !ss.hasExited() {
				placeTab(st, a, ss)
			}
		}
		return LayoutCommand{}
	})
}

// hasExited tells a session whose program exited.
func (s *session) hasExited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

// placeTab gives ss a tab as the window would show it: in the default
// space, after this machine's tabs, without the focus.
func placeTab(st *layout.Store, a *applying, ss *session) {
	w := defaultSpace(st)
	if w == nil {
		w = newSpace(st, "")
	}
	ss.mu.Lock()
	cols, rows := ss.cols, ss.rows
	ss.mu.Unlock()
	t := layout.Tab{ID: layout.NewTabID(), Root: &layout.Node{SID: ss.id, Dir: ss.dir, Cols: cols, Rows: rows}}
	a.touch(t.ID, nil)
	insertTab(w, afterLocal(w), t, false)
}

// afterLocal is the place after the last tab of this machine's in w, where
// a new tab goes: this machine's tabs stay first.
func afterLocal(w *layout.Workspace) int {
	at := 0
	for i := range w.Tabs {
		if tabHost(&w.Tabs[i]) == "" {
			at = i + 1
		}
	}
	return at
}

// restoreBatch is how many lost sessions restoreLost starts at a time.
const restoreBatch = 8

// lostPanes are the panes of this machine of the layout whose sessions
// the server does not have, as the run before ended with them, a crash,
// a restart or a stop: restoreLost starts them once the server listens,
// and an attach to one waits for it meanwhile (awaitRestore).
func (s *Server) lostPanes() []layout.Node {
	st := layout.Parse(s.layout)
	// The spaces the windows show first, and in each its active tab first:
	// the panes in sight attach first.
	var spaces []*layout.Workspace
	for _, id := range st.Windows {
		if w := st.Space(id); w != nil {
			spaces = append(spaces, w)
		}
	}
	for i := range st.Sessions {
		if w := &st.Sessions[i]; !slices.Contains(spaces, w) {
			spaces = append(spaces, w)
		}
	}
	var lost []layout.Node
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range spaces {
		for k := range w.Tabs {
			t := w.Tabs[(k+max(w.Active, 0))%len(w.Tabs)]
			for _, r := range t.Roots() {
				for _, n := range layout.Leaves(r) {
					if n.Host != "" || n.SID == "" || s.sessions[n.SID] != nil || s.restoring[n.SID] != nil {
						continue
					}
					if s.restoring == nil {
						s.restoring = map[string]chan struct{}{}
					}
					s.restoring[n.SID] = make(chan struct{})
					lost = append(lost, *n)
				}
			}
		}
	}
	return lost
}

// restoreLost starts the sessions of the lost panes, a batch at a time,
// in the background: the layout is read meanwhile. owners are the
// journal's. The pane of one that does not start goes. Then the sessions
// no pane has get tabs (placeOrphans).
func (s *Server) restoreLost(owners map[string]string, lost []layout.Node) {
	var failed []string
	for i := 0; i < len(lost); i += restoreBatch {
		// One after another, in the order the windows show them: shells
		// started side by side, as consoles on Windows, slow each other
		// down. An attach to one waits for it alone.
		s.ops.RLock()
		for _, n := range lost[i:min(i+restoreBatch, len(lost))] {
			s.restorePane(&n, owners[n.SID])
			s.mu.Lock()
			if s.sessions[n.SID] == nil {
				failed = append(failed, n.SID)
			}
			close(s.restoring[n.SID])
			delete(s.restoring, n.SID)
			s.mu.Unlock()
		}
		s.ops.RUnlock()
	}
	if len(failed) > 0 {
		a := &applying{s: s}
		s.change(a, func(st *layout.Store) LayoutCommand {
			for _, sid := range failed {
				prune(st, sid, a)
			}
			return LayoutCommand{}
		})
	}
	s.placeOrphans()
}

// awaitRestore waits for the session sid when restoreLost is to start it,
// a minute at most, and returns it; nil for none.
func (s *Server) awaitRestore(sid string) *session {
	s.mu.Lock()
	ch := s.restoring[sid]
	s.mu.Unlock()
	if ch != nil {
		select {
		case <-ch:
		case <-time.After(time.Minute):
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[sid]
}

// teleporting makes pane.teleporting: the pane's work goes to the host of
// address Host, as the session Target there, which the layout notes before
// that host starts it (Store.Teleports), so that the app opens no tab of
// its own for it; no Target ends a teleport that failed.
func (s *Server) teleporting(st *layout.Store, c LayoutCommand) LayoutCommand {
	var res LayoutCommand
	st.Teleports = slices.DeleteFunc(st.Teleports, func(t layout.Teleport) bool { return t.From == c.SID })
	if c.Target == "" {
		return res
	}
	host, code, msg := s.hostAt(c.Host)
	if code != "" {
		res.Code, res.Error = code, msg
		return res
	}
	st.Teleports = append(st.Teleports, layout.Teleport{Host: host, SID: c.Target, From: c.SID})
	return res
}

// teleported makes pane.teleported: the work of pane p went to the host of
// address Host, the app's, as the session Target there. A tab of that
// host's after p's tab, active, shows it, named as p was, and p goes as
// closing it does, its session ending. A pane of that session the app
// placed meanwhile, as a host's new session, moves there.
func (s *Server) teleported(st *layout.Store, c LayoutCommand, a *applying) LayoutCommand {
	var res LayoutCommand
	fail := func(code, msg string) LayoutCommand {
		res.Code, res.Error = code, msg
		return res
	}
	if _, ok := find(st, c.Space, c.SID); !ok {
		return fail("pane_not_found", "no such pane in the GoRex layout")
	}
	host, code, msg := s.hostAt(c.Host)
	if code != "" {
		return fail(code, msg)
	}
	if c.Target == "" {
		return fail("bad_args", "no session")
	}
	st.Teleports = slices.DeleteFunc(st.Teleports, func(t layout.Teleport) bool { return t.From == c.SID })
	if q, ok := findLeaf(st, hostLeaf(host, c.Target)); ok {
		a.touch(q.tab().ID, q.tab())
		if unlink(q, "") {
			removeTab(q.w, q.ti)
		}
	}
	p, _ := find(st, c.Space, c.SID)
	t := p.tab()
	leaf := &layout.Node{Host: host, SID: c.Target, Name: p.n.Name, Dir: p.n.Dir, Cols: p.n.Cols, Rows: p.n.Rows}
	nt := layout.Tab{ID: layout.NewTabID(), Root: leaf}
	a.touch(t.ID, t)
	a.end(p.n)
	at := p.ti + 1
	if unlink(p, "") {
		// Its tab goes with its last pane, and the host's takes its name.
		nt.Name = t.Name
		removeTab(p.w, p.ti)
		at--
	}
	a.touch(nt.ID, nil)
	insertTab(p.w, min(at, len(p.w.Tabs)), nt, true)
	res.Tab = nt.ID
	return res
}

// findLeaf returns the first leaf of st that match takes.
func findLeaf(st *layout.Store, match func(*layout.Node) bool) (spot, bool) {
	for i := range st.Sessions {
		if p, ok := findIn(&st.Sessions[i], match); ok {
			return p, true
		}
	}
	return spot{}, false
}

// hostLeaf matches the leaf of host's session sid.
func hostLeaf(host, sid string) func(*layout.Node) bool {
	return func(n *layout.Node) bool { return n.Host == host && n.SID == sid }
}

// HostMachines maps the app's hosts, by the ID the layout names each by
// (layout.Node.Host), to the machine each is (Hello.Machine), from
// hosts.json in dir, which the app keeps and others only read. A host the
// app has not connected to since it kept machines is left out.
func HostMachines(dir string) map[string]string {
	var f struct {
		Hosts []struct {
			ID      string `json:"id"`
			Machine string `json:"machine"`
		} `json:"hosts"`
	}
	b, _ := os.ReadFile(filepath.Join(dir, "hosts.json"))
	json.Unmarshal(b, &f)
	out := map[string]string{}
	for _, h := range f.Hosts {
		if h.ID != "" && h.Machine != "" {
			out[h.ID] = h.Machine
		}
	}
	return out
}

// hostAt is the ID of the app's host of address addr, from hosts.json,
// which the app keeps and the server only reads; an error code and its
// message when it has none, or more than one.
func (s *Server) hostAt(addr string) (id, code, msg string) {
	var f struct {
		Hosts []struct {
			ID      string `json:"id"`
			Address string `json:"address"`
		} `json:"hosts"`
	}
	b, _ := os.ReadFile(filepath.Join(cmp.Or(s.dir, Dir()), "hosts.json"))
	json.Unmarshal(b, &f)
	var found []string
	for _, h := range f.Hosts {
		if addr = strings.TrimSpace(addr); addr != "" && h.ID != "" && strings.TrimSpace(h.Address) == addr {
			found = append(found, h.ID)
		}
	}
	switch len(found) {
	case 0:
		return "", "host_not_found", "no such host of the GoRex window's"
	case 1:
		return found[0], "", ""
	}
	return "", "host_ambiguous", "more than one host of the GoRex window has that address"
}
