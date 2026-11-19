package main

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"rsc.io/qr"

	"gorex/internal/rex"
	"gorex/internal/rexapi"
	"gorex/locales"
)

// The settings window owns this dialog and its asynchronous endpoint lookup.
type hostPairQR struct {
	host, address                     string
	image                             ui.ImageSource
	open, loading, copied, copyFailed bool
	local                             bool
	// tailnet tells that the address is the gateway's tailnet one, which
	// the device must be on the same tailnet as; the local network's
	// needs the same network as the address.
	tailnet bool
	// rexGo is this machine's code for Rex Go, of its tailcat address: a
	// secret, so the page keeps the image only, and shows no address and
	// no Copy.
	rexGo bool
	// direct is that its address is the second one, for devices that dial
	// this machine directly (Reachable at).
	direct bool
	// tab is the code the dialog shows when the listener has a web link
	// too: 0 the address's own code, 1 the page's (rexapi.WebOrigin).
	tab int
	// webImage is the code for the page at rexapi.WebOrigin, opened with
	// this listener's address, certificate hash and pairing token
	// (rexapi.LANConfig.WebPairURL); nil when the listener has no leg to
	// put in it. It holds a credential, so the page keeps the image and
	// says where it leads, never the link itself. webTrusted is whether
	// this computer lets that page in (rexapi.LoadWeb).
	webImage                  ui.ImageSource
	webTrusted, webAllowFailed bool
	webErr                    string
}

// localRexPairs are this computer's Rex pairing addresses: the gateway's
// tailnet HTTPS address, as "gorex rexapi pair" shows it, and the local
// network listener's, for a phone on the same network. The code holds the
// address only. Neither waits, so the server page draws them every
// frame: the tailnet's is the gateway's last answer
// (rexapi.CachedPairURL), and a new one draws the window again; the
// listener's comes from the settings the page's poll reads.
func localRexPairs() (tailnet, lan string) {
	win := settingsUI.win
	tailnet = rexapi.CachedPairURL(func() {
		if win != nil {
			win.Update(func() {})
		}
	})
	return tailnet, remoteUI.lan.PairURL(remoteUI.lanStatus)
}

func (a *App) openLocalPairQR() {
	address, lan := localRexPairs()
	tailnet := address != ""
	if !tailnet {
		address = lan
	}
	_, image := mobilePairQR(address)
	if image != nil {
		name, _, _ := a.hostIdentity()
		page := &hostPairQR{host: name, address: address, image: image, open: true, local: true, tailnet: tailnet}
		// The page's code, and whether this computer lets the page in. A
		// listener that is off, holds no token or has no WebTransport leg
		// gives no link: the page could not reach it anyway.
		if web := remoteUI.lan.WebPairURL(remoteUI.lanStatus, rexapi.WebOrigin); web != "" {
			page.webImage = qrImage(web)
			page.webTrusted = trustsWebOrigin(rexapi.TailnetDir(), rexapi.WebOrigin)
		}
		remoteUI.pairQR = page
	}
}

// trustsWebOrigin tells whether this computer lets the page at origin in
// (rexapi.LoadWeb), which the pair dialog's web tab adds when it does not.
func trustsWebOrigin(dir, origin string) bool {
	c, err := rexapi.LoadWeb(dir)
	if err != nil {
		return false
	}
	for _, o := range c.Origins {
		if o == origin {
			return true
		}
	}
	return false
}

func openHostPairQR(h *hostConn) {
	win := settingsUI.win
	if win == nil {
		return
	}
	page := &hostPairQR{host: h.label(), open: true, loading: true}
	remoteUI.pairQR = page
	target := h.Address
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		var address string
		var image ui.ImageSource
		if c, err := rex.ConnectRemote(ctx, target); err == nil {
			hello, err := c.Hello()
			c.Close()
			if err == nil {
				address, image = mobilePairQR(hello.RexPairURL)
			}
		}
		win.Update(func() {
			if remoteUI.pairQR == page {
				page.address, page.image, page.loading = address, image, false
			}
		})
	}()
}

// openRexGoQR opens the pairing window and shows this machine's code for
// Rex Go (docs/ios-tailcat.md, Pairing a phone). The address is asked for
// here and goes into the image only: never into the page, a log or the
// clipboard. With direct it is the second address's, of Reachable at.
func (a *App) openRexGoQR(direct bool) {
	name, _, _ := a.hostIdentity()
	// Set off the UI thread, read on it once the change is done.
	var image ui.ImageSource
	a.remoteDo(func(srv *rex.Client) error {
		if err := srv.StartPairing(); err != nil {
			return err
		}
		st, err := srv.RemoteStatus()
		addr := st.Address
		if direct {
			addr = st.ReachableAddress
		}
		if err == nil && addr != "" {
			image = qrImage(rexGoPairURL(addr, name))
		}
		return err
	}, func() {
		remoteUI.pairQR = &hostPairQR{host: name, image: image, open: true, rexGo: true, direct: direct}
	})
}

// rexGoPairURL is the code Rex Go pairs by: the tailcat address and the
// machine's name, rexgo://tailcat?a=<address>&n=<name>. A space is %20,
// as Rex Go reads no + for one.
func rexGoPairURL(address, name string) string {
	q := url.Values{"a": {address}, "n": {name}}.Encode()
	return "rexgo://tailcat?" + strings.ReplaceAll(q, "+", "%20")
}

// mobilePairQR returns the code for a Rex pairing address, and the
// address to show and copy: a bare http(s) origin, or the local network
// listener's with its pairing token, http://IP:PORT/#k=TOKEN
// (rexapi.LANConfig.PairURL). Anything else is no code.
func mobilePairQR(address string) (string, ui.ImageSource) {
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery {
		return "", nil
	}
	if u.Fragment == "" {
		if u.Path != "" {
			return "", nil
		}
	} else if u.Path != "/" || !strings.HasPrefix(u.Fragment, "k=") || len(u.Fragment) == len("k=") {
		return "", nil
	}
	img := qrImage(address)
	if img == nil {
		return "", nil
	}
	return address, img
}

