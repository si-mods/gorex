//go:build !darwin

package rex

import "errors"

// errNoJob is what spawnJob returns on a platform that has no job of its
// own to start the server as, where Spawn starts it as a detached process.
var errNoJob = errors.New("rex: no job to start the server as")

// spawnJob starts no job here.
func spawnJob(string) error { return errNoJob }
