package main

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

const (
	tabH     = 28
	tabMaxW  = 214
	tabMinW  = 120
	tileW    = 21
	tileH    = 16
	tileStep = 5 // how far each tile behind peeks out
	// tileSlot is the room of one tile: its width and a point of air each
	// side, which the sidebar's marks (a folder tile, a tab's tiles) share.
	tileSlot = tileW + 2
)

// tabStrip draws the tabs, in a track as wide as they are, which shrinks
// with them when the title bar has no room for them all. The track is part
// of the title bar: its edges and the gaps between tabs drag the window.
func (a *App) tabStrip(c *ui.Context, k *colors) {
	widths := make([]float32, len(a.tabs))
	total := float32(4 + max(len(a.tabs)-1, 0)) // the padding and separators
	for i, t := range a.tabs {
		widths[i] = tabWidth(c, t)
		total += widths[i]
	}
	track := ui.Row(c).Shrink(1).MinWidth(0).Height(tabH+4).Padding(2).Radius((tabH+4)/2).
		Background(k.track).Border(0.5, k.trackBorder).AlignItems(ui.Center).ClipX().
		DragWindow().Role(ui.RoleTabList).Label(locales.TabListLabel())
	// With a style of animation the track's width follows its tabs as
	// they open, close and slide, on their spring: a tab after one that
	// closes slides in whole, and one opening unfolds into the room the
	// track makes. Growing, it is narrower than its tabs, which overflow
	// it then rather than all shrinking for a moment.
	anim := motion(c)
	shown, squeeze := total, true
	if anim != nil {
		shown = track.AnimateTo("width", total, *anim)
		squeeze = shown >= total-0.5
	}
	track.Basis(shown)
	tabs := slices.Clone(a.tabs)
	track.Children(func() {
		var activeEl ui.Element
		var hasActive bool
		for i, t := range tabs {
			if i > 0 {
				// The separator before a tab goes with it.
				sep := ui.Box(c.Key(tabSepKey(t.ID))).Size(1, 16).Shrink(0)
				if i != a.active && i-1 != a.active {
					sep.Background(k.tabSep)
				}
				if anim != nil {
					sep.Transition(ui.ElementTransition{Animation: anim, Position: true, Colors: true, Enter: tabMotion, Exit: tabMotion})
				}
			}
			el := a.tabItem(c, k, i, t, widths[i], squeeze)
			if i == a.active {
				activeEl = el
				hasActive = true
			}
		}
		if hasActive {
			trackB := track.LayoutBounds()
			activeB := activeEl.LayoutBounds()
			if activeB.W > 0 && trackB.W > 0 {
				targetX := activeB.X - trackB.X
				targetW := activeB.W
				pill := ui.Box(c.Key("tab-active-pill")).Absolute().Top(2).Height(tabH).ZIndex(-1)
				glassPill(pill, k, tabH)
				if anim != nil {
					pill.Left(pill.AnimateTo("x", targetX, *anim))
					pill.Width(pill.AnimateTo("w", targetW, *anim))
				} else {
					pill.Left(pill.Jump("x", targetX))
					pill.Width(pill.Jump("w", targetW))
				}
				a.tabPill = pill.Bounds()
			} else {
				c.Invalidate()
			}
		}
	})
}

// tabSepKey is the key of the separator before a tab in the title bar.
type tabSepKey int

// tabWidth returns the width a tab takes when the title bar has room: its
// tiles and its label, the label faded out past tabMaxW, and room for its
// close button or its dot.
func tabWidth(c *ui.Context, t *Tab) float32 {
	name, detail := t.label()
	w, _ := c.MeasureText(0, tabLabel(name, detail, ui.Color{}, ui.Color{})...)
	tiles := float32(tileW + tileStep*min(len(t.panes())-1, 2) + 2)
	// A host's badge takes room of its own, past the label's widest.
	badge := float32(0)
	if h := t.host(); h != nil {
		bw, _ := c.MeasureText(0, ui.Span{Text: h.label(), Size: badgeSize, Weight: 600})
		badge = min(bw, badgeMaxW) + 12 + 6
	}
	return min(max(9+tiles+6+w+6+18+10, tabMinW), tabMaxW) + badge
}

// The badge of a remote host's tab: its text's size, and its widest.
const (
	badgeSize = fontCaption
	badgeMaxW = 90
)

