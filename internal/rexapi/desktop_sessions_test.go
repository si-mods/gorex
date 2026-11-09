package rexapi

import (
	"encoding/json"
	"testing"

	"gorex/internal/rex"
)

// TestDesktopTerminalsUseExistingSessions checks that the live sessions a
// desktop made here, over system Tailscale or tailcat, and that no local
// tab shows, are one Rex session of their own, opened in place.
func TestDesktopTerminalsUseExistingSessions(t *testing.T) {
	raw := json.RawMessage(`{"sessions":[{"id":"local","tabs":[{"root":{"sid":"placed"}},{"root":{"sid":"proxy","host":"different-server"}}]}]}`)
	sessions := []rex.SessionInfo{
		{ID: "placed", Owner: "tailscale:current:peer"},
		{ID: "proxy", Owner: "tailscale:current:peer", Dir: "/work/windflow", Program: "codex"},
		{ID: "exited", Owner: "tailscale:current:peer", Exited: true},
		{ID: "tailcat", Owner: "nodekey:tailcat", Program: "vim"},
		{ID: "local-unplaced"},
	}
	mp := newMapper()
	m, err := mp.build(raw, "office", nil, sessions...)
	if err != nil {
		t.Fatal(err)
	}
	desktop := m.byID[sessionID("gorex-desktop-terminals")]
	if desktop == nil || len(desktop.windows) != 2 || len(desktop.blocks) != 2 {
		t.Fatalf("expected the two unplaced desktop terminals, got %#v", desktop)
	}
	b := desktop.blocks[0]
	if b.sid != "proxy" || b.id != blockID("proxy") || desktop.windows[0].label != "windflow · codex" {
		t.Fatalf("desktop window did not preserve the session: %#v, %#v", desktop.windows[0], b)
	}
	if desktop.blocks[1].sid != "tailcat" || desktop.blocks[1].id != blockID("tailcat") {
		t.Fatalf("a tailcat desktop's terminal is not shown: %#v", desktop.blocks[1])
	}
	if len(m.blocks) != 3 {
		t.Fatalf("should neither duplicate a local tab nor show exited or local sessions: %#v", m.blocks)
	}
}
