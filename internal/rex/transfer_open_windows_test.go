//go:build windows

package rex

import (
	"testing"
	"time"
)

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