// hostBadge names a tab's host, above, where tabs of every host mix.
func hostBadge(c *ui.Context, k *colors, h *hostConn) {
	ui.Text(c, h.label()).FontSize(badgeSize).FontWeight(600).TextColor(k.textMuted).Padding(2, 6).Radius(radiusS).
		Background(k.hover).SingleLine().Ellipsis("…").MaxWidth(badgeMaxW + 12).Shrink(0)
}

// tabLabel is the label of a tab: the name and, fainter, the detail.
func tabLabel(name, detail string, nameColor, detailColor ui.Color) []ui.Span {
	return append(
		titleSpans(name, 13.5, 500, nameColor),
		titleSpans(" "+detail, 13.5, 400, detailColor)...,
	)
}

// titleSpans lays out a title programs set, as a tab's name or a pane
// header shows it: a title's icons, which programs head their titles
// with, as omp names its sessions with a Nerd Font glyph, draw in the
// terminals' font, where alone they have glyphs, and its text in the
// window's, which draws a box where the font of the terminals is absent.
func titleSpans(text string, size float32, weight int, col ui.Color) []ui.Span {
	var spans []ui.Span
	start, private, first := 0, false, true
	for i := 0; i < len(text); {
		r, w := utf8.DecodeRuneInString(text[i:])
		if first {
			private, first, i = privateUse(r), false, i+w
			continue
		}
		p := privateUse(r)
		if p == private {
			i += w
			continue
		}
		// The layout drops a space that heads a span: the spaces after
		// a run of glyphs stay in its span, in the font that draws it.
		j := i
		for j < len(text) && text[j] == ' ' {
			j++
		}
		spans = append(spans, titleSpan(text[start:j], size, weight, col, private))
		start, private, i = j, p, j
	}
	return append(spans, titleSpan(text[start:], size, weight, col, private))
}

// titleSpan is a span of a title, drawn in the terminals' font when its
// text is a glyph of a private use area.
func titleSpan(text string, size float32, weight int, col ui.Color, private bool) ui.Span {
	sp := ui.Span{Text: text, Size: size, Weight: weight, Color: col}
	if private {
		sp.Font = termFont.Family
	}
	return sp
}

// privateUse reports a rune of a private use area: where icon fonts,
// as the Nerd Fonts that head titles, keep their glyphs.
func privateUse(r rune) bool {
	return r >= 0xE000 && r <= 0xF8FF || r >= 0xF0000 && r <= 0xFFFFD || r >= 0x100000 && r <= 0x10FFFD
}

// titleFree is how much of the title bar the tabs leave free, after
// them, to drag the window by.
const titleFree = 56

// tabItem draws a tab: the tiles of its panes' programs and its label. It
// shrinks to fit the title bar while squeeze.
func (a *App) tabItem(c *ui.Context, k *colors, i int, t *Tab, width float32, squeeze bool) ui.Element {
	active := i == a.active
	shrink := float32(0)
	if squeeze {
		shrink = 1
	}
	e := ui.Row(c.Key(t.ID)).Height(tabH).Basis(width).Shrink(shrink).MinWidth(64).
		Padding(0, 10, 0, 10).Gap(6).AlignItems(ui.Center).Radius(tabH / 2).Role(ui.RoleTab).AccessSelected(active)
	name, detail := t.label()
	e.Label(name + " " + detail)
	if !active && e.Hovered() {
		e.Background(hoverFace(k))
	}
	a.tabEvents(c, e, k, i, t)
	// The tab stays hovered while its close button is pressed, which it
	// shows then.
	hovered := e.Hovered()
	e.Children(func() {
		a.tiles(c, k, t)
		if h := t.host(); h != nil {
			hostBadge(c, k, h)
		}
		a.tabRest(c, k, t, active, hovered)
	})
	return e
}

