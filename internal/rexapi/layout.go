package rexapi

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"gorex/internal/rex"
)

// specTree is a layout a request asks for: one new block of the shell
// flavor, or a split of two such trees.
type specTree struct {
	leaf     bool
	label    string
	cwd      string
	vertical bool
	ratio    float64
	a, b     *specTree
}

// specLeaf is one block of a specTree, and the splits above it.
type specLeaf struct {
	tree *specTree
	path []specStep
}

// specStep is a split on the way to a block.
type specStep struct {
	vertical bool
	ratio    float64
	after    bool
}

// parseTree reads a layout node of session.create, session.new_window or
// session.new_split. It fails with bad_request when the node is
// malformed, and with not_found when a flavor is not one GoRex makes.
func parseTree(raw json.RawMessage) (*specTree, *apiError) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errBadRequest("layout is required")
	}
	node, aerr := decodeNode(raw)
	if aerr != nil {
		return nil, aerr
	}
	block, hasBlock, aerr := node.obj("block")
	if aerr != nil {
		return nil, aerr
	}
	split, hasSplit, aerr := node.obj("split")
	if aerr != nil {
		return nil, aerr
	}
	for name := range node {
		if name != "block" && name != "split" {
			return nil, errBadRequest("a layout node has no %q", name)
		}
	}
	switch {
	case hasBlock == hasSplit:
		return nil, errBadRequest("a layout node holds a block or a split")
	case hasBlock:
		for name := range block {
			switch name {
			case "flavor", "options", "label":
			default:
				return nil, errBadRequest("a block has no %q", name)
			}
		}
		flavor, aerr := block.reqStr("flavor")
		if aerr != nil {
			return nil, aerr
		}
		if flavor != flavorShell {
			return nil, errNotFound("GoRex makes no blocks of %s", flavor)
		}
		t := &specTree{leaf: true}
		if label, _, aerr := block.str("label"); aerr != nil {
			return nil, aerr
		} else if label != "" {
			if aerr := validText(label, 128); aerr != nil {
				return nil, aerr
			}
			t.label = label
		}
		options, present, aerr := block.obj("options")
		if aerr != nil {
			return nil, aerr
		}
		if present && options != nil {
			for name := range options {
				if name != "cwd" {
					return nil, errBadRequest("a shell has no option %q", name)
				}
			}
			cwd, _, aerr := options.str("cwd")
			if aerr != nil {
				return nil, aerr
			}
			t.cwd = cwd
		}
		return t, nil
	}
	for name := range split {
		switch name {
		case "direction", "ratio", "before", "after":
		default:
			return nil, errBadRequest("a split has no %q", name)
		}
	}
	direction, aerr := split.reqStr("direction")
	if aerr != nil {
		return nil, aerr
	}
	var vertical bool
	switch direction {
	case "horizontal":
	case "vertical":
		vertical = true
	default:
		return nil, errBadRequest("a split's direction is horizontal or vertical")
	}
	ratio := 0.5
	if r, ok, aerr := split.number("ratio"); aerr != nil {
		return nil, aerr
	} else if ok {
		if r <= 0 || r >= 1 || math.IsNaN(r) {
			return nil, errBadRequest("a split's ratio is greater than 0 and less than 1")
		}
		ratio = r
	}
	beforeRaw, ok := split["before"]
	if !ok || string(beforeRaw) == "null" {
		return nil, errBadRequest("a split needs a before")
	}
	afterRaw, ok := split["after"]
	if !ok || string(afterRaw) == "null" {
		return nil, errBadRequest("a split needs an after")
	}
	before, aerr := parseTree(beforeRaw)
	if aerr != nil {
		return nil, aerr
	}
	after, aerr := parseTree(afterRaw)
	if aerr != nil {
		return nil, aerr
	}
	return &specTree{vertical: vertical, ratio: ratio, a: before, b: after}, nil
}

