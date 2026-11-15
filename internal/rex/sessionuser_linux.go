//go:build linux

package rex

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// sessionUserSupported tells whether a server may run sessions as another
// user: the ids and the capabilities to drop are Linux's.
const sessionUserSupported = true

// loginShell is the login shell of name, as its passwd entry gives it, ""
// when the entry has none.
func loginShell(name string) string {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ":")
		if len(fields) < 7 || fields[0] != name {
			continue
		}
		return fields[6]
	}
	return ""
}

// dropCapabilities clears the capabilities a child of the server is left
// with, so that a session holds none. The ambient set survives the change
// of user: a server that needs CAP_NET_RAW for the system Tailscale would
// hand it to every session, and to the programs in it. Dropping needs no
// privilege, and runs in the session's own process, before its program.
func dropCapabilities() error {
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil {
		return fmt.Errorf("gorex: %s: clearing the ambient capabilities: %w", sessionExecFlag, err)
	}
	// The two words of the version 3 header cover every capability.
	hdr := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	if err := unix.Capset(&hdr, &data[0]); err != nil {
		return fmt.Errorf("gorex: %s: clearing the capabilities: %w", sessionExecFlag, err)
	}
	return nil
}
