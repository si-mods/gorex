//go:build windows

package rexapi

import "testing"

// A path a client sends absolute that names a device or a pipe is read by
// nothing here: opening or looking at it may wait on it forever. A plain
// drive path and a share are places a user names, as openToSend takes
// them, and they stay.
func TestResolveDirRefusesDevices(t *testing.T) {
	for _, p := range []string{
		`\\.\pipe\x`,
		`//./pipe/x`,
		`\\?\pipe\x`,
		`\??\C:\Windows`,
		`\\?\GLOBALROOT\Device\NamedPipe\x`,
		`\\;LanmanRedirector\localhost\pipe\x`,
	} {
		if dir, status := resolveDir(p, `C:\work`, `C:\Users\x`); status != "missing" {
			t.Errorf("resolveDir(%q) = %q, %q, want missing", p, dir, status)
		}
	}
	for _, p := range []string{`C:\work\sub`, `\\server\share\sub`} {
		if _, status := resolveDir(p, "", ""); status != "ok" {
			t.Errorf("resolveDir(%q) = %q, want ok", p, status)
		}
	}
}