// decodeNode reads a layout node's fields as raw arguments.
func decodeNode(raw json.RawMessage) (args, *apiError) {
	var node args
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil, errBadRequest("a layout node is an object")
	}
	return node, nil
}

// leaves returns the tree's blocks in layout order, with the splits above
// them.
func (t *specTree) leaves() []specLeaf {
	var out []specLeaf
	var walk func(*specTree, []specStep)
	walk = func(n *specTree, path []specStep) {
		if n == nil {
			return
		}
		if n.leaf {
			out = append(out, specLeaf{tree: n, path: path})
			return
		}
		walk(n.a, append(append([]specStep(nil), path...), specStep{vertical: n.vertical, ratio: n.ratio}))
		walk(n.b, append(append([]specStep(nil), path...), specStep{vertical: n.vertical, ratio: n.ratio, after: true}))
	}
	walk(t, nil)
	return out
}

// isSingle tells whether the tree holds one block and no split.
func (t *specTree) isSingle() bool { return t != nil && t.leaf }

// siblings returns the split's place where two leaves part.
func siblings(a, b specLeaf) (vertical bool, ratio float64) {
	i := 0
	for i < len(a.path) && i < len(b.path) && a.path[i].after == b.path[i].after && a.path[i].vertical == b.path[i].vertical {
		i++
	}
	if i >= len(a.path) {
		return false, 0.5
	}
	return a.path[i].vertical, a.path[i].ratio
}

// ratioCommand returns the direction and amount that move a split's
// ratio from current to want, as pane.resize takes them.
func ratioCommand(vertical bool, current, want float64) (direction string, amount float32) {
	grow, shrink := "right", "left"
	if vertical {
		grow, shrink = "down", "up"
	}
	if want >= current {
		return grow, float32(want - current)
	}
	return shrink, float32(current - want)
}

// command asks the app for a change, and fails with the error code the
// docs give its answer.
func (g *Gateway) command(cmd rex.LayoutCommand) (rex.LayoutCommand, *apiError) {
	res, err := g.opt.Client.Command(cmd)
	if err != nil {
		return res, errInternal("asking the GoRex window: %v", err)
	}
	if res.Code != "" {
		return res, layoutError(g, res)
	}
	return res, nil
}

// layoutError maps the app's answer to one of Rex's errors.
func layoutError(g *Gateway, res rex.LayoutCommand) *apiError {
	switch res.Code {
	case "desktop_unavailable":
		return errConflict("Open the GoRex window on %s", g.hello.Host.Name)
	case "space_not_found", "tab_not_found", "pane_not_found", "not_found":
		return errNotFound("%s", res.Error)
	case "last_tab", "zoomed_tab", "same_tab", "same_pane", "cross_tab":
		return errConflict("%s", res.Error)
	case "create_failed":
		return errInternal("%s", res.Error)
	}
	return errConflict("%s: %s", res.Code, res.Error)
}

// afterCommand reads the layout again, and waits until shows is true, or
// the app's change is too long coming.
func (g *Gateway) afterCommand(shows func(*model) bool) (*model, *apiError) {
	m, err := g.current()
	if err != nil {
		return m, errInternal("reading the layout: %v", err)
	}
	if shows == nil || shows(m) {
		return m, nil
	}
	deadline := time.Now().Add(commandWait)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		m, err = g.current()
		if err != nil {
			return m, errInternal("reading the layout: %v", err)
		}
		if shows(m) {
			return m, nil
		}
	}
	return m, errConflict("GoRex did not show the change in time; it may still appear")
}

// revisionOf returns a session's revision in a model.
func revisionOf(m *model, sid string) uint64 {
	if s := m.byID[sid]; s != nil {
		return s.revision
	}
	return 0
}

