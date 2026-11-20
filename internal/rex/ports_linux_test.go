//go:build linux

package rex

import "testing"

// TestParseProcNetAddr reads the local address of /proc/net/tcp and tcp6:
// little-endian groups and a hex port.
func TestParseProcNetAddr(t *testing.T) {
	cases := []struct {
		in   string
		addr string
		port int
	}{
		{"0100007F:1F90", "127.0.0.1", 8080},
		{"00000000:0050", "0.0.0.0", 80},
		{"00000000000000000000000001000000:1F90", "::1", 8080},
		{"00000000000000000000000000000000:0050", "::", 80},
	}
	for _, tc := range cases {
		addr, port, ok := parseProcNetAddr(tc.in)
		if !ok || addr != tc.addr || port != tc.port {
			t.Errorf("%s = %q %d %v, want %q %d", tc.in, addr, port, ok, tc.addr, tc.port)
		}
	}
	if _, _, ok := parseProcNetAddr("nope"); ok {
		t.Error("bad address accepted")
	}
}
