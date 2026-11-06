package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
)

// gitIn runs git in dir for a test.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestTeleportPane teleports a shell pane from the pane's menu to a host:
// the pane becomes a tab of the host's, in a work tree of the repo there,
// and its own session ends.
func TestTeleportPane(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	home := testDir(t)
	t.Cleanup(func() { os.RemoveAll(home) })
	cfg := filepath.Join(home, "gitconfig")
	os.WriteFile(cfg, []byte("[user]\n\tname = T\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n[commit]\n\tgpgsign = false\n"), 0o600)
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	bare, work := filepath.Join(home, "remote.git"), filepath.Join(home, "elsewhere", "proj")
	gitIn(t, home, "init", "-q", "--bare", bare)
	gitIn(t, home, "clone", "-q", bare, work)
	os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644)
	gitIn(t, work, "add", ".")
	gitIn(t, work, "commit", "-q", "-m", "a")
	gitIn(t, work, "push", "-q", "origin", "HEAD")
	os.WriteFile(filepath.Join(work, "a.txt"), []byte("changed\n"), 0o644)

	a, tt := newTestApp(t)
	// The host finds the clone where the source has it under its home,
	// both machines' here: a home of the test's, once
	// the terminals found their library under the user's.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	fakeHosts(t)
	sock, remote := startHostServer(t)
	t.Cleanup(a.stopHosts)
	h := a.addHost(savedHost{ID: "h1", Name: "office-mac", Address: sock})
	waitFor(t, tt, "the host to connect", func() bool { return h.state == hostConnected })

	tab := a.newTab(work)
	p := tab.Focus
	waitFor(t, tt, "the shell", func() bool {
		refresh(a)
		return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt) && p.info.Idle
	})
	if why := teleportBlocked(p); why != "" {
		t.Fatalf("blocked: %s", why)
	}
	items := a.teleportItems(func(string) bool { return true })
	if len(items) != 1 || items[0].title != "Teleport to office-mac" || items[0].disabled {
		t.Fatalf("palette items %+v", items)
	}
	tt.Frame()
	name, _ := p.label()
	if err := tt.RightClick(name); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	if !slices.Contains(tt.Menu(), "Teleport to") {
		t.Fatalf("menu %q", tt.Menu())
	}
	// A banner of an earlier failure of the pane's goes with its success.
	p.teleportErr = "Teleport to office-mac failed: before"
	a.err = p.teleportErr
	if err := tt.ChooseMenuItem("Teleport to", "office-mac"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the pane on the host", func() bool {
		cur := a.tab()
		return p.closed && cur != nil && cur.host() == h && cur.Focus != nil && cur.Focus.SID != ""
	})
	if a.err != "" {
		t.Errorf("the banner stayed: %q", a.err)
	}
	// One tab of the session, though the host's list came meanwhile.
	for range 3 {
		refreshHosts(a)
		tt.Frame()
	}
	shows := 0
	for _, tb := range a.tabs {
		for _, q := range tb.allPanes() {
			if q.host == h && q.SID == a.tab().Focus.SID {
				shows++
			}
		}
	}
	if shows != 1 {
		t.Errorf("%d panes show the session", shows)
	}
	np := a.tab().Focus
	if !slices.Contains(sessionIDs(t, remote), np.SID) {
		t.Errorf("session %s is not the host's", np.SID)
	}
	waitFor(t, tt, "the source session to end", func() bool { return !slices.Contains(sessionIDs(t, a.client), p.SID) })
	trees, _ := filepath.Glob(filepath.Join(work, ".gorex", "teleport", "*", "a.txt"))
	if len(trees) != 1 {
		t.Fatalf("work trees %v", trees)
	}
	if b, _ := os.ReadFile(trees[0]); string(b) != "changed\n" {
		t.Errorf("a.txt there is %q", b)
	}
	if slices.Contains(a.tabs, tab) {
		t.Error("the pane's tab stayed")
	}
}

