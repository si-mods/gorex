package rexapi

import (
	"encoding/json"
	"math"
	"sort"
	"strings"

	"gorex/internal/rex"
)

// newInitialWindow is one window of session.create's initial_windows.
type newInitialWindow struct {
	WindowLabel string          `json:"window_label"`
	Layout      json.RawMessage `json:"layout"`
	tree        *specTree
	ids         []string
}

// initialWindow is a window session.create made.
type initialWindow struct {
	WindowID string   `json:"window_id"`
	BlockIDs []string `json:"block_ids"`
	Revision uint64   `json:"revision"`
}

// createResult is session.create's result.
type createResult struct {
	SessionID      string          `json:"session_id"`
	InitialWindows []initialWindow `json:"initial_windows,omitempty"`
}

// methodSessionCreate creates a session, which is a GoRex workspace.
func methodSessionCreate(c *control, a args) (any, *apiError) {
	if aerr := c.g.requireLocks(); aerr != nil {
		return nil, aerr
	}
	label, _, aerr := a.str("label")
	if aerr != nil {
		return nil, aerr
	}
	if label != "" {
		if aerr := validText(label, 128); aerr != nil {
			return nil, aerr
		}
	}
	rawWindows, present, aerr := a.raws("initial_windows")
	if aerr != nil {
		return nil, aerr
	}
	var windows []newInitialWindow
	if present && rawWindows != nil {
		for _, raw := range rawWindows {
			var w newInitialWindow
			if err := json.Unmarshal(raw, &w); err != nil {
				return nil, errBadRequest("a window is an object")
			}
			var fields args
			if err := json.Unmarshal(raw, &fields); err != nil {
				return nil, errBadRequest("a window is an object")
			}
			for name := range fields {
				switch name {
				case "window_label", "layout":
				default:
					return nil, errBadRequest("a window has no %q", name)
				}
			}
			if aerr := validText(w.WindowLabel, 128); aerr != nil {
				return nil, aerr
			}
			if w.tree, aerr = parseTree(w.Layout); aerr != nil {
				return nil, aerr
			}
			windows = append(windows, w)
		}
	}
	for name := range a {
		switch name {
		case "label", "initial_windows":
		default:
			return nil, errBadRequest("session.create has no %q", name)
		}
	}
	res, aerr := c.g.command(rex.LayoutCommand{Op: "space.create", Name: strings.TrimSpace(label)})
	if aerr != nil {
		return nil, aerr
	}
	space := res.Space
	if space == "" {
		return nil, errInternal("GoRex made no workspace")
	}
	fail := func(aerr *apiError) (any, *apiError) {
		c.g.command(rex.LayoutCommand{Op: "space.close", Space: space})
		return nil, aerr
	}
	out := createResult{SessionID: sessionID(space)}
	for i, w := range windows {
		sids, aerr := c.g.buildWindow(space, strings.TrimSpace(w.WindowLabel), w.tree, i == len(windows)-1, "")
		if aerr != nil {
			return fail(aerr)
		}
		// The window's first block has the focus, as a new window's has.
		if len(sids) > 1 {
			if _, aerr := c.g.command(rex.LayoutCommand{Op: "pane.focus", Space: space, SID: sids[0]}); aerr != nil {
				return fail(aerr)
			}
		}
		w.ids = paneIDs(sids)
		windows[i] = w
	}
	m, aerr := c.g.afterCommand(func(m *model) bool { return m.sessionOfSpace(space) != nil })
	if aerr != nil {
		return fail(aerr)
	}
	s := m.sessionOfSpace(space)
	if s == nil {
		return fail(errInternal("GoRex made no session"))
	}
	var created []string
	for _, w := range windows {
		created = append(created, w.ids...)
	}
	c.g.emitOwnerChanges(c.g.claimCreated(c.rec, s, created))
	if fresh, err := c.g.rebuild(); err == nil {
		m = fresh
		if now := m.sessionOfSpace(space); now != nil {
			s = now
		}
	}
	// The windows are the model's, in the order the tabs were made.
	for i := range windows {
		if i >= len(s.windows) {
			break
		}
		out.InitialWindows = append(out.InitialWindows, initialWindow{
			WindowID: s.windows[i].id,
			BlockIDs: s.windows[i].blockIDs(),
			Revision: s.revision,
		})
	}
	return out, nil
}

