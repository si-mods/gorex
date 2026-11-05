package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorex/internal/rex"
)

// A rejected upgrade must not contact the server, even when another path
// names a copy of the same build.
func TestUpgradeServerChecksBuild(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "gorex")
	if err := os.WriteFile(exe, []byte("build"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		hello rex.Hello
		want  string
		out   string
	}{
		{name: "unsupported", want: "cannot upgrade in place"},
		{name: "already running", hello: rex.Hello{Upgrade: true, Exe: exe, ExeTime: info.ModTime()}, out: "already runs this build"},
		{name: "older build", hello: rex.Hello{Upgrade: true, Exe: "other", ExeTime: info.ModTime().Add(time.Minute)}, want: "newer build"},
		{name: "older at same path", hello: rex.Hello{Upgrade: true, Exe: exe, ExeTime: info.ModTime().Add(time.Minute)}, want: "newer build"},
		{name: "same time at another path", hello: rex.Hello{Upgrade: true, Exe: "other", ExeTime: info.ModTime()}, want: "newer build"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := upgradeServer(nil, tc.hello, exe, &out)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("upgrade: %v; want %q", err, tc.want)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.out) {
				t.Fatalf("output %q; want %q", out.String(), tc.out)
			}
		})
	}
}
