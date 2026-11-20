//go:build windows

package rex

import (
	"encoding/binary"
	"net"
	"testing"
)

// TestWindowsRows reads the listener table of both address families: a
// count, then each row's local address, port and owning process.
func TestWindowsRows(t *testing.T) {
	v4 := make([]byte, 4+24)
	binary.LittleEndian.PutUint32(v4[0:], 1)
	copy(v4[4:8], net.IPv4(127, 0, 0, 1).To4())
	binary.BigEndian.PutUint16(v4[8:10], 8080)
	binary.LittleEndian.PutUint32(v4[20:24], 42)

	v6 := make([]byte, 4+56)
	binary.LittleEndian.PutUint32(v6[0:], 1)
	copy(v6[4:20], net.ParseIP("::1").To16())
	binary.BigEndian.PutUint16(v6[20:22], 443)
	binary.LittleEndian.PutUint32(v6[52:56], 43)

	want := map[int]bool{42: true, 43: true}
	got := append(windowsRows(v4, false, want), windowsRows(v6, true, want)...)
	if len(got) != 2 {
		t.Fatalf("rows = %v", got)
	}
	if got[0].Address != "127.0.0.1" || got[0].Port != 8080 || got[0].PID != 42 {
		t.Errorf("v4 = %+v", got[0])
	}
	if got[1].Address != "::1" || got[1].Port != 443 || got[1].PID != 43 {
		t.Errorf("v6 = %+v", got[1])
	}
	if rows := windowsRows(v4, false, map[int]bool{7: true}); len(rows) != 0 {
		t.Errorf("kept a process that is not in the tree: %v", rows)
	}
}