// buildWindow asks the app for a tab of tree in a space, and returns the
// new panes' GoRex session IDs in layout order, and the app's answers.
func (g *Gateway) buildWindow(space, label string, tree *specTree, focus bool, after string) ([]string, *apiError) {
	leaves := tree.leaves()
	if len(leaves) == 0 {
		return nil, errBadRequest("the layout holds no block")
	}
	first := leaves[0].tree
	res, aerr := g.command(rex.LayoutCommand{
		Op: "tab.create", Space: space, Name: label, Dir: first.cwd, After: after, Focus: focus,
	})
	if aerr != nil {
		return nil, aerr
	}
	sids := []string{res.New}
	if res.New != "" && first.label != "" {
		if _, aerr := g.command(rex.LayoutCommand{Op: "pane.rename", Space: space, SID: res.New, Name: first.label}); aerr != nil {
			return nil, aerr
		}
	}
	for i := 1; i < len(leaves); i++ {
		vertical, ratio := siblings(leaves[i-1], leaves[i])
		res, aerr := g.command(rex.LayoutCommand{
			Op: "pane.split", Space: space, SID: sids[i-1],
			Vertical: vertical, Dir: leaves[i].tree.cwd,
		})
		if aerr != nil {
			g.command(rex.LayoutCommand{Op: "tab.close", Space: space, SID: sids[0]})
			return nil, aerr
		}
		sids = append(sids, res.New)
		if math.Abs(ratio-0.5) > 0.01 {
			direction, amount := ratioCommand(vertical, 0.5, ratio)
			if _, aerr := g.command(rex.LayoutCommand{
				Op: "pane.resize", Space: space, SID: sids[i-1], Direction: direction, Amount: amount,
			}); aerr != nil {
				g.command(rex.LayoutCommand{Op: "tab.close", Space: space, SID: sids[0]})
				return nil, aerr
			}
		}
		if leaves[i].tree.label != "" {
			if _, aerr := g.command(rex.LayoutCommand{Op: "pane.rename", Space: space, SID: sids[i], Name: leaves[i].tree.label}); aerr != nil {
				g.command(rex.LayoutCommand{Op: "tab.close", Space: space, SID: sids[0]})
				return nil, aerr
			}
		}
	}
	return sids, nil
}

// paneIDs returns the block IDs of the panes a build made.
func paneIDs(sids []string) []string {
	out := make([]string, len(sids))
	for i, sid := range sids {
		out[i] = blockID(sid)
	}
	return out
}

// claimCreated makes the caller the owner of the blocks a build made,
// with reason "create".
func (g *Gateway) claimCreated(rec *clientRecord, s *mSession, ids []string) []ownerChange {
	_, changes := g.claimBlocks(rec, s, ids, "take", "create")
	return changes
}

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
	if fresh, err := c.g.current(); err == nil {
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
	}
	if fresh, err := c.g.current(); err == nil {
		m = fresh
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
	leaves := tree.leaves()
	if len(leaves) == 0 {
		return nil, errBadRequest("the layout holds no block")
	}
	res, aerr := g.command(rex.LayoutCommand{
		Op: "pane.split", Space: s.space, SID: anchor.sid,
		Vertical: vertical, Dir: leaves[0].tree.cwd,
	})
	if aerr != nil {
		return nil, aerr
	}
	sids := []string{res.New}
	first := leaves[0].tree
	if first.label != "" {
		g.command(rex.LayoutCommand{Op: "pane.rename", Space: s.space, SID: sids[0], Name: first.label})
	}
	for i := 1; i < len(leaves); i++ {
		v, r := siblings(leaves[i-1], leaves[i])
		res, aerr := g.command(rex.LayoutCommand{
			Op: "pane.split", Space: s.space, SID: sids[i-1], Vertical: v, Dir: leaves[i].tree.cwd,
		})
		if aerr != nil {
			return nil, aerr
		}
		sids = append(sids, res.New)
		if math.Abs(r-0.5) > 0.01 {
			direction, amount := ratioCommand(v, 0.5, r)
			g.command(rex.LayoutCommand{Op: "pane.resize", Space: s.space, SID: sids[i-1], Direction: direction, Amount: amount})
		}
		if leaves[i].tree.label != "" {
			g.command(rex.LayoutCommand{Op: "pane.rename", Space: s.space, SID: sids[i], Name: leaves[i].tree.label})
		}
	}
	if math.Abs(ratio-0.5) > 0.01 && len(sids) == 1 {
		direction, amount := ratioCommand(vertical, 0.5, ratio)
		g.command(rex.LayoutCommand{Op: "pane.resize", Space: s.space, SID: anchor.sid, Direction: direction, Amount: amount})
	}
	return sids, nil
}

