package rex

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
	"tailscale.com/client/local"
)

// tailnetBuiltin tells whether discovery runs on the built-in node. Linux
// keeps the system client. Tests set it to take the built-in side.
var tailnetBuiltin = false

func systemTailscaleClient() systemTailscale { return &local.Client{} }

func tailnetSocketControl(dev net.Interface) func(string, string, syscall.RawConn) error {
	return func(_, _ string, raw syscall.RawConn) error {
		var optErr error
		if err := raw.Control(func(fd uintptr) {
			optErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, dev.Name)
		}); err != nil {
			return err
		}
		return optErr
	}
}
