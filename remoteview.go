package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"gorex/internal/coordproto"
	"gorex/internal/rex"
	"gorex/internal/rexapi"
	"gorex/locales"
)

// The Remote page of the settings window: the server's remote access, as
// the server tells it. Every call to the server runs off the UI thread.

// remoteUI is the page's state; the main thread owns it.
var remoteUI struct {
	stop        chan struct{} // closed to stop the refresh; non-nil while it runs
	st          rex.RemoteStatus
	loaded      bool          // st is what the server said
	unreachable bool          // the last ask failed
	myKey       string        // this machine's public key
	busy        bool          // a change is on its way
	err         func() string // what went wrong with the last change, said when drawn
	// reveal is that the address is shown. The address is a secret: st
	// holds it only while reveal is on, and the page asks for it again
	// to copy it.
	reveal    bool
	confirm   bool   // regenerate asks to be sure
	copied    string // what was copied just now
	name, key string // the client to add
	addErr    bool   // the client to add is not valid: its reason shows
	// The network to join, as typed: its URL, key and the name this
	// machine joins by.
	netURL, netKey, netName string
	netErr                  bool // what was typed does not read
	// lan is the gateway's listener on the local network, as its files
	// say (rexapi.LoadLAN, LoadLANStatus); lanListen and lanAdd are what
	// is typed in its fields, and lanErr what was wrong with the last.
	lan       rexapi.LANConfig
	lanStatus rexapi.LANStatus
	lanListen string
	lanEdited bool
	lanAdd    string
	lanErr    string
	// addingKey and joining show the forms that add a client by its key
	// and join a network, in place of the buttons that open them.
	addingKey, joining bool
}

// showSection chooses a settings section. The Remote page asks the server
// every two seconds while it shows, and drops the address when it goes.
func (a *App) showSection(id string) {
	if id != "keyboard" && shortcutsUI.recording != nil {
		a.stopRecording()
	}
	settingsUI.section = id
	if id == "remote" {
		a.startRemotePoll()
	} else {
		stopRemotePoll()
	}
}

func stopRemotePoll() {
	if remoteUI.stop != nil {
		close(remoteUI.stop)
	}
	page := &remoteUI
	page.stop, page.st, page.loaded, page.busy = nil, rex.RemoteStatus{}, false, false
	page.reveal, page.confirm, page.err, page.addErr, page.name, page.key = false, false, nil, false, "", ""
	page.netURL, page.netKey, page.netName, page.netErr = "", "", "", false
	page.addingKey, page.joining = false, false
}

// remoteAsk runs work off the UI thread, with the app's client of the
// moment, which a restarted server replaces; the function work returns
// runs on the UI thread afterwards, unless the page has gone. It reports
// whether work was started. Call it from the UI thread.
func (a *App) remoteAsk(work func(srv *rex.Client) (apply func())) bool {
	win, srv, stop := settingsUI.win, a.client, remoteUI.stop
	if win == nil || srv == nil || stop == nil {
		return false
	}
	go func() {
		apply := work(srv)
		win.Update(func() {
			select {
			case <-stop:
				return // the page is gone
			default:
			}
			if apply != nil {
				apply()
			}
		})
	}()
	return true
}

func (a *App) startRemotePoll() {
	win := settingsUI.win
	if remoteUI.stop != nil || win == nil {
		return
	}
	stop := make(chan struct{})
	remoteUI.stop = stop
	go func() {
		pub := rex.ClientPublicKey()
		win.Update(func() { remoteUI.myKey = pub })
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			win.Update(a.fetchRemote)
			select {
			case <-stop:
				return
			case <-t.C:
			}
		}
	}()
}

// fetchLAN reads the local network listener's settings and state, which
// the server's gateway follows.
func fetchLAN() {
	dir := rexapi.TailnetDir()
	if c, err := rexapi.LoadLAN(dir); err == nil {
		remoteUI.lan = c
		if !remoteUI.lanEdited {
			remoteUI.lanListen = c.Listen
		}
	}
	remoteUI.lanStatus = rexapi.LoadLANStatus(dir)
}

// saveLAN writes the listener's settings, which the server's gateway
// applies within a second.
func saveLAN(c rexapi.LANConfig) bool {
	if err := rexapi.SaveLAN(rexapi.TailnetDir(), c); err != nil {
		remoteUI.lanErr = err.Error()
		return false
	}
	remoteUI.lanErr = ""
	remoteUI.lan = c
	return true
}

