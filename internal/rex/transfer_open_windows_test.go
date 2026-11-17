//go:build windows

package rex

import (
	"testing"
	"time"
)

// TestIsDevicePathAllowsFileShares: a normal UNC file share is not a
// device path (opening it may still fail, for other reasons).
func TestIsDevicePathAllowsFileShares(t *testing.T) {
	for _, p := range []string{
		`\\server\share\file.txt`,
		`//server/share/pipe`,
		`\\?\UNC\server\share\file.txt`,
		`\\server\pipes\x`,
		`C:\pipe\x`,
	} {
		if isDevicePath(p) {
			t.Errorf("isDevicePath(%q) = true, want false", p)
		}
	}
}

// TestOpenToSendFileShareNotRefused: a file share on this host passes the
// guard; opening it fails later, not as a not-regular refusal.
func TestOpenToSendFileShareNotRefused(t *testing.T) {
	p := `\\localhost\share\file.txt`
	f, _, err := openToSend(p)
	if f != nil {
		f.Close()
	}
	if NotRegular(err) {
		t.Errorf("openToSend(%q) refused as not regular: %v", p, err)
	}
}

// TestOpenToSendRefusesDevicePaths: a device or pipe path is refused by
// name, before anything opens it, in both slash directions.
func TestOpenToSendRefusesDevicePaths(t *testing.T) {
	for _, p := range []string{
		`\\.\pipe\x`,
		`//./pipe/x`,
		`\\?\pipe\x`,
		`//?/pipe/x`,
		`\\?\GLOBALROOT\Device\x`,
		`//?/globalroot/Device/x`,
		`\\.\PIPE\x`,
		`\\localhost\pipe\x`,
		`\\127.0.0.1\pipe\x`,
		`\\?\UNC\localhost\pipe\x`,
		`//localhost/PIPE/x`,
		`//?/unc/localhost/pipe/x`,
		`\\server\pipe`,
	} {
		type result struct{ err error }
		done := make(chan result, 1)
		go func() {
			f, _, err := openToSend(p)
			if f != nil {
				f.Close()
			}
			done <- result{err}
		}()
		select {
		case r := <-done:
			if !NotRegular(r.err) {
				t.Errorf("openToSend(%q) = %v, want a not-regular refusal", p, r.err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("openToSend(%q) blocked", p)
		}
	}
}
