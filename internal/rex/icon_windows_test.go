//go:build windows

package rex

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestIconReply checks that a program in a pseudo console made by the bundled
// conpty.dll reads the server's answer to its OSC 7502 query and sets an icon.
func TestIconReply(t *testing.T) {
	if os.Getenv("GOREX_TEST_ICON_REPLY") == "helper" {
		iconReplyHelper(t)
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
	env := append(os.Environ(), "GOREX_TEST_ICON_REPLY=helper")
	info, err := c.Create(CreateOptions{
		Command: []string{exe, "-test.run=^TestIconReply$"},
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
	readUntil(t, st, `REPLY`)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		l, _ := c.List()
		if len(l) == 1 && l[0].ProgramIcon != nil && l[0].ProgramIcon.Format == "svg" {
			return
		}
		if time.Now().After(deadline) {
			if len(l) == 1 {
				t.Fatalf("programIcon = %+v", l[0].ProgramIcon)
			}
			t.Fatalf("%d sessions", len(l))
		}
	}
}

func iconReplyHelper(t *testing.T) {
	if h, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE); err == nil {
		windows.SetConsoleMode(h, windows.ENABLE_VIRTUAL_TERMINAL_INPUT)
	}
	if o, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); err == nil {
		var mode uint32
		if windows.GetConsoleMode(o, &mode) == nil {
			windows.SetConsoleMode(o, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
		}
	}
	fmt.Println("ready")
	fmt.Print("\x1b]7502;?\x1b\\")
	reply := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 256)
		for !strings.Contains(b.String(), "\x1b]7502;?") {
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
	svg := `<svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="6"/></svg>`
	fmt.Printf("\x1b]7502;format=svg:data=%s\x07", base64.StdEncoding.EncodeToString([]byte(svg)))
	one := make([]byte, 1)
	for {
		if _, err := os.Stdin.Read(one); err != nil {
			return
		}
	}
}
