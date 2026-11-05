package main

import (
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// The settings window: a sidebar of sections and the one chosen beside it,
// as Rex's.
var settingsUI struct {
	win     *mygo.Window
	section string
	// themeQuery narrows the list of themes; fonts are the monospaced
	// families of the font picker, listed once, and themes the themes read
	// so far.
	themeQuery string
	fonts      []string
	themes     map[string]*terminal.Theme
	// fontOptions are the font picker's options, the first being the
	// choice that sets nothing, whose label follows the language; langTags
	// and langNames are the language control's, "" and its label first.
	// All are made once the window opens.
	fontOptions, langTags, langNames []string
}

// Run is set here: the window's view lists the commands, which list this one.
func init() { cmdSettings.Run = func(a *App) { a.openSettings() } }

// openSettings opens the settings window, or shows it when open. It is a
// window of its own, which MyGo allows.
func (a *App) openSettings() {
	if w := settingsUI.win; w != nil {
		w.Show()
		w.Focus()
		return
	}
	settingsUI.section, settingsUI.themeQuery = "appearance", ""
	settingsUI.themes = map[string]*terminal.Theme{}
	settingsUI.fonts, settingsUI.fontOptions, settingsUI.langTags = nil, nil, nil
	for _, f := range ui.FontFamilies() {
		if f.Monospaced {
			settingsUI.fonts = append(settingsUI.fonts, f.Name)
		}
	}
	opts := mygo.WindowOptions{
		Title:     locales.SettingsTitle(),
		Width:     780,
		Height:    540,
		MinWidth:  620,
		MinHeight: 400,
		// No wider than the sidebar and the settings column need.
		MaxWidth:             settingsSidebarW + 2*settingsPad + settingsMaxW + 4,
		StateKey:             "settings",
		TitleBarStyle:        mygo.TitleBarHidden,
		TrafficLightPosition: &mygo.Point{X: 18, Y: 19},
		BackgroundColor:      "light-dark(#efe1e6, #231e27)",
		Vibrancy:             glassVibrancy(),
		Transparent:          glassTransparent(),
		Content:              ui.View(a.settingsView),
	}
	if ownControls {
		opts.HideWindowButtons = true
	}
	win := mygo.NewWindow(opts)
	settingsUI.win = win
	win.SetMenu(mygo.NewMenu(nil)) // not the terminal window's menus
	win.OnClosed(func() {
		stopRemotePoll()
		closeAddHost()
		closeHostLayout()
		hostsPairQR = nil
		shortcutsUI = shortcutsState{}
		if a.st != nil && a.st.recording {
			a.st.recording = false
			rebuildMenu(a.st)
		}
		settingsUI.win = nil
	})
}

// sections are the sidebar's items.
var sections = []struct {
	id, glyph string
	label     func(locales.Messages) string
}{
	{"appearance", "settings-2", locales.Messages.SettingsAppearance},
	{"terminal", "square-terminal", locales.Messages.SettingsTerminal},
	{"theme", "sparkles", locales.Messages.SettingsTheme},
	{"keyboard", "keyboard", locales.Messages.SettingsKeyboard},
	{"remote", "globe", locales.Messages.SettingsRemote},
	{"hosts", "server", locales.Messages.SettingsHosts},
	{"about", "hexagon", locales.Messages.SettingsAbout},
}

// The settings window's layout: the sidebar's width, the height of the
// header rows at the top of the sidebar and of the page, the room around
// a page and between its groups, and the width hints wrap at.
const (
	settingsSidebarW = 220
	settingsHeaderH  = 52
	settingsPad      = 28
	settingsGroupGap = 18
	settingsHintW    = 560
	settingsMaxW     = 720 // the settings column, at most
)

// settingsTheme is the widgets' theme in the settings window: the app's,
// with capsule controls, as macOS's settings have. It is the settings
// window's alone; the terminal window keeps its own.
func settingsTheme(base *ui.Theme, k *colors) *ui.Theme {
	t := widgetTheme(base, k)
	t.Radius = 14
	return t
}

func (a *App) settingsView(c *ui.Context) {
	a.runPosted() // the hosts' changes, without a terminal window
	k := colorsOf(c)
	c.SetTheme(settingsTheme(c.Theme(), k))
	setupMenus(c)
	rootBackground(c, k.bgBottom)
	wash := backgroundAlpha(c)
	ui.Row(c).Fill().AlignItems(ui.Stretch).Draw(func(p *ui.Painter, r ui.Rect) { paintBackground(p, r, k, wash) }).Children(func() {
		// The sidebar paints no face: the window's glass shows through it,
		// and the traffic lights sit at its top.
		ui.Column(c).Width(settingsSidebarW).Shrink(0).Children(func() {
			ui.Box(c).Height(settingsHeaderH).Shrink(0).DragWindow()
			ui.Column(c).Grow(1).MinHeight(0).Padding(0, 10, 10, 10).Gap(2).Children(func() {
				a.serverCard(c, k)
				ui.Box(c).Height(8).Shrink(0)
				for _, s := range sections {
					a.sidebarItem(c, k, s.id, s.glyph, say(s.label))
				}
			})
		})
		ui.Box(c).Width(1).Shrink(0).Background(k.tabSep.Alpha(0.6))
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			a.settingsTitleBar(c, k)
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(4, settingsPad, settingsPad, settingsPad).Children(func() {
				ui.Column(c).Gap(settingsGroupGap).MaxWidth(settingsMaxW).Children(func() {
					switch settingsUI.section {
					case "server":
						a.serverSettings(c, k)
					case "terminal":
						a.terminalSettings(c, k)
					case "theme":
						a.themeSettings(c, k)
					case "keyboard":
						a.keyboardSettings(c, k)
					case "remote":
						a.remoteSettings(c, k)
					case "hosts":
						a.hostsSettings(c, k)
					case "about":
						a.aboutSettings(c, k)
					default:
						a.appearanceSettings(c, k)
					}
				})
			})
		})
	})
}

