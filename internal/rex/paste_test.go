//go:build darwin || linux

package rex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEncodePaste(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		bracketed bool
		want      string
	}{
		{"text", "hello", false, "hello"},
		{"newlines become returns", "a\nb\r\nc\r", false, "a\rb\rc\r"},
		{"control characters go", "a\x1b[31mb\x07c\x00d", false, "a[31mbcd"},
		{"tabs stay", "a\tb", false, "a\tb"},
		{"bracketed", "hello", true, "\x1b[200~hello\x1b[201~"},
		{"bracketed keeps what it holds", "a\nb\x1b", true, "\x1b[200~a\nb\x1b\x1b[201~"},
		{"nothing", "", false, ""},
	}
	for _, c := range cases {
		if got := string(encodePaste(c.text, c.bracketed)); got != c.want {
			t.Errorf("%s: encodePaste(%q, %v) = %q, want %q", c.name, c.text, c.bracketed, got, c.want)
		}
	}
}

func TestDecModes(t *testing.T) {
	var m decModes
	m.feed([]byte("\x1b[?2004h"))
	if !m.bracketed {
		t.Fatal("2004 set, not tracked")
	}
	m.feed([]byte("\x1b[?2004l"))
	if m.bracketed {
		t.Error("2004 reset, still set")
	}
	// A sequence cut by a read is joined to the next.
	m.feed([]byte("text\x1b[?20"))
	m.feed([]byte("04h more"))
	if !m.bracketed {
		t.Error("a sequence cut between reads was lost")
	}
	// Another private mode is no mode of ours.
	m.feed([]byte("\x1b[?1049h\x1b[?1;2005h"))
	if !m.bracketed {
		t.Error("the paste mode changed with another mode")
	}
	m.feed([]byte("\x1b[?1049;2004l"))
	if m.bracketed {
		t.Error("2004 in a list was not read")
	}
	// Text without escapes leaves it as it is.
	m.feed([]byte("plain \x1b[0m text"))
	if m.bracketed {
		t.Error("plain output changed the mode")
	}
	// A full reset takes the mode with the screen.
	m.feed([]byte("\x1b[?2004h\x1bc"))
	if m.bracketed {
		t.Error("a reset left the paste mode set")
	}
}

// pasteServer starts a server in a directory of its own and returns a
// client of it.
func pasteServer(t *testing.T) *Client {
	t.Helper()
	base := "/tmp"
	if _, err := os.Stat(base); err != nil {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, "rexpaste")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("GOREX_DIR", dir)
	go Serve()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "server.sock")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	c, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Shutdown() })
	return c
}

// waitScreen waits for the session's screen to show want: what a paste
// needs, its modes printed by then.
func waitScreen(t *testing.T, c *Client, sid, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if res, err := c.Capture(sid, 0); err == nil && strings.Contains(string(res.Screen), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the session never showed %q", want)
}

// waitFile waits for a file to hold want, and returns what it holds.
func waitFile(t *testing.T, path, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var got []byte
	for time.Now().Before(deadline) {
		got, _ = os.ReadFile(path)
		if strings.Contains(string(got), want) {
			return string(got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return string(got)
}

// TestPasteIntoASession: a paste writes its text to the session's program,
// with its newlines as returns, as the terminal's paste keys do.
func TestPasteIntoASession(t *testing.T) {
	c := pasteServer(t)
	file := filepath.Join(t.TempDir(), "pasted")
	info, err := c.Create(CreateOptions{Command: []string{"sh", "-c", "echo PASTEREADY; cat > " + file}, Dir: "/tmp", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if info.ConPTY {
		t.Error("a pty reported a ConPTY")
	}
	waitScreen(t, c, info.ID, "PASTEREADY")
	if err := c.Paste(info.ID, "hello\nworld\n"); err != nil {
		t.Fatal(err)
	}
	// The console's line discipline takes a return for a newline: the
	// program reads the lines, one by one.
	if got := waitFile(t, file, "world"); !strings.Contains(got, "hello\nworld\n") {
		t.Errorf("the program read %q, want the text as lines", got)
	}
}

// TestPasteBracketedWhenAsked: a program that asked for bracketed pastes
// gets them, as the terminal's paste keys send.
func TestPasteBracketedWhenAsked(t *testing.T) {
	c := pasteServer(t)
	file := filepath.Join(t.TempDir(), "pasted")
	info, err := c.Create(CreateOptions{
		Command: []string{"sh", "-c", `printf '\033[?2004h'; echo PASTEREADY; cat > ` + file},
		Dir:     "/tmp", Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitScreen(t, c, info.ID, "PASTEREADY")
	if err := c.Paste(info.ID, "hello\n"); err != nil {
		t.Fatal(err)
	}
	// The console delivers lines: a return flushes the end of the paste.
	if err := c.Input(info.ID, []byte("\n")); err != nil {
		t.Fatal(err)
	}
	got := waitFile(t, file, "\x1b[201~")
	if !strings.Contains(got, "\x1b[200~hello\n\x1b[201~") {
		t.Errorf("the program read %q, want a bracketed paste", got)
	}
}

// TestPasteOfALargeText: a text larger than one request goes as several,
// in order, and the program reads it whole.
func TestPasteOfALargeText(t *testing.T) {
	c := pasteServer(t)
	file := filepath.Join(t.TempDir(), "pasted")
	info, err := c.Create(CreateOptions{Command: []string{"sh", "-c", "echo PASTEREADY; cat > " + file}, Dir: "/tmp", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	waitScreen(t, c, info.ID, "PASTEREADY")
	var text strings.Builder
	for i := 0; text.Len() < 150<<10; i++ {
		fmt.Fprintf(&text, "line %d of a long paste\n", i)
	}
	if err := c.Paste(info.ID, text.String()); err != nil {
		t.Fatal(err)
	}
	// Every line ends with a return, which the console delivers.
	want := text.String()
	deadline := time.Now().Add(10 * time.Second)
	var got []byte
	for time.Now().Before(deadline) {
		got, _ = os.ReadFile(file)
		if len(got) >= len(want) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if string(got) != want {
		t.Errorf("the program read %d bytes, want %d", len(got), len(want))
	}
}

// TestPasteEndedSession: a paste into a session whose program ended is an
// error the client sees.
func TestPasteIntoAnEndedSession(t *testing.T) {
	c := pasteServer(t)
	info, err := c.Create(CreateOptions{Command: []string{"sh", "-c", "exit 0"}, Dir: "/tmp", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		list, err := c.List()
		if err == nil && len(list) == 1 && list[0].Exited {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := c.Paste(info.ID, "hello"); err == nil {
		t.Error("a paste into an ended session was taken")
	}
}

// TestEncodePasteDropsWhatRuns: an unbracketed paste cannot end a
// bracketed one a program is in, nor start one.
func TestEncodePasteDropsWhatRuns(t *testing.T) {
	got := encodePaste("rm -rf /\n\x1b[201~oops", false)
	if bytes.Contains(got, []byte("\x1b[201~")) {
		t.Errorf("a paste carried the end of a bracketed paste: %q", got)
	}
}
