//go:build !darwin

package main

import "testing"

// quarantined is true: only macOS marks downloads.
func quarantined(*testing.T, string) bool { return true }
