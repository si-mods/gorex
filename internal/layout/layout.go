// Package layout is the saved shape of GoRex's windows, tabs and panes.
// The app owns it; the session server keeps it read-only, and reads it to
// start again the sessions of a run that did not stop.
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
	// ID is the tab's own ID, which a client of another window keeps to
	// follow it: it stays while the tab does.
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Root  *Node  `json:"root"`
	Focus int    `json:"focus"`
	Zoom  bool   `json:"zoom,omitempty"`
}

// Node is a pane, of session SID, or a split: Vertical stacks A above B,
// else A is left of B; Ratio is A's share.
type Node struct {
	SID  string `json:"sid,omitempty"`
	Name string `json:"name,omitempty"`
	// Host is the id of the remote host of the session, "" for this
	// machine's.
	Host     string  `json:"host,omitempty"`
	Dir      string  `json:"dir,omitempty"`
	Cols     int     `json:"cols,omitempty"`
	Rows     int     `json:"rows,omitempty"`
	Vertical bool    `json:"vertical,omitempty"`
	Ratio    float32 `json:"ratio,omitempty"`
	A        *Node   `json:"a,omitempty"`
	B        *Node   `json:"b,omitempty"`
	// Unavailable marks a pane the server would not share with this
	// client: its SID and host are gone, but its place in the layout stays.
	Unavailable bool `json:"unavailable,omitempty"`
}
