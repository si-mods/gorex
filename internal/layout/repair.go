package layout

import (
	"slices"
	"time"
)

// RepairHostPanes takes the panes of other hosts out of st, a layout of
// Version, as a layout of before projects, or a client of one, leaves
// them (docs/MOD-002-projects.md, "The desktop window": this machine's layout
// keeps no pane of another host): each goes into an Adoption for its
// host, of its tab's part of the split, as Migrate makes them, and a tab
// left with no pane goes, as a space left with no tab that was no
// project: its name joins Spaces, as an old space's does.
//
// n counts the panes it took out; 0, and st unchanged, for a layout of
// before projects, which Migrate makes, and one that keeps this machine's
// panes only.
func RepairHostPanes(st Store, machineOf func(host string) string) (Store, int) {
	if st.Version < Version {
		return st, 0
	}
	taken := 0
	for i := range st.Sessions {
		taken += hostPanes(&st.Sessions[i].Layout)
	}
	if taken == 0 {
		return st, 0
	}
	m := migration{machineOf: machineOf}
	var spaces []string
	gone := map[string]bool{}
	sessions := make([]Workspace, 0, len(st.Sessions))
	for _, w := range st.Sessions {
		if hostPanes(&w.Layout) == 0 {
			sessions = append(sessions, w)
			continue
		}
		tabs := make([]Tab, 0, len(w.Tabs))
		active := w.Active
		for ti, t := range w.Tabs {
			if hostPanesIn(&t) == 0 {
				if ti == w.Active {
					active = len(tabs)
				}
				tabs = append(tabs, t)
				continue
			}
			local := m.split(t, "", nil)
			if local == nil {
				continue
			}
			if ti == w.Active {
				active = len(tabs)
			}
			tabs = append(tabs, *local)
		}
		if len(tabs) == 0 && w.Dir == "" {
			// An old space, of no project: it goes, and its name stays
			// one the picker offers, as Migrate leaves such a space. The
			// workspace of tabs with no project is no space, and a client
			// of before projects keeps its ID: it goes unnamed.
			if w.Name != NoProjectName {
				spaces = addName(spaces, w.Name)
			}
			gone[w.ID] = true
			continue
		}
		w.Tabs = tabs
		w.Active = min(max(active, 0), max(len(tabs)-1, 0))
		sessions = append(sessions, w)
	}
	if len(m.list) == 0 {
		return st, 0
	}
	st.Sessions = sessions
	for _, name := range spaces {
		st.Spaces = addName(st.Spaces, name)
	}
	if len(gone) > 0 {
		st.Windows = slices.DeleteFunc(st.Windows, func(id string) bool { return gone[id] })
		if gone[st.Show] {
			st.Show, st.ShowAt = "", time.Time{}
		}
	}
	st.Adopt = append(st.Adopt, m.list...)
	return st, taken
}

// hostPanes counts the panes of other hosts in s' tabs.
func hostPanes(s *Layout) int {
	n := 0
	for i := range s.Tabs {
		n += hostPanesIn(&s.Tabs[i])
	}
	return n
}

// hostPanesIn counts the panes of other hosts in tab t, tiled and
// floating.
func hostPanesIn(t *Tab) int {
	n := 0
	for _, r := range t.Roots() {
		for _, l := range Leaves(r) {
			if l.Host != "" {
				n++
			}
		}
	}
	return n
}
