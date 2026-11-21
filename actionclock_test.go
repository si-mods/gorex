package main

import (
	"testing"
	"time"
)

// TestActionClockPauses: a question's time does not count toward the
// action's, and the time it had left continues after the answer.
func TestActionClockPauses(t *testing.T) {
	clock, ctx := newActionClock(200 * time.Millisecond)
	defer clock.stop()
	clock.pause()
	select {
	case <-ctx.Done():
		t.Fatal("the action's time ran while the question was open")
	case <-time.After(400 * time.Millisecond):
	}
	if clock.expiredTime() {
		t.Fatal("the action expired while the question was open")
	}
	clock.resume()
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the action's time never ended after the answer")
	}
	if !clock.expiredTime() {
		t.Fatal("the end was not an expiry")
	}
}

// TestActionClockEndsInTime: an action that returns before its time is
// not expired.
func TestActionClockEndsInTime(t *testing.T) {
	clock, ctx := newActionClock(time.Second)
	defer clock.stop()
	select {
	case <-ctx.Done():
		t.Fatal("the action's time ended at once")
	case <-time.After(50 * time.Millisecond):
	}
	clock.stop()
	if clock.expiredTime() {
		t.Fatal("a stopped clock expired")
	}
}