// sectionTitle is the title of a settings section, as the page's header
// shows it.
func sectionTitle(id string) string {
	switch id {
	case "server":
		return locales.HostServer()
	case "remote":
		return locales.RemoteTitle()
	case "hosts":
		return locales.SettingsHosts()
	}
	for _, s := range sections {
		if s.id == id {
			return say(s.label)
		}
	}
	return locales.SettingsAppearance()
}

// settingsTitleBar is the page's header: the section's title, and the
// window's controls where the app draws them. It moves the window.
func (a *App) settingsTitleBar(c *ui.Context, k *colors) {
	bar := c.TitleBar()
	ui.Row(c).Height(settingsHeaderH).Shrink(0).Padding(0, max(bar.Right, 10), 0, settingsPad).Gap(14).AlignItems(ui.Center).DragWindow().Children(func() {
		ui.Text(c, sectionTitle(settingsUI.section)).FontSize(15).FontWeight(700).TextColor(k.text).Grow(1).SingleLine().Ellipsis("…")
		windowControls(c, k, settingsUI.win)
	})
}

// sidebarItemH is the height of a sidebar item.
const sidebarItemH = 32

// sidebarItem is a section's chip, as a tab's: the chosen one has the
// active tab's face, and its icon and label in the accent.
func (a *App) sidebarItem(c *ui.Context, k *colors, id, glyph, label string) {
	active := settingsUI.section == id
	e := ui.Row(c.Key(id)).Height(sidebarItemH).Padding(0, 10).Gap(9).AlignItems(ui.Center).Radius(pillRadius(sidebarItemH, 10)).
		Cursor(ui.CursorPointer).Role(ui.RoleTab).Selected(active).Label(label)
	if active {
		glassPill(e, k, sidebarItemH)
	} else if e.Hovered() {
		e.Background(hoverFace(k))
	}
	e.Transition(ui.ElementTransition{Colors: true, Duration: 160 * time.Millisecond})
	if e.Clicked() {
		a.showSection(id)
	}
	icon, text, weight := k.iconMuted, k.text, 500
	if active {
		icon, text, weight = k.accent, k.accent, 600
	}
	e.Children(func() {
		glyphIcon(c, glyph, 16, icon)
		ui.Text(c, label).FontSize(13).FontWeight(weight).TextColor(text).SingleLine()
	})
}

