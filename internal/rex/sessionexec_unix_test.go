//go:build darwin || linux

package rex

import (
	"slices"
	"testing"
)

// A session of another user starts the server's helper as that user, which
// runs the program once the capabilities are gone; the pid stays the
// session's.
func TestSessionArgsRunAsUser(t *testing.T) {
	sp := sessionSpawn{
		path: "/bin/bash", argv: []string{"-bash"},
		exe: "/opt/gorex/gorex", user: &sessionUser{uid: 1001, gid: 1001},
	}
	path, args := sessionArgs(sp)
	if path != "/opt/gorex/gorex" {
		t.Errorf("path = %q, want the helper", path)
	}
	want := []string{"/opt/gorex/gorex", sessionExecFlag, "/bin/bash", "-bash"}
	if !slices.Equal(args, want) {
		t.Errorf("args = %q, want %q", args, want)
	}
	sp.user = nil
	path, args = sessionArgs(sp)
	if path != "/bin/bash" || !slices.Equal(args, []string{"-bash"}) {
		t.Errorf("the server's own user: %q %q", path, args)
	}
}
