// Package layout is the saved shape of GoRex's windows, tabs and panes.
// The session server keeps it and makes every change of it (rex's apply),
// the app's as its clients' (the app sends its arrangement of a tab as
// tab.set), its own as its sessions end and start, and reads it to start
// again the sessions a run before lost; the app shows it.
package layout

import (
	"strings"
	"time"
)

// Store is every workspace and the windows that show them, in order.
type Store struct {
	Version  int         `json:"version"`
	Sessions []Workspace `json:"sessions"`
	Windows  []string    `json:"windows"`
	// Teleports are the teleports of this machine's panes under way: the
	// session each starts on a host, which the app opens as no tab of its
	// own; the server shows it in the pane's place once it runs.
	Teleports []Teleport `json:"teleports,omitempty"`
	// Kills are the sessions of other hosts whose panes a client of the
	// server closed: the app ends each on its host, as it ends those its
	// user closes, and the server forgets it then (kills.taken).
	Kills []Kill `json:"kills,omitempty"`
	// Show is the space a client's tab.create with the focus made a tab
	// in while no window showed it, at ShowAt: a window of the app shows
	// it, and the server forgets it then (show.taken).
	Show   string    `json:"show,omitempty"`
	ShowAt time.Time `json:"showAt,omitzero"`
	// Adopt are the panes of other hosts the migration took out of this
	// host's tabs, for each host to adopt (Adoption).
	Adopt []Adoption `json:"adopt,omitempty"`
	// Spaces are space names the clients offer though no project carries
	// them: the names of the spaces of a layout of before projects.
	Spaces []string `json:"spaces,omitempty"`
	// The first window's tabs, for a GoRex from before workspaces.
	Layout
	// Named tells that Parse gave a browser pane an ID: the layout read is
	// not the one written, which a writer writes once.
	Named bool `json:"-"`
}

// Teleport is a teleport under way of the pane of this machine's session
// From, to the session SID it starts on host Host, by the host's ID.
type Teleport struct {
	Host string `json:"host"`
	SID  string `json:"sid"`
	From string `json:"from"`
}

// Kill is the session SID to end on host Host, by the host's ID.
type Kill struct {
	Host string `json:"host"`
	SID  string `json:"sid"`
}

// Workspace is a workspace as the layout keeps it: a project, a folder of
// this host's (docs/projects.md), or with no Dir the host's tabs with no
// project.
type Workspace struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
	// Space is the name of the space the project shows in; "" is
	// "Default". The workspace of tabs with no project has none.
	Space string `json:"space,omitempty"`
	// Dir is the project's folder on this host, absolute and unique among
	// the host's projects (SameDir). "" for the host's workspace of tabs
	// with no project. A new tab of the project starts there.
	Dir string `json:"dir,omitempty"`
	// Used is when a tab of the project last took input or was made.
	Used time.Time `json:"used,omitzero"`
	Layout
}

// NoProjectName is the name of a host's workspace of tabs with no
// project, as Rex's apps show it as a session.
const NoProjectName = "Tabs with no project"

// NoProject is the host's workspace of tabs with no project (no Dir), nil
// for none.
func (s *Store) NoProject() *Workspace {
	for i := range s.Sessions {
		if s.Sessions[i].Dir == "" {
			return &s.Sessions[i]
		}
	}
	return nil
}

// Project is the project of folder dir (SameDir), nil for none.
func (s *Store) Project(dir string) *Workspace {
	for i := range s.Sessions {
		if w := &s.Sessions[i]; w.Dir != "" && SameDir(w.Dir, dir) {
			return w
		}
	}
	return nil
}

// Adoption is what the migration of a layout of v0.38.0 to v0.40.x kept
// of the panes another host ran in this host's tabs (Store.Adopt): the
// app sends it to that host as it connects (the "project.adopt"
// command), which arranges them there, and the server forgets it then.
type Adoption struct {
	ID string `json:"id"`
	// Host is the host by its ID in hosts.json, as Node.Host had it, and
	// Machine its machine (rex.MachineID) when known; one of them is set.
	Host    string `json:"host,omitempty"`
	Machine string `json:"machine,omitempty"`
	// Project is the project the tabs go in there, of a workspace homed
	// on that host; nil for its tabs with no project.
	Project *AdoptProject `json:"project,omitempty"`
	Tabs    []AdoptTab    `json:"tabs"`
}

// AdoptProject is the project an adoption makes on its host, unless the
// host has a project of the folder already.
type AdoptProject struct {
	Name  string `json:"name"`
	Dir   string `json:"dir"`
	Space string `json:"space,omitempty"`
}

// AdoptTab is a tab an adoption makes: its title and the panes, a tree of
// leaves of sessions of that host (Node.SID) and their splits.
type AdoptTab struct {
	Title string `json:"title,omitempty"`
	Split *Node  `json:"split"`
}

// machineHost starts a home host of a layout of v0.38.0 to v0.40.x that
// names a machine (rex.MachineID) rather than a host of hosts.json: the
// home of a project another device set on a machine this one has no host
// of. Only the migration reads it (Migrate).
const machineHost = "machine:"

// HostMachine is the machine a home host of MachineHost names, and
// whether it is one.
func HostMachine(host string) (string, bool) {
	m, ok := strings.CutPrefix(host, machineHost)
	return m, ok && m != ""
}

