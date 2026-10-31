//go:build darwin || linux

package rex

import (
	"strings"
	"testing"
	"time"
)

// waitCapture waits for a session's screen to hold want.
func waitCapture(t *testing.T, c *Client, sid, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res, err := c.Capture(sid, 0)
		if err == nil && strings.Contains(string(res.Screen), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the session never showed %q", want)
}

// TestInputEvents: the input ops encode what a client sends as the
// program's modes ask.
func TestInputEvents(t *testing.T) {
	c := pasteServer(t)
	script := `printf '\033[?1h\033[?1000h\033[?1006h\033[?1004h\033[?2004h'; stty raw -echo; exec cat -v`
	info, err := c.Create(CreateOptions{Command: []string{"/bin/sh", "-c", script}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, err := c.Modes(info.ID)
		if err != nil {
			t.Fatal(err)
		}
		on := map[string]bool{}
		for _, m := range st.Modes {
			on[m.Name] = m.Set
		}
		if on["application_cursor_keys"] && on["bracketed_paste"] && on["focus_event"] && on["mouse_click"] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the modes were never set: %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := c.SendKey(info.ID, KeyInput{Key: "ArrowUp"}); err != nil {
		t.Fatal(err)
	}
	waitCapture(t, c, info.ID, "^[OA")
	if err := c.SendMouse(info.ID, MouseInput{Action: "press", Button: "left", Col: 2, Row: 4}); err != nil {
		t.Fatal(err)
	}
	waitCapture(t, c, info.ID, "^[[<0;3;5M")
	if err := c.SendFocus(info.ID, true); err != nil {
		t.Fatal(err)
	}
	waitCapture(t, c, info.ID, "^[[I")
	if err := c.Paste(info.ID, "foo"); err != nil {
		t.Fatal(err)
	}
	waitCapture(t, c, info.ID, "^[[200~foo^[[201~")

	// A bad event is an error.
	if err := c.SendKey(info.ID, KeyInput{Key: "KeyA", Mods: "hyper"}); err == nil {
		t.Error("an unknown modifier was accepted")
	}
	if err := c.SendKey(info.ID, KeyInput{Key: "KeyA", Action: "tap"}); err == nil {
		t.Error("an unknown action was accepted")
	}
	if err := c.SendMouse(info.ID, MouseInput{Action: "press", Button: "fourth"}); err == nil {
		t.Error("an unknown button was accepted")
	}
}

// TestTerminalQueries: the ops that read and change a session's terminal.
func TestTerminalQueries(t *testing.T) {
	c := pasteServer(t)
	info, err := c.Create(CreateOptions{Command: []string{"/bin/sh", "-c", "echo marker; sleep 60"}})
	if err != nil {
		t.Fatal(err)
	}
	waitCapture(t, c, info.ID, "marker")
	f, err := c.Format(info.ID, "text")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(f.Content), "marker") || f.Cols == 0 || f.Rows == 0 {
		t.Errorf("format: %q %dx%d", f.Content, f.Cols, f.Rows)
	}
	if vt, err := c.Format(info.ID, "vt"); err != nil || !strings.Contains(string(vt.Content), "\x1b[") {
		t.Errorf("format vt: %q %v", vt.Content, err)
	}
	if _, err := c.Format(info.ID, "pdf"); err == nil {
		t.Error("a format the server does not have was accepted")
	}
	mode, err := c.InputMode(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !mode.Echo || !mode.Canonical || !mode.Signals {
		t.Errorf("input mode: %+v", mode)
	}
	if err := c.ClearScreen(info.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		f, err := c.Format(info.ID, "text")
		if err == nil && !strings.Contains(string(f.Content), "marker") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("clear left the screen: %q", f.Content)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := c.ResetTerminal(info.ID); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		f, err := c.Format(info.ID, "text")
		if err == nil && string(f.Content) == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reset left the screen: %q", f.Content)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The modes go back to a new terminal's on a reset.
	st, err := c.Modes(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range st.Modes {
		if m.Name == "insert" && m.Set {
			t.Error("a reset left a mode set")
		}
	}
}