// qrImage is s as a QR code; nil when it doesn't fit one.
func qrImage(s string) ui.ImageSource {
	code, err := qr.Encode(s, qr.M)
	if err != nil {
		return nil
	}
	img, err := ui.DecodeImage(code.PNG())
	if err != nil {
		return nil
	}
	return img
}

// pairQRCode draws a pairing code: a scanner expects the white behind it,
// inside the panel's corner radius.
func pairQRCode(c *ui.Context, img ui.ImageSource, label string) {
	ui.Row(c).Justify(ui.Center).Margin(2, 0, 2, 0).Children(func() {
		ui.Column(c).Padding(10).Radius(12).Background(ui.RGB(255, 255, 255)).Children(func() {
			ui.Image(c, img).Size(240, 240).Label(label)
		})
	})
}

func hostPairQRSheet(c *ui.Context, k *colors, page *hostPairQR) {
	title := locales.HostsRexQRTitle(page.host)
	if page.rexGo {
		title = locales.RemoteRexGoQRTitle(page.host)
	}
	// The web tab's code opens a browser, not Rex: it says so.
	if page.webImage != nil && page.tab == 1 {
		title = locales.HostsPairWebTitle(page.host)
	}
	ui.DialogBase(c, &page.open, func(backdrop, panel ui.Element) {
		// The backdrop dims the window; the glass is the panel's, as in
		// the other sheets over the app.
		backdrop.Background(k.backdrop)
		glassPanel(panel.Width(380).Padding(20).Gap(12).Radius(16), k.panel).
			Border(1, k.panelBorder).Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.28)).
			Role(ui.RoleDialog).Label(title)
		ui.Text(c, title).FontSize(15).FontWeight(700).TextColor(k.text)
		// A listener with a web link offers both codes: this address's,
		// for Rex, and the page's, for a browser on the same network.
		web := page.webImage != nil
		if web {
			ui.Segmented(c, &page.tab, locales.HostsPairCodeAddress(), locales.HostsPairCodePage()).Label(title).Changed()
		}
		switch {
		case page.loading:
			ui.Text(c, locales.HostsRexQRLoading()).FontSize(13).TextColor(k.textMuted).LineHeight(1.35)
		case web && page.tab == 1:
			ui.Text(c, locales.HostsPairWebHint(rexapi.WebOrigin)).FontSize(12.5).TextColor(k.textMuted).LineHeight(1.35)
			pairQRCode(c, page.webImage, title)
			if page.webTrusted {
				ui.Text(c, locales.HostsPairWebTrusted()).FontSize(12).TextColor(k.textMuted).LineHeight(1.3)
			} else {
				ui.Text(c, locales.HostsPairWebUntrusted()).FontSize(12).TextColor(k.textMuted).LineHeight(1.3)
				if ui.Button(c.Key("trust-web"), locales.HostsPairWebTrust(rexapi.WebOrigin)).Clicked() {
					if _, err := rexapi.AllowWebOrigin(rexapi.TailnetDir(), rexapi.WebOrigin); err != nil {
						page.webAllowFailed, page.webErr = true, err.Error()
					} else {
						page.webTrusted, page.webAllowFailed = true, false
					}
				}
			}
			if page.webAllowFailed {
				ui.Text(c, locales.HostsPairWebTrustFailed(page.webErr)).FontSize(12).TextColor(k.danger).LineHeight(1.3)
			}
		case page.image != nil:
			hint := locales.HostsRexQRHint()
			if page.direct {
				hint = locales.RemoteDirectQRHint()
			} else if page.rexGo {
				hint = locales.RemoteRexGoQRHint()
			} else if page.local {
				hint = locales.HostsLocalRexQRHint()
				if !page.tailnet {
					hint = locales.HostsLocalLanRexQRHint()
				}
			}
			ui.Text(c, hint).FontSize(12.5).TextColor(k.textMuted).LineHeight(1.35)
			pairQRCode(c, page.image, title)
			if !page.rexGo {
				// The pairing token stays in the code and the copy, off
				// the screen.
				shown, _, _ := strings.Cut(page.address, "#")
				ui.Text(c, shown).Font(monoFamily()).FontSize(12).TextColor(k.text).SingleLine().Label(shown)
			}
		case page.rexGo:
			ui.Text(c, locales.RemoteRexGoQRUnavailable()).FontSize(12.5).TextColor(k.textMuted).LineHeight(1.35)
		default:
			ui.Text(c, locales.HostsRexQRUnavailable()).FontSize(12.5).TextColor(k.textMuted).LineHeight(1.35)
		}
		if page.copyFailed {
			ui.Text(c, locales.HostsRexQRCopyFailed()).FontSize(12).TextColor(k.danger).LineHeight(1.3)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
			if ui.Button(c.Key("close"), locales.HostsLayoutClose()).Clicked() {
				page.open = false
			}
			// The web tab's code holds the token too: a copy of the other
			// address's link would be a different code than the one shown.
			if page.image != nil && !page.rexGo && !(web && page.tab == 1) {
				label := locales.HostsRexQRCopy()
				if page.copied {
					label = locales.HostsRexQRCopied()
				}
				if ui.Button(c.Key("copy"), label).Clicked() {
					err := mygo.Clipboard.Write(transfer.TextData(page.address))
					page.copied, page.copyFailed = err == nil, err != nil
				}
			}
		})
	})
	if !page.open {
		remoteUI.pairQR = nil
	}
}
