package main

import (
	"cmp"
	"fmt"
	"math"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
)

const (
	gap       = 8  // between panes, and around them
	titleH    = 44 // the title bar
	windowR   = 16 // the window's corners, as macOS 27 draws them
	headerH   = 33 // the panes' headers
	activeFor = 1500 * time.Millisecond
)

// cardR is the panes' corners: concentric with the window's, which the
// panes in its bottom corners sit in, and one radius for every pane.
const cardR = windowR - gap

// concentric is the corner radius of a shape inset by inset (its
// container's border and padding) in a container of radius outer, as
// Apple's concentric shapes: the container's less the inset, so that the
// two curves keep one center.
func concentric(outer, inset float32) float32 { return max(outer-inset, 0) }

func (a *App) view(c *ui.Context) {
	a.services = c.Services()
	a.runPosted()
	a.sidebarMoving = false
	a.dividerHeld, a.dividerNow = a.dividerNow, false
	k := a.colors(c)
	a.focusedWin = a.win == nil || a.win.IsFocused()
	breathing = a.focusedWin
	a.claimFocus()
	a.syncSizes()
	c.SetTheme(menuTheme(c.Theme(), k))
	setupMenus(c)
	rootBackground(c, k.bgBottom)
	wash := backgroundAlpha(c)
	ui.Column(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { paintBackground(p, r, k, wash) }).Children(func() {
		if prefs.TabPlacement != "" {
			a.sidebarLayout(c, k, prefs.TabPlacement == "right")
			return
		}
		a.titleBar(c, k)
		a.panes(c, k, gap, gap)
	})
	// The layer over the window is there in every frame, so that what
	// shows in it, as the palette and the error banner, comes in and goes
	// out by its Transition: in a layer new to the frame it would appear at
	// once, and with the layer gone its copy could not go out.
	ui.Overlay(c, func() {})
	if a.err != "" || a.errMsg != nil {
		a.errorBanner(c, k)
	}
	a.shortcuts(c)
	a.palette(c, k)
	a.workspaceConfirm(c, k)
	a.teleportConfirm(c, k)
	// mygo paints a text input's caret in the theme of the frame as it
	// paints, and a field cannot give it one of its own: the widgets above
	// have taken their colors already, so the theme changes now, after
	// them, and the caret of the field with the focus is the space's
	// accent. A popover's rows took their colors as they were made.
	t := *c.Theme()
	t.Accent = k.accent
	c.SetTheme(&t)
	rootBackground(c, k.bgBottom) // SetTheme gave the root the theme's
	if a.saveDue && time.Since(a.lastSave) > time.Second && !a.gesturing() {
		a.save()
	}
	// The window's title, which the Window menu and Mission Control show,
	// is the workspace's and the active tab's.
	if a.tab() != nil && a.win != nil {
		if title := a.windowTitle(); title != a.title {
			a.title = title
			a.win.SetTitle(title)
		}
	}
}

// ownControls tells that the title bar draws the window's controls: on
// Windows, whose own would look like none of the rest of it.
var ownControls = runtime.GOOS == "windows"

// titleBar draws the title bar under the window's controls: the host,
// the tabs, and the buttons of the command palette and a new tab.
func (a *App) titleBar(c *ui.Context, k *colors) {
	bar := c.TitleBar()
	left := bar.Left
	if left == 0 {
		left = 12 // in full screen, or beside no controls
	} else {
		left += 14
	}
	ui.Row(c).Height(titleH).Padding(0, max(bar.Right, 10), 0, left).Gap(14).AlignItems(ui.Center).DragWindow().Children(func() {
		a.hostChip(c, k)
		a.workspaceChip(c, k, false)
		a.tabStrip(c, k)
		// The title bar between the tabs and the buttons, which drags the
		// window, and a double click on which zooms it.
		ui.Spacer(c).MinWidth(titleFree - 14)
		a.titleButtons(c, k, nil)
		a.windowControls(c, k)
	})
}

// titleButtons draws the buttons of the command palette and a new tab,
// then more's.
func (a *App) titleButtons(c *ui.Context, k *colors, more func()) {
	ui.Row(c).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
		if iconButton(c, k, "command", locales.PaletteTitle(), 30, 17).Tooltip(tip(locales.PaletteTitle(), &cmdPalette)).Clicked() {
			a.openPalette()
		}
		if iconButton(c, k, "plus", locales.CmdNewTab(), 30, 19).Tooltip(tip(locales.CmdNewTab(), &cmdNewTab)).Clicked() {
			a.newTab(a.currentDir())
		}
		if more != nil {
			more()
		}
	})
}

// panes lays out the active tab's panes, left and right of them the room
// given.
func (a *App) panes(c *ui.Context, k *colors, left, right float32) {
	ui.Column(c).Grow(1).MinHeight(0).MinWidth(0).Padding(0, right, gap, left).Children(func() {
		if t := a.tab(); t != nil {
			a.tabContent(c, k, t)
		}
	})
}

// windowControls draws the buttons that minimize, maximize and close the
// window, when it has none of its own: they are the title bar's other
// buttons, and close's face turns red, as Windows has it.
func (a *App) windowControls(c *ui.Context, k *colors) { windowControls(c, k, a.win) }

// windowControls draws the controls of win.
func windowControls(c *ui.Context, k *colors, win *mygo.Window) {
	if !ownControls || win == nil || win.IsFullScreen() {
		return
	}
	maximized := win.IsMaximized()
	ui.Row(c).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Box(c).Size(1, 16).Margin(0, 6).Background(k.tabSep)
		if iconButton(c, k, "minus", locales.WindowMinimize(), 30, 16).Tooltip(locales.WindowMinimize()).Clicked() {
			win.Minimize()
		}
		glyph, label := "square", locales.WindowMaximize()
		if maximized {
			glyph, label = "copy", locales.WindowRestore()
		}
		// MaximizeButton shows Windows 11's snap layouts over the button.
		if iconButton(c, k, glyph, label, 30, 13).Tooltip(label).MaximizeButton().Clicked() {
			if maximized {
				win.Unmaximize()
			} else {
				win.Maximize()
			}
		}
		b := ui.Box(c).Size(30, 30).Center().Radius(30 / 2.6).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(locales.WindowClose()).Tooltip(locales.WindowClose())
		col := k.iconMuted
		if b.Pressed() {
			b.Background(closeRed.Alpha(0.8))
			col = ui.Hex("#ffffff")
		} else if b.Hovered() {
			b.Background(closeRed)
			col = ui.Hex("#ffffff")
		}
		b.Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond})
		b.Children(func() {
			glyphIcon(c, "x", 17, col)
		})
		if b.Clicked() {
			win.Close()
		}
	})
}

// closeRed is the face of the close button under the pointer: Windows 11's.
var closeRed = ui.Hex("#c42b1c")

// tip is the tooltip of a button that runs a command: its label and its
// keys.
func tip(label string, cmd *command) string {
	if cmd.Keys == "" {
		return label
	}
	return label + "  " + cmd.Keys
}

// iconButton is a borderless button of an icon, with a face on hover;
// label names it for screen readers and its tooltip.
func iconButton(c *ui.Context, k *colors, name, label string, size, iconSize float32) ui.Element {
	return iconButtonWith(c, k, name, label, size, iconSize, false, nil)
}

// iconButtonWith is iconButton drawn as pressed in while active, with more
// drawn over its icon.
func iconButtonWith(c *ui.Context, k *colors, name, label string, size, iconSize float32, active bool, more func()) ui.Element {
	b := ui.Box(c).Size(size, size).Center().Radius(size / 2.6).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
	if b.Pressed() {
		b.Background(k.pressed)
	} else if b.Hovered() || active {
		b.Background(k.hover)
	}
	if fadeColors {
		b.Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond})
	}
	col := k.iconMuted
	if b.Hovered() || active {
		col = k.text
	}
	b.Children(func() {
		glyphIcon(c, name, iconSize, col)
		if more != nil {
			more()
		}
	})
	return b
}