// serverState is the session server's state as the app knows it from its
// connection: its label and the color of its dot. It asks nothing.
func (a *App) serverState(k *colors) (label string, dot ui.Color, up bool) {
	switch {
	case a.client == nil:
		return locales.RemoteUnreachable(), k.danger, false
	case clientClosed(a.client):
		return locales.RemoteStarting(), k.attention, false // connecting again
	case a.hello.PID == 0:
		return locales.RemoteStarting(), k.attention, false
	}
	return locales.HostsConnected(), k.busy, true
}

// clientClosed tells that the connection c has closed.
func clientClosed(c *rex.Client) bool {
	select {
	case <-c.Closed():
		return true
	default:
		return false
	}
}

// serverCard is the session server's card at the top of the sidebar: its
// state, and a click shows more of it.
func (a *App) serverCard(c *ui.Context, k *colors) {
	label, dot, _ := a.serverState(k)
	active := settingsUI.section == "server"
	e := ui.Row(c.Key("server")).Gap(10).Padding(8, 10).Radius(14).AlignItems(ui.Center).Shrink(0).
		Cursor(ui.CursorPointer).Role(ui.RoleTab).Selected(active).Label(locales.HostServer()+", "+label).
		Background(k.card).Border(1, k.cardBorder)
	if active || e.Hovered() {
		e.Background(k.cardFocused)
	}
	if active {
		e.Border(1, k.accent.Alpha(0.55))
	}
	e.Transition(ui.ElementTransition{Colors: true, Duration: 160 * time.Millisecond})
	if e.Clicked() {
		a.showSection("server")
	}
	e.Children(func() {
		ui.Box(c).Size(34, 34).Radius(9).Background(k.hover).Center().Shrink(0).Children(func() {
			glyphIcon(c, "server", 19, k.text)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, locales.HostServer()).FontSize(13).FontWeight(600).TextColor(k.text).SingleLine().Ellipsis("…")
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(7, 7).Radius(4).Background(dot).Shrink(0)
				ui.Text(c, label).FontSize(11.5).TextColor(k.textFaint).SingleLine().Ellipsis("…").MinWidth(0)
			})
		})
	})
}

// serverSettings is the server's page: the machine and the session
// server, as the host's panel tells them.
func (a *App) serverSettings(c *ui.Context, k *colors) {
	name, _, glyph := a.hostIdentity()
	label, dot, up := a.serverState(k)
	h := a.hello.Host
	sub := thisMachine()
	if up {
		sub = locales.HostConnected(sub)
	}
	a.serverGroup(c, k, name, glyph, sub, label, dot, h)
}

// serverGroup is the server page's card: the machine, its state, and the
// server's and the machine's facts, and the host's note under it.
func (a *App) serverGroup(c *ui.Context, k *colors, name, glyph, sub, label string, dot ui.Color, h rex.HostInfo) {
	settingsGroup(c, k, "", "", locales.HostNote(), func() {
		groupRow(c, k, nil).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(44, 44).Radius(12).Background(k.hover).Center().Shrink(0).Children(func() {
					glyphIcon(c, glyph, 26, k.text)
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, name).FontSize(15).FontWeight(700).TextColor(k.text).SingleLine().Ellipsis("…")
					ui.Text(c, sub).FontSize(12).TextColor(k.textFaint).SingleLine().Ellipsis("…")
				})
				ui.Row(c).Gap(6).Padding(4, 10).Radius(12).AlignItems(ui.Center).Shrink(0).Background(dot.Alpha(0.14)).Children(func() {
					ui.Box(c).Size(7, 7).Radius(4).Background(dot)
					ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(k.text).SingleLine()
				})
			})
		})
		row := func(label, value string) {
			if value == "" {
				return
			}
			groupRow(c, k, label).Children(func() {
				ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
					ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(k.text).Shrink(0)
					ui.Text(c, value).FontSize(13).TextColor(k.textFaint).Grow(1).MinWidth(0).TextAlign(ui.End).SingleLine().Ellipsis("…").Selectable()
				})
			})
		}
		if a.hello.PID > 0 {
			row(locales.HostServer(), locales.HostServerUp(a.hello.PID, roundDur(time.Since(a.hello.Started))))
		}
		sessions, running := a.sessionCounts()
		row(locales.HostSessions(), locales.HostSessionCount(sessions, running))
		hostFacts(h, row)
	})
}

