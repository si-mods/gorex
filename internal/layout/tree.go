package layout

import "encoding/json"

// IsLeaf tells a pane from a split.
func (n *Node) IsLeaf() bool { return n != nil && n.A == nil && n.B == nil }

// Find returns the leaf of session sid under n, nil for none.
func Find(n *Node, sid string) *Node {
	if n == nil {
		return nil
	}
	if n.IsLeaf() {
		if n.SID == sid {
			return n
		}
		return nil
	}
	if x := Find(n.A, sid); x != nil {
		return x
	}
	return Find(n.B, sid)
}

// Leaves are the panes of a tree, in order: a tab's Focus counts them.
func Leaves(n *Node) []*Node {
	if n == nil {
		return nil
	}
	if n.IsLeaf() {
		return []*Node{n}
	}
	return append(Leaves(n.A), Leaves(n.B)...)
}

// LeafIndex returns the place of a session among a tree's leaves, -1 for
// none.
func LeafIndex(n *Node, sid string) int {
	for i, l := range Leaves(n) {
		if l.SID == sid {
			return i
		}
	}
	return -1
}

// FirstLeaf returns a tree's first pane.
func FirstLeaf(n *Node) *Node {
	for n != nil && n.A != nil {
		n = n.A
	}
	return n
}

// Parent returns the split above node under root, nil for none.
func Parent(root, node *Node) *Node {
	if root == nil || root == node {
		return nil
	}
	if root.A == node || root.B == node {
		return root
	}
	if p := Parent(root.A, node); p != nil {
		return p
	}
	return Parent(root.B, node)
}

// SplitAt returns the split whose divider is node's edge toward direction,
// "left", "right", "up" or "down": the one that has the pane on that side,
// else the nearest of the same axis, which an outer edge moves.
func SplitAt(root, node *Node, direction string) *Node {
	vertical := direction == "up" || direction == "down"
	grows := direction == "right" || direction == "down"
	var nearest *Node
	for n := node; ; {
		split := Parent(root, n)
		if split == nil {
			break
		}
		if split.Vertical == vertical {
			if nearest == nil {
				nearest = split
			}
			if (split.A == n) == grows {
				return split
			}
		}
		n = split
	}
	return nearest
}

// Replace puts next where old was in the tab's tree.
func (t *Tab) Replace(old, next *Node) {
	if t.Root == old {
		t.Root = next
		return
	}
	p := Parent(t.Root, old)
	if p == nil {
		return
	}
	if p.A == old {
		p.A = next
	} else {
		p.B = next
	}
}

// Remove takes a leaf out of the tab's tree, the other side of its split
// taking the split's place, and returns that side; nil for the root.
func (t *Tab) Remove(node *Node) *Node {
	if t.Root == node {
		t.Root, t.Zoom = nil, false
		return nil
	}
	p := Parent(t.Root, node)
	if p == nil {
		return nil
	}
	keep := p.A
	if p.A == node {
		keep = p.B
	}
	t.Replace(p, keep)
	t.Focus = min(max(t.Focus, 0), len(Leaves(t.Root))-1)
	return keep
}

// Space returns the workspace of an id, nil for none.
func (s *Store) Space(id string) *Workspace {
	for i := range s.Sessions {
		if s.Sessions[i].ID == id {
			return &s.Sessions[i]
		}
	}
	return nil
}

// Arrangement is the tab as its arrangement: without its revision, a
// canvas's view (the window's own), and its panes' facts, which the server
// keeps of their sessions (directory, size, program). Two tabs of the same
// arrangement encode the same.
func (t Tab) Arrangement() []byte { return t.Encode(false) }

// Encode is the tab as Arrangement has it, with its panes' directories and
// programs too for facts; never their sizes, which are their sessions'.
func (t Tab) Encode(facts bool) []byte {
	t.Rev = 0
	if t.Canvas != nil {
		cv := *t.Canvas
		cv.X, cv.Y, cv.Scale = 0, 0, 0
		t.Canvas = &cv
	}
	var strip func(n *Node) *Node
	strip = func(n *Node) *Node {
		if n == nil {
			return nil
		}
		c := *n
		c.Cols, c.Rows = 0, 0
		if !facts {
			c.Dir, c.Prog, c.Args, c.AgentSession, c.AgentConfigDir = "", "", nil, "", ""
		}
		c.A, c.B = strip(n.A), strip(n.B)
		return &c
	}
	t.Root = strip(t.Root)
	t.Floats = append([]Float(nil), t.Floats...)
	for i := range t.Floats {
		t.Floats[i].Pane = strip(t.Floats[i].Pane)
	}
	b, _ := json.Marshal(t)
	return b
}
