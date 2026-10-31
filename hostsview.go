package main

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// The Remote Hosts page of the settings window: the hosts this machine
// connects to, their state, and the sheet that adds one, pairing this
// machine with it when the host does not allow it yet.

// hostsUI is the page's state; the main thread owns it.
var hostsUI struct {
	adding bool // the sheet shows
	// The sheet's fields. The address is a secret: its field hides it,
	// and it is forgotten once the host is saved or the sheet closes.
	address, name, pairAs string
	formErr               func() string // what is wrong, said when drawn
	note                  func() string // why the host is not reached while it is asked to approve
	stage                 addStage
	fingerprint           string             // this machine's, while the host is asked to approve it
	cancel                context.CancelFunc // stops the attempt under way
	removing              string             // the id of the host Remove asks about
	layoutOpen            bool
	layoutHost            *hostConn
	layout                savedStore
	layoutErr             string
	layoutLoading         bool
	layoutAt              time.Time
}

type addStage int

const (
	addForm       addStage = iota // the fields
	addConnecting                 // trying the address
	addWaiting                    // the host's user is asked to approve
)

// pairPoll is how often the sheet tries again while the host is asked to
// approve this machine.
var pairPoll = 3 * time.Second

// localName names this machine for the hosts it pairs with: its name, as
// the server tells it.
func (a *App) localName() string {
	if n := a.hello.Host.Name; n != "" {
		return n
	}
	n, _ := os.Hostname()
	return strings.TrimSuffix(n, ".local")
}

// openSettingsAt opens the settings window at a section.
func (a *App) openSettingsAt(id string) {
	a.openSettings()
	a.showSection(id)
	if w := settingsUI.win; w != nil {
		w.Update(func() {})
	}
}

// openAddHost opens the sheet that adds a host, its fields empty.
func (a *App) openAddHost() {
	cancelAddHost()
	page := &hostsUI
	page.adding, page.address, page.name, page.pairAs = true, "", "", a.localName()
	page.formErr, page.fingerprint = nil, ""
}

// cancelAddHost stops an attempt under way and goes back to the fields.
func cancelAddHost() {
	page := &hostsUI
	if page.cancel != nil {
		page.cancel()
		page.cancel = nil
	}
	page.stage, page.fingerprint, page.note = addForm, "", nil
}

// closeAddHost closes the sheet, forgetting the address.
func closeAddHost() {
	cancelAddHost()
	page := &hostsUI
	page.adding, page.address, page.name, page.formErr = false, "", "", nil
}

// validateHost returns what is wrong with a host to add, "" if nothing.
func validateHost(address, name, pairAs string, hosts []*hostConn) string {
	address, name = strings.TrimSpace(address), strings.TrimSpace(name)
	switch {
	case name == "":
		return locales.HostsEnterName()
	case address == "":
		return locales.HostsEnterAddress()
	case !validAddress(address):
		return locales.HostsBadAddress()
	case strings.TrimSpace(pairAs) == "":
		return locales.HostsEnterPairName()
	}
	for _, h := range hosts {
		if h.Address == address {
			return locales.HostsAlreadySaved()
		}
	}
	for _, h := range hosts {
		if strings.EqualFold(h.Name, name) {
			return locales.HostsNameTaken()
		}
	}
	return ""
}

// submitHost checks the sheet's fields, and tries the host: it is saved
// once it answers, after this machine is paired with it if need be.
func (a *App) submitHost() {
	page := &hostsUI
	if page.stage != addForm {
		return
	}
	address, name, pairAs, hosts := page.address, page.name, page.pairAs, a.hosts
	if validateHost(address, name, pairAs, hosts) != "" {
		page.formErr = func() string { return validateHost(address, name, pairAs, hosts) }
		return
	}
	s := savedHost{ID: newHostID(), Name: strings.TrimSpace(name), Address: strings.TrimSpace(address)}
	ctx, cancel := context.WithCancel(context.Background())
	page.stage, page.cancel, page.formErr, page.note = addConnecting, cancel, nil, nil
	// The pairing is the settings window's: it goes on, and saves the host,
	// though the terminal window closes. Its reports apply while it is the
	// attempt under way.
	update := a.settingsUpdate()
	report := func(fn func()) {
		update(func() {
			if ctx.Err() == nil {
				fn()
			}
		})
	}
	go pairHost(ctx, s, strings.TrimSpace(pairAs), report, func() {
		page.cancel = nil
		cancel()
		closeAddHost()
		a.addHost(s)
	})
}

