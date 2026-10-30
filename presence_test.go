package main

import "testing"

// TestUserIdle: the machine tells how long ago the user last gave it
// input, a time that is never negative.
func TestUserIdle(t *testing.T) {
	idle, ok := userIdle()
	if !ok {
		t.Skip("no idle time on this platform")
	}
	if idle < 0 {
		t.Fatalf("idle time %v", idle)
	}
}
