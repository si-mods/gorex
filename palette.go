package main

import (
	"cmp"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

func (a *App) openPalette() {
	// Opened again while open, as it switches to the spaces, it stays
	// where it is.
	a.paletteFresh = a.paletteFresh || !a.paletteOpen
	a.paletteOpen, a.paletteQuery, a.paletteSel, a.paletteSelID, a.paletteList = true, "", 0, nil, listCommands
	a.focusReq = nil // the palette takes the focus
}

// openSessionPalette opens the palette on the workspaces, to show one.
func (a *App) openSessionPalette() {
	if a.st != nil {
		a.openPalette()
		a.paletteList = listSessions
	}
}

// paletteItem is a row of the command palette: a command, or a pane to
// go to.
type paletteItem struct {
	title, detail, keys string
	glyph               string
	icon                *rex.ProgramIcon
	run                 func()
	// status is what the program of a pane reported.
	status *rex.ProgramStatus
	// dot is the color of the space a row stands for, drawn before its
	// title, when hasDot.
	dot    ui.Color
	hasDot bool
	// disabled rows show, dimmed, what cannot run now, detail saying why.
	disabled bool
	// header rows name the section of the rows after them; aside is said
	// at the end of a row, before how long ago its status came when the
	// palette lists what needs the user.
	header bool
	aside  string
	// id is what the row stands for, which the selection follows as the
	// rows change under it; nil for rows that hold still.
	id any
}

// chosen tells whether the selection may rest on the row: not a
// section's header, nor a row that cannot run.
func (it paletteItem) chosen() bool { return !it.header && !it.disabled }

// paletteStep moves the selection from i by d, over the rows it may not
// rest on; it stays at i when no row is that way.
func paletteStep(items []paletteItem, i, d int) int {
	for j := i + d; j >= 0 && j < len(items); j += d {
		if items[j].chosen() {
			return j
		}
	}
	return i
}

// paletteList is what the palette lists.
type paletteList int

const (
	listCommands paletteList = iota // the panes and the commands
	listSessions                    // the workspaces, to show one
	listNeeds                       // what needs the user
)

func (a *App) paletteItems() []paletteItem {
	var items []paletteItem
	q := strings.ToLower(strings.TrimSpace(a.paletteQuery))
	match := func(s string) bool { return q == "" || fuzzy(strings.ToLower(s), q) }
	switch a.paletteList {
	case listSessions:
		return a.sessionItems(match)
	case listNeeds:
		return a.needItems(match)
	}
	for ti, t := range a.tabs {
		for _, p := range t.allPanes() {
			name, detail := p.label()
			if !match(fmt.Sprintf("%s %s tab %d", name, detail, ti+1)) && !match(fmt.Sprintf("%s %s %s", name, detail, locales.PaletteTabKeys(ti+1))) {
				continue
			}
			s := p.status()
			if s != nil && s.State != "idle" {
				detail = cmp.Or(s.Message(), s.Title, detail)
			}
			prog := p.program()
			items = append(items, paletteItem{
				title: name, detail: detail, glyph: prog.Glyph, icon: prog.Icon,
				keys: locales.PaletteTabKeys(ti + 1), status: s,
				run: func() {
					a.selectTab(ti)
					a.revealPane(p)
					t.setFocus(p)
					a.focusReq = p
				},
			})
		}
	}
	// Without a query, the panes waiting for the user come first: blocked,
	// then failed, then done.
	if q == "" {
		slices.SortStableFunc(items, func(x, y paletteItem) int {
			rank := func(s *rex.ProgramStatus) int {
				return needsYou(false, s)
			}
			return rank(y.status) - rank(x.status)
		})
	}
	for _, cmd := range paletteCommands {
		if cmd.Hidden || cmd == &cmdUpdates && !mygo.Updater.Enabled() || cmd == &cmdSidebar && prefs.TabPlacement == "" || cmd == &cmdAmbience && !themeAmbient() ||
			a.st == nil && slices.Contains(sessionCommands, cmd) ||
			!match(cmd.Title()) && !match(cmd.English()) {
			continue
		}
		it := paletteItem{title: cmd.Title(), keys: cmd.Keys, glyph: "command", run: func() { a.invokeCommand(cmd, nil) }}
		if h := cmd.hostAway(a); h != nil {
			it.disabled, it.detail = true, locales.HostsNotConnectedTo(a.hostName(h))
		} else if cmd.Host != nil && cmd.Available != nil && !cmd.Available(a) {
			it.disabled = true
		}
		items = append(items, it)
		if cmd == &cmdNewTab {
			items = append(items, a.newTabOnItems(match)...)
		}
	}
	items = append(items, a.teleportItems(match)...)
	for _, cmd := range a.custom {
		if !match(cmd.Title()) && !match(cmd.English()) {
			continue
		}
		if slices.ContainsFunc(cmd.Args, func(a actionArg) bool { return !a.Optional }) {
			continue // the palette supplies no arguments
		}
		items = append(items, paletteItem{title: cmd.Title(), glyph: "command", run: func() { a.invokeCommand(cmd, nil) }})
	}
	return items
}

// newTabOnItems are the palette's New Tab on a host, for this machine and
// each host, when there are hosts.
func (a *App) newTabOnItems(match func(string) bool) []paletteItem {
	if len(a.hosts) == 0 {
		return nil
	}
	var items []paletteItem
	for _, h := range a.allHosts() {
		name := a.hostName(h)
		title := locales.CmdNewTabOn(name)
		if !match(title) && !match(locales.In("en").CmdNewTabOn(name)) {
			continue
		}
		it := paletteItem{title: title, glyph: "plus", run: func() { a.newTabOn(h, a.dirOn(h)) }}
		if h != nil && h.current() == nil {
			it.disabled, it.detail = true, locales.HostsNotConnectedTo(name)
		}
		items = append(items, it)
	}
	return items
}

// dirOn is the directory a new tab on host h opens in: the focused pane's
// when it is on h, else the home directory there.
func (a *App) dirOn(h *hostConn) string {
	if a.currentHost() == h {
		return a.currentDir()
	}
	if h != nil {
		return ""
	}
	home, _ := os.UserHomeDir()
	return home
}

// fuzzy reports whether the letters of q appear in s, in order.
func fuzzy(s, q string) bool {
	if strings.Contains(s, q) {
		return true
	}
	i, want := 0, []rune(q)
	for _, r := range s {
		if i < len(want) && r == want[i] {
			i++
		}
	}
	return i == len(want)
}

// Where the palette comes in from: its panel, its rows, the first
// paletteStagger one after another, and the dimmed window behind it.
var (
	paletteFrom    = ui.Pose{Y: -6, Scale: 0.96, Fade: 1}
	paletteRowFrom = ui.Pose{Y: -4, Fade: 1}
	paletteFade    = &ui.Motion{Opacity: 0}
)

const paletteStagger = 8

// paletteR and palettePad are the palette's radius token and its list's
// padding, which its rows' corners are concentric with; paletteDrop is
// how far below the title bar it opens.
const paletteR, palettePad = radiusXXL, 6
const paletteDrop = 78

// palette draws the command palette, over the window.
func (a *App) palette(c *ui.Context, k *colors) {
	if !a.paletteOpen {
		return
	}
	items := a.paletteItems()
	// reveal scrolls the selection into view in the frame the keys or the
	// query move it, and only then, so that the wheel scrolls the list.
	reveal := false
	// sel selects row i, and follows what it stands for.
	sel := func(i int) {
		a.paletteSel, a.paletteSelID = i, nil
		if i < len(items) {
			a.paletteSelID = items[i].id
		}
	}
	// The selection stays on its row as rows come and go before it, and
	// moves off a row it may not rest on, as the first row after the
	// query changed.
	if i := slices.IndexFunc(items, func(it paletteItem) bool { return it.id != nil && it.id == a.paletteSelID }); i >= 0 {
		a.paletteSel = i
	}
	sel(min(max(a.paletteSel, 0), max(len(items)-1, 0)))
	if len(items) > 0 && !items[a.paletteSel].chosen() {
		sel(paletteStep(items, a.paletteSel, 1))
		reveal = true
	}
	was := a.paletteOpen
	// Opened, the panel comes in from a little above and smaller, and the
	// first rows after it, one after another; the dimmed window behind
	// fades in, and all of it out as it closes. The rows come in on the
	// palette's opening only, not as the query changes them.
	anim := motion(c)
	fresh := a.paletteFresh
	a.paletteFresh = false
	if fresh && anim != nil {
		// The frame after this one starts them moving.
		c.AnimationFrame()
	}
	// Keyed, the palette is the same element whatever shows before it over
	// the window, as the error banner coming and going.
	ui.DialogBase(c.Key("palette"), &a.paletteOpen, func(backdrop, panel ui.Element) {
		// The backdrop dims the window; the glass is the panel's, which
		// blurs only what it covers.
		backdrop.Justify(ui.Start).Padding(paletteDrop, 0, 0, 0).Background(k.backdrop)
		// Under the glass setting the panel is Liquid Glass over the
		// dimmed window, as the other floating layers are.
		panel.Width(560).MaxHeight(440).Radius(k.radius(paletteR))
		frostPanel(panel, k, k.radius(paletteR))
		dialogShadow(panel, k).Clip()
		if anim != nil {
			dialogMotion(c, backdrop, panel, fresh)
		}
		ui.Row(c).Padding(12, 16).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(k.panelBorder).Children(func() {
			glyphIcon(c, "search", 17, k.textMuted)
			placeholder := locales.PalettePlaceholder()
			switch a.paletteList {
			case listSessions:
				placeholder = locales.SessionPlaceholder()
			case listNeeds:
				placeholder = locales.NeedsPlaceholder()
			}
			in := ui.TextInputBase(c, &a.paletteQuery).Grow(1).FontSize(fontTitle).Placeholder(placeholder).PlaceholderColor(k.textMuted).AutoFocus()
			if in.Changed() {
				a.paletteSel, a.paletteSelID, reveal = 0, nil, true
			}
			if in.Shortcut(0, ui.KeyDown) {
				sel(paletteStep(items, a.paletteSel, 1))
				reveal = true
			}
			if in.Shortcut(0, ui.KeyUp) {
				sel(paletteStep(items, a.paletteSel, -1))
				reveal = true
			}
			if in.Submitted() && len(items) > 0 && items[a.paletteSel].chosen() {
				a.paletteOpen = false
				items[a.paletteSel].run()
			}
		})
		// Nothing matched, or nothing needs the user, though the hosts not
		// connected are listed after it.
		empty := len(items) == 0 || a.paletteList == listNeeds && !slices.ContainsFunc(items, paletteItem.chosen)
		// Rows are 34 high, the sections' headers 28, and the line saying
		// there is none.
		listH := 0
		if empty {
			listH = 34
		}
		for _, it := range items {
			listH += 34
			if it.header {
				listH -= 6
			}
		}
		ui.Scroll(c).Height(float32(min(listH+12, 384))).Padding(palettePad).Children(func() {
			if empty {
				q := strings.TrimSpace(a.paletteQuery)
				text := locales.PaletteNoMatchesFor(q)
				switch {
				case a.paletteList == listNeeds && q == "":
					text = locales.NeedsNothing()
				case q == "":
					text = locales.PaletteNoMatches()
				}
				emptyMenu(c, k, "palette-empty", text)
			}
			for i, it := range items {
				if it.header {
					ui.Row(c.Key(i)).Height(28).Padding(8, 10, 0, 10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, it.title).FontSize(fontFootnote).FontWeight(600).TextColor(k.textMuted)
					})
					continue
				}
				row := ui.Row(c.Key(i)).Height(34).Padding(0, 10).Gap(10).Radius(concentric(k.radius(paletteR), 1+palettePad)).AlignItems(ui.Center).Cursor(ui.CursorPointer)
				if anim != nil {
					pose := ui.Pose{}
					if fresh {
						pose = paletteRowFrom
					}
					// The rows past the first paletteStagger come in with
					// the last of those, not before them.
					delay := time.Duration(min(i, paletteStagger-1)) * 15 * time.Millisecond
					row.AnimatePose(pose, ui.Animation{Spring: anim.Spring, Delay: delay})
				}
				if i == a.paletteSel {
					row.Background(k.panelSel)
					if reveal {
						row.ScrollIntoView()
					}
				} else if row.Hovered() {
					row.Background(k.hover)
				}
				if it.disabled {
					dimmed(row, 0.5)
				} else if row.Clicked() {
					a.paletteOpen = false
					it.run()
				}
				row.Children(func() {
					programIconEl(c, it.icon, it.glyph, 15, k.textMuted)
					if it.hasDot {
						ui.Box(c).Size(8, 8).Radius(k.capsule(8)).Background(it.dot).Shrink(0)
					}
					// The title takes its width, the detail what is left.
					ui.RichText(c, titleSpans(it.title, 13.5, 500, k.text)...).Label(it.title).SingleLine().Ellipsis("…").Shrink(0).MaxWidthPercent(70)
					if it.detail != "" {
						ui.RichText(c, titleSpans(it.detail, 13, 400, k.textMuted)...).Label(it.detail).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
					}
					ui.Spacer(c)
					aside := it.aside
					if s := it.status; a.paletteList == listNeeds && needsUser(s) && !s.Updated.IsZero() {
						when, next := ago(s.Updated, c.Now())
						c.After(next)
						if aside != "" {
							aside += " · "
						}
						aside += when
					}
					if aside != "" {
						ui.Text(c, aside).FontSize(fontSubhead).TextColor(k.textMuted).SingleLine().Shrink(0)
					}
					if s := it.status; s != nil && s.State != "idle" {
						statusDot(c, k, s, 6)
					}
					if it.keys != "" {
						ui.Text(c, it.keys).FontSize(fontSubhead).TextColor(k.textMuted).Padding(2, 8).Radius(k.radius(radiusS)).Background(k.panel).Border(k.borderW(), k.panelBorder).Shrink(0)
					}
				})
			}
		})
	})
	if was && !a.paletteOpen {
		if t := a.tab(); t != nil && a.focusReq == nil {
			a.focusReq = t.Focus
		}
	}
}

// splitsLocal tells whether the focused pane is on this machine, whose
// tab a pane with no session may join.
func splitsLocal(a *App) bool {
	t := a.tab()
	return t != nil && t.Focus != nil && t.Focus.host == nil
}
