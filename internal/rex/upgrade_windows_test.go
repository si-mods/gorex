package rex

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestUpgradeWindows upgrades a server, of a process of its own, to
// another executable, twice: the second takes over consoles the first
// did not make. The shells keep their state, what they print is neither
// lost nor doubled, they take a new size, and a hangup ends them. An
// upgrade that fails leaves the server serving.
func TestUpgradeWindows(t *testing.T) {
	dir, err := os.MkdirTemp("", "rex")
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
	if err != nil || !h.Upgrade {
		t.Fatalf("hello %+v %v", h, err)
	}
	pids := []int{h.PID}
	defer func() {
		c.Shutdown()
		c.Close()
		time.Sleep(200 * time.Millisecond)
		for _, pid := range pids {
			if p, err := os.FindProcess(pid); err == nil && processAlive(pid) {
				p.Kill()
			}
		}
		if b, err := os.ReadFile(filepath.Join(dir, "server.log")); err == nil && t.Failed() {
			t.Logf("server.log:\n%s", b)
		}
	}()
	// An app runs, so the sessions stay headless.
	app, err := ConnectExisting()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	go app.TakeCommand()
	time.Sleep(50 * time.Millisecond)
	app.Hello()
	work := t.TempDir()
	info, err := c.Create(CreateOptions{Command: []string{"cmd.exe"}, Dir: work, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	s := c.Stream(info.ID, 80, 24)
	s.Resize(80, 24)
	s.Write([]byte("set X=42\r"))
	s.Write([]byte("echo before^-upgrade\r"))
	readUntil(t, s, `before-upgrade`)
	s.Close()
	// Ticks that go on through the upgrades, on a screen tall enough that
	// the console, which repaints its screen, drops none as it waits.
	const ticks = 250
	ticker, err := c.Create(CreateOptions{Command: []string{"powershell.exe", "-NoLogo", "-NoProfile", "-Command",
		"for($i=1;$i -le " + strconv.Itoa(ticks) + ";$i++){'tick'+$i; Start-Sleep -Milliseconds 40}; 'ticks-'+'done'; Start-Sleep 60"},
		Dir: work, Cols: 80, Rows: 400})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if res, err := c.Capture(ticker.ID, 0); err == nil && strings.Contains(string(res.Screen), "tick5") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no ticks")
		}
	}

	// An executable that does not run, does not read this server's
	// handoffs, or does not take over, leaves the server as it was.
	bad := filepath.Join(dir, "not-executable.exe")
	os.WriteFile(bad, []byte("nothing"), 0o600)
	older := filepath.Join(dir, "older.bat")
	os.WriteFile(older, []byte("@echo 0\r\n"), 0o700)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	noResume := filepath.Join(dir, "rex-no-resume.test.exe")
	if err := copyFile(self, noResume); err != nil {
		t.Fatal(err)
	}
	for _, exe := range []string{bad, older, noResume} {
		if _, err := c.Upgrade(exe); err == nil {
			t.Fatalf("upgraded to %s", exe)
		} else {
			t.Logf("upgrade to %s: %v", filepath.Base(exe), err)
		}
	}
	c.Close()
	if c, err = ConnectExisting(); err != nil {
		t.Fatal(err)
	}
	if h, err := c.Hello(); err != nil || h.PID != pids[0] {
		t.Fatalf("hello after the failed upgrades %+v %v", h, err)
	}
	s = c.Stream(info.ID, 80, 24)
	s.Resize(80, 24)
	s.Write([]byte("echo still^-here\r"))
	readUntil(t, s, `still-here`)
	s.Close()

	for i := range 2 {
		exe := filepath.Join(dir, "rex-upgraded-"+strconv.Itoa(i)+".test.exe")
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
		if err != nil || h2.Exe != wantExe || !h2.ExeTime.Equal(wantTime) {
			t.Fatalf("hello after upgrade %+v %v", h2, err)
		}
		before := pids[len(pids)-1]
		pids = append(pids, h2.PID)
		for deadline := time.Now().Add(5 * time.Second); processAlive(before); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("the server that handed over, %d, still runs", before)
			}
		}
	}
	// The last server took the lock once the one before it exited.
	lock, err := os.OpenFile(filepath.Join(dir, "server.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); lockFile(lock) == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no server holds the lock")
		}
		lock.Close()
		lock, _ = os.OpenFile(filepath.Join(dir, "server.lock"), os.O_RDWR, 0)
	}
	lock.Close()

	list, err := c.List()
	if err != nil || len(list) != 2 || list[0].ID != info.ID || list[0].PID != info.PID || list[0].Exited {
		t.Fatalf("list %+v %v, created %+v", list, err, info)
	}
	if !processAlive(info.PID) || !processAlive(ticker.PID) {
		t.Fatal("a shell is gone")
	}
	s = c.Stream(info.ID, 80, 24)
	s.Resize(80, 24)
	readUntil(t, s, `before-upgrade`)
	s.Write([]byte("echo x%X%\r"))
	readUntil(t, s, `x42`)
	// The new size reaches the console through its signal pipe.
	s.Resize(123, 41)
	time.Sleep(300 * time.Millisecond)
	s.Write([]byte(`powershell -NoProfile -Command "'w='+[Console]::WindowWidth+'x'+[Console]::WindowHeight"` + "\r"))
	readUntil(t, s, `w=123x41`)
	s.Close()

	// Each tick once, in order.
	var text string
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if f, err := c.Format(ticker.ID, "text"); err == nil {
			text = string(f.Content)
		}
		if regexp.MustCompile(`ticks-done`).MatchString(text) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ticks did not end: %q", text)
		}
	}
	got := regexp.MustCompile(`tick(\d+)`).FindAllStringSubmatch(text, -1)
	for i, m := range got {
		if m[1] != strconv.Itoa(i+1) {
			t.Fatalf("tick %d is %s: %q", i+1, m[1], text)
		}
	}
	if len(got) != ticks {
		t.Fatalf("%d ticks, want %d: %q", len(got), ticks, text)
	}

	// A hangup closes the console, which ends the shell, that reads no
	// input meanwhile: the session's end kills the shell only when it did
	// not end within a second (kill).
	start := time.Now()
	if err := c.Kill(ticker.ID); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took >= time.Second || processAlive(ticker.PID) {
		t.Fatalf("the shell ended after %v, alive %v", took, processAlive(ticker.PID))
	}
}
