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
	settingsUI.fonts = nil
	for _, f := range ui.FontFamilies() {
		if f.Monospaced {
			settingsUI.fonts = append(settingsUI.fonts, f.Name)
		}
	}
	opts := mygo.WindowOptions{
		Title:                locales.SettingsTitle(),
		Width:                780,
		Height:               540,
		MinWidth:             620,
		MinHeight:            400,
		StateKey:             "settings",
		TitleBarStyle:        mygo.TitleBarHidden,
		TrafficLightPosition: &mygo.Point{X: 16, Y: 15},
		BackgroundColor:      "light-dark(#efe1e6, #231e27)",
		Content:              ui.View(a.settingsView),
	}
	if ownControls {
		opts.HideWindowButtons = true
	}
	win := mygo.NewWindow(opts)
	settingsUI.win = win
	win.SetMenu(mygo.NewMenu(nil)) // not the terminal window's menus
	win.OnClosed(func() { stopRemotePoll(); settingsUI.win = nil })
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
	{"about", "hexagon", locales.Messages.SettingsAbout},
}

// card gives e the look of a pane's card.
func card(e ui.Element, k *colors) ui.Element {
	return e.Radius(cardR).Background(k.card).Border(1, k.cardBorder).
		Shadow(0, 1, 2, 0, k.shadow).Shadow(0, 6, 22, -2, k.shadow)
}

