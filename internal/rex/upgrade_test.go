//go:build darwin || linux

package rex

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain runs the test binary as a server when Spawn or an upgrade
// starts it so, as main does the app's.
func TestMain(m *testing.M) {
	if server, err := ServerMain(os.Args[1:]); server {
		if err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// readUntil reads s until what it read matches re, for a few seconds.
func readUntil(t *testing.T, s *Stream, re string) string {
	t.Helper()
	var out bytes.Buffer
	buf := make([]byte, 4096)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for !regexp.MustCompile(re).Match(out.Bytes()) {
			n, err := s.Read(buf)
			out.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.Close()
		<-done
	}
	if !regexp.MustCompile(re).Match(out.Bytes()) {
		t.Fatalf("no %q in %q", re, out.String())
	}
	return out.String()
}

// TestUpgrade upgrades a server, of a process of its own, to another
// executable: the session, its shell and what it printed stay.
func TestUpgrade(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "rex")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("GOREX_DIR", dir)
	// The server is this test binary, started by Spawn.
	c, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Hello()
	if err != nil || !h.Upgrade {
		t.Fatalf("hello %+v %v", h, err)
	}
	defer func() {
		c.Shutdown()
		c.Close()
		time.Sleep(100 * time.Millisecond)
		if processAlive(h.PID) {
			syscall.Kill(h.PID, syscall.SIGKILL)
		}
		if b, err := os.ReadFile(filepath.Join(dir, "server.log")); err == nil && t.Failed() {
			t.Logf("server.log:\n%s", b)
		}
	}()
	// An app runs, so the sessions stay headless: one of a pane that
	// exits would go with its pane.
	app, err := ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	go app.TakeCommand()
	time.Sleep(50 * time.Millisecond)
	app.Hello() // answered after the take, which the server reads first
	info, err := c.Create(CreateOptions{Command: []string{"/bin/sh"}, Dir: "/tmp", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	s := c.Stream(info.ID, 80, 24)
	s.Resize(80, 24)
	s.Write([]byte("X=42; echo before-''upgrade; sleep 1000 & echo sleeper=$!\n"))
	out := readUntil(t, s, `sleeper=(\d+)`)
	sleeper, _ := strconv.Atoi(regexp.MustCompile(`sleeper=(\d+)`).FindStringSubmatch(out)[1])
	defer syscall.Kill(sleeper, syscall.SIGKILL)
	s.Close()
	// A session that exited keeps what it showed last.
	done, err := c.Create(CreateOptions{Command: []string{"/bin/sh", "-c", "printf '\\033]2;last-title\\007'; echo last-words; exit 5"}, Dir: "/tmp", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if l, _ := c.List(); len(l) == 2 && l[1].Exited {
			break
		}
	}

	// An executable that does not run, or does not read this server's
	// handoffs, as an older build, leaves the server as it was.
	bad := filepath.Join(dir, "not-executable")
	os.WriteFile(bad, []byte("nothing"), 0o600)
	older := filepath.Join(dir, "older")
	os.WriteFile(older, []byte("#!/bin/sh\necho 0\n"), 0o700)
	for _, exe := range []string{bad, older} {
		if _, err := c.Upgrade(exe); err == nil {
			t.Fatalf("upgraded to %s", exe)
		}
	}
	s = c.Stream(info.ID, 80, 24)
	s.Resize(80, 24)
	s.Write([]byte("echo still-''here\n"))
	readUntil(t, s, `still-here`)
	s.Close()

	// A copy of the test binary is another executable.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "rex-upgraded.test")
	if err := copyFile(self, exe); err != nil {
		t.Fatal(err)
	}
	c2, err := c.Upgrade(exe)
	if err != nil {
		t.Fatal(err)
	}
	c = c2
	h2, err := c.Hello()
	wantExe, wantTime := executable(exe)
	if err != nil || h2.PID != h.PID || h2.Exe != wantExe || !h2.ExeTime.Equal(wantTime) || h2.Exe == h.Exe {
		t.Fatalf("hello after upgrade %+v %v, before %+v", h2, err, h)
	}
	list, err := c.List()
	if err != nil || len(list) != 2 || list[0].ID != info.ID || list[0].PID != info.PID || list[0].Exited {
		t.Fatalf("list %+v %v, created %+v", list, err, info)
	}
	if in := list[1]; in.ID != done.ID || !in.Exited || in.ExitCode != 5 || in.Title != "last-title" {
		t.Fatalf("exited session %+v", in)
	}
	s = c.Stream(done.ID, 80, 24)
	s.Resize(80, 24)
	readUntil(t, s, `last-words`)
	s.Close()
	if !processAlive(info.PID) || !processAlive(sleeper) {
		t.Fatal("the shell or its job is gone")
	}
	s = c.Stream(info.ID, 80, 24)
	s.Resize(80, 24)
	readUntil(t, s, `before-upgrade`)
	s.Write([]byte("echo x$X\n"))
	readUntil(t, s, `x42`)
	s.Write([]byte("exit 3\n"))
	var in SessionInfo
	for deadline := time.Now().Add(5 * time.Second); !in.Exited && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if list, err := c.List(); err == nil && len(list) == 2 {
			in = list[0]
		}
	}
	if !in.Exited || in.ExitCode != 3 {
		t.Fatalf("after exit %+v", in)
	}
	s.Close()
}

// TestUpgradeKeepsAppliedLayout makes a tab with no window, upgrades the
// server, and finds the tab, its session and the layout's revision as they
// were.
func TestUpgradeKeepsAppliedLayout(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "rex")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("GOREX_DIR", dir)
	c, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Hello()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c.Shutdown()
		c.Close()
		time.Sleep(100 * time.Millisecond)
		if processAlive(h.PID) {
			syscall.Kill(h.PID, syscall.SIGKILL)
		}
	}()
	res, err := c.Command(LayoutCommand{Op: "tab.create", Dir: "/tmp", Command: []string{"/bin/sh"}, Name: "kept"})
	if err != nil || res.Code != "" || res.New == "" {
		t.Fatalf("tab.create: %+v %v", res, err)
	}
	before, err := c.LayoutState()
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "rex-upgraded.test")
	if err := copyFile(self, exe); err != nil {
		t.Fatal(err)
	}
	if c, err = c.Upgrade(exe); err != nil {
		t.Fatal(err)
	}
	after, err := c.LayoutState()
	if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Layout, before.Layout) {
		t.Fatalf("after the upgrade %d %s %v, before %d %s", after.Revision, after.Layout, err, before.Revision, before.Layout)
	}
	if !bytes.Contains(after.Layout, []byte(res.New)) || !bytes.Contains(after.Layout, []byte(`"kept"`)) {
		t.Errorf("the tab is gone: %s", after.Layout)
	}
	if list, err := c.List(); err != nil || len(list) != 1 || list[0].ID != res.New || list[0].Exited {
		t.Errorf("sessions %+v %v", list, err)
	}
	// The server goes on making changes after it.
	if res, err := c.Command(LayoutCommand{Op: "pane.split", SID: res.New, Command: []string{"/bin/sh"}}); err != nil || res.Code != "" {
		t.Errorf("split after the upgrade: %+v %v", res, err)
	}
}

