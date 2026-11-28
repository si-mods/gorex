package config

import (
	"testing"
	"time"
)

func TestActionClockPauses(t *testing.T) {
	clock, ctx := NewActionClock(200 * time.Millisecond)
	defer clock.Stop()
	clock.Pause()
	select {
	case <-ctx.Done():
		t.Fatal("the action's time ran while the question was open")
	case <-time.After(400 * time.Millisecond):
	}
	if clock.Expired() {
		t.Fatal("the action expired while the question was open")
	}
	clock.Resume()
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the action's time never ended after the answer")
	}
	if !clock.Expired() {
		t.Fatal("the end was not an expiry")
	}
}

func TestActionClockEndsInTime(t *testing.T) {
	clock, ctx := NewActionClock(time.Second)
	defer clock.Stop()
	select {
	case <-ctx.Done():
		t.Fatal("the action's time ended at once")
	case <-time.After(50 * time.Millisecond):
	}
	clock.Stop()
	if clock.Expired() {
		t.Fatal("a stopped clock expired")
	}
}