// remoteLAN is the group of Rex's apps on the local network: the switch,
// its state, the address and the networks let in.
func (a *App) remoteLAN(c *ui.Context, k *colors) {
	page := &remoteUI
	conf, st := page.lan, page.lanStatus
	settingsGroup(c, k, locales.RemoteLanTitle(), "", locales.RemoteLanNote(), func() {
		label, dot := locales.RemoteLanOff(), k.exited
		switch {
		case st.Listening != "":
			label, dot = locales.RemoteLanListening("http://"+st.Listening), k.busy
		case conf.Enabled && st.Error != "":
			label, dot = locales.RemoteLanError(st.Error), k.danger
		case conf.Enabled:
			label, dot = locales.RemoteLanWaiting(), k.attention
		}
		on := conf.Enabled
		groupRow(c, k, nil).Children(func() {
			tileLine(c, k, "app-window", locales.RemoteLanSwitch(), func() {
				statusLine(c, k, dot, label).Selectable()
			}, func() {
				if ui.Switch(c.Key("lan"), &on).Label(locales.RemoteLanSwitch()).Changed() {
					next := conf
					next.Enabled = on
					if on {
						next = next.WithDefaults()
						if next.Listen == "" {
							page.lanErr = locales.RemoteLanNoAddress()
							return
						}
						page.lanListen, page.lanEdited = next.Listen, false
					}
					saveLAN(next)
				}
			})
			// Not in a dimmed row: its color is its meaning.
			if page.lanErr != "" {
				rowError(c, k, page.lanErr)
			}
		})
		listen := groupRow(c, k, nil)
		if !conf.Enabled {
			dimmed(listen, 0.5)
		}
		listen.Children(func() {
			rowLine(c, k, locales.RemoteLanListenOn(), func() {
				if ui.TextInput(c.Key("lan-listen"), &page.lanListen).Placeholder("192.168.1.20:7297").
					Label(locales.RemoteLanAddress()).Font(monoFamily()).Width(200).Changed() {
					page.lanEdited = true
				}
				if ui.Button(c.Key("lan-save"), locales.RemoteLanSave()).Disabled(!page.lanEdited).Clicked() {
					next := conf
					next.Listen = strings.TrimSpace(page.lanListen)
					if saveLAN(next) {
						page.lanEdited = false
					}
				}
			})
		})
		nets := groupRow(c, k, nil).Gap(8)
		if !conf.Enabled {
			dimmed(nets, 0.5)
		}
		nets.Children(func() {
			ui.Text(c, locales.RemoteLanNetworks()).FontSize(13).FontWeight(500).TextColor(k.text)
			if len(conf.Allow) == 0 {
				rowHint(c, k, locales.RemoteLanNoNetworks())
			}
			for _, n := range conf.Allow {
				ui.Row(c.Key("net-" + n)).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, n).Font(monoFamily()).FontSize(12.5).TextColor(k.text).Grow(1).MinWidth(0)
					if ui.Button(c.Key("rm"), locales.RemoteLanRemove()).Clicked() {
						next := conf
						next.Allow = slices.DeleteFunc(slices.Clone(conf.Allow), func(x string) bool { return x == n })
						saveLAN(next)
					}
				})
			}
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c.Key("lan-add"), &page.lanAdd).Placeholder(locales.RemoteLanAddPlaceholder()).Grow(1)
				if ui.Button(c.Key("lan-add-btn"), locales.RemoteLanAdd()).Disabled(strings.TrimSpace(page.lanAdd) == "").Clicked() {
					p, err := rexapi.ParseNetwork(page.lanAdd)
					if err != nil {
						page.lanErr = err.Error()
						return
					}
					next := conf
					if !slices.Contains(next.Allow, p.String()) {
						next.Allow = append(slices.Clone(next.Allow), p.String())
					}
					if saveLAN(next) {
						page.lanAdd = ""
					}
				}
			})
		})
	})
}

