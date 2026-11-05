package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
)

// remotePane opens a tab on a host, through a proxy, in dir there, and
// returns its pane once its shell prompts.
func remotePane(t *testing.T, a *App, tt *ui.Tester, dir string) (*hostConn, *hostProxy, *Pane) {
	h, proxy, p, _ := remotePaneOf(t, a, tt, dir)
	return h, proxy, p
}

// remotePaneOf is remotePane, with a client of the host's server.
func remotePaneOf(t *testing.T, a *App, tt *ui.Tester, dir string) (*hostConn, *hostProxy, *Pane, *rex.Client) {
	t.Helper()
	fakeHosts(t)
	sock, remote := startHostServer(t)
	proxy := newHostProxy(t, sock)
	t.Cleanup(a.stopHosts)
	h := a.addHost(savedHost{ID: "h1", Name: "office-mac", Address: proxy.path})
	waitFor(t, tt, "the host to connect", func() bool { return h.state == hostConnected })
	rt := a.newTabOn(h, dir)
	if rt == nil {
		t.Fatal("no tab on the host")
	}
	p := rt.Focus
	waitFor(t, tt, "the remote shell", func() bool { return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt) })
	return h, proxy, p, remote
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

// partialOf is the path of the partial the server keeps in dir of a put of
// name for the transfer id (rex.FilePut).
func partialOf(dir, name, id string) string {
	sum := sha256.Sum256([]byte(name + "\x00" + id))
	return filepath.Join(dir, ".gorex-"+hex.EncodeToString(sum[:8])+".partial")
}

// idOf is the upload id of the file at path into the session sid.
func idOf(t *testing.T, sid, path string) string {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return uploadID(sid, path, fi)
}

// waitUploaded waits for the uploads of p to end.
func waitUploaded(t *testing.T, tt *ui.Tester, p *Pane) {
	t.Helper()
	waitFor(t, tt, "the uploads", func() bool {
		select {
		case <-p.uploading:
			return true
		default:
			return false
		}
	})
	tt.Frame() // what they posted
}

// TestDropUploadsToHost drops files on a pane of a host: they go into the
// directory of its shell there, going on from what an earlier try left,
// one of a name taken renamed, and their paths there are pasted, quoted,
// as a drop here pastes paths. A file gone pastes nothing.
func TestDropUploadsToHost(t *testing.T) {
	if testShell.path == "cmd.exe" {
		t.Skip("POSIX quoting")
	}
	a, tt := newTestApp(t)
	there := testDir(t)
	t.Cleanup(func() { os.RemoveAll(there) })
	os.WriteFile(filepath.Join(there, "b.txt"), []byte("old"), 0o644)
	_, _, p := remotePane(t, a, tt, there)
	if !uploads(p) {
		t.Fatal("the host takes no files")
	}
	paths := dropped(t, "a b.txt", "hello", "b.txt", "new")
	gone := filepath.Join(filepath.Dir(paths[0]), "gone.txt")
	part := partialOf(there, "a b.txt", idOf(t, p.SID, paths[0]))
	os.WriteFile(part, []byte("hel"), 0o644)
	a.upload(p, append(paths, gone))
	if len(p.transfers) != 3 {
		t.Fatalf("transfers %v", p.transfers)
	}
	waitUploaded(t, tt, p)
	waitFor(t, tt, "the paths there", func() bool { return strings.Contains(p.term.Text(), `b (1).txt`) })
	for name, want := range map[string]string{"a b.txt": "hello", "b.txt": "old", "b (1).txt": "new"} {
		if b, err := os.ReadFile(filepath.Join(there, name)); err != nil || string(b) != want {
			t.Errorf("%s there: %q, %v", name, b, err)
		}
	}
	if _, err := os.Stat(part); err == nil {
		t.Error("the earlier try's partial stays")
	}
	text := p.term.Text()
	if !strings.Contains(text, `/a b.txt' '`) || !strings.Contains(text, `/b (1).txt'`) || strings.Contains(text, paths[0]) || strings.Contains(text, "gone.txt") {
		t.Errorf("pasted %q", text)
	}
	if !tt.HasText("Done") || !tt.HasText("no such file or directory") {
		t.Errorf("texts %q", tt.Texts())
	}
	tt.Advance(transferShown)
	tt.Frame()
	if len(p.transfers) != 1 || tt.HasText("Done") || !tt.HasText("gone.txt") {
		t.Errorf("transfers after a while: %v", p.transfers)
	}
}