// splitsOf returns every split ID of a session's active view.
func (g *Gateway) splitsOf(s *mSession) (map[string]bool, *apiError) {
	m, err := g.current()
	if err != nil {
		return nil, errInternal("reading the layout: %v", err)
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
	if _, aerr := c.g.command(rex.LayoutCommand{Op: "tab.close", Space: s.space, SID: first.sid}); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		return now == nil || now.window(windowID) == nil
	})
	if aerr != nil {
		return nil, aerr
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
	if _, aerr := c.g.command(rex.LayoutCommand{Op: "tab.rename", Space: s.space, SID: first.sid, Name: label}); aerr != nil {
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
			if m, err := c.g.current(); err == nil {
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
	if side == "before" {
		return nil, errConflict("GoRex cannot move a block before another; use side after")
	}
	before := s.splitSet()
	res, aerr := c.g.command(rex.LayoutCommand{
		Op: "pane.move", Space: s.space, SID: b.sid, Target: anchor.sid,
		Direction: map[bool]string{true: "down", false: "right"}[vertical],
		Ratio:     float32(ratio),
	})
	if aerr != nil {
		return nil, aerr
	}
	if !res.Changed {
		switch res.Reason {
		case "not_found":
			return nil, errNotFound("one of the blocks is gone")
		case "same_tab":
			return nil, errConflict("the block is already in the window")
		case "zoomed_tab":
			return nil, errConflict("a window is zoomed; unzoom it first")
		}
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		if now == nil {
			return true
		}
		return now.splitSet() != before
	})
	if aerr != nil {
		return nil, aerr
	}
	now := m.byID[s.id]
	created := newSplits(splitSetOf(before), now)
	removed := removedSplits(splitSetOf(before), now)
	out := moveResult{Revision: revisionOf(m, s.id)}
	if len(created) > 0 {
		out.CreatedSplitID = created[0]
	} else if w := now.windowOf(now.findBlock(blockID)); w != nil {
		if sp := lowestSplit(w.layout, blockID, anchorID); sp != nil {
			out.CreatedSplitID = sp.SplitID
		}
	}
	if len(removed) > 0 {
		out.RemovedSplitID = &removed[0]
	}
	return out, nil
}

// splitSet returns every split ID of a session's windows, sorted.
func (s *mSession) splitSet() string {
	var ids []string
	for _, w := range s.windows {
		ids = append(ids, splitIDs(w.layout)...)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// splitSetOf returns a split set as a map.
func splitSetOf(set string) map[string]bool {
	out := map[string]bool{}
	for _, id := range strings.Split(set, ",") {
		if id != "" {
			out[id] = true
		}
	}
	return out
}

// removedSplits returns the splits a change removed from a session.
func removedSplits(before map[string]bool, s *mSession) []string {
	now := map[string]bool{}
	if s != nil {
		for _, w := range s.windows {
			for _, id := range splitIDs(w.layout) {
				now[id] = true
			}
		}
	}
	var out []string
	for id := range before {
		if !now[id] {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
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
	// which is the one Rex means when that split is the border's. Either
	// block of the pair is tried, in the direction that moves the border
	// towards ratio.
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
	if _, aerr := c.g.command(rex.LayoutCommand{Op: "tab.move", Space: s.space, SID: first.sid, Index: index}); aerr != nil {
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

// blockViewResult is session.list_blocks' and session.describe_block's
// block.
type blockViewResult struct {
	BlockID     string  `json:"block_id"`
	Label       string  `json:"label"`
	CreatorName string  `json:"creator_name,omitempty"`
	Flavor      string  `json:"flavor,omitempty"`
	HostID      string  `json:"host_id,omitempty"`
	Placement   string  `json:"placement"`
	WindowID    *string `json:"window_id,omitempty"`
	Owner       *string `json:"owner,omitempty"`
}

// blockViewOf returns a block as the API reports it.
func (m *model) blockViewOf(b *mBlock) blockViewResult {
	out := blockViewResult{
		BlockID: b.id, Label: b.label, CreatorName: creatorName,
		Flavor: flavorShell, HostID: m.host, Placement: "placed",
	}
	if w := b.window; w != "" {
		window := w
		out.WindowID = &window
	}
	if b.owner != "" {
		owner := b.owner
		out.Owner = &owner
	}
	return out
}

// methodSessionListBlocks lists every block of a session.
func methodSessionListBlocks(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	m, err := c.g.current()
	if err != nil {
		return nil, errInternal("reading the layout: %v", err)
	}
	now := m.byID[s.id]
	if now == nil {
		return nil, errSessionNotFound("no session %s", s.id)
	}
	blocks := make([]blockViewResult, 0, len(now.blocks))
	seen := map[string]bool{}
	for _, b := range now.blocks {
		if seen[b.id] {
			continue
		}
		seen[b.id] = true
		blocks = append(blocks, m.blockViewOf(b))
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].BlockID < blocks[j].BlockID })
	return map[string]any{"blocks": blocks}, nil
}

// clientViewResult is a client of session.list_clients.
type clientViewResult struct {
	ClientID        string         `json:"client_id"`
	Principal       map[string]any `json:"principal"`
	Info            map[string]any `json:"info,omitempty"`
	ConnectedAt     string         `json:"connected_at"`
	ConnectionState string         `json:"connection_state,omitempty"`
	LostAt          *string        `json:"connection_lost_at,omitempty"`
}

// methodSessionListClients lists the clients attached to a session.
func methodSessionListClients(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	c.g.mu.Lock()
	recs := make([]*clientRecord, 0, len(c.g.clients))
	for _, r := range c.g.clients {
		recs = append(recs, r)
	}
	c.g.mu.Unlock()
	sort.Slice(recs, func(i, j int) bool { return recs[i].connectedAt.Before(recs[j].connectedAt) })
	out := []clientViewResult{}
	for _, r := range recs {
		if !r.attachedTo(s.id) {
			continue
		}
		v := clientViewResult{
			ClientID: r.id, Principal: r.principal,
			Info: r.info.json(), ConnectedAt: connectedAtString(r.connectedAt),
		}
		if lost, at := r.state(); lost {
			v.ConnectionState = "lost"
			s := connectedAtString(at)
			v.LostAt = &s
		}
		out = append(out, v)
	}
	return map[string]any{"clients": out}, nil
}

// methodSessionHelp describes the methods a session accepts.
func methodSessionHelp(c *control, a args) (any, *apiError) {
	if _, aerr := c.attachedArg(a); aerr != nil {
		return nil, aerr
	}
	return helpDocument(true), nil
}

// methodSessionDescribeBlock describes a block and its methods.
func methodSessionDescribeBlock(c *control, a args) (any, *apiError) {
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
	m, err := c.g.current()
	if err != nil {
		return nil, errInternal("reading the layout: %v", err)
	}
	now := m.byID[s.id]
	if now == nil {
		return nil, errSessionNotFound("no session %s", s.id)
	}
	b := now.findBlock(blockID)
	if b == nil {
		return nil, errNotFound("no block %s in %s", blockID, s.id)
	}
	out := map[string]any{"block": m.blockViewOf(b)}
	if api := blockCreatorAPI(); api != nil {
		out["api"] = api
	}
	return out, nil
}

// methodBlockClose closes a block and ends its program.
func methodBlockClose(c *control, a args) (any, *apiError) {
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
	keepLast, _, aerr := a.boolean("keep_last")
	if aerr != nil {
		return nil, aerr
	}
	if hasBlock == hasWindow {
		return nil, errBadRequest("send block_id or window_id, not both")
	}
	if hasBlock && blockID == "" || hasWindow && windowID == "" {
		return nil, errBadRequest("the ID is empty")
	}
	var b *mBlock
	if blockID != "" {
		b = s.findBlock(blockID)
		if b == nil {
			return nil, errNotFound("no block %s in %s", blockID, s.id)
		}
	} else {
		w := s.window(windowID)
		if w == nil {
			return nil, errNotFound("no window %s in %s", windowID, s.id)
		}
		b = w.focusedBlock()
		if b == nil {
			return nil, errNotFound("window %s has no focused block", windowID)
		}
	}
	if keepLast {
		if w := s.window(b.window); w != nil && len(w.blocks) <= 1 {
			return nil, errBadRequest("the block is the only one in its window")
		}
	}
	if _, aerr := c.g.command(rex.LayoutCommand{Op: "pane.close", Space: s.space, SID: b.sid}); aerr != nil {
		return nil, aerr
	}
	m, aerr := c.g.afterCommand(func(m *model) bool {
		now := m.byID[s.id]
		return now == nil || now.findBlock(b.id) == nil
	})
	if aerr != nil {
		return nil, aerr
	}
	return map[string]any{"block_id": b.id, "revision": revisionOf(m, s.id)}, nil
}

// methodSessionOwnBlock claims blocks for the calling client.
func methodSessionOwnBlock(c *control, a args) (any, *apiError) {
	if aerr := c.g.requireLocks(); aerr != nil {
		return nil, aerr
	}
	s, aerr := c.attachedSessionArg(a)
	if aerr != nil {
		return nil, aerr
	}
	mode, _, aerr := a.str("mode")
	if aerr != nil {
		return nil, aerr
	}
	if mode == "" {
		mode = "take"
	}
	if mode != "take" && mode != "request" {
		return nil, errBadRequest("mode is take or request")
	}
	ids, aerr := c.claimIDs(a, s)
	if aerr != nil {
		return nil, aerr
	}
	blocks, changes := c.g.claimBlocks(c.rec, s, ids, mode, mode)
	c.g.emitOwnerChanges(changes)
	c.g.refresh()
	return claimsResult{Blocks: blocks}, nil
}

// methodSessionDisownBlock gives up the calling client's claims.
func methodSessionDisownBlock(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedSessionArg(a)
	if aerr != nil {
		return nil, aerr
	}
	ids, aerr := c.claimIDs(a, s)
	if aerr != nil {
		return nil, aerr
	}
	blocks, changes := c.g.giveUp(c.rec, s, ids)
	c.g.emitOwnerChanges(changes)
	c.g.refresh()
	return claimsResult{Blocks: blocks}, nil
}

// attachedSessionArg is attachedArg for own_block and disown_block: a
// connection that only called session.attach_all is turned away.
func (c *control) attachedSessionArg(a args) (*mSession, *apiError) {
	s, aerr := c.sessionArg(a)
	if aerr != nil {
		return nil, aerr
	}
	if !c.rec.attachedTo(s.id) {
		return nil, errBadRequest("session.attach or session.bootstrap is required")
	}
	return s, nil
}

// claimIDs returns the blocks own_block or disown_block names, or every
// block of the session, sorted, when it names none. A block that isn't in
// the session is bad_request.
func (c *control) claimIDs(a args, s *mSession) ([]string, *apiError) {
	ids, present, aerr := a.strs("block_ids")
	if aerr != nil {
		return nil, aerr
	}
	if !present || len(ids) == 0 {
		out := make([]string, 0, len(s.blocks))
		for _, b := range s.blocks {
			out = append(out, b.id)
		}
		sort.Strings(out)
		return out, nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			return nil, errBadRequest("a block ID is empty")
		}
		if s.findBlock(id) == nil {
			return nil, errBadRequest("%s is not a block of %s", id, s.id)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}