// tabContent lays out the panes of a tab, or the one zoomed. Each pane is
// a cell placed by its share of the tab's room, and the dividers between
// them over the cells: all of them in one parent, so that the panes move
// to their new places as the layout changes, and open and close there.
func (a *App) tabContent(c *ui.Context, k *colors, t *Tab) {
	a.contentFocus, a.pageHover = false, nil
	a.syncNoteItem()
	room := ui.Box(c.Key(roomKey(t.ID))).Grow(1).MinWidth(0).MinHeight(0)
	bounds := room.Bounds()
	// A tab shown the first time has no bounds yet: the last tab's room
	// stands for its own.
	ref := bounds
	if ref.W <= 0 || ref.H <= 0 {
		ref = a.floatRoom
	}
	a.syncCanvas(t, ref)
	if prefs.Canvas && t.Root != nil {
		// The room is not measured yet: the canvas comes in the next frame.
		c.Invalidate()
	}
	a.floatRoom = bounds
	a.passes++
	tr := a.paneTransition(c, t)
	if t.canvas != nil {
		a.canvasRoom(t, room)
	}
	room.Children(func() {
		if t.canvas != nil {
			a.canvasContent(c, k, t, bounds)
			return
		}
		if t.Zoom != nil {
			a.paneCell(c, k, t, t.Zoom, whole, bounds, tr)
		} else {
			type split struct {
				n *Node
				s share
			}
			var splits []split
			t.Root.shares(whole, func(p *Pane, s share) {
				a.paneCell(c, k, t, p, s, bounds, tr)
			}, func(n *Node, s share) {
				splits = append(splits, split{n, s})
			})
			for _, sp := range splits {
				a.divider(c, k, sp.n, sp.s, bounds, tr)
			}
		}
		var minimized []*Float
		for _, f := range slices.Clone(t.Floats) {
			// A pane is built once: the zoomed one, always tiled, is not
			// a float too.
			if f.Hidden {
				minimized = append(minimized, f)
				continue
			}
			if f.Pane == t.Zoom {
				continue
			}
			a.floatCell(c, k, t, f, bounds)
		}
		if len(minimized) > 0 {
			a.minimizedRail(c, k, minimized)
		}
	})
	// A press on a page gives the page the keyboard, and MyGo clears the
	// content's focus for it: with no pane's content focused and the
	// pointer over a page, the page has the keyboard, and its pane takes
	// the tab's focus, so that the shortcuts act on it.
	if a.focusReq == nil && !a.contentFocus && a.pageHover != nil && t.Focus != a.pageHover &&
		!a.paletteOpen && !a.wsConfirm && !a.teleportAsk.open && !a.wsOpen && !a.hostOpen && a.paneRenaming == nil && a.renaming == nil && a.findPane == nil {
		t.setFocus(a.pageHover)
		a.changed()
	}
}

// share is a part of a tab's room, as fractions of its width and height.
type share struct{ x, y, w, h float32 }

var whole = share{0, 0, 1, 1}

// The keys of a tab's room, and of the cells and dividers in it. A pane's
// cell keeps its key as the pane floats and docks, so that the one element
// moves from its old box to its new one rather than popping out of one and
// into the other.
type (
	roomKey        int
	cellKey        int
	dividerKey     int
	addressKey     int
	noteKey        int
	notePreviewKey int
	floatGripKey   int
	minimizedKey   int
)

// shares gives each pane of the tree of n, and each split, its share of s.
func (n *Node) shares(s share, pane func(*Pane, share), split func(*Node, share)) {
	if n == nil {
		return
	}
	if n.Pane != nil {
		pane(n.Pane, s)
		return
	}
	split(n, s)
	a, b := s, s
	if n.Vertical {
		a.h = s.h * n.Ratio
		b.y, b.h = s.y+a.h, s.h-a.h
	} else {
		a.w = s.w * n.Ratio
		b.x, b.w = s.x+a.w, s.w-a.w
	}
	n.A.shares(a, pane, split)
	n.B.shares(b, pane, split)
}

// cellInsets are the paddings of a pane's cell in its share s of the room:
// half the gap on each side another pane is beside.
func cellInsets(s share) (top, right, bottom, left float32) {
	const half, eps = gap / 2, 1e-4
	if s.y > eps {
		top = half
	}
	if s.x+s.w < 1-eps {
		right = half
	}
	if s.y+s.h < 1-eps {
		bottom = half
	}
	if s.x > eps {
		left = half
	}
	return
}

// paneCell places pane p in its share s of the tab's room, which is
// bounds, half the gap from the panes beside it.
func (a *App) paneCell(c *ui.Context, k *colors, t *Tab, p *Pane, s share, bounds ui.Rect, tr *ui.ElementTransition) {
	top, right, bottom, left := cellInsets(s)
	cell := ui.Column(c.Key(cellKey(p.ID))).Absolute().
		LeftPercent(s.x*100).TopPercent(s.y*100).WidthPercent(s.w*100).HeightPercent(s.h*100).
		Padding(top, right, bottom, left)
	// Where the pane rests, not where it shows on the way there: moving
	// the focus goes by the layout, not by a spring.
	p.bounds = ui.Rect{
		X: bounds.X + s.x*bounds.W + left,
		Y: bounds.Y + s.y*bounds.H + top,
		W: s.w*bounds.W - left - right,
		H: s.h*bounds.H - top - bottom,
	}
	if tr != nil {
		cell.Transition(*tr)
	}
	// While the cell moves, the sidebar resizes the panes, or a divider
	// is dragged, the terminals keep the size they had at rest: a drag
	// would otherwise lay the program's output out at every width it
	// passed through, which the program, told of one size at the end,
	// never wrapped, and its frames would draw over rows.
	held := a.dividerHeld || tr != nil && (cell.Transitioning() || a.sidebarMoving)
	// The size a drag's hold leaves to take follows it by a frame, which
	// the count tells (syncSize): a hold that ends at the size it began
	// takes none.
	if a.dividerHeld {
		p.held = holdGrace
	} else if p.held > 0 {
		p.held--
	}
	cell.Children(func() {
		if tr != nil && tr.Position {
			fadeColors = false
			defer func() { fadeColors = true }()
		}
		a.paneCard(c, k, t, p, held).Grow(1)
	})
}

// cardFace is the face of a pane's card, focused or not: the theme's
// background with the configuration's colors. A float's is opaque, so that
// the tiled panes under it show through nowhere.
func cardFace(c *ui.Context, k *colors, focused, float bool) ui.Color {
	bg := k.card
	if focused {
		bg = k.cardFocused
	}
	if termOpaque {
		bg = termLight.Background
		if c.Theme().Dark {
			bg = termDark.Background
		}
	}
	if float {
		bg.A = 255
	} else if windowGlass(c) {
		// The window's glass shows through the terminals' background, by
		// as much as the setting says.
		bg = bg.Alpha(termOpacity())
	}
	return bg
}

// glassTint is a pane's tint over its glass, which terminal panes have
// under the glass setting: its face as the setting fades it, at 0.4,
// enough to hold its text and little enough for the glass to show; a
// configured theme's whole, whose text is set for its own background,
// not the glass's.
func glassTint(c *ui.Context, k *colors, focused bool) ui.Color {
	bg := cardFace(c, k, focused, false)
	if termOpaque {
		return bg
	}
	return bg.Alpha(0.4)
}

