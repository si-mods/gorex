package rexapi

import (
	"encoding/json"
	"net/netip"
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

func TestSystemTailnetPairURLRequiresConfiguredListener(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	dir := TailnetDir()
	ip := netip.MustParseAddr("100.64.1.2")
	listen := "100.64.1.2:7297"
	if err := SaveLAN(dir, LANConfig{Enabled: true, Listen: listen, Allow: []string{"100.64.1.3/32"}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		status LANStatus
		want   string
	}{
		{"active", LANStatus{Running: true, Listening: listen}, "http://" + listen},
		{"not listening", LANStatus{Running: true}, ""},
		{"other interface", LANStatus{Running: true, Listening: "192.168.1.2:7297"}, ""},
		{"error", LANStatus{Running: true, Listening: listen, Error: "bind failed"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saveLANStatus(dir, tc.status)
			if got := SystemTailnetPairURL(ip); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
