//go:build unix

package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorex/internal/rex"
)

// linkSelf puts a link named name to this executable in a directory of
// its own, as rex.CLIDir has, and returns the directory.
func linkSelf(t *testing.T, name string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runVoice(t *testing.T, args ...string) (int, bool, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code, handled := Voice(args, &out, &errOut)
	return code, handled, out.String(), errOut.String()
}

// The real rec is the next one on PATH that is not this executable.
func TestRealTool(t *testing.T) {
	self := linkSelf(t, "rec")
	other := t.TempDir()
	real := filepath.Join(other, "rec")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", self+string(os.PathListSeparator)+other)
	if got := realTool("rec"); got != real {
		t.Errorf("real rec %q, want %q", got, real)
	}
	t.Setenv("PATH", self)
	if got := realTool("rec"); got != "" {
		t.Errorf("real rec %q, want none", got)
	}
}

// Another GoRex's links, which have gorex beside them as the same file,
// are not the real SoX either: the two would run each other forever.
func TestRealToolPastAnotherGoRex(t *testing.T) {
	other, real := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "gorex"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("gorex", filepath.Join(other, "rec")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", linkSelf(t, "rec")+string(os.PathListSeparator)+other)
	if got := realTool("rec"); got != "" {
		t.Errorf("took another GoRex's rec, %q", got)
	}
	want := filepath.Join(real, "rec")
	if err := os.WriteFile(want, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+real)
	if got := realTool("rec"); got != want {
		t.Errorf("the real rec past another GoRex's: %q, want %q", got, want)
	}
}

// rec takes the voice only for raw audio to stdout, as Claude Code asks.
func TestClaudeRec(t *testing.T) {
	for args, want := range map[string]bool{
		"-q --buffer 1024 -t raw -r 16000 -e signed -b 16 -c 1 -":                           true,
		"-q --buffer 1024 -t raw -r 16000 -e signed -b 16 -c 1 - silence 1 0.1 3% 1 2.0 3%": true,
		"out.wav":        false,
		"-t raw out.raw": false,
		"-t wav -":       false,
		"--version":      false,
		"":               false,
	} {
		if got := claudeRec(strings.Fields(args)); got != want {
			t.Errorf("rec %s: %v, want %v", args, got, want)
		}
	}
}

// Without SoX, rec and sox say a version, and refuse the rest; another
// name is not theirs.
func TestVoiceWithoutSoX(t *testing.T) {
	t.Setenv("PATH", linkSelf(t, "sox"))
	t.Setenv("GOREX_SESSION", "")
	for _, name := range []string{"rec", "sox"} {
		if code, handled, out, _ := runVoice(t, "/bin/"+name, "--version"); !handled || code != ExitOK || !strings.Contains(out, "SoX") {
			t.Errorf("%s --version: %d %v %q", name, code, handled, out)
		}
	}
	if code, handled, out, errOut := runVoice(t, "rec", "-q", "-t", "raw", "-"); !handled || code != ExitFailed || out != "" || errOut == "" {
		t.Errorf("rec with no voice: %d %v %q %q", code, handled, out, errOut)
	}
	if code, handled, _, errOut := runVoice(t, "sox", "a.wav", "b.wav"); !handled || code != ExitFailed || errOut == "" {
		t.Errorf("sox: %d %v %q", code, handled, errOut)
	}
	if _, handled, _, _ := runVoice(t, "gorex", "ls"); handled {
		t.Error("gorex taken for rec")
	}
}

// rec writes the session's voice, from its beginning to its end.
func TestRecVoice(t *testing.T) {
	c := startServer(t)
	info, err := c.Create(rex.CreateOptions{Command: []string{"/bin/sh"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOREX_SESSION", info.ID)
	t.Setenv("GOREX_SERVER", rex.SocketPath())
	t.Setenv("PATH", linkSelf(t, "rec"))
	feed, err := c.FeedVoice(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The voice begins before rec runs, and ends once rec read it.
	pcm := bytes.Repeat([]byte{1, 2, 3, 4}, 8000)
	feed.Write(pcm)
	// A recording to a file is not Claude's, and leaves the voice.
	if code, _, out, _ := runVoice(t, "rec", "out.wav"); code != ExitFailed || out != "" {
		t.Errorf("rec out.wav: %d %q", code, out)
	}
	r, w := io.Pipe()
	code := make(chan int, 1)
	go func() {
		c, _ := Voice([]string{"rec", "-q", "--buffer", "1024", "-t", "raw", "-r", "16000", "-e", "signed", "-b", "16", "-c", "1", "-"}, w, io.Discard)
		w.Close()
		code <- c
	}()
	got := make([]byte, len(pcm))
	if _, err := io.ReadFull(r, got); err != nil || !bytes.Equal(got, pcm) {
		t.Fatalf("rec wrote %d bytes: %v", len(got), err)
	}
	feed.Close()
	if rest, _ := io.ReadAll(r); len(rest) != 0 {
		t.Errorf("rec wrote %d more bytes", len(rest))
	}
	if c := <-code; c != ExitOK {
		t.Errorf("rec exited %d", c)
	}
}
