package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/internal/rexapi"
	"gorex/internal/tailnode"
	"gorex/locales"
)

// The Remote page of the settings window: the server's remote access, as
// the server tells it, inbound only; the hosts this machine connects to
// have a page of their own.
// Every call to the server runs off the UI thread.

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
	reveal bool
	// revealDirect is the same for the second address, of Reachable at.
	revealDirect bool
	confirm      bool   // regenerate asks to be sure
	copied       string // what was copied just now
	name, key    string // the client to add
	addErr       bool   // the client to add is not valid: its reason shows
	// lan is the gateway's listener on the local network, as its files
	// say (rexapi.LoadLAN, LoadLANStatus); lanListen and lanAdd are what
	// is typed in its fields, and lanErr what was wrong with the last.
	lan       rexapi.LANConfig
	lanStatus rexapi.LANStatus
	lanListen string
	lanEdited bool
	lanAdd    string
	lanErr    string
	focusLAN  bool
	// ts is the gateway's built-in Tailscale settings, as its files say
	// (rexapi.LoadTailscale); tsURL is what is typed in its field, and
	// tsErr what was wrong with the last save.
	ts       rexapi.TailscaleConfig
	tsURL    string
	tsEdited bool
	tsErr    string
	// relay is what is typed in the relay field, relayErr what was wrong
	// with it.
	relay       string
	relayEdited bool
	relayErr    string
	// reach is what is typed in the Reachable at field, reachErr what was
	// wrong with it.
	reach       string
	reachEdited bool
	reachErr    string
	// fwFailed tells that Allow did not change Windows Firewall.
	fwFailed bool
	// addingKey shows the form that adds a client by its key, in place
	// of the button that opens it.
	addingKey bool
	// details shows this machine's key and address under the devices.
	details bool
}

// showSection chooses a settings section and refreshes its Rex listener
// status while shown.
func (a *App) showSection(id string) {
	if id != "keyboard" && shortcutsUI.recording != nil {
		a.stopRecording()
	}
	if settingsUI.section == id {
		return
	}
	stopRemotePoll()
	hostsPairQR = nil
	settingsUI.section = id
	stopServerInfoPoll()
	if id == "server" {
		fetchLAN()
		a.startRemotePoll()
		startServerInfoPoll()
	}
}

func stopRemotePoll() {
	if remoteUI.stop != nil {
		close(remoteUI.stop)
	}
	page := &remoteUI
	page.stop, page.st, page.loaded, page.busy = nil, rex.RemoteStatus{}, false, false
	page.reveal, page.confirm, page.err, page.addErr, page.name, page.key = false, false, nil, false, "", ""
	page.revealDirect = false
	page.addingKey, page.details = false, false
	page.focusLAN = false
	page.ts, page.tsURL, page.tsEdited, page.tsErr = rexapi.TailscaleConfig{}, "", false, ""
	page.relay, page.relayEdited, page.relayErr = "", false, ""
	page.reach, page.reachEdited, page.reachErr, page.fwFailed = "", false, "", false
}

// firewallAllow adds Windows Firewall's rules for Reachable at, as
// administrator; tests fake it.
var firewallAllow = rex.AllowFirewall

