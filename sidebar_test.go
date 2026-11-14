package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestSidebarLeavesRoomForRightControls: with the sidebar on the left and
// the window's controls on the right, as on Linux, the panes start under
// the title bar, so their buttons do not lie under the controls.
func TestSidebarLeavesRoomForRightControls(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	_, tt := newTestApp(t)
	prefs = settings{TabPlacement: "left", Animation: "off"}
	tt.SetTitleBar(ui.TitleBar{Height: titleH, Right: 120})
	tt.Frame()
	settle(tt)
	r, ok := tt.Find("Split Right")
	if !ok {
		t.Fatalf("no Split Right; texts %q", tt.Texts())
	}
	if r.Y < titleH {
		t.Errorf("the pane's button is at y %v, in the controls' band of %v", r.Y, float32(titleH))
	}
}