// fetchRemote asks the server for its status; the address comes only
// while it is shown.
func (a *App) fetchRemote() {
	fetchLAN()
	withAddress := remoteUI.reveal
	a.remoteAsk(func(srv *rex.Client) func() {
		st, err := srv.RemoteStatus()
		if !withAddress {
			st.Address = ""
		}
		return func() {
			remoteUI.unreachable = err != nil
			if err == nil {
				if !remoteUI.reveal {
					st.Address = "" // hidden since the ask
				}
				remoteUI.st, remoteUI.loaded = st, true
			}
		}
	})
}

// remoteDo makes a change on the server, then asks for the status; ok runs
// on the UI thread when it worked.
func (a *App) remoteDo(change func(srv *rex.Client) error, ok func()) {
	if remoteUI.busy {
		return
	}
	remoteUI.busy, remoteUI.err = true, nil
	started := a.remoteAsk(func(srv *rex.Client) func() {
		err := change(srv)
		return func() {
			remoteUI.busy = false
			if err != nil {
				msg := serverMessage(err)
				remoteUI.err = func() string { return msg }
			} else if ok != nil {
				ok()
			}
			a.fetchRemote()
		}
	})
	if !started {
		remoteUI.busy, remoteUI.err = false, locales.RemoteNotConnected
	}
}

// serverMessage is an error of the server without its package prefix.
func serverMessage(err error) string {
	s := strings.TrimPrefix(err.Error(), "rex: ")
	if s == "" {
		return locales.RemoteSomethingWrong()
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

type remoteKind int

const (
	remoteOff remoteKind = iota
	remoteStarting
	remoteListening
	remoteError
	remoteDown
)

// remoteState tells what the status line says: its label, the detail
// under it and its kind. unreachable is that the server did not answer.
func remoteState(st rex.RemoteStatus, unreachable bool) (label, detail string, kind remoteKind) {
	switch {
	case unreachable:
		return locales.RemoteUnreachable(), locales.RemoteUnreachableHint(), remoteDown
	case st.Running:
		return locales.RemoteListening(), "", remoteListening
	case st.LastError != "" && st.Enabled:
		return locales.RemoteError(), locales.RemoteErrorRetry(st.LastError), remoteError
	case st.LastError != "":
		return locales.RemoteError(), st.LastError, remoteError
	case st.Enabled:
		return locales.RemoteStarting(), "", remoteStarting
	}
	return locales.RemoteOff(), "", remoteOff
}

// validateClientKey returns what is wrong with a public key, "" when the
// server takes it.
func validateClientKey(s string) string {
	if strings.TrimSpace(s) == "" {
		return locales.RemotePasteKey()
	}
	if _, err := rex.NormalizePublicKey(s); err != nil {
		return locales.RemoteBadKey()
	}
	return ""
}

// validateNewClient returns what is wrong with a client to add, "" if none.
func validateNewClient(name, pub string, allowed []rex.RemoteClient) string {
	if strings.TrimSpace(name) == "" {
		return locales.RemoteNameClient()
	}
	if m := validateClientKey(pub); m != "" {
		return m
	}
	want, _ := rex.NormalizePublicKey(pub)
	for _, c := range allowed {
		if have, _ := rex.NormalizePublicKey(c.PublicKey); have == want {
			return locales.RemoteAlreadyAllowed(c.Name)
		}
	}
	return ""
}

// shortKey shortens a public key to its first and last digits.
func shortKey(k string) string {
	prefix, digits, ok := strings.Cut(strings.TrimSpace(k), ":")
	if !ok || len(digits) < 16 {
		return k
	}
	return prefix + ":" + digits[:6] + "…" + digits[len(digits)-6:]
}

// maskedAddress is the dots in place of a hidden address.
var maskedAddress = strings.Repeat("•", 28)

// monoFamily is the font of keys and addresses: the terminal's.
func monoFamily() string {
	if len(ghostty.Families) > 0 {
		return ghostty.Families[0]
	}
	return "JetBrains Mono"
}

// copyText copies s, and says so on the button named by what for a while.
// It does not clear the clipboard afterwards.
func copyText(what, s string) {
	if err := mygo.Clipboard.Write(transfer.TextData(s)); err != nil {
		msg := err.Error()
		remoteUI.err = func() string { return locales.RemoteCopyFailed(msg) }
		return
	}
	remoteUI.copied = what
	win := settingsUI.win
	go func() {
		time.Sleep(1500 * time.Millisecond)
		win.Update(func() {
			if remoteUI.copied == what {
				remoteUI.copied = ""
			}
		})
	}()
}

// copyLabel is a copy button's label.
func copyLabel(what string) string {
	if remoteUI.copied == what {
		return locales.RemoteCopied()
	}
	return locales.RemoteCopy()
}

// copyAddress asks the server for the address and copies it, keeping it
// nowhere.
func (a *App) copyAddress() {
	a.remoteAsk(func(srv *rex.Client) func() {
		st, err := srv.RemoteStatus()
		return func() {
			if err != nil || st.Address == "" {
				remoteUI.err = locales.RemoteNoAddress
				return
			}
			copyText("addr", st.Address)
		}
	})
}

// rowError says what went wrong, under a row's first line.
func rowError(c *ui.Context, k *colors, msg string) {
	ui.Text(c, msg).FontSize(11.5).TextColor(k.danger).LineHeight(1.4).MaxWidth(settingsHintW)
}

// remoteTile is the small grey tile of a row of the Remote page, with
// its glyph.
func remoteTile(c *ui.Context, k *colors, glyph string) {
	ui.Box(c).Size(30, 30).Radius(8).Background(k.hover).Center().Shrink(0).Children(func() {
		glyphIcon(c, glyph, 16, k.iconMuted)
	})
}

// statusLine is a dot of a state's color and its label.
func statusLine(c *ui.Context, k *colors, dot ui.Color, label string) ui.Element {
	var text ui.Element
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(7, 7).Radius(4).Background(dot).Shrink(0)
		text = ui.Text(c, label).FontSize(11.5).FontWeight(500).TextColor(k.textMuted).MinWidth(0)
	})
	return text
}

