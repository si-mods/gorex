package rexapi

import (
	"slices"
	"strings"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// gorex.merge_windows and gorex.move_block_to_new_window are the desktop's
// Merge (a tab dropped on another tab's middle, mergeTab) and Move Pane to
// New Tab (movePaneToNewTab), GoRex's own: Rex's session.move_block places
// one block beside another, which can neither graft a window's tree beside
// another's nor give a block a window of its own. The GoRex server makes
// both (tab.set, pane.move with new_tab), so the desktop and every client
// see them as another client's change of the layout; the blocks keep their
// IDs and their programs go on.

// featureMergeWindows and featureNewWindowFromBlock are the welcome's
// features of a gateway that takes the methods of the same names.
const (
	featureMergeWindows       = "gorex.merge_windows"
	featureNewWindowFromBlock = "gorex.move_block_to_new_window"
)

// methodGorexMergeWindows puts a window's tree beside another window's,
// side by side, half each, as the desktop's mergeTab: the window it
// leaves closes, the other keeps its ID and label, takes the moved
// window's floating layers and its focus, and becomes the active window.
func methodGorexMergeWindows(c *control, a args) (any, *apiError) {
	if !c.g.hello.Can(rex.CapabilityLayoutServer) {
		return nil, errNotSupported("the GoRex server does not take a window's layout; update GoRex")
	}
	if aerr := a.only("session_id", "window_id", "into_window_id"); aerr != nil {
		return nil, aerr
	}
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	srcID, aerr := a.reqStr("window_id")
	if aerr != nil {
		return nil, aerr
	}
	dstID, aerr := a.reqStr("into_window_id")
	if aerr != nil {
		return nil, aerr
	}
	if srcID == dstID {
		return nil, errBadRequest("a window cannot merge into itself")
	}
	src, dst := s.window(srcID), s.window(dstID)
	if src == nil || dst == nil {
		return nil, errNotFound("no such window in %s", s.id)
	}
	if src.tab == "" || dst.tab == "" {
		return nil, errNotSupported("the GoRex window predates tab IDs; update GoRex")
	}
	raw, err := c.g.opt.Client.Layout()
	if err != nil {
		return nil, errInternal("reading the layout: %v", err)
	}
	st := layout.Parse(raw)
	ws := st.Space(s.space)
	if ws == nil {
		return nil, errNotFound("no space of %s", s.id)
	}
	si := slices.IndexFunc(ws.Tabs, func(t layout.Tab) bool { return t.ID == src.tab })
	di := slices.IndexFunc(ws.Tabs, func(t layout.Tab) bool { return t.ID == dst.tab })
	if si < 0 || di < 0 {
		return nil, errNotFound("no such window in %s", s.id)
	}
	from, into := ws.Tabs[si], ws.Tabs[di]
	if from.Root == nil || into.Root == nil || from.Canvas != nil || into.Canvas != nil {
		return nil, errConflict("only windows of tiled panes merge")
	}
	// As the desktop's tabsCanMerge: the panes of one host only.
	if hosts(from) != hosts(into) {
		return nil, errConflict("the windows hold panes of different hosts")
	}
	// tab.set moves only the panes of a session off their tab: a note, a
	// browser or an unavailable pane would stay behind, and be in both.
	for _, r := range from.Roots() {
		if slices.ContainsFunc(layout.Leaves(r), func(n *layout.Node) bool { return n.SID == "" }) {
			return nil, errConflict("the window holds a pane of no terminal, which only the desktop merges")
		}
	}
	// Both tabs are built on as read: a revision of 0 would check nothing.
	if from.Rev == 0 || into.Rev == 0 {
		return nil, errConflict("the windows have no revision; update GoRex")
	}
	merged := into
	merged.Root = &layout.Node{A: into.Root, B: from.Root, Ratio: 0.5}
	merged.Floats = append(slices.Clone(into.Floats), from.Floats...)
	merged.Zoom = false
	merged.Focus, merged.FocusFloat = len(layout.Leaves(into.Root))+from.Focus, 0
	if from.FocusFloat > 0 {
		merged.FocusFloat = len(into.Floats) + from.FocusFloat
	}
	// tab.set places the tab at Index of the tabs left once the moved
	// window's goes.
	at := di
	if si < di {
		at--
	}
	moved := src.allBlocks()
	// One layout.apply, all or nothing: the tab.set, built on the into
	// tab's revision, after a change that does nothing (space.rename of no
	// name nor color) but carries the source tab's: the server checks each
	// change's Base against its tab before it makes any, so a source window
	// changed meanwhile (a new split) refuses the merge rather than leave
	// a part behind.
	if _, aerr := c.g.command(rex.LayoutCommand{Op: "layout.apply", Batch: []rex.LayoutCommand{
		{Op: "space.rename", Space: s.space, Tab: from.ID, Base: from.Rev},
		{Op: "tab.set", Space: s.space, TabLayout: &merged, Index: at, Focus: true, Base: into.Rev},
	}}); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		if now == nil {
			return true
		}
		w := now.window(dstID)
		return now.window(srcID) == nil && w != nil && !slices.ContainsFunc(moved, func(b *mBlock) bool {
			nb := now.findBlock(b.id)
			return nb == nil || nb.window != dstID
		})
	})
	if aerr != nil {
		return nil, aerr
	}
	return revisionResult{Revision: revisionOf(m, s.id)}, nil
}

