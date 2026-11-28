package main

import (
	"errors"
	"log"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorex/internal/atomicfile"
	"gorex/internal/duotone"
	"gorex/internal/rex"
	"gorex/internal/rexapi"
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
	// apiInfo is the Rex API gateway's version and listeners, for the
	// server page's Local API group, fetched while that page shows:
	// apiLoaded when it has been asked once, apiErr when no gateway
	// answered.
	apiStop   chan struct{}
	apiInfo   rexapi.GatewayInfo
	apiErr    string
	apiLoaded bool
	// json is the settings.json page's editor, made on its first draw.
	json *jsonEditorState
	// toast is the window's toast (toast.go), as the failure of a reset
	// of the shortcuts shows.
	toast toastSlot
}

// Run is set here: the window's view lists the commands, which list this one.
func init() { cmdSettings.Run = func(a *App) { a.openSettings() } }

// startServerInfoPoll asks the Rex API gateway every ten seconds while the
// server page shows. Each ask is a control connection that every Rex
// client hears of, and the build cannot change while the gateway runs:
// only whether it runs, and what it listens on, is worth asking about.
func startServerInfoPoll() {
	win := settingsUI.win
	if settingsUI.apiStop != nil || win == nil {
		return
	}
	stop := make(chan struct{})
	settingsUI.apiStop = stop
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			info, err := rexapi.GatewayStatus()
			win.Update(func() {
				if settingsUI.apiStop != stop {
					return
				}
				settingsUI.apiInfo, settingsUI.apiLoaded = info, true
				settingsUI.apiErr = ""
				if err != nil {
					settingsUI.apiErr = err.Error()
				}
			})
			select {
			case <-stop:
				return
			case <-t.C:
			}
		}
	}()
}

// stopServerInfoPoll stops asking the gateway, dropping what it said.
func stopServerInfoPoll() {
	if settingsUI.apiStop != nil {
		close(settingsUI.apiStop)
	}
	settingsUI.apiStop = nil
	settingsUI.apiInfo, settingsUI.apiErr, settingsUI.apiLoaded = rexapi.GatewayInfo{}, "", false
}

// openSettings opens the settings window, or shows it when open. It is a
// window of its own, which MyGo allows.
func (a *App) openSettings() {
	if w := settingsUI.win; w != nil {
		w.Show()
		w.Focus()
		return
	}
	settingsUI.section, settingsUI.themeQuery = "appearance", ""
	resetIconPacksUI()
	settingsUI.themes = map[string]*terminal.Theme{}
	settingsUI.json = nil
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
		CornerRadius:         windowCorner,
		TrafficLightPosition: &mygo.Point{X: 18, Y: 19},
		BackgroundColor:      "light-dark(#efe1e6, #231e27)",
		Vibrancy:             glassVibrancy(),
		Transparent:          glassTransparent(),
		Content:              ui.View(a.settingsView),
		// A click that brings the window to the front only does that; see
		// DESIGN.md, Decisions.
		ConsumeActivationClick: true,
	}
	if ownControls {
		opts.HideWindowButtons = true
	}
	win := mygo.NewWindow(opts)
	settingsUI.win = win
	win.SetMenu(mygo.NewMenu(nil)) // not the terminal window's menus
	win.OnClosed(func() {
		stopRemotePoll()
		stopServerInfoPoll()
		closeAddHost()
		closeHostRename()
		remoteUI.pairQR = nil
		shortcutsUI = shortcutsState{}
		if a.st != nil && a.st.recording {
			a.st.recording = false
			rebuildMenu(a.st)
		}
		settingsUI.win = nil
		settingsUI.toast = toastSlot{}
	})
}

