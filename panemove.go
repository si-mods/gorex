package main

import (
	"slices"

	"gorex/internal/layout"
)

// movePaneToTab moves pane p into tab t, beside the pane that has its
// focus. Its session keeps running and its term is not recreated.
func (a *App) movePaneToTab(p *Pane, t *Tab) {
	if p == nil || t == nil || p.Tab == nil || p.Tab == t {
		return
	}
	if t.Root != nil && p.host != t.host() {
		return
	}
	target := t.tiledTarget()
	if target == nil || t.Root == nil {
		src, wasActive := p.Tab, a.tab() == p.Tab
		a.detachPane(p)
		p.Tab, p.Node = t, &Node{ID: a.id(), Pane: p}
		t.Root, t.Zoom = p.Node, nil
		t.setFocus(p)
		a.finishPaneMove(t, p, src, wasActive)
		a.changed()
		return
	}
	a.movePaneToPane(p, target)
}

// mergeTab grafts src's pane tree beside dst's, keeping every session and
// terminal attached to its existing pane.
func (a *App) mergeTab(src, dst *Tab) {
	if !tabsCanMerge(src, dst) || slices.Index(a.tabs, src) < 0 || slices.Index(a.tabs, dst) < 0 {
		return
	}

	moved := src.panes()
	recent := append(slices.Clone(dst.recent), src.recent...)
	focused := src.Focus
	if focused == nil {
		focused = moved[0]
	}

	if dst.Root == nil {
		dst.Root = src.Root
		dst.Root.Parent = nil
	} else {
		root := &Node{ID: a.id(), A: dst.Root, B: src.Root, Vertical: false, Ratio: 0.5}
		dst.Root.Parent = root
		src.Root.Parent = root
		dst.Root = root
	}
	for _, p := range moved {
		p.Tab = dst
	}
	for _, f := range src.Floats {
		f.Pane.Tab = dst
		dst.Floats = append(dst.Floats, f)
	}
	src.Floats = nil
	dst.Zoom = nil

	// Keep both tabs' focus history, and add any source panes absent from it.
	allPanes := dst.allPanes()
	dst.recent = nil
	for _, p := range append(recent, moved...) {
		if p != nil && slices.Contains(allPanes, p) && !slices.Contains(dst.recent, p) {
			dst.recent = append(dst.recent, p)
		}
	}
	dst.setFocus(focused)

	a.removeTab(src)
	a.groupTabs()
	a.selectTab(slices.Index(a.tabs, dst))
	a.focusReq = focused
	a.changed()
}

// movePaneToPane moves pane p beside target, in target's tab. It works
// within a tab too, detaching p and splitting target's node.
func (a *App) movePaneToPane(p, target *Pane) { a.movePaneBeside(p, target, false, false, 0.5) }

// movePaneBeside moves pane p beside target in target's tab, in a split
// of the given orientation and ratio, pane.move; the session
// keeps running. With before, p goes left of or above target, and ratio is
// p's share.
func (a *App) movePaneBeside(p, target *Pane, vertical, before bool, ratio float32) {
	if p == nil || target == nil || p == target || p.Tab == nil || target.float != nil || p.host != target.host {
		return
	}
	t, src, wasActive := target.Tab, p.Tab, a.tab() == p.Tab
	a.detachPane(p)
	a.placePane(t, p, target, vertical, before, ratio)
	a.finishPaneMove(t, p, src, wasActive)
	a.changed()
}

// placePane puts p beside target in tab t, or alone when the tab has no
// pane; p is not in a tree. With before, p goes in the split's first side
// and ratio is its share; else p goes in the second and ratio is the
// target's.
func (a *App) placePane(t *Tab, p *Pane, target *Pane, vertical, before bool, ratio float32) {
	if t == nil || p == nil {
		return
	}
	if float32IsBad(ratio) || ratio <= 0 || ratio >= 1 {
		ratio = 0.5
	}
	ratio = min(max(ratio, 0.1), 0.9)
	if t.Root == nil {
		p.Tab, p.Node = t, &Node{ID: a.id(), Pane: p}
		t.Root, t.Zoom = p.Node, nil
		t.setFocus(p)
		return
	}
	if target == nil || target.Node == nil {
		return
	}
	old := target.Node
	fresh := &Node{ID: a.id(), Pane: p}
	sp := &Node{ID: a.id(), Parent: old.Parent, A: old, B: fresh, Vertical: vertical, Ratio: ratio}
	if before {
		sp.A, sp.B = fresh, old
	}
	fresh.Parent = sp
	if old.Parent == nil {
		t.Root = sp
	} else if old.Parent.A == old {
		old.Parent.A = sp
	} else {
		old.Parent.B = sp
	}
	old.Parent = sp
	p.Tab, p.Node = t, fresh
	t.Zoom = nil
	t.setFocus(p)
}