// hosts names the hosts of a tab's panes, "" for this machine's alone.
func hosts(t layout.Tab) string {
	var out []string
	for _, r := range t.Roots() {
		for _, n := range layout.Leaves(r) {
			if n.Host != "" && !slices.Contains(out, n.Host) {
				out = append(out, n.Host)
			}
		}
	}
	slices.Sort(out)
	return strings.Join(out, " ")
}

// blockWindowResult is gorex.move_block_to_new_window's result.
type blockWindowResult struct {
	WindowID string `json:"window_id"`
	Revision uint64 `json:"revision"`
}

// methodGorexMoveBlockToNewWindow moves a tiled block out of its window
// into a window of its own, after it, as the desktop's Move Pane to New
// Tab: the session's active window stays, the block keeps its program.
func methodGorexMoveBlockToNewWindow(c *control, a args) (any, *apiError) {
	if aerr := a.only("session_id", "block_id"); aerr != nil {
		return nil, aerr
	}
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	id, aerr := a.reqStr("block_id")
	if aerr != nil {
		return nil, aerr
	}
	b := s.findBlock(id)
	if b == nil {
		return nil, errNotFound("no block %s in %s", id, s.id)
	}
	w := s.windowOf(b)
	if w == nil || !slices.Contains(w.blocks, b) {
		return nil, errConflict("only a tiled block moves to a window of its own")
	}
	if len(w.allBlocks()) < 2 {
		return nil, errConflict("the block is its window's only one")
	}
	res, aerr := c.g.command(rex.LayoutCommand{Op: "pane.move", Space: s.space, SID: b.sid, NewTab: true})
	if aerr != nil {
		return nil, aerr
	}
	if !res.Changed {
		if res.Reason == "zoomed_tab" {
			return nil, errConflict("the window is zoomed; unzoom it first")
		}
		return nil, errNotFound("the block is gone")
	}
	var to string
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		if now == nil {
			return true
		}
		nb := now.findBlock(id)
		if nb == nil || nb.window == w.id {
			return false
		}
		to = nb.window
		return true
	})
	if aerr != nil {
		return nil, aerr
	}
	return blockWindowResult{WindowID: to, Revision: revisionOf(m, s.id)}, nil
}

// mergeWindowsEntry and newWindowFromBlockEntry are the methods' entries
// of the help document, in the shape of Rex's own.
var mergeWindowsEntry = map[string]any{
	"name":        "gorex.merge_windows",
	"description": "Put a window's blocks beside another window's, and close it. GoRex's own method: use it only when the welcome's features list gorex.merge_windows.",
	"invoke":      map[string]any{"method": "gorex.merge_windows"},
	"input_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id":     map[string]any{"type": "string", "description": "The session the windows are in."},
			"window_id":      map[string]any{"type": "string", "description": "The window to merge. It closes."},
			"into_window_id": map[string]any{"type": "string", "description": "The window it merges into. It must be another window of the session."},
		},
		"required":             []any{"session_id", "window_id", "into_window_id"},
		"additionalProperties": false,
	},
	"output_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"revision": map[string]any{"type": "integer", "minimum": 0, "description": "The session's revision after this call."},
		},
		"required":             []any{"revision"},
		"additionalProperties": false,
	},
	"request_example":  "{\n  \"session_id\": \"session:<uuid>\",\n  \"window_id\": \"window:01234567-89ab-cdef-0123-456789abcde1\",\n  \"into_window_id\": \"window:01234567-89ab-cdef-0123-456789abcde2\"\n}",
	"response_example": "{\"revision\": 42}",
	"validation_notes": []any{
		"The into window's layout becomes a horizontal split, half each: its old layout before, the merged window's after. It takes the merged window's floating layers and focus, keeps its ID and label, ends its zoom, and becomes the active window.",
		"The blocks keep their IDs and their programs go on. The merged window closes.",
		"Windows of panes of different hosts, or of no tiled layer, a window to merge that holds a pane of no terminal (a note, a browser), and windows changed since the call read them, fail with conflict and change nothing. An unknown window fails with not_found, a window merged into itself with bad_request.",
		"A server that can't take a window's layout, whose welcome doesn't list gorex.merge_windows, fails with not_supported.",
	},
}

