package main

import (
	"testing"

	"gorex/locales"
)

// TestSidebarHostIcon chooses a host's icon from its name's right click in
// the sidebar.
func TestSidebarHostIcon(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	a, tt := newTestApp(t)
	prefs = settings{TabPlacement: "left", Animation: "off"}
	h := newHostConn(savedHost{ID: "1", Name: "studio", Address: "/tmp/nowhere"})
	a.hosts = []*hostConn{h}
	a.st.hosts = a.hosts
	// A host away shows while it keeps a project of the space, from the
	// copy of it kept.
	a.st.workspaces = append(a.st.workspaces, &Workspace{ID: "p", Name: "site", Dir: "/srv/site", host: h})
	tt.Frame()
	if err := tt.RightClick("studio"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem(locales.HostsIcon(), "Mac mini"); err != nil {
		t.Fatalf("%v; menu %q", err, tt.Menu())
	}
	tt.Frame()
	if g := h.remoteGlyph(); g != "mac-mini" {
		t.Errorf("chose the Mac mini, shows %q", g)
	}
	if hs, _ := loadHosts(); len(hs) != 1 || hs[0].Icon != "mac-mini" {
		t.Errorf("hosts.json: %+v", hs)
	}
}