// tileLine is a row's first line with a tile: the tile, the title and what
// under shows below it, and the controls at the right. under and control
// may be nil.
func tileLine(c *ui.Context, k *colors, glyph, title string, under, control func()) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		remoteTile(c, k, glyph)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(13).FontWeight(500).TextColor(k.text).SingleLine().Ellipsis("…")
			if under != nil {
				under()
			}
		})
		if control != nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(control)
		}
	})
}

// remoteSwitch is a row of the Remote page's first group: a switch of the
// server's, its title, and its hint under them; dim shows it inactive.
func remoteSwitch(c *ui.Context, k *colors, key, title, hint string, on *bool, disabled, dim bool) bool {
	changed := false
	row := groupRow(c, k, nil)
	if dim {
		dimmed(row, 0.5)
	}
	row.Children(func() {
		rowLine(c, k, title, func() {
			changed = ui.Switch(c.Key(key), on).Label(title).Disabled(disabled).Changed()
		})
		rowHint(c, k, hint)
	})
	return changed
}

func (a *App) remoteSettings(c *ui.Context, k *colors) {
	page := &remoteUI
	if !page.loaded {
		label := locales.RemoteAsking()
		if page.unreachable {
			label, _, _ = remoteState(rex.RemoteStatus{}, true)
		}
		ui.Text(c, label).FontSize(12.5).TextColor(k.textFaint).Padding(0, 0, 0, 14)
		return
	}
	st := page.st
	a.remoteHeader(c, k, st)
	a.remoteDevices(c, k, st)
	a.remoteThisMachine(c, k, st)
	a.remoteNetwork(c, k, st)
	a.remoteLAN(c, k)
}

