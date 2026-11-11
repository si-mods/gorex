package main

import (
	"cmp"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// hostChip shows the machine the sessions run on; a click shows more of
// it, and of the session server.
func (a *App) hostChip(c *ui.Context, k *colors) {
	name, model, glyph := a.hostIdentity()
	chip := ui.Row(c).Gap(9).Padding(3, 8, 3, 6).Radius(9).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Role(ui.RoleButton).Label(locales.HostLabel(name)).MaxWidth(190).Shrink(0)
	if chip.Hovered() || a.hostOpen {
		chip.Background(k.hover)
	}
	if chip.Clicked() {
		a.hostOpen = !a.hostOpen
	}
	chip.Children(func() {
		glyphIcon(c, glyph, 19, k.textMuted)
		ui.Column(c).MinWidth(0).Children(func() {
			ui.Text(c, name).FontSize(13.5).FontWeight(700).TextColor(k.text.Alpha(0.85)).SingleLine().Ellipsis("…")
			ui.Text(c, model).FontSize(11.5).TextColor(k.textFaint).SingleLine().Ellipsis("…")
		})
	})
	a.hostPopover(c, k, chip, name, glyph)
}

// hostButton is the host chip as an icon, for the sidebar's foot: the
// same panel opens from it. A remote host that is not connected shows as
// a dot on the icon and in the button's label and tooltip; one making
// its first attempt, as every launch does, is not flagged: it shows
// once that attempt failed.
func (a *App) hostButton(c *ui.Context, k *colors) {
	name, _, glyph := a.hostIdentity()
	label := locales.HostLabel(name)
	var bad *hostConn
	away := a.awayHosts()
	for _, h := range away {
		if bad == nil || (h.state == hostFailed && bad.state != hostFailed) {
			bad = h
		}
	}
	if bad != nil {
		status, _, _ := hostStatus(bad)
		label = locales.HostLabelIssue(name, a.hostName(bad), status, len(away)-1)
	}
	b := iconButtonWith(c, k, glyph, label, 30, 16, a.hostOpen, func() {
		if bad != nil {
			ui.Box(c).Absolute().Top(5).Right(5).Size(7, 7).Radius(4).Background(hostDot(k, bad))
		}
	}).Tooltip(label)
	if b.Clicked() {
		a.hostOpen = !a.hostOpen
	}
	a.hostPopover(c, k, b, name, glyph)
}

// awayHosts is the remote hosts that are not connected, but for those
// making their first attempt.
func (a *App) awayHosts() (away []*hostConn) {
	for _, h := range a.hosts {
		if h.state != hostConnected && h.state != hostConnecting {
			away = append(away, h)
		}
	}
	return
}

// hostIdentity is this machine's name, model, and the icon that stands
// for it.
func (a *App) hostIdentity() (name, model, glyph string) {
	h := a.hello.Host
	name, model = h.Name, h.Model
	if name == "" {
		name, _ = os.Hostname()
		name = strings.TrimSuffix(name, ".local")
	}
	return name, model, hostGlyph(h, runtime.GOOS == "windows")
}

// hostGlyph is the icon that stands for a machine the server described,
// windows telling that it runs Windows.
func hostGlyph(h rex.HostInfo, windows bool) string {
	switch {
	case strings.Contains(h.Model, "MacBook"), h.Portable:
		return "laptop"
	case strings.Contains(h.Model, "iMac"), windows:
		return "monitor"
	case strings.Contains(h.Model, "Mac mini"):
		return "mac-mini"
	case h.Model == "Linux":
		return "server"
	}
	return "mac-studio"
}

// remoteGlyph is the icon of a remote host: the one the user chose, else
// as its last hello described it, kept while it is away; a server, before
// it ever connected or when it said nothing of its machine.
func (h *hostConn) remoteGlyph() string {
	return cmp.Or(knownGlyph(h.Icon), knownGlyph(h.Glyph), "server")
}

// knownGlyph is g if it is one of hostIcons, else "": hosts.json may name
// an icon this build lacks, from a newer one or a hand's edit.
func knownGlyph(g string) string {
	if slices.ContainsFunc(hostIcons, func(i hostIcon) bool { return i.glyph == g }) {
		return g
	}
	return ""
}

type hostIcon struct {
	glyph string
	name  func() string
}

// hostIcons are the icons the user can give a remote host, as the iOS app
// offers them, with their names.
var hostIcons = []hostIcon{
	{"globe", locales.HostIconGlobe},
	{"server", locales.HostIconServer},
	{"cloud", locales.HostIconCloud},
	{"laptop", func() string { return "MacBook" }},
	{"mac-mini", func() string { return "Mac mini" }},
	{"mac-studio", func() string { return "Mac Studio" }},
	{"monitor", locales.HostIconDesktop},
}

// hostIconMenu fills a menu with the icons h can take, the one it has
// checked: Automatic, which follows its machine, and those of hostIcons.
func (a *App) hostIconMenu(m *ui.Menu, h *hostConn) {
	if m.Item(locales.HostIconAutomatic()).Icon(menuIcon(cmp.Or(knownGlyph(h.Glyph), "server"))).Checked(knownGlyph(h.Icon) == "").Chosen() {
		a.setHostIcon(h, "")
	}
	m.Separator()
	for _, it := range hostIcons {
		if m.Item(it.name()).Icon(menuIcon(it.glyph)).Checked(h.Icon == it.glyph).Chosen() {
			a.setHostIcon(h, it.glyph)
		}
	}
}

// setHostIcon gives h the icon it shows by in every window, "" for its
// machine's.
func (a *App) setHostIcon(h *hostConn, glyph string) {
	h.mu.Lock()
	h.Icon = glyph
	h.mu.Unlock()
	a.saveHosts()
	if a.st != nil {
		a.st.redraw()
	}
}

// menuIcons are the icons of menu items, by glyph, each made once.
var menuIcons = map[string]*mygo.MenuIcon{}

// menuIcon is the menu items' icon of the SVG named name, in the color of
// their text.
func menuIcon(name string) *mygo.MenuIcon {
	if m, ok := menuIcons[name]; ok {
		return m
	}
	m, err := mygo.NewMenuIconSVG(icon(name), true)
	if err != nil {
		panic(err)
	}
	menuIcons[name] = m
	return m
}

// hostPopover opens the host's panel from its anchor, a margin below it,
// or above where there is no room below.
func (a *App) hostPopover(c *ui.Context, k *colors, anchor ui.Element, name, glyph string) {
	ui.PopoverBase(c, anchor, &a.hostOpen, func(panel ui.Element) {
		_, winH := c.Size()
		// No higher than the room on the side it opens on, the larger:
		// above the button at the sidebar's foot, it would otherwise cover
		// the button or go past the window's top.
		room := winH - 24
		if r := anchor.Bounds(); r.H > 0 {
			room = min(room, max(r.Y, winH-r.Y-r.H)-16)
		}
		glassPanel(panel.Margin(8, 0, 0, 0).MaxHeight(room).Width(300).Padding(14).Radius(14), k.panel).Border(1, k.panelBorder).
			Shadow(0, 12, 32, 0, k.shadowFocused)
		// A short window scrolls the panel's body.
		ui.Scroll(c).MinHeight(0).Children(func() {
			ui.Column(c).Gap(12).Children(func() { a.hostPanel(c, k, name, glyph) })
		})
	})
}

func (a *App) hostPanel(c *ui.Context, k *colors, name, glyph string) {
	h := a.hello.Host
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(38, 38).Radius(10).Background(k.hover).Center().Children(func() {
			glyphIcon(c, glyph, 24, k.text)
		})
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, name).FontSize(14).FontWeight(700).TextColor(k.text).SingleLine().Ellipsis("…")
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(7, 7).Radius(4).Background(k.busy)
				ui.Text(c, locales.HostConnected(thisMachine())).FontSize(12).TextColor(k.textFaint)
			})
		})
	})
	ui.Divider(c)
	row := func(label, value string) {
		if value == "" {
			return
		}
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, label).FontSize(12.5).TextColor(k.textFaint).Width(78)
			ui.Text(c, value).FontSize(12.5).TextColor(k.text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
		})
	}
	ui.Column(c).Gap(7).Children(func() { hostFacts(h, row) })
	ui.Divider(c)
	sessions, running := a.sessionCounts()
	ui.Column(c).Gap(7).Children(func() {
		row(locales.HostServer(), locales.HostServerUp(a.hello.PID, roundDur(time.Since(a.hello.Started))))
		row(locales.HostSessions(), locales.HostSessionCount(sessions, running))
	})
	ui.Divider(c)
	a.hostsInPanel(c, k)
	ui.Text(c, locales.HostNote()).
		FontSize(11.5).TextColor(k.textFaint).LineHeight(1.35)
}