// sections are the sidebar's items, each with a Kubai two-tone icon and a
// semantic primary hue for light and dark surfaces (DSK-014).
var sections = []struct {
	id, icon    string
	light, dark ui.Color
	label       func(locales.Messages) string
}{
	{"appearance", "sun", ui.Hex("#007AFF"), ui.Hex("#3B9EFF"), locales.Messages.SettingsAppearance},  // blue
	{"terminal", "terminal", ui.Hex("#3A3A3C"), ui.Hex("#AEAEB2"), locales.Messages.SettingsTerminal}, // graphite
	{"theme", "sparkles", ui.Hex("#AF52DE"), ui.Hex("#BF6AF0"), locales.Messages.SettingsTheme},       // purple
	{"icons", "layers", ui.Hex("#FF9500"), ui.Hex("#FFA426"), locales.Messages.SettingsIconPacks},     // orange
	{"keyboard", "keyboard", ui.Hex("#636366"), ui.Hex("#98989D"), locales.Messages.SettingsKeyboard}, // gray
	{"hosts", "monitor", ui.Hex("#28A745"), ui.Hex("#3DD664"), locales.Messages.SettingsHosts},        // green
	{"file", "file-code", ui.Hex("#1FA0B8"), ui.Hex("#40C4DC"), locales.Messages.SettingsJSON},        // teal
	{"about", "info", ui.Hex("#636366"), ui.Hex("#98989D"), locales.Messages.SettingsAbout},           // gray
}

// The settings window's layout: the sidebar's width, the height of the
// header rows at the top of the sidebar and of the page, the room around
// a page and between its groups, and the width hints wrap at.
const (
	settingsSidebarW = 220
	settingsHeaderH  = 52
	settingsPad      = 28
	settingsGroupGap = 20
	settingsHintW    = 560
	settingsMaxW     = 720 // the settings column, at most
)

// settingsSidebarTop is the room above the sidebar's items: the traffic
// lights' row, which the card starts under. Windows draws its controls in
// the page's header instead, and there the card takes that room.
func settingsSidebarTop() float32 {
	if ownControls {
		return 14
	}
	return settingsHeaderH
}

// settingsTheme is the widgets' theme in the settings window: the app's,
// with capsule controls, as macOS's settings have. It is the settings
// window's alone; the terminal window keeps its own.
func settingsTheme(base *ui.Theme, k *colors) *ui.Theme {
	t := widgetTheme(base, k)
	t.Radius = k.radius(radiusXL)
	return t
}

