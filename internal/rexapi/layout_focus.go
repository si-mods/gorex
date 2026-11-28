package rexapi

import (
	"strings"

	"gorex/internal/rex"
)

// focusBlock asks the app to focus a block, and waits for the view to
// show it.
func (c *control) focusBlock(s *mSession, b *mBlock) (*model, *apiError) {
	if _, aerr := c.g.command(rex.LayoutCommand{Op: "pane.focus", Space: s.space, SID: b.sid}); aerr != nil {
		return nil, aerr
	}
	return c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		if now == nil {
			return true
		}
		w := now.windowOf(now.findBlock(b.id))
		return w != nil && w.focused == b.id
	})
}

// methodSessionFocusBlock focuses a block.
func methodSessionFocusBlock(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	blockID, aerr := a.reqStr("block_id")
	if aerr != nil {
		return nil, aerr
	}
	b := s.findBlock(blockID)
	if b == nil {
		return nil, errNotFound("no block %s in %s", blockID, s.id)
	}
	if s.active == b.window {
		if w := s.window(b.window); w != nil && w.focused == b.id {
			return revisionResult{Revision: s.revision}, nil
		}
	}
	m, aerr := c.focusBlock(s, b)
	if aerr != nil {
		return nil, aerr
	}
	return revisionResult{Revision: revisionOf(m, s.id)}, nil
}

// methodSessionFocusWindow makes a window active.
func methodSessionFocusWindow(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	windowID, aerr := a.reqStr("window_id")
	if aerr != nil {
		return nil, aerr
	}
	w := s.window(windowID)
	if w == nil {
		return nil, errNotFound("no window %s in %s", windowID, s.id)
	}
	if s.active == windowID {
		return revisionResult{Revision: s.revision}, nil
	}
	b := w.focusedBlock()
	if b == nil {
		return nil, errNotFound("window %s has no block", windowID)
	}
	if _, aerr := c.g.command(rex.LayoutCommand{Op: "pane.focus", Space: s.space, SID: b.sid}); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		return now == nil || now.active == windowID
	})
	if aerr != nil {
		return nil, aerr
	}
	return revisionResult{Revision: revisionOf(m, s.id)}, nil
}

// methodSessionFocusDirection moves the focus to the nearest block in a
// direction.
func methodSessionFocusDirection(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	direction, aerr := a.reqStr("direction")
	if aerr != nil {
		return nil, aerr
	}
	switch direction {
	case "left", "right", "up", "down":
	default:
		return nil, errBadRequest("direction is left, right, up or down")
	}
	wrap, _, aerr := a.boolean("wrap")
	if aerr != nil {
		return nil, aerr
	}
	w := s.window(s.active)
	if w == nil {
		return nil, errConflict("%s has no windows", s.id)
	}
	from := w.focusedBlock()
	if from == nil {
		return nil, errConflict("%s has no focused block", s.id)
	}
	next, ok := w.neighbor(from, direction, wrap)
	if !ok {
		return revisionResult{Revision: s.revision}, nil
	}
	m, aerr := c.focusBlock(s, next)
	if aerr != nil {
		return nil, aerr
	}
	return revisionResult{Revision: revisionOf(m, s.id)}, nil
}

// methodSessionFocusNextPane focuses the next block of the active window.
func methodSessionFocusNextPane(c *control, a args) (any, *apiError) {
	return c.focusSiblingPane(a, true)
}

// methodSessionFocusPreviousPane focuses the previous block of the
// active window.
func methodSessionFocusPreviousPane(c *control, a args) (any, *apiError) {
	return c.focusSiblingPane(a, false)
}

// focusSiblingPane moves the focus one block along the active window's
// layout order.
func (c *control) focusSiblingPane(a args, next bool) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	w := s.window(s.active)
	if w == nil {
		return nil, errConflict("%s has no windows", s.id)
	}
	from := w.focusedBlock()
	at := -1
	for i, b := range w.blocks {
		if from != nil && b.id == from.id {
			at = i
			break
		}
	}
	switch {
	case at < 0 && next:
		at = 0
	case at < 0:
		at = len(w.blocks) - 1
	case next:
		at++
	default:
		at--
	}
	if at < 0 || at >= len(w.blocks) {
		return revisionResult{Revision: s.revision}, nil
	}
	m, aerr := c.focusBlock(s, w.blocks[at])
	if aerr != nil {
		return nil, aerr
	}
	return revisionResult{Revision: revisionOf(m, s.id)}, nil
}

// methodSessionFocusNextWindow makes the next window active.
func methodSessionFocusNextWindow(c *control, a args) (any, *apiError) {
	return c.focusSiblingWindow(a, true)
}

// methodSessionFocusPreviousWindow makes the previous window active.
func methodSessionFocusPreviousWindow(c *control, a args) (any, *apiError) {
	return c.focusSiblingWindow(a, false)
}