// settingsUpdate returns how the settings window's work changes the state:
// on the main thread, through the settings window, drawing the terminal
// window too when it is open. Call it on the main thread.
func (a *App) settingsUpdate() func(func()) {
	set := settingsUI.win
	return func(fn func()) {
		if set == nil {
			a.post(fn) // tests: no window
			return
		}
		set.Update(func() {
			fn()
			if w := a.win; w != nil {
				w.Update(func() {})
			}
		})
	}
}

// pairHost connects to a host to add: when it allows this machine, done
// runs; else it asks to pair, shows the fingerprint, and tries again while
// the request waits, until the host's user approves (done runs), rejects
// it or lets the window close (the sheet says so), or the attempt is
// canceled. A host not reached meanwhile is tried less often, and the
// sheet says why. report runs a change on the main thread.
func pairHost(ctx context.Context, s savedHost, pairAs string, report func(func()), done func()) {
	var logged failureLog
	fail := func(msg func() string) {
		logged.log(s.Name, msg)
		report(func() {
			hostsUI.stage, hostsUI.formErr, hostsUI.cancel, hostsUI.note = addForm, msg, nil, nil
		})
	}
	c, _, why, failed := connectHost(ctx, s.Address)
	switch {
	case c != nil:
		c.Close()
		report(done)
		return
	case ctx.Err() != nil:
		return
	case failed != failRefused && failed != failPending:
		// Only a host that refuses this machine is asked to pair.
		fail(why)
		return
	}
	c, err := dialHost(ctx, s.Address)
	if err != nil {
		fail(locales.HostsUnreachable)
		return
	}
	fp, err := c.RequestPairing(pairAs)
	c.Close()
	if err != nil {
		if strings.Contains(err.Error(), "not pairing") {
			name := s.Name
			fail(func() string { return locales.HostsNotPairing(name) })
			return
		}
		msg := serverMessage(errors.New(rex.Scrub(err.Error(), s.Address)))
		fail(func() string { return msg })
		return
	}
	report(func() { hostsUI.stage, hostsUI.fingerprint = addWaiting, fp })
	wait := pairPoll
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		c, _, why, failed := connectHost(ctx, s.Address)
		switch {
		case c != nil:
			c.Close()
			report(done)
			return
		case ctx.Err() != nil:
			return
		case failed == failPending:
			wait = pairPoll
			logged.log(s.Name, nil)
			report(func() { hostsUI.note = nil })
		case failed == failRefused:
			// Rejected, or the window closed: a new request is the user's
			// to make.
			name := s.Name
			fail(func() string { return locales.HostsPairRejected(name) })
			return
		default:
			wait = min(wait*2, 30*time.Second)
			logged.log(s.Name, why)
			report(func() { hostsUI.note = why })
		}
	}
}

func (a *App) hostsSettings(c *ui.Context, k *colors) {
	settingsTitle(c, k, locales.SettingsHosts())
	page := &hostsUI
	ui.Text(c, locales.HostsNote()).FontSize(12).TextColor(k.textFaint).LineHeight(1.3).Padding(0, 0, 0, 4)
	card(ui.Column(c).Padding(6, 14).Gap(0), k).Children(func() {
		if len(a.hosts) == 0 {
			ui.Text(c, locales.HostsNone()).FontSize(12.5).TextColor(k.textFaint).Padding(8, 0)
		}
		for i, h := range a.hosts {
			if i > 0 {
				ui.Box(c).Height(1).Background(k.tabSep.Alpha(0.5))
			}
			a.hostRow(c, k, h)
		}
	})
	ui.Row(c).Children(func() {
		if ui.Button(c, locales.HostsAdd()).Clicked() {
			a.openAddHost()
		}
	})
	if page.adding {
		a.addHostSheet(c, k)
	}
	if page.layoutOpen {
		a.hostLayoutSheet(c, k)
	}
}

