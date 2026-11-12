//go:build darwin || linux

package rex

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestGetRefusesSpecial: a get of a FIFO or a device is refused at once,
// never waiting on the FIFO's writer nor reading the device.
func TestGetRefusesSpecial(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	dir := t.TempDir()
	sid := transferSession(t, c, c, dir)
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{fifo, "fifo", "/dev/null", "/dev/zero"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		start := time.Now()
		rc, _, err := c.GetFile(ctx, sid, p)
		cancel()
		if err == nil {
			rc.Close()
		}
		if !NotRegular(err) || Folder(err) || time.Since(start) > 3*time.Second {
			t.Errorf("get of %s: %v after %v", p, err, time.Since(start))
		}
	}
}
