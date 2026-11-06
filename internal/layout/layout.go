// Package layout is the saved shape of GoRex's windows, tabs and panes.
// The app writes it while a window is open; the session server keeps it,
// makes the clients' changes of it while no window is (rex's apply), and
// reads it to start again the sessions of a run that did not stop.
package layout

// Store is every workspace and the windows that show them, in order.
type Store struct {
	Version  int         `json:"version"`
	Sessions []Workspace `json:"sessions"`
	Windows  []string    `json:"windows"`
	// The first window's tabs, for a GoRex from before workspaces.
	Layout
}

// Workspace is a workspace as the layout keeps it.
type Workspace struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
	Layout
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
	ID    string `json:"id,omitempty"`
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
	// URL is the page of a browser pane, which has no session.
	URL string `json:"url,omitempty"`
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