// hostRow is a saved host: its state, Retry while it is not connected, and
// Remove, which asks first.
func (a *App) hostRow(c *ui.Context, k *colors, h *hostConn) {
	page := &hostsUI
	label, detail, up := hostStatus(h)
	ui.Column(c.Key(h.ID)).Gap(8).Padding(9, 0).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			glyphIcon(c, "server", 18, k.textMuted).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, h.Name).FontSize(13.5).FontWeight(600).TextColor(k.text).SingleLine().Ellipsis("…")
				ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(7, 7).Radius(4).Background(hostDot(k, h)).Shrink(0)
					ui.Text(c, label).FontSize(12.5).FontWeight(500).TextColor(k.textMuted)
					if up && h.hello.Host.Model != "" {
						ui.Text(c, h.hello.Host.Model).FontSize(12).TextColor(k.textFaint).SingleLine().Ellipsis("…").MinWidth(0)
					}
				})
				if detail != "" {
					ui.Text(c, detail).FontSize(12).TextColor(k.textFaint).LineHeight(1.3)
				}
			})
			if up && ui.Button(c.Key("layout"), locales.HostsLayout()).Clicked() {
				a.openHostLayout(h)
			}
			if !up && ui.Button(c.Key("retry"), locales.HostsRetry()).Clicked() {
				h.poke()
			}
			// A host of the network comes back with its next
			// directory; removing it here would only make it return.
			if !h.Network && ui.Button(c.Key("remove"), locales.HostsRemove()).Disabled(page.removing == h.ID).Clicked() {
				page.removing = h.ID
			}
		})
		if page.removing != h.ID {
			return
		}
		ui.Column(c).Gap(8).Padding(8, 10).Radius(9).Background(k.hover).Children(func() {
			ui.Text(c, locales.HostsRemoveAsk(h.Name)).FontSize(12).TextColor(k.text).LineHeight(1.3)
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c.Key("remove-yes"), locales.HostsRemove()).Clicked() {
					page.removing = ""
					a.removeHost(h)
				}
				if ui.Button(c.Key("remove-no"), locales.RemoteCancel()).Clicked() {
					page.removing = ""
				}
			})
		})
	})
}

// openHostLayout shows h's layout read-only.
func (a *App) openHostLayout(h *hostConn) {
	page := &hostsUI
	page.layoutOpen, page.layoutHost = true, h
	page.layout, page.layoutErr, page.layoutLoading, page.layoutAt = savedStore{}, "", false, time.Time{}
	a.refreshHostLayout()
}

// refreshHostLayout reads the host's layout, at most once a second.
func (a *App) refreshHostLayout() {
	page := &hostsUI
	h := page.layoutHost
	if h == nil || page.layoutLoading || time.Since(page.layoutAt) < time.Second {
		return
	}
	if h.update == nil {
		return
	}
	c := h.current()
	if c == nil {
		label, _, _ := hostStatus(h)
		page.layoutErr = label
		return
	}
	page.layoutLoading = true
	go func() {
		raw, err := c.Layout()
		h.update(func() {
			page := &hostsUI
			if page.layoutHost != h {
				return
			}
			page.layoutLoading, page.layoutAt = false, time.Now()
			if err != nil {
				page.layoutErr = err.Error()
				return
			}
			page.layoutErr = ""
			page.layout = parseLayout(raw)
		})
	}()
}

