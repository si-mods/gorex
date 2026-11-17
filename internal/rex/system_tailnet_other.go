//go:build !darwin && !linux && !windows

package rex

import (
	"net"
	"syscall"
)

// tailnetBuiltin tells whether discovery runs on the built-in node.
// Tests set it to take the built-in side.
var tailnetBuiltin = false

// Enable other platforms after their receive-side interface isolation is
// verified. A socket bound only to an IP is not sufficient here.
func systemTailscaleClient() systemTailscale { return nil }

func tailnetSocketControl(net.Interface) func(string, string, syscall.RawConn) error {
	return func(string, string, syscall.RawConn) error { return errTailnetUnavailable }
}