// floatCell places a float's pane over the tab's tiled ones, at its box of
// the room, which is bounds, with a grip that resizes it.
func (a *App) floatCell(c *ui.Context, k *colors, t *Tab, f *Float, bounds ui.Rect) {
	p := f.Pane
	if p == nil {
		return
	}
	cell := ui.Column(c.Key(cellKey(p.ID))).Absolute()
	anim := motion(c)
	if anim == &snappy {
		anim = &springBack
	}
	if f.built+1 != a.passes {
		// Not built in the last pass, as hidden or in another tab: it is
		// at rest.
		f.gliding, f.fling = false, nil
	}
	f.built = a.passes
	// Dragged past the room's edge, the float follows the pointer less and
	// less; let go, it glides from where it showed to its box, at the
	// pointer's velocity, and springs back in.
	s := share{f.X + rubber(f.overX), f.Y + rubber(f.overY), f.W, f.H}
	if fl := f.fling; fl != nil && !fl.started {
		f.gliding = false
		if anim != nil && !f.Zoom {
			fl.started = true
			// The place goes in fractions of the room: it rests within a
			// tenth of a DIP of a room 1000 DIPs across.
			glide := *anim
			glide.Spring.Precision = 1e-4
			cell.Jump(glideX, fl.x)
			cell.Jump(glideY, fl.y)
			// The throw starts it toward its rest, no faster than it
			// settles there. A velocity of zero would keep the one the
			// value had: the least one stands for none.
			rate := glideRate(anim)
			gx, gy := glide, glide
			gx.Spring.Velocity = cmp.Or(glideVelocity(fl.vx, fl.x, f.X, rate), 1e-9)
			gy.Spring.Velocity = cmp.Or(glideVelocity(fl.vy, fl.y, f.Y, rate), 1e-9)
			cell.AnimateTo(glideX, f.X, gx)
			cell.AnimateTo(glideY, f.Y, gy)
			f.gliding = true
		} else {
			f.fling = nil
		}
	}
	if f.gliding {
		if anim == nil || f.Zoom || f.dragging || f.fling == nil {
			f.gliding, f.fling = false, nil
		} else {
			glide := *anim
			glide.Spring.Precision = 1e-4
			s.x, s.y = cell.AnimateTo(glideX, f.X, glide), cell.AnimateTo(glideY, f.Y, glide)
			if s.x == f.X && s.y == f.Y {
				f.gliding, f.fling = false, nil
			} else {
				// Past the room's edges it goes a little way at most,
				// unless it was let go of past them.
				if bounds.W > 0 && f.fling.inside[0] {
					s.x = edgeBand(s.x, f.W, glideOver/bounds.W)
				}
				if bounds.H > 0 && f.fling.inside[1] {
					s.y = edgeBand(s.y, f.H, glideOver/bounds.H)
				}
			}
		}
	}
	f.shown = [2]float32{s.x, s.y}
	if f.Zoom {
		s = whole
	}
	cell.LeftPercent(s.x * 100).TopPercent(s.y * 100).WidthPercent(s.w * 100).HeightPercent(s.h * 100).
		Radius(cardR)
	// Under the glass setting a float is a pane of the system's Liquid
	// Glass, as the tiled panes are; where the system draws none, the
	// tiled panes blurred through its face on a GPU, and opaque elsewhere,
	// as before.
	if face := cardFace(c, k, t.Focus == p, true); glassOn() {
		tint := glassTint(c, k, t.Focus == p)
		cell.Material(ui.SystemGlass{Tint: tint, Fallback: paneGlass{face: tint, flat: face}})
	} else {
		cell.Background(face)
	}
	// Under the pointer, or gliding from it, it moves as it does; else, as
	// it is nudged by the keys, zooms, or floats from its place among the
	// tiled panes, it eases there.
	moves := false
	if anim != nil && !f.dragging && !f.gliding {
		cell.Transition(ui.ElementTransition{Animation: anim, Position: true, Size: true})
		moves = true
	}
	// Shadows as of a sheet lifted off the panes: a tight one where it
	// nearly touches them, a soft one under it, and a wide one of the
	// light around; deeper while it has the focus.
	lift := float32(1)
	if t.Focus == p {
		lift = 1.35
	}
	// Over light cards a soft tinted shadow lifts it; over dark ones, as
	// terminals mostly are, it takes a deep black one to show at all.
	ink := ui.RGBA(40, 30, 40, 1)
	alpha := [3]float32{0.10, 0.14, 0.16}
	if face := cardFace(c, k, false, true); c.Theme().Dark || int(face.R)+int(face.G)+int(face.B) < 3*110 {
		ink = ui.RGBA(0, 0, 0, 1)
		alpha = [3]float32{0.55, 0.5, 0.55}
	}
	// A canvas's boxes lie on its plane, over no panes: they lift off none.
	if !f.free() {
		cell.Shadow(0, 1, 3, 0, ink.Alpha(min(alpha[0]*lift, 1))).
			Shadow(0, 12*lift, 32*lift, -4, ink.Alpha(min(alpha[1]*lift, 1))).
			Shadow(0, 30*lift, 80*lift, -10, ink.Alpha(min(alpha[2]*lift, 1)))
	}
	rest := ui.Rect{
		X: bounds.X + s.x*bounds.W,
		Y: bounds.Y + s.y*bounds.H,
		W: s.w * bounds.W,
		H: s.h * bounds.H,
	}
	// It moves from the frame its box changes, as from its place among
	// the tiled panes, until it rests.
	// Gliding, it moves too.
	moving := f.gliding || moves && (rest != p.bounds || cell.Transitioning())
	p.bounds = rest
	// While the pointer resizes it or it eases to a new size, the terminal
	// keeps the size it rested at, and the program hears of the new one
	// once: else a shell redraws its prompt at every step, over itself.
	held := f.dragging || f.gliding || cell.Transitioning()
	cell.Children(func() {
		// Moving, the card goes with the cell, its colors not fading: an
		// element of a Transition of its own would be laid out where the
		// cell goes, its terminal out of the cell on the way.
		if moving {
			fadeColors = false
			defer func() { fadeColors = true }()
		}
		a.paneCard(c, k, t, p, held).Grow(1)
		if f.Zoom {
			return
		}
		// Every edge and corner resizes it, on a band astride the edge.
		for _, e := range floatEdges {
			a.floatEdge(c, k, f, p, e, bounds)
		}
	})
}

// A float's edges, as the sides of its box a drag of each moves.
const (
	edgeLeft = 1 << iota
	edgeRight
	edgeTop
	edgeBottom
)

// floatEdges are the handles of a float's sides, then of its corners,
// which lie over them.
var floatEdges = []int{
	edgeLeft, edgeRight, edgeTop, edgeBottom,
	edgeLeft | edgeTop, edgeRight | edgeTop, edgeLeft | edgeBottom, edgeRight | edgeBottom,
}

// floatEdge is the handle of edges e of float f: dragging it moves those
// sides of its box, the others staying put.
func (a *App) floatEdge(c *ui.Context, k *colors, f *Float, p *Pane, e int, bounds ui.Rect) {
	const band, corner = 8, 16
	h := ui.Box(c.Key(floatGripKey(p.ID*16 + e))).Absolute()
	switch {
	case e&(edgeLeft|edgeRight) != 0 && e&(edgeTop|edgeBottom) != 0:
		h.Size(corner, corner)
		cur := ui.CursorResizeNWSE
		if e == edgeRight|edgeTop || e == edgeLeft|edgeBottom {
			cur = ui.CursorResizeNESW
		}
		h.Cursor(cur)
	case e&(edgeLeft|edgeRight) != 0:
		h.Width(band).TopPercent(0).HeightPercent(100).Cursor(ui.CursorResizeEW)
	default:
		h.Height(band).LeftPercent(0).WidthPercent(100).Cursor(ui.CursorResizeNS)
	}
	if e&edgeLeft != 0 {
		h.Left(-band / 2)
	}
	if e&edgeRight != 0 {
		h.Right(-band / 2)
	}
	if e&edgeTop != 0 {
		h.Top(-band / 2)
	}
	if e&edgeBottom != 0 {
		h.Bottom(-band / 2)
	}
	if dx, dy, ok := h.Dragged(); ok && bounds.W > 0 && bounds.H > 0 {
		f.resizeEdges(e, dx/bounds.W, dy/bounds.H)
		a.changed()
	}
	f.dragging = f.dragging || h.Dragging()
}