// focusSiblingWindow makes the window after or before the active one
// active.
func (c *control) focusSiblingWindow(a args, next bool) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	if len(s.windows) == 0 {
		return nil, errConflict("%s has no windows", s.id)
	}
	at := 0
	for i, w := range s.windows {
		if w.id == s.active {
			at = i
			break
		}
	}
	if next {
		at++
	} else {
		at--
	}
	if at < 0 || at >= len(s.windows) {
		return revisionResult{Revision: s.revision}, nil
	}
	b := s.windows[at].focusedBlock()
	if b == nil {
		return revisionResult{Revision: s.revision}, nil
	}
	if _, aerr := c.g.command(rex.LayoutCommand{Op: "pane.focus", Space: s.space, SID: b.sid}); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		return now == nil || now.active == s.windows[at].id
	})
	if aerr != nil {
		return nil, aerr
	}
	return revisionResult{Revision: revisionOf(m, s.id)}, nil
}

// zoomResult is session.zoom_block's result.
type zoomResult struct {
	BlockID  string `json:"block_id"`
	Zoomed   bool   `json:"zoomed"`
	Revision uint64 `json:"revision"`
}

// methodSessionZoomBlock zooms a block, or restores the layout.
func methodSessionZoomBlock(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	blockID, hasBlock, aerr := a.str("block_id")
	if aerr != nil {
		return nil, aerr
	}
	windowID, hasWindow, aerr := a.str("window_id")
	if aerr != nil {
		return nil, aerr
	}
	if hasBlock && hasWindow && blockID != "" && windowID != "" {
		return nil, errBadRequest("send block_id or window_id, not both")
	}
	zoom, hasZoom, aerr := a.boolean("zoom")
	if aerr != nil {
		return nil, aerr
	}
	var b *mBlock
	switch {
	case blockID != "":
		b = s.findBlock(blockID)
		if b == nil {
			return nil, errNotFound("no block %s in %s", blockID, s.id)
		}
		if _, aerr := c.focusBlock(s, b); aerr != nil {
			return nil, aerr
		}
	case windowID != "":
		w := s.window(windowID)
		if w == nil {
			return nil, errNotFound("no window %s in %s", windowID, s.id)
		}
		b = w.focusedBlock()
		if b == nil {
			return nil, errNotFound("window %s has no block", windowID)
		}
	default:
		w := s.window(s.active)
		if w == nil {
			return nil, errConflict("%s has no windows", s.id)
		}
		b = w.focusedBlock()
		if b == nil {
			return nil, errConflict("%s has no focused block", s.id)
		}
	}
	mode := ""
	if hasZoom {
		if zoom {
			mode = "on"
		} else {
			mode = "off"
		}
	}
	res, aerr := c.g.command(rex.LayoutCommand{Op: "pane.zoom", Space: s.space, SID: b.sid, Mode: mode})
	if aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		if now == nil {
			return true
		}
		w := now.windowOf(now.findBlock(b.id))
		if w == nil {
			return true
		}
		if res.Zoomed {
			return w.zoomed == b.id
		}
		return w.zoomed != b.id
	})
	if aerr != nil {
		return nil, aerr
	}
	return zoomResult{BlockID: b.id, Zoomed: res.Zoomed, Revision: revisionOf(m, s.id)}, nil
}

// methodSessionSwapBlocks swaps the places of two blocks.
func methodSessionSwapBlocks(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	blockID, aerr := a.reqStr("block_id")
	if aerr != nil {
		return nil, aerr
	}
	otherID, aerr := a.reqStr("other_block_id")
	if aerr != nil {
		return nil, aerr
	}
	if blockID == otherID {
		return nil, errBadRequest("the blocks are the same")
	}
	b, other := s.findBlock(blockID), s.findBlock(otherID)
	if b == nil {
		return nil, errNotFound("no block %s in %s", blockID, s.id)
	}
	if other == nil {
		return nil, errNotFound("no block %s in %s", otherID, s.id)
	}
	if b.window != other.window {
		return nil, errConflict("GoRex swaps blocks in one window; move one to the other's window first")
	}
	before := windowShape(s.window(b.window))
	prev := ""
	if f := s.focused(); f != nil && f.window == b.window {
		prev = f.id
	}
	res, aerr := c.g.command(rex.LayoutCommand{Op: "pane.swap", Space: s.space, SID: b.sid, Target: other.sid})
	if aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		if now == nil {
			return true
		}
		return windowShape(now.window(b.window)) != before
	})
	if aerr != nil {
		return nil, aerr
	}
	if !res.Changed {
		switch res.Reason {
		case "not_found":
			return nil, errNotFound("one of the blocks is gone")
		case "same_pane":
			return nil, errBadRequest("the blocks are the same")
		case "cross_tab":
			return nil, errConflict("GoRex swaps blocks in one window")
		}
	} else if prev != "" && prev != b.id {
		// The app gives the focus to the block it swapped, which moves it
		// back where the focus was if that is not one of the two.
		if f := s.findBlock(prev); f != nil {
			if _, aerr := c.focusBlock(s, f); aerr != nil {
				return nil, aerr
			}
			if m, aerr := c.g.modelReq(); aerr == nil {
				return revisionResult{Revision: revisionOf(m, s.id)}, nil
			}
		}
	}
	return revisionResult{Revision: revisionOf(m, s.id)}, nil
}

// windowShape names the blocks of a window in layout order.
func windowShape(w *mWindow) string {
	if w == nil {
		return ""
	}
	var b strings.Builder
	for _, block := range w.blocks {
		b.WriteString(block.id)
		b.WriteByte(',')
	}
	return b.String()
}