// blockIDs returns the IDs of a window's blocks in layout order.
func (w *mWindow) blockIDs() []string {
	out := make([]string, len(w.blocks))
	for i, b := range w.blocks {
		out[i] = b.id
	}
	return out
}

// methodSessionDestroy destroys a session and its blocks.
func methodSessionDestroy(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	if _, aerr := c.g.command(rex.LayoutCommand{Op: "space.close", Space: s.space}); aerr != nil {
		return nil, aerr
	}
	if _, aerr := c.g.afterCommand(func(m *model) bool { return m.byID[s.id] == nil }); aerr != nil {
		return nil, aerr
	}
	c.g.removeSessionTunnels(s.id)
	return struct{}{}, nil
}

// revisionResult is a result that carries a session's revision.
type revisionResult struct {
	Revision uint64 `json:"revision"`
}

// methodSessionSetLabel sets a session's label, which is its workspace's
// name.
func methodSessionSetLabel(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	label, _, aerr := a.str("label")
	if aerr != nil {
		return nil, aerr
	}
	label = strings.TrimSpace(label)
	if label != "" {
		if aerr := validText(label, 128); aerr != nil {
			return nil, aerr
		}
	}
	if label == s.label {
		return revisionResult{Revision: s.revision}, nil
	}
	if _, aerr := c.g.command(rex.LayoutCommand{Op: "space.rename", Space: s.space, Name: label}); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		return now == nil || now.label == label
	})
	if aerr != nil {
		return nil, aerr
	}
	return revisionResult{Revision: revisionOf(m, s.id)}, nil
}

// newWindowResult is session.new_window's and session.new_split's result.
type newWindowResult struct {
	WindowID string   `json:"window_id"`
	BlockIDs []string `json:"block_ids"`
	Revision uint64   `json:"revision"`
}

// methodSessionNewWindow creates a window, which is a GoRex tab.
func methodSessionNewWindow(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	label, _, aerr := a.str("window_label")
	if aerr != nil {
		return nil, aerr
	}
	if label != "" {
		if aerr := validText(label, 128); aerr != nil {
			return nil, aerr
		}
	}
	tree, aerr := parseTree(a["layout"])
	if aerr != nil {
		return nil, aerr
	}
	focus, set, aerr := a.boolean("focus")
	if aerr != nil {
		return nil, aerr
	}
	if !set {
		focus = true
	}
	// The window the focus goes back to when the new one is not active.
	prev := s.focused()
	sids, aerr := c.g.buildWindow(s.space, strings.TrimSpace(label), tree, focus, "")
	if aerr != nil {
		return nil, aerr
	}
	ids := paneIDs(sids)
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		if now == nil {
			return false
		}
		for _, w := range now.windows {
			if len(w.blocks) > 0 && w.blocks[0].id == ids[0] {
				return true
			}
		}
		return false
	})
	if aerr != nil {
		return nil, aerr
	}
	now := m.byID[s.id]
	var w *mWindow
	for _, cand := range now.windows {
		if len(cand.blocks) > 0 && cand.blocks[0].id == ids[0] {
			w = cand
			break
		}
	}
	if w == nil {
		return nil, errInternal("GoRex did not show the new window")
	}
	// The new window's first block has the focus. A window the call does
	// not make active gets it while its own return is made, then gives
	// the active window back.
	if w.focused != ids[0] {
		if _, aerr := c.g.command(rex.LayoutCommand{Op: "pane.focus", Space: s.space, SID: sids[0]}); aerr != nil {
			return nil, aerr
		}
		if !focus && prev != nil {
			if _, aerr := c.g.command(rex.LayoutCommand{Op: "pane.focus", Space: s.space, SID: prev.sid}); aerr != nil {
				return nil, aerr
			}
		}
		// The focus commands ran after afterCommand saw the window: read
		// the layout once more, so the revision answered is theirs.
		if fresh, aerr := c.g.afterCommand(nil); aerr == nil {
			m = fresh
		}
	}
	return newWindowResult{WindowID: w.id, BlockIDs: ids, Revision: revisionOf(m, s.id)}, nil
}

// splitResult is session.new_split's result.
type splitResult struct {
	AnchorID string   `json:"anchor_id"`
	BlockIDs []string `json:"block_ids"`
	SplitIDs []string `json:"split_ids"`
	Revision uint64   `json:"revision"`
}