// remoteHeader is the Remote page's first group: remote access, its
// state and its switch, and what the clients share.
func (a *App) remoteHeader(c *ui.Context, k *colors, st rex.RemoteStatus) {
	page := &remoteUI
	label, detail, kind := remoteState(st, page.unreachable)
	dot := map[remoteKind]ui.Color{
		remoteOff: k.exited, remoteStarting: k.attention, remoteListening: k.busy, remoteError: k.danger, remoteDown: k.danger,
	}[kind]
	settingsGroup(c, k, "", "", "", func() {
		// The switch and the state.
		on := st.Enabled && !page.unreachable
		groupRow(c, k, nil).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(40, 40).Radius(11).Background(k.accent).Center().Shrink(0).Children(func() {
					glyphIcon(c, "globe", 22, ui.RGB(255, 255, 255))
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					ui.Text(c, locales.TailcatMode()).FontSize(15).FontWeight(600).TextColor(k.text)
					statusLine(c, k, dot, label)
				})
				sw := ui.Switch(c.Key("allow"), &on).Label(locales.TailcatMode()).Disabled(page.unreachable || page.busy)
				if sw.Changed() {
					if on {
						a.remoteDo((*rex.Client).EnableRemote, nil)
					} else {
						a.remoteDo((*rex.Client).DisableRemote, nil)
					}
				}
			})
			rowHint(c, k, detail)
			if page.err != nil {
				rowError(c, k, page.err())
			}
		})
		if st.Tailnet != nil {
			tailnet := st.Tailnet.Enabled
			if remoteSwitch(c, k, "system-tailscale", locales.TailnetMode(), locales.TailnetModeNote(), &tailnet, page.unreachable || page.busy, false) {
				a.remoteDo(func(srv *rex.Client) error { return srv.SetTailnet(tailnet) }, nil)
			}
			if st.Tailnet.Enabled && !st.Tailnet.Running {
				groupRow(c, k, nil).Children(func() { rowHint(c, k, locales.TailnetUnavailable()) })
			}
		}
		// Whether remote clients see the sessions open here.
		share := st.ShareSessions
		if remoteSwitch(c, k, "share", locales.RemoteShare(), locales.RemoteShareNote(), &share, page.unreachable || page.busy, !st.Enabled && (st.Tailnet == nil || !st.Tailnet.Running)) {
			a.remoteDo(func(srv *rex.Client) error { return srv.ShareSessions(share) }, nil)
		}
		// Whether remote clients read the layout and follow it.
		layoutShare := st.ShareLayout
		if remoteSwitch(c, k, "share-layout", locales.RemoteShareLayout(), locales.RemoteShareLayoutNote(), &layoutShare, page.unreachable || page.busy, !st.Enabled && (st.Tailnet == nil || !st.Tailnet.Running)) {
			a.remoteDo(func(srv *rex.Client) error { return srv.ShareLayout(layoutShare) }, nil)
		}
	})
}

