package rexapi

import (
	"encoding/json"
	"testing"

	"gorex/internal/rex"
)

// TestDesktopTerminalsUseExistingSessions checks that the live sessions a
// desktop of this machine's user made here and that no local tab shows
// are one Rex session of their own, opened in place: a system Tailscale
// peer's, and a tailcat client's the server shares every session with.
// Another paired client's unshared sessions are not listed.
func TestDesktopTerminalsUseExistingSessions(t *testing.T) {
	raw := json.RawMessage(`{"sessions":[{"id":"local","tabs":[{"root":{"sid":"placed"}},{"root":{"sid":"proxy","host":"different-server"}}]}]}`)
	sessions := []rex.SessionInfo{
		{ID: "placed", Owner: "tailscale:current:peer"},
		{ID: "proxy", Owner: "tailscale:current:peer", Dir: "/work/windflow", Program: "codex"},
		{ID: "exited", Owner: "tailscale:current:peer", Exited: true},
		{ID: "shared", Owner: "nodekey:mine", OwnerShared: true, Program: "vim"},
		{ID: "someone-else", Owner: "nodekey:theirs", Program: "secret"},
		{ID: "local-unplaced"},
		{ID: "local-claims-shared", OwnerShared: true},
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
	if desktop.blocks[1].sid != "shared" || desktop.blocks[1].id != blockID("shared") {
		t.Fatalf("a shared tailcat desktop's terminal is not shown: %#v", desktop.blocks[1])
	}
	if m.blocks[blockID("someone-else")] != nil {
		t.Error("another client's unshared session is listed")
	}
	if len(m.blocks) != 3 {
		t.Fatalf("should neither duplicate a local tab nor show exited, local or unshared sessions: %#v", m.blocks)
	}
}
