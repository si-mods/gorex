//go:build darwin

package main

import (
	"sync"
	"time"

	"github.com/ebitengine/purego"
)

var (
	presenceOnce   sync.Once
	cgSecondsSince func(state int32, eventType uint32) float64
)

const (
	// kCGEventSourceStateHIDSystemState: the state of the system's input
	// devices; kCGAnyInputEventType, for any event from them.
	cgHIDSystemState    = 1
	cgAnyInputEventType = 0xFFFFFFFF
)

func loadCoreGraphics() {
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	purego.RegisterLibFunc(&cgSecondsSince, lib, "CGEventSourceSecondsSinceLastEventType")
}

// userIdle tells how long ago the last input on this machine was, system
// wide; ok is false where CoreGraphics could not be loaded.
func userIdle() (time.Duration, bool) {
	presenceOnce.Do(loadCoreGraphics)
	if cgSecondsSince == nil {
		return 0, false
	}
	secs := cgSecondsSince(cgHIDSystemState, cgAnyInputEventType)
	return time.Duration(secs * float64(time.Second)), true
}
