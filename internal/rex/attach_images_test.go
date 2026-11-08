//go:build darwin || linux

package rex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
)

// printing starts a session whose program prints out and stays, and
// waits for it to show end.
func printing(t *testing.T, out, end string) *session {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out"), []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := newSession("images", CreateOptions{
		Command: []string{"/bin/sh", "-c", "cat out; exec sleep 30"},
		Dir:     dir, Cols: 40, Rows: 8,
	}, "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.kill)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		s.mu.Lock()
		text := s.vt.Text()
		s.mu.Unlock()
		if strings.Contains(text, end) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("the session never showed %q: %q", end, text)
		}
	}
}

// TestAttachSendsCurrentImages: an image a program sends again, as it
// redraws at each new size, reaches a window that attaches once, and one
// it deleted not at all; the window's screen is then the session's,
// images and cursor too.
func TestAttachSendsCurrentImages(t *testing.T) {
	var out strings.Builder
	for range 20 {
		out.WriteString("\x1b[2;3H" + kittyRGBA(5, 16, 16, ",p=2,c=4,r=2,C=1"))
	}
	out.WriteString("\x1b[5;1H" + kittyRGBA(6, 16, 16, ",C=1") + "\x1b_Ga=d,d=I,i=6,q=2\x1b\\")
	out.WriteString("\x1b[6;4Hend")
	s := printing(t, out.String(), "end")

	s.mu.Lock()
	snap, _ := s.view()
	gsnap, after, ok := s.ghosttyView()
	want, images := s.vt.Snapshot(), s.vt.Images()
	s.mu.Unlock()
	if !ok {
		t.Fatal("no Ghostty snapshot")
	}
	t.Logf("attach: %d bytes of escape sequences, %d of images", len(snap), len(images))
	for name, b := range map[string][]byte{"escape sequences": snap, "Ghostty snapshot": after} {
		if n := transmits(b, 5); n != 1 {
			t.Errorf("the %s send image 5 %d times, want once", name, n)
		}
		if n := transmits(b, 6); n != 0 {
			t.Errorf("the %s send deleted image 6 %d times", name, n)
		}
	}

	check := func(name string, w *terminal.Terminal) {
		if got := w.Images(); !bytes.Equal(got, images) {
			t.Errorf("after the %s, the window's images\n%q, want\n%q", name, got, images)
		}
		if got := w.Snapshot(); !bytes.Equal(got, want) {
			t.Errorf("after the %s, the window's screen\n%q, want\n%q", name, got, want)
		}
	}
	w := headless(t)
	w.Resize(40, 8)
	w.Feed(snap)
	check("escape sequences", w)
	w = headless(t)
	w.Resize(40, 8)
	if err := w.RestoreGhosttySnapshot(gsnap); err != nil {
		t.Fatal(err)
	}
	w.Feed(after)
	check("Ghostty snapshot", w)
}

// TestAttachKeepsPlaceholderImage: an image a program shows through
// Unicode placeholders, as omp does, shows in their cells in a window
// that attaches, by the ids the program gave it.
func TestAttachKeepsPlaceholderImage(t *testing.T) {
	// Two cells of image 7: row 0, columns 0 and 1.
	cells := "\x1b[38;5;7m\U0010EEEE̅̅\U0010EEEE̅̍\x1b[39m"
	s := printing(t, kittyRGBA(7, 16, 16, ",U=1,p=3,c=2,r=1")+kittyRGBA(7, 16, 16, ",U=1,p=3,c=2,r=1")+"\x1b[3;1H"+cells+"\r\nend", "end")
	s.mu.Lock()
	snap, _ := s.view()
	images := s.vt.Images()
	s.mu.Unlock()
	if n := transmits(snap, 7); n != 1 {
		t.Errorf("image 7 sent %d times, want once", n)
	}
	if !bytes.Contains(snap, []byte("a=p,U=1,i=7,p=3,c=2,r=1,")) {
		t.Errorf("no virtual placement of image 7 in %q", snap)
	}
	w := headless(t)
	w.Resize(40, 8)
	w.Feed(snap)
	if got := w.Images(); !bytes.Equal(got, images) {
		t.Errorf("the window's images\n%q, want\n%q", got, images)
	}
	if !strings.Contains(w.Text(), "\U0010EEEE") {
		t.Errorf("the placeholder cells are gone: %q", w.Text())
	}
}