// divider is the edge between the two sides of split n, in its share s of
// the tab's room, which is bounds: dragging it moves the split's ratio.
func (a *App) divider(c *ui.Context, k *colors, n *Node, s share, bounds ui.Rect, tr *ui.ElementTransition) {
	div := ui.Box(c.Key(dividerKey(n.ID))).Absolute().Role(ui.RoleSplitter).Label(locales.PaneDivider())
	total := bounds.W * s.w
	if n.Vertical {
		total = bounds.H * s.h
		div.LeftPercent(s.x*100).WidthPercent(s.w*100).TopPercent((s.y+s.h*n.Ratio)*100).
			Height(gap).Translate(0, -gap/2).Cursor(ui.CursorResizeRow)
	} else {
		div.TopPercent(s.y*100).HeightPercent(s.h*100).LeftPercent((s.x+s.w*n.Ratio)*100).
			Width(gap).Translate(-gap/2, 0).Cursor(ui.CursorResizeColumn)
	}
	if tr != nil {
		// It moves with the cells, but neither opens nor closes.
		div.Transition(ui.ElementTransition{Animation: tr.Animation, Position: tr.Position, Size: tr.Size, Colors: tr.Colors})
	}
	dx, dy, held := div.Dragged()
	d := dx
	if n.Vertical {
		d = dy
	}
	// Held, it drags: the panes follow the pointer at once, and their
	// terminals keep the size they had at rest until the drag ends.
	// Dragged tells of a press, which a pause in the drag keeps, not of a
	// pointer that stays over it; a press that does not move, as a double
	// click's, changes no share and leaves the panes' springs be.
	if held {
		a.dividerNow = true
	}
	if held && d != 0 && total > 0 {
		n.Ratio = min(max(n.Ratio+d/total, 0.08), 0.92)
		a.dividerDrag = true
		a.changed()
	}
	if div.DoubleClicked() {
		n.Ratio = 0.5
		a.changed()
	}
	if div.Hovered() || held {
		div.Draw(func(p *ui.Painter, r ui.Rect) {
			if n.Vertical {
				p.Fill(ui.Rect{X: r.X + r.W/2 - 18, Y: r.Y + r.H/2 - 1.5, W: 36, H: 3}, k.iconMuted.Alpha(0.6), 1.5)
			} else {
				p.Fill(ui.Rect{X: r.X + r.W/2 - 1.5, Y: r.Y + r.H/2 - 18, W: 3, H: 36}, k.iconMuted.Alpha(0.6), 1.5)
			}
		})
	}
}

// paneCard draws a pane: a card with a header over its terminal, which
// holds its size while held.
func (a *App) paneCard(c *ui.Context, k *colors, t *Tab, p *Pane, held bool) ui.Element {
	focused := t.Focus == p
	card := ui.Column(c.Key(p.ID)).Radius(cardR).Clip().MinWidth(0).MinHeight(0)
	bg, border, shadow := k.card, k.cardBorder, k.shadow
	if focused {
		bg, border, shadow = k.cardFocused, k.cardBorderFocused, k.shadowFocused
	}
	// With the configuration's colors, the card takes the theme's
	// background, under its header too.
	hk := k
	if termOpaque {
		hk = headerColors(k, cardFace(c, k, focused, false))
	}
	// The card and its float's cell take one face: the cell's, of another
	// color, would show at the card's rounded corners.
	bg = cardFace(c, k, focused, p.float != nil)
	// Under the glass setting a tiled terminal is a pane of the system's
	// Liquid Glass, its face a tint, and its face where the system draws
	// none; a float takes its face from its cell's glass. A browser pane
	// keeps its face.
	switch {
	case !glassOn() || p.browser != nil && p.float == nil:
		card.Background(bg)
	case p.float == nil:
		card.Material(ui.SystemGlass{Tint: glassTint(c, k, focused), Fallback: faceMaterial{bg}})
	}
	// A float has no edge drawn: the shadows of its cell lift it over the
	// tiled panes.
	if p.float == nil {
		card.Border(1, border).
			Shadow(0, 1, 2, 0, shadow).
			Shadow(0, 6, 22, -2, shadow)
	}
	if fadeColors {
		card.Transition(ui.ElementTransition{Colors: true, Duration: 160 * time.Millisecond})
	}
	hovered := card.Hovered()
	card.Children(func() {
		top := float32(0)
		// A browser pane keeps its header: the address field is the only
		// way to its pages. A note pane keeps its too, which names it.
		if paneHeaders() || p.browser != nil || p.note != nil {
			a.paneHeader(c, hk, t, p, focused, hovered)
		} else if p.float != nil {
			a.floatHandle(c, hk, t, p, p.float)
		} else {
			top = 6
		}
		body := ui.Column(c).Grow(1).MinHeight(0).Padding(top, 5, 6, 5)
		if held {
			// A terminal that outgrows the body as the pane shrinks stays
			// out of sight.
			body.Clip()
		}
		body.Children(func() {
			if p.browser != nil {
				a.browserBody(c, k, t, p)
				return
			}
			if p.note != nil {
				a.noteBody(c, k, t, p)
				return
			}
			if a.findPane == p {
				a.findBar(c, k, p)
			}
			if p.resume != nil {
				a.resumeBar(c, k, p)
			}
			if p.term == nil {
				return
			}
			tv := terminal.View(c, p.term)
			if tv.Pressed() {
				// A press on its terminal catches a gliding float.
				a.catchFloat(p.float)
			}
			if held && p.termSize.W > 0 && p.termSize.H > 0 {
				// Painted at the size it rested at: the program hears of
				// the new size once the pane rests again.
				tv.Width(p.termSize.W).Height(p.termSize.H).Shrink(0)
			} else {
				tv.Grow(1).MinHeight(0)
				if !held {
					if b := tv.LayoutBounds(); b.W > 0 && b.H > 0 {
						p.termSize = b
					}
				}
			}
			if a.focusReq == p && a.tab() == t {
				tv.Focus()
				a.focusReq = nil
			}
			if tv.Focused() && t.Focus != p {
				t.setFocus(p)
				a.changed()
			}
			if tv.Focused() {
				a.contentFocus = true
				p.attention = false
				if a.focusedWin {
					p.see()
				}
			}
			a.reconnecting(c, k, p)
			a.fileDrop(c, k, p, card, tv)
			a.transfers(c, k, p)
			a.teleportOverlay(c, k, p)
		})
	})
	if dragged, over := ui.DragOver[*Pane](card); over {
		if z, ok := paneDropZoneAt(card, dragged, p); ok {
			drawPaneDrop(card, k, z)
			a.paneDrop = shownDrop{p, z}
		}
	}
	if dragged, ok := ui.Drop[*Pane](card); ok {
		// The zone shown last, where the user let go: the pointer may
		// have left the card since, or the card moved.
		z, ok := a.paneDrop.zone, a.paneDrop.target == p
		if !ok {
			z, ok = paneDropZoneAt(card, dragged, p)
		}
		a.paneDrop = shownDrop{}
		// Once the pass is built: a swap now would show p's card again
		// in this pass, still holding the drop.
		if ok {
			a.later(c, func() { a.dropPaneOn(dragged, p, z) })
		}
	}
	if card.Pressed() {
		if f := p.float; f != nil {
			a.catchFloat(f)
			a.raiseFloat(f)
		}
		if t.Focus != p {
			t.setFocus(p)
			a.focusReq = p
		}
	}
	return card
}

