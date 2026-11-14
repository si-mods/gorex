//go:build !darwin

package rex

import "time"

// ServerExecutable is the executable a server of this build runs: the
// app's own, where a copy of an app bundle is a macOS matter.
func ServerExecutable() (string, time.Time) { return Executable() }