// methodSessionNewSplit creates blocks in a new split beside a block.
func methodSessionNewSplit(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	anchorID, hasAnchor, aerr := a.str("anchor_id")
	if aerr != nil {
		return nil, aerr
	}
	windowID, hasWindow, aerr := a.str("window_id")
	if aerr != nil {
		return nil, aerr
	}
	if hasAnchor && hasWindow && anchorID != "" && windowID != "" {
		return nil, errBadRequest("send anchor_id or window_id, not both")
	}
	direction, aerr := a.reqStr("direction")
	if aerr != nil {
		return nil, aerr
	}
	var vertical bool
	switch direction {
	case "horizontal":
	case "vertical":
		vertical = true
	default:
		return nil, errBadRequest("direction is horizontal or vertical")
	}
	side, aerr := a.reqStr("side")
	if aerr != nil {
		return nil, aerr
	}
	if side != "before" && side != "after" {
		return nil, errBadRequest("side is before or after")
	}
	ratio := 0.5
	if r, ok, aerr := a.number("ratio"); aerr != nil {
		return nil, aerr
	} else if ok {
		if r <= 0 || r >= 1 || math.IsNaN(r) {
			return nil, errBadRequest("ratio is greater than 0 and less than 1")
		}
		ratio = r
	}
	tree, aerr := parseTree(a["layout"])
	if aerr != nil {
		return nil, aerr
	}
	focus, _, aerr := a.boolean("focus")
	if aerr != nil {
		return nil, aerr
	}
	var anchor *mBlock
	switch {
	case anchorID != "":
		anchor = s.findBlock(anchorID)
		if anchor == nil {
			// A split may be named, which GoRex cannot split: its place
			// has no command of its own.
			if w := s.windowBySplit(anchorID); w != nil {
				return nil, errConflict("GoRex cannot split a split; split one of its blocks")
			}
			return nil, errNotFound("no block %s in %s", anchorID, s.id)
		}
	case windowID != "":
		w := s.window(windowID)
		if w == nil {
			return nil, errNotFound("no window %s in %s", windowID, s.id)
		}
		anchor = w.focusedBlock()
	default:
		w := s.window(s.active)
		if w == nil {
			return nil, errConflict("%s has no windows", s.id)
		}
		anchor = w.focusedBlock()
	}
	if anchor == nil {
		return nil, errConflict("%s has no focused block", s.id)
	}
	if side == "before" && !tree.isSingle() {
		return nil, errConflict("GoRex cannot place a split before a block; use side after")
	}
	prev := s.focused()
	before, aerr := c.g.splitsOf(s)
	if aerr != nil {
		return nil, aerr
	}
	sids, aerr := c.g.buildBeside(s, anchor, tree, vertical, ratio, focus)
	if aerr != nil {
		return nil, aerr
	}
	ids := paneIDs(sids)
	if side == "before" {
		if _, aerr := c.g.command(rex.LayoutCommand{Op: "pane.swap", Space: s.space, SID: anchor.sid, Target: sids[0]}); aerr != nil {
			return nil, aerr
		}
		// The swap moves the blocks, not the split: the new block holds
		// the before part, and its split keeps the ratio.
	}
	if focus {
		if _, aerr := c.g.command(rex.LayoutCommand{Op: "pane.focus", Space: s.space, SID: sids[0]}); aerr != nil {
			return nil, aerr
		}
	} else if prev != nil {
		// The app gives the new block the focus; the call gives it back.
		if _, aerr := c.g.command(rex.LayoutCommand{Op: "pane.focus", Space: s.space, SID: prev.sid}); aerr != nil {
			return nil, aerr
		}
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		if now == nil {
			return false
		}
		for _, b := range ids {
			if now.findBlock(b) == nil {
				return false
			}
		}
		return true
	})
	if aerr != nil {
		return nil, aerr
	}
	now := m.byID[s.id]
	created := newSplits(before, now)
	sortSplits(created, anchorSplits(now, anchor.id, ids))
	anchorOut := anchorID
	if anchorOut == "" {
		anchorOut = anchor.id
	}
	return splitResult{AnchorID: anchorOut, BlockIDs: ids, SplitIDs: created, Revision: revisionOf(m, s.id)}, nil
}

