//go:build darwin || windows

package rex

import "errors"

// sessionUserSupported tells whether a server may run sessions as another
// user: the ids and the capabilities to drop are Linux's.
const sessionUserSupported = false

// loginShell is the login shell of name: no passwd entry here, and the
// sessions of this system never run as another user.
func loginShell(string) string { return "" }

// dropCapabilities clears the capabilities a session is left with: none,
// as this system has none, and a session here never drops to another user.
func dropCapabilities() error { return errors.New("gorex: sessions run as another user on Linux only") }
