package main

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"gorex/internal/rex"
	"gorex/locales"

	"github.com/egoist/mygo/ui"
)

// TestSidebarViewSetting: the command toggles the sidebar's view and keeps
// it in the settings; it is there only with a sidebar and remote hosts,
// where the view shows.
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
	lists := func() bool {
		for _, it := range a.paletteItems() {
			if it.title == cmdSidebarView.Title() {
				return true
			}
		}
		return false
	}
	// Without remote hosts the view would show as the spaces' one.
	if lists() || cmdSidebarView.invoke(a, nil) == nil || prefs.SidebarView != "" {
		t.Fatalf("without hosts the command shows (%v) or runs (view %q)", lists(), prefs.SidebarView)
	}
	a.hosts = []*hostConn{newHostConn(savedHost{ID: "1", Name: "office"})}
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
// the window's space shows its tabs, and so does another the window
// showed, until closed, while one never opened is closed; a click on
// another shows it on its tab of that host, and the space left stays
// open. The strip and the chip of the space go, and without remote hosts
// the sidebar is the spaces' view again.
func TestSidebarHostView(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	a, tt := newTestApp(t)
	prefs = settings{TabPlacement: "left", SidebarView: "host", Animation: "off"}
	h := newHostConn(savedHost{ID: "1", Name: "office"})
	h.state = hostConnected
	a.hosts = []*hostConn{h}
	a.st.hosts = a.hosts // as syncHosts shares them
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
	if !tt.HasText("Home ") || !tt.HasText("Home2 ") || !tt.HasText("Away ") {
		t.Errorf("the space the window left does not stay open; texts %q", tt.Texts())
	}
	// Its chevron under this machine closes it there alone, and opens it
	// again.
	chevron := func(label string) {
		t.Helper()
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("no row %q; texts %q", label, tt.Texts())
		}
		tt.ClickAt(r.X+10, r.Y+r.H/2)
		tt.Frame()
		tt.Frame()
	}
	chevron("icy-ridge, " + locales.SessionTabs(2))
	if a.ws != first || tt.HasText("Home ") || !tt.HasText("Away ") || !tt.HasText("Here ") || !tt.HasText("Far ") {
		t.Errorf("closing the other space under this machine: window on %v, texts %q", a.ws.Name, tt.Texts())
	}
	chevron("icy-ridge, " + locales.SessionTabs(2))
	if !tt.HasText("Home ") {
		t.Errorf("opening the other space again shows %q", tt.Texts())
	}
	// A space the window never showed is closed, and opens on the tabs of
	// its layout.
	third := &Workspace{ID: "w3", Name: "side", layout: savedLayout{Tabs: []savedTab{
		{ID: "t1", Name: "notes", Root: &savedNode{SID: "n1"}},
		{ID: "t2", Root: &savedNode{SID: "n2", Prog: "vim"}},
	}}}
	a.st.workspaces = append(a.st.workspaces, third)
	tt.Frame()
	if tt.HasText("notes ") {
		t.Errorf("a space never opened shows its tabs; texts %q", tt.Texts())
	}
	chevron("side, " + locales.SessionTabs(2))
	if a.ws != first || !tt.HasText("notes ") || !tt.HasText("Vim ") || !tt.HasText("Home ") {
		t.Errorf("opening a space of a layout: window on %v, texts %q", a.ws.Name, tt.Texts())
	}
	a.st.workspaces = slices.DeleteFunc(a.st.workspaces, func(w *Workspace) bool { return w == third })
	tt.Frame()
	if _, ok := tt.Find(cmdNewSession.Title()); ok {
		t.Error("the strip of spaces shows in the host view")
	}
	if _, ok := tt.Find(locales.SessionLabel(first.Name)); ok {
		t.Error("the space's chip shows in the host view")
	}
	// The window's first row of its space renames it in place, from its
	// right click, without the chip.
	own := func() string { return first.Name + ", " + workspaceDetail(first, a, 1) }
	if err := tt.RightClick(own()); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem(cmdRenameSession.Title()); err != nil {
		t.Fatalf("%v; menu %q", err, tt.Menu())
	}
	tt.Frame()
	for range first.Name {
		tt.Key(0, ui.KeyBackspace)
	}
	tt.Type("warm-moss")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if first.Name != "warm-moss" || a.wsRenaming {
		t.Errorf("the row's rename left %q (renaming %v)", first.Name, a.wsRenaming)
	}
	if _, ok := tt.Find(locales.SessionLabel(first.Name)); ok {
		t.Error("renaming brought the chip back in the host view")
	}
	first.Name = "calm-fern"
	tt.Frame()
	// Its Color… opens the colors alone, by the row.
	if err := tt.RightClick(own()); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem(locales.SpaceColor() + "…"); err != nil {
		t.Fatalf("%v; menu %q", err, tt.Menu())
	}
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find("Rose"); !ok {
		t.Errorf("no colors from the space's menu; texts %q", tt.Texts())
	}
	if tt.HasText(locales.SessionListTitle()) {
		t.Errorf("Color… shows the list of spaces; texts %q", tt.Texts())
	}
	a.wsOpen = false
	tt.Frame()
	if a.wsColorOnly {
		t.Error("the popover keeps to the colors once closed")
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

	// Folded, this machine keeps another space only while it needs the user.
	if err := tt.Click(thisMachine()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("icy-ridge, " + locales.SessionTabs(2)); ok {
		t.Errorf("a folded host shows another space; texts %q", tt.Texts())
	}
	sid := second.layout.Tabs[0].Root.SID
	a.st.infos = map[string]rex.SessionInfo{sid: {ID: sid, Statuses: []rex.ProgramStatus{{ID: "a", State: "error", Updated: time.Now().Add(time.Second)}}}}
	tt.Frame()
	if _, ok := tt.Find("icy-ridge, " + locales.SessionTabs(2)); !ok {
		t.Errorf("a folded host hides the space that needs the user; texts %q", tt.Texts())
	}
	// Its tabs cannot show there: its chevron shows the space, as its row.
	chevron("icy-ridge, " + locales.SessionTabs(2))
	if a.ws != second {
		t.Errorf("the chevron of a space under a folded host left the window on %v", a.ws.Name)
	}
	a.showWorkspace(first)
	tt.Frame()
	a.st.infos = nil
	if err := tt.Click(thisMachine()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	// A session of the host's, in the hidden space, needs the user: the
	// host's last list tells, and the space's row under the host shows it,
	// folded too; the strip's status counts it.
	waits := rex.ProgramStatus{ID: "a", State: "blocked", Kind: "question", Updated: time.Now()}
	h.infos = map[string]rex.SessionInfo{"far2": {ID: "far2", Statuses: []rex.ProgramStatus{waits}}}
	if err := tt.Click("office"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("icy-ridge, " + locales.SessionTabs(1)); !ok {
		t.Errorf("a folded host hides the space whose session there needs the user; texts %q", tt.Texts())
	}
	if _, ok := tt.Find(statusTip(&waits)); !ok {
		t.Errorf("no status dot for the host's session; texts %q", tt.Texts())
	}
	if s := a.st.status(second); s == nil || s.State != "blocked" {
		t.Errorf("the space's status is %v, want the host's session's", s)
	}
	h.infos = nil
	if err := tt.Click("office"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

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
	if !tt.HasText("Away ") || !tt.HasText("Home ") || !tt.HasText("Far ") || !tt.HasText("Here ") {
		t.Errorf("after the switch the sidebar shows %q", tt.Texts())
	}

	// A click on a tab of the space left, which the window keeps, shows
	// it on that tab; one of a space shown from its layout too.
	if err := tt.Click("Here "); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if got := a.tab(); a.ws != first || got == nil || got.Name != "Here" {
		t.Fatalf("a click on a tab of the space left shows %v on %v", a.ws.Name, got)
	}
	second.unkeep()
	tt.Frame()
	if err := tt.Click("Home2 "); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Frame()
	if got := a.tab(); a.ws != second || got == nil || got.Name != "Home2" {
		t.Errorf("a click on a tab of a layout shows %v on %v", a.ws.Name, got)
	}

	// A space closed under a host stays closed there as the window goes
	// to show it; it opens where it was never closed.
	chevron("calm-fern, " + locales.SessionTabs(1)) // under this machine
	if tt.HasText("Here ") || !tt.HasText("Far ") {
		t.Fatalf("closing the space left under this machine shows %q", tt.Texts())
	}
	a.showWorkspace(first)
	tt.Frame()
	if a.ws != first || tt.HasText("Here ") || !tt.HasText("Far ") {
		t.Errorf("going back to the space closed under this machine shows %q", tt.Texts())
	}

	// Without remote hosts, the spaces' view.
	a.hosts = nil
	tt.Frame()
	if _, ok := tt.Find(cmdNewSession.Title()); !ok {
		t.Errorf("no strip of spaces without hosts; texts %q", tt.Texts())
	}
	if _, ok := tt.Find(locales.SessionLabel(a.ws.Name)); !ok {
		t.Errorf("no chip of the space without hosts; texts %q", tt.Texts())
	}
}

// TestSidebarHostViewSwitchKeepsRows: in the host view, as the window goes
// to show another space, from a row under another host, from a tab of a
// space it keeps, and from one of a layout, the tab rows stay where they
// are: none opens or closes again.
func TestSidebarHostViewSwitchKeepsRows(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	a, tt := newTestApp(t)
	prefs = settings{TabPlacement: "left", SidebarView: "host"}
	h := newHostConn(savedHost{ID: "1", Name: "office"})
	h.state = hostConnected
	a.hosts = []*hostConn{h}
	a.st.hosts = a.hosts
	far := func(name, sid string) {
		rp := &Pane{ID: a.id(), SID: sid, host: h, info: rex.SessionInfo{ID: sid}}
		rt := a.makeTab("")
		rt.Name, rt.Root = name, &Node{ID: a.id(), Pane: rp}
		rp.Tab, rp.Node = rt, rt.Root
		rt.setFocus(rp)
		a.tabs = append(a.tabs, rt)
	}
	first := a.ws
	first.Name = "calm-fern"
	a.tabs[0].Name = "Here"
	far("Far", "far1")
	a.newWorkspace()
	second := a.ws
	second.Name = "icy-ridge"
	a.tabs[0].Name = "Home"
	far("Away", "far2")
	a.showWorkspace(first)
	tt.Frame()
	settle(tt)

	rows := []string{"Here ", "Far ", "Home ", "Away "}
	at := map[string]ui.Rect{}
	for _, label := range rows {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("no row %q; texts %q", label, tt.Texts())
		}
		at[label] = r
	}
	// still checks the rows on the frames right after the switch, while
	// a row that opened or closed again would be moving.
	still := func(what string, want *Workspace) {
		t.Helper()
		for i := range 12 {
			tt.Advance(16 * time.Millisecond)
			tt.Frame()
			for _, label := range rows {
				if r, ok := tt.Find(label); !ok || r != at[label] {
					t.Fatalf("%s: frame %d: row %q at %v (found %v), was at %v", what, i, label, r, ok, at[label])
				}
			}
		}
		if a.ws != want {
			t.Fatalf("%s: the window shows %v", what, a.ws.Name)
		}
		settle(tt)
	}
	if err := tt.Click("icy-ridge, " + locales.SessionTabs(1)); err != nil {
		t.Fatal(err)
	}
	still("a space's row under the host", second)
	if err := tt.Click("Here "); err != nil {
		t.Fatal(err)
	}
	still("a tab the window keeps", first)
	second.unkeep()
	tt.Frame()
	settle(tt)
	if err := tt.Click("Home "); err != nil {
		t.Fatal(err)
	}
	still("a tab of a layout", second)
}

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