// groupRows counts the rows of the group being built: each after the
// first has a hairline above it.
var groupRows int

// settingsGroup is a group of rows on one card, with an optional label
// and note above it, and an optional footnote under it.
func settingsGroup(c *ui.Context, k *colors, label, note, foot string, body func()) {
	ui.Column(c).Gap(6).Children(func() {
		if label != "" || note != "" {
			ui.Column(c).Gap(2).Padding(0, 14).Children(func() {
				if label != "" {
					ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(k.text)
				}
				if note != "" {
					rowHint(c, k, note)
				}
			})
		}
		saved := groupRows
		groupRows = 0
		ui.Column(c).Gap(2).Children(func() {
			ui.Column(c).Radius(14).Background(k.card).Border(1, k.cardBorder).Shadow(0, 1, 2, 0, k.shadow).Children(body)
			if foot != "" {
				ui.Text(c, foot).FontSize(11.5).TextColor(k.textFaint).LineHeight(1.4).MaxWidth(settingsHintW).Padding(0, 14)
			}
		})
		groupRows = saved
	})
}

// groupRow is a row of a group, keyed by key unless nil: a hairline
// above it but the first, and its content laid out by the caller.
func groupRow(c *ui.Context, k *colors, key any) ui.Element {
	if groupRows > 0 {
		ui.Box(c).Height(0.5).Margin(0, 0, 0, 14).Shrink(0).Background(k.tabSep)
	}
	groupRows++
	if key != nil {
		c = c.Key(key)
	}
	return ui.Column(c).MinHeight(44).Padding(11, 14).Gap(4).Justify(ui.Center)
}

// rowHint is the hint under a row's first line, across the row; nothing
// when hint is "".
func rowHint(c *ui.Context, k *colors, hint string) {
	if hint != "" {
		ui.Text(c, hint).FontSize(11.5).TextColor(k.textFaint).LineHeight(1.4).MaxWidth(settingsHintW)
	}
}

// rowLine is a row's first line: its title at the left and its controls
// at the right, centered on one line.
func rowLine(c *ui.Context, k *colors, label string, control func()) {
	ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(k.text).Grow(1).MinWidth(0)
		if control != nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(control)
		}
	})
}

// settingRow lays out a setting in its group: its label and control on
// one line, and its hint under them.
func settingRow(c *ui.Context, k *colors, label, hint string, control func()) {
	groupRow(c, k, nil).Children(func() {
		rowLine(c, k, label, control)
		rowHint(c, k, hint)
	})
}

// appearances are the values of the appearance control, in order.
var appearances = []string{"", "light", "dark"}

func (a *App) appearanceSettings(c *ui.Context, k *colors) {
	settingsGroup(c, k, "", "", "", func() { a.appearanceWindow(c, k) })
	settingsGroup(c, k, "", "", "", func() { a.appearanceText(c, k) })
	settingsGroup(c, k, "", "", "", func() { a.appearanceGlass(c, k) })
}

