package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/internal/rexapi"
	"gorex/locales"
)

// TestRemoteLANCard draws the card of Rex's apps on the local network,
// turns it on, adds and removes a network, all saved in the gateway's
// state for the server's gateway to follow.
func TestRemoteLANCard(t *testing.T) {
	isolateSettings(t, "")
	registerFonts()
	t.Setenv("GOREX_DIR", t.TempDir())
	t.Cleanup(stopRemotePoll)
	a := &App{}
	settingsUI.section = "remote"
	remoteUI.loaded, remoteUI.st = true, rex.RemoteStatus{}
	remoteUI.lan, remoteUI.lanStatus, remoteUI.lanErr, remoteUI.lanAdd = rexapi.LANConfig{}, rexapi.LANStatus{}, "", ""
	tt := ui.NewTester(a.settingsView, 780, 1400)
	tt.Frame()
	for _, s := range []string{"Rex apps on the local network", "Off", "Networks allowed", "None: no device can connect."} {
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
