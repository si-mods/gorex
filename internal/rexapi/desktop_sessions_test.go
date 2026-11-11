package rexapi

import (
	"encoding/json"
	"testing"

	"gorex/internal/rex"
)

// TestDesktopTerminalsUseExistingSessions checks that the live sessions a
// desktop of this machine's user made here and that no local tab shows
// are one Rex session of their own, opened in place: a system Tailscale
// peer's, and every paired tailcat client's. Local sessions are not
// listed.
func TestDesktopTerminalsUseExistingSessions(t *testing.T) {
	raw := json.RawMessage(`{"sessions":[{"id":"local","tabs":[{"root":{"sid":"placed"}},{"root":{"sid":"proxy","host":"different-server"}}]}]}`)
	sessions := []rex.SessionInfo{
		{ID: "placed", Owner: "tailscale:current:peer"},
		{ID: "proxy", Owner: "tailscale:current:peer", Dir: "/work/windflow", Program: "codex"},
		{ID: "exited", Owner: "tailscale:current:peer", Exited: true},
		{ID: "mine", Owner: "nodekey:mine", Program: "vim"},
		{ID: "theirs", Owner: "nodekey:theirs", Program: "top"},
		{ID: "local-unplaced"},
	}
	mp := newMapper()
	m, err := mp.build(raw, "office", nil, sessions...)
	if err != nil {
		t.Fatal(err)
	}
	desktop := m.byID[sessionID("gorex-desktop-terminals")]
	if desktop == nil || len(desktop.windows) != 3 || len(desktop.blocks) != 3 {
		t.Fatalf("expected the three unplaced desktop terminals, got %#v", desktop)
	}
	b := desktop.blocks[0]
	if b.sid != "proxy" || b.id != blockID("proxy") || desktop.windows[0].label != "windflow · codex" {
		t.Fatalf("desktop window did not preserve the session: %#v, %#v", desktop.windows[0], b)
	}
	for i, sid := range []string{"mine", "theirs"} {
		if b := desktop.blocks[i+1]; b.sid != sid || b.id != blockID(sid) {
			t.Fatalf("a tailcat desktop's terminal is not shown: %#v", b)
		}
	}
	if len(m.blocks) != 4 {
		t.Fatalf("should neither duplicate a local tab nor show exited or local sessions: %#v", m.blocks)
	}
}