// appearanceWindow is the Appearance page's first group: the window, the
// tabs, the pane headers and the animations.
func (a *App) appearanceWindow(c *ui.Context, k *colors) {
	i := max(slices.Index(appearances, prefs.Appearance), 0)
	settingRow(c, k, locales.SettingsWindow(), locales.SettingsWindowHint(), func() {
		if ui.Segmented(c, &i, locales.AppearanceSystem(), locales.AppearanceLight(), locales.AppearanceDark()).Label(locales.MenuAppearance()).Changed() {
			a.setAppearance(appearances[i])
		}
	})
	place := max(slices.Index(placements, prefs.TabPlacement), 0)
	settingRow(c, k, locales.SettingsTabPlacement(), locales.SettingsTabPlacementHint(), func() {
		if ui.Segmented(c, &place, locales.SettingsPlacementAbove(), locales.SettingsPlacementLeft(), locales.SettingsPlacementRight()).Label(locales.SettingsTabPlacement()).Changed() {
			a.setWindowPref(func(s *settings) { setTabPlacement(s, placements[place]) })
		}
	})
	headers := !prefs.HidePaneHeaders
	settingRow(c, k, locales.SettingsPaneHeaders(), locales.SettingsPaneHeadersHint(), func() {
		if ui.Switch(c, &headers).Label(locales.SettingsPaneHeaders()).Changed() {
			a.setWindowPref(func(s *settings) { s.HidePaneHeaders = !headers })
		}
	})
	anim := max(slices.Index(animations, prefs.Animation), 0)
	settingRow(c, k, locales.SettingsAnimation(), locales.SettingsAnimationHint(), func() {
		if ui.Segmented(c, &anim, locales.AnimationSnappy(), locales.AnimationNatural(), locales.AnimationOff()).Label(locales.SettingsAnimation()).Changed() {
			a.setWindowPref(func(s *settings) { s.Animation = animations[anim] })
		}
	})
}

// appearanceText is the Appearance page's group of the text: its size and
// the language.
func (a *App) appearanceText(c *ui.Context, k *colors) {
	size := float64(termFont.Size)
	hint := locales.SettingsTextSizeHint(float64(ghosttyRaw.fontSize(0)), float64(defaultFontSize))
	settingRow(c, k, locales.SettingsTextSize(), hint, func() {
		ui.Text(c, locales.SettingsTextSizeValue(size)).FontSize(13).TextColor(k.textMuted)
		if ui.Stepper(c, &size, 8, 32, 1).Label(locales.SettingsTextSize()).Changed() {
			a.setFontSize(float32(size))
		}
		if ui.Button(c, locales.SettingsReset()).Disabled(prefs.FontSize == 0).Clicked() {
			a.setFontSize(0)
		}
	})
	if settingsUI.langTags == nil {
		settingsUI.langTags = append([]string{""}, mygo.App.Languages()...)
		settingsUI.langNames = []string{""}
		for _, tag := range settingsUI.langTags[1:] {
			settingsUI.langNames = append(settingsUI.langNames, i18n.Autonym(tag))
		}
	}
	tags, names := settingsUI.langTags, settingsUI.langNames
	names[0] = locales.SettingsLanguageSystem()
	lang := names[max(slices.Index(tags, prefs.Language), 0)]
	settingRow(c, k, locales.SettingsLanguage(), locales.SettingsLanguageHint(), func() {
		if ui.Select(c, &lang, names).Label(locales.SettingsLanguage()).Width(160).Changed() {
			a.setLanguage(tags[slices.Index(names, lang)])
		}
	})
}

// appearanceGlass is the Appearance page's group of the glass, the
// terminals' opacity over it and the Dock badge.
func (a *App) appearanceGlass(c *ui.Context, k *colors) {
	glassy := glassOn()
	settingRow(c, k, locales.SettingsGlass(), locales.SettingsGlassHint(), func() {
		if ui.Switch(c, &glassy).Label(locales.SettingsGlass()).Changed() {
			a.setGlass(glassy)
		}
	})
	opacity := float64(termOpacity())
	settingRow(c, k, locales.SettingsTermOpacity(), locales.SettingsTermOpacityHint(), func() {
		ui.Text(c, locales.SettingsTermOpacityValue(opacity)).FontSize(13).TextColor(k.textMuted)
		if ui.StepSlider(c, &opacity, minTermOpacity, maxTermOpacity, 0.1).Label(locales.SettingsTermOpacity()).Width(160).Disabled(!glassy).Changed() {
			a.setWindowPref(func(s *settings) { s.TermOpacity = float32(opacity) })
		}
	})
	badge := !prefs.HideDockBadge
	if badgeSupported {
		settingRow(c, k, locales.SettingsDockBadge(), locales.SettingsDockBadgeHint(), func() {
			if ui.Switch(c, &badge).Label(locales.SettingsDockBadge()).Changed() {
				a.setWindowPref(func(s *settings) { s.HideDockBadge = !badge })
			}
		})
	}
}