// findOpen is how the find bar opens: unfolding down from nothing over
// the terminal, which takes its new size at once, as without it. It has
// no Exit: a bar going out goes under its siblings, and the terminal takes
// its room at once, over it.
var findOpen = &ui.Motion{Collapse: true, Opacity: 0}

// findBar is the find bar of pane p: its query, how the search goes, and
// the buttons that step through the matches and close it.
func (a *App) findBar(c *ui.Context, k *colors, p *Pane) ui.Element {
	row := ui.Row(c.Key("find")).Height(30).Shrink(0).Padding(0, 8).Gap(6).AlignItems(ui.Center).
		Background(k.hover).Radius(7)
	// Not in a pane that moves, as fadeColors tells.
	if anim := motion(c); anim != nil && fadeColors {
		row.Transition(ui.ElementTransition{Animation: anim, Colors: true, Enter: findOpen})
	}
	row.Children(func() {
		in := textField(c.Key("query"), k, &a.findText, fieldCompact).Placeholder(locales.FindPlaceholder()).
			Grow(1).MinWidth(0)
		if a.findFocus {
			in.Focus()
			a.findFocus = false
		}
		in.OnChange(a.findQuery)
		in.OnShortcut(0, ui.KeyEscape, a.closeFind)
		in.OnTextKey(func(k ui.TextKey) bool {
			if k.Key != ui.KeyEnter || k.Mods&^ui.Shift != 0 {
				return false
			}
			if q := k.Text(); q != a.findText {
				a.findText = q
				a.findQuery()
			}
			if k.Mods&ui.Shift != 0 {
				a.findPrevious()
			} else {
				a.findNext()
			}
			return true
		})
		if count := a.findCount(); count != "" {
			ui.Text(c.Key("count"), count).FontSize(11.5).TextColor(k.textMuted).Shrink(0)
		}
		if ui.Button(c.Key("prev"), "↑").Tooltip(locales.FindPrevious()).Clicked() {
			a.findPrevious()
			a.findFocus = true
			a.changed()
		}
		if ui.Button(c.Key("next"), "↓").Tooltip(locales.FindNext()).Clicked() {
			a.findNext()
			a.findFocus = true
			a.changed()
		}
		if ui.Button(c.Key("close"), "×").Tooltip(locales.FindClose()).Clicked() {
			a.closeFind()
		}
	})
	return row
}

// paneHeader draws a pane's program, title and directory, its activity,
// and its buttons.
func (a *App) paneHeader(c *ui.Context, k *colors, t *Tab, p *Pane, focused, hovered bool) {
	if a.paneRenaming == p {
		a.paneRename(c, k)
		return
	}
	if p.browser != nil {
		a.browserHeader(c, k, t, p, focused, hovered)
		return
	}
	name, detail := p.label()
	prog := p.program()
	h := ui.Row(c).Height(headerH).Padding(0, 8, 0, 12).Gap(7).AlignItems(ui.Center).MinWidth(0)
	h.ContextMenu(func(m *ui.Menu) { a.paneMenu(m, p) })
	if f := p.float; f != nil {
		a.dragFloat(c, f, h)
	} else {
		h.Drag(p)
		if h.Dragging() {
			dimmed(h, 0.4)
		}
	}
	if h.DoubleClicked() {
		if f := p.float; f != nil {
			a.raiseFloat(f)
		}
		t.setFocus(p)
		a.toggleZoom()
	}
	if h.Clicked() {
		if f := p.float; f != nil {
			a.raiseFloat(f)
		}
		t.setFocus(p)
		a.focusReq = p
	}
	h.Children(func() {
		glyphIcon(c, prog.Glyph, 14.5, k.text)
		ui.Row(c).Grow(1).MinWidth(0).Gap(5).AlignItems(ui.Center).ClipX().Children(func() {
			ui.RichText(c, titleSpans(name, 12.5, 600, k.text)...).Label(name).SingleLine().Ellipsis("…").Shrink(0).MaxWidthPercent(80)
			if detail != "" {
				ui.RichText(c, titleSpans(detail, 12.5, 500, k.text.Alpha(0.86))...).Label(detail).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			}
			if p.note == nil {
				a.activityBadge(c, k, p)
			}
		})
		a.paneActions(c, k, t, p, focused || hovered)
	})
}

// noteBody shows a note pane's text, which fills the pane and is edited
// in place. Its keys are the text's: none goes to a terminal.
func (a *App) noteBody(c *ui.Context, k *colors, t *Tab, p *Pane) {
	n := p.note
	if n.preview {
		a.notePreviewBody(c, k, t, p)
		return
	}
	// The text is the whole pane: no field of its own inside it, so no
	// border, background or corners, only the pane's card around it.
	in := ui.TextAreaBase(c.Key(noteKey(p.ID)), &n.text).
		TextColor(k.text).SelectionColor(k.accent.Alpha(0.3)).
		FontSize(n.textSize()).Padding(10, 12).Grow(1).MinHeight(0).
		Placeholder(locales.NotePlaceholder()).Label(locales.NotePlaceholder())
	if a.focusReq == p && a.tab() == t {
		in.Focus()
		a.focusReq = nil
	}
	if in.Focused() {
		a.contentFocus = true
		if t.Focus != p {
			t.setFocus(p)
			a.changed()
		}
	}
	// The text is written as it changes, to its own file, not to the
	// layout: the title shows at once, and nothing waits on a save.
	n.save()
}

// notePreviewBody shows a note's text as Markdown, filling the pane and
// scrolling. Focus in it gives the pane the focus, so that the app's
// shortcuts act on it.
func (a *App) notePreviewBody(c *ui.Context, k *colors, t *Tab, p *Pane) {
	n := p.note
	n.md.Theme = n.previewTheme(k)
	sc := ui.Scroll(c.Key(notePreviewKey(p.ID))).Grow(1).MinHeight(0).Focusable().FocusRing(false)
	if a.focusReq == p && a.tab() == t {
		sc.Focus()
		a.focusReq = nil
	}
	if sc.FocusWithin() {
		a.contentFocus = true
		if t.Focus != p {
			t.setFocus(p)
			a.changed()
		}
	}
	sc.Children(func() {
		ui.Column(c).Padding(10, 12).Children(func() {
			if strings.TrimSpace(n.text) == "" {
				ui.Text(c, locales.NotePlaceholder()).FontSize(n.textSize()).TextColor(k.textFaint)
				return
			}
			markdown.View(c, n.text, &n.md)
		})
	})
}

// minimizedRail lists the tab's minimized floats over its bottom right:
// one chip each, which shows that float again. A hidden float is a
// minimized layer to Rex's clients, and GoRex draws no pane for it, so
// this is where the desktop finds it.
func (a *App) minimizedRail(c *ui.Context, k *colors, floats []*Float) {
	rail := ui.Row(c.Key("minimized-rail")).Absolute().Bottom(10).Right(10).Gap(6).AlignItems(ui.Center)
	rail.Children(func() {
		for _, f := range floats {
			if f.Pane == nil {
				continue
			}
			name, _ := f.Pane.label()
			label := locales.FloatRestore(name)
			chip := ui.Row(c.Key(minimizedKey(f.Pane.ID))).Gap(6).Padding(4, 8).Radius(9).
				AlignItems(ui.Center).Background(k.panel).Border(1, k.panelBorder).
				Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
			chip.Tooltip(label)
			if chip.Clicked() {
				a.later(c, func() { a.showFloat(f) })
			}
			chip.Children(func() {
				glyphIcon(c, "square-terminal", 12, k.iconMuted)
				ui.Text(c, name).FontSize(11.5).FontWeight(500).TextColor(k.text).
					SingleLine().Ellipsis("…").MaxWidth(180)
			})
		}
	})
}