// pairingLeft is the time left in the pairing window, as m:ss.
func pairingLeft(until, now time.Time) string {
	d := max(until.Sub(now).Round(time.Second), 0)
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

// remoteDevices is the group of the devices: the pairing window and the
// requests made in it to approve or reject, the clients allowed, and the
// buttons that pair a new device or add one by its key.
func (a *App) remoteDevices(c *ui.Context, k *colors, st rex.RemoteStatus) {
	page := &remoteUI
	settingsGroup(c, k, locales.RemoteDevices(), locales.RemoteClientsNote(), locales.RemotePairNote(), func() {
		if st.Enabled && st.Pairing.Open {
			groupRow(c, k, nil).Children(func() {
				tileLine(c, k, "plus", locales.RemotePairTitle(), func() {
					statusLine(c, k, k.attention, locales.RemotePairWaiting(pairingLeft(st.Pairing.ExpiresAt, time.Now())))
				}, func() {
					if ui.Button(c.Key("pair-stop"), locales.RemotePairStop()).Disabled(page.busy).Clicked() {
						a.remoteDo((*rex.Client).StopPairing, nil)
					}
				})
				if len(st.Pending) == 0 {
					rowHint(c, k, locales.RemotePairNoRequest())
				}
			})
		}
		for _, p := range st.Pending {
			groupRow(c, k, "pending:"+p.PublicKey).Children(func() {
				tileLine(c, k, "laptop", p.Name, func() {
					ui.Text(c, rex.Fingerprint(p.PublicKey)).Font(monoFamily()).FontSize(11.5).TextColor(k.textMuted).SingleLine().
						Ellipsis("…").Tooltip(p.PublicKey)
				}, func() {
					if ui.Button(c.Key("r"), locales.RemoteReject()).Disabled(page.busy).Clicked() {
						a.remoteDo(func(srv *rex.Client) error { return srv.RejectPairing(p.PublicKey) }, nil)
					}
					if ui.PrimaryButton(c.Key("a"), locales.RemoteApprove()).Disabled(page.busy).Clicked() {
						a.remoteDo(func(srv *rex.Client) error { return srv.ApprovePairing(p.PublicKey) }, nil)
					}
				})
			})
		}
		if len(st.Allowed) == 0 {
			groupRow(c, k, nil).Children(func() {
				ui.Text(c, locales.RemoteNoClients()).FontSize(13).TextColor(k.textFaint)
			})
		}
		for _, client := range st.Allowed {
			row := groupRow(c, k, "client:"+client.PublicKey)
			if !st.Enabled {
				dimmed(row, 0.5)
			}
			row.Children(func() {
				tileLine(c, k, "laptop", client.Name, func() {
					ui.Text(c, shortKey(client.PublicKey)).Font(monoFamily()).FontSize(11.5).TextColor(k.textFaint).SingleLine().
						Tooltip(client.PublicKey)
				}, func() {
					if iconButton(c.Key("c"), k, "copy", locales.RemoteCopyKey(), 26, 15).Tooltip(locales.RemoteCopyKeyTip()).Clicked() {
						copyText(client.PublicKey, client.PublicKey)
					}
					if ui.Button(c.Key("r"), locales.RemoteRevoke()).Disabled(page.busy).Clicked() {
						a.remoteDo(func(srv *rex.Client) error { return srv.RevokeRemote(client.PublicKey) }, nil)
					}
				})
				// The client's shares, on a line of their own under its name,
				// so that a narrow window keeps the name.
				ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(4, 0, 0, 42).Children(func() {
					ui.Text(c, locales.RemoteClientShare()).FontSize(12).TextColor(k.textMuted).Shrink(0)
					share := client.ShareSessions
					sw := ui.Switch(c.Key("s"), &share).Label(locales.RemoteClientShare()).Disabled(page.busy)
					if sw.Changed() {
						a.remoteDo(func(srv *rex.Client) error { return srv.ShareClientSessions(client.PublicKey, share) }, nil)
					}
					ui.Text(c, locales.RemoteClientLayout()).FontSize(12).TextColor(k.textMuted).Shrink(0).Margin(0, 0, 0, 6)
					layoutShare := client.ShareLayout
					lsw := ui.Switch(c.Key("l"), &layoutShare).Label(locales.RemoteClientLayout()).Disabled(page.busy)
					if lsw.Changed() {
						a.remoteDo(func(srv *rex.Client) error { return srv.ShareClientLayout(client.PublicKey, layoutShare) }, nil)
					}
				})
			})
		}
		if page.addingKey {
			a.addByKey(c, k, st)
			return
		}
		groupRow(c, k, nil).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if !st.Enabled {
					ui.Text(c, locales.RemotePairTurnOn()).FontSize(13).TextColor(k.textFaint).Grow(1).MinWidth(0)
				} else {
					ui.Spacer(c)
				}
				if ui.Button(c.Key("add-key"), locales.RemoteAddByKeyAsk()).Clicked() {
					page.addingKey = true
				}
				if st.Enabled && !st.Pairing.Open {
					if ui.PrimaryButton(c.Key("pair-start"), locales.RemotePairNew()).Disabled(page.busy).Clicked() {
						a.remoteDo((*rex.Client).StartPairing, nil)
					}
				}
			})
		})
	})
}

// addByKey is the row that allows a client by its key: its name and key,
// Add, and Cancel, which hides it.
func (a *App) addByKey(c *ui.Context, k *colors, st rex.RemoteStatus) {
	page := &remoteUI
	groupRow(c, k, nil).Gap(8).Children(func() {
		ui.Text(c, locales.RemoteAddByKey()).FontSize(13).FontWeight(500).TextColor(k.text)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.TextInput(c.Key("name"), &page.name).Placeholder(locales.RemoteNamePlaceholder()).Label(locales.RemoteNameLabel()).Width(150).Shrink(0).Changed() {
				page.addErr = false
			}
			if ui.TextInput(c.Key("key"), &page.key).Placeholder("nodekey:…").Label(locales.RemoteKeyLabel()).Grow(1).MinWidth(0).Changed() {
				page.addErr = false
			}
			if ui.Button(c.Key("add-cancel"), locales.RemoteCancel()).Clicked() {
				page.addingKey, page.addErr = false, false
			}
			if ui.Button(c, locales.RemoteAdd()).Disabled(page.busy).Clicked() {
				if page.addErr = validateNewClient(page.name, page.key, st.Allowed) != ""; !page.addErr {
					name, pub := strings.TrimSpace(page.name), strings.TrimSpace(page.key)
					a.remoteDo(func(srv *rex.Client) error { return srv.AllowRemote(name, pub) },
						func() { page.name, page.key = "", "" })
				}
			}
		})
		if page.addErr {
			rowError(c, k, validateNewClient(page.name, page.key, st.Allowed))
		}
	})
}

