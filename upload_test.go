package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
	"gorex/locales"
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

// dropsDir points the temporary directory at a new one, for the hosts the
// test starts there, and returns the folder in it dropped files go into,
// made.
func dropsDir(t *testing.T) string {
	t.Helper()
	tmp := testDir(t)
	t.Cleanup(func() { os.RemoveAll(tmp) })
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, tmp)
	}
	name := "gorex-drops"
	if uid := os.Getuid(); uid >= 0 {
		name = fmt.Sprintf("gorex-drops-%d", uid)
	}
	dir := filepath.Join(tmp, name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
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
		case <-uploadingOf(p):
			return true
		default:
			return false
		}
	})
	tt.Frame() // what they posted
}

// TestDropUploadsToHost drops files on a pane of a host: they go into a
// temporary folder there, not the directory of its shell, going on from
// what an earlier try left, one of a name taken renamed, and their paths
// there are pasted, quoted, a space after each, as a drop here pastes
// paths, so that what is typed next is another word. A file gone pastes
// nothing.
func TestDropUploadsToHost(t *testing.T) {
	if testShell.path == "cmd.exe" {
		t.Skip("POSIX quoting")
	}
	there := dropsDir(t)
	a, tt := newTestApp(t)
	if err := os.WriteFile(filepath.Join(there, "b.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	_, _, p, c := remotePaneOf(t, a, tt, cwd)
	if !transfersFiles(p) {
		t.Fatal("the host takes no files")
	}
	// cat shows the paths pasted as they are: sh scrolls a line longer
	// than the pane.
	if err := c.Input(p.SID, []byte("exec cat\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "cat", func() bool { return p.info.Program == "cat" })
	paths := dropped(t, "a b.txt", "hello", "b.txt", "new")
	gone := filepath.Join(filepath.Dir(paths[0]), "gone.txt")
	part := partialOf(there, "a b.txt", idOf(t, p.SID, paths[0]))
	if err := os.WriteFile(part, []byte("hel"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.upload(p, append(paths, gone))
	if len(listOf(p)) != 3 {
		t.Fatalf("transfers %v", listOf(p))
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
	if entries, _ := os.ReadDir(cwd); len(entries) > 0 {
		t.Errorf("the shell's directory has %v", entries)
	}
	text := p.term.Text()
	if !strings.Contains(text, `/a b.txt' '`) || !strings.Contains(text, `/b (1).txt'`) || strings.Contains(text, paths[0]) || strings.Contains(text, "gone.txt") {
		t.Errorf("pasted %q", text)
	}
	if !tt.HasText("Done") || !tt.HasText("no such file or directory") {
		t.Errorf("texts %q", tt.Texts())
	}
	// What is typed next follows the space after the last path.
	if err := c.Input(p.SID, []byte("next")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the typing", func() bool {
		return strings.Contains(strings.ReplaceAll(p.term.Text(), "\n", ""), `/b (1).txt' next`)
	})
	tt.Advance(transferShown)
	tt.Frame()
	if len(listOf(p)) != 1 || tt.HasText("Done") || !tt.HasText("gone.txt") {
		t.Errorf("transfers after a while: %v", listOf(p))
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
	if len(listOf(p)) != 0 {
		t.Errorf("the failure stays after a click: %v", listOf(p))
	}
}

// heldFile is a file of size bytes whose first half reads at once, and
// the rest once released, failing: an upload held under way.
type heldFile struct {
	size     int64
	reached  chan struct{}
	release  chan struct{}
	reaching *sync.Once
}

func (f heldFile) ReadAt(b []byte, off int64) (int, error) {
	if off >= f.size/2 {
		f.reaching.Do(func() { close(f.reached) })
		<-f.release
		return 0, errors.New("released")
	}
	b = b[:min(int64(len(b)), f.size/2-off)]
	clear(b)
	return len(b), nil
}

func (heldFile) Close() error { return nil }

// TestClosePaneMidUpload closes a pane of a host while it sends a file,
// which ends its session at once: what the host has of the file goes all
// the same, in the directory the put named.
func TestClosePaneMidUpload(t *testing.T) {
	there := dropsDir(t)
	a, tt := newTestApp(t)
	_, _, p, remote := remotePaneOf(t, a, tt, t.TempDir())
	a.split(false) // the tab stays
	sid := p.SID
	path := dropped(t, "big.bin", strings.Repeat("x", 256<<10))[0]
	held := heldFile{size: 256 << 10, reached: make(chan struct{}), release: make(chan struct{}), reaching: new(sync.Once)}
	old := openUpload
	openUpload = func(string) (uploadFile, error) { return held, nil }
	t.Cleanup(func() { openUpload = old })
	part := partialOf(there, "big.bin", idOf(t, sid, path))
	a.upload(p, []string{path})
	<-held.reached
	if _, err := os.Stat(part); err != nil {
		t.Fatalf("no partial under way: %v", err)
	}
	done := uploadingOf(p)
	a.closePane(p)
	waitFor(t, tt, "the session to end", func() bool { return !slices.Contains(sessionIDs(t, remote), sid) })
	close(held.release)
	waitFor(t, tt, "the upload", func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if _, err := os.Stat(part); err == nil {
		t.Error("the partial stays")
	}
}

// TestPaneEndStopsUploads stops a session's uploads as the session is
// ended with its pane, and not as the pane moves to another window, nor
// as it goes with the session going on, as its host's removal drops it.
func TestPaneEndStopsUploads(t *testing.T) {
	a, _ := newTestApp(t)
	a.split(false) // the tab stays
	p := a.tab().Focus
	p.host = newHostConn(savedHost{ID: "h1", Name: "office-mac"})
	ctx := p.host.transfersOf(p.SID).ctx
	p.end()
	p.closed = false
	a.dropPane(p, false)
	if ctx.Err() != nil {
		t.Fatal("a pane gone, its session going on, stops its upload")
	}
	q := a.tab().Focus
	q.host = p.host
	ctx = q.host.transfersOf(q.SID).ctx
	a.closePane(q)
	if ctx.Err() == nil {
		t.Error("ending the session leaves its upload")
	}
}

// TestPasteWaitsForHost drops a folder, whose path here is pasted, on a
// pane of a host while it reconnects: the paste waits for the host. One
// that waits too long says why on the list.
func TestPasteWaitsForHost(t *testing.T) {
	a, tt := newTestApp(t)
	h, proxy, p := remotePane(t, a, tt, t.TempDir())
	folder := t.TempDir()
	proxy.setDown(true)
	waitFor(t, tt, "the host to go", func() bool { return h.current() == nil })
	a.upload(p, []string{folder})
	proxy.setDown(false)
	h.poke()
	waitFor(t, tt, "the folder's path", func() bool { return strings.Contains(p.term.Text(), filepath.Base(folder)) })
	waitUploaded(t, tt, p) // nothing reads hostWait now

	proxy.setDown(true)
	waitFor(t, tt, "the host to go", func() bool { return h.current() == nil })
	hostWait = 100 * time.Millisecond
	t.Cleanup(func() { hostWait = time.Minute })
	a.upload(p, []string{folder})
	waitUploaded(t, tt, p)
	if l, why := listOf(p), hostWhy(h); why == "" || l[len(l)-1].err != why {
		t.Errorf("the list: %+v, the host %q", l[len(l)-1], why)
	}
}

// TestUploadsOnlyToHostsTaking takes drops only on panes of a host that is
// connected and takes files; any other pane's drop is its terminal's.
func TestUploadsOnlyToHostsTaking(t *testing.T) {
	a, tt := newTestApp(t)
	local := a.tab().Focus
	h, proxy, p := remotePane(t, a, tt, "")
	if transfersFiles(local) || !transfersFiles(p) {
		t.Fatalf("transfers files: local %v, remote %v", transfersFiles(local), transfersFiles(p))
	}
	caps := h.hello.Capabilities
	h.hello.Capabilities = nil
	if transfersFiles(p) {
		t.Error("uploads to a host that takes no files")
	}
	h.hello.Capabilities = caps
	proxy.setDown(true)
	waitFor(t, tt, "the host to go", func() bool { return h.current() == nil })
	if !transfersFiles(p) {
		t.Error("no uploads to a host coming back")
	}
	if transfersFiles(&Pane{host: newHostConn(savedHost{ID: "h2", Name: "never"})}) {
		t.Error("uploads to a host never connected")
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
	if len(listOf(p)) != 0 || uploadingOf(p) != nil {
		t.Errorf("an upload to a host that takes none: %v", listOf(p))
	}
}

// TestDropOnLocalPane keeps a drop on a pane of this machine the terminal's:
// the path is pasted, nothing is sent. A space follows it, so that a second
// drop's path does not join it (#204).
func TestDropOnLocalPane(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	waitFor(t, tt, "the shell", func() bool { return p.term != nil && strings.Contains(p.term.Text(), testShell.prompt) })
	paths := dropped(t, "here.txt", "x", "there.txt", "y")
	if !tt.DropFiles(500, 300, paths[0]) {
		t.Fatal("no element took the drop")
	}
	waitFor(t, tt, "the path", func() bool { return strings.Contains(p.term.Text(), "here.txt") })
	if len(listOf(p)) != 0 || uploadingOf(p) != nil {
		t.Errorf("a local drop uploads: %v", listOf(p))
	}
	// The shell, a plain sh, echoes what the drop pasted, quoted: as the
	// path is only when nothing in it needs quoting.
	if runtime.GOOS == "windows" || strings.ContainsFunc(paths[1], func(r rune) bool {
		return !(r == '/' || r == '.' || r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r))
	}) {
		return
	}
	if !tt.DropFiles(500, 300, paths[1]) {
		t.Fatal("no element took the second drop")
	}
	waitFor(t, tt, "the second path after a space", func() bool {
		return strings.Contains(strings.ReplaceAll(p.term.Text(), "\n", ""), "here.txt "+paths[1])
	})
}

// TestTransferList shows a pane's transfers: progress, a file there for a
// while, a failure until clicked, and a cancel that stops one.
func TestTransferList(t *testing.T) {
	a, tt := newTestApp(t)
	p := a.tab().Focus
	ctx, cancel := context.WithCancel(context.Background())
	active := &fileTransfer{name: "big.bin", size: 4_800_000, done: 1_200_000, cancel: cancel}
	p.host = newHostConn(savedHost{ID: "h1", Name: "office-mac"})
	ts := p.host.transfersOf(p.SID)
	ts.list = []*fileTransfer{
		active,
		{name: "small.txt", size: 10, done: 10, ok: true},
		{name: "bad.txt", size: 10, err: "disk full"},
		{name: "new.bin", from: "/tmp/new.bin", cancel: cancel},
	}
	tt.Frame()
	if !tt.HasText("…") || tt.HasText("0 B of 0 B") {
		t.Errorf("a download of no size yet: texts %q", tt.Texts())
	}
	ts.list = ts.list[:3]
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
	if len(listOf(p)) != 0 || tt.HasText("bad.txt") {
		t.Errorf("the list stays: %v", listOf(p))
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
		{"/bin/zsh", `/tmp/a.txt '/tmp/b c.txt' `},
		{"", `/tmp/a.txt '/tmp/b c.txt' `},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, `/tmp/a.txt '/tmp/b c.txt' `},
		{`C:\Windows\System32\cmd.exe`, `/tmp/a.txt "/tmp/b c.txt" `},
	} {
		if got := pastedPaths(c.shell, []string{"/tmp/a.txt", "/tmp/b c.txt"}); got != c.want {
			t.Errorf("pastedPaths(%q) = %q, want %q", c.shell, got, c.want)
		}
	}
}

// TestRefusedPathNotPasted leaves out of a drop's paste a path the shell's
// quoting refuses: cmd.exe would expand its % and run what follows its &.
// Nothing pastes when no path is left.
func TestRefusedPathNotPasted(t *testing.T) {
	cmd := `C:\Windows\System32\cmd.exe`
	if got := pastedPaths(cmd, []string{`C:\t\a.txt`, `C:\t\a&b%c%.txt`, `C:\t\b c.txt`}); !strings.HasPrefix(got, `"C:\t\a.txt" "C:\t\b c.txt"`) || strings.Contains(got, "%") {
		t.Errorf("pasted %q", got)
	}
	if got := pastedPaths(cmd, []string{`C:\t\a%b%.txt`}); got != "" {
		t.Errorf("pasted %q for a refused path alone", got)
	}
}

// TestDropNameForShell drops a file whose name the pane's shell cannot
// take on a host: into its temporary folder it goes by a name the shell
// takes, which is pasted; into the shell's directory, of a host before
// temporary folders, it keeps its name, and the list says, as it says a
// file is done, why it was not pasted.
func TestDropNameForShell(t *testing.T) {
	if testShell.path == "cmd.exe" {
		t.Skip("POSIX shell")
	}
	there := dropsDir(t)
	a, tt := newTestApp(t)
	cwd := t.TempDir()
	h, _, p := remotePane(t, a, tt, cwd)
	p.info.Shell = `C:\Windows\System32\cmd.exe` // as the quoting goes
	a.upload(p, dropped(t, "50%!.txt", "x"))
	waitUploaded(t, tt, p)
	waitFor(t, tt, "the path there", func() bool { return strings.Contains(p.term.Text(), "50__.txt") })
	if _, err := os.Stat(filepath.Join(there, "50__.txt")); err != nil {
		t.Error(err)
	}
	h.hello.Capabilities = slices.DeleteFunc(slices.Clone(h.hello.Capabilities), func(c string) bool { return c == rex.CapabilityFileTemp })
	a.upload(p, dropped(t, "50%!.txt", "x"))
	x := listOf(p)[len(listOf(p))-1]
	waitUploaded(t, tt, p)
	if _, err := os.Stat(filepath.Join(cwd, "50%!.txt")); err != nil {
		t.Error(err)
	}
	// The row is of a file there, with why it was not pasted; it goes as
	// one done does.
	if !x.ok || x.err != "" || x.note != locales.PaneUploadNotPasted() || !tt.HasText(x.note) {
		t.Errorf("the row: %+v", x)
	}
	tt.Advance(transferShown)
	tt.Frame()
	if slices.Contains(listOf(p), x) {
		t.Error("the row stays")
	}
}

// downloadsHere makes Command-clicked files of hosts go into a new
// directory, which it returns.
func downloadsHere(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := downloadsDir
	downloadsDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { downloadsDir = old })
	return dir
}

// clicked Command-clicks link in p, a pane of a host, and waits for its
// download to end, which it returns.
func clicked(t *testing.T, a *App, tt *ui.Tester, p *Pane, link string) *fileTransfer {
	t.Helper()
	if !a.openLink(p, link) {
		t.Fatalf("%s not taken", link)
	}
	x := listOf(p)[len(listOf(p))-1]
	waitFor(t, tt, link, func() bool { return x.ok || x.err != "" })
	return x
}

// TestDownloadFromHost Command-clicks paths printed in a pane of a host:
// each file comes into Downloads, renamed when the name is taken, and
// opens; a relative path is of the shell's directory there, "~/" of the
// home there, and :line:col is trimmed. A folder and a missing file say
// why until clicked. A URL is the system's.
func TestDownloadFromHost(t *testing.T) {
	a, tt := newTestApp(t)
	there := testDir(t)
	t.Cleanup(func() { os.RemoveAll(there) })
	os.WriteFile(filepath.Join(there, "report.txt"), []byte("report"), 0o644)
	os.Mkdir(filepath.Join(there, "sub"), 0o755)
	os.WriteFile(filepath.Join(there, "sub", "notes.go"), []byte("package x"), 0o644)
	os.Mkdir(filepath.Join(there, "folder"), 0o755)
	os.WriteFile(filepath.Join(there, "linked.txt"), []byte("linked"), 0o644)
	_, _, p := remotePane(t, a, tt, there)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	os.WriteFile(filepath.Join(home, "h.txt"), []byte("home"), 0o644)
	downloads := downloadsHere(t)
	os.WriteFile(filepath.Join(downloads, "report.txt"), []byte("old"), 0o644)

	for _, c := range []struct{ link, name, want string }{
		{"report.txt", "report (1).txt", "report"},
		{"sub/notes.go:12:3", "notes.go", "package x"},
		{"~/h.txt", "h.txt", "home"},
		{(&url.URL{Scheme: "file", Host: "office-mac", Path: filepath.ToSlash(filepath.Join(there, "linked.txt"))}).String(), "linked.txt", "linked"},
	} {
		x := clicked(t, a, tt, p, c.link)
		path := filepath.Join(downloads, c.name)
		if !x.ok {
			t.Fatalf("%s: %+v", c.link, x)
		}
		if b, err := os.ReadFile(path); err != nil || string(b) != c.want {
			t.Errorf("%s: %q, %v", c.link, b, err)
		}
		if !slices.Contains(tt.OpenedURLs(), fileURL(path)) {
			t.Errorf("%s not opened: %q", path, tt.OpenedURLs())
		}
		if !quarantined(t, path) {
			t.Errorf("%s not quarantined", path)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(downloads, "report.txt")); string(b) != "old" {
		t.Errorf("the file there already became %q", b)
	}
	opened := len(tt.OpenedURLs())
	for _, link := range []string{"folder", "folder/"} {
		if x := clicked(t, a, tt, p, link); x.err != "Folders are not downloaded yet" {
			t.Errorf("a folder %s: %+v", link, x)
		}
	}
	if x := clicked(t, a, tt, p, "missing.txt"); !strings.Contains(x.err, "no such file") {
		t.Errorf("a missing file: %+v", x)
	}
	if a.openLink(p, "https://example.com/a.txt") {
		t.Error("a URL is downloaded")
	}
	if len(tt.OpenedURLs()) != opened {
		t.Errorf("opened %q", tt.OpenedURLs()[opened:])
	}
	tt.Advance(transferShown)
	tt.Frame()
	if !tt.HasText("Folders are not downloaded yet") || tt.HasText("report.txt") {
		t.Errorf("texts after a while %q", tt.Texts())
	}
	if err := tt.Click("Folders are not downloaded yet"); err != nil {
		t.Fatal(err)
	}
	if len(listOf(p)) != 2 {
		t.Errorf("transfers after a click: %v", listOf(p))
	}
	if left, _ := filepath.Glob(filepath.Join(downloads, ".gorex-*.partial")); len(left) > 0 {
		t.Errorf("partials left: %v", left)
	}
}

// TestDownloadCancelled cancels a download, as its pane closes: what
// Downloads has of the file goes, an earlier try's too, and nothing opens.
// A second click on a file being downloaded starts no other; one on its
// name printed in another directory does.
func TestDownloadCancelled(t *testing.T) {
	a, tt := newTestApp(t)
	there := testDir(t)
	t.Cleanup(func() { os.RemoveAll(there) })
	os.WriteFile(filepath.Join(there, "big.bin"), []byte(strings.Repeat("x", 1<<20)), 0o644)
	_, _, p := remotePane(t, a, tt, there)
	downloads := downloadsHere(t)
	part := partialOf(downloads, "big.bin", rex.DownloadID(p.SID, hostPath(p.info.Dir, "big.bin")))
	os.WriteFile(part, []byte("xxxx"), 0o644)
	if !a.openLink(p, "big.bin") || !a.openLink(p, "big.bin:3") {
		t.Fatal("the link not taken")
	}
	if len(listOf(p)) != 1 {
		t.Fatalf("transfers %v", listOf(p))
	}
	// The same name printed in another directory is another file.
	dir := p.info.Dir
	p.info.Dir = filepath.Join(dir, "other")
	a.openLink(p, "big.bin")
	p.info.Dir = dir
	if len(listOf(p)) != 2 {
		t.Fatalf("transfers %v", listOf(p))
	}
	list := listOf(p)
	a.closePane(p)
	for _, x := range list {
		waitFor(t, tt, "the download to stop", func() bool { return x.err != "" })
	}
	if _, err := os.Stat(part); err == nil {
		t.Error("the partial stays")
	}
	if _, err := os.Stat(filepath.Join(downloads, "big.bin")); err == nil {
		t.Error("the file came")
	}
	if len(tt.OpenedURLs()) != 0 {
		t.Errorf("opened %q", tt.OpenedURLs())
	}
}

// TestOpenLinkOnlyFromHostsTaking downloads only from a host connected
// that transfers files; a link in a pane of this machine opens here, as
// before.
func TestOpenLinkOnlyFromHostsTaking(t *testing.T) {
	a, tt := newTestApp(t)
	local := a.tab().Focus
	downloads := downloadsHere(t)
	here := dropped(t, "here.txt", "x")[0]
	if !a.openLink(local, here) || !slices.Equal(tt.OpenedURLs(), []string{fileURL(here)}) || len(listOf(local)) != 0 {
		t.Errorf("a local link: opened %q, transfers %v", tt.OpenedURLs(), listOf(local))
	}
	there := testDir(t)
	t.Cleanup(func() { os.RemoveAll(there) })
	os.WriteFile(filepath.Join(there, "f.txt"), []byte("f"), 0o644)
	h, proxy, p := remotePane(t, a, tt, there)
	caps := h.hello.Capabilities
	h.hello.Capabilities = nil
	if a.openLink(p, "f.txt") || len(listOf(p)) != 0 {
		t.Error("a download from a host that transfers no files")
	}
	h.hello.Capabilities = caps
	proxy.setDown(true)
	waitFor(t, tt, "the host to go", func() bool { return h.current() == nil })
	if a.openLink(p, "f.txt") || len(listOf(p)) != 0 {
		t.Error("a download from a host not connected")
	}
	if entries, _ := os.ReadDir(downloads); len(entries) != 0 || len(tt.OpenedURLs()) != 1 {
		t.Errorf("downloads %v, opened %q", entries, tt.OpenedURLs())
	}
}

func TestHostPath(t *testing.T) {
	for _, c := range []struct{ dir, path, want string }{
		{"/srv/app", "config.json", "/srv/app/config.json"},
		{"/srv/app/", "sub/", "/srv/app/sub/"},
		{"/", "a", "/a"},
		{"", "config.json", "config.json"},
		{"/srv/app", "/etc/hosts", "/etc/hosts"},
		{"/srv/app", "~/a", "~/a"},
		{`C:\Users\yibo`, `a\b.txt`, `C:\Users\yibo\a\b.txt`},
		{`C:\Users\yibo`, `D:\b.txt`, `D:\b.txt`},
	} {
		if got := hostPath(c.dir, c.path); got != c.want {
			t.Errorf("hostPath(%q, %q) = %q, want %q", c.dir, c.path, got, c.want)
		}
	}
}

// TestTransferListContrast draws the transfer list without glass, light
// and dark: its progress track and its edge stand out from the panel.
func TestTransferListContrast(t *testing.T) {
	a, tt := newTestApp(t)
	old := prefs.NoGlass
	prefs.NoGlass = true
	t.Cleanup(func() { prefs.NoGlass = old })
	p := a.tab().Focus
	p.host = newHostConn(savedHost{ID: "h1", Name: "office-mac"})
	p.host.transfersOf(p.SID).list = []*fileTransfer{{name: "big.bin", size: 100, cancel: func() {}}}
	// spread is how far apart in brightness, of 255, the pixels of img
	// from x0, y0 to x1, y1 are.
	spread := func(img image.Image, x0, y0, x1, y1 int) float64 {
		lo, hi := 255.0, 0.0
		for x := x0; x <= x1; x++ {
			for y := y0; y <= y1; y++ {
				r, g, b, _ := img.At(x, y).RGBA()
				l := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 257
				lo, hi = min(lo, l), max(hi, l)
			}
		}
		return hi - lo
	}
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		tt.Advance(time.Second) // the list's entrance
		tt.Frame()
		name, ok := tt.Find("big.bin")
		if !ok {
			t.Fatalf("no row; texts %q", tt.Texts())
		}
		// The track runs under the name, whose text ends well before its
		// box; the list's edge is 13 left of the name, past the row's
		// padding and the list's.
		img, x, y := tt.Image(), int(name.X+name.W)-10, int(name.Y+name.H)
		track := spread(img, x, y, x, y+8)
		edge := spread(img, int(name.X)-16, y, int(name.X)-8, y)
		if track < 12 || edge < 12 {
			t.Errorf("dark %v: the track differs from the panel by %.1f, the edge by %.1f, of 255", dark, track, edge)
		}
	}
}

// listOf is the transfer list of p's session.
func listOf(p *Pane) []*fileTransfer {
	if p.host == nil || p.host.transfers[p.SID] == nil {
		return nil
	}
	return p.host.transfers[p.SID].list
}

// uploadingOf closes once the uploads into p's session end.
func uploadingOf(p *Pane) chan struct{} {
	if p.host == nil || p.host.transfers[p.SID] == nil {
		return nil
	}
	return p.host.transfers[p.SID].uploading
}

// gateUploads holds each upload's open until the returned func is called.
func gateUploads(t *testing.T) (release func()) {
	t.Helper()
	gate := make(chan struct{})
	old := openUpload
	openUpload = func(path string) (uploadFile, error) {
		<-gate
		return os.Open(path)
	}
	t.Cleanup(func() { openUpload = old })
	return sync.OnceFunc(func() { close(gate) })
}

// movedPane is the pane of sid in b, nil when none.
func movedPane(b *App, sid string) *Pane {
	for _, t := range b.tabs {
		for _, q := range t.allPanes() {
			if q.SID == sid {
				return q
			}
		}
	}
	return nil
}

// TestUploadMovesWithItsTab moves a tab to a new window while a file
// dropped on its pane goes to the host, and closes the window it was
// dropped in: the new window's pane lists the transfer and gets its path
// pasted.
func TestUploadMovesWithItsTab(t *testing.T) {
	dropsDir(t)
	a, tt := newTestApp(t)
	_, _, p := remotePane(t, a, tt, t.TempDir())
	release := gateUploads(t)
	defer release()
	a.upload(p, dropped(t, "moved.txt", "x"))
	sid, x := p.SID, listOf(p)[0]
	a.moveTabToNewWindow(p.Tab)
	b := a.st.apps[0]
	q := movedPane(b, sid)
	if b == a || q == nil || q.term == nil {
		t.Fatal("the tab did not move")
	}
	if !slices.Contains(listOf(q), x) {
		t.Fatalf("the moved pane's transfers %v", listOf(q))
	}
	a.st.closed(a)
	release()
	waitFor(t, tt, "the path there", func() bool { return strings.Contains(q.term.Text(), "moved.txt") })
	waitFor(t, tt, "the upload", func() bool { return x.ok })
}

// TestUploadMovesWithItsPane is TestUploadMovesWithItsTab for a pane moved
// into another window's tab.
func TestUploadMovesWithItsPane(t *testing.T) {
	dropsDir(t)
	a, tt := newTestApp(t)
	h, _, p := remotePane(t, a, tt, t.TempDir())
	b := a.st.openWindow(a.st.addWorkspace())
	b.newTabOn(h, t.TempDir())
	release := gateUploads(t)
	defer release()
	a.upload(p, dropped(t, "moved.txt", "x"))
	sid, x := p.SID, listOf(p)[0]
	a.movePaneToWindow(p, b)
	q := movedPane(b, sid)
	if q == nil || q.term == nil {
		t.Fatal("the pane did not move")
	}
	if !slices.Contains(listOf(q), x) {
		t.Fatalf("the moved pane's transfers %v", listOf(q))
	}
	release()
	waitFor(t, tt, "the path there", func() bool { return strings.Contains(q.term.Text(), "moved.txt") })
}

// TestClosedPaneDropsItsPaste closes a pane while a file dropped on it
// waits to go: the upload stops and its session's list goes with it.
func TestClosedPaneDropsItsPaste(t *testing.T) {
	dropsDir(t)
	a, tt := newTestApp(t)
	h, _, p := remotePane(t, a, tt, t.TempDir())
	a.split(false) // the tab stays
	release := gateUploads(t)
	defer release()
	a.upload(p, dropped(t, "closed.txt", "x"))
	x, done := listOf(p)[0], uploadingOf(p)
	a.closePane(p)
	if h.transfers[p.SID] != nil {
		t.Error("the closed pane's session keeps its transfers")
	}
	release()
	waitFor(t, tt, "the upload to stop", func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if x.ok {
		t.Error("the upload went on")
	}
}

// TestDropWaitsForHost drops a file on a pane of a host while it
// reconnects: the drop is the upload's, which waits for the host and
// pastes the path there once it is back. One that waits too long says
// why the host is not back, as its status does.
func TestDropWaitsForHost(t *testing.T) {
	dropsDir(t)
	a, tt := newTestApp(t)
	h, proxy, p := remotePane(t, a, tt, t.TempDir())
	proxy.setDown(true)
	waitFor(t, tt, "the host to go", func() bool { return h.current() == nil })
	if !transfersFiles(p) {
		t.Fatal("the drop not taken")
	}
	a.upload(p, dropped(t, "late.txt", "x"))
	x := listOf(p)[0]
	proxy.setDown(false)
	h.poke()
	waitFor(t, tt, "the path there", func() bool { return strings.Contains(p.term.Text(), "late.txt") })
	waitFor(t, tt, "the upload", func() bool { return x.ok })
	waitUploaded(t, tt, p) // nothing reads hostWait now

	hostWait = 100 * time.Millisecond
	t.Cleanup(func() { hostWait = time.Minute })
	proxy.setDown(true)
	waitFor(t, tt, "the host to fail", func() bool { return h.state == hostFailed })
	a.upload(p, dropped(t, "never.txt", "x"))
	x = listOf(p)[len(listOf(p))-1]
	waitUploaded(t, tt, p)
	// As its status says it.
	if why := hostWhy(h); why == "" || x.err != why {
		t.Errorf("the upload: %+v, the host %q", x, why)
	}
}

// resumedFile is a file of size zeros whose first open reads half, then
// waits for release and goes on; it tells reached once there.
type resumedFile struct {
	size             int64
	reached, release chan struct{}
	reaching         *sync.Once
}

func (f resumedFile) ReadAt(b []byte, off int64) (int, error) {
	if off >= f.size/2 {
		f.reaching.Do(func() { close(f.reached) })
		<-f.release
	}
	if off >= f.size {
		return 0, io.EOF
	}
	b = b[:min(int64(len(b)), f.size-off)]
	clear(b)
	return len(b), nil
}

func (resumedFile) Close() error { return nil }

// TestUploadOutlivesConnection loses the connection to the host halfway
// through a file: the upload goes on through the next connection, from
// what the host has, and pastes the path there; no second copy is made.
func TestUploadOutlivesConnection(t *testing.T) {
	there := dropsDir(t)
	a, tt := newTestApp(t)
	h, proxy, p := remotePane(t, a, tt, t.TempDir())
	const size = 4 << 20
	path := dropped(t, "kept.bin", string(make([]byte, size)))[0]
	f := resumedFile{size: size, reached: make(chan struct{}), release: make(chan struct{}), reaching: new(sync.Once)}
	old := openUpload
	openUpload = func(string) (uploadFile, error) { return f, nil }
	t.Cleanup(func() { openUpload = old })
	c := h.current()
	a.upload(p, []string{path})
	<-f.reached
	proxy.setDown(true)
	waitFor(t, tt, "the host to go", func() bool { return h.current() != c })
	close(f.release)
	proxy.setDown(false)
	h.poke()
	waitFor(t, tt, "the path there", func() bool { return strings.Contains(p.term.Text(), "kept.bin") })
	if st, err := os.Stat(filepath.Join(there, "kept.bin")); err != nil || st.Size() != size {
		t.Errorf("there: %v, %v", st, err)
	}
	if entries, _ := os.ReadDir(there); len(entries) != 1 {
		t.Errorf("there: %v", entries)
	}
}

// TestUploadWaitsForHeldFile drops a file whose partial on the host another
// put holds, as the put of a connection lost does until the host notices:
// the upload waits for it to let go, then sends the file.
func TestUploadWaitsForHeldFile(t *testing.T) {
	there := dropsDir(t)
	a, tt := newTestApp(t)
	_, _, p, remote := remotePaneOf(t, a, tt, t.TempDir())
	path := dropped(t, "held.bin", string(make([]byte, 1<<20)))[0]
	fi, _ := os.Stat(path)
	ctx, cancel := context.WithCancel(context.Background())
	held := heldFile{size: 1 << 20, reached: make(chan struct{}), release: make(chan struct{}), reaching: new(sync.Once)}
	go remote.PutFile(ctx, rex.FilePut{SID: p.SID, Name: "held.bin", Size: 1 << 20, ID: uploadID(p.SID, path, fi), Temp: true}, held, nil)
	<-held.reached
	var opens atomic.Int32
	old := openUpload
	openUpload = func(path string) (uploadFile, error) {
		opens.Add(1)
		return os.Open(path)
	}
	t.Cleanup(func() { openUpload = old })
	a.upload(p, []string{path})
	x := listOf(p)[0]
	// Refused once, it tries again.
	waitFor(t, tt, "a try again", func() bool { return opens.Load() >= 2 })
	if x.err != "" || x.ok {
		t.Fatalf("the upload did not wait: %+v", x)
	}
	cancel()
	close(held.release)
	waitFor(t, tt, "the path there", func() bool { return strings.Contains(p.term.Text(), "held.bin") })
	if st, err := os.Stat(filepath.Join(there, "held.bin")); err != nil || st.Size() != 1<<20 {
		t.Errorf("there: %v, %v", st, err)
	}
}

// hostWhy is why h is not connected, as its status says.
func hostWhy(h *hostConn) string {
	_, why, _ := hostStatus(h)
	return why
}