func (a *App) terminalSettings(c *ui.Context, k *colors) {
	family := prefs.Font
	if settingsUI.fontOptions == nil {
		settingsUI.fontOptions = append([]string{""}, settingsUI.fonts...)
	}
	// The first option sets nothing: it is told by its place, not its label.
	fonts := settingsUI.fontOptions
	fonts[0] = locales.SettingsFollowGhostty()
	if family == "" {
		family = fonts[0]
	} else if !slices.Contains(fonts, family) {
		fonts = append(slices.Clone(fonts), family)
	}
	settingsGroup(c, k, "", "", "", func() {
		settingRow(c, k, locales.SettingsFont(), locales.SettingsFontHint(), func() {
			if ui.Select(c, &family, fonts).Label(locales.SettingsFont()).Width(220).Changed() {
				a.setPref(func(s *settings) {
					s.Font = family
					if slices.Index(fonts, family) == 0 {
						s.Font = ""
					}
				})
			}
		})
		groupRow(c, k, nil).Children(func() { a.fontPreview(c, k) })
	})
	settingsGroup(c, k, "", "", "", func() { a.cursorSettings(c, k) })
	settingsGroup(c, k, "", "", "", func() {
		groupRow(c, k, nil).Children(func() {
			rowLine(c, k, locales.SettingsUseGhosttyHint(), func() {
				if ui.Button(c, locales.SettingsUseGhostty()).Clicked() {
					a.setCursor(func(s *settings) { s.Font, s.Cursor, s.CursorBlink, s.CopyOnSelect = "", "", nil, nil })
				}
			})
		})
	})
}

// cursorSettings is the Terminal page's group of the cursor and the
// selection.
func (a *App) cursorSettings(c *ui.Context, k *colors) {
	style := int(ghostty.Cursor)
	settingRow(c, k, locales.SettingsCursorStyle(), "", func() {
		if ui.Segmented(c, &style, locales.SettingsCursorBlock(), locales.SettingsCursorBar(), locales.SettingsCursorUnderline()).Label(locales.SettingsCursorStyle()).Changed() {
			a.setCursor(func(s *settings) { s.Cursor = cursorNames[style] })
		}
	})
	blink := !ghostty.NoBlink
	settingRow(c, k, locales.SettingsCursorBlink(), "", func() {
		if ui.Switch(c, &blink).Label(locales.SettingsCursorBlink()).Changed() {
			a.setCursor(func(s *settings) { s.CursorBlink = &blink })
		}
	})
	copyOn := ghostty.CopyOnSelect == terminal.CopyOnSelectClipboard
	settingRow(c, k, locales.SettingsCopyOnSelect(), locales.SettingsCopyOnSelectHint(), func() {
		if ui.Switch(c, &copyOn).Label(locales.SettingsCopyOnSelect()).Changed() {
			a.setPref(func(s *settings) { s.CopyOnSelect = &copyOn })
		}
	})
}

// cursorNames are the cursor styles' names, in the order of terminal's.
var cursorNames = []string{"block", "bar", "underline"}

// fontPreview shows text in the terminal's font, on the theme's colors.
func (a *App) fontPreview(c *ui.Context, k *colors) {
	th := termLight
	if c.Theme().Dark {
		th = termDark
	}
	family := "JetBrains Mono"
	if len(ghostty.Families) > 0 {
		family = ghostty.Families[0]
	}
	ui.Column(c).Padding(12, 14).Radius(10).Gap(2).Background(th.Background).Border(1, k.cardBorder).Children(func() {
		for _, line := range []string{"The quick brown fox jumps over the lazy dog", "0123456789 {}[]() => != <= ~/gorex $"} {
			ui.Text(c, line).Font(family).FontSize(termFont.Size).TextColor(th.Foreground)
		}
	})
}