// remoteAsk runs work off the UI thread, with the app's client of the
// moment, which a restarted server replaces; the function work returns
// runs on the UI thread afterwards, unless the page has gone. It reports
// whether work was started. Call it from the UI thread.
func (a *App) remoteAsk(work func(srv *rex.Client) (apply func())) bool {
	win, srv, stop := settingsUI.win, a.client, remoteUI.stop
	if srv == nil || stop == nil {
		return false
	}
	update := a.post // no window in tests: the app's posts
	if win != nil {
		update = func(fn func()) { win.Update(fn) }
	}
	go func() {
		apply := work(srv)
		update(func() {
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
	refresh := a.fetchRemote
	go func() {
		pub := rex.ClientPublicKey()
		win.Update(func() { remoteUI.myKey = pub })
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			win.Update(func() {
				select {
				case <-stop:
					return
				default:
					refresh()
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

// fetchTailscale reads built-in Tailscale's settings, which the gateway
// keeps and follows when it next turns the device on.
func fetchTailscale() {
	if c, err := rexapi.LoadTailscale(rexapi.TailnetDir()); err == nil {
		remoteUI.ts = c
		if !remoteUI.tsEdited {
			remoteUI.tsURL = c.ControlURL
		}
	}
}

// fetchLAN reads the local network listener's settings and state, which
// the server's gateway follows.
func fetchLAN() {
	dir := rexapi.TailnetDir()
	c, _ := rexapi.LoadLAN(dir)
	remoteUI.lan = c
	if !remoteUI.lanEdited {
		remoteUI.lanListen = c.Listen
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

// localAPI is the server page's group of Rex's Local API: the gateway's
// revisions and version, the primary endpoint's URL to copy, each other
// listener's address and whom it lets in, then the listener of Rex's apps on the local network with its
// controls, which shows off too and is not listed again among the others.
func (a *App) localAPI(c *ui.Context, k *colors) {
	info, lan := settingsUI.apiInfo, remoteUI.lan
	settingsGroup(c, k, locales.RemoteApiTitle(), "", locales.RemoteApiNote(), func() {
		if settingsUI.apiErr != "" {
			apiRow(c, k, locales.RemoteApiUrl(), false, func() {
				ui.Text(c, locales.HostApiOff()).FontSize(11.5).TextColor(k.textFaint)
			}, nil)
		} else if settingsUI.apiLoaded {
			factRow(c, k, locales.HostApiTitle(), locales.HostApiRevisions(info.APIRevision, info.ServerRevision, info.MinRevision))
			factRow(c, k, locales.HostApiVersion(), info.RexVersion)
		}
		for _, l := range info.Listeners {
			if lanListener(l, lan) {
				continue
			}
			if l.Primary {
				apiRow(c, k, locales.RemoteApiUrl(), false, func() {
					ui.Text(c, l.Endpoint).Font(monoFamily()).FontSize(11.5).TextColor(k.textFaint).SingleLine().Ellipsis("…").
						Tooltip(l.Endpoint).Selectable()
				}, func() {
					if iconButton(c.Key("copy-api"), k, "copy", copyLabel("api"), 26, 15).Tooltip(copyLabel("api")).Clicked() {
						copyText("api", l.Endpoint)
					}
				})
				continue
			}
			addr := l.Endpoint
			if len(l.Addresses) > 0 {
				addr = l.Addresses[0]
			}
			label, open := admissionLabel(l)
			apiRow(c, k, addr, true, func() {
				switch {
				case open:
					statusLine(c, k, k.attention, label)
				case label != "":
					ui.Text(c, label).FontSize(11.5).TextColor(k.textFaint)
				}
			}, nil)
		}
		a.lanRow(c, k)
	})
}

// lanRow is the Local API group's row of Rex's apps on the local network:
// the switch and its state, the address it listens on and the networks
// let in.
func (a *App) lanRow(c *ui.Context, k *colors) {
	page := &remoteUI
	conf, st := page.lan, page.lanStatus
	label, dot := locales.RemoteLanOff(), k.exited
	switch {
	case conf.Enabled && st.Error != "":
		label, dot = locales.RemoteLanError(st.Error), k.danger
	case conf.Enabled && st.Listening != "":
		label, dot = locales.RemoteLanListening("http://"+st.Listening), k.busy
	case conf.Enabled:
		label, dot = locales.RemoteLanWaiting(), k.attention
	}
	on := conf.Enabled
	row := groupRow(c, k, "lan").Gap(10)
	if page.focusLAN {
		page.focusLAN = false
		row.ScrollIntoView()
	}
	row.Children(func() {
		apiLine(c, k, locales.RemoteLanSwitch(), false, func() {
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
		// Not dimmed: its color is its meaning.
		if page.lanErr != "" {
			rowError(c, k, page.lanErr)
		}
		if tailnet, lan := localRexPairs(); tailnet != "" || lan != "" {
			ui.Row(c).Children(func() {
				if ui.Button(c.Key("local-rex-qr"), locales.HostsRexQR()).Clicked() {
					a.openLocalPairQR()
				}
			})
		} else if conf.Enabled && st.Running && st.Error == "" && st.Listening != "" && len(conf.Allow) != 0 {
			rowHint(c, k, locales.RemoteLanPairAddress())
		}
		more := ui.Column(c).Gap(10)
		if !conf.Enabled {
			dimmed(more, 0.5)
		}
		more.Children(func() {
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
		rowHint(c, k, locales.RemoteLanNote())
	})
}

// apiRow is a row of the Local API group: its line, as apiLine draws it.
func apiRow(c *ui.Context, k *colors, title string, mono bool, under, control func()) {
	groupRow(c, k, title).Children(func() { apiLine(c, k, title, mono, under, control) })
}

// apiLine is a Local API row's first line: its title, in the terminal's
// font when mono, what under shows below it, and control at the right.
func apiLine(c *ui.Context, k *colors, title string, mono bool, under, control func()) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			t := ui.Text(c, title).FontSize(13).FontWeight(500).TextColor(k.text).SingleLine().Ellipsis("…")
			if mono {
				t.Font(monoFamily()).Selectable()
			}
			under()
		})
		if control != nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(control)
		}
	})
}

// lanListener is that l is the local network's listener, which lanRow
// shows. An anonymous one at its address while it is off is --listen's.
func lanListener(l rexapi.Listener, lan rexapi.LANConfig) bool {
	return l.Transport == "tcp" && l.Admission == rexapi.AdmissionAnonymous && lan.Enabled && l.Endpoint == lan.Listen
}

// admissionLabel says whom a listener lets in, "" when the gateway does
// not say, and a value it does not know as it is. open is an anonymous
// TCP listener: one that lets in anyone who reaches it.
func admissionLabel(l rexapi.Listener) (label string, open bool) {
	switch l.Admission {
	case rexapi.AdmissionPeer:
		return locales.RemoteApiPeer(), false
	case rexapi.AdmissionAnonymous:
		return locales.RemoteApiAnyone(), l.Transport == "tcp"
	}
	return l.Admission, false
}

// fetchRemote asks the server for its status; the address comes only
// while it is shown.
func (a *App) fetchRemote() {
	fetchLAN()
	fetchTailscale()
	withAddress, withDirect := remoteUI.reveal, remoteUI.revealDirect
	a.remoteAsk(func(srv *rex.Client) func() {
		st, err := srv.RemoteStatus()
		if !withAddress {
			st.Address = ""
		}
		if !withDirect {
			st.ReachableAddress = ""
		}
		return func() {
			remoteUI.unreachable = err != nil
			if err == nil {
				if !remoteUI.reveal {
					st.Address = "" // hidden since the ask
				}
				if !remoteUI.revealDirect {
					st.ReachableAddress = ""
				}
				remoteUI.st, remoteUI.loaded = st, true
				if !remoteUI.relayEdited {
					remoteUI.relay = st.Relay
				}
				if !remoteUI.reachEdited {
					remoteUI.reach = st.ReachableAt
				}
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

// copyAddress asks the server for the address, or the direct one, and
// copies it, keeping it nowhere.
func (a *App) copyAddress(direct bool) {
	a.remoteAsk(func(srv *rex.Client) func() {
		st, err := srv.RemoteStatus()
		addr, what := st.Address, "addr"
		if direct {
			addr, what = st.ReachableAddress, "direct"
		}
		return func() {
			if err != nil || addr == "" {
				remoteUI.err = locales.RemoteNoAddress
				return
			}
			copyText(what, addr)
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
// server's, its title, and its hint under them.
func remoteSwitch(c *ui.Context, k *colors, key, title, hint string, on *bool, disabled bool) bool {
	changed := false
	groupRow(c, k, nil).Children(func() {
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
		// The control server is the gateway's file, not the server's: it
		// can be set, a bad one fixed, while the server does not answer.
		settingsGroup(c, k, "", "", "", func() { a.remoteControlServer(c, k) })
		return
	}
	a.remoteHeader(c, k, page.st)
	a.remoteDevices(c, k, page.st)
}

// remoteServes is that remote clients can reach this machine: Tailcat's
// remote access is on, or Tailscale's discovery runs. Off, the Remote
// page shows its switches alone.
func remoteServes(st rex.RemoteStatus) bool {
	return st.Enabled || st.Tailnet != nil && st.Tailnet.Running
}

// remoteControlServer is the row of built-in Tailscale's control server,
// under its switch: the field that sets it, saved to the gateway's
// settings, as the Tailscale the switch turns on registers with it.
func (a *App) remoteControlServer(c *ui.Context, k *colors) {
	page := &remoteUI
	conf := page.ts
	groupRow(c, k, nil).Children(func() {
		rowLine(c, k, locales.RemoteTailscaleControl(), func() {
			if ui.TextInput(c.Key("ts-url"), &page.tsURL).Placeholder(locales.RemoteTailscalePlaceholder()).
				Label(locales.RemoteTailscaleControl()).Font(monoFamily()).Width(260).Changed() {
				page.tsEdited, page.tsErr = true, ""
			}
			if ui.Button(c.Key("ts-save"), locales.RemoteLanSave()).Disabled(!page.tsEdited).Clicked() {
				url := strings.TrimSpace(page.tsURL)
				if url != "" && !tailnode.ValidControlURL(url) {
					page.tsErr = locales.RemoteTailscaleInvalid()
					return
				}
				next := conf
				next.ControlURL = url
				if err := rexapi.SaveTailscale(rexapi.TailnetDir(), next); err != nil {
					page.tsErr = err.Error()
					return
				}
				page.ts, page.tsErr, page.tsEdited = next, "", false
			}
		})
		rowHint(c, k, locales.RemoteTailscaleNote())
		// A saved control server this version refuses, such as a
		// cleartext one from before it, keeps the device off.
		if page.tsErr != "" {
			rowError(c, k, page.tsErr)
		} else if !page.tsEdited && conf.ControlURL != "" && !tailnode.ValidControlURL(conf.ControlURL) {
			rowError(c, k, locales.RemoteTailscaleInvalid())
		}
	})
}

// remoteHeader is the Remote page's first group: remote access, its
// state and its switch, Tailscale's discovery, and, while either serves,
// what the clients share.
func (a *App) remoteHeader(c *ui.Context, k *colors, st rex.RemoteStatus) {
	page := &remoteUI
	label, detail, kind := remoteState(st, page.unreachable)
	if kind == remoteOff {
		detail = locales.RemoteOffNote()
	}
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
			if remoteSwitch(c, k, "system-tailscale", locales.TailnetMode(), locales.TailnetModeNote(), &tailnet, page.unreachable || page.busy) {
				a.remoteDo(func(srv *rex.Client) error { return srv.SetTailnet(tailnet) }, nil)
			}
			if st.Tailnet.Enabled && !st.Tailnet.Running {
				hint := locales.TailnetUnavailable()
				if st.Tailnet.Error != "" {
					hint = st.Tailnet.Error
				}
				groupRow(c, k, nil).Children(func() { rowHint(c, k, hint) })
			}
		}
		a.remoteControlServer(c, k)
		if !remoteServes(st) {
			return
		}
		if st.Enabled {
			a.remoteReachable(c, k, st)
		}
		// Whether remote clients read the layout and follow it.
		layoutShare := st.ShareLayout
		if remoteSwitch(c, k, "share-layout", locales.RemoteShareLayout(), locales.RemoteShareLayoutNote(), &layoutShare, page.unreachable || page.busy) {
			a.remoteDo(func(srv *rex.Client) error { return srv.ShareLayout(layoutShare) }, nil)
		}
	})
}

// remoteReachable is the Reachable at row: the IP address of this machine
// where it is its own relay, as the field sets it, and Turn Off while one
// is set; the relay's error shows under it.
func (a *App) remoteReachable(c *ui.Context, k *colors, st rex.RemoteStatus) {
	page := &remoteUI
	groupRow(c, k, "reachable").Children(func() {
		rowLine(c, k, locales.RemoteReachableTitle(), func() {
			if ui.TextInput(c.Key("reach"), &page.reach).Placeholder("192.0.2.10").
				Label(locales.RemoteReachableTitle()).Font(monoFamily()).Width(160).Changed() {
				page.reachEdited, page.reachErr = true, ""
			}
			if ui.Button(c.Key("reach-save"), locales.RemoteLanSave()).Label(locales.RemoteReachableTitle() + ": " + locales.RemoteLanSave()).Disabled(!page.reachEdited || page.busy).Clicked() {
				ip, err := rex.NormalizeReachable(strings.TrimSpace(page.reach))
				if err != nil || ip == "" {
					page.reachErr = locales.RemoteReachableInvalid()
					return
				}
				a.remoteDo(func(srv *rex.Client) error { return srv.SetReachable(ip) },
					func() { page.reach, page.reachEdited = ip, false })
			}
			if st.ReachableAt != "" && ui.Button(c.Key("reach-off"), locales.RemoteReachableOff()).Disabled(page.busy).Clicked() {
				a.remoteDo(func(srv *rex.Client) error { return srv.SetReachable("") },
					func() { page.reach, page.reachEdited, page.reachErr = "", false, "" })
			}
		})
		rowHint(c, k, locales.RemoteReachableNote(strconv.Itoa(rex.RemotePort)))
		if page.reachErr != "" {
			rowError(c, k, page.reachErr)
		}
		if st.ReachableError != "" {
			rowError(c, k, st.ReachableError)
		}
		a.remoteFirewall(c, k, st)
	})
}

// remoteFirewall is the line under Reachable at while Windows Firewall
// blocks its port, with Allow, which adds a rule as administrator and asks
// the server to check again; or, when a rule blocks GoRex, where to
// remove it.
func (a *App) remoteFirewall(c *ui.Context, k *colors, st rex.RemoteStatus) {
	page := &remoteUI
	switch st.ReachableFirewall {
	case "blockRule":
		rowError(c, k, locales.RemoteFirewallBlockRule())
	case "blocked":
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.RemoteFirewallBlocked(strconv.Itoa(rex.RemotePort))).FontSize(11.5).TextColor(k.danger).
				LineHeight(1.4).Grow(1).MinWidth(0)
			if ui.Button(c.Key("firewall-allow"), locales.RemoteFirewallAllow()).
				Label(locales.RemoteReachableTitle() + ": " + locales.RemoteFirewallAllow()).Disabled(page.busy).Clicked() {
				failed := false
				a.remoteDo(func(srv *rex.Client) error {
					if err := firewallAllow(); err != nil {
						failed = true
						return nil
					}
					return srv.CheckFirewall()
				}, func() { page.fwFailed = failed })
			}
		})
		if page.fwFailed {
			rowHint(c, k, locales.RemoteFirewallNotChanged())
		}
	}
}

// pairingLeft is the time left in the pairing window, as m:ss.
func pairingLeft(until, now time.Time) string {
	d := max(until.Sub(now).Round(time.Second), 0)
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

// remoteDevices is the group of the devices: the pairing window and the
// requests made in it to approve or reject, the clients allowed, the
// buttons that pair a new device or add one by its key, and this
// machine's details. While remote access serves no one, only requests
// still waiting show.
func (a *App) remoteDevices(c *ui.Context, k *colors, st rex.RemoteStatus) {
	page := &remoteUI
	on := remoteServes(st)
	if !on && len(st.Pending) == 0 && len(st.PendingByAddress) == 0 {
		// Off, this machine's key, relay and Rex's code stay at hand, for
		// the other way: this machine connecting to hosts.
		settingsGroup(c, k, "", "", "", func() { a.remoteDetails(c, k, st) })
		return
	}
	settingsGroup(c, k, locales.RemoteDevices(), locales.RemoteClientsNote(), locales.RemotePairNote(), func() {
		if st.Enabled && st.Pairing.Open {
			groupRow(c, k, nil).Children(func() {
				tileLine(c, k, "plus", locales.RemotePairTitle(), func() {
					statusLine(c, k, k.attention, locales.RemotePairWaiting(pairingLeft(st.Pairing.ExpiresAt, c.Now())))
					c.After(time.Second)
				}, func() {
					if ui.Button(c.Key("pair-stop"), locales.RemotePairStop()).Disabled(page.busy).Clicked() {
						a.remoteDo((*rex.Client).StopPairing, nil)
					}
				})
				if len(st.Pending) == 0 && len(st.PendingByAddress) == 0 {
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
		for _, p := range st.PendingByAddress {
			a.pendingByAddress(c, k, p, len(st.PendingByAddress) > 1)
		}
		if !on {
			return
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
			})
		}
		if page.addingKey {
			a.addByKey(c, k, st)
		} else {
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
					// Rex Go pairs by a code of the address, which opens the
					// window too.
					if st.Running && ui.Button(c.Key("rexgo-qr"), locales.RemoteRexGoQR()).Disabled(page.busy).Clicked() {
						a.openRexGoQR(false)
					}
					if st.Running && st.ReachableAt != "" && st.ReachableError == "" &&
						ui.Button(c.Key("direct-qr"), locales.RemoteDirectQR()).Disabled(page.busy).Clicked() {
						a.openRexGoQR(true)
					}
					if st.Enabled && !st.Pairing.Open {
						if ui.PrimaryButton(c.Key("pair-start"), locales.RemotePairNew()).Disabled(page.busy).Clicked() {
							a.remoteDo((*rex.Client).StartPairing, nil)
						}
					}
				})
			})
		}
		a.remoteDetails(c, k, st)
	})
}

// pendingByAddress is a request made by address: its code, large, to
// compare with the one the device shows, its name and where it came from,
// and Reject and Approve, which is no default button. With several
// waiting, none can be approved: the server refuses it too.
func (a *App) pendingByAddress(c *ui.Context, k *colors, p rex.RemoteAddrPending, many bool) {
	page := &remoteUI
	groupRow(c, k, "pending-addr:"+p.Code).Gap(8).Children(func() {
		tileLine(c, k, "laptop", p.Name, func() {
			ui.Text(c, locales.RemotePairAddrFrom(p.Source)).Font(monoFamily()).FontSize(11.5).TextColor(k.textMuted).SingleLine().Ellipsis("…")
		}, func() {
			if ui.Button(c.Key("r"), locales.RemoteReject()).Disabled(page.busy).Clicked() {
				a.remoteDo(func(srv *rex.Client) error { return srv.RejectPairingCode(p.Code) }, nil)
			}
			if ui.Button(c.Key("a"), locales.RemoteApprove()).Disabled(page.busy || many).Clicked() {
				a.remoteDo(func(srv *rex.Client) error { return srv.ApprovePairingCode(p.Code) }, nil)
			}
		})
		ui.Text(c, p.Code).Font(monoFamily()).FontSize(28).FontWeight(700).TextColor(k.text).Label(p.Code)
		if many {
			rowHint(c, k, locales.RemotePairAddrMany())
		} else {
			rowHint(c, k, locales.RemotePairAddrCompare())
		}
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

// remoteDetails is the Devices group's last row, as Rex's Show Details:
// opened, this machine's address, while the server listens, and its key,
// which a device to pair with asks for. Closing it hides the address.
func (a *App) remoteDetails(c *ui.Context, k *colors, st rex.RemoteStatus) {
	page := &remoteUI
	label := locales.RemoteShowDetails()
	if page.details {
		label = locales.RemoteHideDetails()
	}
	row := groupRow(c, k, "details").Cursor(ui.CursorPointer).Role(ui.RoleDisclosure).Expanded(page.details).Label(label)
	if row.Clicked() {
		page.details = !page.details
		page.reveal, page.confirm, page.st.Address, page.st.ReachableAddress = false, false, "", ""
		page.revealDirect = false
	}
	row.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(k.accent)
			chevron := glyphIcon(c.Key("chevron"), "chevron-down", 11, k.accent).Shrink(0)
			turn := float32(-90)
			if page.details {
				turn = 0
			}
			chevron.Rotate(animated(c, chevron, "fold", turn))
		})
	})
	if !page.details {
		return
	}
	if st.Running {
		a.remoteAddress(c, k, false)
		if st.ReachableAt != "" && st.ReachableError == "" {
			a.remoteAddress(c, k, true)
		}
	}
	a.remoteRelay(c, k)
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
			ui.Row(c).Gap(8).Shrink(0).Children(func() {
				// Rex pairs by this machine's QR code; until an address
				// serves it, the way to set it up is the local network's
				// listener, in the Local API group above.
				if tailnet, lan := localRexPairs(); tailnet != "" || lan != "" {
					if ui.Button(c.Key("local-rex-qr"), locales.HostsRexQR()).Clicked() {
						a.openLocalPairQR()
					}
				} else if ui.Button(c.Key("setup-rex"), locales.HostsSetUpRex()).Clicked() {
					remoteUI.focusLAN = true
				}
				if ui.Button(c.Key("copy-mine"), copyLabel("mine")).Disabled(page.myKey == "").Clicked() {
					copyText("mine", page.myKey)
				}
			})
		})
		rowHint(c, k, locales.RemoteMyKeyNote())
	})
}

// remoteAddress is the address row, or with direct the second address's,
// for devices that dial this machine directly: the address, hidden until
// shown, and the buttons to show, copy and regenerate it (the second one
// changes with the first, so it has no Regenerate).
func (a *App) remoteAddress(c *ui.Context, k *colors, direct bool) {
	page := &remoteUI
	title, note, addr, copyKey := locales.RemoteAddress(), locales.RemoteAddressNote(), &page.st.Address, "addr"
	reveal := &page.reveal // each address is shown by its own button
	if direct {
		title, note, addr, copyKey = locales.RemoteDirectAddress(), locales.RemoteDirectAddressNote(), &page.st.ReachableAddress, "direct"
		reveal = &page.revealDirect
	}
	asking := page.confirm && !direct // Regenerate asks to be sure
	groupRow(c, k, copyKey).Gap(8).Children(func() {
		shown := maskedAddress
		if *reveal && *addr != "" {
			shown = *addr
		}
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, title).FontSize(13).FontWeight(500).TextColor(k.text)
				ui.Text(c, shown).Font(monoFamily()).FontSize(12).TextColor(k.textMuted).LineHeight(1.4)
			})
			if asking {
				return
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
				if *reveal {
					if ui.Button(c.Key("hide-"+copyKey), locales.RemoteHide()).Clicked() {
						*reveal, *addr = false, ""
					}
				} else if ui.Button(c.Key("show-"+copyKey), locales.RemoteShow()).Clicked() {
					*reveal = true
					a.fetchRemote()
				}
				if ui.Button(c.Key("copy-"+copyKey), copyLabel(copyKey)).Clicked() {
					a.copyAddress(direct)
				}
				if !direct && ui.Button(c, locales.RemoteRegenerateAsk()).Clicked() {
					page.confirm = true
				}
			})
		})
		if !asking {
			rowHint(c, k, note)
			return
		}
		ui.Text(c, locales.RemoteRegenerateNote()).
			FontSize(12).TextColor(k.text).LineHeight(1.4).MaxWidth(settingsHintW)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, locales.RemoteRegenerate()).Disabled(page.busy).Clicked() {
				page.confirm, page.reveal, page.st.Address, page.st.ReachableAddress = false, false, "", ""
				page.revealDirect = false
				a.remoteDo((*rex.Client).RegenerateRemote, nil)
			}
			if ui.Button(c, locales.RemoteCancel()).Clicked() {
				page.confirm = false
			}
		})
	})
}