// buildBeside asks the app for blocks beside anchor: a split of the
// anchor's space, and the trees' blocks under it.
func (g *Gateway) buildBeside(s *mSession, anchor *mBlock, tree *specTree, vertical bool, ratio float64, focus bool) ([]string, *apiError) {
	beside := &specTree{vertical: vertical, ratio: ratio, a: &specTree{leaf: true}, b: tree}
	sids, err := rex.SplitTree(beside, anchor.sid, (*specTree).split, g.splitIn(s.space))
	if err != nil {
		return nil, err.(*apiError)
	}
	g.applyShellOptions(sids[1:], tree.leaves())
	return sids[1:], nil
}

// splitsOf returns every split ID of a session's active view.
func (g *Gateway) splitsOf(s *mSession) (map[string]bool, *apiError) {
	m, aerr := g.modelReq()
	if aerr != nil {
		return nil, aerr
	}
	out := map[string]bool{}
	if now := m.byID[s.id]; now != nil {
		for _, w := range now.windows {
			for _, id := range splitIDs(w.layout) {
				out[id] = true
			}
		}
	}
	return out, nil
}

// newSplits returns the split IDs a change added to a session, in tree
// order.
func newSplits(before map[string]bool, s *mSession) []string {
	var out []string
	if s == nil {
		return out
	}
	for _, w := range s.windows {
		for _, id := range splitIDs(w.layout) {
			if !before[id] {
				out = append(out, id)
			}
		}
	}
	return out
}

// anchorSplits moves the splits around the anchor to the front of ids.
func sortSplits(ids []string, around []string) {
	rank := map[string]int{}
	for i, id := range around {
		rank[id] = i
	}
	sort.SliceStable(ids, func(i, j int) bool {
		ri, iok := rank[ids[i]]
		rj, jok := rank[ids[j]]
		switch {
		case iok && jok:
			return ri < rj
		case iok:
			return true
		case jok:
			return false
		}
		return false
	})
}

// anchorSplits returns the splits near the anchor and the new blocks,
// innermost first.
func anchorSplits(s *mSession, anchor string, newIDs []string) []string {
	var out []string
	for _, w := range s.windows {
		if !containsBlock(w.layout, anchor) {
			continue
		}
		for _, id := range newIDs {
			if sp := lowestSplit(w.layout, anchor, id); sp != nil {
				out = append(out, sp.SplitID)
			}
		}
	}
	return out
}

// windowBySplit returns the window that holds a split.
func (s *mSession) windowBySplit(id string) *mWindow {
	for _, w := range s.windows {
		if splitByID(w.layout, id) != nil {
			return w
		}
	}
	return nil
}

// focusedBlock returns the window's focused block, or its first.
func (w *mWindow) focusedBlock() *mBlock {
	if w == nil {
		return nil
	}
	if w.focused != "" {
		for _, b := range w.allBlocks() {
			if b.id == w.focused {
				return b
			}
		}
	}
	if len(w.blocks) > 0 {
		return w.blocks[0]
	}
	return nil
}

// methodSessionCloseWindow closes a window and its blocks.
func methodSessionCloseWindow(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	windowID, _, aerr := a.str("window_id")
	if aerr != nil {
		return nil, aerr
	}
	if windowID == "" {
		windowID = s.active
	}
	w := s.window(windowID)
	if w == nil {
		if windowID == "" {
			return nil, errConflict("%s has no windows", s.id)
		}
		return nil, errNotFound("no window %s in %s", windowID, s.id)
	}
	first := w.focusedBlock()
	if first == nil {
		return nil, errNotFound("window %s has no block", windowID)
	}
	closed, hadFocus := c.lastFocus()
	if _, aerr := c.g.command(rex.LayoutCommand{Op: "tab.close", Space: s.space, Tab: w.tab, SID: first.sid}); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		return now == nil || now.window(windowID) == nil
	})
	if aerr != nil {
		return nil, aerr
	}
	if hadFocus && closed.session == s.id && closed.window == windowID {
		m = c.focusBack(m, closed)
	}
	return revisionResult{Revision: revisionOf(m, s.id)}, nil
}