// tabRest draws a tab after its tiles: its label and its end, or the field
// that renames it.
func (a *App) tabRest(c *ui.Context, k *colors, t *Tab, active, hovered bool) {
	name, detail := t.label()
	if a.renaming == t {
		a.renameField(c, k, t, name)
		return
	}
	nameColor, detailColor := k.text, k.textFaint
	if !active {
		nameColor = k.textMuted
	}
	label := ui.Box(c).Grow(1).MinWidth(0).Height(18)
	label.Draw(func(p *ui.Painter, r ui.Rect) {
		fadeText(p, r, tabLabel(name, detail, nameColor, detailColor))
	})
	if dragged, ok := ui.Drop[*Pane](label); ok {
		a.movePaneToTab(dragged, t)
	}
	if dragged, over := ui.DragOver[*Pane](label); over && (t.Root == nil || dragged.host == t.host()) {
		label.Border(1.5, k.busy.Alpha(0.6))
	}
	a.tabEnd(c, k, t, hovered)
}

// tabMotion is where a tab comes from as it opens, and goes as it closes:
// folded to nothing along the tabs, so that those after it slide over.
var tabMotion = &ui.Motion{Collapse: true, Opacity: 0}

// tabEvents makes a tab's element do what tabs do: a click selects it, a
// double click renames it, it drags to another tab's place, and it has the
// tab's menu. With a style of animation it springs to its place, opens and
// closes; without, only its colors fade.
func (a *App) tabEvents(c *ui.Context, e ui.Element, k *colors, i int, t *Tab) {
	if anim := motion(c); anim != nil {
		e.Transition(ui.ElementTransition{Animation: anim, Colors: true, Position: true, Enter: tabMotion, Exit: tabMotion})
	} else {
		e.Transition(ui.ElementTransition{Colors: true, Duration: 160 * time.Millisecond})
	}
	e.Drag(t)
	// A tab of another host's takes a tab dropped on it to that host: it
	// asks to teleport it.
	away := func(dragged *Tab) bool { return len(t.allPanes()) > 0 && teleportsTo(dragged, t.host()) }
	if dragged, ok := ui.Drop[*Tab](e); ok && dragged != t {
		merge, _, _ := tabDropZoneAt(e, prefs.TabPlacement)
		switch {
		case away(dragged):
			a.askTeleport(dragged, t.host())
		case merge && tabsCanMerge(dragged, t):
			a.mergeTab(dragged, t)
		default:
			a.moveTab(dragged, i)
		}
	}
	if dragged, over := ui.DragOver[*Tab](e); over && dragged != t {
		merge, _, inside := tabDropZoneAt(e, prefs.TabPlacement)
		merge = merge && tabsCanMerge(dragged, t) || away(dragged)
		if inside && merge {
			e.Border(1.5, k.busy.Alpha(0.6))
		} else if inside {
			drawTabInsertion(e, k, prefs.TabPlacement, slices.Index(a.tabs, dragged) > i)
		}
	}
	if e.Dragging() {
		dimmed(e, 0.4)
	}
	if e.Clicked() && i != a.active {
		a.selectTab(i)
	}
	if e.DoubleClicked() {
		a.selectTab(i)
		a.startRename()
	}
	e.ContextMenu(func(m *ui.Menu) { a.tabMenu(m, t) })
}

// tabDropZone divides a tab along the tabs' list axis: the outer quarters
// reorder, and the middle half merges. before tells which edge is nearer.
func tabDropZone(pos, size float32) (merge, before bool) {
	if size <= 0 {
		return false, true
	}
	before = pos < size/2
	switch {
	case pos < size/4:
		return false, true
	case pos >= size*3/4:
		return false, false
	default:
		return true, before
	}
}

// tabDropZoneAt uses the pointer's position in a tab and its previous-frame
// bounds, along the list axis.
func tabDropZoneAt(e ui.Element, placement string) (merge, before, over bool) {
	x, y, over := e.PointerPosition()
	if !over {
		return false, false, false
	}
	bounds := e.LayoutBounds()
	if placement == "left" || placement == "right" {
		merge, before = tabDropZone(y, bounds.H)
	} else {
		merge, before = tabDropZone(x, bounds.W)
	}
	return merge, before, true
}

// drawTabInsertion shows the edge where a reordered tab goes.
func drawTabInsertion(e ui.Element, k *colors, placement string, before bool) {
	e.DrawOver(func(p *ui.Painter, r ui.Rect) {
		const inset, thickness = float32(7), float32(2)
		if placement == "left" || placement == "right" {
			y := r.Y + r.H - thickness
			if before {
				y = r.Y
			}
			p.Fill(ui.Rect{X: r.X + inset, Y: y, W: max(r.W-2*inset, 0), H: thickness}, k.busy, thickness/2)
		} else {
			x := r.X + r.W - thickness
			if before {
				x = r.X
			}
			p.Fill(ui.Rect{X: x, Y: r.Y + inset, W: thickness, H: max(r.H-2*inset, 0)}, k.busy, thickness/2)
		}
	})
}