// hostsInPanel leads, from the host's panel, to the remote hosts: it
// names those the host button flags, with a button to the Remote Hosts
// settings, or, with no hosts saved, a button that adds one.
func (a *App) hostsInPanel(c *ui.Context, k *colors) {
	away := a.awayHosts()
	for _, h := range away {
		status, _, _ := hostStatus(h)
		ui.Row(c.Key(hostKey(h))).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(7, 7).Radius(4).Background(hostDot(k, h)).Shrink(0)
			ui.Text(c, a.hostName(h)).FontSize(12.5).FontWeight(600).TextColor(k.text).MinWidth(0).SingleLine().Ellipsis("…")
			ui.Text(c, status).FontSize(12.5).TextColor(k.textFaint).Shrink(0)
		})
	}
	label := locales.HostsManage()
	switch {
	case len(a.hosts) == 0:
		label = locales.HostsAdd()
	case len(away) == 0:
		return
	}
	if ui.Button(c, label).Clicked() {
		a.hostOpen = false
		a.openSettingsAt("hosts")
		if len(a.hosts) == 0 {
			a.openAddHost()
		}
	}
}

// hostFacts tells the machine h's facts to row, each as its label and
// value: its model, chip, memory, system and user.
func hostFacts(h rex.HostInfo, row func(label, value string)) {
	row(locales.HostModel(), h.Model)
	row(locales.HostChip(), h.Chip)
	if h.Memory > 0 {
		row(locales.HostMemory(), fmt.Sprintf("%d GB", h.Memory>>30))
	}
	row(locales.HostSystem(), h.OS)
	row(locales.HostUser(), h.User)
}

// sessionCounts is how many sessions the window has open, and how many of
// them run a program.
func (a *App) sessionCounts() (sessions, running int) {
	for _, t := range a.tabs {
		for _, p := range t.allPanes() {
			sessions++
			if !p.info.Idle && p.info.Program != "" {
				running++
			}
		}
	}
	return
}

func roundDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return locales.HostDurSeconds(int(d.Seconds()))
	case d < time.Hour:
		return locales.HostDurMinutes(int(d.Minutes()))
	case d < 48*time.Hour:
		return locales.HostDurHoursMinutes(int(d.Hours()), int(d.Minutes())%60)
	}
	return locales.HostDurDays(int(d.Hours() / 24))
}

// thisMachine names the machine the app runs on, as its system does.
func thisMachine() string { return locales.HostThisMachine(runtime.GOOS) }