// remoteThisMachine is the group of this machine: the address remote
// clients connect to, while the server listens, and its key.
func (a *App) remoteThisMachine(c *ui.Context, k *colors, st rex.RemoteStatus) {
	page := &remoteUI
	foot := ""
	if st.Running {
		foot = locales.RemoteAddressNote()
	}
	settingsGroup(c, k, thisMachine(), "", foot, func() {
		if st.Running {
			a.remoteAddress(c, k)
		}
		groupRow(c, k, nil).Children(func() {
			ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, locales.RemoteMyKey()).FontSize(13).FontWeight(500).TextColor(k.text)
					if page.myKey == "" {
						ui.Text(c, locales.RemoteNoKey()).FontSize(11.5).TextColor(k.textFaint)
						return
					}
					if fp := rex.Fingerprint(page.myKey); fp != "" {
						ui.Text(c, locales.RemoteFingerprint(fp)).Font(monoFamily()).FontSize(12).TextColor(k.textMuted)
					}
					ui.Text(c, page.myKey).Font(monoFamily()).FontSize(10.5).TextColor(k.textFaint).LineHeight(1.4).Selectable()
				})
				if ui.Button(c.Key("copy-mine"), copyLabel("mine")).Disabled(page.myKey == "").Clicked() {
					copyText("mine", page.myKey)
				}
			})
			rowHint(c, k, locales.RemoteMyKeyNote())
		})
	})
}

// remoteAddress is the address row: the address, hidden until shown, and
// the buttons to show, copy and regenerate it.
func (a *App) remoteAddress(c *ui.Context, k *colors) {
	page := &remoteUI
	groupRow(c, k, nil).Gap(8).Children(func() {
		shown := maskedAddress
		if page.reveal && page.st.Address != "" {
			shown = page.st.Address
		}
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, locales.RemoteAddress()).FontSize(13).FontWeight(500).TextColor(k.text)
				ui.Text(c, shown).Font(monoFamily()).FontSize(12).TextColor(k.textMuted).LineHeight(1.4)
			})
			if page.confirm {
				return
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
				if page.reveal {
					if ui.Button(c, locales.RemoteHide()).Clicked() {
						page.reveal, page.st.Address = false, ""
					}
				} else if ui.Button(c, locales.RemoteShow()).Clicked() {
					page.reveal = true
					a.fetchRemote()
				}
				if ui.Button(c.Key("copy-addr"), copyLabel("addr")).Clicked() {
					a.copyAddress()
				}
				if ui.Button(c, locales.RemoteRegenerateAsk()).Clicked() {
					page.confirm = true
				}
			})
		})
		if !page.confirm {
			return
		}
		ui.Text(c, locales.RemoteRegenerateNote()).
			FontSize(12).TextColor(k.text).LineHeight(1.4).MaxWidth(settingsHintW)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, locales.RemoteRegenerate()).Disabled(page.busy).Clicked() {
				page.confirm, page.reveal, page.st.Address = false, false, ""
				a.remoteDo((*rex.Client).RegenerateRemote, nil)
			}
			if ui.Button(c, locales.RemoteCancel()).Clicked() {
				page.confirm = false
			}
		})
	})
}

// validateNetwork returns what is wrong with a network to join, "" if
// nothing: the join URL reads, the key and the name are there.
func validateNetwork(url, key, name string) string {
	if strings.TrimSpace(url) == "" {
		return locales.RemoteNetURL()
	}
	if _, _, err := coordproto.SplitJoinURL(url); err != nil {
		return locales.RemoteNetBadURL()
	}
	if strings.TrimSpace(key) == "" {
		return locales.RemoteNetKey()
	}
	if strings.TrimSpace(name) == "" {
		return locales.RemoteNetName()
	}
	return ""
}

