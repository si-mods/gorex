//go:build linux

package rex

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// dropCapabilities leaves the process a session's, with none of the
// server's ambient capabilities, which survive the change of user: a
// server that needs CAP_NET_RAW for the system Tailscale must not hand it
// to every session. The test binary's child does it, so that a suite run
// as root keeps what the other tests need.
func TestDropCapabilities(t *testing.T) {
	if childDropCaps() {
		if err := dropCapabilities(); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile("/proc/self/status")
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			for _, name := range []string{"CapInh", "CapPrm", "CapEff", "CapAmb"} {
				if !strings.HasPrefix(line, name+":") {
					continue
				}
				if v := strings.TrimSpace(strings.TrimPrefix(line, name+":")); strings.Trim(v, "0") != "" {
					t.Errorf("%s = %s after dropping, want none", name, v)
				}
			}
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDropCapabilities$")
	cmd.Env = append(os.Environ(), envDropCaps+"=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the child: %v\n%s", err, out)
	}
}

// envDropCaps marks the child of TestDropCapabilities.
const envDropCaps = "GOREX_TEST_DROP_CAPS"

func childDropCaps() bool { return os.Getenv(envDropCaps) == "1" }
