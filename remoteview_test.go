package main

import (
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/internal/rexapi"
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

func TestServerMessage(t *testing.T) {
	if got := serverMessage(errors.New("rex: that client is allowed already")); got != "That client is allowed already" {
		t.Errorf("serverMessage = %q", got)
	}
}

// TestDevicesButtons checks the Devices group's buttons: the form that
// adds a client by its key shows only once Add by Key… is clicked, and
// Cancel hides it; Pair New Device… asks the server to start pairing, as
// Start pairing did, and is gone while the pairing window is open.
func TestDevicesButtons(t *testing.T) {
	isolateSettings(t, "")
	registerFonts()
	t.Cleanup(stopRemotePoll)
	a := &App{}
	settingsUI.section = "server"
	remoteUI.loaded, remoteUI.st, remoteUI.err = true, rex.RemoteStatus{Enabled: true}, nil
	tt := ui.NewTester(a.settingsView, 780, 1400)
	tt.Frame()
	if tt.HasText(locales.RemoteAddByKey()) {
		t.Fatal("the add-by-key form shows before Add by Key… is clicked")
	}
	if err := tt.Click(locales.RemoteAddByKeyAsk()); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if !tt.HasText(locales.RemoteAddByKey()) || !remoteUI.addingKey {
		t.Fatalf("no add-by-key form after the click; texts %q", tt.Texts())
	}
	if err := tt.Click(locales.RemoteCancel()); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if tt.HasText(locales.RemoteAddByKey()) {
		t.Fatal("Cancel did not hide the add-by-key form")
	}

	// No server here: the call says so, as every change of the page does.
	if err := tt.Click(locales.RemotePairNew()); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if remoteUI.err == nil || remoteUI.err() != locales.RemoteNotConnected() {
		t.Error("Pair New Device… did not ask the server")
	}
	remoteUI.st.Pairing = rex.RemotePairing{Open: true, ExpiresAt: time.Now().Add(time.Minute)}
	tt.Frame()
	if tt.HasText(locales.RemotePairNew()) || !tt.HasText(locales.RemotePairStop()) {
		t.Errorf("pairing open: texts %q", tt.Texts())
	}
}

// TestRexGoPairURL: Rex Go's code carries the address and the machine's
// name, a space as %20, which Rex Go reads.
func TestRexGoPairURL(t *testing.T) {
	s := rexGoPairURL("tcAbc-_9", "Yibo's Mac & mini+")
	if strings.Contains(s, "+") {
		t.Errorf("a + in %s", s)
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "rexgo" || u.Host != "tailcat" || u.Path != "" {
		t.Fatalf("%s: %v %+v", s, err, u)
	}
	if q := u.Query(); q.Get("a") != "tcAbc-_9" || q.Get("n") != "Yibo's Mac & mini+" {
		t.Errorf("%s reads as %v", s, q)
	}
}

// TestRexGoQR: Rex Go QR… shows while remote access listens; its code
// shows neither the address nor Copy, and closes once remote access
// stops.
func TestRexGoQR(t *testing.T) {
	isolateSettings(t, "")
	registerFonts()
	t.Cleanup(stopRemotePoll)
	qr := hostsPairQR
	t.Cleanup(func() { hostsPairQR = qr })
	a := &App{}
	settingsUI.section = "server"
	remoteUI.loaded, remoteUI.st, remoteUI.err = true, rex.RemoteStatus{Enabled: true}, nil
	tt := ui.NewTester(a.settingsView, 780, 1400)
	tt.Frame()
	if tt.HasText(locales.RemoteRexGoQR()) {
		t.Fatal("Rex Go QR… shows before remote access listens")
	}
	remoteUI.st.Running = true
	tt.Frame()
	// No server here: the call says so.
	if err := tt.Click(locales.RemoteRexGoQR()); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if remoteUI.err == nil || remoteUI.err() != locales.RemoteNotConnected() || hostsPairQR != nil {
		t.Error("Rex Go QR… did not ask the server")
	}
	// While a change is on its way, it waits, as every change does.
	remoteUI.busy, remoteUI.err = true, nil
	a.openRexGoQR()
	if remoteUI.err != nil {
		t.Error("Rex Go QR… asked while busy")
	}
	remoteUI.busy = false

	const secret = "tcSECRETADDRESS"
	hostsPairQR = &hostPairQR{host: "mac", image: qrImage(rexGoPairURL(secret, "mac")), open: true, rexGo: true}
	tt.Frame()
	if !tt.HasText(locales.RemoteRexGoQRHint()) {
		t.Fatalf("no hint in %q", tt.Texts())
	}
	for _, s := range tt.Texts() {
		if strings.Contains(s, secret) || s == locales.HostsRexQRCopy() {
			t.Errorf("the dialog shows %q", s)
		}
	}
	remoteUI.st.Running = false
	tt.Frame()
	if hostsPairQR != nil {
		t.Error("the code stayed once remote access stopped")
	}
}

// TestRemoteOff checks the Remote page with remote access off: the
// switches alone, with a hint, and Tailscale's discovery as a way in; the
// devices only for a request still waiting. On, the devices show, with
// this machine's details behind Show Details.
func TestRemoteOff(t *testing.T) {
	isolateSettings(t, "")
	registerFonts()
	t.Cleanup(stopRemotePoll)
	a := &App{}
	settingsUI.section = "server"
	remoteUI.loaded, remoteUI.err = true, nil
	remoteUI.st = rex.RemoteStatus{Tailnet: &rex.TailnetStatus{}, Allowed: []rex.RemoteClient{{Name: "bob-desktop", PublicKey: testKey}}}
	tt := ui.NewTester(a.settingsView, 780, 1400)
	tt.Frame()
	for _, s := range []string{locales.TailcatMode(), locales.RemoteOff(), locales.RemoteOffNote(), locales.TailnetMode()} {
		if !tt.HasText(s) {
			t.Errorf("off: no %q in %q", s, tt.Texts())
		}
	}
	for _, s := range []string{locales.RemoteShareLayout(), locales.RemoteDevices(), "bob-desktop",
		locales.RemoteShowDetails(), locales.RemoteMyKey(), locales.HostsNone(), locales.RemoteApiTitle()} {
		if tt.HasText(s) {
			t.Errorf("off: %q shows; texts %q", s, tt.Texts())
		}
	}

	// A request still waiting shows, alone.
	remoteUI.st.Pending = []rex.RemotePending{{Name: "alice-laptop", PublicKey: testKey}}
	tt.Frame()
	if !tt.HasText("alice-laptop") || tt.HasText("bob-desktop") || tt.HasText(locales.RemoteAddByKeyAsk()) {
		t.Errorf("off with a request: texts %q", tt.Texts())
	}
	remoteUI.st.Pending = nil

	// Tailscale's discovery running serves too.
	remoteUI.st.Tailnet.Running = true
	tt.Frame()
	if !tt.HasText(locales.RemoteShareLayout()) || !tt.HasText("bob-desktop") {
		t.Errorf("discovery running: texts %q", tt.Texts())
	}

	// On, the details open and close.
	remoteUI.st = rex.RemoteStatus{Enabled: true}
	tt.Frame()
	if !tt.HasText(locales.RemoteDevices()) || !tt.HasText(locales.RemoteShareLayout()) || tt.HasText(locales.RemoteOffNote()) {
		t.Fatalf("on: texts %q", tt.Texts())
	}
	if tt.HasText(locales.RemoteMyKey()) {
		t.Fatal("the details show before Show Details is clicked")
	}
	if err := tt.Click(locales.RemoteShowDetails()); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if !tt.HasText(locales.RemoteMyKey()) || !tt.HasText(locales.RemoteMyKeyNote()) || !tt.HasText(locales.RemoteHideDetails()) {
		t.Fatalf("details: texts %q", tt.Texts())
	}
	if err := tt.Click(locales.RemoteHideDetails()); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	if tt.HasText(locales.RemoteMyKey()) {
		t.Error("Hide Details did not hide them")
	}
}

// TestRemoteHostsPage checks that the hosts this machine connects to have
// a page of their own, and are no longer on the Remote page.
func TestRemoteHostsPage(t *testing.T) {
	isolateSettings(t, "")
	registerFonts()
	t.Cleanup(stopRemotePoll)
	a := &App{}
	settingsUI.section = "hosts"
	remoteUI.loaded, remoteUI.st = true, rex.RemoteStatus{Enabled: true}
	tt := ui.NewTester(a.settingsView, 780, 900)
	tt.Frame()
	for _, s := range []string{locales.HostsNote(), locales.HostsNone(), locales.HostsAdd()} {
		if !tt.HasText(s) {
			t.Errorf("hosts: no %q in %q", s, tt.Texts())
		}
	}
	if tt.HasText(locales.TailcatMode()) {
		t.Errorf("hosts: the Remote page shows; texts %q", tt.Texts())
	}
	settingsUI.section = "server"
	tt.Frame()
	if tt.HasText(locales.HostsAdd()) || tt.HasText(locales.HostsNote()) {
		t.Errorf("remote: the hosts show; texts %q", tt.Texts())
	}
}

// TestLocalAPIGroup draws the Developer page's Local API group: the
// primary endpoint's URL, then each other listener's address and whom it
// lets in, then the local network's listener with its controls, once and
// not again among the others; the gateway off, its row says so, and the
// local network's row shows still.
func TestLocalAPIGroup(t *testing.T) {
	isolateSettings(t, "")
	registerFonts()
	t.Setenv("GOREX_DIR", t.TempDir())
	t.Cleanup(stopRemotePoll)
	t.Cleanup(stopServerInfoPoll)
	a := &App{}
	settingsUI.section = "developer"
	remoteUI.loaded, remoteUI.st = true, rex.RemoteStatus{}
	remoteUI.lan = rexapi.LANConfig{Enabled: true, Listen: "192.0.2.10:7297", Allow: []string{"192.0.2.0/24"}}
	remoteUI.lanStatus = rexapi.LANStatus{Running: true, Listening: "192.0.2.10:7297"}
	remoteUI.lanListen, remoteUI.lanEdited = "", true
	settingsUI.apiLoaded, settingsUI.apiErr = true, ""
	settingsUI.apiInfo = rexapi.GatewayInfo{Listeners: []rexapi.Listener{
		{Endpoint: "unix:///tmp/a%20b/rexapi.sock", Addresses: []string{"/tmp/a b/rexapi.sock"}, Transport: "unix", Admission: rexapi.AdmissionPeer, Primary: true},
		{Endpoint: "127.0.0.1:0", Addresses: []string{"127.0.0.1:52061"}, Transport: "tcp", Admission: rexapi.AdmissionAnonymous},
		{Endpoint: "192.0.2.10:7297", Addresses: []string{"192.0.2.10:7297"}, Transport: "tcp", Admission: rexapi.AdmissionAnonymous},
		{Endpoint: "198.51.100.1:7297", Transport: "tcp", Admission: "login_server"},
	}}
	tt := ui.NewTester(a.settingsView, 780, 1400)
	tt.Frame()
	for _, s := range []string{
		locales.SettingsDeveloper(), locales.RemoteApiTitle(), locales.RemoteApiUrl(), "unix:///tmp/a%20b/rexapi.sock",
		"127.0.0.1:52061", locales.RemoteApiAnyone(),
		"198.51.100.1:7297", "login_server", locales.RemoteApiNote(),
		locales.RemoteLanSwitch(), "Listening on http://192.0.2.10:7297", locales.RemoteLanNetworks(), "192.0.2.0/24", locales.RemoteLanNote(),
	} {
		if !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	if tt.HasText(locales.RemoteApiPeer()) {
		t.Error("the primary endpoint's row says whom it lets in, which Rex's does not")
	}
	if slices.Contains(tt.Texts(), "192.0.2.10:7297") {
		t.Error("the local network's listener is listed among the others too")
	}
	if tt.HasText(locales.TailcatMode()) {
		t.Error("the Remote page's header shows on the Developer page")
	}

	// Off, the local network's row shows still, so that it can be turned on,
	// and a listener at its address is --listen's, marked.
	remoteUI.lan.Enabled, remoteUI.lanStatus = false, rexapi.LANStatus{}
	tt.Frame()
	if !tt.HasText(locales.RemoteLanSwitch()) || !tt.HasText(locales.RemoteLanOff()) || !slices.Contains(tt.Texts(), "192.0.2.10:7297") {
		t.Errorf("local network off: texts %q", tt.Texts())
	}

	settingsUI.apiErr = "no gateway"
	settingsUI.apiInfo = rexapi.GatewayInfo{}
	tt.Frame()
	if !tt.HasText(locales.RemoteApiUrl()) || !tt.HasText(locales.HostApiOff()) || tt.HasText("127.0.0.1:52061") || !tt.HasText(locales.RemoteLanSwitch()) {
		t.Errorf("gateway off: texts %q", tt.Texts())
	}
}

func TestAdmissionLabel(t *testing.T) {
	for _, c := range []struct {
		l     rexapi.Listener
		label string
		open  bool
	}{
		{rexapi.Listener{Transport: "unix", Admission: rexapi.AdmissionPeer}, locales.RemoteApiPeer(), false},
		{rexapi.Listener{Endpoint: "0.0.0.0:7297", Transport: "tcp", Admission: rexapi.AdmissionAnonymous}, locales.RemoteApiAnyone(), true},
		{rexapi.Listener{Transport: "unix", Admission: rexapi.AdmissionAnonymous}, locales.RemoteApiAnyone(), false},
		{rexapi.Listener{Transport: "tcp", Admission: "tailscale_owner"}, "tailscale_owner", false},
		{rexapi.Listener{Transport: "tcp"}, "", false},
	} {
		if label, open := admissionLabel(c.l); label != c.label || open != c.open {
			t.Errorf("admissionLabel(%+v) = %q, %v; want %q, %v", c.l, label, open, c.label, c.open)
		}
	}
}

func TestLanListener(t *testing.T) {
	lan := rexapi.LANConfig{Enabled: true, Listen: "192.0.2.10:7297"}
	at := rexapi.Listener{Endpoint: "192.0.2.10:7297", Transport: "tcp", Admission: rexapi.AdmissionAnonymous}
	if !lanListener(at, lan) {
		t.Error("the local network's listener is not told")
	}
	other := at
	other.Endpoint = "0.0.0.0:7297"
	if lanListener(other, lan) {
		t.Error("another listener is the local network's")
	}
	// Turned off, a listener at its address is --listen's.
	if lanListener(at, rexapi.LANConfig{Listen: "192.0.2.10:7297"}) {
		t.Error("a listener at a turned-off LAN's address is the LAN's")
	}
}
