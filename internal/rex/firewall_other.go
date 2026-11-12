//go:build !windows

package rex

import (
	"errors"
	"net/netip"
)

// firewallQuery does not know: only Windows Firewall is read.
func firewallQuery(netip.Addr, int) firewallResult { return firewallUnknown }

// AllowFirewall is for Windows Firewall only.
func AllowFirewall() error { return errors.New("rex: only Windows Firewall can be changed here") }
