package rexapi

import (
	"encoding/json"

	"gorex/internal/rex"
)

// rexLayout is a layout as Rex's session.create and session.new_split take
// it: a block, or a split of two layouts.
type rexLayout struct {
	Block *rexBlock `json:"block,omitempty"`
	Split *rexSplit `json:"split,omitempty"`
}

type rexBlock struct {
	Flavor  string      `json:"flavor"`
	Options *rexOptions `json:"options,omitempty"`
	Label   string      `json:"label,omitempty"`
}

type rexOptions struct {
	Command []string `json:"command,omitempty"`
	Cwd     string   `json:"cwd,omitempty"`
}

type rexSplit struct {
	Direction string     `json:"direction"`
	Ratio     float64    `json:"ratio,omitempty"`
	Before    *rexLayout `json:"before"`
	After     *rexLayout `json:"after"`
}

// windowDef is one of session.create's initial windows.
type windowDef struct {
	WindowLabel string          `json:"window_label"`
	Layout      json.RawMessage `json:"layout"`
}

// parseLayout reads a layout argument.
func parseLayout(raw json.RawMessage) (*rexLayout, *apiError) {
	if len(raw) == 0 {
		return nil, errBadRequest("the layout is missing")
	}
	var l rexLayout
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, errBadRequest("invalid layout: %v", err)
	}
	if l.Block == nil && l.Split == nil {
		return nil, errBadRequest("a layout holds a block or a split")
	}
	return &l, nil
}

// leaf is one block of a layout, and where it sits.
type leaf struct {
	*rexBlock
	path []string // "h"/"v" for each split above it, and "a"/"b" for its side
}

// leaves returns the layout's blocks in layout order.
func (l *rexLayout) leaves() []leaf {
	var out []leaf
	var walk func(*rexLayout, []string)
	walk = func(n *rexLayout, path []string) {
		if n == nil {
			return
		}
		if n.Block != nil {
			out = append(out, leaf{n.Block, path})
			return
		}
		if n.Split == nil {
			return
		}
		d := "h"
		if n.Split.Direction == "vertical" {
			d = "v"
		}
		walk(n.Split.Before, append(append([]string(nil), path...), d, "a"))
		walk(n.Split.After, append(append([]string(nil), path...), d, "b"))
	}
	walk(l, nil)
	return out
}

// cwd is where the block's program starts.
func (b *rexBlock) cwd() string {
	if b.Options != nil {
		return b.Options.Cwd
	}
	return ""
}

// command is the program the block runs instead of a shell.
func (b *rexBlock) command() []string {
	if b.Options != nil {
		return b.Options.Command
	}
	return nil
}

// flavor checks the block's flavor, which GoRex has only one of.
func (b *rexBlock) check() *apiError {
	if b.Flavor != "" && b.Flavor != FlavorShell {
		return errNotFound("no creator has the flavor %q", b.Flavor)
	}
	return nil
}

// splitOf returns the direction and side of the split between two leaves,
// which is their lowest common ancestor's.
func splitOf(a, b leaf) (vertical bool, side string) {
	i := 0
	for i < len(a.path) && i < len(b.path) && a.path[i] == b.path[i] {
		i++
	}
	if i >= len(a.path) || i >= len(b.path) {
		return false, "after"
	}
	return a.path[i] == "v", b.path[i]
}

// createWindow makes a Rex window: a GoRex tab of the first block, split
// for each block after it, in layout order.
func (c *control) createWindow(s *mSession, label string, l *rexLayout, focus bool) (string, []string, *apiError) {
	leaves := l.leaves()
	if len(leaves) == 0 {
		return "", nil, errBadRequest("the layout has no blocks")
	}
	for _, leaf := range leaves {
		if apiErr := leaf.check(); apiErr != nil {
			return "", nil, apiErr
		}
	}
	first := leaves[0]
	res, apiErr := c.command(s, rex.LayoutCommand{
		Op:      "tab.create",
		Name:    label,
		Dir:     first.cwd(),
		Command: first.command(),
		Focus:   focus,
	})
	if apiErr != nil {
		return "", nil, apiErr
	}
	seed := res.New
	if res.Tab != "" {
		seed = res.Tab
	}
	window := windowIDOf(seed)
	blockIDs := []string{blockID(res.New)}
	prev := res.New
	for i, leaf := range leaves[1:] {
		// The split between this block and the one before it is their
		// lowest common ancestor's: blocks are made in layout order, so
		// this one is always on the ancestor's after side.
		vertical, _ := splitOf(leaves[i], leaf)
		cmd := rex.LayoutCommand{
			Op:       "pane.split",
			SID:      prev,
			Dir:      leaf.cwd(),
			Command:  leaf.command(),
			Vertical: vertical,
			Name:     leaf.Label,
			Focus:    focus,
		}
		res, apiErr := c.command(s, cmd)
		if apiErr != nil {
			return "", nil, apiErr
		}
		prev = res.New
		blockIDs = append(blockIDs, blockID(res.New))
	}
	return window, blockIDs, nil
}

// windowIDOf names a window from the ID its tab keeps, as the model does.
func windowIDOf(seed string) string { return "window:" + uuidLike("window", seed) }

// createSession makes a session and its windows.
func (c *control) createSession(label string, windows []windowDef) (any, *apiError) {
	// The windows are read and checked before anything is made, so that a
	// bad layout does not leave half a session.
	layouts := make([]*rexLayout, len(windows))
	for i, w := range windows {
		l, apiErr := parseLayout(w.Layout)
		if apiErr != nil {
			return nil, apiErr
		}
		if apiErr := checkLayout(l); apiErr != nil {
			return nil, apiErr
		}
		layouts[i] = l
	}
	res, apiErr := c.command(nil, rex.LayoutCommand{Op: "space.create", Name: label})
	if apiErr != nil {
		return nil, apiErr
	}
	s := &mSession{ID: sessionID(res.Space), Label: label, Workspace: res.Space}
	if s.Label == "" {
		s.Label = "session " + short(res.Space)
	}
	type created struct {
		WindowID string   `json:"window_id"`
		BlockIDs []string `json:"block_ids"`
		Revision uint64   `json:"revision"`
	}
	out := make([]created, 0, len(windows))
	for i, w := range windows {
		wid, bids, apiErr := c.createWindow(s, w.WindowLabel, layouts[i], true)
		if apiErr != nil {
			// A session whose windows did not all come is closed again:
			// Rex makes none of it.
			_, _ = c.command(s, rex.LayoutCommand{Op: "space.close"})
			return nil, apiErr
		}
		out = append(out, created{WindowID: wid, BlockIDs: bids})
	}
	c.s.changed()
	m := c.s.current()
	rev := m.rev
	for i := range out {
		out[i].Revision = rev
	}
	return map[string]any{"session_id": s.ID, "initial_windows": out}, nil
}

// checkLayout refuses a layout whose blocks GoRex cannot make.
func checkLayout(l *rexLayout) *apiError {
	for _, leaf := range l.leaves() {
		if apiErr := leaf.check(); apiErr != nil {
			return apiErr
		}
	}
	if len(l.leaves()) == 0 {
		return errBadRequest("the layout has no blocks")
	}
	return nil
}
