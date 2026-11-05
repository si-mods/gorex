//go:build !darwin

package main

// quarantine marks a download on macOS only.
func quarantine(string) error { return nil }
