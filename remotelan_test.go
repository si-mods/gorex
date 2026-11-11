package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/internal/rexapi"
	"gorex/internal/testrex"
	"gorex/locales"
)

// TestRemoteLANCard draws the Developer page's row of Rex's apps on the
// local network, turns it on, adds and removes a network, all saved in the gateway's
// state for the server's gateway to follow.
func TestRemoteLANCard(t *testing.T) {
	isolateSettings(t, "")
	registerFonts()
	t.Setenv("GOREX_DIR", t.TempDir())
	t.Cleanup(stopRemotePoll)
	a := &App{}
	settingsUI.section = "developer"
	remoteUI.loaded, remoteUI.st = true, rex.RemoteStatus{}
	remoteUI.lan, remoteUI.lanStatus, remoteUI.lanErr, remoteUI.lanAdd = rexapi.LANConfig{}, rexapi.LANStatus{}, "", ""
	tt := ui.NewTester(a.settingsView, 780, 1400)
	tt.Frame()
	for _, s := range []string{locales.RemoteLanSwitch(), "Off", "Networks allowed", "None: no device can connect."} {
		if !tt.HasText(s) {
			t.Fatalf("no %q in %q", s, tt.Texts())
		}
	}

	// On, it takes the machine's address and network, or says it has none.
	// The switch is at the right of the switch's title and state.
	title, _ := tt.Find(locales.RemoteLanSwitch())
	for y := title.Y - 6; y < title.Y+title.H+20 && !remoteUI.lan.Enabled && remoteUI.lanErr == ""; y += 4 {
		for x := float32(775); x > title.X+60 && !remoteUI.lan.Enabled && remoteUI.lanErr == ""; x -= 6 {
			tt.ClickAt(x, y)
			tt.Frame()
		}
	}
	dir := rexapi.TailnetDir()
	saved, _ := rexapi.LoadLAN(dir)
	if remoteUI.lanErr == "" {
		if !saved.Enabled || saved.Listen == "" || len(saved.Allow) != 1 {
			t.Fatalf("turned on: %+v", saved)
		}
	} else if saved.Enabled {
		t.Fatalf("on without an address: %+v", saved)
	}

	// A network typed is checked, then allowed.
	remoteUI.lanAdd = "10.6.0.2"
	tt.Frame()
	add, ok := tt.Find("Add")
	if !ok {
		t.Fatalf("no Add in %q", tt.Texts())
	}
	tt.ClickAt(add.X+add.W/2, add.Y+add.H/2)
	tt.Frame()
	saved, _ = rexapi.LoadLAN(dir)
	found := false
	for _, n := range saved.Allow {
		found = found || n == "10.6.0.2/32"
	}
	if !found || remoteUI.lanAdd != "" {
		t.Fatalf("added: %+v (field %q, err %q)", saved, remoteUI.lanAdd, remoteUI.lanErr)
	}
	remoteUI.lanAdd = "everyone"
	tt.Frame()
	add, _ = tt.Find("Add") // a row more above it now
	tt.ClickAt(add.X+add.W/2, add.Y+add.H/2)
	tt.Frame()
	if remoteUI.lanErr == "" {
		t.Error("a network that does not read was taken")
	}

	// Removed, it goes from the state.
	tt.Frame()
	row, ok := tt.Find("10.6.0.2/32")
	if !ok {
		t.Fatalf("no row for 10.6.0.2/32 in %q", tt.Texts())
	}
	for x := float32(775); x > row.X+row.W; x -= 4 {
		tt.ClickAt(x, row.Y+row.H/2)
		tt.Frame()
		if s, _ := rexapi.LoadLAN(dir); len(s.Allow) < len(saved.Allow) {
			break
		}
	}
	if s, _ := rexapi.LoadLAN(dir); len(s.Allow) != len(saved.Allow)-1 {
		t.Fatalf("removed: %+v", s)
	}

	// The gateway's word shows.
	remoteUI.lan.Enabled, remoteUI.lanStatus = true, rexapi.LANStatus{Running: true, Listening: "192.168.88.241:7297"}
	tt.Frame()
	if !tt.HasText("Listening on http://192.168.88.241:7297") {
		t.Errorf("no listening state in %q", tt.Texts())
	}
	remoteUI.lanStatus = rexapi.LANStatus{Running: true, Error: "address already in use"}
	tt.Frame()
	if !tt.HasText("Not listening: address already in use") {
		t.Errorf("no error state in %q", tt.Texts())
	}
}

