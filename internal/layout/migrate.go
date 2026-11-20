package layout

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// SameDir tells whether two folders of this host are one: equal once
// cleaned, without regard to case on Windows and macOS, whose file
// systems are so by default.
func SameDir(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Migrate reads raw, a layout the server keeps, and makes one of version 2
// (v0.38.0 to v0.40.x) one of Version, as docs/MOD-002-projects.md's Migration
// says; changed tells it did. It starts and ends nothing: every pane of
// this host stays in a tab, with its split as far as one host holds it.
//
//   - A project of this machine (a Dir, no home host) stays a project, in
//     space Default; two of one folder become one, with the tabs of both.
//   - An old space (no Dir) gives its tabs to the tabs with no project,
//     and its name to Spaces, which the clients offer.
//   - The panes of other hosts go from the tabs, into an Adoption for
//     each host and project: the project, when the workspace was homed on
//     that host, and each tab's part of the split that ran there. A tab
//     left with no pane goes; what a project homed elsewhere keeps of
//     this machine goes to the tabs with no project.
//   - Teleports and Kills, of other hosts' panes, go.
//
// machineOf is the machine (rex.MachineID) of a host by its ID in
// hosts.json, "" when unknown.
func Migrate(raw []byte, machineOf func(host string) string) (st Store, changed bool) {
	st = Parse(raw)
	if st.Version >= Version {
		return st, false
	}
	// The home hosts of the workspaces, which Workspace no longer has.
	var old struct {
		Sessions []struct {
			ID   string `json:"id"`
			Host string `json:"host"`
		} `json:"sessions"`
	}
	json.Unmarshal(raw, &old)
	homes := map[string]string{}
	for _, w := range old.Sessions {
		if _, ok := homes[w.ID]; !ok {
			homes[w.ID] = w.Host
		}
	}
	m := migration{machineOf: machineOf}
	out := Store{Version: Version, Show: st.Show, ShowAt: st.ShowAt}
	moved := map[string]string{} // old workspace ID → the one it went to
	var noProject *Workspace
	noProjectAt := -1
	projectAt := map[string]int{} // a project's ID → its place in out
	var projects []*Workspace
	// The tabs with no project take the ID of the first old space, which
	// Rex's apps of before keep as a session; a new one with none.
	firstSpace := ""
	for _, w := range st.Sessions {
		if w.Dir == "" && homes[w.ID] == "" {
			firstSpace = w.ID
			break
		}
	}
	if firstSpace == "" {
		firstSpace = NewSpaceID()
	}
	// The active tab of a workspace that several became is that of the
	// one a window showed first (rank), else of the first.
	rank := func(id string, at int) int {
		if i := slices.Index(st.Windows, id); i >= 0 {
			return i
		}
		return len(st.Windows) + at
	}
	ranked := map[*Workspace]int{}
	for at, w := range st.Sessions {
		home := homes[w.ID]
		var to *Workspace
		switch {
		case w.Dir != "" && home == "":
			for _, p := range projects {
				if SameDir(p.Dir, w.Dir) {
					to = p
				}
			}
			if to == nil {
				p := Workspace{ID: w.ID, Name: w.Name, Color: w.Color, Dir: w.Dir, Layout: Layout{Active: -1, Tabs: []Tab{}}}
				projectAt[p.ID] = len(out.Sessions)
				out.Sessions = append(out.Sessions, p)
				to = &p
				projects = append(projects, to)
			}
		default:
			if w.Dir == "" && home == "" {
				out.Spaces = addName(out.Spaces, w.Name)
			}
			if noProject == nil {
				noProject = &Workspace{ID: firstSpace, Name: NoProjectName, Layout: Layout{Tabs: []Tab{}}}
				noProjectAt = len(out.Sessions)
				out.Sessions = append(out.Sessions, Workspace{})
			}
			to = noProject
		}
		moved[w.ID] = to.ID
		var project *AdoptProject
		if w.Dir != "" && home != "" {
			project = &AdoptProject{Name: w.Name, Dir: w.Dir}
		}
		for ti, t := range w.Tabs {
			local := m.split(t, home, project)
			if local == nil {
				continue
			}
			if r, ok := ranked[to]; ti == w.Active && (!ok || rank(w.ID, at) < r) {
				ranked[to] = rank(w.ID, at)
				to.Active = len(to.Tabs)
			}
			to.Tabs = append(to.Tabs, *local)
		}
	}
	// The projects were appended by value: write the tabs back.
	for _, p := range projects {
		p.Active = min(max(p.Active, 0), max(len(p.Tabs)-1, 0))
		out.Sessions[projectAt[p.ID]] = *p
	}
	if noProject != nil {
		if len(noProject.Tabs) == 0 {
			// No tab of this machine's was left with no project: no
			// workspace of none.
			out.Sessions = slices.Delete(out.Sessions, noProjectAt, noProjectAt+1)
			for id, to := range moved {
				if to == noProject.ID {
					delete(moved, id)
				}
			}
		} else {
			noProject.Active = min(max(noProject.Active, 0), len(noProject.Tabs)-1)
			out.Sessions[noProjectAt] = *noProject
		}
	}
	for _, id := range st.Windows {
		if to, ok := moved[id]; ok && !slices.Contains(out.Windows, to) {
			out.Windows = append(out.Windows, to)
		}
	}
	if len(out.Windows) == 0 && len(out.Sessions) > 0 {
		out.Windows = []string{out.Sessions[0].ID}
	}
	if to, ok := moved[out.Show]; ok {
		out.Show = to
	} else {
		out.Show, out.ShowAt = "", time.Time{}
	}
	out.Adopt = m.list
	return out, true
}

// addName adds name to names, unless it is empty or there already.
func addName(names []string, name string) []string {
	name = Label(name)
	if name == "" || slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, name) }) {
		return names
	}
	return append(names, name)
}