func (a *App) settingsView(c *ui.Context) {
	a.runPosted() // the hosts' changes, without a terminal window
	// The settings window has no workspace of its own: as the commands it
	// sends do (appOf), it shows the colors of the window focused last, so
	// a space's tint paints it as it paints the window under it.
	k := colorsOf(c)
	if a.st != nil {
		if o := a.st.appOf(settingsUI.win); o != nil {
			k = o.colors(c)
		}
	}
	c.SetTheme(settingsTheme(c.Theme(), k))
	setupMenus(c)
	rootBackground(c, k.bgBottom)
	wash, dark, moves := backgroundAlpha(c), c.Theme().Dark, themeShaderMoves(c)
	root := ui.Row(c).Fill().AlignItems(ui.Stretch)
	sw := windowShader(root)
	root.Draw(func(p *ui.Painter, r ui.Rect) { paintBackground(p, r, k, wash, dark, moves, sw) }).Children(func() {
		// The sidebar paints no face: the window's glass shows through it,
		// and the traffic lights sit at its top on macOS.
		ui.Column(c).Width(settingsSidebarW).Shrink(0).Children(func() {
			ui.Box(c).Height(settingsSidebarTop()).Shrink(0).DragWindow()
			ui.Column(c).Grow(1).MinHeight(0).Padding(0, 10, 10, 10).Gap(2).Children(func() {
				a.serverCard(c, k)
				ui.Box(c).Height(8).Shrink(0)
				for _, s := range sections {
					primary := s.light
					if dark {
						primary = s.dark
					}
					a.sidebarItem(c, k, s.id, s.icon, primary, dark, say(s.label))
				}
			})
		})
		ui.Box(c).Width(1).Shrink(0).Background(k.tabSep.Alpha(0.6))
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			a.settingsTitleBar(c, k)
			// The settings.json page is a code editor, which scrolls
			// itself: it takes the scroll column's place.
			if settingsUI.section == "file" {
				a.jsonSettings(c, k)
				return
			}
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(4, settingsPad, settingsPad, settingsPad).Children(func() {
				ui.Column(c).Gap(settingsGroupGap).MaxWidth(settingsMaxW).Children(func() {
					switch settingsUI.section {
					case "server":
						a.serverSettings(c, k)
					case "terminal":
						a.terminalSettings(c, k)
					case "theme":
						a.themeSettings(c, k)
					case "icons":
						a.iconPacksSettings(c, k)
					case "keyboard":
						a.keyboardSettings(c, k)
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
	settingsUI.toast.build(c, k, 0)
	if page := remoteUI.pairQR; page != nil {
		// A code of this machine closes once its address no longer works.
		if tailnet, lan := localRexPairs(); page.local && page.address != tailnet && page.address != lan ||
			page.rexGo && !remoteUI.st.Running {
			remoteUI.pairQR = nil
		} else {
			a.hostPairQRSheet(c, k, page)
		}
	}
}

// terminalFace gives e the face of the main window's unfocused tiled
// terminal pane, as paneCard draws it: the system's glass tinted by it
// under the glass setting, else the face whole. A configured theme's
// background is not taken: the settings' text is set for the window's
// colors, and could not be read over a theme of the other appearance.
func terminalFace(c *ui.Context, k *colors, e ui.Element) {
	if termOpaque {
		e.Background(k.card)
		return
	}
	bg := cardFace(c, k, false, false)
	if glassOn() {
		e.Material(ui.SystemGlass{Tint: glassTint(c, k, false), Fallback: faceMaterial{bg}})
		return
	}
	e.Background(bg)
}

// sectionTitle is the title of a settings section, as the page's header
// shows it.
func sectionTitle(id string) string {
	switch id {
	case "server":
		return locales.HostServer()
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
		ui.Text(c, sectionTitle(settingsUI.section)).FontSize(fontTitle).FontWeight(700).TextColor(k.text).Grow(1).SingleLine().Ellipsis("…")
		windowControls(c, k, settingsUI.win)
	})
}

// sidebarItemH is the height of a sidebar item.
const sidebarItemH = 32

// sidebarItem is a section's chip, as a tab's: the chosen one has the
// active tab's face, and keeps its two-tone icon and its label's color.
func (a *App) sidebarItem(c *ui.Context, k *colors, id, iconName string, primary ui.Color, dark bool, label string) {
	active := settingsUI.section == id
	e := ui.Row(c.Key(id)).Height(sidebarItemH).Padding(0, 10).Gap(10).AlignItems(ui.Center).Radius(pillRadius(k, sidebarItemH, k.radius(radiusM))).
		Cursor(ui.CursorPointer).Role(ui.RoleTab).Selected(active).Label(label)
	hovered := e.Hovered()
	if active {
		glassPill(e, k, sidebarItemH)
	} else if hovered {
		e.Background(hoverFace(k))
	}
	e.Transition(hoverFade)
	if e.Clicked() {
		a.showSection(id)
	}
	e.Children(func() {
		// Bare Kubai two-tone icon driven by the row's hover state (DSK-014).
		duotone.Must(iconName).View(c, duotone.Options{
			Size:     20,
			Primary:  primary,
			Dark:     dark,
			Hovered:  hovered,
			Still:    true,
			NoMotion: prefs.NoIconMotion || prefs.Animation == "off",
		})
		ui.Text(c, label).FontSize(fontBody).FontWeight(500).TextColor(k.text).SingleLine()
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
	e := ui.Row(c.Key("server")).Gap(10).Padding(8, 10).Radius(k.radius(radiusXL)).AlignItems(ui.Center).Shrink(0).
		Cursor(ui.CursorPointer).Role(ui.RoleTab).Selected(active).Label(locales.SettingsServerCard()+", "+label).
		Background(k.card).Border(1, k.cardBorder)
	if active || e.Hovered() {
		e.Background(k.cardFocused)
	}
	if active {
		e.Border(1, k.accent.Alpha(0.55))
	}
	e.Transition(hoverFade)
	if e.Clicked() {
		a.showSection("server")
	}
	e.Children(func() {
		ui.Image(c, appIcon()).Size(34, 34).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, locales.SettingsServerCard()).FontSize(fontBody).FontWeight(600).TextColor(k.text).SingleLine().Ellipsis("…")
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(7, 7).Radius(k.capsule(7)).Background(dot).Shrink(0)
				ui.Text(c, label).FontSize(fontFootnote).TextColor(k.textFaint).SingleLine().Ellipsis("…").MinWidth(0)
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
	// Where this machine's programs reach the server's API, then who may
	// connect to it from elsewhere.
	a.localAPI(c, k)
	a.remoteSettings(c, k)
}

// serverGroup is the server page's card: the machine and its state, and
// the server's and the machine's facts.
func (a *App) serverGroup(c *ui.Context, k *colors, name, glyph, sub, label string, dot ui.Color, h rex.HostInfo) {
	settingsGroup(c, k, "", "", "", func() {
		groupRow(c, k, nil).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(44, 44).Radius(k.radius(radiusL)).Background(k.hover).Center().Shrink(0).Children(func() {
					glyphIcon(c, glyph, 26, k.text)
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, name).FontSize(fontTitle).FontWeight(700).TextColor(k.text).SingleLine().Ellipsis("…")
					ui.Text(c, sub).FontSize(fontSubhead).TextColor(k.textFaint).SingleLine().Ellipsis("…")
				})
				ui.Row(c).Gap(6).Padding(4, 10).Radius(k.radius(radiusL)).AlignItems(ui.Center).Shrink(0).Background(dot.Alpha(0.14)).Children(func() {
					ui.Box(c).Size(7, 7).Radius(k.capsule(7)).Background(dot)
					ui.Text(c, label).FontSize(fontSubhead).FontWeight(600).TextColor(k.text).SingleLine()
				})
			})
		})
		row := func(label, value string) { factRow(c, k, label, value) }
		if a.hello.PID > 0 {
			row(locales.HostServer(), a.serverUp(c))
		}
		sessions, running := a.sessionCounts()
		row(locales.HostSessions(), locales.HostSessionCount(sessions, running))
		hostFacts(h, row)
	})
}

// factRow is a group's row of a fact: its label, and its value at the
// right; none when the value is "".
func factRow(c *ui.Context, k *colors, label, value string) {
	if value == "" {
		return
	}
	groupRow(c, k, label).Children(func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(fontBody).FontWeight(500).TextColor(k.text).Shrink(0)
			ui.Text(c, value).FontSize(fontBody).TextColor(k.textFaint).Grow(1).MinWidth(0).TextAlign(ui.End).SingleLine().Ellipsis("…").Selectable()
		})
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
					ui.Text(c, label).FontSize(fontSubhead).FontWeight(600).TextColor(k.text)
				}
				if note != "" {
					rowHint(c, k, note)
				}
			})
		}
		saved := groupRows
		groupRows = 0
		ui.Column(c).Gap(2).Children(func() {
			// A group has the face of the main window's terminals.
			group := raisedShadow(ui.Column(c).Radius(k.radius(radiusXL)).Clip().Border(1, k.cardBorder), k)
			terminalFace(c, k, group)
			group.Children(body)
			if foot != "" {
				ui.Text(c, foot).FontSize(fontFootnote).TextColor(k.textFaint).LineHeight(1.4).MaxWidth(settingsHintW).Padding(0, 14)
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
	return ui.Column(c).MinHeight(44).Padding(12, 14).Gap(4).Justify(ui.Center)
}

// rowHint is the hint under a row's first line, across the row; nothing
// when hint is "".
func rowHint(c *ui.Context, k *colors, hint string) {
	if hint != "" {
		ui.Text(c, hint).FontSize(fontFootnote).TextColor(k.textFaint).LineHeight(1.4).MaxWidth(settingsHintW)
	}
}

// rowLine is a row's first line: its title at the left and its controls
// at the right, centered on one line.
func rowLine(c *ui.Context, k *colors, label string, control func()) {
	ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(fontBody).FontWeight(500).TextColor(k.text).Grow(1).MinWidth(0)
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
	settingsGroup(c, k, locales.SettingsExperimental(), "", "", func() {
		on := prefs.Canvas
		settingRow(c, k, locales.SettingsCanvas(), locales.SettingsCanvasHint(), func() {
			if ui.Switch(c, &on).Label(locales.SettingsCanvas()).Changed() {
				a.setCanvas(on)
			}
		})
	})
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
	iconMotion := !prefs.NoIconMotion
	settingRow(c, k, locales.SettingsIconMotion(), locales.SettingsIconMotionHint(), func() {
		if ui.Switch(c, &iconMotion).Label(locales.SettingsIconMotion()).Changed() {
			a.setWindowPref(func(s *settings) { s.NoIconMotion = !iconMotion })
		}
	})
}

// appearanceText is the Appearance page's group of the text: its size and
// the language.
func (a *App) appearanceText(c *ui.Context, k *colors) {
	size := float64(termFont.Size)
	hint := locales.SettingsTextSizeHint(float64(ghosttyRaw.fontSize(0)), float64(defaultFontSize))
	settingRow(c, k, locales.SettingsTextSize(), hint, func() {
		ui.Text(c, locales.SettingsTextSizeValue(size)).FontSize(fontBody).TextColor(k.textMuted)
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
	// While the theme's glass key decides, the switch shows its value and
	// takes no change (docs/DSK-012-theme-shaders.md, Glass).
	hint, byTheme := locales.SettingsGlassHint(), themeGlass() != nil
	if byTheme {
		hint = locales.SettingsGlassByTheme(palettes.Load().name)
	}
	settingRow(c, k, locales.SettingsGlass(), hint, func() {
		if ui.Switch(c, &glassy).Label(locales.SettingsGlass()).Disabled(byTheme).Changed() && !byTheme {
			a.setGlass(glassy)
		}
	})
	opacity := float64(termOpacity())
	settingRow(c, k, locales.SettingsTermOpacity(), locales.SettingsTermOpacityHint(), func() {
		ui.Text(c, locales.SettingsTermOpacityValue(opacity)).FontSize(fontBody).TextColor(k.textMuted)
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
	a.notificationsOff(c, k)
}

// notificationShown takes what came of showing a notification, off the
// main thread: main has the store record it (store.notified).
var notificationShown = func(error) {}

// notified records what came of showing a notification: macOS refused it,
// as the user turned GoRex's notifications off, or showed it, which
// clears the refusal. Settings says so (notificationsOff). Call it on the
// main thread.
func (st *store) notified(err error) {
	switch {
	case errors.Is(err, mygo.ErrNotificationsDenied):
		st.notifyOff = true
	case err == nil:
		st.notifyOff = false
	default:
		return
	}
	st.redraw()
}

// notificationSettingsURL opens the Notifications page of macOS's System
// Settings.
const notificationSettingsURL = "x-apple.systempreferences:com.apple.Notifications-Settings.extension"

// notificationsOff is the row that says macOS refuses GoRex's
// notifications, with a button to the system's setting; nothing while it
// does not. Only macOS refuses them (mygo.ErrNotificationsDenied).
func (a *App) notificationsOff(c *ui.Context, k *colors) {
	if a.st == nil || !a.st.notifyOff {
		return
	}
	settingRow(c, k, locales.SettingsNotificationsOff(), locales.SettingsNotificationsOffHint(), func() {
		if ui.Button(c, locales.SettingsNotificationsOpen()).Clicked() {
			c.OpenURL(notificationSettingsURL)
		}
	})
}

// jsonEditorState is the settings.json page's editor (settingsUI.json):
// its state, the buffer version it last saved or loaded, and the marks
// the bar shows.
type jsonEditorState struct {
	st       *editor.State
	saved    uint64    // the buffer's version at its last load or save
	conflict bool      // the file changed on disk while the buffer is dirty
	invalid  bool      // the last Save did not decode
	savedAt  time.Time // of the last save, for the brief "Saved"
}

// settingsFileText is the text of settings.json, "" when it cannot be read.
func settingsFileText() string {
	b, _ := os.ReadFile(settingsPath())
	return string(b)
}

// settingsFileStale marks a write of settings.json the watcher skipped —
// the app's own saves — for the settings.json page to resync on its next
// draw, on the main thread.
var settingsFileStale atomic.Bool

// jsonEditor is the settings.json page's editor state, made on its first
// draw: the file's text, JSON colors, and its saved version.
func jsonEditor() *jsonEditorState {
	if settingsUI.json == nil {
		st := &editor.State{
			Buffer: editor.NewBuffer(settingsFileText()),
			Label:  locales.SettingsJSON(),
		}
		st.Buffer.SetLanguage(editor.LanguageFor("settings.json"))
		settingsUI.json = &jsonEditorState{st: st, saved: st.Buffer.Version()}
	}
	return settingsUI.json
}

// syncSettingsJSONEditor refreshes the settings.json page's buffer with
// the file, as an apply may have changed it: a clean buffer takes the
// file's new text, a dirty one keeps its text and marks the conflict. It
// runs on the main thread, as the state it reads is.
func syncSettingsJSONEditor() {
	settingsFileStale.Store(false)
	j := settingsUI.json
	if j == nil || j.st == nil {
		return
	}
	text := settingsFileText()
	buf := j.st.Buffer
	if buf.String() == text {
		// Same bytes already: a Set would only bump the version and look
		// dirty.
		j.saved, j.conflict = buf.Version(), false
		return
	}
	if buf.Version() != j.saved {
		j.conflict = true
		return
	}
	buf.Set(text)
	j.saved, j.conflict = buf.Version(), false
}

// saveSettingsJSON writes the editor's text as settings.json: a text that
// decodes writes the file and applies it, as an outside edit does; one
// that does not writes nothing and marks the bar
// (docs/DSK-011-live-settings.md).
func (a *App) saveSettingsJSON() {
	j := settingsUI.json
	if j == nil || j.st == nil {
		return
	}
	text := j.st.Buffer.String()
	next, ok := decodeSettings([]byte(text))
	if !ok {
		j.invalid = true
		return
	}
	j.invalid = false
	// Recorded before the write, so the watcher's event of it is skipped,
	// as a write of saveSettings is.
	recordSettingsWritten([]byte(text))
	if err := atomicfile.Write(settingsPath(), []byte(text), 0o600); err != nil {
		log.Printf("gorex settings: %v", err)
		return
	}
	if a.st != nil {
		a.st.applySettingsFile(next)
	}
	j.saved, j.conflict, j.savedAt = j.st.Buffer.Version(), false, time.Now()
}

// revertSettingsJSON reads the file into the editor's buffer and clears
// its dirty and conflict marks.
func (a *App) revertSettingsJSON() {
	j := settingsUI.json
	if j == nil || j.st == nil {
		return
	}
	j.st.Buffer.Set(settingsFileText())
	j.saved, j.conflict, j.invalid, j.savedAt = j.st.Buffer.Version(), false, false, time.Time{}
}

// jsonSettings is the settings.json page: a bar over a code editor of the
// file, which fills the page's room where the other pages' scroll column
// is (docs/DSK-011-live-settings.md).
func (a *App) jsonSettings(c *ui.Context, k *colors) {
	j := jsonEditor()
	// A save of the app's own skipped the watcher: resync the buffer.
	if settingsFileStale.Swap(false) {
		syncSettingsJSONEditor()
	}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).Shrink(0).Padding(10, settingsPad).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.SettingsJSONHint()).FontSize(fontFootnote).TextColor(k.textFaint).Grow(1).MinWidth(0).SingleLine()
			status, statusColor := "", k.textFaint
			switch {
			case j.conflict:
				status, statusColor = locales.SettingsJSONChanged(), k.attention
			case j.invalid:
				status, statusColor = locales.SettingsJSONInvalid(), k.danger
			case j.st.Buffer.Version() != j.saved:
				status = locales.SettingsJSONUnsaved()
			case !j.savedAt.IsZero() && time.Since(j.savedAt) < 2*time.Second:
				status = locales.SettingsJSONSaved()
			}
			if status != "" {
				ui.Text(c, status).FontSize(fontSubhead).TextColor(statusColor).Shrink(0).SingleLine()
			}
			if ui.Button(c, locales.SettingsJSONRevert()).Clicked() {
				a.revertSettingsJSON()
			}
			if ui.Button(c, locales.SettingsJSONSave()).Clicked() {
				a.saveSettingsJSON()
			}
		})
		ui.Box(c).Height(0.5).Shrink(0).Background(k.tabSep)
		editor.View(c, j.st).Grow(1).MinHeight(0).Fill()
	})
}