// TestPairURLOffRender: drawing the Developer page does not wait on the
// gateway, even when its socket answers no one. At most one look reaches
// it, in the background, and the frames are drawn while that look still
// waits: a frame that waited would have given up on it and closed it.
func TestPairURLOffRender(t *testing.T) {
	isolateSettings(t, "")
	registerFonts()
	t.Setenv("GOREX_DIR", t.TempDir())
	section, remote := settingsUI.section, remoteUI
	t.Cleanup(func() { settingsUI.section, remoteUI = section, remote })
	gw := testrex.NewStalled(t)
	t.Setenv("GOREX_REXAPI_SOCKET", gw.Path)
	// The look ends before the socket goes.
	t.Cleanup(func() {
		waitForThat(t, func() bool { _, open := gw.Counts(); return open == 0 }, "the look to give up")
	})

	a := &App{}
	settingsUI.section = "developer"
	remoteUI.loaded, remoteUI.st = true, rex.RemoteStatus{}
	remoteUI.lan, remoteUI.lanStatus = rexapi.LANConfig{}, rexapi.LANStatus{}
	tt := ui.NewTester(a.settingsView, 780, 1400)
	for range 3 {
		tt.Frame()
	}
	if dials, open := gw.Counts(); dials > 1 || open != dials {
		t.Fatalf("drawing dialed the gateway %d times, %d given up", dials, dials-open)
	}
}

// TestLocalPairQRCloses: this machine's code stays open while its
// address works, and closes once the listener it names goes away.
func TestLocalPairQRCloses(t *testing.T) {
	isolateSettings(t, "")
	registerFonts()
	t.Setenv("GOREX_DIR", t.TempDir())
	section, remote, qr := settingsUI.section, remoteUI, hostsPairQR
	t.Cleanup(func() { settingsUI.section, remoteUI, hostsPairQR = section, remote, qr })
	gw := testrex.NewStalled(t)
	t.Setenv("GOREX_REXAPI_SOCKET", gw.Path)
	t.Cleanup(func() {
		waitForThat(t, func() bool { _, open := gw.Counts(); return open == 0 }, "the look to give up")
	})

	a := &App{}
	settingsUI.section = "developer"
	remoteUI.loaded, remoteUI.st = true, rex.RemoteStatus{}
	remoteUI.lan = rexapi.LANConfig{Enabled: true, Listen: "192.168.1.20:7297", Allow: []string{"192.168.1.0/24"}}
	remoteUI.lanStatus = rexapi.LANStatus{Running: true, Listening: "192.168.1.20:7297"}
	tt := ui.NewTester(a.settingsView, 780, 1400)
	tt.Frame()
	if !tt.HasText(locales.HostsRexQR()) {
		t.Fatalf("no QR button for the listener: %q", tt.Texts())
	}
	a.openLocalPairQR()
	tt.Frame()
	tt.Frame()
	if hostsPairQR == nil || hostsPairQR.address != "http://192.168.1.20:7297" {
		t.Fatalf("the code: %+v", hostsPairQR)
	}
	remoteUI.lanStatus = rexapi.LANStatus{Running: true}
	tt.Frame()
	if hostsPairQR != nil {
		t.Errorf("the code stayed for a listener that went away")
	}
}

// TestRemoteRelayRow draws the relay field, and refuses a URL in it.
func TestRemoteRelayRow(t *testing.T) {
	isolateSettings(t, "")
	registerFonts()
	t.Setenv("GOREX_DIR", t.TempDir())
	t.Cleanup(stopRemotePoll)
	a := &App{}
	settingsUI.section = "server"
	remoteUI.loaded, remoteUI.st, remoteUI.details = true, rex.RemoteStatus{Enabled: true, Relay: "derp.example.com"}, true
	remoteUI.relay, remoteUI.relayEdited, remoteUI.relayErr = "https://derp.example.com", true, ""
	tt := ui.NewTester(a.settingsView, 780, 1400)
	tt.Frame()
	for _, s := range []string{locales.RemoteRelay(), locales.RemoteRelayNote()} {
		if !tt.HasText(s) {
			t.Fatalf("no %q in %q", s, tt.Texts())
		}
	}
	save, ok := tt.Find(locales.RemoteLanSave())
	if !ok {
		t.Fatalf("no Save in %q", tt.Texts())
	}
	tt.ClickAt(save.X+save.W/2, save.Y+save.H/2)
	tt.Frame()
	if remoteUI.relayErr != locales.RemoteRelayInvalid() || !tt.HasText(locales.RemoteRelayInvalid()) {
		t.Errorf("a URL saved: %q", remoteUI.relayErr)
	}

	// A save the server does not take says why under the field, which
	// keeps what was typed.
	remoteUI.relay = "derp.example.com"
	tt.Frame()
	tt.ClickAt(save.X+save.W/2, save.Y+save.H/2)
	tt.Frame()
	if remoteUI.relayErr != locales.RemoteNotConnected() || !tt.HasText(remoteUI.relayErr) || !remoteUI.relayEdited || remoteUI.err != nil {
		t.Errorf("a failed save: %q, edited %v, page error %v", remoteUI.relayErr, remoteUI.relayEdited, remoteUI.err != nil)
	}
}
