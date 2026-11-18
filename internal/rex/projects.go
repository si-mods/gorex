//go:build darwin || linux || windows

package rex

import (
	"cmp"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"gorex/internal/layout"
)

// Projects (docs/projects.md): a host keeps its projects, each a folder of
// its own, as workspaces of its layout, and one workspace of its tabs with
// no project. The commands of this file make and change them; the
// migration makes a layout of v0.38.0 to v0.40.x one of them once, as the
// server loads it.

// CapabilityProjects is what a server that keeps its projects as
// docs/projects.md says does: the layout of layout.Version 3, and the
// commands project.create, project.adopt, project.move, project.update,
// spaces.rename and adopt.taken.
const CapabilityProjects = "projects.v2"

// migrateLayout makes the layout the server loaded, of v0.38.0 to v0.40.x,
// one of this version (layout.Migrate), once: as a server starts and as
// one resumes after an upgrade. It starts and ends no session. The layout
// read is kept as layout.json.v039, unless one is there already.
func (s *Server) migrateLayout() {
	dir := cmp.Or(s.dir, Dir())
	s.mu.Lock()
	raw := s.layout
	s.mu.Unlock()
	if len(raw) == 0 {
		return
	}
	machines := HostMachines(dir)
	st, changed := layout.Migrate(raw, func(host string) string { return machines[host] })
	if !changed {
		return
	}
	keep := filepath.Join(dir, "layout.json.v039")
	if _, err := os.Stat(keep); err != nil {
		if err := os.WriteFile(keep, raw, 0o600); err != nil {
			log.Printf("rex: keeping the layout before projects: %v", err)
		}
	}
	l := encodeLayout(&st)
	s.mu.Lock()
	s.layout = l
	s.layoutRev++
	rev := s.layoutRev
	s.mu.Unlock()
	s.writeLayout(rev, true)
	tabs := 0
	for _, a := range st.Adopt {
		tabs += len(a.Tabs)
	}
	log.Printf("rex: the layout is of projects now: %d workspaces, %d tabs of other hosts to adopt", len(st.Sessions), tabs)
}

// noProject is the layout's workspace of tabs with no project, made when
// it has none.
func noProject(st *layout.Store) *layout.Workspace {
	if w := st.NoProject(); w != nil {
		return w
	}
	st.Sessions = append(st.Sessions, layout.Workspace{ID: layout.NewSpaceID(), Name: layout.NoProjectName, Layout: layout.Layout{Tabs: []layout.Tab{}}})
	return &st.Sessions[len(st.Sessions)-1]
}

// projectDir is the folder of a project a command names: dir cleaned,
// absolute and not a device or pipe (VolumePath); an error code and its
// message when it is not.
func projectDir(dir string) (string, string, string) {
	dir = strings.TrimSpace(dir)
	if dir == "" || !layout.IsAbsDir(dir) || !filepath.IsAbs(dir) {
		return "", "bad_dir", "a project's folder must be an absolute path"
	}
	dir = filepath.Clean(dir)
	if _, ok := VolumePath(dir); !ok {
		return "", "bad_dir", "a project's folder must be a folder, not a device"
	}
	return dir, "", ""
}

// isDir tells whether dir is a directory the server can read.
func isDir(dir string) bool {
	if _, ok := VolumePath(dir); !ok {
		return false
	}
	st, err := os.Stat(dir)
	return err == nil && st.IsDir()
}

// spaceName is a space's name as a command gives it.
func spaceName(name *string) string {
	if name == nil {
		return ""
	}
	return layout.Label(*name)
}

// carried takes a space name off the names offered once a project
// carries it.
func carried(st *layout.Store, space string) {
	st.Spaces = slices.DeleteFunc(st.Spaces, func(n string) bool { return space != "" && n == space })
}

// createProject makes project.create: a project of the folder Dir, named
// Name (the folder's name for ""), in the space SpaceName, and its first
// tab in the folder. A folder that is a project already fails with
// "exists", and Space is that project; a folder that is not there fails
// with FolderMissing, and makes nothing.
func (s *Server) createProject(st *layout.Store, c LayoutCommand, a *applying) LayoutCommand {
	var res LayoutCommand
	dir, code, msg := projectDir(c.Dir)
	if code != "" {
		res.Code, res.Error = code, msg
		return res
	}
	if w := st.Project(dir); w != nil {
		res.Code, res.Error, res.Space = "exists", "the folder is a project already", w.ID
		return res
	}
	colors := make([]string, len(st.Sessions))
	for i, w := range st.Sessions {
		colors[i] = w.Color
	}
	w := layout.Workspace{
		ID: layout.NewSpaceID(), Name: cmp.Or(layout.Label(c.Name), layout.Label(filepath.Base(dir)), dir),
		Color: layout.NextColor(colors), Space: spaceName(c.SpaceName), Dir: dir,
		Layout: layout.Layout{Tabs: []layout.Tab{}},
	}
	if c.Color != "" {
		setColor(&w, c.Color)
	}
	st.Sessions = append(st.Sessions, w)
	carried(st, w.Space)
	tab := s.createTab(st, LayoutCommand{Space: w.ID, Name: c.Name, Command: c.Command, Focus: c.Focus}, a)
	if tab.Code != "" {
		return tab
	}
	res.Space, res.Tab, res.New = w.ID, tab.Tab, tab.New
	return res
}

