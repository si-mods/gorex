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
		remoteUI.pairQR = &hostPairQR{host: name, address: address, image: image, open: true, local: true, tailnet: tailnet}
	}
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

func mobilePairQR(address string) (string, ui.ImageSource) {
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
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

func hostPairQRSheet(c *ui.Context, k *colors, page *hostPairQR) {
	title := locales.HostsRexQRTitle(page.host)
	if page.rexGo {
		title = locales.RemoteRexGoQRTitle(page.host)
	}
	ui.DialogBase(c, &page.open, func(backdrop, panel ui.Element) {
		// The backdrop dims the window; the glass is the panel's, as in
		// the other sheets over the app.
		backdrop.Background(k.backdrop)
		glassPanel(panel.Width(380).Padding(20).Gap(12).Radius(16), k.panel).
			Border(1, k.panelBorder).Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.28)).
			Role(ui.RoleDialog).Label(title)
		ui.Text(c, title).FontSize(15).FontWeight(700).TextColor(k.text)
		switch {
		case page.loading:
			ui.Text(c, locales.HostsRexQRLoading()).FontSize(13).TextColor(k.textMuted).LineHeight(1.35)
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
			// A QR code keeps its own white, the way a scanner expects,
			// inside the panel's corner radius.
			ui.Row(c).Justify(ui.Center).Margin(2, 0, 2, 0).Children(func() {
				ui.Column(c).Padding(10).Radius(12).Background(ui.RGB(255, 255, 255)).Children(func() {
					ui.Image(c, page.image).Size(240, 240).Label(title)
				})
			})
			if !page.rexGo {
				ui.Text(c, page.address).Font(monoFamily()).FontSize(12).TextColor(k.text).SingleLine().Label(page.address)
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
			if page.image != nil && !page.rexGo {
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
