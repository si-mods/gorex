package main

import (
	"maps"
	"slices"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// Names given here to what runs on a host go to the host too, so that its
// other clients, Rex Go, the web and its own window, show them: a pane's
// by its session (pane.rename), a tab's only when it is plainly one tab of
// the host's (hostTabOf).

// renameKey names what a rename for a host is of: a pane by its session,
// or a tab by its lasting ID in the host's layout.
type renameKey struct {
	tab bool
	id  string
}

// setPaneName gives pane p the name the user gave it, "" to follow its
// program again, here and, for a pane of a host, on the host. Call it on
// the main thread.
func (a *App) setPaneName(p *Pane, name string) {
	if p.Name == name {
		return
	}
	p.Name = name
	a.changed()
	if p.host != nil && p.SID != "" {
		p.host.rename(renameKey{id: p.SID}, name)
	}
}

// setTabName gives tab t the name the user gave it, "" to follow its
// panes again, here and, when it is one tab of a host's (hostTabOf), on
// the host. Call it on the main thread.
func (a *App) setTabName(t *Tab, name string) {
	if t.Name == name {
		return
	}
	t.Name = name
	a.changed()
	if h, id := hostTabOf(t); h != nil {
		h.rename(renameKey{tab: true, id: id}, name)
	}
}

// hostTabOf returns the host of tab t and the ID of its tab there when the
// two are plainly one: every pane of t is of that host, and they are the
// panes of the one tab of the host's layout, as last read, no more and no
// fewer. Else, as for a tab that mixes hosts or holds a part of a tab of
// the host's, or panes of two, it returns nil: its name stays here.
func hostTabOf(t *Tab) (*hostConn, string) {
	ps := t.allPanes()
	if len(ps) == 0 {
		return nil, ""
	}
	h := ps[0].host
	if h == nil {
		return nil, ""
	}
	sids := map[string]bool{}
	for _, p := range ps {
		if p.host != h || p.SID == "" {
			return nil, ""
		}
		sids[p.SID] = true
	}
	for _, st := range h.layoutTabs {
		var there []string
		for _, r := range st.Roots() {
			for _, n := range layout.Leaves(r) {
				if n.Host == "" && n.SID != "" {
					there = append(there, n.SID)
				}
			}
		}
		if st.ID == "" || !slices.ContainsFunc(there, func(sid string) bool { return sids[sid] }) {
			continue
		}
		if len(there) != len(sids) || slices.ContainsFunc(there, func(sid string) bool { return !sids[sid] }) {
			return nil, ""
		}
		return h, st.ID
	}
	return nil, ""
}

// rename keeps the name of k to give it on the host until the host took
// it, and gives it through the client of the moment, or the next one
// (runHost). Call it on the main thread.
func (h *hostConn) rename(k renameKey, name string) {
	h.mu.Lock()
	if h.renames == nil {
		h.renames = map[renameKey]string{}
	}
	h.renames[k] = name
	c := h.client
	h.mu.Unlock()
	if c != nil {
		go h.runRenames(c)
	}
}

// renaming tells whether a name of k waits to go to the host: the host's
// layout has the name before it, which the names here keep
// (syncHostNames).
func (h *hostConn) renaming(k renameKey) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.renames[k]
	return ok
}

// runRenames gives the host, through c, the names kept for it, one run at
// a time so that the last name of a pane goes last, and forgets those the
// host answered: a pane or tab it no longer has keeps its name here only.
// Those it could not reach stay for the next connection.
func (h *hostConn) runRenames(c *rex.Client) {
	h.renameMu.Lock()
	defer h.renameMu.Unlock()
	h.mu.Lock()
	renames := maps.Clone(h.renames)
	h.mu.Unlock()
	for k, name := range renames {
		cmd := rex.LayoutCommand{Op: "pane.rename", SID: k.id, Name: name}
		if k.tab {
			cmd = rex.LayoutCommand{Op: "tab.rename", Tab: k.id, Name: name}
		}
		if _, err := c.Command(cmd); err != nil {
			continue
		}
		h.mu.Lock()
		if n, ok := h.renames[k]; ok && n == name {
			delete(h.renames, k)
		}
		h.mu.Unlock()
	}
}