// floatHandle is the row a float keeps when pane headers are off: a grip
// that moves it, and its minimize, zoom and close buttons.
func (a *App) floatHandle(c *ui.Context, k *colors, t *Tab, p *Pane, f *Float) {
	h := ui.Row(c).Height(14).Shrink(0).Padding(0, 3, 0, 6).AlignItems(ui.Center).Cursor(ui.CursorMove)
	a.dragFloat(c, f, h)
	if h.Pressed() {
		a.raiseFloat(f)
		t.setFocus(p)
		a.focusReq = p
	}
	h.Children(func() {
		ui.Spacer(c)
		if iconButton(c, k, "minus", locales.PaneMinimize(), 18, 12).Tooltip(locales.PaneMinimize()).Clicked() {
			a.later(c, func() { a.hideFloat(f) })
		}
		zoom, label := "maximize-2", locales.PaneZoom()
		if f.Zoom {
			zoom, label = "minimize-2", locales.PaneUnzoom()
		}
		if iconButton(c, k, zoom, label, 18, 12).Tooltip(label).Clicked() {
			t.setFocus(p)
			a.toggleZoom()
		}
		if iconButton(c, k, "x", locales.CmdClosePane(), 18, 11).Tooltip(locales.CmdClosePane()).Clicked() {
			a.later(c, func() { a.closePane(p) })
		}
	})
}

// paneMenu is the menu of a pane's header: renaming it, and moving it to
// another tab or window.
func (a *App) paneMenu(m *ui.Menu, p *Pane) {
	if m.Item(locales.PaneRename()).Chosen() {
		a.startPaneRename(p)
	}
	if p.Name != "" && m.Item(locales.PaneResetName()).Chosen() {
		p.Name = ""
		a.changed()
	}
	// A canvas's panes are all free: none docks.
	if p.float != nil {
		if m.Item(locales.PaneMinimize()).Chosen() {
			a.hideFloat(p.float)
		}
		if p.Tab != nil && p.Tab.canvas == nil && m.Item(locales.CmdDockPane()).Chosen() {
			a.dockPane(p)
		}
	} else if t := p.Tab; t != nil && len(t.panes()) > 1 {
		if m.Item(locales.CmdFloatPane()).Chosen() {
			a.floatPane(p)
		}
	}
	if len(a.tabs) > 1 {
		m.Separator()
		m.Submenu(locales.PaneMoveTab(), func(sm *ui.Menu) {
			for _, o := range append([]*Tab(nil), a.tabs...) {
				if o == p.Tab || o.Root != nil && o.host() != p.host {
					continue
				}
				o := o
				name, _ := o.label()
				if sm.Item(name).Chosen() {
					a.movePaneToTab(p, o)
				}
			}
		})
	}
	if a.st != nil && len(a.st.apps) > 1 {
		m.Submenu(locales.PaneMoveWindow(), func(sm *ui.Menu) {
			for _, o := range append([]*App(nil), a.st.apps...) {
				if o == a || o.ws == nil {
					continue
				}
				if t := o.tab(); t != nil && t.Root != nil && t.host() != p.host {
					continue
				}
				o := o
				if sm.Item(o.ws.Name).Chosen() {
					a.movePaneToWindow(p, o)
				}
			}
		})
	}
	if p.SID != "" && p.browser == nil && p.note == nil {
		m.Separator()
		m.Submenu(locales.CmdTeleport(), func(sm *ui.Menu) { a.teleportMenu(sm, p) })
	}
}

// paneActions draws a pane's buttons, while show: split right, split
// down, move to a tab of its own, zoom and close. With a style of
// animation they fade in as they show; they hide at once, leaving nothing
// to click. The row is posed faded while they hide, for the fade to start
// from there.
func (a *App) paneActions(c *ui.Context, k *colors, t *Tab, p *Pane, show bool) {
	row := ui.Row(c).Gap(1).AlignItems(ui.Center)
	// The fade only paints, without building the view again.
	anim := motion(c)
	if !show {
		if anim != nil {
			row.AnimatePose(ui.Pose{Fade: 1}, *anim)
		}
		return
	}
	if anim != nil {
		row.AnimatePose(ui.Pose{}, *anim)
	}
	row.Children(func() {
		// The symbols keep the sizes they have beside each other, as Rex
		// draws them, rather than each filling its button.
		naturalGlyphs = true
		defer func() { naturalGlyphs = false }()
		if n := p.note; n != nil {
			icon, label := "eye", locales.NotePreview()
			if n.preview {
				icon, label = "pencil", locales.NoteEdit()
			}
			if iconButton(c, k, icon, label, 26, 16).Tooltip(tip(label, &cmdNotePreview)).Clicked() {
				t.setFocus(p)
				a.toggleNotePreview(p)
			}
		}
		// As Rex has them: split right, split down, float, zoom and close.
		// A float's float button, lit, docks it.
		if f := p.float; f != nil {
			if iconButton(c, k, "minus", locales.PaneMinimize(), 26, 16).Tooltip(locales.PaneMinimize()).Clicked() {
				a.later(c, func() { a.hideFloat(f) })
			}
			// A canvas's panes are all free: none docks.
			if t.canvas == nil && iconButtonWith(c, k, "float", locales.CmdDockPane(), 26, 16, true, nil).Tooltip(tip(locales.CmdDockPane(), &cmdFloatPane)).Clicked() {
				a.later(c, func() { a.dockPane(p) })
			}
		} else {
			if iconButton(c, k, "columns-2", locales.CmdSplitRight(), 26, 16).Tooltip(tip(locales.CmdSplitRight(), &cmdSplitRight)).Clicked() {
				t.setFocus(p)
				a.split(false)
			}
			if iconButton(c, k, "rows-2", locales.CmdSplitDown(), 26, 16).Tooltip(tip(locales.CmdSplitDown(), &cmdSplitDown)).Clicked() {
				t.setFocus(p)
				a.split(true)
			}
			if len(t.panes()) > 1 && iconButton(c, k, "float", locales.CmdFloatPane(), 26, 16).Tooltip(tip(locales.CmdFloatPane(), &cmdFloatPane)).Clicked() {
				a.later(c, func() { a.floatPane(p) })
			}
		}
		zoom, label := "maximize-2", locales.PaneZoom()
		if t.Zoom == p || p.float != nil && p.float.Zoom {
			zoom, label = "minimize-2", locales.PaneUnzoom()
		}
		if iconButton(c, k, zoom, locales.PaneZoom(), 26, 16).Tooltip(tip(label, &cmdZoom)).Clicked() {
			t.setFocus(p)
			a.toggleZoom()
		}
		if iconButton(c, k, "x", locales.CmdClosePane(), 26, 16).Tooltip(tip(locales.CmdClosePane(), &cmdClosePane)).Clicked() {
			a.later(c, func() { a.closePane(p) })
		}
	})
}