// migration gathers the adoptions of a Migrate.
type migration struct {
	machineOf func(string) string
	list      []Adoption
	order     []string
}

// key names a host for good: by its machine when known, so that a home
// kept by its machine (MachineHost) and a pane kept by the host's ID are
// one host; else by the host's ID.
func (m *migration) key(host string) (key, id, machine string) {
	if mm, ok := HostMachine(host); ok {
		return "m:" + mm, "", mm
	}
	if m.machineOf != nil {
		if mm := m.machineOf(host); mm != "" {
			return "m:" + mm, host, mm
		}
	}
	return "h:" + host, host, ""
}

// add puts tab tab in the adoption of host, for project when the
// workspace was homed on that host.
func (m *migration) add(host, home string, project *AdoptProject, tab AdoptTab) {
	key, id, machine := m.key(host)
	var p *AdoptProject
	if project != nil && home != "" {
		if hk, _, _ := m.key(home); hk == key {
			p = project
		}
	}
	full := key
	if p != nil {
		full += "\x00" + p.Dir
	}
	i := slices.Index(m.order, full)
	if i < 0 {
		m.order = append(m.order, full)
		m.list = append(m.list, Adoption{ID: NewSpaceID(), Host: id, Machine: machine, Project: p, Tabs: []AdoptTab{}})
		i = len(m.list) - 1
	}
	a := &m.list[i]
	if a.Host == "" {
		a.Host = id
	}
	a.Tabs = append(a.Tabs, tab)
}

// split takes the panes of other hosts out of tab t, into adoptions, and
// returns what is left of it here: nil when nothing is.
func (m *migration) split(t Tab, home string, project *AdoptProject) *Tab {
	var hosts []string
	for _, r := range t.Roots() {
		for _, l := range Leaves(r) {
			if l.Host != "" && !slices.Contains(hosts, l.Host) {
				hosts = append(hosts, l.Host)
			}
		}
	}
	for _, h := range hosts {
		of := func(n *Node) bool { return n.Host == h && n.SID != "" }
		sub, _ := prune(t.Root, of)
		for _, f := range t.Floats {
			if fp, _ := prune(f.Pane, of); fp != nil {
				if sub == nil {
					sub = fp
				} else {
					sub = &Node{Ratio: 0.5, A: sub, B: fp}
				}
			}
		}
		if sub == nil {
			continue
		}
		for _, l := range Leaves(sub) {
			l.Host = ""
		}
		m.add(h, home, project, AdoptTab{Title: t.Name, Split: sub})
	}
	if len(hosts) == 0 {
		return &t
	}
	here := func(n *Node) bool { return n.Host == "" }
	// The focus, among the tiled panes and among the floats.
	var focusLeaf *Node
	if leaves := Leaves(t.Root); t.Focus >= 0 && t.Focus < len(leaves) {
		focusLeaf = leaves[t.Focus]
	}
	root, kept := prune(t.Root, here)
	out := t
	out.Root, out.Floats, out.Focus, out.FocusFloat = root, nil, 0, 0
	if nf, ok := kept[focusLeaf]; ok {
		out.Focus = max(slices.Index(Leaves(root), nf), 0)
	} else {
		out.Zoom = false
	}
	for i, f := range t.Floats {
		fp, _ := prune(f.Pane, here)
		if fp == nil {
			continue
		}
		f.Pane = fp
		out.Floats = append(out.Floats, f)
		if t.FocusFloat == i+1 {
			out.FocusFloat = len(out.Floats)
		}
	}
	if len(Leaves(root)) < 2 {
		out.Zoom = false
	}
	if out.Root == nil && len(out.Floats) == 0 {
		return nil
	}
	if out.Root == nil && out.Canvas == nil {
		// A tab of floats only: the top one becomes its tiled pane.
		top := out.Floats[len(out.Floats)-1]
		out.Floats = out.Floats[:len(out.Floats)-1]
		out.Root, out.FocusFloat = top.Pane, 0
	}
	return &out
}

// prune copies the tree n with only the leaves keep takes, a split of one
// side gone giving its place to the other; nil when none is left. kept
// maps each leaf kept to its copy.
func prune(n *Node, keep func(*Node) bool) (*Node, map[*Node]*Node) {
	kept := map[*Node]*Node{}
	var walk func(n *Node) *Node
	walk = func(n *Node) *Node {
		if n == nil {
			return nil
		}
		if n.IsLeaf() {
			if !keep(n) {
				return nil
			}
			c := *n
			kept[n] = &c
			return &c
		}
		a, b := walk(n.A), walk(n.B)
		switch {
		case a == nil:
			return b
		case b == nil:
			return a
		}
		c := *n
		c.A, c.B = a, b
		return &c
	}
	return walk(n), kept
}
