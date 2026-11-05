package main

import (
	"context"
	"net/url"
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
}

var hostsPairQR *hostPairQR

// localRexPair is this computer's Rex pairing address: the gateway's
// tailnet HTTPS address, as "gorex rexapi pair" shows it, else the local
// network listener's, for a phone on the same network. The code holds the
// address only.
func (a *App) localRexPair() (string, bool) {
	if url := rexapi.PairURL(); url != "" {
		return url, true
	}
	dir := rexapi.TailnetDir()
	conf, err := rexapi.LoadLAN(dir)
	if err != nil {
		return "", false
	}
	return conf.PairURL(rexapi.LoadLANStatus(dir)), false
}

func (a *App) localRexPairURL() string {
	url, _ := a.localRexPair()
	return url
}

func (a *App) openLocalPairQR() {
	address, tailnet := a.localRexPair()
	_, image := mobilePairQR(address)
	if image != nil {
		name, _, _ := a.hostIdentity()
		hostsPairQR = &hostPairQR{host: name, address: address, image: image, open: true, local: true, tailnet: tailnet}
	}
}

func openHostPairQR(h *hostConn) {
	win := settingsUI.win
	if win == nil {
		return
	}
	page := &hostPairQR{host: h.Name, open: true, loading: true}
	hostsPairQR = page
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
			if hostsPairQR == page {
				page.address, page.image, page.loading = address, image, false
			}
		})
	}()
}

func mobilePairQR(address string) (string, ui.ImageSource) {
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return "", nil
	}
	code, err := qr.Encode(address, qr.M)
	if err != nil {
		return "", nil
	}
	img, err := ui.DecodeImage(code.PNG())
	if err != nil {
		return "", nil
	}
	return address, img
}

func hostPairQRSheet(c *ui.Context, k *colors, page *hostPairQR) {
	ui.DialogBase(c, &page.open, func(backdrop, panel ui.Element) {
		// The backdrop dims the window; the glass is the panel's, as in
		// the other sheets over the app.
		backdrop.Background(k.backdrop)
		glassPanel(panel.Width(380).Padding(20).Gap(12).Radius(16), k.panel).
			Border(1, k.panelBorder).Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.28)).
			Role(ui.RoleDialog).Label(locales.HostsRexQRTitle(page.host))
		ui.Text(c, locales.HostsRexQRTitle(page.host)).FontSize(15).FontWeight(700).TextColor(k.text)
		switch {
		case page.loading:
			ui.Text(c, locales.HostsRexQRLoading()).FontSize(13).TextColor(k.textMuted).LineHeight(1.35)
		case page.image != nil:
			hint := locales.HostsRexQRHint()
			if page.local {
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
					ui.Image(c, page.image).Size(240, 240).Label(locales.HostsRexQRTitle(page.host))
				})
			})
			ui.Text(c, page.address).Font(monoFamily()).FontSize(12).TextColor(k.text).SingleLine().Label(page.address)
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
			if page.image != nil {
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
		hostsPairQR = nil
	}
}