// browserHeader draws a browser pane's header: the page's history, its
// address field, and the buttons of every pane.
func (a *App) browserHeader(c *ui.Context, k *colors, t *Tab, p *Pane, focused, hovered bool) {
	b := p.browser
	h := ui.Row(c).Height(headerH).Padding(0, 8, 0, 8).Gap(4).AlignItems(ui.Center).MinWidth(0)
	h.ContextMenu(func(m *ui.Menu) { a.paneMenu(m, p) })
	if f := p.float; f != nil {
		a.dragFloat(c, f, h)
	} else {
		h.Drag(p)
		if h.Dragging() {
			dimmed(h, 0.4)
		}
	}
	if h.DoubleClicked() {
		if f := p.float; f != nil {
			a.raiseFloat(f)
		}
		t.setFocus(p)
		a.toggleZoom()
	}
	if h.Clicked() {
		if f := p.float; f != nil {
			a.raiseFloat(f)
		}
		t.setFocus(p)
		a.focusReq = p
	}
	h.Children(func() {
		ui.Icon(c, icon(browserProgram.Glyph)).Size(14.5, 14.5).TextColor(k.text)
		var page *mygo.Page
		if b.web != nil {
			page = b.web.Page()
		}
		nav := func(glyph, label string, enabled bool, run func()) {
			e := iconButton(c, k, glyph, label, 24, 15)
			if !enabled {
				dimmed(e, 0.35)
			}
			if e.Clicked() && enabled {
				run()
			}
		}
		back, forward := false, false
		if page != nil {
			back, forward = page.CanGoBack(), page.CanGoForward()
		}
		nav("arrow-left", locales.BrowserBack(), back, func() { page.GoBack() })
		nav("arrow-right", locales.BrowserForward(), forward, func() { page.GoForward() })
		nav("rotate-ccw", locales.BrowserReload(), page != nil, func() { page.Reload() })
		in := textField(c.Key(addressKey(p.ID)), k, &b.addr, fieldCompact).
			Placeholder(locales.BrowserAddress()).Label(locales.BrowserAddress()).Grow(1).MinWidth(0)
		if a.focusReq == p && a.tab() == t {
			// The field of a new pane, which has its URL to type; else the
			// page.
			if b.auto {
				in.Focus()
			} else if b.web != nil {
				b.web.Focus()
			}
			a.focusReq = nil
		}
		if in.Focused() {
			a.contentFocus = true
			b.auto = false
			if t.Focus != p {
				t.setFocus(p)
				a.changed()
			}
		}
		b.editing = in.Focused()
		if in.Submitted() {
			a.navigateBrowser(p, b.addr)
			b.auto = false
			a.focusReq = p
		}
		a.paneActions(c, k, t, p, focused || hovered)
	})
}

// browserBody shows a browser pane's page, or a word about why it shows
// none.
func (a *App) browserBody(c *ui.Context, k *colors, t *Tab, p *Pane) {
	b := p.browser
	if b.web == nil {
		ui.Text(c, locales.BrowserUnavailable()).FontSize(13).TextColor(k.textFaint).Padding(12)
		return
	}
	wv := ui.WebView(c, b.web).Grow(1).MinHeight(0)
	if a.focusReq == p && a.tab() == t {
		b.web.Focus()
		a.focusReq = nil
	}
	if wv.Focused() {
		a.contentFocus = true
	}
	if wv.Hovered() {
		a.pageHover = p
	}
	if wv.Focused() && t.Focus != p {
		t.setFocus(p)
		a.changed()
	}
	if wv.Focused() {
		p.attention = false
	}
}

// paneRename draws the label field over a pane's header, in the row the
// header would take: a field of its own, a child of the card, would take
// half of it.
func (a *App) paneRename(c *ui.Context, k *colors) {
	p := a.paneRenaming
	if p == nil {
		return
	}
	var in ui.Element
	ui.Row(c).Height(headerH).Padding(0, 8, 0, 12).Gap(7).AlignItems(ui.Center).Children(func() {
		in = textField(c, k, &a.paneNameText, fieldCompact).Placeholder(p.labelName()).Grow(1).MinWidth(0).AutoFocus()
	})
	done := func(keep bool) {
		if keep {
			p.Name = layout.PaneName(a.paneNameText)
			a.changed()
		}
		a.paneRenaming, a.paneNameFocus = nil, false
		a.focusReq = p
	}
	switch {
	case in.Shortcut(0, ui.KeyEscape):
		done(false)
	case in.Submitted():
		done(true)
	case in.Focused():
		a.paneNameFocus = true
	case a.paneNameFocus:
		done(true)
	}
}

func (a *App) startPaneRename(p *Pane) {
	a.paneRenaming, a.paneNameText, a.paneNameFocus = p, p.Name, false
	a.focusReq = nil
}

// labelName is the pane's current name, or its program's.
func (p *Pane) labelName() string {
	name, _ := p.label()
	return name
}

// reconnecting shows over the screen of a pane whose host or stream is
// away, without hiding it, what is going on.
func (a *App) reconnecting(c *ui.Context, k *colors, p *Pane) {
	h := p.host
	if h == nil {
		return
	}
	var title, detail string
	switch {
	case h.state != hostConnected:
		title, detail = locales.PaneReconnecting(h.Name), locales.PaneConnectionDropped()
	case p.remote != nil && p.remote.Detached():
		title = locales.PaneStreamInterrupted()
	default:
		return
	}
	ui.Row(c).Absolute().Top(10).Left(0).Right(0).Justify(ui.Center).PassThrough().Children(func() {
		ui.Row(c).Gap(10).Padding(8, 14, 8, 12).Radius(12).Background(k.panel).Border(1, k.panelBorder).
			Shadow(0, 8, 24, 0, k.shadowFocused).AlignItems(ui.Center).MaxWidth(460).Label(title).Children(func() {
			ui.Box(c).Size(8, 8).Radius(4).Background(k.attention).Shrink(0)
			ui.Column(c).Gap(2).MinWidth(0).Children(func() {
				ui.Text(c, title).FontSize(12.5).FontWeight(600).TextColor(k.text)
				if detail != "" {
					ui.Text(c, detail).FontSize(12).TextColor(k.textFaint).LineHeight(1.3)
				}
			})
		})
	})
}

// status is what a pane is doing.
type status int

const (
	statusIdle      status = iota // a shell at its prompt
	statusRunning                 // a program printing
	statusQuiet                   // a program running quietly
	statusAttention               // done or ringing out of sight
)

func (a *App) statusOf(c *ui.Context, p *Pane) status {
	if p.attention {
		return statusAttention
	}
	if s := p.status(); s != nil && s.State == "working" {
		return statusRunning
	}
	if p.info.Idle || p.info.Program == "" || programOf(p.info.Program).Shell {
		return statusIdle
	}
	last := time.Unix(0, p.lastData.Load())
	if since := c.Now().Sub(last); since < activeFor {
		c.After(activeFor - since)
		return statusRunning
	}
	return statusQuiet
}

// activityBadge shows what a pane does: a pulse while its program
// prints, a dot when it asks for attention.
func (a *App) activityBadge(c *ui.Context, k *colors, p *Pane) {
	if s := p.status(); needsUser(s) || s != nil && s.State == "working" && !p.attention {
		statusDot(c, k, s, 6).Margin(0, 0, 0, 2)
		return
	}
	switch a.statusOf(c, p) {
	case statusRunning:
		activityDot(c, k.busy, 6)
	case statusAttention:
		attentionDot(c, k, p).Margin(0, 0, 0, 2).Tooltip(locales.PaneNeedsAttention())
	}
}

// attentionPop is how a dot of attention pops as a pane comes to ask for
// it: up to half again its size and back.
var attentionPop = []ui.Keyframe{
	{At: 0, Value: 1},
	{At: 140 * time.Millisecond, Value: 1.5, Ease: ui.EaseOut},
	{At: 350 * time.Millisecond, Value: 1, Ease: ui.EaseInOut},
}

// attentionDot is the dot of a pane, or of a tab of panes, asking for
// attention. With a style of animation it pops as one of them comes to
// ask, from when a frame first shows that: every dot of the pane, as its
// tab's and its header's, at the same point of the pop, whenever it is
// built, and none once it is over.
func attentionDot(c *ui.Context, k *colors, panes ...*Pane) ui.Element {
	dot := ui.Box(c).Size(7, 7).Radius(4).Background(k.attention)
	now := c.Now()
	var at time.Time
	for _, p := range panes {
		if !p.attention {
			continue
		}
		if p.popped != p.asked {
			p.popped, p.popAt = p.asked, now
		}
		if p.popAt.After(at) {
			at = p.popAt
		}
	}
	if motion(c) == nil || at.IsZero() {
		return dot
	}
	if since := now.Sub(at); since < attentionPop[len(attentionPop)-1].At {
		s := keyframeAt(attentionPop, since)
		dot.Scale(s, s)
		c.AnimationFrame()
	}
	return dot
}