// movePaneToWindow moves p to target's active tab, as a session target
// attaches again to: its program is not restarted.
func (a *App) movePaneToWindow(p *Pane, target *App) {
	if p == nil || target == nil || target == a || p.Tab == nil {
		return
	}
	var h *hostConn
	if p.host != nil {
		h = target.hostByID(p.host.ID)
		if h == nil {
			return
		}
	}
	t := target.tab()
	if t == nil || t.Root != nil && t.host() != p.host {
		return
	}
	cols, rows := p.paneSize()
	if cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	src := p.Tab
	a.disposePane(p, false)
	a.detachPane(p)
	if a.tab() == src && src.Focus != nil {
		a.focusReq = src.Focus
	}
	if len(src.allPanes()) == 0 {
		a.removeTab(src)
	}
	var np *Pane
	if p.ext != nil {
		np = p.ext.clone(target, p)
	} else {
		np = &Pane{ID: target.id(), SID: p.SID, Name: p.Name, info: p.info, title: p.title, startDir: p.startDir, host: h}
		target.attach(np, cols, rows)
	}
	targetPane := t.tiledTarget()
	target.placePane(t, np, targetPane, false, false, 0.5)
	target.focusReq = np
	target.front()
	target.changed()
}

// movePaneToNewTab moves pane p into a tab of its own, after its tab,
// keeping its session and program.
func (a *App) movePaneToNewTab(p *Pane, label string) bool {
	if p == nil || p.Tab == nil {
		return false
	}
	src, cur := p.Tab, a.tab()
	wasActive := cur == src
	a.detachPane(p)
	t := a.makeTab("")
	t.Name = layout.PaneName(label)
	t.Root = &Node{ID: a.id(), Pane: p}
	p.Tab, p.Node = t, t.Root
	t.setFocus(p)
	at := len(a.tabs)
	if i := slices.Index(a.tabs, src); i >= 0 {
		at = i + 1
	}
	a.tabs = slices.Insert(a.tabs, at, t)
	if i := slices.Index(a.tabs, cur); cur != nil && i >= 0 {
		a.active = i
	}
	if len(src.allPanes()) == 0 {
		a.removeTab(src)
		if wasActive {
			a.active = max(slices.Index(a.tabs, t), 0)
		}
	}
	switch {
	case a.tab() == t:
		a.focusReq = t.Focus
	case wasActive && a.tab() == src && src.Focus != nil:
		a.focusReq = src.Focus
	}
	a.groupTabs()
	a.changed()
	return true
}

// paneToNewTab moves the focused pane of the active tab into a tab of its
// own, doing nothing when that pane is the tab's only one.
func (a *App) paneToNewTab() {
	t := a.tab()
	if t == nil || t.Focus == nil || len(t.panes()) < 2 {
		return
	}
	a.movePaneToNewTab(t.Focus, "")
}

// finishPaneMove settles focus after p moved to t from src.
func (a *App) finishPaneMove(t *Tab, p *Pane, src *Tab, wasActive bool) {
	if src != t && len(src.allPanes()) == 0 {
		a.removeTab(src)
		if wasActive {
			if i := slices.Index(a.tabs, t); i >= 0 {
				a.selectTab(i)
			}
		}
	}
	switch {
	case a.tab() == t:
		a.focusReq = p
	case wasActive && a.tab() == src && src.Focus != nil:
		a.focusReq = src.Focus
	}
}

// unlinkTiled takes a tiled pane out of its tab's tree, the other side of
// its split taking its place, and leaves the pane's tab as it was.
func (a *App) unlinkTiled(p *Pane) {
	n := p.Node
	if n == nil || n.Parent == nil {
		return
	}
	parent, other := n.Parent, n.Parent.A
	if other == n {
		other = n.Parent.B
	}
	parent.Pane, parent.Vertical, parent.Ratio = other.Pane, other.Vertical, other.Ratio
	parent.A, parent.B = other.A, other.B
	if parent.Pane != nil {
		parent.Pane.Node = parent
	} else {
		parent.A.Parent, parent.B.Parent = parent, parent
	}
}

// detachPane removes p from its tab's tree, promoting its sibling, and
// leaves the tab empty when p was its only pane.
func (a *App) detachPane(p *Pane) {
	t, n := p.Tab, p.Node
	if t == nil || n == nil {
		return
	}
	if t.Zoom == p {
		t.Zoom = nil
	}
	t.recent = slices.DeleteFunc(t.recent, func(q *Pane) bool { return q == p })
	switch {
	case p.float != nil:
		t.removeFloat(p.float)
	case n.Parent == nil:
		t.Root = nil
		a.promoteFloat(t)
	default:
		a.unlinkTiled(p)
	}
	if t.Focus == p {
		t.Focus = nil
		t.setFocus(t.nextFocus(p))
	}
	p.Tab, p.Node = nil, nil
}

// splitNode puts pane p beside node n of tab t, after it (right or below)
// when after, else before it, with ratio as the first side's share. The
// tab shows all its tiled panes, as no pane fills it; its focus stays.
func (a *App) splitNode(t *Tab, n *Node, p *Pane, after, vertical bool, ratio float32) {
	if ratio == 0 {
		ratio = 0.5
	}
	t.Zoom = nil
	p.Tab = t
	old := &Node{ID: a.id(), Pane: n.Pane, Parent: n, Vertical: n.Vertical, Ratio: n.Ratio, A: n.A, B: n.B}
	if old.Pane != nil {
		old.Pane.Node = old
	}
	if old.A != nil {
		old.A.Parent, old.B.Parent = old, old
	}
	node := &Node{ID: a.id(), Pane: p, Parent: n}
	p.Node = node
	n.Pane, n.Vertical, n.Ratio = nil, vertical, min(max(ratio, 0.05), 0.95)
	n.A, n.B = old, node
	if !after {
		n.A, n.B = node, old
	}
}