// tabsCanMerge reports whether src's panes can join dst's panes.
func tabsCanMerge(src, dst *Tab) bool {
	if src == nil || dst == nil || src == dst || len(src.panes()) == 0 {
		return false
	}
	return len(dst.panes()) == 0 || src.host() == dst.host()
}

// tabEnd draws the end of a tab: its close button while hovered, else the
// dot of its record, or of its attention.
func (a *App) tabEnd(c *ui.Context, k *colors, t *Tab, hovered bool) {
	if hovered && len(a.tabs) > 1 {
		x := iconButton(c, k, "x", locales.CmdCloseTab(), 18, 12).Tooltip(locales.CmdCloseTab())
		if x.Clicked() {
			a.later(c, func() { a.closeTab(t) })
		}
	} else if s := t.status(); needsUser(s) {
		statusDot(c, k, s, 5.5).Margin(0, 6, 0, 0)
	} else if t.attention() {
		attentionDot(c, k, t.allPanes()...).Margin(0, 6, 0, 0)
	} else if s != nil && s.State == "working" {
		statusDot(c, k, s, 5.5)
	}
}

// renameField edits the name of a tab in place: Enter or moving the focus
// away keeps it, Escape goes back, and an empty name follows the panes
// again.
func (a *App) renameField(c *ui.Context, k *colors, t *Tab, name string) {
	in := textField(c, k, &a.renameText, fieldCompact).Placeholder(name).Grow(1).MinWidth(0).AutoFocus()
	done := func(keep bool) {
		if keep {
			a.setTabName(t, strings.TrimSpace(a.renameText))
		}
		a.renaming, a.renameFocus = nil, false
		a.focusReq = t.Focus
	}
	renameKeys(in, a.renameText, &a.renameFocus, done)
}

func (t *Tab) attention() bool {
	for _, p := range t.allPanes() {
		if p.attention {
			return true
		}
	}
	return false
}

// status is the record of its panes' programs that most needs the user,
// nil for none.
func (t *Tab) status() *rex.ProgramStatus {
	var best *rex.ProgramStatus
	for _, p := range t.allPanes() {
		if s := p.status(); s != nil && (best == nil || rex.StatusRank(s.State) > rex.StatusRank(best.State)) {
			best = s
		}
	}
	return best
}

func (a *App) moveTab(t *Tab, to int) {
	from := slices.Index(a.tabs, t)
	if from < 0 || to == from {
		return
	}
	activeTab := a.tab()
	a.tabs = slices.Delete(a.tabs, from, from+1)
	a.tabs = slices.Insert(a.tabs, min(to, len(a.tabs)), t)
	a.active = slices.Index(a.tabs, activeTab)
	a.groupTabs()
	a.changed()
}

func (a *App) tabMenu(m *ui.Menu, t *Tab) {
	if m.Item(locales.CmdRenameTab()).Chosen() {
		a.selectTab(slices.Index(a.tabs, t))
		a.startRename()
	}
	if t.Name != "" && m.Item(locales.TabResetName()).Chosen() {
		a.setTabName(t, "")
	}
	m.Separator()
	i := slices.Index(a.tabs, t)
	back, forth := locales.TabMoveLeft(), locales.TabMoveRight()
	if prefs.TabPlacement != "" {
		back, forth = locales.TabMoveUp(), locales.TabMoveDown()
	}
	if m.Item(back).Disabled(i <= 0).Chosen() {
		a.moveTab(t, i-1)
	}
	if m.Item(forth).Disabled(i >= len(a.tabs)-1).Chosen() {
		a.moveTab(t, i+1)
	}
	// Move to Project lists the other workspaces of the tab's host, which
	// keeps its panes' sessions (docs/MOD-002-projects.md): a tab lives on one host.
	var to []*Workspace
	if a.st != nil {
		to = slices.DeleteFunc(slices.Clone(a.st.workspaces), func(w *Workspace) bool { return !a.canMoveTab(t, w) })
	}
	if len(to) > 0 {
		m.Submenu(locales.TabMoveSpace(), func(sm *ui.Menu) {
			for _, w := range to {
				name := w.Name
				if w.Dir == "" {
					name = locales.ProjectsNoProject()
				}
				if sm.Item(name).Chosen() {
					a.laterService(func() { a.moveTabToSpace(t, w) })
				}
			}
		})
	}
	m.Separator()
	mods, key := ui.Cmd, ui.KeyT
	if runtime.GOOS != "darwin" {
		mods, key = cmdNewTab.Other.Mods, cmdNewTab.Other.Key
	}
	if m.Item(locales.CmdNewTab()).Shortcut(mods, key).Chosen() {
		a.newProjectTab()
	}
	if m.Item(locales.CmdCloseTab()).Chosen() {
		a.laterService(func() { a.closeTab(t) })
	}
	if m.Item(locales.TabCloseOthers()).Disabled(len(a.tabs) < 2).Chosen() {
		a.laterService(func() {
			for _, o := range slices.Clone(a.tabs) {
				if o != t {
					a.closeTab(o)
				}
			}
		})
	}
}

