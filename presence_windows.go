//go:build windows

package main

import (
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	presenceOnce sync.Once
	lastInput    *windows.LazyProc
	tickCount    *windows.LazyProc
)

func loadUser32() {
	lastInput = windows.NewLazySystemDLL("user32.dll").NewProc("GetLastInputInfo")
	tickCount = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount")
}

// lastInputInfo is LASTINPUTINFO: the tick count of the last input.
type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

// userIdle tells how long ago the last input on this machine was, system
// wide; ok is false where the system call failed.
func userIdle() (time.Duration, bool) {
	presenceOnce.Do(loadUser32)
	li := lastInputInfo{cbSize: uint32(unsafe.Sizeof(lastInputInfo{}))}
	r, _, _ := lastInput.Call(uintptr(unsafe.Pointer(&li)))
	if r == 0 {
		return 0, false
	}
	ticks, _, _ := tickCount.Call()
	// Both count milliseconds since boot, and their difference is the idle
	// time even when the counter wrapped: uint32 arithmetic wraps alike.
	idle := uint32(ticks) - li.dwTime
	return time.Duration(idle) * time.Millisecond, true
}