// themeNames are the names that hold the query, in order.
func themeNames(all []string, query string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []string
	for _, n := range all {
		if q == "" || strings.Contains(strings.ToLower(n), q) {
			out = append(out, n)
		}
	}
	return out
}

// maxThemes is how many themes the picker lists at once.
const maxThemes = 60

// themeColumns is how many themes a row of the picker shows.
const themeColumns = 3

func (a *App) themeSettings(c *ui.Context, k *colors) {
	ui.TextInput(c, &settingsUI.themeQuery).Placeholder(locales.SettingsSearchThemes()).Label(locales.SettingsSearchThemes())
	// pick is a theme's card: a swatch of its colors, its name, and the
	// ring of the chosen one.
	pick := func(id, name, sub string, th *terminal.Theme, chosen bool, set string) {
		card := ui.Column(c.Key(id)).Grow(1).Basis(0).MinWidth(0).Gap(6).Padding(5, 5, 8, 5).Radius(14).
			Cursor(ui.CursorPointer).Role(ui.RoleButton).Selected(chosen).Label(name).
			Background(k.card).Border(2, ui.Transparent)
		if chosen {
			card.Border(2, k.accent)
		} else if card.Hovered() {
			card.Background(k.cardFocused)
		}
		if card.Clicked() {
			a.setPref(func(s *settings) { s.Theme = set })
		}
		card.Children(func() {
			ui.Column(c).Height(76).Padding(10, 12).Gap(8).Radius(concentric(14, 2+5)).Background(th.Background).Border(1, k.cardBorder).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Aa").Font(monoFamily()).FontSize(15).FontWeight(700).TextColor(th.Foreground).Grow(1)
					if chosen {
						glyphIcon(c, "check", 15, k.accent)
					}
				})
				ui.Row(c).Gap(5).Children(func() {
					for _, i := range []int{1, 2, 4, 5} {
						ui.Box(c).Size(12, 12).Radius(6).Background(th.Palette[i])
					}
				})
			})
			ui.Column(c).Padding(0, 4).Gap(1).Children(func() {
				ui.Text(c, name).FontSize(12.5).FontWeight(600).TextColor(k.text).SingleLine().Ellipsis("…")
				if sub != "" {
					ui.Text(c, sub).FontSize(11).TextColor(k.textFaint).SingleLine().Ellipsis("…")
				}
			})
		})
	}
	themeOf := func(name string) *terminal.Theme {
		th, ok := settingsUI.themes[name]
		if !ok {
			th, _ = terminal.GhosttyTheme(name)
			settingsUI.themes[name] = th
		}
		if th == nil {
			return termLight
		}
		return th
	}
	sub := locales.SettingsGorexColors()
	if ghosttyRaw.Theme != "" {
		sub = ghosttyRaw.Theme
	}
	names := themeNames(terminal.GhosttyThemes(), settingsUI.themeQuery)
	shown := append([]string{""}, names[:min(len(names), maxThemes)]...)
	ui.Column(c).Gap(10).Children(func() {
		for row := 0; row < len(shown); row += themeColumns {
			ui.Row(c.Key(row)).Gap(10).AlignItems(ui.Stretch).Children(func() {
				for i := row; i < row+themeColumns; i++ {
					switch {
					case i >= len(shown):
						ui.Box(c).Grow(1).Basis(0).MinWidth(0) // the row's empty places
					case i == 0:
						pick("follow", locales.SettingsFollowGhostty(), sub, termLight, prefs.Theme == "", "")
					default:
						n := shown[i]
						pick(n, n, "", themeOf(n), prefs.Theme == n, n)
					}
				}
			})
		}
	})
	if len(names) > maxThemes {
		ui.Text(c, locales.SettingsMoreThemes(len(names)-maxThemes)).FontSize(11.5).TextColor(k.textFaint).Padding(0, 14)
	}
}

