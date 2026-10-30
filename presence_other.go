//go:build !darwin && !windows

package main

import "time"

// userIdle tells nothing on this platform: its user counts as present, so
// a claim works as it always did.
func userIdle() (time.Duration, bool) { return 0, false }