// remoteRelay is the relay row: the DERP relay the address names,
// tailcat's unless one of one's own is saved.
func (a *App) remoteRelay(c *ui.Context, k *colors) {
	page := &remoteUI
	groupRow(c, k, nil).Children(func() {
		rowLine(c, k, locales.RemoteRelay(), func() {
			if ui.TextInput(c.Key("relay"), &page.relay).Placeholder(locales.RemoteRelayPlaceholder()).
				Label(locales.RemoteRelay()).Font(monoFamily()).Width(220).Changed() {
				page.relayEdited, page.relayErr = true, ""
			}
			if ui.Button(c.Key("relay-save"), locales.RemoteLanSave()).Disabled(!page.relayEdited || page.busy).Clicked() {
				relay, err := rex.NormalizeRelay(page.relay)
				if err != nil {
					page.relayErr = locales.RemoteRelayInvalid()
					return
				}
				a.saveRelay(relay)
			}
		})
		if page.relayErr != "" {
			rowError(c, k, page.relayErr)
		}
		rowHint(c, k, locales.RemoteRelayNote())
	})
}

// saveRelay sets the server's relay, saying under the field what went
// wrong, as the field keeps what was typed.
func (a *App) saveRelay(relay string) {
	page := &remoteUI
	if page.busy {
		return
	}
	page.busy, page.relayErr = true, ""
	started := a.remoteAsk(func(srv *rex.Client) func() {
		err := srv.SetRelay(relay)
		return func() {
			page.busy = false
			if err != nil {
				page.relayErr = serverMessage(err)
			} else {
				page.relay, page.relayEdited = relay, false
			}
			a.fetchRemote()
		}
	})
	if !started {
		page.busy, page.relayErr = false, locales.RemoteNotConnected()
	}
}