var newWindowFromBlockEntry = map[string]any{
	"name":        "gorex.move_block_to_new_window",
	"description": "Move a block into a new window of its own. GoRex's own method: use it only when the welcome's features list gorex.move_block_to_new_window.",
	"invoke":      map[string]any{"method": "gorex.move_block_to_new_window"},
	"input_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string", "description": "The session the block is in."},
			"block_id":   map[string]any{"type": "string", "description": "The block to move. It must be in a window's tiled layer, with another block."},
		},
		"required":             []any{"session_id", "block_id"},
		"additionalProperties": false,
	},
	"output_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"window_id": map[string]any{"type": "string", "description": "The new window."},
			"revision":  map[string]any{"type": "integer", "minimum": 0, "description": "The session's revision after this call."},
		},
		"required":             []any{"window_id", "revision"},
		"additionalProperties": false,
	},
	"request_example":  "{\n  \"session_id\": \"session:<uuid>\",\n  \"block_id\": \"block:01234567-89ab-cdef-0123-456789abcde1\"\n}",
	"response_example": "{\"window_id\": \"window:<uuid>\", \"revision\": 42}",
	"validation_notes": []any{
		"The new window goes just after the block's window. The session's active window doesn't change.",
		"The block keeps its ID and its program goes on.",
		"A block that is its window's only one, a floating one, or one of a zoomed window fails with conflict; an unknown block with not_found.",
	},
}

// llmsMergeWindows documents gorex.merge_windows and
// gorex.move_block_to_new_window, after GoRex's other methods.
var llmsMergeWindows = []byte(`
### gorex.merge_windows

Feature: ` + "`gorex.merge_windows`" + `. Put a window's blocks beside another window's, and close it, as the GoRex
desktop merges a tab dropped on another tab's middle.

Arguments:

- ` + "`session_id`" + ` (string, required): The session the windows are in.
- ` + "`window_id`" + ` (string, required): The window to merge. It closes.
- ` + "`into_window_id`" + ` (string, required): The window it merges into.

Result: ` + "`revision`" + ` (integer), the session's revision after the call.

Notes:

- The into window's layout becomes a horizontal split, half each: its old layout before, the merged window's after.
  It takes the merged window's floating layers and focus, keeps its ID and label, ends its zoom, and becomes the
  active window. The blocks keep their IDs and their programs go on.
- Windows of panes of different hosts, or of no tiled layer, a window to merge that holds a pane of no terminal
  (a note, a browser), and windows changed meanwhile, fail with ` + "`conflict`" + ` and change nothing; an unknown window with
  ` + "`not_found`" + `; a window merged into itself with ` + "`bad_request`" + `. A server that can't take a window's layout
  fails with ` + "`not_supported`" + `.

### gorex.move_block_to_new_window

Feature: ` + "`gorex.move_block_to_new_window`" + `. Move a block into a new window of its own, just after its window, as
the GoRex desktop's Move Pane to New Tab.

Arguments:

- ` + "`session_id`" + ` (string, required): The session the block is in.
- ` + "`block_id`" + ` (string, required): The block to move, in a window's tiled layer with another block.

Result: ` + "`window_id`" + ` (string), the new window, and ` + "`revision`" + ` (integer), the session's revision after the call.

Notes:

- The session's active window doesn't change. The block keeps its ID and its program goes on.
- A block that is its window's only one, a floating one, or one of a zoomed window fails with ` + "`conflict`" + `; an
  unknown block with ` + "`not_found`" + `.
`)