// methodSessionSetWindowLabel sets a window's label, which is a tab's
// name.
func methodSessionSetWindowLabel(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	windowID, aerr := a.reqStr("window_id")
	if aerr != nil {
		return nil, aerr
	}
	label, _, aerr := a.str("label")
	if aerr != nil {
		return nil, aerr
	}
	label = strings.TrimSpace(label)
	if label != "" {
		if aerr := validText(label, 128); aerr != nil {
			return nil, aerr
		}
	}
	w := s.window(windowID)
	if w == nil {
		return nil, errNotFound("no window %s in %s", windowID, s.id)
	}
	first := w.focusedBlock()
	if first == nil {
		return nil, errNotFound("window %s has no block", windowID)
	}
	if _, aerr := c.g.command(rex.LayoutCommand{Op: "tab.rename", Space: s.space, Tab: w.tab, SID: first.sid, Name: label}); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		if now == nil {
			return true
		}
		nw := now.window(windowID)
		if nw == nil {
			return true
		}
		if label == "" {
			return strings.HasPrefix(nw.label, "Window ")
		}
		return nw.label == label
	})
	if aerr != nil {
		return nil, aerr
	}
	return revisionResult{Revision: revisionOf(m, s.id)}, nil
}

// methodSessionSetBlockLabel sets a block's label, which is a pane's
// name.
func methodSessionSetBlockLabel(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	blockID, present, aerr := a.str("block_id")
	if aerr != nil {
		return nil, aerr
	}
	if !present || blockID == "" {
		return nil, errBadRequest("block_id is required")
	}
	label, _, aerr := a.str("label")
	if aerr != nil {
		return nil, aerr
	}
	label = strings.TrimSpace(label)
	if label != "" {
		if aerr := validText(label, 128); aerr != nil {
			return nil, aerr
		}
	}
	b := s.findBlock(blockID)
	if b == nil {
		return nil, errNotFound("no block %s in %s", blockID, s.id)
	}
	if _, aerr := c.g.command(rex.LayoutCommand{Op: "pane.rename", Space: s.space, SID: b.sid, Name: label}); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		if now == nil {
			return true
		}
		nb := now.findBlock(blockID)
		if nb == nil {
			return true
		}
		if label == "" {
			return nb.label == "shell"
		}
		return nb.label == label
	})
	if aerr != nil {
		return nil, aerr
	}
	return revisionResult{Revision: revisionOf(m, s.id)}, nil
}

// moveResult is session.move_block's result.
type moveResult struct {
	CreatedSplitID string  `json:"created_split_id"`
	RemovedSplitID *string `json:"removed_split_id,omitempty"`
	Revision       uint64  `json:"revision"`
}

// methodSessionMoveBlock moves a block beside another.
func methodSessionMoveBlock(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	blockID, aerr := a.reqStr("block_id")
	if aerr != nil {
		return nil, aerr
	}
	anchorID, aerr := a.reqStr("anchor_block_id")
	if aerr != nil {
		return nil, aerr
	}
	if blockID == anchorID {
		return nil, errBadRequest("the block and the anchor are the same")
	}
	direction, _, aerr := a.str("direction")
	if aerr != nil {
		return nil, aerr
	}
	vertical := direction == "vertical"
	if direction != "" && direction != "horizontal" && direction != "vertical" {
		return nil, errBadRequest("direction is horizontal or vertical")
	}
	side, _, aerr := a.str("side")
	if aerr != nil {
		return nil, aerr
	}
	if side != "" && side != "before" && side != "after" {
		return nil, errBadRequest("side is before or after")
	}
	ratio := 0.5
	if r, ok, aerr := a.number("ratio"); aerr != nil {
		return nil, aerr
	} else if ok {
		if r <= 0 || r >= 1 || math.IsNaN(r) {
			return nil, errBadRequest("ratio is greater than 0 and less than 1")
		}
		ratio = r
	}
	b, anchor := s.findBlock(blockID), s.findBlock(anchorID)
	if b == nil {
		return nil, errNotFound("no block %s in %s", blockID, s.id)
	}
	if anchor == nil {
		return nil, errNotFound("no block %s in %s", anchorID, s.id)
	}
	// The app's pane.move takes the side of the anchor the block goes on.
	dir := "right"
	switch {
	case vertical && side == "before":
		dir = "up"
	case vertical:
		dir = "down"
	case side == "before":
		dir = "left"
	}
	var held string
	if w := s.windowOf(b); w != nil {
		held = parentSplit(w.layout, blockID)
	}
	res, aerr := c.g.command(rex.LayoutCommand{
		Op: "pane.move", Space: s.space, SID: b.sid, Target: anchor.sid,
		Direction: dir, Ratio: float32(ratio),
	})
	if aerr != nil {
		return nil, aerr
	}
	if !res.Changed {
		switch res.Reason {
		case "not_found":
			return nil, errNotFound("one of the blocks is gone")
		case "same_pane":
			return nil, errBadRequest("the block and the anchor are the same")
		case "same_tab":
			// A GoRex server before same-window moves.
			return nil, errConflict("the block is already in the window")
		case "zoomed_tab":
			return nil, errConflict("a window is zoomed; unzoom it first")
		}
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		return now == nil || besideAnchor(now, blockID, anchorID, vertical, side == "before")
	})
	if aerr != nil {
		return nil, aerr
	}
	out := moveResult{Revision: revisionOf(m, s.id)}
	now := m.byID[s.id]
	if now == nil {
		return out, nil
	}
	if w := now.windowOf(now.findBlock(blockID)); w != nil {
		if sp := lowestSplit(w.layout, blockID, anchorID); sp != nil {
			out.CreatedSplitID = sp.SplitID
		}
	}
	// The split that held the block is removed when no split of its ID,
	// which names its shape, is left.
	if held != "" && !now.hasSplit(held) {
		out.RemovedSplitID = &held
	}
	return out, nil
}

