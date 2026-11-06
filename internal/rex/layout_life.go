//go:build darwin || linux || windows

package rex

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorex/internal/layout"
)

// The server's own changes of the layout, which no client asks for: the
// facts of its panes' sessions, the panes of the sessions that ended, the
// sessions a remote client starts here, and those a run before lost.

// refreshWait is how long after a change of the sessions the server
// refreshes the layout (refresh): the changes of a second go together,
// and a client that polls the sessions sees one exit before its pane goes.
var refreshWait = newDuration(time.Second)

// refreshSoon refreshes the layout refreshWait from now, unless a refresh
// is to come already.
func (s *Server) refreshSoon() {
	if s.refreshDue.CompareAndSwap(false, true) {
		time.AfterFunc(refreshWait.get(), func() {
			s.refreshDue.Store(false)
			s.refresh()
		})
	}
}

// refresh takes out the panes of this machine's sessions that exited or
// ended, as pane.close does but ending nothing, and forgets the sessions
// that exited; then it stamps the facts of the others, which counts no
// revision. It changes nothing while the run stops, nor while an upgrade
// hands the sessions on, which holds the changes off.
func (s *Server) refresh() {
	s.mu.Lock()
	gone := s.gone
	s.gone = nil
	s.mu.Unlock()
	var exited []string
	a := &applying{s: s}
	s.change(a, func(st *layout.Store) LayoutCommand {
		exited = nil
		s.mu.Lock()
		stopping := s.stopping
		s.mu.Unlock()
		if stopping {
			return LayoutCommand{Code: "stopping"}
		}
		for sid := range localSIDs(st) {
			s.mu.Lock()
			ss := s.sessions[sid]
			s.mu.Unlock()
			switch {
			case ss == nil && gone[sid]:
			case ss != nil && ss.hasExited():
				exited = append(exited, sid)
			default:
				continue
			}
			prune(st, sid, a)
		}
		return LayoutCommand{}
	})
	s.endAll(exited)
	s.restamp()
}

// hasExited tells a session whose program exited.
func (s *session) hasExited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

// prune takes the pane of session sid out of st, as pane.close does, its
// tab with its last pane, ending nothing.
func prune(st *layout.Store, sid string, a *applying) {
	if p, ok := find(st, "", sid); ok {
		a.touch(p.tab().ID, p.tab())
		if unlink(p, "") {
			removeTab(p.w, p.ti)
		}
	}
}

// restamp stamps the facts of the sessions on the layout (stampAll), in
// layout.json but not the journal: the revision stays, as the layout's
// arrangement does, and the next change takes them along.
func (s *Server) restamp() {
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

// place gives the session ss, which a remote client started here, a tab
// of its own, as the window would show it: in the default space, after
// this machine's tabs, without the focus.
func (s *Server) place(ss *session) {
	a := &applying{s: s}
	s.change(a, func(st *layout.Store) LayoutCommand {
		if _, ok := find(st, "", ss.id); ok {
			return LayoutCommand{}
		}
		w := defaultSpace(st)
		if w == nil {
			w = newSpace(st, "")
		}
		in := ss.info()
		t := layout.Tab{ID: layout.NewTabID(), Root: &layout.Node{SID: ss.id, Dir: ss.dir, Cols: in.Cols, Rows: in.Rows}}
		a.touch(t.ID, nil)
		insertTab(w, afterLocal(w), t, false)
		return LayoutCommand{}
	})
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

// restoreLost starts again the sessions the layout's panes of this machine
// name that the server does not have, as the run before lost them, past
// the restoreMost it started before it listened, or as it stopped; owners
// are the journal's. The pane of one that does not start goes. Clients
// read the layout once it is done (settled), and close restored then.
func (s *Server) restoreLost(owners map[string]string) {
	defer close(s.restored)
	a := &applying{s: s}
	s.change(a, func(st *layout.Store) LayoutCommand {
		var lost []string
		for _, w := range st.Sessions {
			for _, t := range w.Tabs {
				for _, r := range t.Roots() {
					for _, n := range layout.Leaves(r) {
						if n.Host != "" || n.SID == "" {
							continue
						}
						s.restorePane(n, owners[n.SID], false)
						s.mu.Lock()
						_, live := s.sessions[n.SID]
						s.mu.Unlock()
						if !live {
							lost = append(lost, n.SID)
						}
					}
				}
			}
		}
		for _, sid := range lost {
			prune(st, sid, a)
		}
		return LayoutCommand{}
	})
}

// teleported makes pane.teleported: the work of pane p went to the host of
// address Host, the app's, as the session Target there. A tab of that
// host's after p's tab, active, shows it, named as p was, and p goes as
// closing it does, its session ending. A pane of that session the app
// placed meanwhile, as a host's new session, moves there.
func (s *Server) teleported(st *layout.Store, c LayoutCommand, a *applying) LayoutCommand {
	var res LayoutCommand
	host, code, msg := s.hostAt(c.Host)
	if code == "" && c.Target == "" {
		code, msg = "bad_args", "no session"
	}
	if code != "" {
		res.Code, res.Error = code, msg
		return res
	}
	if q, ok := findHost(st, host, c.Target); ok {
		a.touch(q.tab().ID, q.tab())
		if unlink(q, "") {
			removeTab(q.w, q.ti)
		}
	}
	p, ok := find(st, c.Space, c.SID)
	if !ok {
		res.Code, res.Error = "pane_not_found", "no such pane in the GoRex layout"
		return res
	}
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

// findHost returns the pane of host's session sid in st; ok is false for
// none.
func findHost(st *layout.Store, host, sid string) (spot, bool) {
	for i := range st.Sessions {
		w := &st.Sessions[i]
		for ti := range w.Tabs {
			t := &w.Tabs[ti]
			for fi := -1; fi < len(t.Floats); fi++ {
				r := t.Root
				if fi >= 0 {
					r = t.Floats[fi].Pane
				}
				for _, l := range layout.Leaves(r) {
					if l.Host == host && l.SID == sid {
						return spot{w, ti, l, fi}, true
					}
				}
			}
		}
	}
	return spot{}, false
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
