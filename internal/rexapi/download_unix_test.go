//go:build !windows

package rexapi

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestDownloadSpecial: a FIFO is not_regular, answered at once, and a
// file this user may not read is denied.
func TestDownloadSpecial(t *testing.T) {
	f := startFixture(t)
	c, sid, block, dir := downloadSession(t, f)
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"fifo", "/dev/null"} {
		if e := failure(t, downloadBegin(c, sid, block, p)); e["code"] != "not_regular" {
			t.Errorf("%s: %v", p, e)
		}
	}
	if os.Geteuid() == 0 {
		t.Log("root reads any file: the denied case is skipped")
		return
	}
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	if e := failure(t, downloadBegin(c, sid, block, secret)); e["code"] != "denied" {
		t.Errorf("an unreadable file: %v", e)
	}
}
