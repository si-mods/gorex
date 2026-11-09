package tailnet

import (
	"encoding/json"
	"net/netip"
	"testing"

	"tailscale.com/ipn/ipnstate"
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
	b, _ := json.Marshal(fromStatus(st))
	want := `{"state":"Running","name":"rexgo-kqzt.example.ts.net","ips":["100.64.0.7"],"suffix":"example.ts.net"}`
	if string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}
	b, _ = json.Marshal(fromStatus(&ipnstate.Status{BackendState: "NeedsLogin", AuthURL: "https://login.tailscale.com/a/x"}))
	if want := `{"state":"NeedsLogin","auth_url":"https://login.tailscale.com/a/x"}`; string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}
}

// Before Start, the calls that need the device fail, and Status says
// it is stopped.
func TestNotStarted(t *testing.T) {
	if Status() != `{"state":"Stopped"}` {
		t.Errorf("Status() = %s", Status())
	}
	if _, err := Proxy(); err == nil {
		t.Error("Proxy() did not fail")
	}
	if Login() == nil || Logout() == nil {
		t.Error("Login or Logout did not fail")
	}
	Stop()
}