// remoteNetwork is the group of the coordinator network: the network
// joined and its members, or the button that shows the fields to join
// one.
func (a *App) remoteNetwork(c *ui.Context, k *colors, st rex.RemoteStatus) {
	page := &remoteUI
	settingsGroup(c, k, locales.RemoteNetGroup(), "", locales.RemoteNetNote(), func() {
		if st.Network == nil {
			if st.Enabled && page.joining {
				a.joinNetwork(c, k)
				return
			}
			groupRow(c, k, nil).Children(func() {
				var under, control func()
				if !st.Enabled {
					under = func() { ui.Text(c, locales.RemoteNetTurnOn()).FontSize(11.5).TextColor(k.textFaint).LineHeight(1.4) }
				} else {
					control = func() {
						if ui.Button(c.Key("netjoin-ask"), locales.RemoteNetJoinAsk()).Clicked() {
							page.joining = true
						}
					}
				}
				tileLine(c, k, "globe", locales.RemoteNetNone(), under, control)
			})
			return
		}
		net := st.Network
		groupRow(c, k, nil).Children(func() {
			tileLine(c, k, "globe", locales.RemoteNetJoined(net.Name), func() {
				ui.Text(c, locales.RemoteNetCoordinator(net.Coordinator)).FontSize(11.5).TextColor(k.textFaint).SingleLine().Ellipsis("…")
			}, func() {
				if ui.Button(c.Key("netleave"), locales.RemoteNetLeave()).Disabled(page.busy).Clicked() {
					a.remoteDo((*rex.Client).LeaveNetwork, nil)
				}
			})
			if net.LastError != "" {
				rowError(c, k, locales.RemoteNetError(net.LastError))
			}
		})
		if len(net.Members) == 0 {
			groupRow(c, k, nil).Children(func() {
				ui.Text(c, locales.RemoteNetNoMembers()).FontSize(13).TextColor(k.textFaint)
			})
		}
		for _, m := range net.Members {
			groupRow(c, k, m.PublicKey).Children(func() {
				tileLine(c, k, "laptop", m.Name, func() {
					ui.Text(c, rex.Fingerprint(m.PublicKey)).Font(monoFamily()).FontSize(11.5).TextColor(k.textMuted).SingleLine().
						Ellipsis("…").Tooltip(m.PublicKey)
				}, func() {
					if ui.Button(c.Key("r"), locales.RemoteNetKick()).Disabled(page.busy).Clicked() {
						a.remoteDo(func(srv *rex.Client) error { return srv.KickNetworkMember(m.Name, m.PublicKey) }, nil)
					}
				})
			})
		}
	})
}

// joinNetwork is the row of the fields to join a network: its URL, its key
// and this machine's name in it, Join, and Cancel, which hides them.
func (a *App) joinNetwork(c *ui.Context, k *colors) {
	page := &remoteUI
	groupRow(c, k, nil).Gap(8).Children(func() {
		if page.netName == "" {
			page.netName = a.localName()
		}
		if ti := ui.TextInput(c.Key("neturl"), &page.netURL).Placeholder("https://host/0123456789abcdef").
			Label(locales.RemoteNetURLLabel()); ti.Changed() {
			page.netErr = false
		}
		if ti := ui.TextInput(c.Key("netkey"), &page.netKey).Placeholder(locales.RemoteNetKeyPlaceholder()).
			Label(locales.RemoteNetKeyLabel()); ti.Changed() {
			page.netErr = false
		}
		if ti := ui.TextInput(c.Key("netname"), &page.netName).Placeholder("yibo-macbook").
			Label(locales.RemoteNetNameLabel()); ti.Changed() {
			page.netErr = false
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c.Key("netjoin-cancel"), locales.RemoteCancel()).Clicked() {
				page.joining, page.netErr = false, false
			}
			if ui.PrimaryButton(c.Key("netjoin"), locales.RemoteNetJoin()).Disabled(page.busy).Clicked() {
				if msg := validateNetwork(page.netURL, page.netKey, page.netName); msg != "" {
					page.netErr = true
					remoteUI.err = func() string { return msg }
				} else {
					url, key := strings.TrimSpace(page.netURL), strings.TrimSpace(page.netKey)
					name := page.netName
					a.remoteDo(func(srv *rex.Client) error { return srv.JoinNetwork(url, key, name) },
						func() { page.netURL, page.netKey, page.netErr, page.joining = "", "", false, false })
				}
			}
		})
		if page.netErr {
			rowError(c, k, validateNetwork(page.netURL, page.netKey, page.netName))
		}
	})
}