// hostLayoutSheet lists the host's workspaces, tabs and panes, read-only.
func (a *App) hostLayoutSheet(c *ui.Context, k *colors) {
	page := &hostsUI
	was := page.layoutOpen
	ui.DialogBase(c, &page.layoutOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(k.backdrop)
		panel.Width(620).MaxHeight(560).Padding(18).Gap(12).Radius(16).Background(k.panel).Border(1, k.panelBorder).
			Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.28)).Role(ui.RoleDialog).Label(locales.HostsLayoutTitle())
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.HostsLayoutTitle()).FontSize(15).FontWeight(700).TextColor(k.text)
			if page.layoutHost != nil {
				ui.Text(c, page.layoutHost.Name).FontSize(12.5).TextColor(k.textFaint)
			}
			ui.Spacer(c)
			if page.layoutHost != nil && ui.Button(c, locales.HostsLayoutNewTab()).Clicked() {
				a.hostLayoutCommand(page.layoutHost, rex.LayoutCommand{Op: "tab.create"}, "")
			}
			if ui.Button(c, locales.HostsLayoutClose()).Clicked() {
				page.layoutOpen = false
			}
		})
		if page.layoutErr != "" {
			ui.Text(c, page.layoutErr).FontSize(12.5).TextColor(k.danger).LineHeight(1.3)
		}
		if page.layoutLoading {
			ui.Text(c, locales.HostsLayoutLoading()).FontSize(12.5).TextColor(k.textMuted)
		}
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Gap(10).Children(func() {
				if len(page.layout.Sessions) == 0 && !page.layoutLoading && page.layoutErr == "" {
					ui.Text(c, locales.HostsLayoutEmpty()).FontSize(12.5).TextColor(k.textFaint)
				}
				for _, w := range page.layout.Sessions {
					ui.Column(c).Gap(4).Children(func() {
						ui.Text(c, w.Name).FontSize(13).FontWeight(600).TextColor(k.text)
						for ti, t := range w.Tabs {
							ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Text(c, "  "+layoutTabName(t, ti)).FontSize(12.5).TextColor(k.textMuted).Grow(1).MinWidth(0)
								if sid := tabSID(t.Root); sid != "" && ui.Button(c.Key("close-tab-"+sid), locales.HostsLayoutCloseTab()).Clicked() {
									a.hostLayoutCommand(page.layoutHost, rex.LayoutCommand{Op: "tab.close", SID: sid}, "")
								}
							})
							lineRow := func(line layoutLine) {
								ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
									ui.Text(c, line.text).Font(monoFamily()).FontSize(11.5).TextColor(k.textFaint).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
									if line.sid != "" {
										if ui.Button(c.Key("open-"+line.sid), locales.HostsLayoutOpen()).Clicked() {
											a.openHostPane(page.layoutHost, line.sid)
										}
										if ui.Button(c.Key("focus-"+line.sid), locales.HostsLayoutFocus()).Clicked() {
											a.focusHostPane(page.layoutHost, line.sid)
										}
										for _, dir := range []struct{ name, label string }{
											{"left", "←"}, {"right", "→"}, {"up", "↑"}, {"down", "↓"},
										} {
											dir := dir
											label := map[string]string{
												"left": locales.HostsLayoutLeft(), "right": locales.HostsLayoutRight(),
												"up": locales.HostsLayoutUp(), "down": locales.HostsLayoutDown(),
											}[dir.name]
											if ui.Button(c.Key("resize-"+line.sid+"-"+dir.name), dir.label).Tooltip(label).Clicked() {
												a.hostLayoutCommand(page.layoutHost, rex.LayoutCommand{
													Op: "pane.resize", SID: line.sid, Direction: dir.name, Amount: 0.05,
												}, "")
											}
										}
									}
								})
							}
							for _, line := range layoutLines(t.Root, 2) {
								lineRow(line)
							}
							// A float's session lists under the tree's, with
							// the same buttons.
							for _, f := range t.Floats {
								if f.Pane == nil {
									continue
								}
								for _, line := range layoutLines(f.Pane, 2) {
									lineRow(line)
								}
							}
						}
					})
				}
			})
		})
		a.refreshHostLayout()
	})
	if was && !page.layoutOpen {
		closeHostLayout()
	}
}

