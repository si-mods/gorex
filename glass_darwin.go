package main

import "github.com/egoist/mygo/ui"

// paintLens paints nothing on macOS, which has only the system's glass:
// where the system draws none, before macOS 26, a layer is flat, as the
// tiled panes are. MyGo's glass is not linked here, so that nothing on
// macOS can paint it.
func paintLens(*ui.Painter, ui.Rect, [4]float32) bool { return false }
