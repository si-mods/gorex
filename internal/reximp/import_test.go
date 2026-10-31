package reximp

import (
	"testing"

	"gorex/internal/rexpeer"
)

// TestNames checks what a tab and a pane keep of Rex's names: a window's
// own label stays, Rex's made-up one does not, and a block named "shell"
// leaves its pane to follow the program.
func TestNames(t *testing.T) {
	id := "window:01a18b87-cffa-7e48-b7dc-929d2b63efe2"
	if got := tabName(rexpeer.Window{WindowID: id, Label: "Window 2b63efe2"}); got != "" {
		t.Errorf("a window's default label made a tab %q", got)
	}
	if got := tabName(rexpeer.Window{WindowID: id, Label: "window 2B63EFE2"}); got != "" {
		t.Errorf("the default label in another case made a tab %q", got)
	}
	if got := tabName(rexpeer.Window{WindowID: id, Label: "editor"}); got != "editor" {
		t.Errorf("a window's label made a tab %q", got)
	}
	if got := tabName(rexpeer.Window{WindowID: id}); got != "" {
		t.Errorf("a window without a label made a tab %q", got)
	}
	for _, label := range []string{"shell", "Shell", " shell ", ""} {
		if namedBlock(label) {
			t.Errorf("%q is a name a person gave", label)
		}
	}
	if !namedBlock("FromRex") {
		t.Error("a named block lost its label")
	}
}
