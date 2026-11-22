//go:build linux

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPackageOf(t *testing.T) {
	dir := t.TempDir()
	fake := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "dpkg-query"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	fake(`echo "fakegorex: /opt/gorex/gorex"`)
	if got := packageOf("/opt/gorex/gorex"); got != "fakegorex" {
		t.Fatalf("packageOf = %q; want fakegorex", got)
	}
	fake("exit 1")
	if got := packageOf("/opt/gorex/gorex"); got != "" {
		t.Fatalf("packageOf with a failing dpkg-query = %q; want no package", got)
	}
	if err := os.Remove(filepath.Join(dir, "dpkg-query")); err != nil {
		t.Fatal(err)
	}
	if got := packageOf("/opt/gorex/gorex"); got != "" {
		t.Fatalf("packageOf without dpkg-query = %q; want no package", got)
	}
}
