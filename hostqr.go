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
	"gorex/locales"
)

// The settings window owns this dialog and its asynchronous endpoint lookup.
type hostPairQR struct {
	host, address                     string
	image                             ui.ImageSource
	open, loading, copied, copyFailed bool
	local                             bool
}

var hostsPairQR *hostPairQR

func (a *App) localRexPairURL() string {
	if a.client == nil {
		return ""
	}
	select {
	case <-a.client.Closed():
		return ""
	default:
		return remoteUI.lan.PairURL(remoteUI.lanStatus)
	}
}

func (a *App) openLocalPairQR() {
	fetchLAN()
	address, image := mobilePairQR(a.localRexPairURL())
	if image != nil {
		name, _, _ := a.hostIdentity()
		hostsPairQR = &hostPairQR{host: name, address: address, image: image, open: true, local: true}
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
		backdrop.Background(k.backdrop)
		glassPanel(panel.Width(370).Padding(18).Gap(12).AlignItems(ui.Center), k.panel).
			Border(1, k.panelBorder).Shadow(0, 24, 60, -8, ui.RGBA(0, 0, 0, 0.28)).
			Role(ui.RoleDialog).Label(locales.HostsRexQRTitle(page.host))
		ui.Text(c, locales.HostsRexQRTitle(page.host)).FontSize(15).FontWeight(700).TextColor(k.text)
		switch {
		case page.loading:
			ui.Text(c, locales.HostsRexQRLoading()).FontSize(13).TextColor(k.textMuted)
		case page.image != nil:
			hint := locales.HostsRexQRHint()
			if page.local {
				hint = locales.HostsLocalRexQRHint()
			}
			ui.Text(c, hint).FontSize(13).TextColor(k.textMuted).LineHeight(1.4)
			ui.Image(c, page.image).Size(248, 248).Label(locales.HostsRexQRTitle(page.host))
			ui.Text(c, page.address).Font(monoFamily()).FontSize(12).TextColor(k.text).SingleLine()
		default:
			ui.Text(c, locales.HostsRexQRUnavailable()).FontSize(13).TextColor(k.textMuted).LineHeight(1.4)
		}
		ui.Row(c).Gap(8).Children(func() {
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
			if ui.Button(c.Key("close"), locales.HostsLayoutClose()).Clicked() {
				page.open = false
			}
		})
		if page.copyFailed {
			ui.Text(c, locales.HostsRexQRCopyFailed()).FontSize(12).TextColor(k.danger)
		}
	})
	if !page.open {
		hostsPairQR = nil
	}
}
