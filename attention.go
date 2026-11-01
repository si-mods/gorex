package main

import (
	"slices"

	"github.com/egoist/mygo"

	"gorex/internal/rex"
)

// needsYou ranks how much a pane asks for the user, 0 for not at all: a
// program blocked on them first, then a done or error record they did not
// see, last the attention of a bell or of a program that finished out of
// sight. It is the one predicate of both the jump to the pane that needs
// you and the Dock badge.
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
	// ws and sid stand for a session of a space no window shows; it has
	// no pane till a window shows the space.
	ws   *Workspace
	sid  string
	rank int
}

// needing lists what needs the user, across the windows' tabs, which hold
// the panes of this machine and of the hosts, and the spaces no window
// shows: the most urgent first, those alike in the order of the windows
// (the one focused last first), their tabs and panes.
func (a *App) needing() []needy {
	var out []needy
	for _, o := range a.peers() {
		for _, t := range o.tabs {
			for _, p := range t.allPanes() {
				if r := p.needsYou(); r > 0 {
					out = append(out, needy{app: o, tab: t, pane: p, rank: r})
				}
			}
		}
	}
	if a.st != nil {
		for _, w := range a.st.workspaces {
			if w.app != nil || w.hidden() {
				continue
			}
			var sids []string
			for _, t := range w.layout.Tabs {
				for _, n := range t.Roots() {
					savedSIDs(n, &sids)
				}
			}
			for _, sid := range sids {
				if s := rex.MostUrgent(a.st.infos[sid].Statuses, a.st.seen[sid]); needsUser(s) {
					out = append(out, needy{ws: w, sid: sid, rank: rex.StatusRank(s.State)})
				}
			}
		}
	}
	slices.SortStableFunc(out, func(x, y needy) int { return y.rank - x.rank })
	return out
}

// jumpToNeedy focuses the pane that most needs the user, other than the
// focused one: in this window, in another that shows its space, or in this
// one after showing its space. Pressed again it goes on to the next, and
// round to the first. It
// does nothing when no other pane asks.
func (a *App) jumpToNeedy() {
	var focused *Pane
	if t := a.tab(); t != nil {
		focused = t.Focus
	}
	list := a.needing()
	// Start after the focused pane when it is in the list, and wrap
	// around, so that presses visit each in turn; else start at the top.
	start := slices.IndexFunc(list, func(n needy) bool { return n.pane != nil && n.pane == focused }) + 1
	for k := range list {
		n := list[(start+k)%len(list)]
		if n.pane != nil && n.pane == focused {
			continue
		}
		if n.pane == nil {
			a.showWorkspace(n.ws)
			for _, t := range a.tabs {
				for _, p := range t.allPanes() {
					if p.SID == n.sid && p.host == nil {
						n.app, n.tab, n.pane = a, t, p
					}
				}
			}
			if n.pane == nil {
				return
			}
		}
		o := n.app
		if o != a {
			o.front()
		}
		if i := slices.Index(o.tabs, n.tab); i >= 0 {
			o.selectTab(i)
			o.revealPane(n.pane)
			n.tab.setFocus(n.pane)
			o.focusReq = n.pane
			o.do(func() {})
		}
		return
	}
}

// badgeCount is how many panes need the user, what the Dock icon shows,
// 0 when the setting is off.
func (a *App) badgeCount() int {
	if prefs.HideDockBadge {
		return 0
	}
	return len(a.needing())
}

// setBadge shows a count on the application icon; tests, which run no
// app, take its place.
var setBadge = func(n int) { mygo.App.SetBadgeCount(n) }

// syncBadge shows the count on the icon when it changed. Call it on the
// main thread, as the windows draw.
func (a *App) syncBadge() {
	if a.st == nil {
		return
	}
	if n := a.badgeCount(); n != a.st.badge {
		a.st.badge = n
		setBadge(n)
	}
}