// TestDropFolderOnHost drops a folder on a pane of a host: it is not sent,
// its path here is pasted as before, and the list says why until clicked.
func TestDropFolderOnHost(t *testing.T) {
	a, tt := newTestApp(t)
	_, _, p := remotePane(t, a, tt, "")
	folder := t.TempDir()
	a.upload(p, []string{folder})
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

// TestUploadCancelled cancels an upload: what the host has of the file
// goes, an earlier try's too, and nothing is pasted. Closing the pane
// cancels its uploads.
func TestUploadCancelled(t *testing.T) {
	a, tt := newTestApp(t)
	there := testDir(t)
	t.Cleanup(func() { os.RemoveAll(there) })
	_, _, p := remotePane(t, a, tt, there)
	paths := dropped(t, "c.bin", strings.Repeat("x", 1<<20))
	part := partialOf(there, "c.bin", idOf(t, p.SID, paths[0]))
	os.WriteFile(part, []byte("xxxx"), 0o644)
	a.upload(p, paths)
	p.stopUploads()
	waitUploaded(t, tt, p)
	if _, err := os.Stat(part); err == nil {
		t.Error("the partial stays")
	}
	if _, err := os.Stat(filepath.Join(there, "c.bin")); err == nil {
		t.Error("the file went")
	}
	if strings.Contains(p.term.Text(), "c.bin") {
		t.Error("a cancelled file's path is pasted")
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.transfers = []*fileTransfer{{name: "d.bin", cancel: cancel}}
	a.dropPane(p, false)
	if ctx.Err() == nil {
		t.Error("closing the pane leaves its upload")
	}
}

// TestClosePaneMidUpload closes a pane of a host while it sends a file:
// what the host has of the file goes before its session ends, which names
// the directory it is in.
func TestClosePaneMidUpload(t *testing.T) {
	a, tt := newTestApp(t)
	there := testDir(t)
	t.Cleanup(func() { os.RemoveAll(there) })
	_, proxy, p, remote := remotePaneOf(t, a, tt, there)
	sid := p.SID
	paths := dropped(t, "big.bin", strings.Repeat("x", 1<<20))
	part := partialOf(there, "big.bin", idOf(t, sid, paths[0]))
	os.WriteFile(part, []byte("xxxx"), 0o644)
	a.split(false) // the tab stays
	q := a.tab().Focus
	waitFor(t, tt, "the split's shell", func() bool { return q.term != nil && strings.Contains(q.term.Text(), testShell.prompt) })
	// The put's connection waits on the way, as over a slow network.
	gate := make(chan struct{})
	proxy.mu.Lock()
	proxy.gate = gate
	proxy.mu.Unlock()
	a.upload(p, paths)
	waitFor(t, tt, "the put under way", func() bool { return proxy.held.Load() == 1 })
	a.closePane(p)
	time.Sleep(200 * time.Millisecond)
	if !slices.Contains(sessionIDs(t, remote), sid) {
		t.Fatal("the session ended before the upload removed its partial")
	}
	proxy.mu.Lock()
	proxy.gate = nil
	proxy.mu.Unlock()
	close(gate)
	waitFor(t, tt, "the session to end", func() bool { return !slices.Contains(sessionIDs(t, remote), sid) })
	if _, err := os.Stat(part); err == nil {
		t.Error("the partial stays")
	}
	if _, err := os.Stat(filepath.Join(there, "big.bin")); err == nil {
		t.Error("the file went")
	}
}

// TestUploadsOnlyToHostsTaking takes drops only on panes of a host that is
// connected and takes files; any other pane's drop is its terminal's.
func TestUploadsOnlyToHostsTaking(t *testing.T) {
	a, tt := newTestApp(t)
	local := a.tab().Focus
	h, proxy, p := remotePane(t, a, tt, "")
	if uploads(local) || !uploads(p) {
		t.Fatalf("uploads: local %v, remote %v", uploads(local), uploads(p))
	}
	caps := h.hello.Capabilities
	h.hello.Capabilities = nil
	if uploads(p) {
		t.Error("uploads to a host that takes no files")
	}
	h.hello.Capabilities = caps
	proxy.setDown(true)
	waitFor(t, tt, "the host to go", func() bool { return h.current() == nil })
	if uploads(p) {
		t.Error("uploads to a host not connected")
	}
}

// TestUploadID is the same for the same file, and another once it changes.
func TestUploadID(t *testing.T) {
	path := dropped(t, "f.txt", "x")[0]
	id := idOf(t, "s1", path)
	if idOf(t, "s1", path) != id || idOf(t, "s2", path) == id {
		t.Error("the id is not of the session and the file")
	}
	later := time.Now().Add(time.Minute)
	os.Chtimes(path, later, later)
	if idOf(t, "s1", path) == id {
		t.Error("a file changed keeps its id")
	}
}

// TestDropOnOldHost drops a file on a pane of a host that takes no files:
// the terminal pastes its path here, as before.
func TestDropOnOldHost(t *testing.T) {
	a, tt := newTestApp(t)
	h, _, p := remotePane(t, a, tt, "")
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