// closeHostLayout forgets the sheet's host and state.
func closeHostLayout() {
	page := &hostsUI
	page.layoutOpen, page.layoutHost = false, nil
	page.layout, page.layoutErr, page.layoutLoading, page.layoutAt = savedStore{}, "", false, time.Time{}
}

func tabSID(n *savedNode) string {
	if n == nil {
		return ""
	}
	if n.SID != "" {
		return n.SID
	}
	if sid := tabSID(n.A); sid != "" {
		return sid
	}
	return tabSID(n.B)
}

// hostLayoutCommand asks the host app to change its layout, then reloads
// the sheet's copy.
func (a *App) hostLayoutCommand(h *hostConn, cmd rex.LayoutCommand, errText string) {
	c := h.current()
	if c == nil || h.update == nil {
		hostsUI.layoutErr = errText
		return
	}
	go func() {
		res, err := c.Command(cmd)
		h.update(func() {
			page := &hostsUI
			if err != nil {
				page.layoutErr = err.Error()
			} else if res.Code != "" {
				page.layoutErr = res.Error
			} else {
				page.layoutErr = ""
			}
			page.layoutAt = time.Time{}
			a.refreshHostLayout()
		})
	}()
}

func layoutTabName(t savedTab, at int) string {
	if t.Name != "" {
		return t.Name
	}
	return "Tab " + strconv.Itoa(at+1)
}

type layoutLine struct {
	text string
	sid  string
}

func layoutLines(n *savedNode, depth int) []layoutLine {
	if n == nil {
		return nil
	}
	prefix := strings.Repeat("  ", depth)
	if n.SID != "" || n.A == nil && n.B == nil || n.Unavailable {
		name := n.Name
		if name == "" {
			name = n.SID
		}
		dir := n.Dir
		if dir != "" {
			name += " — " + dir
		}
		return []layoutLine{{text: prefix + name, sid: n.SID}}
	}
	return append(layoutLines(n.A, depth), layoutLines(n.B, depth)...)
}

// openHostPane shows a host session in a tab of this window, or focuses
// the pane that already shows it.
func (a *App) openHostPane(h *hostConn, sid string) {
	if h == nil {
		return
	}
	for ti, t := range a.tabs {
		for _, p := range t.allPanes() {
			if p.host == h && p.SID == sid {
				a.selectTab(ti)
				a.revealPane(p)
				t.setFocus(p)
				a.focusReq = p
				return
			}
		}
	}
	c := h.current()
	if c == nil || h.update == nil {
		return
	}
	go func() {
		infos, err := c.List()
		if err != nil {
			h.update(func() { hostsUI.layoutErr = err.Error() })
			return
		}
		var in rex.SessionInfo
		found := false
		for _, o := range infos {
			if o.ID == sid && !o.Exited {
				in, found = o, true
				break
			}
		}
		h.update(func() {
			if !found {
				hostsUI.layoutErr = locales.HostsLayoutNoSession()
				return
			}
			a.tabs = append(a.tabs, a.sessionTab(h, in))
			a.active = len(a.tabs) - 1
			a.focusReq = a.tab().Focus
			a.changed()
		})
	}()
}

// focusHostPane asks the host app to focus a pane it shared.
func (a *App) focusHostPane(h *hostConn, sid string) {
	if h == nil {
		return
	}
	c := h.current()
	if c == nil || h.update == nil {
		return
	}
	go func() {
		_, err := c.Command(rex.LayoutCommand{Op: "pane.focus", SID: sid})
		h.update(func() {
			if err != nil {
				hostsUI.layoutErr = err.Error()
			} else {
				hostsUI.layoutErr = ""
			}
		})
	}()
}

