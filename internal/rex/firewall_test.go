package rex

import (
	"net/netip"
	"path/filepath"
	"testing"
)

func TestFirewallDecide(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "GoRex.exe")
	ip := netip.MustParseAddr("192.0.2.10")
	pub := []firewallProfile{{Type: 4, Enabled: true}}
	allowPort := firewallRule{Proto: 6, Ports: "7464", Profiles: 4, Action: 1, Local: "*", Remote: "*"}
	allowApp := firewallRule{App: exe, Proto: 256, Ports: "*", Profiles: 4, Action: 1, Local: "*", Remote: "*"}
	blockApp := allowApp
	blockApp.Action = 0
	with := func(r firewallRule, f func(*firewallRule)) firewallRule { f(&r); return r }
	for _, tc := range []struct {
		name string
		st   firewallState
		want firewallResult
	}{
		{"query failed", firewallState{}, firewallUnknown},
		{"firewall off", firewallState{Profiles: []firewallProfile{{Type: 4}}}, firewallAllowed},
		{"on, no rule", firewallState{Profiles: pub}, firewallBlocked},
		{"program rule", firewallState{Profiles: pub, Rules: []firewallRule{allowApp}}, firewallAllowed},
		{"another program's rule", firewallState{Profiles: pub, Rules: []firewallRule{with(allowApp, func(r *firewallRule) { r.App = `C:\other.exe` })}}, firewallBlocked},
		{"port rule, TCP only", firewallState{Profiles: pub, Rules: []firewallRule{allowPort}}, firewallAllowed},
		{"port rule, UDP only", firewallState{Profiles: pub, Rules: []firewallRule{with(allowPort, func(r *firewallRule) { r.Proto = 17 })}}, firewallBlocked},
		{"port range", firewallState{Profiles: pub, Rules: []firewallRule{with(allowPort, func(r *firewallRule) { r.Ports = "80,7000-7999" })}}, firewallAllowed},
		{"other port", firewallState{Profiles: pub, Rules: []firewallRule{with(allowPort, func(r *firewallRule) { r.Ports = "7465,RPC" })}}, firewallBlocked},
		{"a block rule wins", firewallState{Profiles: pub, Rules: []firewallRule{allowPort, blockApp}}, firewallBlockRule},
		{"a block rule for some sources", firewallState{Profiles: pub, Rules: []firewallRule{allowPort, with(blockApp, func(r *firewallRule) { r.Remote = "LocalSubnet" })}}, firewallAllowed},
		{"wrong profile", firewallState{Profiles: pub, Rules: []firewallRule{with(allowApp, func(r *firewallRule) { r.Profiles = 2 })}}, firewallBlocked},
		{"block all inbound", firewallState{Profiles: []firewallProfile{{Type: 4, Enabled: true, BlockAll: true}}, Rules: []firewallRule{allowApp}}, firewallBlocked},
		{"default allow", firewallState{Profiles: []firewallProfile{{Type: 4, Enabled: true, DefaultIn: 1}}}, firewallAllowed},
		{"a service's rule", firewallState{Profiles: pub, Rules: []firewallRule{with(allowPort, func(r *firewallRule) { r.Service = "svc" })}}, firewallBlocked},
		{"local address, as COM says it", firewallState{Profiles: pub, Rules: []firewallRule{with(allowPort, func(r *firewallRule) { r.Local = "192.0.2.10/255.255.255.255" })}}, firewallAllowed},
		{"local subnet", firewallState{Profiles: pub, Rules: []firewallRule{with(allowPort, func(r *firewallRule) { r.Local = "192.0.2.0/24" })}}, firewallAllowed},
		{"another local address", firewallState{Profiles: pub, Rules: []firewallRule{with(allowPort, func(r *firewallRule) { r.Local = "192.0.2.20-192.0.2.30" })}}, firewallBlocked},
		// Two profiles active: blocked only when both block.
		{"one of two profiles allows", firewallState{Profiles: []firewallProfile{{Type: 2, Enabled: true}, {Type: 4, Enabled: true}}, Rules: []firewallRule{with(allowApp, func(r *firewallRule) { r.Profiles = 2 })}}, firewallAllowed},
	} {
		if got := firewallDecide(tc.st, exe, ip, RemotePort); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFirewallElevated(t *testing.T) {
	sys := filepath.Join("C:", "Windows", "System32")
	file, params := firewallElevated(sys)
	netsh := filepath.Join(sys, "netsh.exe")
	if want := filepath.Join(sys, "cmd.exe"); file != want {
		t.Errorf("file %q, want %q", file, want)
	}
	want := `/d /s /c "` + netsh + ` advfirewall firewall delete rule name="GoRex Reachable at" >nul 2>&1 & ` +
		netsh + ` advfirewall firewall add rule name="GoRex Reachable at" dir=in action=allow protocol=TCP localport=7464 profile=any && ` +
		netsh + ` advfirewall firewall add rule name="GoRex Reachable at" dir=in action=allow protocol=UDP localport=7464 profile=any"`
	if params != want {
		t.Errorf("params\n%s\nwant\n%s", params, want)
	}
}