// tileMotion is how a tab's front tile moves for what its panes need, as
// iOS's TileMotion (ios/GoRex/Views/ProgramTile.swift): a Dock icon's
// bounce, attention's urgent, done's a calmer hop, still for the rest.
type tileMotion int

const (
	tileStill tileMotion = iota
	tileDone
	tileAttention
)

// tileMotion returns how the tab's front tile bounces: for the pane that
// most needs the user, which drives the bounce as the most urgent state
// does on iOS, where the attention of a bell or an unseen end bounces as
// a blocked program does.
func (t *Tab) tileMotion() tileMotion {
	rank := 0
	for _, p := range t.allPanes() {
		rank = max(rank, p.needsYou())
	}
	switch {
	case rank >= rex.StatusRank("error"), rank == 1:
		return tileAttention
	case rank == rex.StatusRank("done"):
		return tileDone
	}
	return tileStill
}

// tileBounce is a tileMotion's keyframe tracks and their cycle, at the
// timings of iOS's TileBounceModifier, its offsets two thirds of iOS's
// for the smaller tile. EaseInOut stands for iOS's CubicKeyframe, the
// closest of MyGo's easings; the tail holds the rest, as iOS's
// LinearKeyframe does.
type tileBounce struct {
	cycle      time.Duration
	dy, sx, sy []ui.Keyframe
}

