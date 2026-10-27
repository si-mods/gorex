package main

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

const (
	gap       = 8  // between panes, and around them
	titleH    = 44 // the title bar
	cardR     = 12 // the panes' corners
	headerH   = 33 // the panes' headers
	activeFor = 1500 * time.Millisecond
)

func (a *App) view(c *ui.Context) {
	a.services = c.Services()
	a.runPosted()
	k := colorsOf(c)
	a.focusedWin = a.win == nil || a.win.IsFocused()
	c.Root().Background(k.bgBottom)
	ui.Column(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { paintBackground(p, r, k) }).Children(func() {
		if prefs.TabPlacement != "" {
			a.sidebarLayout(c, k, prefs.TabPlacement == "right")
			return
		}
		a.titleBar(c, k)
		a.panes(c, k, gap, gap)
	})
	if a.err != "" || a.errMsg != nil {
		a.errorBanner(c, k)
	}
	a.shortcuts(c)
	a.palette(c, k)
	a.workspaceConfirm(c, k)
	if a.saveDue && time.Since(a.lastSave) > time.Second {
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
			ui.Icon(c, icon("x")).Size(17, 17).TextColor(col)
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
	b.Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond})
	col := k.iconMuted
	if b.Hovered() || active {
		col = k.text
	}
	b.Children(func() {
		ui.Icon(c, icon(name)).Size(iconSize, iconSize).TextColor(col)
		if more != nil {
			more()
		}
	})
	return b
}

// tabContent lays out the panes of a tab, or the one zoomed.
func (a *App) tabContent(c *ui.Context, k *colors, t *Tab) {
	if t.Zoom != nil {
		a.paneCard(c, k, t, t.Zoom).Grow(1)
		return
	}
	a.node(c, k, t, t.Root).Grow(1)
}

// node lays out a node of the tree of splits.
func (a *App) node(c *ui.Context, k *colors, t *Tab, n *Node) ui.Element {
	if n.Pane != nil {
		return a.paneCard(c, k, t, n.Pane)
	}
	var box ui.Element
	if n.Vertical {
		box = ui.Column(c.Key(n.ID))
	} else {
		box = ui.Row(c.Key(n.ID))
	}
	box.MinWidth(0).MinHeight(0).AlignItems(ui.Stretch)
	bounds := box.Bounds()
	// The tree may change as its panes build, as a split: build the
	// children it has now.
	first, second, ratio := n.A, n.B, n.Ratio
	box.Children(func() {
		a.node(c, k, t, first).Grow(ratio).Basis(0).MinWidth(0).MinHeight(0)
		div := ui.Box(c.Key("divider")).Role(ui.RoleSplitter).Label(locales.PaneDivider())
		if n.Vertical {
			div.Height(gap).Cursor(ui.CursorResizeRow)
		} else {
			div.Width(gap).Cursor(ui.CursorResizeColumn)
		}
		if dx, dy, ok := div.Dragged(); ok {
			total := bounds.W - gap
			d := dx
			if n.Vertical {
				total, d = bounds.H-gap, dy
			}
			if total > 0 {
				n.Ratio = min(max(n.Ratio+d/total, 0.08), 0.92)
				a.changed()
			}
		}
		if div.DoubleClicked() {
			n.Ratio = 0.5
			a.changed()
		}
		if div.Hovered() || div.Dragging() || div.Pressed() {
			div.Draw(func(p *ui.Painter, r ui.Rect) {
				if n.Vertical {
					p.Fill(ui.Rect{X: r.X + r.W/2 - 18, Y: r.Y + r.H/2 - 1.5, W: 36, H: 3}, k.iconMuted.Alpha(0.6), 1.5)
				} else {
					p.Fill(ui.Rect{X: r.X + r.W/2 - 1.5, Y: r.Y + r.H/2 - 18, W: 3, H: 36}, k.iconMuted.Alpha(0.6), 1.5)
				}
			})
		}
		a.node(c, k, t, second).Grow(1 - ratio).Basis(0).MinWidth(0).MinHeight(0)
	})
	return box
}

// paneCard draws a pane: a card with a header over its terminal.
func (a *App) paneCard(c *ui.Context, k *colors, t *Tab, p *Pane) ui.Element {
	focused := t.Focus == p
	card := ui.Column(c.Key(p.ID)).Radius(cardR).Clip().MinWidth(0).MinHeight(0)
	bg, border, shadow := k.card, k.cardBorder, k.shadow
	if focused {
		bg, border, shadow = k.cardFocused, k.cardBorderFocused, k.shadowFocused
	}
	// With the configuration's colors, the card takes the theme's
	// background, under its header too.
	if termOpaque {
		th := termLight
		if c.Theme().Dark {
			th = termDark
		}
		bg = th.Background
	}
	card.Background(bg).Border(1, border).
		Shadow(0, 1, 2, 0, shadow).
		Shadow(0, 6, 22, -2, shadow)
	card.Transition(ui.ElementTransition{Colors: true, Duration: 160 * time.Millisecond})
	hovered := card.Hovered()
	card.Children(func() {
		top := float32(0)
		if paneHeaders() {
			a.paneHeader(c, k, t, p, focused, hovered)
		} else {
			top = 6
		}
		body := ui.Box(c).Grow(1).MinHeight(0).Padding(top, 5, 6, 5)
		body.Children(func() {
			if p.term == nil {
				return
			}
			tv := terminal.View(c, p.term).Fill()
			if a.focusReq == p && a.tab() == t {
				tv.Focus()
				a.focusReq = nil
			}
			if tv.Focused() && t.Focus != p {
				t.setFocus(p)
				a.changed()
			}
			if tv.Focused() {
				p.attention = false
				if a.focusedWin {
					p.see()
				}
			}
			a.reconnecting(c, k, p)
		})
	})
	p.bounds = card.Bounds()
	if card.Pressed() && t.Focus != p {
		t.setFocus(p)
		a.focusReq = p
	}
	return card
}

