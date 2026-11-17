//go:build windows

package rex

import (
	"strings"
	"testing"
)

var devicePathCases = []struct {
	path   string
	device bool
}{
	{`\\.\pipe\x`, true},
	{`\\.\PhysicalDrive0`, true},
	{`//./pipe/x`, true},
	{`\\?\pipe\x`, true},
	{`//?/PIPE/x`, true},
	{`\\?\GLOBALROOT\Device\x`, true},
	{`//?/globalroot/Device/x`, true},
	{`C:\x\file.txt`, false},
	{`\\?\C:\x\file.txt`, false},
	{`\\?\UNC\server\share\f`, false},
	{`\\server\share\f`, false},
	{`.\pipe\x`, false},
	{`\.\x`, false},
}

func TestIsDevicePath(t *testing.T) {
	for _, c := range devicePathCases {
		if got := isDevicePath(c.path); got != c.device {
			t.Errorf("isDevicePath(%q) = %v, want %v", c.path, got, c.device)
		}
	}
}

// TestOpenToSendRefusesDevicePaths must return at once: opening a pipe
// path for a get would wait on it.
func TestOpenToSendRefusesDevicePaths(t *testing.T) {
	for _, c := range devicePathCases {
		if !c.device {
			continue
		}
		f, _, err := openToSend(c.path)
		if err == nil {
			f.Close()
			t.Errorf("openToSend(%q) opened", c.path)
		} else if !strings.HasSuffix(err.Error(), notRegular) {
			t.Errorf("openToSend(%q) = %v, want the not-regular error", c.path, err)
		}
	}
}

func TestVolumePathRefusesDevicePaths(t *testing.T) {
	for _, c := range devicePathCases {
		if _, ok := volumePath(c.path); c.device && ok {
			t.Errorf("volumePath(%q) accepted", c.path)
		}
	}
}
