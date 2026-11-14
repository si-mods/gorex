//go:build darwin || windows

package rex

import (
	"net"
	"syscall"
)

// tailnetBuiltin tells whether discovery runs on the built-in node. On
// Windows and macOS it does, always: a system Tailscale client is not
// used, installed or not.
var tailnetBuiltin = true

func systemTailscaleClient() systemTailscale { return nil }

func tailnetSocketControl(net.Interface) func(string, string, syscall.RawConn) error {
	return func(string, string, syscall.RawConn) error { return errTailnetUnavailable }
}
