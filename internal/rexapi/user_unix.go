//go:build darwin || linux

package rexapi

import "os"

// currentUID and currentGID name the account the server runs as: a local
// client is the same account, on a socket only it may open.
func currentUID() int { return os.Getuid() }
func currentGID() int { return os.Getgid() }