// TestTeleportBlocked checks the panes that cannot go, and that the
// palette shows them disabled with the reason, and the menu its reason.
func TestTeleportBlocked(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	waitFor(t, tt, "the shell", func() bool {
		refresh(a)
		return p.term != nil && p.info.Idle
	})
	tt.Frame()
	name, _ := p.label()
	if err := tt.RightClick(name); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	if err := tt.ChooseMenuItem("Teleport to", "No host is connected"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("the menu's reason: %v", err)
	}
	tt.CloseMenu()
	p.info.Idle, p.info.Program = false, "vim"
	if why := teleportBlocked(p); why != "vim is running: only a shell at its prompt, Claude Code or omp can teleport" {
		t.Errorf("blocked %q", why)
	}
	p.info = rex.SessionInfo{Program: "claude", AgentSession: "5d454f1b-0c2e-4b8a-9f00-0123456789ab", Status: &rex.ProgramStatus{State: "working"}}
	if why := teleportBlocked(p); !strings.Contains(why, "working") {
		t.Errorf("blocked %q", why)
	}
	p.host = &hostConn{}
	if why := teleportBlocked(p); why != "Only a pane of this machine can teleport" {
		t.Errorf("blocked %q", why)
	}
	p.host = nil
}

// TestTeleportOverlay checks the frosted overlay of a teleport under way:
// the host, the step, and a Cancel that stops it.
func TestTeleportOverlay(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	cancelled := false
	p.teleport = &teleportRun{host: &hostConn{savedHost: savedHost{Name: "office-mac"}}, step: rex.TeleportSending, cancel: func() { cancelled = true }}
	tt.Frame()
	for _, s := range []string{"Teleporting to office-mac", "Sending it to office-mac…"} {
		if !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	if teleportBlocked(p) != "Teleporting already" {
		t.Errorf("a pane teleporting may teleport again")
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if !cancelled {
		t.Error("Cancel did not cancel")
	}
}

// refreshHosts adopts what each host lists, as its watch does.
func refreshHosts(a *App) {
	for _, h := range a.hosts {
		if c := h.current(); c != nil {
			if infos, err := c.List(); err == nil {
				byID := map[string]rex.SessionInfo{}
				for _, in := range infos {
					byID[in.ID] = in
				}
				a.adopt(h, byID, hostLayout{})
			}
		}
	}
}

// TestAdoptLeavesTeleport checks that a session a teleport starts on a
// host, which the pane takes, does not open as a tab of its own as the
// host's list comes first.
func TestAdoptLeavesTeleport(t *testing.T) {
	a, _ := newTestApp(t)
	h := &hostConn{savedHost: savedHost{ID: "h1", Name: "office-mac"}}
	a.hosts = append(a.hosts, h)
	n := len(a.tabs)
	h.expecting("0123456789ab", true)
	a.adopt(h, map[string]rex.SessionInfo{"0123456789ab": {ID: "0123456789ab"}}, hostLayout{})
	if len(a.tabs) != n {
		t.Errorf("a tab of the session the teleport starts: %d tabs, were %d", len(a.tabs), n)
	}
	h.expecting("0123456789ab", false)
	if len(h.expect) != 0 {
		t.Errorf("still expected: %v", h.expect)
	}
}

// TestAdoptLeavesCLITeleport runs a CLI teleport's interleaving with the
// window following the server: the layout notes the session before the
// host lists it (pane.teleporting), so the window adopts no tab of it, and
// pane.teleported then shows it in the pane's place, active and named, in
// one tab.
func TestAdoptLeavesCLITeleport(t *testing.T) {
	a, tt := newTestApp(t)
	if err := os.WriteFile(hostsPath(), []byte(`{"hosts":[{"id":"h1","name":"office","address":"tcOFFICE"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &hostConn{savedHost: savedHost{ID: "h1", Name: "office", Address: "tcOFFICE"}}
	a.hosts = append(a.hosts, h)
	other := followApp(t, a)
	a.newTab(testShell.dir)
	caughtUp(t, a, tt)
	src := a.tab()
	src.Name = "work"
	a.changed()
	a.save()
	caughtUp(t, a, tt)
	sid := src.Focus.SID
	if res, err := other.Command(rex.LayoutCommand{Op: "pane.teleporting", SID: sid, Target: "0123456789ab", Host: "tcOFFICE"}); err != nil || res.Code != "" {
		t.Fatalf("pane.teleporting: %+v %v", res, err)
	}
	caughtUp(t, a, tt)
	n := len(a.tabs)
	// The host lists the session before the CLI tells the server it runs.
	a.adopt(h, map[string]rex.SessionInfo{"0123456789ab": {ID: "0123456789ab"}}, hostLayout{})
	if len(a.tabs) != n {
		t.Fatalf("the window adopted the teleport's session: %d tabs, were %d", len(a.tabs), n)
	}
	if res, err := other.Command(rex.LayoutCommand{Op: "pane.teleported", SID: sid, Target: "0123456789ab", Host: "tcOFFICE"}); err != nil || res.Code != "" {
		t.Fatalf("pane.teleported: %+v %v", res, err)
	}
	caughtUp(t, a, tt)
	var shown []*Tab
	for _, t := range a.tabs {
		for _, p := range t.allPanes() {
			if p.host == h && p.SID == "0123456789ab" {
				shown = append(shown, t)
			}
		}
	}
	if len(shown) != 1 || a.tab() != shown[0] || shown[0].Name != "work" || len(a.tabs) != n {
		t.Errorf("the host's session shows in %d tabs, active %v, named %q; %d tabs", len(shown), len(shown) == 1 && a.tab() == shown[0], a.tab().Name, len(a.tabs))
	}
}

// TestPaletteTitleWidth checks that a palette row's title keeps its width
// beside a long detail: a teleport row, disabled, with its reason.
func TestPaletteTitleWidth(t *testing.T) {
	a, tt := newTestApp(t)
	fakeHosts(t)
	sock, _ := startHostServer(t)
	t.Cleanup(a.stopHosts)
	h := a.addHost(savedHost{ID: "h1", Name: "tp-office-mac-studio", Address: sock})
	waitFor(t, tt, "the host to connect", func() bool { return h.state == hostConnected })
	p := a.tab().Focus
	title := "Teleport to tp-office-mac-studio"
	width := func(program string) float32 {
		t.Helper()
		p.info = rex.SessionInfo{Idle: program == "", Program: program, LastOutput: time.Now().Add(-time.Minute)}
		a.paletteOpen, a.paletteQuery = true, "teleport"
		tt.Frame()
		r, ok := tt.Find(title)
		if !ok {
			t.Fatalf("no row %q in %q", title, tt.Texts())
		}
		return r.W
	}
	alone := width("")
	beside := width("a-long-running-program-whose-name-takes-room")
	if beside < alone-1 {
		t.Errorf("the title is %v wide beside the detail, %v alone", beside, alone)
	}
}

// TestTabDropTeleportAsks drags a tab onto a tab of another host's, in the
// tab strip and the sidebar: it asks to teleport there, and neither
// merges nor moves. Onto a tab of its own host's, it moves as before.
func TestTabDropTeleportAsks(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	for _, place := range []string{"left", ""} {
		t.Run("placement="+place, func(t *testing.T) {
			prefs = settings{TabPlacement: place}
			a, src, dst := newMergeTestTabs()
			src.Name, dst.Name = "source", "target"
			h := &hostConn{savedHost: savedHost{ID: "h1", Name: "office-mac"}}
			a.hosts = []*hostConn{h}
			dst.Focus.host = h
			tt := ui.NewTester(func(c *ui.Context) {
				k := colorsOf(c)
				list := ui.Column(c)
				if place == "" {
					list = ui.Row(c)
				}
				list.Children(func() {
					for i, tab := range slices.Clone(a.tabs) {
						a.tabEvents(c, ui.Box(c.Key(tab.ID)).Size(120, 32).Label(tab.Name), k, i, tab)
					}
				})
			}, 300, 100)
			drag := func(from, to string, at float32) {
				t.Helper()
				f, _ := tt.Find(from)
				r, ok := tt.Find(to)
				if !ok {
					t.Fatalf("no %q in %q", to, tt.Texts())
				}
				x, y := r.X+r.W*at, r.Y+r.H/2
				if place != "" {
					x, y = r.X+r.W/2, r.Y+r.H*at
				}
				tt.Press(f.X+f.W/2, f.Y+f.H/2)
				tt.Frame()
				tt.Move(f.X+f.W/2+5, f.Y+f.H/2+5)
				tt.Frame()
				tt.Move(x, y)
				tt.Frame()
				tt.Release(x, y)
				tt.Frame()
			}
			drag("source", "target", 0.5)
			if ask := a.teleportAsk; !ask.open || ask.tab != src || ask.host != h {
				t.Fatalf("dropped on the host's tab: ask %+v", ask)
			}
			if len(a.tabs) != 2 || a.tabs[0] != src || len(src.panes()) != 2 || len(dst.panes()) != 1 {
				t.Fatalf("the drop moved or merged tabs: %d tabs, source %d panes", len(a.tabs), len(src.panes()))
			}
			a.teleportAsk = teleportAsk{}
			// Of one host, the target's leading edge moves it.
			dst.Focus.host = nil
			a.hosts = nil
			drag("target", "source", 0.1)
			if a.teleportAsk.open || a.tabs[0] != dst {
				t.Errorf("a drop on a tab of the same host: ask %v, first tab %q", a.teleportAsk.open, a.tabs[0].Name)
			}
		})
	}
}

// TestTeleportDropDialog drops a tab on a host's header in the sidebar: a
// tab of two panes is told why it cannot go, with no Teleport button; a
// tab of one pane is asked, and Teleport starts its teleport to the host.
func TestTeleportDropDialog(t *testing.T) {
	old := prefs
	t.Cleanup(func() { prefs = old })
	a, tt := newTestApp(t)
	prefs = settings{TabPlacement: "left"}
	fakeHosts(t)
	sock, _ := startHostServer(t)
	t.Cleanup(a.stopHosts)
	h := a.addHost(savedHost{ID: "h1", Name: "office-mac", Address: sock})
	waitFor(t, tt, "the host to connect", func() bool { return h.state == hostConnected })
	tab := a.tab()
	tab.Name = "source"
	drop := func() {
		t.Helper()
		tt.Frame()
		name, detail := tab.label()
		f, ok := tt.Find(name + " " + detail)
		header, ok2 := tt.Find("office-mac")
		if !ok || !ok2 {
			t.Fatalf("no tab or header in %q", tt.Texts())
		}
		x, y := f.X+f.W/2, f.Y+f.H/2
		tt.Press(x, y)
		tt.Frame()
		for _, yy := range []float32{y + 4, y + 10, y + 20, header.Y + header.H/2} {
			tt.Move(x, yy)
			tt.Frame()
		}
		tt.Release(x, header.Y+header.H/2)
		tt.Frame()
		if ask := a.teleportAsk; !ask.open || ask.tab != tab || ask.host != h {
			t.Fatalf("dropped on the header: ask %+v", ask)
		}
		if !tt.HasText("Teleport “source” to office-mac?") {
			t.Fatalf("no title in %q", tt.Texts())
		}
	}

	a.split(false)
	drop()
	if !tt.HasText("Only a tab with one pane can teleport") {
		t.Errorf("no reason in %q", tt.Texts())
	}
	if _, ok := tt.Find("Teleport"); ok {
		t.Error("a Teleport button for a tab of two panes")
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if a.teleportAsk.open {
		t.Fatal("Cancel left the question open")
	}

	a.closePane(tab.Focus)
	p := tab.Focus
	waitFor(t, tt, "the shell", func() bool {
		refresh(a)
		return len(tab.allPanes()) == 1 && p.term != nil && p.info.Idle
	})
	// Another tab is in front: the dragged one comes to it on Teleport,
	// so that its overlay shows.
	a.newTabOn(nil, t.TempDir())
	drop()
	if !tt.HasText("move to office-mac") {
		t.Errorf("no body in %q", tt.Texts())
	}
	if err := tt.Click("Teleport"); err != nil {
		t.Fatal(err)
	}
	// The temporary directory is no work tree: the teleport goes, and
	// fails there.
	waitFor(t, tt, "the teleport", func() bool { return p.teleport != nil || p.teleportErr != "" })
	if a.teleportAsk.open || !slices.Contains(a.tabs, tab) || a.tab() != tab {
		t.Errorf("after Teleport: ask %v, the tab kept %v, in front %v", a.teleportAsk.open, slices.Contains(a.tabs, tab), a.tab() == tab)
	}
}
