package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// remotePane opens a tab on a host, in dir there, and returns its pane once
// its shell prompts.
func remotePane(t *testing.T, a *App, tt *ui.Tester, dir string) (*hostConn, *Pane) {
	t.Helper()
	fakeHosts(t)
	sock, _ := startHostServer(t)
	t.Cleanup(a.stopHosts)
	h := a.addHost(savedHost{ID: "h1", Name: "office-mac", Address: sock})
	waitFor(t, tt, "the host to connect", func() bool { return h.state == hostConnected })
	rt := a.newTabOn(h, dir)
	if rt == nil {
		t.Fatal("no tab on the host")
	}
	p := rt.Focus
	waitFor(t, tt, "the remote shell", func() bool { return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt) })
	return h, p
}

// dropped writes files of these names and contents into a new directory,
// and returns their paths.
func dropped(t *testing.T, files ...string) []string {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for i := 0; i < len(files); i += 2 {
		path := filepath.Join(dir, files[i])
		if err := os.WriteFile(path, []byte(files[i+1]), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}

// TestDropUploadsToHost drops files on a pane of a host: they go into the
// directory of its shell there, one of a name taken renamed, and their
// paths there are pasted, quoted, as a drop here pastes paths.
func TestDropUploadsToHost(t *testing.T) {
	if testShell.path == "cmd.exe" {
		t.Skip("POSIX quoting")
	}
	a, tt := newTestApp(t)
	there := testDir(t)
	t.Cleanup(func() { os.RemoveAll(there) })
	os.WriteFile(filepath.Join(there, "b.txt"), []byte("old"), 0o644)
	_, p := remotePane(t, a, tt, there)
	paths := dropped(t, "a b.txt", "hello", "b.txt", "new")
	// Dragged over, the pane says where they go in its middle; a drop
	// there is the pane's.
	r, _ := tt.Find("Terminal")
	if !tt.DropFiles(r.X+r.W/2, r.Y+r.H/2, paths...) {
		t.Fatal("no element took the drop")
	}
	if len(p.transfers) != 2 {
		t.Fatalf("transfers %v", p.transfers)
	}
	waitFor(t, tt, "the paths there", func() bool { return strings.Contains(p.term.Text(), `b (1).txt`) })
	for name, want := range map[string]string{"a b.txt": "hello", "b.txt": "old", "b (1).txt": "new"} {
		if b, err := os.ReadFile(filepath.Join(there, name)); err != nil || string(b) != want {
			t.Errorf("%s there: %q, %v", name, b, err)
		}
	}
	text := p.term.Text()
	if !strings.Contains(text, `/a b.txt' '`) || !strings.Contains(text, `/b (1).txt'`) || strings.Contains(text, paths[0]) {
		t.Errorf("pasted %q", text)
	}
	if !tt.HasText("Done") {
		t.Errorf("no file done; texts %q", tt.Texts())
	}
	tt.Advance(transferShown)
	tt.Frame()
	if len(p.transfers) != 0 || tt.HasText("Done") {
		t.Errorf("transfers stay: %v", p.transfers)
	}
}

// TestDropFolderOnHost drops a folder on a pane of a host: it is not sent,
// its path here is pasted as before, and the list says why until clicked.
func TestDropFolderOnHost(t *testing.T) {
	a, tt := newTestApp(t)
	_, p := remotePane(t, a, tt, "")
	folder := t.TempDir()
	tt.DropFiles(500, 300, folder)
	waitFor(t, tt, "the folder's path", func() bool { return strings.Contains(p.term.Text(), filepath.Base(folder)) })
	if !tt.HasText("Folders are not uploaded yet") {
		t.Fatalf("texts %q", tt.Texts())
	}
	tt.Advance(transferShown)
	tt.Frame()
	if err := tt.Click("Folders are not uploaded yet"); err != nil {
		t.Fatal(err)
	}
	if len(p.transfers) != 0 {
		t.Errorf("the failure stays after a click: %v", p.transfers)
	}
}

// TestDropOnOldHost drops a file on a pane of a host that takes no files:
// the terminal pastes its path here, as before.
func TestDropOnOldHost(t *testing.T) {
	a, tt := newTestApp(t)
	h, p := remotePane(t, a, tt, "")
	h.hello.Capabilities = nil
	// Dragged over, the pane says why in its middle; a drop there is the
	// terminal's.
	r, _ := tt.Find("Terminal")
	paths := dropped(t, "old.txt", "x")
	if !tt.DropFiles(r.X+r.W/2, r.Y+r.H/2, paths...) {
		t.Fatal("no element took the drop")
	}
	waitFor(t, tt, "the path here", func() bool { return strings.Contains(p.term.Text(), "old.txt") })
	if len(p.transfers) != 0 || p.uploading != nil {
		t.Errorf("an upload to a host that takes none: %v", p.transfers)
	}
}

// TestDropOnLocalPane keeps a drop on a pane of this machine the terminal's:
// the path is pasted, nothing is sent.
func TestDropOnLocalPane(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	waitFor(t, tt, "the shell", func() bool { return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt) })
	paths := dropped(t, "here.txt", "x")
	if !tt.DropFiles(500, 300, paths...) {
		t.Fatal("no element took the drop")
	}
	waitFor(t, tt, "the path", func() bool { return strings.Contains(p.term.Text(), "here.txt") })
	if len(p.transfers) != 0 || p.uploading != nil {
		t.Errorf("a local drop uploads: %v", p.transfers)
	}
}

// TestTransferList shows a pane's transfers: progress, a file there for a
// while, a failure until clicked, and a cancel that stops one.
func TestTransferList(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	ctx, cancel := context.WithCancel(context.Background())
	active := &fileTransfer{name: "big.bin", size: 4_800_000, done: 1_200_000, cancel: cancel}
	p.transfers = []*fileTransfer{
		active,
		{name: "small.txt", size: 10, done: 10, ok: true},
		{name: "bad.txt", size: 10, err: "disk full"},
	}
	tt.Frame()
	for _, s := range []string{"big.bin", "1.2 MB of 4.8 MB", "small.txt", "Done", "bad.txt", "disk full"} {
		if !tt.HasText(s) {
			t.Fatalf("no %q; texts %q", s, tt.Texts())
		}
	}
	active.done = 2_400_000
	tt.Frame()
	if !tt.HasText("2.4 MB of 4.8 MB") {
		t.Errorf("progress not shown; texts %q", tt.Texts())
	}
	tt.Advance(transferShown + time.Millisecond)
	tt.Frame()
	if tt.HasText("small.txt") || !tt.HasText("disk full") {
		t.Errorf("after a while: texts %q", tt.Texts())
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil || tt.HasText("big.bin") {
		t.Error("cancel did not stop the transfer")
	}
	if err := tt.Click("disk full"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(p.transfers) != 0 || tt.HasText("bad.txt") {
		t.Errorf("the list stays: %v", p.transfers)
	}
}

func TestByteSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 999: "999 B", 1000: "1.0 kB", 1_234_567: "1.2 MB", 5_000_000_000: "5.0 GB"} {
		if got := byteSize(n); got != want {
			t.Errorf("byteSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestPastedPaths(t *testing.T) {
	for _, c := range []struct{ shell, want string }{
		{"/bin/zsh", `/tmp/a.txt '/tmp/b c.txt'`},
		{"", `/tmp/a.txt '/tmp/b c.txt'`},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, `/tmp/a.txt '/tmp/b c.txt'`},
		{`C:\Windows\System32\cmd.exe`, `/tmp/a.txt "/tmp/b c.txt"`},
	} {
		if got := pastedPaths(c.shell, []string{"/tmp/a.txt", "/tmp/b c.txt"}); got != c.want {
			t.Errorf("pastedPaths(%q) = %q, want %q", c.shell, got, c.want)
		}
	}
}