// hasSplit tells whether a window of s has the split of an ID.
func (s *mSession) hasSplit(id string) bool {
	for _, w := range s.windows {
		if splitByID(w.layout, id) != nil {
			return true
		}
	}
	return false
}

// besideAnchor tells whether a session shows block in a split of its
// own with anchor, on the side asked.
func besideAnchor(s *mSession, block, anchor string, vertical, before bool) bool {
	w := s.windowOf(s.findBlock(block))
	if w == nil || w != s.windowOf(s.findBlock(anchor)) {
		return false
	}
	sp := lowestSplit(w.layout, block, anchor)
	if sp == nil || (sp.Direction == "vertical") != vertical {
		return false
	}
	first, second := anchor, block
	if before {
		first, second = block, anchor
	}
	return sp.Before != nil && sp.Before.BlockID == first && sp.After != nil && sp.After.BlockID == second
}

// parentSplit returns the ID of the split one of whose sides is block,
// "" when block is a whole layout or not in it.
func parentSplit(l *layoutView, block string) string {
	if l == nil || l.Split == nil {
		return ""
	}
	for _, side := range []*layoutView{l.Split.Before, l.Split.After} {
		if side != nil && side.BlockID == block {
			return l.Split.SplitID
		}
	}
	if id := parentSplit(l.Split.Before, block); id != "" {
		return id
	}
	return parentSplit(l.Split.After, block)
}

