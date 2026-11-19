//go:build windows

package rex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestStatusReply checks that a program in a pseudo console made by the
// bundled conpty.dll reads the server's answer to its OSC 7501 query,
// which the inbox console turns into nothing. The program is this test
// binary again, in helper mode: it turns on ENABLE_VIRTUAL_TERMINAL_INPUT,
// as an agent does, asks, and says REPLY when the answer came.
func TestStatusReply(t *testing.T) {
	if os.Getenv("GOREX_TEST_STATUS_REPLY") == "helper" {
		statusReplyHelper(t)
		return
	}
	installBundledConpty(t)
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "GOREX_TEST_STATUS_REPLY=helper")
	info, err := c.Create(CreateOptions{
		Command: []string{exe, "-test.run=^TestStatusReply$"},
		Dir:     os.TempDir(),
		Env:     env,
		Cols:    80,
		Rows:    24,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Kill(info.ID)
	st := c.Stream(info.ID, 80, 24)
	defer st.Close()
	// The bundled console's layout must be the one consoleSignal reads,
	// or canUpgrade stops letting the server upgrade in place.
	if badConsole.Load() {
		t.Fatal("the bundled console's signal pipe could not be read")
	}
	readUntil(t, st, `REPLY`)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		l, _ := c.List()
		if len(l) == 1 {
			if p := l[0].Status; p != nil && p.State == "done" && p.Msg == "ok" {
				return
			}
		}
		if time.Now().After(deadline) {
			if len(l) == 1 {
				t.Fatalf("status %+v", l[0].Status)
			}
			t.Fatalf("%d sessions", len(l))
		}
	}
}

// statusReplyHelper asks the terminal about OSC 7501 and reports done:
// REPLY when the server's answer arrived, TIMEOUT otherwise. It uses
// ENABLE_VIRTUAL_TERMINAL_INPUT alone, as a TUI program does: with the
// console's input processing or echo on, the answer never comes back as
// bytes.
func statusReplyHelper(t *testing.T) {
	h, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if err == nil {
		windows.SetConsoleMode(h, windows.ENABLE_VIRTUAL_TERMINAL_INPUT)
	}
	// The console passes a program's escape sequences on only with
	// ENABLE_VIRTUAL_TERMINAL_PROCESSING, which a TUI program sets too.
	if o, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); err == nil {
		var mode uint32
		if windows.GetConsoleMode(o, &mode) == nil {
			windows.SetConsoleMode(o, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
		}
	}
	fmt.Println("ready")
	fmt.Print("\x1b]7501;?\x1b\\")
	reply := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 256)
		for !strings.Contains(b.String(), "\x1b]7501;?") {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if err != nil {
				reply <- "READERR " + err.Error()
				return
			}
		}
		reply <- "REPLY got=" + fmt.Sprintf("%q", b.String())
	}()
	select {
	case r := <-reply:
		fmt.Println(r)
	case <-time.After(4 * time.Second):
		fmt.Println("TIMEOUT")
	}
	fmt.Print("\x1b]7501;state=done:msg=b2s=\x07")
	// Stay until the console closes, so the session outlives the status
	// write; closing the stream ends it, and stdin's read ends then.
	one := make([]byte, 1)
	for {
		_, err := os.Stdin.Read(one)
		if err != nil {
			return
		}
	}
}

// installBundledConpty puts the resources' conpty.dll and OpenConsole.exe
// next to the test executable, where LoadLibrary finds the DLL, and makes
// bundledConpty look again. They stay in the test binary's build
// directory: a loaded DLL cannot be deleted.
func installBundledConpty(t *testing.T) {
	t.Helper()
	var platform, archDir string
	switch runtime.GOARCH {
	case "amd64":
		platform, archDir = "windows-amd64", "x64"
	case "arm64":
		platform, archDir = "windows-arm64", "arm64"
	default:
		t.Skipf("no bundled ConPTY for %s", runtime.GOARCH)
	}
	src := filepath.Join("..", "..", "resources", platform)
	if d := os.Getenv("GOREX_TEST_CONPTY_DIR"); d != "" {
		src = d
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(exe)
	copy := func(from, to string) {
		b, err := os.ReadFile(from)
		if err != nil {
			t.Skipf("bundled ConPTY not in resources: %v", err)
		}
		// A run may leave the files, and a loaded DLL cannot be written:
		// keep the ones already there when they are the same.
		if have, err := os.ReadFile(to); err == nil && bytes.Equal(have, b) {
			return
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	copy(filepath.Join(src, "conpty.dll"), filepath.Join(dir, "conpty.dll"))
	copy(filepath.Join(src, archDir, "OpenConsole.exe"), filepath.Join(dir, archDir, "OpenConsole.exe"))
	conptyOnce = sync.Once{}
	conpty = nil
	badConsole.Store(false)
	if bundledConpty() == nil {
		t.Skip("conpty.dll next to the test executable did not load")
	}
	if d, err := windows.LoadDLL("conpty.dll"); err == nil {
		var buf [windows.MAX_PATH]uint16
		if n, err := windows.GetModuleFileName(windows.Handle(d.Handle), &buf[0], windows.MAX_PATH); err == nil {
			t.Logf("conpty.dll loaded from %s", windows.UTF16ToString(buf[:n]))
		}
	}
}