// hostDot is the color of a host's state.
func hostDot(k *colors, h *hostConn) ui.Color {
	switch h.state {
	case hostConnected:
		return k.busy
	case hostFailed:
		return k.danger
	}
	return k.attention
}

// addHostSheet is the sheet that adds a host: its fields, then the
// connection's progress and the fingerprint to approve.
func (a *App) addHostSheet(c *ui.Context, k *colors) {
	page := &hostsUI
	was := page.adding
	ui.DialogBase(c, &page.adding, func(backdrop, panel ui.Element) {
		backdrop.Background(k.backdrop)
		panel.Width(440).Padding(18).Gap(12).Radius(16).Background(k.panel).Border(1, k.panelBorder).
			Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.28)).Role(ui.RoleDialog).Label(locales.HostsAddTitle())
		ui.Text(c, locales.HostsAddTitle()).FontSize(15).FontWeight(700).TextColor(k.text)
		switch page.stage {
		case addConnecting:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(7, 7).Radius(4).Background(k.attention)
				ui.Text(c, locales.HostsConnectingTo(strings.TrimSpace(page.name))).FontSize(13).TextColor(k.textMuted)
			})
		case addWaiting:
			ui.Text(c, locales.HostsApprove(strings.TrimSpace(page.name))).FontSize(13).TextColor(k.text).LineHeight(1.35)
			ui.Column(c).Gap(4).Padding(12).Radius(10).Background(k.hover).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.HostsFingerprint()).FontSize(11.5).FontWeight(600).TextColor(k.textFaint)
				ui.Text(c, page.fingerprint).Font(monoFamily()).FontSize(22).FontWeight(700).TextColor(k.text).
					Label(locales.RemoteFingerprint(page.fingerprint))
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(7, 7).Radius(4).Background(k.attention)
				ui.Text(c, locales.HostsWaiting()).FontSize(12.5).FontWeight(500).TextColor(k.textMuted)
			})
			ui.Text(c, locales.HostsApproveWhere()).FontSize(12).TextColor(k.textFaint).LineHeight(1.3)
			if page.note != nil {
				ui.Text(c, page.note()).FontSize(12).TextColor(k.danger).LineHeight(1.3)
			}
		default:
			ui.Text(c, locales.HostsAddNote()).FontSize(12).TextColor(k.textFaint).LineHeight(1.3)
			field := func(label string, input func() ui.Element) {
				ui.Column(c).Gap(4).Children(func() {
					ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(k.textMuted)
					if input().Changed() {
						page.formErr = nil
					}
				})
			}
			field(locales.HostsAddress(), func() ui.Element {
				return ui.TextInput(c.Key("address"), &page.address).Password().Font(monoFamily()).
					Placeholder(locales.HostsAddressPlaceholder()).Label(locales.HostsAddress()).AutoFocus()
			})
			field(locales.HostsName(), func() ui.Element {
				return ui.TextInput(c.Key("name"), &page.name).Placeholder(locales.HostsNamePlaceholder()).Label(locales.HostsName())
			})
			field(locales.HostsPairAs(), func() ui.Element {
				return ui.TextInput(c.Key("pair-as"), &page.pairAs).Label(locales.HostsPairAs())
			})
			ui.Text(c, locales.HostsPairAsHint()).FontSize(12).TextColor(k.textFaint).Margin(-6, 0, 0, 0)
			if page.formErr != nil {
				ui.Text(c, page.formErr()).FontSize(12).TextColor(k.danger).LineHeight(1.3)
			}
		}
		ui.Row(c).Gap(8).Justify(ui.End).Padding(4, 0, 0, 0).Children(func() {
			if ui.Button(c.Key("cancel"), locales.RemoteCancel()).Clicked() {
				closeAddHost()
			}
			if page.stage == addForm && ui.Button(c.Key("add"), locales.RemoteAdd()).Clicked() {
				a.submitHost()
			}
		})
	})
	// Dismissed by Escape or the backdrop: stop trying.
	if was && !page.adding {
		closeAddHost()
	}
}
