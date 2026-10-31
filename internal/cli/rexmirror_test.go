package cli

import (
	"testing"

	"gorex/internal/rexpeer"
)

// TestStatusOSC checks the report a mirror sends for a block's status.
func TestStatusOSC(t *testing.T) {
	got := statusOSC(rexpeer.Status{
		State: "working", App: "claude-code", Title: "build", Msg: "ok", Progress: 40,
	})
	want := "\x1b]7501;state=working:app=claude-code:progress=40:title=YnVpbGQ:msg=b2s\x1b\\"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if key := statusKey(rexpeer.Status{State: "idle", Progress: -1}, true); key == statusKey(rexpeer.Status{State: "idle", Progress: -1}, false) {
		t.Error("a status and no status share a key")
	}
}
