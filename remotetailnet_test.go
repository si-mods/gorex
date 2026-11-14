package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

// TestRemoteTailnetRows draws the Tailscale switch's rows for each state of
// the built-in node, and for the system side, which keeps its old hint.
func TestRemoteTailnetRows(t *testing.T) {
	isolateSettings(t, "")
	registerFonts()
	t.Setenv("GOREX_DIR", t.TempDir())
	t.Cleanup(stopRemotePoll)
	builtin := func(node string) *rex.TailnetStatus {
		return &rex.TailnetStatus{Enabled: true, Side: rex.TailnetBuiltin, Node: node}
	}
	running := builtin(rex.NodeRunning)
	running.Running, running.Account, running.Device = true, "me@example.com", "studio-gorex.example.ts.net"
	tagged := builtin("")
	tagged.Error = "This device is tagged, so it cannot find yours."
	offErr := builtin(rex.NodeOff)
	offErr.Error = "the gateway is in another process"
	runTagged := builtin(rex.NodeRunning)
	runTagged.Device, runTagged.Error = "studio-gorex.example.ts.net", "built-in Tailscale's device is tagged"

	tests := []struct {
		name    string
		tn      *rex.TailnetStatus
		want    []string
		absent  []string
		sign    bool
		noteNew bool
	}{
		{"needs login", builtin(rex.NodeNeedsLogin), []string{locales.TailnetSignInHint(), locales.TailnetSignIn()}, []string{locales.TailnetUnavailable()}, true, true},
		{"needs approval", builtin(rex.NodeNeedsApproval), []string{locales.TailnetApproval()}, []string{locales.TailnetUnavailable(), locales.TailnetSignIn()}, false, true},
		{"starting", builtin(rex.NodeStarting), []string{locales.TailnetStarting()}, []string{locales.TailnetUnavailable(), locales.TailnetSignIn()}, false, true},
		{"off", builtin(rex.NodeOff), []string{locales.TailnetOff()}, []string{locales.TailnetUnavailable(), locales.TailnetStarting()}, false, true},
		{"off with error", offErr, []string{offErr.Error}, []string{locales.TailnetStarting(), locales.TailnetOff()}, false, true},
		{"running tagged", runTagged, []string{runTagged.Device, runTagged.Error}, []string{locales.TailnetSignedIn(""), locales.TailnetSignIn()}, false, true},
		{"running", running, []string{locales.TailnetSignedIn("me@example.com"), "studio-gorex.example.ts.net"}, []string{locales.TailnetUnavailable(), locales.TailnetSignIn()}, false, true},
		{"tagged", tagged, []string{tagged.Error}, []string{locales.TailnetUnavailable(), locales.TailnetSignIn()}, false, true},
		{"system waiting", &rex.TailnetStatus{Enabled: true, Side: rex.TailnetSystem}, []string{locales.TailnetUnavailable()}, []string{locales.TailnetSignIn(), locales.TailnetStarting()}, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{}
			settingsUI.section = "server"
			remoteUI.loaded, remoteUI.details = true, true
			remoteUI.st = rex.RemoteStatus{Tailnet: tc.tn}
			tt := ui.NewTester(a.settingsView, 780, 1600)
			tt.Frame()
			for _, s := range tc.want {
				if !tt.HasText(s) {
					t.Errorf("no %q in %q", s, tt.Texts())
				}
			}
			for _, s := range tc.absent {
				if tt.HasText(s) {
					t.Errorf("%q shows in %q", s, tt.Texts())
				}
			}
			note := false
			for _, s := range tt.Texts() {
				note = note || strings.Contains(s, locales.TailnetModeNoteBuiltin())
			}
			if note != tc.noteNew {
				t.Errorf("built-in note shown %v, want %v", note, tc.noteNew)
			}
			if dir := os.Getenv("GOREX_TEST_SHOTS"); dir != "" { // for a look by eye
				f, err := os.Create(filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".png"))
				if err == nil {
					png.Encode(f, tt.Image())
					f.Close()
				}
			}
			if tc.sign {
				if _, ok := tt.Find(locales.TailnetSignIn()); !ok {
					t.Error("no Sign in button")
				}
			}
		})
	}
}
