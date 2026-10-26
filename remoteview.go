package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
)

// The Remote page of the settings window: the server's remote access, as
// the server tells it. Every call to the server runs off the UI thread.

// remoteUI is the page's state; the main thread owns it.
var remoteUI struct {
	stop        chan struct{} // closed to stop the refresh; non-nil while it runs
	st          rex.RemoteStatus
	loaded      bool   // st is what the server said
	unreachable bool   // the last ask failed
	myKey       string // this machine's public key
	busy        bool   // a change is on its way
	err         string // what went wrong with the last change
	// reveal is that the address is shown. The address is a secret: st
	// holds it only while reveal is on, and the page asks for it again
	// to copy it.
	reveal    bool
	confirm   bool   // regenerate asks to be sure
	copied    string // what was copied just now
	name, key string // the client to add
	addErr    string
}

// showSection chooses a settings section. The Remote page asks the server
// every two seconds while it shows, and drops the address when it goes.
func (a *App) showSection(id string) {
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
	page.reveal, page.confirm, page.err, page.addErr, page.name, page.key = false, false, "", "", "", ""
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

// fetchRemote asks the server for its status; the address comes only
// while it is shown.
func (a *App) fetchRemote() {
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
	remoteUI.busy, remoteUI.err = true, ""
	started := a.remoteAsk(func(srv *rex.Client) func() {
		err := change(srv)
		return func() {
			remoteUI.busy = false
			if err != nil {
				remoteUI.err = serverMessage(err)
			} else if ok != nil {
				ok()
			}
			a.fetchRemote()
		}
	})
	if !started {
		remoteUI.busy, remoteUI.err = false, "Not connected to the session server."
	}
}

// serverMessage is an error of the server without its package prefix.
func serverMessage(err error) string {
	s := strings.TrimPrefix(err.Error(), "rex: ")
	if s == "" {
		return "Something went wrong."
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
		return "Server not reachable", "GoRex asks again every two seconds.", remoteDown
	case st.Running:
		return "Listening", "", remoteListening
	case st.LastError != "" && st.Enabled:
		return "Error", st.LastError + " The server tries again.", remoteError
	case st.LastError != "":
		return "Error", st.LastError, remoteError
	case st.Enabled:
		return "Starting…", "", remoteStarting
	}
	return "Off", "", remoteOff
}

// validateClientKey returns what is wrong with a public key, "" when the
// server takes it.
func validateClientKey(s string) string {
	if strings.TrimSpace(s) == "" {
		return "Paste the public key of the client."
	}
	if _, err := rex.NormalizePublicKey(s); err != nil {
		return `That is not a public key: it reads "nodekey:" and 64 hex digits. Copy it from "This machine's key" on the client.`
	}
	return ""
}

// validateNewClient returns what is wrong with a client to add, "" if none.
func validateNewClient(name, pub string, allowed []rex.RemoteClient) string {
	if strings.TrimSpace(name) == "" {
		return "Name the client, to know it in the list."
	}
	if m := validateClientKey(pub); m != "" {
		return m
	}
	want, _ := rex.NormalizePublicKey(pub)
	for _, c := range allowed {
		if have, _ := rex.NormalizePublicKey(c.PublicKey); have == want {
			return "That client is allowed already, as " + c.Name + "."
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
		remoteUI.err = "Could not copy: " + err.Error()
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
		return "Copied"
	}
	return "Copy"
}

// copyAddress asks the server for the address and copies it, keeping it
// nowhere.
func (a *App) copyAddress() {
	a.remoteAsk(func(srv *rex.Client) func() {
		st, err := srv.RemoteStatus()
		return func() {
			if err != nil || st.Address == "" {
				remoteUI.err = "The server has no address to copy."
				return
			}
			copyText("addr", st.Address)
		}
	})
}

// remoteCard is a card with a title and an optional note, then its body.
func remoteCard(c *ui.Context, k *colors, title, note string, body func()) {
	card(ui.Column(c).Gap(10).Padding(12, 14), k).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(13.5).FontWeight(600).TextColor(k.text)
			if note != "" {
				ui.Text(c, note).FontSize(12).TextColor(k.textFaint).LineHeight(1.3)
			}
		})
		body()
	})
}

