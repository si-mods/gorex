package rex

import (
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Windows Firewall and Reachable at: whether an inbound TCP connection to
// this program on the relay's port passes on the active profiles. On
// Windows firewallQuery reads the firewall's state (firewall_windows.go);
// elsewhere it does not know, and nothing shows.

type firewallResult int

const (
	firewallUnknown firewallResult = iota
	firewallAllowed
	firewallBlocked
	// firewallBlockRule is blocked by a rule, as the one Windows makes when
	// its prompt is cancelled: a rule that allows does not help.
	firewallBlockRule
)

// firewallRuleName names the rules AllowFirewall adds.
const firewallRuleName = "GoRex Reachable at"

// firewallState is what firewallQuery reads, as the firewall's COM API
// (INetFwPolicy2) says it: the profiles active now, and the enabled
// inbound TCP rules of any of them.
type firewallState struct {
	Profiles []firewallProfile
	Rules    []firewallRule
}

type firewallProfile struct {
	Type      int  // NET_FW_PROFILE2_*: 1 domain, 2 private, 4 public
	Enabled   bool // FirewallEnabled
	BlockAll  bool // BlockAllInboundTraffic: no allow rule counts
	DefaultIn int  // DefaultInboundAction: 0 block, 1 allow
}

type firewallRule struct {
	App      string // the program, environment expanded; "" any
	Proto    int    // 6 TCP, 17 UDP, 256 any
	Ports    string // LocalPorts: "*", or ports and ranges
	Profiles int    // a mask of profile types
	Action   int    // 0 block, 1 allow
	Local    string // LocalAddresses
	Remote   string // RemoteAddresses
	Service  string // a service's name: then only that service's
}

// firewallDecide tells whether an inbound TCP connection to exe on ip and
// port passes. A block rule wins over an allow rule, as in the firewall.
// With several profiles active, blocked only when each blocks: which
// profile the address's network has is not read, and a false alarm is
// worse than none. A TCP rule alone allows: the relay is TCP, and without
// UDP a device still connects through it.
func firewallDecide(st firewallState, exe string, ip netip.Addr, port int) firewallResult {
	if len(st.Profiles) == 0 {
		return firewallUnknown
	}
	byRule := false
	for _, p := range st.Profiles {
		if !p.Enabled {
			return firewallAllowed
		}
		blocked, allowed := false, p.DefaultIn == 1 && !p.BlockAll
		for _, r := range st.Rules {
			if r.Profiles&p.Type == 0 || (r.Proto != 6 && r.Proto != 256) || r.Service != "" ||
				!portsMatch(r.Ports, port) || (r.App != "" && !samePath(r.App, exe)) {
				continue
			}
			switch r.Action {
			case 0:
				// Only a rule sure to cover the address and every source.
				if addrsMatch(r.Local, ip, false) && strings.TrimSpace(r.Remote) == "*" {
					blocked = true
				}
			case 1:
				if !p.BlockAll && addrsMatch(r.Local, ip, true) {
					allowed = true
				}
			}
		}
		if allowed && !blocked {
			return firewallAllowed
		}
		byRule = byRule || blocked
	}
	if byRule {
		return firewallBlockRule
	}
	return firewallBlocked
}

// portsMatch tells whether a rule's LocalPorts, as "*" or "80,7000-7999",
// covers port; a keyword, as RPC, does not.
func portsMatch(ports string, port int) bool {
	for _, p := range strings.Split(ports, ",") {
		p = strings.TrimSpace(p)
		lo, hi, isRange := strings.Cut(p, "-")
		if !isRange {
			hi = lo
		}
		a, err1 := strconv.Atoi(lo)
		b, err2 := strconv.Atoi(hi)
		if p == "*" || err1 == nil && err2 == nil && a <= port && port <= b {
			return true
		}
	}
	return false
}

// addrsMatch tells whether a rule's LocalAddresses covers ip: "*", an
// address, a subnet as a/24 or a/255.255.255.0, or a range a-b. An entry
// it cannot read counts as unread.
func addrsMatch(list string, ip netip.Addr, unread bool) bool {
	for _, e := range strings.Split(list, ",") {
		e = strings.TrimSpace(e)
		if e == "*" {
			return true
		}
		if lo, hi, ok := strings.Cut(e, "-"); ok {
			a, err1 := netip.ParseAddr(lo)
			b, err2 := netip.ParseAddr(hi)
			if err1 != nil || err2 != nil {
				if unread {
					return true
				}
				continue
			}
			if a.Compare(ip) <= 0 && ip.Compare(b) <= 0 {
				return true
			}
			continue
		}
		addr, bits, ok := strings.Cut(e, "/")
		a, err := netip.ParseAddr(addr)
		if err != nil {
			if unread {
				return true
			}
			continue
		}
		n := a.BitLen()
		if ok {
			if m, err := netip.ParseAddr(bits); err == nil {
				n, _ = net.IPMask(m.AsSlice()).Size()
			} else if n, err = strconv.Atoi(bits); err != nil {
				if unread {
					return true
				}
				continue
			}
		}
		if pre, err := a.Prefix(n); err == nil && pre.Contains(ip) {
			return true
		}
	}
	return false
}

// FirewallRules are the commands that add the inbound allow rules for the
// relay's port, TCP and UDP, with netsh at the path given. By port, not
// program, so that they hold as GoRex updates itself to another path; on
// any local address, so that they hold as Reachable at's address changes;
// on every profile, so that they hold as the network's category changes.
// Only GoRex listens on that port, and it lets in only paired devices.
// Built from constants only.
func FirewallRules(netsh string) []string {
	var out []string
	for _, proto := range []string{"TCP", "UDP"} {
		out = append(out, fmt.Sprintf(`%s advfirewall firewall add rule name="%s" dir=in action=allow protocol=%s localport=%d profile=any`,
			netsh, firewallRuleName, proto, RemotePort))
	}
	return out
}

// firewallElevated is the program and parameters AllowFirewall starts as
// administrator: cmd.exe, which removes the rules of that name, so that
// they are not doubled, and adds them again. sys is the system directory.
func firewallElevated(sys string) (file, params string) {
	netsh := filepath.Join(sys, "netsh.exe")
	add := FirewallRules(netsh)
	line := fmt.Sprintf(`%s advfirewall firewall delete rule name="%s" >nul 2>&1 & %s && %s`, netsh, firewallRuleName, add[0], add[1])
	return filepath.Join(sys, "cmd.exe"), `/d /s /c "` + line + `"`
}

// firewallRecheck is how old a firewall answer may be before
// remote.status asks again, in the background.
const firewallRecheck = 30 * time.Second

// firewallCheck asks the firewall whether devices can reach the relay of
// Reachable at, off the remote lock, and keeps the answer for
// remote.status. One check runs at a time; wait tells whether to wait for
// one running, or leave it.
func (s *Server) firewallCheck(wait bool) {
	r := &s.rem
	if wait {
		r.fwMu.Lock()
	} else if !r.fwMu.TryLock() {
		return
	}
	defer r.fwMu.Unlock()
	r.mu.Lock()
	at := ""
	if r.cfg.Enabled {
		at = r.cfg.ReachableAt
	}
	r.mu.Unlock()
	res := firewallUnknown
	if ip, err := netip.ParseAddr(at); err == nil {
		res = firewallQuery(ip, ownRelayPort)
	}
	r.fw.Store(int32(res))
	r.fwAt.Store(time.Now().UnixNano())
}
