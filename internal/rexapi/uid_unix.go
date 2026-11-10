//go:build !windows

package rexapi

import "os"

// currentUID returns the account's Unix user ID.
func currentUID() int { return os.Getuid() }

// unixAdmission is how the local socket lets callers in: by their
// credentials, as the user the gateway runs as (peerPrincipal).
const unixAdmission = admissionPeer
