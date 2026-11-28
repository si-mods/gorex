package rexapi

import (
	"encoding/json"
	"math"
	"time"

	"gorex/internal/rex"
)

// specTree is a layout a request asks for: one new block of the shell
// flavor, or a split of two such trees.
type specTree struct {
	leaf     bool
	label    string
	shell    shellOptions
	vertical bool
	ratio    float64
	a, b     *specTree
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
			if t.shell, aerr = parseShellOptions(options); aerr != nil {
				return nil, aerr
			}
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

// leaves returns the tree's blocks in layout order.
func (t *specTree) leaves() []*specTree {
	if t.leaf {
		return []*specTree{t}
	}
	return append(t.a.leaves(), t.b.leaves()...)
}

// split reads a tree for rex.SplitTree.
func (t *specTree) split() (bool, float64, *specTree, *specTree, bool) {
	return t.vertical, t.ratio, t.a, t.b, !t.leaf
}

// isSingle tells whether the tree holds one block and no split.
func (t *specTree) isSingle() bool { return t != nil && t.leaf }

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
	case rex.FolderMissing:
		// The project's folder is gone: the error names the code.
		return errConflict("%s", res.Error)
	}
	return errConflict("%s: %s", res.Code, res.Error)
}

// afterCommand waits for a publish until shows is true, or the app's
// change is too long coming. A nil shows asks for the model as of now:
// a no-op command publishes nothing, so it reads the layout once itself.
func (g *Gateway) afterCommand(shows func(*model) bool) (*model, *apiError) {
	if shows == nil {
		m, err := g.reread(true)
		if err != nil {
			return m, errInternal("reading the layout: %v", err)
		}
		return m, nil
	}
	// The command the caller just ran may have applied already: read the
	// layout once as refresh() used to, and only wait for a publish when
	// the server applies the change after answering the command.
	m, err := g.reread(true)
	if err != nil {
		return m, errInternal("reading the layout: %v", err)
	}
	if shows(m) {
		return m, nil
	}
	m, ok := g.waitModel(shows, time.Now().Add(commandWait))
	if ok {
		return m, nil
	}
	if g.gone() {
		return m, errInternal("reading the layout: the GoRex server is gone")
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
	first := leaves[0]
	res, aerr := g.command(rex.LayoutCommand{
		Op: "tab.create", Space: space, Name: label, Dir: first.shell.cwd, Command: first.shell.command, After: after, Focus: focus,
	})
	if aerr != nil {
		return nil, aerr
	}
	if res.New != "" && first.label != "" {
		if _, aerr := g.command(rex.LayoutCommand{Op: "pane.rename", Space: space, SID: res.New, Name: first.label}); aerr != nil {
			return nil, aerr
		}
	}
	sids, err := rex.SplitTree(tree, res.New, (*specTree).split, g.splitIn(space))
	if err != nil {
		g.command(rex.LayoutCommand{Op: "tab.close", Space: space, SID: res.New})
		return nil, err.(*apiError)
	}
	g.applyShellOptions(sids, leaves)
	return sids, nil
}

// splitIn runs rex.SplitTree's commands in a space: a split's new pane
// runs its block's shell, with its label.
func (g *Gateway) splitIn(space string) func(rex.LayoutCommand, *specTree) (string, error) {
	return func(c rex.LayoutCommand, leaf *specTree) (string, error) {
		c.Space = space
		if c.Op == "pane.split" {
			c.Dir, c.Command, c.Name = leaf.shell.cwd, leaf.shell.command, leaf.label
		}
		res, aerr := g.command(c)
		if aerr != nil {
			return "", aerr
		}
		return res.New, nil
	}
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