// moveToProject makes project.move: the tab Tab, with its panes, their
// sessions, its floats, focus and zoom, goes to the end of the workspace
// ToSpace, a project, or with "" to the tabs with no project. The
// workspace it leaves stays, with no tab perhaps. It starts and ends no
// session.
func moveToProject(st *layout.Store, c LayoutCommand, a *applying) LayoutCommand {
	var res LayoutCommand
	var from *layout.Workspace
	ti := -1
	for i := range st.Sessions {
		if j := slices.IndexFunc(st.Sessions[i].Tabs, func(t layout.Tab) bool { return t.ID == c.Tab }); c.Tab != "" && j >= 0 {
			from, ti = &st.Sessions[i], j
			break
		}
	}
	if from == nil {
		res.Code, res.Error = "tab_not_found", "no such tab in the GoRex layout"
		return res
	}
	fromID := from.ID
	var to *layout.Workspace
	if c.ToSpace == "" {
		to = noProject(st)
	} else if to = st.Space(c.ToSpace); to == nil || to.Dir == "" {
		res.Code, res.Error = "space_not_found", "no such project in the GoRex layout"
		return res
	}
	// noProject may have grown st.Sessions: find the tab's again.
	from = st.Space(fromID)
	if to == from {
		res.Code, res.Error = "same_space", "the tab is in that project already"
		return res
	}
	t := from.Tabs[ti]
	a.touch(t.ID, &t)
	removeTab(from, ti)
	insertTab(to, len(to.Tabs), t, false)
	res.Space, res.Tab = to.ID, t.ID
	return res
}

// updateProject makes project.update: the project Space gets the name
// Name, the color Color and the space SpaceName, each when given.
func updateProject(st *layout.Store, c LayoutCommand) LayoutCommand {
	var res LayoutCommand
	w := st.Space(c.Space)
	if w == nil || w.Dir == "" {
		res.Code, res.Error = "space_not_found", "no such project in the GoRex layout"
		return res
	}
	if name := layout.Label(c.Name); name != "" {
		w.Name = name
	}
	setColor(w, c.Color)
	if c.SpaceName != nil {
		w.Space = spaceName(c.SpaceName)
		carried(st, w.Space)
	}
	res.Space = w.ID
	return res
}

// renameSpace makes spaces.rename: every project of the space From goes
// to the space To, and an offered name From becomes To. Index is how many
// projects it moved.
func renameSpace(st *layout.Store, c LayoutCommand) LayoutCommand {
	var res LayoutCommand
	from, to := layout.Label(c.From), layout.Label(c.To)
	if from == to {
		return res
	}
	for i := range st.Sessions {
		if w := &st.Sessions[i]; w.Dir != "" && w.Space == from {
			w.Space = to
			res.Index++
		}
	}
	if i := slices.Index(st.Spaces, from); i >= 0 && from != "" {
		st.Spaces = slices.Delete(st.Spaces, i, i+1)
		if to != "" && res.Index == 0 && !slices.Contains(st.Spaces, to) {
			st.Spaces = slices.Insert(st.Spaces, i, to)
		}
	}
	carried(st, to)
	return res
}

