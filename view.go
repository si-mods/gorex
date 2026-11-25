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

	"gorex/internal/layout"
	"gorex/internal/rex"
	"gorex/locales"
)

const (
	gap     = 8  // between panes, and around them
	titleH  = 44 // the title bar
	windowR = 16 // the window's corners where the system tells none, as macOS 27 draws them
	headerH = 33 // the panes' headers
)

// cardR is the panes' corners: concentric with the window's, which the
// panes in its bottom corners sit in, and one radius for every pane. The
// window's is, on macOS only, the radius the system rounds it to
// (Window.CornerRadius, read once a frame in view), as that varies with
// the title bar; windowR where that is 0 (full screen), on Windows and
// Linux, and in the tests, whose views have no window. Windows keeps its
// rounded panes in every window state.
func (a *App) cardR(k *colors) float32 { return k.radius(cardRadius(a.winR, runtime.GOOS)) }

// cardRadius is the panes' radius in a window that the system rounds to r
// DIPs, on the operating system goos: r is only taken on macOS, and only
// when it is above 0.
func cardRadius(r float64, goos string) float32 {
	if goos != "darwin" || r <= 0 {
		return concentric(windowR, gap)
	}
	return concentric(float32(r), gap)
}

// concentric is the corner radius of a shape inset by inset (its
// container's border and padding) in a container of radius outer, as
// Apple's concentric shapes: the container's less the inset, so that the
// two curves keep one center.
func concentric(outer, inset float32) float32 { return max(outer-inset, 0) }