// keyframeAt is the value of frames at d from their start, held at the
// last after them.
func keyframeAt(frames []ui.Keyframe, d time.Duration) float32 {
	for i := 1; i < len(frames); i++ {
		a, b := frames[i-1], frames[i]
		if d > b.At {
			continue
		}
		ease := b.Ease
		if ease == nil {
			ease = ui.Linear
		}
		return a.Value + (b.Value-a.Value)*ease(float32(max(d-a.At, 0))/float32(b.At-a.At))
	}
	return frames[len(frames)-1].Value
}

// needsUser tells a record of a program that waits for the user, or whose
// end the user did not see.
func needsUser(s *rex.ProgramStatus) bool {
	return s != nil && rex.StatusRank(s.State) >= rex.StatusRank("done")
}

// statusDot shows a record a program reported: the dot of the activity
// while working, after its progress when known, else the dot of
// attention, red for an error.
func statusDot(c *ui.Context, k *colors, s *rex.ProgramStatus, size float32) ui.Element {
	if s.State == "working" {
		return activityDot(c, k.busy, size).Tooltip(statusTip(s))
	}
	col := k.attention
	if s.State == "error" {
		col = k.danger
	}
	return ui.Box(c).Size(7, 7).Radius(4).Background(col).Shrink(0).Tooltip(statusTip(s))
}

// statusTip says what a record tells: the program, its state, why it
// waits, and its title and message.
func statusTip(s *rex.ProgramStatus) string {
	says := map[string]func() string{
		"idle": locales.StatusIdle, "working": locales.StatusWorking, "blocked": locales.StatusBlocked,
		"done": locales.StatusDone, "error": locales.StatusError,
		"permission": locales.StatusPermission, "question": locales.StatusQuestion, "auth": locales.StatusAuth,
	}
	var state string
	if f := says[s.State]; f != nil {
		state = f()
	}
	if f := says[s.Kind]; f != nil {
		state += ", " + f()
	}
	// The progress shows here only: beside the dot it squeezes the
	// tab's title.
	if s.Progress >= 0 {
		state += fmt.Sprintf(", %d%%", s.Progress)
	}
	var parts []string
	for _, p := range []string{s.App, state, s.Title, s.Message()} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " · ")
}

// breathing tells that the window being built has the focus, which the
// activity dots breathe in: out of it, a program printing for hours would
// keep the window painting for nothing.
var breathing = true

// activityDot is the dot of a program printing: a solid dot in a soft
// halo. With a style of animation the halo breathes while the window has
// the focus, growing and fading in and out, all the dots in step;
// otherwise it stands still.
func activityDot(c *ui.Context, col ui.Color, size float32) ui.Element {
	e := ui.Box(c).Size(size+6, size+6).Margin(0, 0, 0, 1).Tooltip(locales.PanePrinting())
	breathes := breathing && motion(c) != nil
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		halo, alpha := size/2+2.5, float32(0.22)
		if breathes {
			k := breath(p.Now())
			halo, alpha = size/2+1.5+1.5*k, 0.12+0.2*k
			// Only the dot paints again, without building the view, and
			// at a rate the slow change looks as smooth at.
			p.AnimationFrameRate(30)
		}
		p.Fill(ui.Rect{X: cx - halo, Y: cy - halo, W: 2 * halo, H: 2 * halo}, col.Alpha(alpha), halo)
		p.Fill(ui.Rect{X: cx - size/2, Y: cy - size/2, W: size, H: size}, col, size/2)
	})
	return e
}

// breathPeriod is how long a halo takes to grow, and as long to shrink.
const breathPeriod = 1200 * time.Millisecond

// breath is how far a breathing halo is grown at now, from 0 to 1: in and
// out by breathPeriod, easing at both ends, from one epoch for all.
func breath(now time.Time) float32 {
	t := now.UnixNano() % int64(2*breathPeriod)
	f := float32(t) / float32(breathPeriod)
	if f > 1 {
		f = 2 - f
	}
	return ui.EaseInOut(f)
}

// errText is what the error banner says.
func (a *App) errText() string {
	if a.errMsg != nil {
		return say(a.errMsg)
	}
	return a.err
}

// bannerMotion is where the error banner comes up from as it shows, and
// goes as it is dismissed.
var bannerMotion = &ui.Motion{Y: 12, Opacity: 0}

// errorBanner shows what went wrong with the server.
func (a *App) errorBanner(c *ui.Context, k *colors) {
	ui.Overlay(c, func() {
		banner := ui.Row(c.Key("error-banner")).Absolute().Bottom(18).Left(0).Right(0).Justify(ui.Center).PassThrough()
		if anim := motion(c); anim != nil {
			banner.Transition(ui.ElementTransition{Animation: anim, Position: true, Enter: bannerMotion, Exit: bannerMotion})
		}
		banner.Children(func() {
			ui.Row(c).Gap(10).Padding(8, 10, 8, 14).Radius(12).Background(k.panel).Border(1, k.panelBorder).
				Shadow(0, 8, 24, 0, k.shadowFocused).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(8, 8).Radius(4).Background(k.danger)
				ui.Text(c, a.errText()).FontSize(12.5).TextColor(k.text).MaxWidth(520)
				if iconButton(c, k, "x", locales.ErrorDismiss(), 22, 14).Clicked() {
					a.err, a.errMsg = "", nil
				}
			})
		})
	})
}

// dragFloat moves float f as its handle h is dragged, once the pointer
// went floatDragStart from where it pressed, and throws it once the drag
// ends, at the pointer's velocity (throwFloat).
//
// It is not MyGo's Draggable: that moves the element the pointer presses,
// while a float moves by its header, an element of its own (clicked,
// double-clicked, with a menu) inside the float's cell; and the cell's
// Draggable, moved by the app instead, would start its spring at one
// velocity for both axes.
func (a *App) dragFloat(c *ui.Context, f *Float, h ui.Element) {
	dx, dy, pressed := h.Dragged()
	if !pressed {
		f.travel = [2]float32{}
	} else {
		a.catchFloat(f)
		if !f.dragging {
			f.travel[0] += dx
			f.travel[1] += dy
			dx, dy = 0, 0
			if t := f.travel; t[0]*t[0]+t[1]*t[1] >= floatDragStart*floatDragStart {
				f.dragging, f.travel = true, [2]float32{}
				dx, dy = t[0], t[1]
			}
		}
	}
	room := a.floatRoom
	if f.free() {
		// The header's drags come in the canvas's DIPs.
		room = ui.Rect{W: f.Pane.Tab.canvas.W, H: f.Pane.Tab.canvas.H}
	}
	if f.dragging && (dx != 0 || dy != 0) {
		a.moveFloatPx(f, dx, dy, room)
	}
	if f.dragging && !pressed {
		vx, vy, _ := h.DragEnded()
		a.throwFloat(f, vx, vy, room, motion(c) == nil)
	}
}

// glideOver is how far, in DIPs, a gliding float shows past the room's
// edge at most.
const glideOver = 24

// glideRate is the rate, in radians per second, of the spring of anim: a
// glide starting no faster than the distance to its rest times it settles
// there without flying past.
func glideRate(anim *ui.Animation) float32 {
	d := anim.Spring.Duration
	if d <= 0 {
		d = 500 * time.Millisecond
	}
	return float32(2 * math.Pi / d.Seconds())
}

// The keys of the animated place of a float gliding to rest.
type glideKey int

const (
	glideX glideKey = iota
	glideY
)

// rubber is how far a float shows past the room's edge, dragged d past it:
// half as far at first, less and less as d grows, as a scroll view's
// rubber band.
func rubber(d float32) float32 {
	const most, give = 0.6, 0.55
	if d == 0 {
		return 0
	}
	m := most * (1 - 1/(abs(d)*give/most+1))
	if d < 0 {
		return -m
	}
	return m
}