// adopt makes project.adopt: the tabs of Adopt, each a split of this
// machine's sessions that run already, go into the project of
// Adopt.Project, made unless this host has one of its folder, else into
// the tabs with no project. It starts no session and opens no tab of its
// own. A pane of a session that is not running here is left out, a tab of
// none is skipped, and a tab whose panes a tab of the workspace holds all
// together already is adopted: adopting again changes nothing. Each pane
// leaves the tab it was in; a tab it leaves empty goes. A project whose
// folder is not here (a.folder) is not made, and its tabs go to the tabs
// with no project. Space is the workspace the tabs went to, and Tab the
// first tab made.
func (s *Server) adopt(st *layout.Store, c LayoutCommand, a *applying) LayoutCommand {
	var res LayoutCommand
	if c.Adopt == nil {
		res.Code, res.Error = "bad_request", "no adoption"
		return res
	}
	used := map[string]bool{}
	type tab struct {
		title string
		root  *layout.Node
	}
	var tabs []tab
	for _, at := range c.Adopt.Tabs {
		root := s.adoptable(at.Split, used)
		if root != nil {
			tabs = append(tabs, tab{at.Title, root})
		}
	}
	var toID string
	if p := c.Adopt.Project; p != nil {
		dir, code, _ := projectDir(p.Dir)
		switch w := st.Project(dir); {
		case code != "":
		case w != nil:
			toID = w.ID
		case a.folder:
			colors := make([]string, len(st.Sessions))
			for i, w := range st.Sessions {
				colors[i] = w.Color
			}
			w := layout.Workspace{
				ID: layout.NewSpaceID(), Name: cmp.Or(layout.Label(p.Name), layout.Label(filepath.Base(dir))),
				Color: layout.NextColor(colors), Space: layout.Label(p.Space), Dir: dir, Layout: layout.Layout{Tabs: []layout.Tab{}},
			}
			st.Sessions = append(st.Sessions, w)
			carried(st, w.Space)
			toID = w.ID
		}
	}
	for _, t := range tabs {
		to := st.Space(toID)
		if to == nil {
			to = noProject(st)
			toID = to.ID
		}
		res.Space = toID
		if adopted(to, t.root) {
			continue
		}
		for _, l := range layout.Leaves(t.root) {
			if p, ok := find(st, "", l.SID); ok {
				a.touch(p.tab().ID, p.tab())
				if unlink(p, "") {
					removeTab(p.w, p.ti)
				}
			}
		}
		to = st.Space(toID)
		nt := layout.Tab{ID: layout.NewTabID(), Name: layout.Label(t.title), Root: t.root}
		a.touch(nt.ID, nil)
		insertTab(to, len(to.Tabs), nt, false)
		if res.Tab == "" {
			res.Tab = nt.ID
		}
	}
	res.Space = cmp.Or(res.Space, toID)
	return res
}

// adoptable is the split of an adoption's tab as this machine keeps it:
// only the leaves of its sessions that run, each once (used), with their
// names, and the splits between them; nil for none.
func (s *Server) adoptable(n *layout.Node, used map[string]bool) *layout.Node {
	if n == nil {
		return nil
	}
	if n.IsLeaf() {
		if n.SID == "" || n.Host != "" || used[n.SID] || !s.live(n.SID) {
			return nil
		}
		used[n.SID] = true
		return &layout.Node{SID: n.SID, Name: layout.PaneName(n.Name)}
	}
	if n.A == nil || n.B == nil {
		return s.adoptable(cmp.Or(n.A, n.B), used)
	}
	x, y := s.adoptable(n.A, used), s.adoptable(n.B, used)
	switch {
	case x == nil:
		return y
	case y == nil:
		return x
	}
	ratio := n.Ratio
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.5
	}
	return &layout.Node{Vertical: n.Vertical, Ratio: ratio, A: x, B: y}
}

// adopted tells whether one tab of w holds every pane of root.
func adopted(w *layout.Workspace, root *layout.Node) bool {
	leaves := layout.Leaves(root)
	for _, t := range w.Tabs {
		if !slices.ContainsFunc(leaves, func(l *layout.Node) bool { return !tabHas(&t, l.SID) }) {
			return true
		}
	}
	return false
}

// tabHas tells whether tab t has a pane of this machine's session sid.
func tabHas(t *layout.Tab, sid string) bool {
	for _, r := range t.Roots() {
		if n := layout.Find(r, sid); n != nil && n.Host == "" {
			return true
		}
	}
	return false
}

// usedEvery is how often at most a project's Used follows the input of
// one of its sessions: each such change is a new layout.
var usedEvery = newDuration(time.Minute)

// uses throttles the server's notes of input (used).
type uses struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// used notes that session sid took input: the project whose tab has it
// was used then. It writes the layout at most once per usedEvery per
// session, aside from the input.
func (s *Server) used(sid string) {
	now := time.Now()
	s.uses.mu.Lock()
	if s.uses.last == nil {
		s.uses.last = map[string]time.Time{}
	}
	if now.Sub(s.uses.last[sid]) < usedEvery.get() {
		s.uses.mu.Unlock()
		return
	}
	s.uses.last[sid] = now
	s.uses.mu.Unlock()
	go s.change(&applying{s: s}, func(st *layout.Store) LayoutCommand {
		if p, ok := find(st, "", sid); ok && p.w.Dir != "" && now.Sub(p.w.Used) >= usedEvery.get() {
			p.w.Used = now.UTC().Truncate(time.Second)
		}
		return LayoutCommand{}
	})
}

// forgetUse forgets the note of a session that ended.
func (s *Server) forgetUse(sid string) {
	s.uses.mu.Lock()
	delete(s.uses.last, sid)
	s.uses.mu.Unlock()
}

// projectUsed notes that a tab of workspace w was made now, when it is a
// project.
func projectUsed(w *layout.Workspace) {
	if w != nil && w.Dir != "" {
		w.Used = time.Now().UTC().Truncate(time.Second)
	}
}
