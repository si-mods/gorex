//go:build !darwin && !linux

package rexapi

// currentUID and currentGID name the account the server runs as. A
// Windows server has no numeric IDs; a local client is the same account.
func currentUID() int { return 0 }
func currentGID() int { return 0 }