// methodSessionResizePane moves the border between two blocks.
func methodSessionResizePane(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	direction, aerr := a.reqStr("direction")
	if aerr != nil {
		return nil, aerr
	}
	vertical := direction == "vertical"
	if direction != "horizontal" && direction != "vertical" {
		return nil, errBadRequest("direction is horizontal or vertical")
	}
	beforeID, aerr := a.reqStr("before_block_id")
	if aerr != nil {
		return nil, aerr
	}
	afterID, aerr := a.reqStr("after_block_id")
	if aerr != nil {
		return nil, aerr
	}
	if beforeID == afterID {
		return nil, errBadRequest("the blocks are the same")
	}
	ratio, ok, aerr := a.number("ratio")
	if aerr != nil {
		return nil, aerr
	}
	if !ok || ratio <= 0 || ratio >= 1 || math.IsNaN(ratio) {
		return nil, errBadRequest("ratio is greater than 0 and less than 1")
	}
	before, after := s.findBlock(beforeID), s.findBlock(afterID)
	if before == nil {
		return nil, errNotFound("no block %s in %s", beforeID, s.id)
	}
	if after == nil {
		return nil, errNotFound("no block %s in %s", afterID, s.id)
	}
	if before.window != after.window {
		return nil, errBadRequest("the blocks are in different windows")
	}
	w := s.window(before.window)
	sp := lowestSplit(w.layout, beforeID, afterID)
	if sp == nil {
		return nil, errBadRequest("no split lies between the blocks")
	}
	if (sp.Direction == "vertical") != vertical {
		return nil, errBadRequest("the split between the blocks is %s", sp.Direction)
	}
	if !inSplitSide(sp, beforeID, afterID) {
		return nil, errBadRequest("the before block is not before the after block")
	}
	current := sp.Ratio
	if math.Abs(ratio-current) < 0.001 {
		return revisionResult{Revision: s.revision}, nil
	}
	// The app moves the split beside the edge of a block it is given,
	// which is the one Rex means when that split is the border's: the
	// blocks on either side of the border, not the pair's, which may lie
	// further from it. Either is tried, in the direction that moves the
	// border towards ratio.
	last, first := sp.Before, sp.After
	for last.Split != nil {
		last = last.Split.After
	}
	for first.Split != nil {
		first = first.Split.Before
	}
	before, after = s.findBlock(last.BlockID), s.findBlock(first.BlockID)
	beforeLeaf, afterLeaf := findFnode(w.tree, before.sid), findFnode(w.tree, after.sid)
	grows := ratio > current
	sid, moved := before.sid, appResizeSplit(w.tree, beforeLeaf, vertical, grows)
	if moved == nil || moved.split != sp.SplitID {
		sid, moved = after.sid, appResizeSplit(w.tree, afterLeaf, vertical, grows)
	}
	if beforeLeaf == nil || afterLeaf == nil || moved == nil || moved.split != sp.SplitID {
		return nil, errConflict("GoRex cannot move that border; it moves the split beside a block")
	}
	dir, amount := ratioCommand(vertical, current, ratio)
	res, aerr := c.g.command(rex.LayoutCommand{
		Op: "pane.resize", Space: s.space, SID: sid, Direction: dir, Amount: amount,
	})
	if aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		if now == nil {
			return true
		}
		nw := now.window(before.window)
		nsp := lowestSplit(nw.layout, beforeID, afterID)
		return nsp == nil || math.Abs(nsp.Ratio-ratio) < 0.01
	})
	if aerr != nil {
		return nil, aerr
	}
	_ = res
	return revisionResult{Revision: revisionOf(m, s.id)}, nil
}

// inSplitSide tells whether before is on the split's before side.
func inSplitSide(sp *splitView, before, after string) bool {
	return containsBlock(sp.Before, before) && containsBlock(sp.After, after)
}

// methodSessionMoveWindow moves a window in the session's order.
func methodSessionMoveWindow(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	windowID, present, aerr := a.str("window_id")
	if aerr != nil {
		return nil, aerr
	}
	if !present || windowID == "" {
		return nil, errBadRequest("window_id is required")
	}
	beforeID, _, aerr := a.str("before_window_id")
	if aerr != nil {
		return nil, aerr
	}
	if beforeID == windowID {
		return nil, errBadRequest("the window and the one it goes before are the same")
	}
	w := s.window(windowID)
	if w == nil {
		return nil, errNotFound("no window %s in %s", windowID, s.id)
	}
	cur := 0
	for i, other := range s.windows {
		if other.id == windowID {
			cur = i
		}
	}
	want := len(s.windows) - 1
	if beforeID != "" {
		before := s.window(beforeID)
		if before == nil {
			return nil, errNotFound("no window %s in %s", beforeID, s.id)
		}
		want = 0
		for i, other := range s.windows {
			if other.id == beforeID {
				want = i
			}
		}
		// The wanted place is among the windows without the one moving.
		if want > cur {
			want--
		}
	}
	if cur == want {
		return revisionResult{Revision: s.revision}, nil
	}
	first := w.focusedBlock()
	if first == nil {
		return nil, errNotFound("window %s has no block", windowID)
	}
	index := appTabIndex(cur, want, len(s.windows))
	if _, aerr := c.g.command(rex.LayoutCommand{Op: "tab.move", Space: s.space, Tab: w.tab, SID: first.sid, Index: index}); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		if now == nil {
			return true
		}
		for i, other := range now.windows {
			if other.id == windowID {
				return i == want
			}
		}
		return true
	})
	if aerr != nil {
		return nil, aerr
	}
	return revisionResult{Revision: revisionOf(m, s.id)}, nil
}

// appTabIndex returns the index tab.move takes for a tab at cur that
// goes to want among the tabs without it: the app counts the index with
// the tab still in its place, then takes one off for the tab leaving.
func appTabIndex(cur, want, n int) int {
	if want <= cur {
		return want
	}
	return min(want+1, n)
}
