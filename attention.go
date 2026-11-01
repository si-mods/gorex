package main

import (
	"runtime"
	"slices"

	"github.com/egoist/mygo"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// needsYou ranks how much a pane asks for the user, 0 for not at all: a
// program blocked on them first, then a done or error record they did not
// see, last the attention of a bell or of a program that finished out of
// sight. It is the one predicate of the jump to the pane that needs you,
// the Dock badge and the palette's order.
func needsYou(attention bool, s *rex.ProgramStatus) int {
	if needsUser(s) {
		return rex.StatusRank(s.State)
	}
	if attention {
		return 1
	}
	return 0
}

// needsYou ranks the pane, see needsYou.
func (p *Pane) needsYou() int { return needsYou(p.attention, p.status()) }

// needy is a pane, or a session no window shows, that needs the user.
type needy struct {
	app  *App
	tab  *Tab
	pane *Pane
	// ws and sid stand for a local session of a space no window shows; it
	// has no pane till a window shows the space.
	ws  *Workspace
	sid string
	// rank is how much it needs the user, and ord its place among all the
	// panes, which holds while the windows come forward: by window place,
	// tab and pane.
	rank, ord int
}

// visitLocalSIDs calls fn with the sessions of this machine in the layout
// of a space no window shows. Those of a host are left out: their ids are
// the host's own, and this machine's list knows nothing of them.
func (st *store) visitLocalSIDs(w *Workspace, fn func(sid string)) {
	var walk func(n *layout.Node)
	walk = func(n *layout.Node) {
		if n == nil {
			return
		}
		if n.SID != "" && n.Host == "" {
			fn(n.SID)
		}
		walk(n.A)
		walk(n.B)
	}
	for _, t := range w.layout.Tabs {
		for _, n := range t.Roots() {
			walk(n)
		}
	}
}

// hiddenNeeds calls fn for each local session of the spaces no window
// shows that needs the user, by its last status. Their attention is not
// known, and a host's sessions there are not counted.
func (st *store) hiddenNeeds(fn func(w *Workspace, sid string, rank int)) {
	for _, w := range st.workspaces {
		if w.app != nil || w.hidden() {
			continue
		}
		st.visitLocalSIDs(w, func(sid string) {
			if s := rex.MostUrgent(st.infos[sid].Statuses, st.seen[sid]); needsUser(s) {
				fn(w, sid, rex.StatusRank(s.State))
			}
		})
	}
}

// needCount is how many panes need the user, across the windows.
func (st *store) needCount() int {
	n := 0
	for _, o := range st.apps {
		for _, t := range o.tabs {
			for _, p := range t.allPanes() {
				if p.needsYou() > 0 {
					n++
				}
			}
		}
	}
	st.hiddenNeeds(func(*Workspace, string, int) { n++ })
	return n
}

// needing lists what needs the user, across the windows' tabs, which hold
// the panes of this machine and of the hosts, and the spaces no window
// shows: the most urgent first, those alike in a stable order, the
// windows' places, then tabs and panes. ord is the place of mark among all
// the panes, -1 when it is none.
func (a *App) needing(mark *Pane) (out []needy, ord int) {
	apps := slices.Clone(a.peers())
	slices.SortFunc(apps, func(x, y *App) int { return x.slot - y.slot })
	n, ord := 0, -1
	for _, o := range apps {
		for _, t := range o.tabs {
			for _, p := range t.allPanes() {
				if p == mark {
					ord = n
				}
				if r := p.needsYou(); r > 0 {
					out = append(out, needy{app: o, tab: t, pane: p, rank: r, ord: n})
				}
				n++
			}
		}
	}
	if a.st != nil {
		a.st.hiddenNeeds(func(w *Workspace, sid string, rank int) {
			out = append(out, needy{ws: w, sid: sid, rank: rank, ord: n})
			n++
		})
	}
	slices.SortStableFunc(out, func(x, y needy) int { return y.rank - x.rank })
	return out, ord
}

// jumpMark is the pane the jump went to last, and how much it needed the
// user then.
type jumpMark struct {
	pane *Pane
	rank int
}

// jumpToNeedy focuses the pane that most needs the user, other than the
// focused one: in this window, in another that shows its space, or in this
// one after showing its space. Pressed again from the pane it went to, it
// goes on to the next in the order, and round to the first, though the
// pane left the list meanwhile (a done record seen). It does nothing when
// no other pane asks.
func (a *App) jumpToNeedy() {
	var focused *Pane
	if t := a.tab(); t != nil {
		focused = t.Focus
	}
	var mark jumpMark
	if a.st != nil {
		mark = a.st.jumped
	}
	if mark.pane != focused {
		mark = jumpMark{}
	}
	list, markOrd := a.needing(mark.pane)
	start := 0
	if mark.pane != nil && markOrd >= 0 {
		start = slices.IndexFunc(list, func(n needy) bool {
			return n.rank < mark.rank || n.rank == mark.rank && n.ord > markOrd
		})
		start = max(start, 0)
	}
	for k := range list {
		n := list[(start+k)%len(list)]
		if n.pane != nil && n.pane == focused {
			continue
		}
		if p := a.focusNeedy(n); p != nil {
			if a.st != nil {
				a.st.jumped = jumpMark{pane: p, rank: n.rank}
			}
			a.syncBadge()
			return
		}
	}
}

// focusNeedy gives the focus to the pane of n, showing its window or its
// space first, and returns it, nil when it could not.
func (a *App) focusNeedy(n needy) *Pane {
	if n.pane == nil {
		// A space no window shows: its session has a pane once it shows.
		a.showWorkspace(n.ws)
		for _, t := range a.tabs {
			for _, p := range t.allPanes() {
				if p.SID == n.sid && p.host == nil {
					n.app, n.tab, n.pane = a, t, p
				}
			}
		}
		if n.pane == nil {
			return nil
		}
	}
	o, t, p := n.app, n.tab, n.pane
	i := slices.Index(o.tabs, t)
	if i < 0 {
		return nil
	}
	if o != a {
		o.front()
	}
	// A zoomed tab draws one pane: zoom onto the target, or give the
	// panes their room back for a float.
	if t.Zoom != nil && t.Zoom != p {
		if p.float == nil {
			t.Zoom = p
		} else {
			t.Zoom = nil
		}
	}
	o.revealPane(p)
	// The focus moves first, so that showing the tab does not mark the
	// pane it had as seen.
	t.setFocus(p)
	o.selectTab(i)
	o.focusReq = p
	o.do(func() {})
	return p
}

// badgeSupported tells that the platform shows a count on the application
// icon: the Dock of macOS. Tests set it.
var badgeSupported = runtime.GOOS == "darwin"

// setBadge shows a count on the application icon; tests, which run no
// app, take its place.
var setBadge = func(n int) { mygo.App.SetBadgeCount(n) }

// syncBadge shows how many panes need the user on the application icon,
// none when the setting is off, once it changed. Call it on the main
// thread where the statuses, the attention or the seen change.
func (a *App) syncBadge() {
	if a.st == nil || !badgeSupported {
		return
	}
	n := 0
	if !prefs.HideDockBadge {
		n = a.st.needCount()
	}
	if n != a.st.badge {
		a.st.badge = n
		setBadge(n)
	}
}