func (a *App) remoteSettings(c *ui.Context, k *colors) {
	settingsTitle(c, k, "Remote access")
	page := &remoteUI
	if !page.loaded {
		label := "Asking the session server…"
		if page.unreachable {
			label, _, _ = remoteState(rex.RemoteStatus{}, true)
		}
		ui.Text(c, label).FontSize(12.5).TextColor(k.textFaint).Padding(0, 0, 0, 4)
		return
	}
	st := page.st
	label, detail, kind := remoteState(st, page.unreachable)
	dot := map[remoteKind]ui.Color{
		remoteOff: k.exited, remoteStarting: k.attention, remoteListening: k.busy, remoteError: k.danger, remoteDown: k.danger,
	}[kind]

	// The switch and the state.
	on := st.Enabled && !page.unreachable
	noClients := len(st.Allowed) == 0
	card(ui.Row(c).Gap(16).AlignItems(ui.Center).Padding(11, 14), k).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, "Allow remote clients").FontSize(13.5).FontWeight(600).TextColor(k.text)
			ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(7, 7).Radius(4).Background(dot).Shrink(0)
				ui.Text(c, label).FontSize(12.5).FontWeight(500).TextColor(k.textMuted)
			})
			switch {
			case detail != "":
				ui.Text(c, detail).FontSize(12).TextColor(k.textFaint).LineHeight(1.3)
			case noClients && !st.Enabled:
				ui.Text(c, "Allow a client below first: remote access needs at least one.").FontSize(12).TextColor(k.textFaint).LineHeight(1.3)
			}
			if page.err != "" {
				ui.Text(c, page.err).FontSize(12).TextColor(k.danger).LineHeight(1.3)
			}
		})
		sw := ui.Switch(c, &on).Label("Allow remote clients").Disabled(page.unreachable || page.busy || noClients && !st.Enabled)
		if sw.Changed() {
			if on {
				a.remoteDo((*rex.Client).EnableRemote, nil)
			} else {
				a.remoteDo((*rex.Client).DisableRemote, nil)
			}
		}
	})

	if st.Running {
		a.remoteAddress(c, k)
	}

	remoteCard(c, k, "This machine's key", "Give it to the machine you want to connect to, to allow this one there.", func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			shown := page.myKey
			if shown == "" {
				shown = "No key yet."
			}
			ui.Text(c, shown).Font(monoFamily()).FontSize(12).TextColor(k.textMuted).Grow(1).MinWidth(0).LineHeight(1.35)
			if ui.Button(c.Key("copy-mine"), copyLabel("mine")).Disabled(page.myKey == "").Clicked() {
				copyText("mine", page.myKey)
			}
		})
	})

	a.remoteClients(c, k, st)
}

func (a *App) remoteAddress(c *ui.Context, k *colors) {
	page := &remoteUI
	remoteCard(c, k, "Address", "Anyone with this address and an allowed key can run commands here as your user, like SSH.", func() {
		shown := maskedAddress
		if page.reveal && page.st.Address != "" {
			shown = page.st.Address
		}
		ui.Text(c, shown).Font(monoFamily()).FontSize(12).TextColor(k.textMuted).LineHeight(1.35).
			Padding(8, 10).Radius(9).Background(k.hover)
		if page.confirm {
			ui.Text(c, "Regenerate makes a new address: the old one stops working, and the remote clients connected now are dropped.").
				FontSize(12).TextColor(k.text).LineHeight(1.3)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, "Regenerate").Disabled(page.busy).Clicked() {
					page.confirm, page.reveal, page.st.Address = false, false, ""
					a.remoteDo((*rex.Client).RegenerateRemote, nil)
				}
				if ui.Button(c, "Cancel").Clicked() {
					page.confirm = false
				}
			})
			return
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if page.reveal {
				if ui.Button(c, "Hide").Clicked() {
					page.reveal, page.st.Address = false, ""
				}
			} else if ui.Button(c, "Show").Clicked() {
				page.reveal = true
				a.fetchRemote()
			}
			if ui.Button(c.Key("copy-addr"), copyLabel("addr")).Clicked() {
				a.copyAddress()
			}
			if ui.Button(c, "Regenerate…").Clicked() {
				page.confirm = true
			}
		})
	})
}

func (a *App) remoteClients(c *ui.Context, k *colors, st rex.RemoteStatus) {
	page := &remoteUI
	remoteCard(c, k, "Allowed clients", "Revoking the last client turns remote access off.", func() {
		if len(st.Allowed) == 0 {
			ui.Text(c, "No client is allowed yet.").FontSize(12.5).TextColor(k.textFaint)
		}
		for _, client := range st.Allowed {
			ui.Row(c.Key(client.PublicKey)).Gap(10).Padding(4, 0).AlignItems(ui.Center).Children(func() {
				ui.Text(c, client.Name).FontSize(13.5).FontWeight(500).TextColor(k.text).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
				ui.Text(c, shortKey(client.PublicKey)).Font(monoFamily()).FontSize(12).TextColor(k.textFaint).SingleLine().
					Tooltip(client.PublicKey).Grow(1).Shrink(0)
				if iconButton(c.Key("c"), k, "copy", "Copy key", 26, 15).Tooltip("Copy the key").Clicked() {
					copyText(client.PublicKey, client.PublicKey)
				}
				if ui.Button(c.Key("r"), "Revoke").Disabled(page.busy).Clicked() {
					a.remoteDo(func(srv *rex.Client) error { return srv.RevokeRemote(client.PublicKey) }, nil)
				}
			})
		}
		ui.Box(c).Height(1).Background(k.tabSep.Alpha(0.5))
		ui.Text(c, "Add a client").FontSize(12.5).FontWeight(600).TextColor(k.textMuted)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.TextInput(c.Key("name"), &page.name).Placeholder("Name, as alice-laptop").Label("Client name").Width(150).Shrink(0).Changed() {
				page.addErr = ""
			}
			if ui.TextInput(c.Key("key"), &page.key).Placeholder("nodekey:…").Label("Client public key").Grow(1).MinWidth(0).Changed() {
				page.addErr = ""
			}
			if ui.Button(c, "Add").Disabled(page.busy).Clicked() {
				if page.addErr = validateNewClient(page.name, page.key, st.Allowed); page.addErr == "" {
					name, pub := strings.TrimSpace(page.name), strings.TrimSpace(page.key)
					a.remoteDo(func(srv *rex.Client) error { return srv.AllowRemote(name, pub) },
						func() { page.name, page.key = "", "" })
				}
			}
		})
		if page.addErr != "" {
			ui.Text(c, page.addErr).FontSize(12).TextColor(k.danger).LineHeight(1.3)
		}
	})
}