func (a *App) syncHostTheme(dark bool) {
	if a.client == nil {
		return
	}
	src := termLight
	if dark {
		src = termDark
	}
	if src == nil {
		return
	}
	hex := func(c ui.Color) string {
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	th := &rex.Theme{
		Foreground: hex(src.Foreground),
		Background: hex(src.Background),
		Cursor:     hex(src.Cursor),
		Palette:    make([]string, 0, len(src.Palette)),
	}
	for i, c := range src.Palette {
		th.Palette = append(th.Palette, fmt.Sprintf("%d=%s", i, hex(c)))
	}
	key := th.Foreground + "|" + th.Background + "|" + th.Cursor + "|" + strings.Join(th.Palette, ",")
	if key == a.hostTheme {
		return
	}
	a.hostTheme = key
	cl := a.client
	go func() { _ = cl.SetTheme(th) }()
}

func (a *App) view(c *ui.Context) {
	a.services = c.Services()
	a.runPosted()
	a.sidebarMoving = false
	a.dividerHeld, a.dividerNow = a.dividerNow, false
	k := a.colors(c)
	a.syncHostTheme(c.Theme().Dark)
	a.focusedWin = a.win == nil || a.win.IsFocused()
	a.winR = 0
	if a.win != nil && runtime.GOOS == "darwin" {
		a.winR = a.win.CornerRadius()
	}
	breathing = a.focusedWin
	if a.focusedWin {
		a.syncHostItems()
	}
	a.claimFocus()
	a.syncSizes()
	c.SetTheme(menuTheme(c.Theme(), k))
	setupMenus(c)
	rootBackground(c, k.bgBottom)
	wash, moves := backgroundAlpha(c), themeShaderMoves(c)
	dark := c.Theme().Dark
	root := ui.Column(c).Fill()
	sw := windowShader(root)
	root.Draw(func(p *ui.Painter, r ui.Rect) {
		paintBackground(p, r, k, wash, dark, moves, sw)
	}).Children(func() {
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
	} else {
		a.bannerTop = 0
	}
	a.shortcuts(c)
	a.palette(c, k)
	a.promptPanel(c, k)
	a.workspaceConfirm(c, k)
	a.closeAskFallback(c, k)
	a.spaceDialog(c, k)
	a.projectSheetView(c, k)
	a.teleportConfirm(c, k)
	a.quitConfirm(c, k)
	// Over the dialogs: a toast answers what the user did just now.
	a.toasts(c, k)
	// mygo paints a text input's caret in the theme of the frame as it
	// paints, and a field cannot give it one of its own: the widgets above
	// have taken their colors already, so the theme changes now, after
	// them, and the caret of the field with the focus is the space's
	// accent, as are the shades that go with it (focus ring, selection).
	// A popover's rows took their colors as they were made.
	c.SetTheme(c.Theme().WithAccent(k.accent))
	rootBackground(c, k.bgBottom) // SetTheme gave the root the theme's
	if a.saveDue && time.Since(a.lastSave) > saveDelay() && !a.gesturing() {
		a.save()
	}
	// The window's title, which the Window menu and Mission Control show,
	// is the workspace's and the active tab's, or the workspace's alone
	// while its project has no tab.
	if a.win != nil {
		if title := a.windowTitle(); title != "" && title != a.title {
			a.title = title
			a.win.SetTitle(title)
		}
	}
}

// ownControls tells that the title bar draws the window's controls: on
// Windows, whose own would look like none of the rest of it.
var ownControls = runtime.GOOS == "windows"

// windowCorner is the radius MyGo cuts a window's corners to: Linux X11
// draws these windows as plain rectangles without a frame, while macOS
// and Windows give them the system's corners.
var windowCorner = func() int {
	if runtime.GOOS == "linux" {
		return windowR
	}
	return 0
}()

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
	row := ui.Row(c).Height(titleH).Padding(0, max(bar.Right, 10), 0, left).Gap(14).AlignItems(ui.Center).DragWindow()
	// A theme's titleBar.background is the row's face, with the text of
	// its lightness (docs/DSK-012-theme-shaders.md, New roles).
	k, face, ok := themedFace(k, roleTitleBarBackground)
	if ok {
		paintFace(row, k, roleTitleBarBackground, face)
	}
	row.Children(func() {
		a.spacePicker(c, k, false)
		a.projectPicker(c, k)
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
		if naturalIconButton(c, k, "command", locales.PaletteTitle(), 30, 18).Tooltip(tip(locales.PaletteTitle(), &cmdPalette)).Clicked() {
			a.openPalette()
		}
		if naturalIconButton(c, k, "plus", locales.CmdNewTab(), 30, 18).Tooltip(tip(locales.CmdNewTab(), &cmdNewTab)).Clicked() {
			a.newProjectTab()
		}
		if more != nil {
			more()
		}
	})
}

// panes lays out the active tab's panes, left and right of them the room
// given.
func (a *App) panes(c *ui.Context, k *colors, left, right float32) {
	col := ui.Column(c).Grow(1).MinHeight(0).MinWidth(0).Padding(0, right, gap, left).ClipX()
	if t := a.tab(); t != nil {
		if a.lastTabID != 0 && a.lastTabID != t.ID {
			if a.active < a.lastTabIdx {
				a.tabSlideDir = -1
			} else {
				a.tabSlideDir = 1
			}
		}
		a.lastTabID = t.ID
		a.lastTabIdx = a.active
		if anim := motion(c); anim != nil && a.tabSlideDir != 0 {
			col.ChildExit(ui.Motion{XPercent: -a.tabSlideDir, Opacity: 0})
		}
		col.Children(func() {
			a.tabContent(c, k, t)
		})
	} else {
		col.Children(func() {
			a.emptyNoTab(c, k)
		})
	}
}

// emptyNoTab is the empty state of the main area with no tab open
// (docs/DSK-006-empty-states.md): a wireframe of a window and its tabs,
// the buttons that make the first — a new tab, and the command palette.
func (a *App) emptyNoTab(c *ui.Context, k *colors) {
	emptyPage(c, k, func(p *ui.Painter, r ui.Rect, k *colors) {
		drawEmptyScene(p, r, k, true, c.Theme().Dark)
	}, locales.EmptyNoTab(), locales.EmptyNoTabNote(),
		emptyAction{locales.CmdNewTab(), func() { a.newProjectTab() }},
		emptyAction{locales.CmdCommandPalette(), func() { a.openPalette() }})
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
		b := ui.Box(c).Size(30, 30).Center().Radius(k.capsule(30)).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(locales.WindowClose()).Tooltip(locales.WindowClose())
		col := k.iconMuted
		if b.Pressed() {
			b.Background(closeRed.Alpha(0.8))
			col = ui.Hex("#ffffff")
		} else if b.Hovered() {
			b.Background(closeRed)
			col = ui.Hex("#ffffff")
		}
		b.Transition(hoverFade)
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
	return drawIconButton(c, k, name, label, size, iconSize, false, false, nil)
}

// iconButtonWith is iconButton drawn as pressed in while active, with more
// drawn over its icon.
func iconButtonWith(c *ui.Context, k *colors, name, label string, size, iconSize float32, active bool, more func()) ui.Element {
	return drawIconButton(c, k, name, label, size, iconSize, active, false, more)
}

// naturalIconButton is iconButton whose SF Symbol keeps its natural
// proportion beside others in a button row, as Rex draws them.
func naturalIconButton(c *ui.Context, k *colors, name, label string, size, iconSize float32) ui.Element {
	return drawIconButton(c, k, name, label, size, iconSize, false, true, nil)
}

// naturalIconButtonWith is naturalIconButton drawn as pressed in while active.
func naturalIconButtonWith(c *ui.Context, k *colors, name, label string, size, iconSize float32, active bool) ui.Element {
	return drawIconButton(c, k, name, label, size, iconSize, active, true, nil)
}

func drawIconButton(c *ui.Context, k *colors, name, label string, size, iconSize float32, active, natural bool, more func()) ui.Element {
	b := ui.Box(c).Size(size, size).Center().Radius(size / 2).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
	if b.Pressed() {
		b.Background(k.pressed)
	} else if b.Hovered() || active {
		b.Background(k.hover)
	}
	if fadeColors {
		b.Transition(hoverFade)
	}
	col := k.iconMuted
	if b.Hovered() || active {
		col = k.text
	}
	b.Children(func() {
		glyphIconWith(c, name, iconSize, col, natural)
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
	room := ui.Box(c.Key(roomKey(t.ID))).Grow(1).MinWidth(0).MinHeight(0)
	if anim := motion(c); anim != nil && a.tabSlideDir != 0 {
		room.Transition(exitIn(ui.ElementTransition{
			Animation: anim,
			Enter:     &ui.Motion{XPercent: a.tabSlideDir, Opacity: 0},
			Exit:      &ui.Motion{XPercent: -a.tabSlideDir, Opacity: 0},
		}, durExitMove))
	}
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
	roomKey      int
	cellKey      int
	dividerKey   int
	addressKey   int
	floatGripKey int
	minimizedKey int
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

// restAt records rest as where pane p rests, and tells whether it rested
// elsewhere: its cell starts to move in this frame, which Transitioning
// tells of only in the frame after.
func (p *Pane) restAt(rest ui.Rect) bool {
	moved := rest != p.bounds
	p.bounds = rest
	return moved
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
	rest := ui.Rect{
		X: bounds.X + s.x*bounds.W + left,
		Y: bounds.Y + s.y*bounds.H + top,
		W: s.w*bounds.W - left - right,
		H: s.h*bounds.H - top - bottom,
	}
	starts := p.restAt(rest) && tr != nil && tr.Size
	if tr != nil {
		cell.Transition(*tr)
	}
	// While the cell moves, the sidebar resizes the panes, or a divider
	// is dragged, the terminals keep the size they had at rest: a drag
	// would otherwise lay the program's output out at every width it
	// passed through, which the program, told of one size at the end,
	// never wrapped, and its frames would draw over rows.
	held := a.dividerHeld || tr != nil && (starts || cell.Transitioning() || a.sidebarMoving)
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
		Radius(a.cardR(k))
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
	starts := p.restAt(rest) && moves
	moving := f.gliding || starts || moves && cell.Transitioning()
	// While the pointer resizes it or it eases to a new size, the terminal
	// keeps the size it rested at, and the program hears of the new one
	// once: else a shell redraws its prompt at every step, over itself.
	held := f.dragging || f.gliding || starts || cell.Transitioning()
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
	card := ui.Column(c.Key(p.ID)).Radius(a.cardR(k)).Clip().MinWidth(0).MinHeight(0)
	// With the configuration's colors, the card takes the theme's
	// background, under its header too, and its edge the palette of that
	// background's lightness.
	hk := k
	if termOpaque {
		hk = headerColors(k, cardFace(c, k, focused, false))
	}
	border, shadow := hk.cardBorder, k.shadow
	if focused {
		border, shadow = hk.cardBorderFocused, k.shadowFocused
	}
	// A theme's pane.border is the edge, focused or not: the shadow tells
	// the focus (docs/DSK-012-theme-shaders.md, New roles).
	if k.paneBorder.A > 0 {
		border = k.paneBorder
	}
	// The card and its float's cell take one face: the cell's, of another
	// color, would show at the card's rounded corners.
	bg := cardFace(c, k, focused, p.float != nil)
	// Under the glass setting a tiled terminal is a pane of the system's
	// Liquid Glass, its face a tint, and its face where the system draws
	// none; a float takes its face from its cell's glass. A browser pane
	// keeps its face.
	switch {
	case !glassOn() || p.ext != nil && p.ext.opaqueTiled() && p.float == nil:
		card.Background(bg)
	case p.float == nil:
		card.Material(ui.SystemGlass{Tint: glassTint(c, k, focused), Fallback: faceMaterial{bg}})
	}
	// A float has no edge drawn: the shadows of its cell lift it over the
	// tiled panes.
	if p.float == nil {
		card.Border(k.borderW(), border).
			Shadow(0, 1, 2, 0, shadow).
			Shadow(0, 6, 22, -2, shadow)
	}
	if fadeColors {
		card.Transition(hoverFade)
	}
	hovered := card.Hovered()
	card.Children(func() {
		top := float32(0)
		// A browser pane keeps its header: the address field is the only
		// way to its pages. A note pane keeps its too, which names it.
		if paneHeaders() || p.ext != nil && p.ext.keepsHeader() {
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
			if p.ext != nil {
				p.ext.body(a, c, k, t, p)
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
			drawPaneDrop(card, k, z, a.cardR(k))
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
		Background(k.hover).Radius(k.radius(radiusS))
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
			ui.Text(c.Key("count"), count).FontSize(fontFootnote).TextColor(k.textMuted).Shrink(0)
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
	if p.ext != nil && p.ext.header(a, c, k, t, p, focused, hovered) {
		return
	}
	name, detail := p.label()
	prog := p.program()
	h := ui.Row(c).Height(headerH).Padding(0, 8, 0, 12).Gap(8).AlignItems(ui.Center).MinWidth(0)
	// A theme's paneHeader.background is the header's face, with the text
	// of its lightness (docs/DSK-012-theme-shaders.md, New roles).
	k, face, ok := themedFace(k, rolePaneHeaderBackground)
	if ok {
		paintFace(h, k, rolePaneHeaderBackground, face)
	}
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
		programIconEl(c, prog.Icon, prog.Glyph, 14.5, k.text)
		ui.Row(c).Grow(1).MinWidth(0).Gap(6).AlignItems(ui.Center).ClipX().Children(func() {
			ui.RichText(c, titleSpans(name, 12.5, 600, k.text)...).Label(name).SingleLine().Ellipsis("…").Shrink(0).MaxWidthPercent(80)
			if detail != "" {
				ui.RichText(c, titleSpans(detail, 12.5, 500, k.text.Alpha(0.86))...).Label(detail).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			}
			if p.ext == nil || p.ext.badge() {
				a.activityBadge(c, k, p)
			}
		})
		a.paneActions(c, k, t, p, focused || hovered)
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
			chip := ui.Row(c.Key(minimizedKey(f.Pane.ID))).Gap(6).Padding(4, 8).Radius(k.radius(radiusM)).
				AlignItems(ui.Center).Background(k.panel).Border(k.borderW(), k.panelBorder).
				Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
			chip.Tooltip(label)
			if chip.Clicked() {
				a.later(c, func() { a.showFloat(f) })
			}
			chip.Children(func() {
				glyphIcon(c, "square-terminal", 12, k.iconMuted)
				ui.Text(c, name).FontSize(fontFootnote).FontWeight(500).TextColor(k.text).
					SingleLine().Ellipsis("…").MaxWidth(180)
			})
		}
	})
}

// floatHandle is the row a float keeps when pane headers are off: a grip
// that moves it, and its minimize, zoom and close buttons.
func (a *App) floatHandle(c *ui.Context, k *colors, t *Tab, p *Pane, f *Float) {
	h := ui.Row(c).Height(14).Shrink(0).Padding(0, 4, 0, 6).AlignItems(ui.Center).Cursor(ui.CursorMove)
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
		x := iconButton(c, k, "x", locales.CmdClosePane(), 18, 11).Tooltip(locales.CmdClosePane())
		if x.Clicked() {
			a.later(c, func() { a.askClosePane(p) })
		}
		a.closeConfirm(c, k, x, t, p)
	})
}

// paneMenu is the menu of a pane's header: renaming it, and moving it to
// another tab or window.
func (a *App) paneMenu(m *ui.Menu, p *Pane) {
	if m.Item(locales.PaneRename()).Chosen() {
		a.startPaneRename(p)
	}
	if p.Name != "" && m.Item(locales.PaneResetName()).Chosen() {
		a.setPaneName(p, "")
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
				if sm.Item(o.ws.Name).Chosen() {
					a.movePaneToWindow(p, o)
				}
			}
		})
	}
	if p.SID != "" && p.ext == nil {
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
	// They stay while the close button asks before closing.
	show = show || a.asking(t, p)
	row := ui.Row(c).Gap(2).AlignItems(ui.Center)
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
		if p.ext != nil {
			p.ext.headerActions(a, c, k, t, p)
		}
		// As Rex has them: split right, split down, float, zoom and close.
		// A float's float button, lit, docks it.
		if f := p.float; f != nil {
			if naturalIconButton(c, k, "minus", locales.PaneMinimize(), 26, 16).Tooltip(locales.PaneMinimize()).Clicked() {
				a.later(c, func() { a.hideFloat(f) })
			}
			// A canvas's panes are all free: none docks.
			if t.canvas == nil && naturalIconButtonWith(c, k, "float", locales.CmdDockPane(), 26, 16, true).Tooltip(tip(locales.CmdDockPane(), &cmdFloatPane)).Clicked() {
				a.later(c, func() { a.dockPane(p) })
			}
		} else {
			if naturalIconButton(c, k, "columns-2", locales.CmdSplitRight(), 26, 16).Tooltip(tip(locales.CmdSplitRight(), &cmdSplitRight)).Clicked() {
				t.setFocus(p)
				a.split(false)
			}
			if naturalIconButton(c, k, "rows-2", locales.CmdSplitDown(), 26, 16).Tooltip(tip(locales.CmdSplitDown(), &cmdSplitDown)).Clicked() {
				t.setFocus(p)
				a.split(true)
			}
			if len(t.panes()) > 1 && naturalIconButton(c, k, "float", locales.CmdFloatPane(), 26, 16).Tooltip(tip(locales.CmdFloatPane(), &cmdFloatPane)).Clicked() {
				a.later(c, func() { a.floatPane(p) })
			}
		}
		zoom, label := "maximize-2", locales.PaneZoom()
		if t.Zoom == p || p.float != nil && p.float.Zoom {
			zoom, label = "minimize-2", locales.PaneUnzoom()
		}
		if naturalIconButton(c, k, zoom, label, 26, 16).Tooltip(tip(label, &cmdZoom)).Clicked() {
			t.setFocus(p)
			a.toggleZoom()
		}
		x := naturalIconButton(c, k, "x", locales.CmdClosePane(), 26, 16).Tooltip(tip(locales.CmdClosePane(), &cmdClosePane))
		if x.Clicked() {
			a.later(c, func() { a.askClosePane(p) })
		}
		a.closeConfirm(c, k, x, t, p)
	})
}

