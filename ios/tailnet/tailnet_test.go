package tailnet

import (
	"encoding/json"
	"net/netip"
	"testing"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/key"
)

func TestParseConfig(t *testing.T) {
	for _, tc := range []struct {
		json string
		ok   bool
	}{
		{`{"hostname":"rexgo-kqzt"}`, true},
		{`{"hostname":"rexgo-kqzt","control_url":"https://headscale.example.com","auth_key":"tskey-x"}`, true},
		{`{"hostname":"rexgo-kqzt","control_url":"http://192.168.1.2:8080"}`, true},
		{`{"hostname":"rexgo-kqzt","control_url":"http://headscale.example.com"}`, false},
		{`{"hostname":""}`, false},
		{`{"hostname":"-rexgo"}`, false},
		{`{"hostname":"rex go"}`, false},
		{`not json`, false},
	} {
		if _, err := parseConfig(tc.json); (err == nil) != tc.ok {
			t.Errorf("parseConfig(%s) = %v, want ok %v", tc.json, err, tc.ok)
		}
	}
}

func TestFromStatus(t *testing.T) {
	st := &ipnstate.Status{
		BackendState:   "Running",
		TailscaleIPs:   []netip.Addr{netip.MustParseAddr("100.64.0.7")},
		Self:           &ipnstate.PeerStatus{DNSName: "rexgo-kqzt.example.ts.net."},
		CurrentTailnet: &ipnstate.TailnetStatus{MagicDNSSuffix: "example.ts.net"},
	}
	st.Peer = map[key.NodePublic]*ipnstate.PeerStatus{
		{}: {DNSName: "mac-gorex.example.ts.net.", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.8")}, Online: true},
	}
	b, _ := json.Marshal(fromStatus(st))
	want := `{"state":"Running","name":"rexgo-kqzt.example.ts.net","ips":["100.64.0.7"],"suffix":"example.ts.net",` +
		`"peers":[{"name":"mac-gorex.example.ts.net","ips":["100.64.0.8"],"online":true}]}`
	if string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}
	b, _ = json.Marshal(fromStatus(&ipnstate.Status{BackendState: "NeedsLogin", AuthURL: "https://login.tailscale.com/a/x"}))
	if want := `{"state":"NeedsLogin","auth_url":"https://login.tailscale.com/a/x"}`; string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}
}

func TestPath(t *testing.T) {
	for _, tc := range []struct {
		p    ipnstate.PeerStatus
		want string
	}{
		{ipnstate.PeerStatus{Relay: "fra"}, ""},
		{ipnstate.PeerStatus{Active: true, Relay: "fra", CurAddr: "203.0.113.5:41641"}, "direct"},
		{ipnstate.PeerStatus{Active: true, Relay: "fra", PeerRelay: "198.51.100.2:7000:1"}, "peer-relay"},
		{ipnstate.PeerStatus{Active: true, Relay: "fra"}, "derp"},
	} {
		if got := path(&tc.p); got != tc.want {
			t.Errorf("path(%+v) = %q, want %q", tc.p, got, tc.want)
		}
	}
}

func TestValidHostname(t *testing.T) {
	for s, ok := range map[string]bool{"rexgo-kqzt": true, "a": true, "": false, "-rexgo": false, "rexgo-": false, "rex go": false, "rex.go": false} {
		if ValidHostname(s) != ok {
			t.Errorf("ValidHostname(%q) = %v", s, !ok)
		}
	}
	if SetHostname("rex go") == nil {
		t.Error("SetHostname took an invalid name")
	}
}

// Before Start, the calls that need the device fail, and Status says
// it is stopped. The proxy serves without it, the same for the process.
func TestNotStarted(t *testing.T) {
	if Status() != `{"state":"Stopped"}` {
		t.Errorf("Status() = %s", Status())
	}
	p1, err := Proxy()
	p2, _ := Proxy()
	if err != nil || p1 != p2 {
		t.Errorf("Proxy() = %s, %v, then %s; want one proxy", p1, err, p2)
	}
	if Login() == nil || Logout() == nil || SetHostname("rexgo-kqzt") == nil {
		t.Error("Login, Logout or SetHostname did not fail")
	}
	Stop()
}

// A proxy whose listener died, as iOS reclaims a suspended app's, is
// replaced by a new one that works.
func TestProxyHeals(t *testing.T) {
	j1, err := Proxy()
	if err != nil {
		t.Fatal(err)
	}
	proxMu.Lock()
	old := prox
	proxMu.Unlock()
	// At once, before Serve may have seen it.
	old.ln.Close()
	j2, err := Proxy()
	if err != nil || j2 == j1 {
		t.Fatalf("Proxy() after its listener died = %s, %v; want a new one", j2, err)
	}
	var p struct{ Address, Password string }
	json.Unmarshal([]byte(j2), &p)
	// It speaks SOCKS5 and takes its password: a target off the tailnet
	// is refused, not the credentials.
	if _, err := socksConnect(p.Address, "tsnet", p.Password, "example.com", 80); err == nil || err.Error() != "connect refused" {
		t.Errorf("the new proxy: %v; want the target refused", err)
	}
	if j3, _ := Proxy(); j3 != j2 {
		t.Error("a live proxy was replaced")
	}
}