var tileBounces = map[tileMotion]tileBounce{
	tileAttention: {
		cycle: 1600 * time.Millisecond,
		dy: []ui.Keyframe{
			{At: 0, Value: 0},
			{At: 160 * time.Millisecond, Value: -4.333, Ease: ui.EaseInOut},
			{At: 300 * time.Millisecond, Value: 0, Ease: ui.EaseInOut},
			{At: 420 * time.Millisecond, Value: -1.733, Ease: ui.EaseInOut},
			{At: 540 * time.Millisecond, Value: 0, Ease: ui.EaseInOut},
			{At: 1600 * time.Millisecond, Value: 0, Ease: ui.Linear},
		},
		sx: []ui.Keyframe{
			{At: 0, Value: 1},
			{At: 60 * time.Millisecond, Value: 0.96, Ease: ui.EaseInOut},
			{At: 160 * time.Millisecond, Value: 1, Ease: ui.EaseInOut},
			{At: 300 * time.Millisecond, Value: 1.05, Ease: ui.EaseInOut},
			{At: 420 * time.Millisecond, Value: 1, Ease: ui.EaseInOut},
			{At: 540 * time.Millisecond, Value: 1.02, Ease: ui.EaseInOut},
			{At: 620 * time.Millisecond, Value: 1, Ease: ui.EaseInOut},
			{At: 1600 * time.Millisecond, Value: 1, Ease: ui.Linear},
		},
		sy: []ui.Keyframe{
			{At: 0, Value: 1},
			{At: 60 * time.Millisecond, Value: 1.05, Ease: ui.EaseInOut},
			{At: 160 * time.Millisecond, Value: 1, Ease: ui.EaseInOut},
			{At: 300 * time.Millisecond, Value: 0.94, Ease: ui.EaseInOut},
			{At: 420 * time.Millisecond, Value: 1, Ease: ui.EaseInOut},
			{At: 540 * time.Millisecond, Value: 0.97, Ease: ui.EaseInOut},
			{At: 620 * time.Millisecond, Value: 1, Ease: ui.EaseInOut},
			{At: 1600 * time.Millisecond, Value: 1, Ease: ui.Linear},
		},
	},
	tileDone: {
		cycle: 2400 * time.Millisecond,
		dy: []ui.Keyframe{
			{At: 0, Value: 0},
			{At: 160 * time.Millisecond, Value: -3.333, Ease: ui.EaseInOut},
			{At: 300 * time.Millisecond, Value: 0, Ease: ui.EaseInOut},
			{At: 410 * time.Millisecond, Value: -1.333, Ease: ui.EaseInOut},
			{At: 520 * time.Millisecond, Value: 0, Ease: ui.EaseInOut},
			{At: 2400 * time.Millisecond, Value: 0, Ease: ui.Linear},
		},
		sx: []ui.Keyframe{
			{At: 0, Value: 1},
			{At: 60 * time.Millisecond, Value: 0.97, Ease: ui.EaseInOut},
			{At: 160 * time.Millisecond, Value: 1, Ease: ui.EaseInOut},
			{At: 300 * time.Millisecond, Value: 1.04, Ease: ui.EaseInOut},
			{At: 410 * time.Millisecond, Value: 1, Ease: ui.EaseInOut},
			{At: 520 * time.Millisecond, Value: 1, Ease: ui.EaseInOut},
			{At: 2400 * time.Millisecond, Value: 1, Ease: ui.Linear},
		},
		sy: []ui.Keyframe{
			{At: 0, Value: 1},
			{At: 60 * time.Millisecond, Value: 1.04, Ease: ui.EaseInOut},
			{At: 160 * time.Millisecond, Value: 1, Ease: ui.EaseInOut},
			{At: 300 * time.Millisecond, Value: 0.95, Ease: ui.EaseInOut},
			{At: 410 * time.Millisecond, Value: 1, Ease: ui.EaseInOut},
			{At: 520 * time.Millisecond, Value: 1, Ease: ui.EaseInOut},
			{At: 2400 * time.Millisecond, Value: 1, Ease: ui.Linear},
		},
	},
}

// tileBounceAt is where motion's bounce puts the front tile at now: how
// far it is up, and its squash and stretch. The phase is now's share of
// the cycle, so every needy tab hops together, as Dock icons do.
func tileBounceAt(m tileMotion, now time.Time) (dy, sx, sy float32) {
	b, ok := tileBounces[m]
	if !ok {
		return 0, 1, 1
	}
	d := time.Duration(now.UnixMilli()%int64(b.cycle/time.Millisecond)) * time.Millisecond
	return keyframeAt(b.dy, d), keyframeAt(b.sx, d), keyframeAt(b.sy, d)
}

// tiles draws the tiles of a tab's programs: the focused pane's in front,
// the others peeking out behind it.
func (a *App) tiles(c *ui.Context, k *colors, t *Tab) {
	panes := t.panes()
	front := t.Focus
	var back []*Pane
	for _, p := range panes {
		if p != front {
			back = append(back, p)
		}
	}
	if len(back) > 2 {
		back = back[:2]
	}
	// The front tile bounces while a pane needs the user, as a Dock icon
	// does: it tells of an unfocused window too, so it keeps at it where
	// the working dots stand still.
	m := tileStill
	if motion(c) != nil {
		m = t.tileMotion()
	}
	w := float32(tileW + tileStep*len(back) + 2)
	ui.Box(c).Size(w, tileH+4).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		y := r.Y + 2
		for i := len(back) - 1; i >= 0; i-- {
			x := r.X + 1 + float32(tileStep*(i+1))
			inset := float32(i+1) * 0.8
			drawTile(p, ui.Rect{X: x, Y: y + inset, W: tileW, H: tileH - 2*inset}, back[i].program(), false)
		}
		if front != nil {
			fr := ui.Rect{X: r.X + 1, Y: y, W: tileW, H: tileH}
			if m == tileStill {
				drawTile(p, fr, front.program(), true)
			} else {
				p.AnimationFrameRate(30)
				dy, sx, sy := tileBounceAt(m, p.Now())
				// iOS squashes about the tile's bottom edge, then lifts.
				p.Transform(ui.Scaling(sx, sy).Then(ui.Translation(0, dy)), fr.X+fr.W/2, fr.Y+fr.H, func() {
					drawTile(p, fr, front.program(), true)
				})
			}
		}
	})
}