func (a *App) paneRename(c *ui.Context, k *colors) {
	p := a.paneRenaming
	if p == nil {
		return
	}
	var in ui.Element
	ui.Row(c).Height(headerH).Padding(0, 8, 0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		in = textField(c, k, &a.paneNameText, fieldCompact).Placeholder(p.labelName()).Grow(1).MinWidth(0).AutoFocus()
	})
	done := func(keep bool) {
		if keep {
			a.setPaneName(p, layout.PaneName(a.paneNameText))
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
	var title, detail string
	switch {
	case p.host == nil:
		// A pane of this machine's session: its stream attaches again on
		// its own, and until it does the screen is the last it showed.
		// Said once the loss lasted (streamNoteDelay), not the moment it
		// happens: a stream that resumed at once shows nothing.
		if p.stream == nil || p.stream.DetachedFor() < streamNoteDelay {
			return
		}
		title, detail = locales.PaneStreamInterrupted(), locales.PaneConnectionDropped()
	case p.host.state != hostConnected:
		title, detail = locales.PaneReconnecting(p.host.label()), locales.PaneConnectionDropped()
	case p.remote != nil && p.remote.DetachedFor() >= streamNoteDelay:
		title = locales.PaneStreamInterrupted()
	default:
		return
	}
	ui.Row(c).Absolute().Top(10).Left(0).Right(0).Justify(ui.Center).PassThrough().Children(func() {
		toastShadow(ui.Row(c).Gap(10).Padding(8, 14, 8, 12).Radius(k.radius(radiusL)).Background(k.panel).Border(k.borderW(), k.panelBorder), k).AlignItems(ui.Center).MaxWidth(460).Label(title).Children(func() {
			ui.Box(c).Size(8, 8).Radius(k.capsule(8)).Background(k.attention).Shrink(0)
			ui.Column(c).Gap(2).MinWidth(0).Children(func() {
				ui.Text(c, title).FontSize(fontCallout).FontWeight(600).TextColor(k.text)
				if detail != "" {
					ui.Text(c, detail).FontSize(fontSubhead).TextColor(k.textFaint).LineHeight(1.3)
				}
			})
		})
	})
}

// activityBadge shows what a pane does: the dots of its record, or the
// dot of a pane asking for attention.
func (a *App) activityBadge(c *ui.Context, k *colors, p *Pane) {
	if s := p.status(); needsUser(s) || s != nil && s.State == "working" && !p.attention {
		statusDot(c, k, s, 6).Margin(0, 0, 0, 2)
		return
	}
	if p.attention {
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
	dot := ui.Box(c).Size(7, 7).Radius(k.capsule(7)).Background(k.attention)
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

// statusDot shows a record a program reported: three animated dots while
// working, else the dot of attention, red for an error.
func statusDot(c *ui.Context, k *colors, s *rex.ProgramStatus, size float32) ui.Element {
	if s.State == "working" {
		return workingDots(c, k.textMuted).Tooltip(statusTip(s))
	}
	col := k.attention
	if s.State == "error" {
		col = k.danger
	}
	return ui.Box(c).Size(7, 7).Radius(k.capsule(7)).Background(col).Shrink(0).Tooltip(statusTip(s))
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
// working dots animate in: out of it, a program working for hours would
// keep the window painting for nothing.
var breathing = true

// workingDots draws three dots that pulse and hop left-to-right while a
// program is working, or stand still when unfocused or animation is off.
func workingDots(c *ui.Context, col ui.Color) ui.Element {
	const dotBase = float32(3.0)
	const gap = float32(2.5)
	e := ui.Box(c).Size(16, 10).Shrink(0)
	animates := breathing && motion(c) != nil
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		if animates {
			p.AnimationFrameRate(30)
		}
		startX := r.X + (r.W-(3*dotBase+2*gap))/2 + dotBase/2
		cy := r.Y + r.H/2
		for i := range 3 {
			alpha := float32(0.75)
			d := dotBase
			dy := float32(0)
			if animates {
				phase := dotPhase(p.Now(), i)
				alpha = 0.35 + 0.65*phase
				d = dotBase * (0.85 + 0.2*phase)
				dy = -1.5 * phase
			}
			cx := startX + float32(i)*(dotBase+gap)
			p.Fill(ui.Rect{X: cx - d/2, Y: cy + dy - d/2, W: d, H: d}, col.Alpha(alpha), d/2)
		}
	})
	return e
}

// dotPhase returns the 0..1 wave value for dot index at now across a
// 1.2s cycle, staggered left-to-right.
func dotPhase(now time.Time, index int) float32 {
	const period = 1200 * time.Millisecond
	t := float64(now.UnixNano()) / float64(period)
	shifted := math.Mod(t-float64(index)*0.16, 1)
	if shifted < 0 {
		shifted += 1
	}
	if shifted >= 0.5 {
		return 0
	}
	return float32(0.5 - 0.5*math.Cos(2*math.Pi*(shifted/0.5)))
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

// bannerBottom is how far the banner, and a toast while no banner shows,
// sit above the window's bottom edge.
const bannerBottom = 18

// errorBanner shows what went wrong with the server, or a note: a wait,
// whose dot is not an error's, or one with a button that acts on it.
func (a *App) errorBanner(c *ui.Context, k *colors) {
	n := a.note()
	ui.Overlay(c, func() {
		// Under the other overlays: a sheet's or a dialog's scrim covers it,
		// as it covers the window.
		banner := ui.Row(c.Key("error-banner")).Absolute().Bottom(bannerBottom).Left(0).Right(0).Justify(ui.Center).PassThrough().ZIndex(-1)
		if anim := motion(c); anim != nil {
			banner.Transition(exitIn(ui.ElementTransition{Animation: anim, Position: true, Enter: bannerMotion, Exit: bannerMotion}, durExitMove))
		}
		banner.Children(func() {
			panel := toastShadow(ui.Row(c).Gap(10).Padding(8, 10, 8, 14).Radius(k.radius(radiusL)).Background(k.panel).Border(k.borderW(), k.panelBorder), k).AlignItems(ui.Center)
			// A toast sits above the panel: a frame more once it moved.
			if top := panel.Bounds().Y; top != a.bannerTop {
				a.bannerTop = top
				c.Invalidate()
			}
			panel.Children(func() {
				dot := k.danger
				if n != nil && (n.wait || n.info) {
					dot = k.busy
				}
				ui.Box(c).Size(8, 8).Radius(k.capsule(8)).Background(dot)
				ui.Text(c, a.errText()).FontSize(fontCallout).TextColor(k.text).MaxWidth(520)
				button := func(key string, label func(locales.Messages) string, run func()) {
					if label != nil && ui.Button(c.Key(key), say(label)).Clicked() {
						a.clearNote()
						a.later(c, run)
					}
				}
				if n != nil {
					button("note-action", n.action, n.run)
					button("note-alt", n.alt, n.altRun)
				}
				if iconButton(c, k, "x", locales.ErrorDismiss(), 22, 14).Clicked() {
					a.clearNote()
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
