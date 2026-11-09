package main

import (
	"encoding/json"
	"os"
	"testing"

	"gorex/internal/rex"
	"gorex/locales"
)

// TestSidebarViewSetting: the command toggles the sidebar's view and keeps
// it in the settings; the palette offers it only with a sidebar.
func TestSidebarViewSetting(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	a, _ := newTestApp(t)
	prefs = settings{TabPlacement: "left"}
	saved := func() string {
		t.Helper()
		b, err := os.ReadFile(settingsPath())
		if err != nil {
			t.Fatal(err)
		}
		var s settings
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatal(err)
		}
		return s.SidebarView
	}
	if err := cmdSidebarView.invoke(a, nil); err != nil {
		t.Fatal(err)
	}
	if prefs.SidebarView != "host" || saved() != "host" {
		t.Fatalf("after the toggle the view is %q, saved %q; want host", prefs.SidebarView, saved())
	}
	a.toggleSidebarView()
	if prefs.SidebarView != "" || saved() != "" {
		t.Fatalf("toggled back the view is %q, saved %q; want the default", prefs.SidebarView, saved())
	}

	lists := func() bool {
		for _, it := range a.paletteItems() {
			if it.title == cmdSidebarView.Title() {
				return true
			}
		}
		return false
	}
	if !lists() {
		t.Error("the palette does not offer the view beside a sidebar")
	}
	prefs.TabPlacement = ""
	if lists() {
		t.Error("the palette offers the view with the tabs above")
	}
}

// TestSidebarHostView lists every space under the hosts it has tabs on: a
// space with tabs on two hosts shows under both, with its count of each;
// the window's space shows its tabs, another space is closed, and a click
// on it shows it on its tab of that host. The strip of spaces goes, and
// without remote hosts the sidebar is the spaces' view again.
func TestSidebarHostView(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	a, tt := newTestApp(t)
	prefs = settings{TabPlacement: "left", SidebarView: "host", Animation: "off"}
	h := newHostConn(savedHost{ID: "1", Name: "office"})
	h.state = hostConnected
	a.hosts = []*hostConn{h}
	far := func(name, sid string) *Tab {
		rp := &Pane{ID: a.id(), SID: sid, host: h, info: rex.SessionInfo{ID: sid}}
		rt := a.makeTab("")
		rt.Name, rt.Root = name, &Node{ID: a.id(), Pane: rp}
		rp.Tab, rp.Node = rt, rt.Root
		rt.setFocus(rp)
		a.tabs = append(a.tabs, rt)
		return rt
	}
	first := a.ws
	first.Name = "calm-fern"
	a.tabs[0].Name = "Here"
	far("Far", "far1")

	// A second space with two tabs here and one on the host, which the
	// window leaves for the first.
	a.newWorkspace()
	second := a.ws
	second.Name = "icy-ridge"
	a.tabs[0].Name = "Home"
	a.newTab(os.TempDir()).Name = "Home2"
	far("Away", "far2")
	a.selectTab(0)
	a.showWorkspace(first)
	tt.Frame()

	for _, label := range []string{
		"calm-fern, " + locales.SessionTabs(1), // under each host
		"icy-ridge, " + locales.SessionTabs(2), // this machine
		"icy-ridge, " + locales.SessionTabs(1), // the host
	} {
		if _, ok := tt.Find(label); !ok {
			t.Errorf("no row %q; texts %q", label, tt.Texts())
		}
	}
	if !tt.HasText("Here ") || !tt.HasText("Far ") {
		t.Errorf("the window's space does not show its tabs; texts %q", tt.Texts())
	}
	if tt.HasText("Home ") || tt.HasText("Away ") {
		t.Errorf("another space shows its tabs; texts %q", tt.Texts())
	}
	if _, ok := tt.Find(cmdNewSession.Title()); ok {
		t.Error("the strip of spaces shows in the host view")
	}

	// The window's space closes under a host, and opens again.
	if err := tt.Click("calm-fern, " + locales.SessionTabs(1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Here ") || !tt.HasText("Far ") {
		t.Errorf("closing the space under this machine shows %q", tt.Texts())
	}
	if err := tt.Click("calm-fern, " + locales.SessionTabs(1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Here ") {
		t.Errorf("opening the space again shows %q", tt.Texts())
	}

	// A click on the second space under the host shows it, on that tab.
	if err := tt.Click("icy-ridge, " + locales.SessionTabs(1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if a.ws != second {
		t.Fatalf("the window shows %v, want the second space", a.ws.Name)
	}
	if got := a.tab(); got == nil || got.Name != "Away" {
		t.Errorf("the window chose %v, want its tab on the host", got)
	}
	if !tt.HasText("Away ") || !tt.HasText("Home ") || tt.HasText("Far ") {
		t.Errorf("after the switch the sidebar shows %q", tt.Texts())
	}

	// Without remote hosts, the spaces' view.
	a.hosts = nil
	tt.Frame()
	if _, ok := tt.Find(cmdNewSession.Title()); !ok {
		t.Errorf("no strip of spaces without hosts; texts %q", tt.Texts())
	}
}
