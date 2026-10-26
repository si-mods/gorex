package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

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
		ui.Icon(c, icon(glyph)).Size(19, 19).TextColor(k.textMuted)
		ui.Column(c).MinWidth(0).Children(func() {
			ui.Text(c, name).FontSize(13.5).FontWeight(700).TextColor(k.text.Alpha(0.85)).SingleLine().Ellipsis("…")
			ui.Text(c, model).FontSize(11.5).TextColor(k.textFaint).SingleLine().Ellipsis("…")
		})
	})
	a.hostPopover(c, k, chip, name, glyph, 8, 0)
}

// hostButton is the host chip as an icon, for the sidebar's foot: the
// same panel opens from it, and a dot on the icon shows a remote host
// that is not connected.
func (a *App) hostButton(c *ui.Context, k *colors) {
	name, _, glyph := a.hostIdentity()
	label := locales.HostLabel(name)
	b := ui.Box(c).Size(30, 30).Center().Radius(30 / 2.6).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label).
		Tooltip(label)
	switch {
	case b.Pressed():
		b.Background(k.pressed)
	case b.Hovered() || a.hostOpen:
		b.Background(k.hover)
	}
	b.Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond})
	if b.Clicked() {
		a.hostOpen = !a.hostOpen
	}
	col := k.iconMuted
	if b.Hovered() || a.hostOpen {
		col = k.text
	}
	var bad *hostConn
	for _, h := range a.hosts {
		if h.state != hostConnected {
			bad = h
			if h.state == hostFailed {
				break
			}
		}
	}
	b.Children(func() {
		ui.Icon(c, icon(glyph)).Size(16, 16).TextColor(col)
		if bad != nil {
			ui.Box(c).Absolute().Top(5).Right(5).Size(7, 7).Radius(4).Background(hostDot(k, bad))
		}
	})
	a.hostPopover(c, k, b, name, glyph, 0, 8)
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
	glyph = "mac-studio"
	switch {
	case strings.Contains(model, "MacBook"), h.Portable:
		glyph = "laptop"
	case strings.Contains(model, "iMac"), runtime.GOOS == "windows":
		glyph = "monitor"
	case model == "Linux":
		glyph = "server"
	}
	return
}

// hostPopover opens the host's panel from its anchor, a margin away.
func (a *App) hostPopover(c *ui.Context, k *colors, anchor ui.Element, name, glyph string, top, bottom float32) {
	ui.PopoverBase(c, anchor, &a.hostOpen, func(panel ui.Element) {
		panel.Margin(top, 0, bottom, 0).Width(300).Padding(14).Radius(14).Background(k.panel).Border(1, k.panelBorder).
			Shadow(0, 12, 32, 0, k.shadowFocused).Gap(12)
		a.hostPanel(c, k, name, glyph)
	})
}

func (a *App) hostPanel(c *ui.Context, k *colors, name, glyph string) {
	h := a.hello.Host
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(38, 38).Radius(10).Background(k.hover).Center().Children(func() {
			ui.Icon(c, icon(glyph)).Size(24, 24).TextColor(k.text)
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
	ui.Column(c).Gap(7).Children(func() {
		row(locales.HostModel(), h.Model)
		row(locales.HostChip(), h.Chip)
		if h.Memory > 0 {
			row(locales.HostMemory(), fmt.Sprintf("%d GB", h.Memory>>30))
		}
		row(locales.HostSystem(), h.OS)
		row(locales.HostUser(), h.User)
	})
	ui.Divider(c)
	sessions, running := 0, 0
	for _, t := range a.tabs {
		for _, p := range t.panes() {
			sessions++
			if !p.info.Idle && p.info.Program != "" {
				running++
			}
		}
	}
	ui.Column(c).Gap(7).Children(func() {
		row(locales.HostServer(), locales.HostServerUp(a.hello.PID, roundDur(time.Since(a.hello.Started))))
		row(locales.HostSessions(), locales.HostSessionCount(sessions, running))
	})
	a.hostsInPanel(c, k)
	ui.Text(c, locales.HostNote()).
		FontSize(11.5).TextColor(k.textFaint).LineHeight(1.35)
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

// hostsInPanel lists, in the host's panel, this machine and the remote
// hosts with their state, each with a button that opens a tab there, and
// a button to the Remote Hosts settings.
func (a *App) hostsInPanel(c *ui.Context, k *colors) {
	ui.Divider(c)
	if len(a.hosts) > 0 {
		ui.Text(c, locales.SettingsHosts()).FontSize(12).FontWeight(600).TextColor(k.textFaint)
		ui.Column(c).Gap(2).Children(func() {
			for _, h := range a.allHosts() {
				name, label, dot := a.hostName(h), locales.HostsConnected(), k.busy
				if h != nil {
					label, _, _ = hostStatus(h)
					dot = hostDot(k, h)
				}
				sessions := 0
				for _, t := range a.tabsOn(h) {
					sessions += len(t.panes())
				}
				key := "local"
				if h != nil {
					key = h.ID
				}
				ui.Row(c.Key(key)).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(7, 7).Radius(4).Background(dot).Shrink(0)
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						ui.Text(c, name).FontSize(12.5).FontWeight(600).TextColor(k.text).SingleLine().Ellipsis("…")
						ui.Text(c, label+" · "+locales.HostsSessions(sessions)).FontSize(11.5).TextColor(k.textFaint).SingleLine().Ellipsis("…")
					})
					newTab := locales.CmdNewTabOn(name)
					if iconButton(c, k, "plus", newTab, 26, 15).Tooltip(newTab).Clicked() {
						a.hostOpen = false
						a.newTabOn(h, a.dirOn(h))
					}
				})
			}
		})
	}
	label := locales.HostsManage()
	if len(a.hosts) == 0 {
		label = locales.HostsAdd()
	}
	if ui.Button(c, label).Clicked() {
		a.hostOpen = false
		a.openSettingsAt("hosts")
		if len(a.hosts) == 0 {
			a.openAddHost()
		}
	}
}

// thisMachine names the machine the app runs on, as its system does.
func thisMachine() string { return locales.HostThisMachine(runtime.GOOS) }
