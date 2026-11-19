package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/internal/rexapi"
	"gorex/internal/testrex"
	"gorex/locales"
)

// TestLocalPairQRWebTab: the local network's pair dialog offers the web
// page's code beside this address's, and allows the page from there, with
// the pairing token and the certificate's hash kept off the screen.
func TestLocalPairQRWebTab(t *testing.T) {
	isolateSettings(t, "")
	registerFonts()
	dir := rexapi.TailnetDir()
	section, remote, qr := settingsUI.section, remoteUI, remoteUI.pairQR
	t.Cleanup(func() { settingsUI.section, remoteUI, remoteUI.pairQR = section, remote, qr })
	gw := testrex.NewStalled(t)
	t.Setenv("GOREX_REXAPI_SOCKET", gw.Path)
	t.Cleanup(func() {
		waitForThat(t, func() bool { _, open := gw.Counts(); return open == 0 }, "the look to give up")
	})

	const token, hash = "token-for-the-test", "hash-for-the-test"
	a := &App{}
	settingsUI.section = "server"
	remoteUI.loaded, remoteUI.st = true, rex.RemoteStatus{}
	remoteUI.lan = rexapi.LANConfig{Enabled: true, Listen: "192.168.1.20:7297", Allow: []string{"192.168.1.0/24"}, Token: token}
	remoteUI.lanStatus = rexapi.LANStatus{
		Running: true, Listening: "192.168.1.20:7297",
		WTListening: "192.168.1.20:7298", WTCertHash: hash,
	}
	tt := ui.NewTester(a.settingsView, 780, 1400)
	tt.Frame()
	a.openLocalPairQR()
	tt.Frame()
	tt.Frame()

	page := remoteUI.pairQR
	if page == nil || page.webImage == nil {
		t.Fatalf("no web code for a listener with a WebTransport leg: %+v", page)
	}
	if !tt.HasText(locales.HostsPairCodePage()) {
		t.Fatalf("no web tab in %q", tt.Texts())
	}
	if page.webTrusted {
		t.Fatalf("the page is trusted with no web.json")
	}

	// The web tab: the page's code and the button that allows the page.
	if err := tt.Click(locales.HostsPairCodePage()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if page.tab != 1 {
		t.Fatalf("the web tab was not chosen: %d", page.tab)
	}
	for _, s := range []string{locales.HostsPairWebHint(rexapi.WebOrigin), locales.HostsPairWebTrust(rexapi.WebOrigin)} {
		if !tt.HasText(s) {
			t.Fatalf("no %q in %q", s, tt.Texts())
		}
	}
	for _, s := range tt.Texts() {
		if strings.Contains(s, token) || strings.Contains(s, hash) {
			t.Errorf("the pairing token or the hash is on the screen: %q", s)
		}
	}

	if err := tt.Click(locales.HostsPairWebTrust(rexapi.WebOrigin)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if c, err := rexapi.LoadWeb(dir); err != nil || len(c.Origins) != 1 || c.Origins[0] != rexapi.WebOrigin {
		t.Fatalf("web.json after allowing: %+v, %v", c, err)
	}
	if !tt.HasText(locales.HostsPairWebTrusted()) {
		t.Fatalf("no trusted line in %q", tt.Texts())
	}
}