// shortcutRow is a shortcut of GoRex's, for the keyboard section.
type shortcutRow struct {
	Title, Keys string
	Command     *command
	Overridden  bool
}

// shortcutRows lists GoRex's shortcuts from the commands, with the keys of
// this system, and the tab keys the commands lack.
func shortcutRows() []shortcutRow {
	overrides := map[string]string{}
	if b, err := loadShortcuts(); err == nil {
		for _, o := range b {
			overrides[o.Action] = o.Key
		}
	}
	var rows []shortcutRow
	for _, cmd := range commands {
		if cmd.Keys != "" {
			keys, over := cmd.Keys, false
			if k, ok := overrides[cmd.ID]; ok {
				keys, over = k, true
			}
			rows = append(rows, shortcutRow{cmd.Title(), keys, cmd, over})
		}
	}
	first, last, cycle := showTabKey(1).String(), showTabKey(9).String(), "Ctrl+Tab, Ctrl+Shift+Tab"
	if runtime.GOOS == "darwin" {
		first, last, cycle = "⌘1", "⌘9", "⌃Tab, ⇧⌃Tab"
	}
	return append(rows,
		shortcutRow{Title: locales.SettingsShowTabRange(), Keys: first + " … " + last},
		shortcutRow{Title: locales.SettingsShowSpaceRange(), Keys: showSpaceKeys(1) + " … " + showSpaceKeys(9)},
		shortcutRow{Title: locales.SettingsNextPrevTab(), Keys: cycle})
}

func (a *App) keyboardSettings(c *ui.Context, k *colors) {
	a.recordShortcut(c)
	ui.Row(c).Gap(16).AlignItems(ui.Center).Padding(0, 0, 0, 14).Children(func() {
		ui.Text(c, locales.SettingsKeyboardNote()).FontSize(11.5).TextColor(k.textFaint).LineHeight(1.4).Grow(1).MinWidth(0)
		resetAllButton(a, c)
	})
	if shortcutsUI.recording != nil {
		settingsGroup(c, k, "", "", "", func() {
			groupRow(c, k, nil).Children(func() {
				rowLine(c, k, "Press the new shortcut for "+shortcutsUI.recording.Title(), func() {
					if ui.Button(c, "Cancel").Clicked() {
						a.stopRecording()
					}
				})
				if shortcutsUI.err != "" {
					ui.Text(c, shortcutsUI.err).FontSize(11.5).TextColor(k.danger).LineHeight(1.4)
				}
			})
		})
	}
	settingsGroup(c, k, "", "", "", func() {
		for i, r := range shortcutRows() {
			groupRow(c, k, i).MinHeight(40).Padding(7, 14).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, r.Title).FontSize(13).FontWeight(500).TextColor(k.text).Grow(1).MinWidth(0)
					ui.Text(c, r.Keys).FontSize(12).TextColor(k.textMuted).Padding(2, 9).Radius(10).Background(k.hover).Shrink(0)
					if r.Command != nil {
						if !blockedFromClient(r.Command.ID) && ui.Button(c.Key("change"), "Change").Clicked() {
							a.startRecording(r.Command)
						}
						reset := ui.Button(c.Key("reset"), locales.SettingsReset()).Disabled(!r.Overridden)
						if reset.Clicked() {
							a.resetShortcut(r.Command)
						}
					}
				})
			})
		}
	})
}

func (a *App) aboutSettings(c *ui.Context, k *colors) {
	version := mygo.App.Version()
	if version == "" {
		version = locales.SettingsDevBuild()
	}
	settingsGroup(c, k, "", "", "", func() {
		settingRow(c, k, "GoRex", locales.SettingsVersion(version), func() {
			if mygo.Updater.Enabled() {
				if ui.Button(c, locales.CmdCheckForUpdates()).Clicked() {
					a.invokeCommand(&cmdUpdates, nil)
				}
			} else {
				ui.Text(c, locales.SettingsUpdatesOff()).FontSize(11.5).TextColor(k.textFaint)
			}
		})
		settingRow(c, k, locales.SettingsFile(), settingsPath(), nil)
	})
}
