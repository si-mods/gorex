//go:build !darwin && !linux

package rex

import (
	"net"
	"syscall"
)

// Enable other platforms after their receive-side interface isolation is
// verified. A socket bound only to an IP is not sufficient here.
func systemTailscaleClient() systemTailscale { return nil }

func tailnetSocketControl(net.Interface) func(string, string, syscall.RawConn) error {
	return func(string, string, syscall.RawConn) error { return errTailnetUnavailable }
}