func (a *App) settingsView(c *ui.Context) {
	k := colorsOf(c)
	c.SetTheme(widgetTheme(c.Theme(), k))
	c.Root().Background(k.bgBottom)
	ui.Column(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { paintBackground(p, r, k) }).Children(func() {
		a.settingsTitleBar(c, k)
		ui.Row(c).Grow(1).MinHeight(0).Gap(gap).Padding(0, gap, gap, gap).AlignItems(ui.Stretch).Children(func() {
			card(ui.Column(c).Width(196).Shrink(0).Padding(8).Gap(2), k).Children(func() {
				for _, s := range sections {
					a.sidebarItem(c, k, s.id, s.glyph, say(s.label))
				}
			})
			ui.Scroll(c).Grow(1).MinWidth(0).Padding(14, 18, 18, 18).Children(func() {
				ui.Column(c).Gap(gap + 2).MaxWidth(560).Children(func() {
					switch settingsUI.section {
					case "terminal":
						a.terminalSettings(c, k)
					case "theme":
						a.themeSettings(c, k)
					case "keyboard":
						a.keyboardSettings(c, k)
					case "remote":
						a.remoteSettings(c, k)
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

// settingsTitleBar is the main window's title bar, with the title.
func (a *App) settingsTitleBar(c *ui.Context, k *colors) {
	bar := c.TitleBar()
	left := bar.Left
	if left == 0 {
		left = 14
	} else {
		left += 14
	}
	ui.Row(c).Height(titleH).Padding(0, max(bar.Right, 10), 0, left).Gap(14).AlignItems(ui.Center).DragWindow().Children(func() {
		ui.Text(c, locales.SettingsTitle()).FontSize(13.5).FontWeight(700).TextColor(k.text.Alpha(0.85)).Grow(1).SingleLine()
		windowControls(c, k, settingsUI.win)
	})
}

// sidebarItem is a section's chip, as a tab's: the chosen one has the
// active tab's face.
func (a *App) sidebarItem(c *ui.Context, k *colors, id, glyph, label string) {
	active := settingsUI.section == id
	e := ui.Row(c.Key(id)).Height(tabH+4).Padding(0, 12, 0, 10).Gap(9).AlignItems(ui.Center).Radius((tabH+4)/2).
		Cursor(ui.CursorPointer).Role(ui.RoleTab).Selected(active).Label(label)
	if active {
		e.Background(k.tabActive).Shadow(0, 1, 2, 0, k.shadow).Shadow(0, 2, 8, 0, k.shadow)
	} else if e.Hovered() {
		e.Background(k.hover)
	}
	e.Transition(ui.ElementTransition{Colors: true, Duration: 160 * time.Millisecond})
	if e.Clicked() {
		a.showSection(id)
	}
	e.Children(func() {
		ui.Icon(c, icon(glyph)).Size(16, 16).TextColor(map[bool]ui.Color{true: k.text, false: k.iconMuted}[active])
		ui.Text(c, label).FontSize(13.5).FontWeight(500).TextColor(k.text).SingleLine()
	})
}

func settingsTitle(c *ui.Context, k *colors, title string) {
	ui.Text(c, title).FontSize(17).FontWeight(700).TextColor(k.text).Padding(0, 0, 2, 4)
}

// settingRow lays out a setting: its label and hint, and its control.
func settingRow(c *ui.Context, k *colors, label, hint string, control func()) {
	card(ui.Row(c).Gap(16).AlignItems(ui.Center).Padding(11, 14), k).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, label).FontSize(13.5).FontWeight(600).TextColor(k.text)
			if hint != "" {
				ui.Text(c, hint).FontSize(12).TextColor(k.textFaint).LineHeight(1.3)
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(control)
	})
}

// appearances are the values of the appearance control, in order.
var appearances = []string{"", "light", "dark"}

func (a *App) appearanceSettings(c *ui.Context, k *colors) {
	settingsTitle(c, k, locales.SettingsAppearance())
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
	tags := append([]string{""}, mygo.App.Languages()...)
	names := []string{locales.SettingsLanguageSystem()}
	for _, tag := range tags[1:] {
		names = append(names, i18n.Autonym(tag))
	}
	lang := names[max(slices.Index(tags, prefs.Language), 0)]
	settingRow(c, k, locales.SettingsLanguage(), locales.SettingsLanguageHint(), func() {
		if ui.Select(c, &lang, names).Label(locales.SettingsLanguage()).Width(160).Changed() {
			a.setLanguage(tags[slices.Index(names, lang)])
		}
	})
}

func (a *App) terminalSettings(c *ui.Context, k *colors) {
	settingsTitle(c, k, locales.SettingsTerminal())
	family := prefs.Font
	follow := locales.SettingsFollowGhostty()
	fonts := append([]string{follow}, settingsUI.fonts...)
	if family == "" {
		family = follow
	} else if !slices.Contains(fonts, family) {
		fonts = append(fonts, family)
	}
	settingRow(c, k, locales.SettingsFont(), locales.SettingsFontHint(), func() {
		if ui.Select(c, &family, fonts).Label(locales.SettingsFont()).Width(220).Changed() {
			a.setPref(func(s *settings) {
				s.Font = family
				if family == follow {
					s.Font = ""
				}
			})
		}
	})
	a.fontPreview(c, k)

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
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, locales.SettingsUseGhostty()).Clicked() {
			a.setCursor(func(s *settings) { s.Font, s.Cursor, s.CursorBlink, s.CopyOnSelect = "", "", nil, nil })
		}
		ui.Text(c, locales.SettingsUseGhosttyHint()).FontSize(12).TextColor(k.textFaint)
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
	ui.Column(c).Padding(12, 14).Radius(cardR).Gap(2).Background(th.Background).Border(1, k.cardBorder).Shadow(0, 1, 2, 0, k.shadow).Children(func() {
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

func (a *App) themeSettings(c *ui.Context, k *colors) {
	settingsTitle(c, k, locales.SettingsTheme())
	ui.TextInput(c, &settingsUI.themeQuery).Placeholder(locales.SettingsSearchThemes()).Label(locales.SettingsSearchThemes())
	pick := func(id, name, sub string, th *terminal.Theme, chosen bool, set string) {
		row := ui.Row(c.Key(id)).Gap(10).Padding(6, 10).Radius(10).AlignItems(ui.Center).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(name)
		if chosen {
			row.Background(k.tabActive).Shadow(0, 1, 2, 0, k.shadow).Shadow(0, 2, 8, 0, k.shadow)
		} else if row.Hovered() {
			row.Background(k.hover)
		}
		if row.Clicked() {
			a.setPref(func(s *settings) { s.Theme = set })
		}
		row.Children(func() {
			ui.Text(c, "Aa").FontSize(12).FontWeight(700).Width(34).Padding(3, 0).Radius(6).Center().
				Background(th.Background).TextColor(th.Foreground)
			ui.Text(c, name).FontSize(13.5).TextColor(k.text).Grow(1).SingleLine().Ellipsis("…")
			if sub != "" {
				ui.Text(c, sub).FontSize(12).TextColor(k.textFaint).SingleLine()
			}
			if chosen {
				ui.Icon(c, icon("check")).Size(15, 15).TextColor(k.accent)
			}
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
	card(ui.Column(c).Padding(6).Gap(2), k).Children(func() {
		pick("follow", locales.SettingsFollowGhostty(), sub, termLight, prefs.Theme == "", "")
		for _, n := range names[:min(len(names), maxThemes)] {
			pick(n, n, "", themeOf(n), prefs.Theme == n, n)
		}
	})
	if len(names) > maxThemes {
		ui.Text(c, locales.SettingsMoreThemes(len(names)-maxThemes)).FontSize(12).TextColor(k.textFaint)
	}
}

// shortcutRow is a shortcut of GoRex's, for the keyboard section.
type shortcutRow struct{ Title, Keys string }

// shortcutRows lists GoRex's shortcuts from the commands, with the keys of
// this system, and the tab keys the commands lack.
func shortcutRows() []shortcutRow {
	var rows []shortcutRow
	for _, cmd := range commands {
		if cmd.Keys != "" {
			rows = append(rows, shortcutRow{cmd.Title(), cmd.Keys})
		}
	}
	first, last, cycle := showTabKey(1).String(), showTabKey(9).String(), "Ctrl+Tab, Ctrl+Shift+Tab"
	if runtime.GOOS == "darwin" {
		first, last, cycle = "⌘1", "⌘9", "⌃Tab, ⇧⌃Tab"
	}
	return append(rows, shortcutRow{locales.SettingsShowTabRange(), first + " … " + last}, shortcutRow{locales.SettingsNextPrevTab(), cycle})
}

func (a *App) keyboardSettings(c *ui.Context, k *colors) {
	settingsTitle(c, k, locales.SettingsKeyboard())
	ui.Text(c, locales.SettingsKeyboardNote()).FontSize(12).TextColor(k.textFaint)
	card(ui.Column(c).Padding(6), k).Children(func() {
		for i, r := range shortcutRows() {
			ui.Row(c.Key(i)).Gap(12).Padding(7, 10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, r.Title).FontSize(13.5).TextColor(k.text).Grow(1)
				ui.Text(c, r.Keys).FontSize(12).TextColor(k.textMuted).Padding(2, 7).Radius(7).Background(k.hover)
			})
		}
	})
}

func (a *App) aboutSettings(c *ui.Context, k *colors) {
	settingsTitle(c, k, locales.SettingsAbout())
	version := mygo.App.Version()
	if version == "" {
		version = locales.SettingsDevBuild()
	}
	settingRow(c, k, "GoRex", locales.SettingsVersion(version), func() {
		if mygo.Updater.Enabled() {
			if ui.Button(c, locales.CmdCheckForUpdates()).Clicked() {
				cmdUpdates.Run(a)
			}
		} else {
			ui.Text(c, locales.SettingsUpdatesOff()).FontSize(12).TextColor(k.textFaint)
		}
	})
	settingRow(c, k, locales.SettingsFile(), settingsPath(), func() {})
}
