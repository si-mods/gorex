//go:build !windows

package rexapi

import "os"

// currentUID returns the account's Unix user ID.
func currentUID() int { return os.Getuid() }
