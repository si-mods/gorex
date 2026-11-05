package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
	if err := tt.ChooseMenuItem("Teleport to", "office-mac"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the pane on the host", func() bool {
		cur := a.tab()
		return p.closed && cur != nil && cur.host() == h && cur.Focus != nil && cur.Focus.SID != ""
	})
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
	if why := teleportBlocked(p); why != "vim is running: only a shell at its prompt or Claude Code can teleport" {
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