func (a *App) terminalSettings(c *ui.Context, k *colors) {
	family := prefs.TermFont
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
					s.TermFont = family
					if slices.Index(fonts, family) == 0 {
						s.TermFont = ""
					}
				})
			}
		})
		groupRow(c, k, nil).Children(func() { a.fontPreview(c, k) })
	})
	settingsGroup(c, k, "", "", "", func() { a.cursorSettings(c, k) })
	settingsGroup(c, k, "", "", "", func() { a.contrastSettings(c, k) })
	settingsGroup(c, k, "", "", "", func() {
		groupRow(c, k, nil).Children(func() {
			rowLine(c, k, locales.SettingsUseGhosttyHint(), func() {
				if ui.Button(c, locales.SettingsUseGhostty()).Clicked() {
					a.setCursor(func(s *settings) {
						s.TermFont, s.TermCursor, s.TermCursorBlink, s.TermCopyOnSelect, s.TermMinContrast = "", "", nil, nil, 0
					})
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
			a.setCursor(func(s *settings) { s.TermCursor = cursorNames[style] })
		}
	})
	blink := !ghostty.NoBlink
	settingRow(c, k, locales.SettingsCursorBlink(), "", func() {
		if ui.Switch(c, &blink).Label(locales.SettingsCursorBlink()).Changed() {
			a.setCursor(func(s *settings) { s.TermCursorBlink = &blink })
		}
	})
	copyOn := ghostty.CopyOnSelect == terminal.CopyOnSelectClipboard
	settingRow(c, k, locales.SettingsCopyOnSelect(), locales.SettingsCopyOnSelectHint(), func() {
		if ui.Switch(c, &copyOn).Label(locales.SettingsCopyOnSelect()).Changed() {
			a.setPref(func(s *settings) { s.TermCopyOnSelect = &copyOn })
		}
	})
}

// contrastChoices are the minimum contrast picker's values: 0 follows
// Ghostty, 1 turns the correction off.
var contrastChoices = []float32{0, 1, 2, 3, 4.5, 7}

// contrastName names a minimum contrast in the picker.
func contrastName(v float32) string {
	switch v {
	case 1:
		return locales.SettingsContrastOff()
	case 4.5:
		return "4.5 (WCAG AA)"
	case 7:
		return "7 (WCAG AAA)"
	}
	return strconv.FormatFloat(float64(v), 'f', -1, 32)
}

// contrastSettings is the Terminal page's group of the minimum contrast:
// Ghostty's (else GoRex's default), off, or a ratio. A ratio of
// settings.json that the picker lacks is added to it, as a font is.
func (a *App) contrastSettings(c *ui.Context, k *colors) {
	follow := ghosttyRaw.withPrefs(settings{}).MinimumContrast
	values := contrastChoices
	if !slices.Contains(values, prefs.TermMinContrast) {
		values = append(slices.Clone(values), prefs.TermMinContrast)
	}
	names := make([]string, len(values))
	for i, v := range values {
		names[i] = contrastName(v)
	}
	plain := locales.SettingsContrastOff()
	if follow != 1 {
		plain = strconv.FormatFloat(float64(follow), 'f', -1, 32)
	}
	names[0] = locales.SettingsFollowGhosttyValue(plain)
	name := names[slices.Index(values, prefs.TermMinContrast)]
	settingRow(c, k, locales.SettingsMinContrast(), locales.SettingsMinContrastHint(), func() {
		if ui.Select(c, &name, names).Label(locales.SettingsMinContrast()).Width(220).Changed() {
			v := values[slices.Index(names, name)]
			a.setPref(func(s *settings) { s.TermMinContrast = v })
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
	ui.Column(c).Padding(12, 14).Radius(k.radius(radiusM)).Gap(2).Background(th.Background).Border(1, k.cardBorder).Children(func() {
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
		card := ui.Column(c.Key(id)).Grow(1).Basis(0).MinWidth(0).Gap(6).Padding(6, 6, 8, 6).Radius(k.radius(radiusXL)).
			Cursor(ui.CursorPointer).Role(ui.RoleButton).Selected(chosen).Label(name).
			Background(k.card).Border(2, ui.Transparent)
		if chosen {
			card.Border(2, k.accent)
		} else if card.Hovered() {
			card.Background(k.cardFocused)
		}
		if card.Clicked() {
			a.setPref(func(s *settings) { s.TermTheme = set })
		}
		card.Children(func() {
			ui.Column(c).Height(76).Padding(10, 12).Gap(8).Radius(concentric(k.radius(radiusXL), 2+6)).Background(th.Background).Border(1, k.cardBorder).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Aa").Font(monoFamily()).FontSize(fontTitle).FontWeight(700).TextColor(th.Foreground).Grow(1)
					if chosen {
						glyphIcon(c, "check", 15, k.accent)
					}
				})
				ui.Row(c).Gap(6).Children(func() {
					for _, i := range []int{1, 2, 4, 5} {
						ui.Box(c).Size(12, 12).Radius(k.capsule(12)).Background(th.Palette[i])
					}
				})
			})
			ui.Column(c).Padding(0, 4).Gap(2).Children(func() {
				ui.Text(c, name).FontSize(fontCallout).FontWeight(600).TextColor(k.text).SingleLine().Ellipsis("…")
				if sub != "" {
					ui.Text(c, sub).FontSize(fontCaption).TextColor(k.textFaint).SingleLine().Ellipsis("…")
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
	if themeTerm != [2]string{} {
		sub = strings.Join(slices.DeleteFunc(slices.Clone(themeTerm[:]), func(s string) bool { return s == "" }), " · ")
	}
	names := themeNames(terminal.GhosttyThemes(), settingsUI.themeQuery)
	if len(names) == 0 && settingsUI.themeQuery != "" {
		emptyMenu(c, k, "themes-empty", locales.SettingsThemeNoMatch(settingsUI.themeQuery))
	}
	shown := append([]string{""}, names[:min(len(names), maxThemes)]...)
	ui.Column(c).Gap(10).Children(func() {
		for row := 0; row < len(shown); row += themeColumns {
			ui.Row(c.Key(row)).Gap(10).AlignItems(ui.Stretch).Children(func() {
				for i := row; i < row+themeColumns; i++ {
					switch {
					case i >= len(shown):
						ui.Box(c).Grow(1).Basis(0).MinWidth(0) // the row's empty places
					case i == 0:
						pick("follow", locales.SettingsFollowGhostty(), sub, termLight, prefs.TermTheme == "", "")
					default:
						n := shown[i]
						pick(n, n, "", themeOf(n), prefs.TermTheme == n, n)
					}
				}
			})
		}
	})
	if len(names) > maxThemes {
		ui.Text(c, locales.SettingsMoreThemes(len(names)-maxThemes)).FontSize(fontFootnote).TextColor(k.textFaint).Padding(0, 14)
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
		ui.Text(c, locales.SettingsKeyboardNote()).FontSize(fontFootnote).TextColor(k.textFaint).LineHeight(1.4).Grow(1).MinWidth(0)
		resetAllButton(a, c)
	})
	if shortcutsUI.recording != nil {
		settingsGroup(c, k, "", "", "", func() {
			groupRow(c, k, nil).Children(func() {
				rowLine(c, k, locales.SettingsPressShortcut(shortcutsUI.recording.Title()), func() {
					if ui.Button(c, locales.SettingsCancel()).Clicked() {
						a.stopRecording()
					}
				})
				if shortcutsUI.err != "" {
					ui.Text(c, shortcutsUI.err).FontSize(fontFootnote).TextColor(k.danger).LineHeight(1.4)
				}
			})
		})
	}
	settingsGroup(c, k, "", "", "", func() {
		for i, r := range shortcutRows() {
			groupRow(c, k, i).MinHeight(40).Padding(8, 14).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, r.Title).FontSize(fontBody).FontWeight(500).TextColor(k.text).Grow(1).MinWidth(0)
					ui.Text(c, r.Keys).FontSize(fontSubhead).TextColor(k.textMuted).Padding(2, 10).Radius(k.radius(radiusM)).Background(k.hover).Shrink(0)
					if r.Command != nil {
						if !blockedFromClient(r.Command.ID) && ui.Button(c.Key("change"), locales.SettingsChange()).Clicked() {
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
				ui.Text(c, locales.SettingsUpdatesOff()).FontSize(fontFootnote).TextColor(k.textFaint)
			}
		})
		settingRow(c, k, locales.SettingsFile(), settingsPath(), nil)
	})
}
