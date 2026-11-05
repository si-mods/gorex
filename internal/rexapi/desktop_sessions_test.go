package rexapi

import (
	"encoding/json"
	"testing"

	"gorex/internal/rex"
)

func TestDesktopTailscaleTerminalsUseExistingSessions(t *testing.T) {
	raw := json.RawMessage(`{"sessions":[{"id":"local","tabs":[{"root":{"sid":"placed"}},{"root":{"sid":"proxy","host":"different-server"}}]}]}`)
	sessions := []rex.SessionInfo{
		{ID: "placed", Owner: "tailscale:current:peer"},
		{ID: "proxy", Owner: "tailscale:current:peer", Dir: "/work/windflow", Program: "codex"},
		{ID: "exited", Owner: "tailscale:current:peer", Exited: true},
		{ID: "other-client", Owner: "nodekey:tailcat"},
	}
	mp := newMapper()
	m, err := mp.build(raw, "office", nil, sessions...)
	if err != nil {
		t.Fatal(err)
	}
	desktop := m.byID[sessionID("gorex-desktop-terminals")]
	if desktop == nil || len(desktop.windows) != 1 || len(desktop.blocks) != 1 {
		t.Fatalf("expected one unplaced desktop terminal, got %#v", desktop)
	}
	b := desktop.blocks[0]
	if b.sid != "proxy" || b.id != blockID("proxy") || desktop.windows[0].label != "windflow · codex" {
		t.Fatalf("desktop window did not preserve the session: %#v, %#v", desktop.windows[0], b)
	}
	if len(m.blocks) != 2 {
		t.Fatalf("should neither duplicate a local tab nor expose other sessions: %#v", m.blocks)
	}
}