// IsAbsDir tells whether dir is an absolute path on some machine: a Unix
// one, or a Windows one with a drive or a share. A project's folder may
// be on a host of another system than this one.
func IsAbsDir(dir string) bool {
	switch {
	case strings.HasPrefix(dir, "/"), strings.HasPrefix(dir, `\\`):
		return true
	case len(dir) >= 3 && dir[1] == ':' && (dir[2] == '\\' || dir[2] == '/'):
		c := dir[0] | 0x20
		return c >= 'a' && c <= 'z'
	}
	return false
}

// Layout is the tabs of a window, and which is active.
type Layout struct {
	Active int   `json:"active"`
	Tabs   []Tab `json:"tabs"`
}

// Tab is a tab of the window: panes in a tree of splits.
type Tab struct {
	// ID names the tab for good: the app gives it to every tab it makes,
	// keeps it while the tab lasts, wherever the tab goes, and gives one
	// to a tab of an older layout that has none. Clients name a tab by it.
	ID string `json:"id,omitempty"`
	// Rev is the tab's revision: the server counts it up by one at each
	// change of the tab's arrangement, so that a change built on the tab
	// as it was (LayoutCommand.Base) is refused once another came first.
	Rev   uint64 `json:"rev,omitempty"`
	Name  string `json:"name,omitempty"`
	Root  *Node  `json:"root"`
	Focus int    `json:"focus"`
	Zoom  bool   `json:"zoom,omitempty"`
	// Floats are the panes that float over the tab's tiled ones, the lowest
	// first. FocusFloat is the focused one's place in it, from 1; 0 when the
	// focus is on a tiled pane, which Focus names.
	Floats     []Float `json:"floats,omitempty"`
	FocusFloat int     `json:"focusFloat,omitempty"`
	// Canvas, when set, makes the tab an infinite canvas: Root is nil and
	// every pane is a float, its box in fractions of Canvas.W, Canvas.H
	// rather than of the room, and not kept inside them.
	Canvas *Canvas `json:"canvas,omitempty"`
}

// Canvas is a tab's infinite canvas. W and H are the size, in DIPs, that
// its floats' fractions are of: the tab's room when it became a canvas,
// fixed from then on, so that the window's size changes no pane's. X and
// Y are the canvas's point, in DIPs, at the room's top left, and Scale how
// large it shows.
type Canvas struct {
	W     float32 `json:"w"`
	H     float32 `json:"h"`
	X     float32 `json:"x,omitempty"`
	Y     float32 `json:"y,omitempty"`
	Scale float32 `json:"scale,omitempty"`
}

// FloatMin is the least share of the room a float's side takes.
const FloatMin = 0.12

// Float is a pane over a tab's tiled panes: Pane is a leaf, and X, Y, W, H
// are its box as fractions of the tab's room, spanning [X, X+W) and [Y, Y+H).
type Float struct {
	Pane   *Node   `json:"pane"`
	X      float32 `json:"x"`
	Y      float32 `json:"y"`
	W      float32 `json:"w"`
	H      float32 `json:"h"`
	Hidden bool    `json:"hidden,omitempty"`
	Zoom   bool    `json:"zoom,omitempty"`
}

// Roots are the tab's tree and its floats' panes, which every walk over
// the tab's sessions goes through.
func (t Tab) Roots() []*Node {
	roots := make([]*Node, 0, 1+len(t.Floats))
	if t.Root != nil {
		roots = append(roots, t.Root)
	}
	for _, f := range t.Floats {
		if f.Pane != nil {
			roots = append(roots, f.Pane)
		}
	}
	return roots
}

// Node is a pane, of session SID, or a split: Vertical stacks A above B,
// else A is left of B; Ratio is A's share.
type Node struct {
	SID  string `json:"sid,omitempty"`
	Name string `json:"name,omitempty"`
	// Host is the id of the remote host of the session, "" for this
	// machine's.
	Host string `json:"host,omitempty"`
	Dir  string `json:"dir,omitempty"`
	// URL is the page of a browser pane, which has no session, and ID
	// names the pane for good, as a session names a terminal's: Parse
	// gives one to a browser pane of a layout that has none.
	URL string `json:"url,omitempty"`
	ID  string `json:"id,omitempty"`
	// Kind is "note" for a note pane, which has no session either, and
	// Note its id, which names the file of its text; a pane of no kind is a terminal or, with a URL, a
	// browser.
	Kind string `json:"kind,omitempty"`
	Note string `json:"note,omitempty"`
	// NoteSize is a note's text size in points, 0 for the default, and
	// NotePreview whether it shows its text as Markdown.
	NoteSize    float32 `json:"noteSize,omitempty"`
	NotePreview bool    `json:"notePreview,omitempty"`
	// Prog is what ran in the pane's session, with Args, when the layout
	// was saved: the program in the foreground, not the shell at its
	// prompt. A pane whose session is lost offers to run it again.
	Prog     string   `json:"prog,omitempty"`
	Args     []string `json:"args,omitempty"`
	Cols     int      `json:"cols,omitempty"`
	Rows     int      `json:"rows,omitempty"`
	Vertical bool     `json:"vertical,omitempty"`
	Ratio    float32  `json:"ratio,omitempty"`
	A        *Node    `json:"a,omitempty"`
	B        *Node    `json:"b,omitempty"`
	// Unavailable marks a pane the server would not share with this
	// client: its SID and host are gone, but its place in the layout stays.
	Unavailable bool `json:"unavailable,omitempty"`
	// AgentSession is the conversation Claude Code had open as Prog, which
	// the pane resumes, and AgentConfigDir its config dir when not the
	// default.
	AgentSession   string `json:"agentSession,omitempty"`
	AgentConfigDir string `json:"agentConfigDir,omitempty"`
}
