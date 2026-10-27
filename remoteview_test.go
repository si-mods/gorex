package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
)

var testKey = "nodekey:" + strings.Repeat("0123456789abcdef", 4)

// wantMessage checks got against want: "" for no message, else a part of it.
func wantMessage(t *testing.T, what, got, want string) {
	t.Helper()
	if (want == "") != (got == "") || !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to contain %q", what, got, want)
	}
}

func TestValidateClientKey(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{testKey, ""},
		{"  " + testKey + "\n", ""},
		{"", "Paste"},
		{testKey[8:], "not a public key"},
		{testKey[:20], "not a public key"},
		{testKey + "0", "not a public key"},
		{"nodekey:" + strings.Repeat("g", 64), "not a public key"},
	} {
		wantMessage(t, "validateClientKey("+tc.in+")", validateClientKey(tc.in), tc.want)
	}
}

func TestValidateNewClient(t *testing.T) {
	allowed := []rex.RemoteClient{{Name: "bob-desktop", PublicKey: testKey}}
	other := "nodekey:" + strings.Repeat("fedcba9876543210", 4)
	for _, tc := range []struct{ name, key, want string }{
		{"alice-laptop", other, ""},
		{" ", other, "Name the client"},
		{"alice-laptop", "x", "not a public key"},
		{"alice-laptop", " " + testKey, "bob-desktop"},
	} {
		wantMessage(t, "validateNewClient("+tc.name+")", validateNewClient(tc.name, tc.key, allowed), tc.want)
	}
}

func TestShortKey(t *testing.T) {
	if got, want := shortKey(testKey), "nodekey:012345…abcdef"; got != want {
		t.Errorf("shortKey = %q, want %q", got, want)
	}
	for _, s := range []string{"", "alice", "nodekey:abc"} {
		if got := shortKey(s); got != s {
			t.Errorf("shortKey(%q) = %q, want it unchanged", s, got)
		}
	}
}

func TestRemoteState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		st    rex.RemoteStatus
		down  bool
		label string
		kind  remoteKind
	}{
		{"off", rex.RemoteStatus{}, false, "Off", remoteOff},
		{"starting", rex.RemoteStatus{Enabled: true}, false, "Starting…", remoteStarting},
		{"listening", rex.RemoteStatus{Enabled: true, Running: true}, false, "Listening", remoteListening},
		{"retrying", rex.RemoteStatus{Enabled: true, LastError: "no route"}, false, "Error", remoteError},
		{"off with error", rex.RemoteStatus{LastError: "bad file"}, false, "Error", remoteError},
		{"down", rex.RemoteStatus{Enabled: true, Running: true}, true, "Server not reachable", remoteDown},
	} {
		label, detail, kind := remoteState(tc.st, tc.down)
		if label != tc.label || kind != tc.kind {
			t.Errorf("%s: got %q, %v; want %q, %v", tc.name, label, kind, tc.label, tc.kind)
		}
		if !strings.Contains(detail, tc.st.LastError) {
			t.Errorf("%s: detail %q lacks the server's error", tc.name, detail)
		}
	}
}

func TestPairingLeft(t *testing.T) {
	now := time.Now()
	for d, want := range map[time.Duration]string{
		5 * time.Minute: "5:00", 61*time.Second + 400*time.Millisecond: "1:01", 9 * time.Second: "0:09", -time.Second: "0:00",
	} {
		if got := pairingLeft(now.Add(d), now); got != want {
			t.Errorf("pairingLeft(%v) = %q, want %q", d, got, want)
		}
	}
}

// TestShareSwitch checks the Remote page's Share open sessions switch: it
// follows the server, changes nothing by itself as the server's answers
// come, and asks the server once toggled.
func TestShareSwitch(t *testing.T) {
	isolateSettings(t, "")
	registerFonts()
	t.Cleanup(stopRemotePoll)
	a := &App{}
	settingsUI.section = "remote"
	remoteUI.loaded, remoteUI.st = true, rex.RemoteStatus{}
	tt := ui.NewTester(a.settingsView, 780, 900)
	tt.Frame()
	if !tt.HasText("Share open sessions") || !tt.HasText("Allowed clients see and use the sessions open on this machine. Off: each sees only its own.") {
		t.Fatalf("texts %q", tt.Texts())
	}
	// The server's answers flip it, and the other switch: no call.
	for _, st := range []rex.RemoteStatus{{ShareSessions: true}, {Enabled: true, ShareSessions: true}, {}} {
		remoteUI.st = st
		tt.Frame()
		tt.Frame()
		if remoteUI.err != nil || remoteUI.busy {
			t.Fatalf("a change without input, at %+v", st)
		}
	}
	// Toggled, it calls the server: none here, which it says. The switch
	// is at the right of its card's title and note.
	title, _ := tt.Find("Share open sessions")
	note, _ := tt.Find("Allowed clients see and use the sessions open on this machine. Off: each sees only its own.")
	toggle := func() bool {
		y := (title.Y + note.Y + note.H) / 2
		for x := float32(775); x > note.X+note.W; x -= 4 {
			tt.ClickAt(x, y)
			tt.Frame()
			if remoteUI.err != nil {
				return true
			}
		}
		return false
	}
	remoteUI.unreachable = true
	tt.Frame()
	if toggle() {
		t.Error("toggled while the server is not reachable")
	}
	remoteUI.unreachable = false
	tt.Frame()
	if !toggle() || remoteUI.err() != locales.RemoteNotConnected() {
		t.Error("toggling did not ask the server")
	}
}

func TestServerMessage(t *testing.T) {
	if got := serverMessage(errors.New("rex: that client is allowed already")); got != "That client is allowed already" {
		t.Errorf("serverMessage = %q", got)
	}
}