// drawTile draws a program's tile: a rounded card of its color, and its
// glyph when it is in front.
func drawTile(p *ui.Painter, r ui.Rect, prog program, glyph bool) {
	const rad = radiusTile
	p.Shadow(r, rad, 0, 0.5, 1.5, 0, ui.RGBA(0, 0, 0, 0.18))
	p.Fill(r, prog.TileBg, rad)
	if prog.TileBorder {
		p.Stroke(r, ui.RGBA(0, 0, 0, 0.12), rad, 0.5)
	}
	// A sheen along the top, as on the app icons of macOS.
	p.FillGradient(r, ui.LinearGradient{From: ui.RGBA(255, 255, 255, 0.18), To: ui.RGBA(255, 255, 255, 0), Angle: 180, End: 0.6}, rad)
	if !glyph {
		return
	}
	g := prog.TileGlyph
	if g == "" {
		g = prog.Glyph
	}
	s := float32(11)
	glyphIn(p, g, ui.Rect{X: r.X + (r.W-s)/2, Y: r.Y + (r.H-s)/2, W: s, H: s}, prog.TileFg)
}

// fadeText draws spans on a line, fading the end out when they do not
// fit, rather than cutting them with an ellipsis.
func fadeText(p *ui.Painter, r ui.Rect, spans []ui.Span) {
	key := fmt.Sprintf("%.0f|%v", r.W, spans)
	f, ok := fades[key]
	if !ok {
		f = layoutFade(p, r.W, spans)
		if len(fades) > 512 {
			clear(fades)
		}
		fades[key] = f
	}
	p.Clip(r, 0, func() {
		p.RichText(r.X, r.Y+(r.H-f.h)/2, 0, f.spans...)
	})
}

// fades caches the spans of faded texts, by their width and spans.
var fades = map[string]fadeLayout{}

type fadeLayout struct {
	spans []ui.Span
	h     float32
}

// fadeSentinel keeps the advance of a rune that measures alone as
// nothing, as a space does: the layout trims trailing whitespace, not
// whitespace before a letter.
const fadeSentinel = "H"

func layoutFade(p *ui.Painter, width float32, spans []ui.Span) fadeLayout {
	w, h := p.MeasureText(0, spans...)
	if w <= width {
		return fadeLayout{spans, h}
	}
	r := ui.Rect{W: width}
	const fade = 30
	// Characters are measured one by one; those past the start of the
	// fade lose their opacity as they near the edge.
	var out []ui.Span
	x := float32(0)
	for _, s := range spans {
		start := 0
		sentinel := float32(-1) // the sentinel's width, measured once
		for i := 0; i < len(s.Text); {
			_, n := utf8.DecodeRuneInString(s.Text[i:])
			ch := s
			ch.Text = s.Text[i : i+n]
			cw, _ := p.MeasureText(0, ch)
			if cw == 0 {
				// A rune alone loses the advance of a space, which the
				// layout trims at the end of the text: measure it before
				// a letter instead.
				if sentinel < 0 {
					probe := s
					probe.Text = fadeSentinel
					sentinel, _ = p.MeasureText(0, probe)
				}
				probe := s
				probe.Text = ch.Text + fadeSentinel
				adv, _ := p.MeasureText(0, probe)
				cw = max(adv-sentinel, 0)
			}
			if x+cw > r.W-fade {
				if start < i {
					head := s
					head.Text = s.Text[start:i]
					out = append(out, head)
				}
				f := 1 - (x+cw/2-(r.W-fade))/fade
				// A color of no alpha is the text's own color: stop
				// before.
				ch.Color = ch.Color.Alpha(max(f, 0) * max(f, 0))
				if ch.Color.A < 4 {
					return fadeLayout{out, h}
				}
				out = append(out, ch)
				start = i + n
			}
			x += cw
			i += n
		}
		if start < len(s.Text) {
			tail := s
			tail.Text = s.Text[start:]
			out = append(out, tail)
		}
	}
	return fadeLayout{out, h}
}