// TestUpgradeKeepsWhatTheScreenTells upgrades a server and finds what a
// session's screen tells as it was: the modes its program set, which a
// paste follows, the images it shows, and an exited session's text and
// Ghostty snapshot.
func TestUpgradeKeepsWhatTheScreenTells(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "rex")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("GOREX_DIR", dir)
	c, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Hello()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c.Shutdown()
		c.Close()
		time.Sleep(100 * time.Millisecond)
		if processAlive(h.PID) {
			syscall.Kill(h.PID, syscall.SIGKILL)
		}
		if b, err := os.ReadFile(filepath.Join(dir, "server.log")); err == nil && t.Failed() {
			t.Logf("server.log:\n%s", b)
		}
	}()
	app, err := ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	go app.TakeCommand()
	time.Sleep(50 * time.Millisecond)
	app.Hello()
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "image.png"), append(append([]byte(nil), pngHeader...), "pixels"...), 0o600); err != nil {
		t.Fatal(err)
	}
	live, err := c.Create(CreateOptions{
		Command: []string{"/bin/sh", "-c", `printf '\033[?2004h\033_Gi=9,a=T,t=f;image.png\033\\'; echo READY; cat > pasted`},
		Dir:     work, Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitScreen(t, c, live.ID, "READY")
	done, err := c.Create(CreateOptions{Command: []string{"/bin/sh", "-c", "echo last-words"}, Dir: work, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); !sessionInfo(t, c, done.ID).Exited; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the session did not exit")
		}
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "rex-upgraded.test")
	if err := copyFile(self, exe); err != nil {
		t.Fatal(err)
	}
	if c, err = c.Upgrade(exe); err != nil {
		t.Fatal(err)
	}

	m, err := c.Modes(live.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, md := range m.Modes {
		want := md.Default || md.Number == 2004
		if md.Set != want {
			t.Errorf("mode %s set %v after the upgrade, want %v", md.Name, md.Set, want)
		}
	}
	if res, err := c.Capture(live.ID, 0); err != nil || !bytes.Contains(res.Screen, []byte("\x1b_Gi=9,")) {
		t.Errorf("the image is gone after the upgrade: %q %v", res.Screen, err)
	}
	if err := c.Paste(live.ID, "hello\n"); err != nil {
		t.Fatal(err)
	}
	c.Input(live.ID, []byte("\n"))
	if got := waitFile(t, filepath.Join(work, "pasted"), "\n\n"); !strings.Contains(got, "\x1b[200~hello\n\x1b[201~") {
		t.Errorf("the program read %q, want a bracketed paste", got)
	}

	if f, err := c.Format(done.ID, "text"); err != nil || !strings.Contains(string(f.Content), "last-words") {
		t.Errorf("the exited session's text %q %v", f.Content, err)
	}
	head, snap := framedAttach(t, c, done.ID, Attach{Snapshot: SnapshotGhostty})
	if head.Format != SnapshotGhostty || !bytes.HasPrefix(snap, []byte("GHOSTSNP")) {
		t.Errorf("the exited session's attach is of format %q", head.Format)
	}
}

// TestResumeAnExitedSession hands over an exited session whose screen is
// larger than what one read may rewrite, with an image: the resumed one
// replays the image, and its screen is the one it had.
func TestResumeAnExitedSession(t *testing.T) {
	seq := []byte("\x1b_Gi=9,a=T,f=100;AAAA\x1b\\")
	s := &session{cols: 80, rows: 24, p: &pty{}, exited: true}
	s.graphics.restore([][]byte{seq})
	s.screen = append(bytes.Repeat([]byte("x"), maxGraphicsRewrite+1), seq...)
	r, err := resumeSession(s.state(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.graphics.replay(); !bytes.Equal(got, seq) {
		t.Errorf("replay %q after the handoff, want %q", got, seq)
	}
	if !bytes.Equal(r.screen, s.screen) {
		t.Errorf("a screen of %d bytes after the handoff, want %d", len(r.screen), len(s.screen))
	}
}

// TestResumeKeepsTheCursor hands over an exited session whose last image
// moved the cursor: the resumed one's cursor is where it was, not moved
// again by the image's replay.
func TestResumeKeepsTheCursor(t *testing.T) {
	var img bytes.Buffer
	png.Encode(&img, image.NewGray(image.Rect(0, 0, 40, 60)))
	seq := []byte("\x1b_Gi=9,a=T,f=100;" + base64.StdEncoding.EncodeToString(img.Bytes()) + "\x1b\\")
	s := &session{cols: 40, rows: 10, p: &pty{}, exited: true}
	if err := s.newVT(); err != nil {
		t.Fatal(err)
	}
	defer s.vt.Close()
	s.vt.Feed(append([]byte("hello\r\n"), seq...))
	s.graphics.restore([][]byte{seq})
	s.screen = append(s.vt.Snapshot(), s.graphics.replay()...)
	s.ghostty, _ = s.vt.GhosttySnapshot()
	r, err := resumeSession(s.state(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	before, after := headless(t), headless(t)
	before.Resize(40, 10)
	after.Resize(40, 10)
	if err := before.RestoreGhosttySnapshot(s.ghostty); err != nil {
		t.Fatal(err)
	}
	if err := after.RestoreGhosttySnapshot(r.ghostty); err != nil {
		t.Fatal(err)
	}
	if b, a := before.Snapshot(), after.Snapshot(); !bytes.Equal(b, a) {
		t.Errorf("after the handoff the screen ends %q, want %q", a[max(len(a)-12, 0):], b[max(len(b)-12, 0):])
	}
}

// TestResumeAnOlderHandoff resumes an exited session of a handoff an
// older build wrote, which has its screen and nothing it tells.
func TestResumeAnOlderHandoff(t *testing.T) {
	screen := base64.StdEncoding.EncodeToString([]byte("\x1b[?2004h\x1b[Hold-words\x1b_Gi=4,a=T,f=100;AAAA\x1b\\"))
	old := `{"id":"s1","shell":"sh","created":"2026-11-01T00:00:00Z","pid":1,"master":-1,"cols":40,"rows":6,` +
		`"screen":"` + screen + `","title":"t","output":9,"bells":0,` +
		`"lastOutput":"2026-11-01T00:00:00Z","lastInput":"2026-11-01T00:00:00Z","reaped":true,"exited":true,"code":2}`
	var st sessionState
	if err := json.Unmarshal([]byte(old), &st); err != nil {
		t.Fatal(err)
	}
	s, err := resumeSession(st, func() {})
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := s.Format(FormatOptions{}); !strings.Contains(string(res.Content), "old-words") {
		t.Errorf("text %q", res.Content)
	}
	s.mu.Lock()
	snap, after, ok := s.ghosttyView()
	s.mu.Unlock()
	if !ok || !bytes.HasPrefix(snap, []byte("GHOSTSNP")) || bytes.Contains(after, []byte("\x1b_G")) {
		t.Errorf("ghostty view %v, after %q", ok, after)
	}
	if !s.modes.bracketed || s.dir != "" || s.code != 2 {
		t.Errorf("bracketed %v, dir %q, code %d", s.modes.bracketed, s.dir, s.code)
	}
	// A handoff of this build has the session's directory.
	st.Dir = "/work"
	if s, err := resumeSession(st, func() {}); err != nil || s.dir != "/work" {
		t.Errorf("dir after a handoff with one: %v", err)
	}
}

func copyFile(from, to string) error {
	r, err := os.Open(from)
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}
