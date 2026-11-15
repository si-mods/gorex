//go:build darwin || linux || windows

package rex

// sessionSpawn is what startPTY starts: a program in a new pseudo-terminal
// of cols×rows.
type sessionSpawn struct {
	path string
	argv []string
	dir  string
	env  []string
	cols int
	rows int
	// user, when set, runs the program as that user, through the helper
	// exe (-session-exec), which clears the capabilities the server's
	// ambient set leaves on every child.
	user *sessionUser
	exe  string
}