// paneHeader draws a pane's program, title and directory, its activity,
// and its buttons.
func (a *App) paneHeader(c *ui.Context, k *colors, t *Tab, p *Pane, focused, hovered bool) {
	name, detail := p.label()
	prog := programOf(p.info.Program)
	if p.info.Idle || p.info.Program == "" {
		prog = programOf(p.info.Shell)
	}
	h := ui.Row(c).Height(headerH).Padding(0, 8, 0, 12).Gap(7).AlignItems(ui.Center).MinWidth(0)
	if h.DoubleClicked() {
		t.setFocus(p)
		a.toggleZoom()
	}
	if h.Clicked() {
		t.setFocus(p)
		a.focusReq = p
	}
	h.Children(func() {
		ui.Icon(c, icon(prog.Glyph)).Size(14.5, 14.5).TextColor(k.text)
		ui.Row(c).Grow(1).MinWidth(0).Gap(5).AlignItems(ui.Center).ClipX().Children(func() {
			ui.Text(c, name).FontSize(12.5).FontWeight(600).TextColor(k.text).SingleLine().Ellipsis("…").Shrink(0).MaxWidthPercent(80)
			if detail != "" {
				ui.Text(c, detail).FontSize(12.5).FontWeight(500).TextColor(k.text.Alpha(0.86)).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			}
			a.activityBadge(c, k, p)
		})
		show := focused || hovered
		ui.Row(c).Gap(1).AlignItems(ui.Center).Opacity(map[bool]float32{true: 1, false: 0}[show]).Children(func() {
			if !show {
				return
			}
			if iconButton(c, k, "columns-2", locales.CmdSplitRight(), 26, 16).Tooltip(tip(locales.CmdSplitRight(), &cmdSplitRight)).Clicked() {
				t.setFocus(p)
				a.split(false)
			}
			if iconButton(c, k, "rows-2", locales.CmdSplitDown(), 26, 16).Tooltip(tip(locales.CmdSplitDown(), &cmdSplitDown)).Clicked() {
				t.setFocus(p)
				a.split(true)
			}
			zoom, label := "maximize-2", locales.PaneZoom()
			if t.Zoom == p {
				zoom, label = "minimize-2", locales.PaneUnzoom()
			}
			if iconButton(c, k, zoom, locales.PaneZoom(), 26, 16).Tooltip(tip(label, &cmdZoom)).Clicked() {
				t.setFocus(p)
				a.toggleZoom()
			}
			if iconButton(c, k, "x", locales.CmdClosePane(), 26, 17).Tooltip(tip(locales.CmdClosePane(), &cmdClosePane)).Clicked() {
				a.later(c, func() { a.closePane(p) })
			}
		})
	})
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
		ui.Box(c).Size(7, 7).Radius(4).Background(k.attention).Margin(0, 0, 0, 2).Tooltip(locales.PaneNeedsAttention())
	}
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
	for _, p := range []string{s.App, state, s.Title, s.Msg} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " · ")
}

// activityDot is the dot of a program printing: a solid dot in a soft
// halo. It does not animate, as drawing frames all along would cost more
// than it tells.
func activityDot(c *ui.Context, col ui.Color, size float32) ui.Element {
	e := ui.Box(c).Size(size+6, size+6).Margin(0, 0, 0, 1).Tooltip(locales.PanePrinting())
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		halo := size/2 + 2.5
		p.Fill(ui.Rect{X: cx - halo, Y: cy - halo, W: 2 * halo, H: 2 * halo}, col.Alpha(0.22), halo)
		p.Fill(ui.Rect{X: cx - size/2, Y: cy - size/2, W: size, H: size}, col, size/2)
	})
	return e
}

// errText is what the error banner says.
func (a *App) errText() string {
	if a.errMsg != nil {
		return say(a.errMsg)
	}
	return a.err
}

// errorBanner shows what went wrong with the server.
func (a *App) errorBanner(c *ui.Context, k *colors) {
	ui.Overlay(c, func() {
		ui.Row(c).Absolute().Bottom(18).Left(0).Right(0).Justify(ui.Center).PassThrough().Children(func() {
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
