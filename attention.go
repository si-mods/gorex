package main

import (
	"cmp"
	"runtime"
	"slices"
	"time"

	"github.com/egoist/mygo"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
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
	// ws and sid stand for a session of a space no window shows, of host,
	// nil for this machine; it has no pane till a window shows the space.
	ws   *Workspace
	sid  string
	host *hostConn
	// rank is how much it needs the user, and ord its place among all the
	// panes, which holds while the windows come forward: by window place,
	// tab and pane.
	rank, ord int
}

// visitSIDs calls fn with the sessions of the host of id host, "" for this
// machine, in the layout of a space no window shows.
func (st *store) visitSIDs(w *Workspace, host string, fn func(sid string)) {
	for _, t := range w.layout.Tabs {
		for _, r := range t.Roots() {
			for _, n := range layout.Leaves(r) {
				if n.SID != "" && n.Host == host {
					fn(n.SID)
				}
			}
		}
	}
}

// hiddenNeeds calls fn for each session of the spaces no window shows
// that needs the user, by its last status: this machine's, nil, and the
// connected hosts'. Their attention is not known.
func (st *store) hiddenNeeds(fn func(w *Workspace, h *hostConn, sid string, rank int)) {
	for _, w := range st.workspaces {
		if w.app != nil {
			continue
		}
		st.eachHost(func(h *hostConn) {
			infos, id, ok := st.listOf(h)
			if !ok {
				return
			}
			st.visitSIDs(w, id, func(sid string) {
				if s := rex.MostUrgent(infos[sid].Statuses, st.seen[sid]); needsUser(s) {
					fn(w, h, sid, rex.StatusRank(s.State))
				}
			})
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
	st.hiddenNeeds(func(*Workspace, *hostConn, string, int) { n++ })
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
		a.st.hiddenNeeds(func(w *Workspace, h *hostConn, sid string, rank int) {
			out = append(out, needy{ws: w, sid: sid, host: h, rank: rank, ord: n})
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
		// A space no window shows: its session has a pane once it shows,
		// the first in the order of needing. The host is told by its key:
		// a reload of the hosts makes their values anew.
		a.showWorkspace(n.ws)
	find:
		for _, t := range a.tabs {
			for _, p := range t.allPanes() {
				if p.SID == n.sid && hostKey(p.host) == hostKey(n.host) {
					n.app, n.tab, n.pane = a, t, p
					break find
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

// openNeedsPalette opens the palette on what needs the user, as the
// iPhone app's inbox lists it.
func (a *App) openNeedsPalette() {
	a.openPalette()
	a.paletteList = listNeeds
}

// needSection names the section of what needs the user by its rank, as
// needsYou ranks it: a program blocked on them, failed, done, else the
// attention of a bell or of a program that finished out of sight.
func needSection(rank int) string {
	switch rank {
	case rex.StatusRank("blocked"):
		return locales.StatusBlocked()
	case rex.StatusRank("error"):
		return locales.StatusError()
	case rex.StatusRank("done"):
		return locales.StatusDone()
	}
	return locales.PaneNeedsAttention()
}

// needItems are the palette's rows of what needs the user, in the order of
// needing under a header for each rank, with what each program told last.
// The hosts not connected come after them: their panes may need the user
// too, which cannot be told.
func (a *App) needItems(match func(string) bool) []paletteItem {
	list, _ := a.needing(nil)
	var items []paletteItem
	rank := 0
	for _, n := range list {
		it, dir := a.needItem(n)
		if !match(it.title + " " + it.detail + " " + dir + " " + it.aside) {
			continue
		}
		if n.rank != rank {
			rank = n.rank
			items = append(items, paletteItem{title: needSection(rank), header: true})
		}
		items = append(items, it)
	}
	for _, h := range a.hosts {
		// A host of the tailnet not yet opened is not connected to.
		if h.state == hostConnected || rex.IsTailnetTarget(h.Address) && !h.TailnetOpened || !match(h.label()) {
			continue
		}
		label, _, _ := hostStatus(h)
		items = append(items, paletteItem{title: h.label(), detail: label, glyph: "server", disabled: true, id: h})
	}
	return items
}

// needItem is the palette's row of n: the pane's name and what its program
// told, the host or the space it is in when not this machine's or not
// shown, and going there; dir is the detail the pane's name has, which the
// query matches though the program's message shows in its place.
func (a *App) needItem(n needy) (it paletteItem, dir string) {
	p, id := n.pane, any(n.pane)
	if p == nil {
		// A session of a space no window shows has no pane: it is named
		// as its pane would be, from what its machine's list says of it.
		infos, _, _ := a.st.listOf(n.host)
		p, id = &Pane{info: infos[n.sid], host: n.host}, n.sid
		p.seen = a.st.seen[n.sid]
		if n.host != nil {
			id = hostKey(n.host) + "/" + n.sid
		}
	}
	name, dir := p.label()
	s := p.status()
	detail := dir
	if s != nil && s.State != "idle" {
		detail = cmp.Or(s.Message(), s.Title, detail)
	}
	it = paletteItem{title: name, detail: detail, glyph: p.program().Glyph, status: s, id: id}
	switch {
	case p.host != nil && n.ws != nil:
		it.aside = p.host.label() + " · " + n.ws.Name
	case p.host != nil:
		it.aside = p.host.label()
	case n.ws != nil:
		it.aside = n.ws.Name
	}
	it.run = func() {
		if a.focusNeedy(n) != nil {
			a.syncBadge()
		}
	}
	return it, dir
}

// ago says how long before now t was: now under a minute, else the
// duration as the host's panel says it, in minutes at least; and in how
// long it says otherwise.
func ago(t, now time.Time) (string, time.Duration) {
	d := max(now.Sub(t), 0)
	next := time.Minute - d%time.Minute
	if d < time.Minute {
		return locales.AgoNow(), next
	}
	return locales.Ago(roundDur(d)), next
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
	if a.st != nil {
		a.st.syncBadge()
	}
}

// syncBadge is the app's syncBadge, which a host's change calls with no
// window too.
func (st *store) syncBadge() {
	if !badgeSupported {
		return
	}
	n := 0
	if !prefs.HideDockBadge {
		n = st.needCount()
	}
	if n != st.badge {
		st.badge = n
		setBadge(n)
	}
}
