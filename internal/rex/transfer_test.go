//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tailscale.com/types/key"
)

// transferSession starts a session of c's in dir, which the local
// client ends.
func transferSession(t *testing.T, local, c *Client, dir string) string {
	t.Helper()
	cmd, _ := testShell()
	info, err := c.Create(CreateOptions{Command: cmd, Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { local.Kill(info.ID) })
	return info.ID
}

// put sends data as name into dir of sid, telling the first progress.
func put(c *Client, sid, dir, name, id, conflict string, data []byte) (path string, skipped bool, first int64, err error) {
	first = -1
	path, skipped, err = c.PutFile(context.Background(), FilePut{SID: sid, Dir: dir, Name: name, Size: int64(len(data)), ID: id, Conflict: conflict}, bytes.NewReader(data), func(done int64) {
		if first < 0 {
			first = done
		}
	})
	return
}

func wantFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s has %q, want %q", path, got, want)
	}
}

func TestFileRoundTrip(t *testing.T) {
	s, _ := testServer(t)
	local := pipe(s, "")
	defer local.Close()
	alice := key.NewNode().Public().String()
	remote := pipe(s, alice)
	defer remote.Close()
	data := bytes.Repeat([]byte("gorex "), 100_000)
	for _, c := range []struct {
		name   string
		client *Client
	}{{"local", local}, {"remote", remote}} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			sid := transferSession(t, local, c.client, dir)
			path, skipped, first, err := put(c.client, sid, dir, "a.txt", "id1", "", data)
			if err != nil || skipped || first != 0 || path != filepath.Join(dir, "a.txt") {
				t.Fatalf("put: %q %v %d %v", path, skipped, first, err)
			}
			wantFile(t, path, data)
			// No dir: the session's.
			path, _, _, err = put(c.client, sid, "", "b.txt", "id2", "", data[:10])
			if err != nil {
				t.Fatal(err)
			}
			if want, _ := filepath.EvalSymlinks(filepath.Join(dir, "b.txt")); path != want && path != filepath.Join(dir, "b.txt") {
				t.Errorf("put without a dir to %q, want %q", path, want)
			}
			wantFile(t, filepath.Join(dir, "b.txt"), data[:10])

			dest := t.TempDir()
			var done, size int64
			got, err := c.client.DownloadFile(context.Background(), sid, "a.txt", dest, "", func(d, n int64) { done, size = d, n })
			if err != nil || got != filepath.Join(dest, "a.txt") || done != int64(len(data)) || size != done {
				t.Fatalf("download: %q %v, %d of %d", got, err, done, size)
			}
			wantFile(t, got, data)
			src, _ := os.Stat(filepath.Join(dir, "a.txt"))
			if st, _ := os.Stat(got); !st.ModTime().Equal(src.ModTime()) {
				t.Errorf("downloaded at %v, the file's %v", st.ModTime(), src.ModTime())
			}
			if left, _ := filepath.Glob(filepath.Join(dest, ".gorex-*.partial")); len(left) > 0 {
				t.Errorf("partials left: %v", left)
			}
			// A directory is no file.
			if _, err := c.client.DownloadFile(context.Background(), sid, dir, dest, "", nil); err == nil || !strings.Contains(err.Error(), "not a regular file") {
				t.Errorf("download of a directory: %v", err)
			}
		})
	}
}

func TestFileNotShared(t *testing.T) {
	s, _ := testServer(t)
	local := pipe(s, "")
	defer local.Close()
	alice, bob := key.NewNode().Public().String(), key.NewNode().Public().String()
	ac, bc := pipe(s, alice), pipe(s, bob)
	defer ac.Close()
	defer bc.Close()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o644)
	sid := transferSession(t, local, ac, dir)
	if _, _, _, err := put(bc, sid, dir, "a.txt", "id", "", []byte("a")); !NotShared(err) {
		t.Errorf("put by another client: %v", err)
	}
	if _, err := bc.DownloadFile(context.Background(), sid, "x", t.TempDir(), "", nil); !NotShared(err) {
		t.Errorf("get by another client: %v", err)
	}
	if _, _, _, err := put(bc, "nope", dir, "a.txt", "id", "", []byte("a")); err == nil || !strings.Contains(err.Error(), "no session") {
		t.Errorf("put to no session: %v", err)
	}
	if exists(filepath.Join(dir, "a.txt")) {
		t.Error("a refused put wrote the file")
	}
}

func TestFileResume(t *testing.T) {
	s, _ := testServer(t)
	local := pipe(s, "")
	defer local.Close()
	dir := t.TempDir()
	sid := transferSession(t, local, local, dir)
	data := bytes.Repeat([]byte("0123456789"), 10_000)

	// A connection lost halfway leaves the partial.
	conn, b := net.Pipe()
	go s.handle(b, "")
	r := bufio.NewReader(conn)
	line, _ := json.Marshal(FilePut{Op: "file.put", SID: sid, Dir: dir, Name: "r.bin", Size: int64(len(data)), ID: "r1"})
	conn.Write(append(line, '\n'))
	if a, _ := r.ReadString('\n'); !strings.Contains(a, `"ok":true`) {
		t.Fatalf("answer %q", a)
	}
	conn.Write([]byte(`{"offset":0}` + "\n"))
	conn.Write(data[:30_000])
	conn.Close()
	part := filepath.Join(dir, partialName("r.bin", "r1"))
	for i := 0; i < 100; i++ {
		if _, busy := partials.Load(part); !busy {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st, err := os.Stat(part); err != nil || st.Size() != 30_000 {
		t.Fatalf("partial %v %v", st, err)
	}
	path, _, first, err := put(local, sid, dir, "r.bin", "r1", "", data)
	if err != nil || first != 30_000 {
		t.Fatalf("resumed from %d: %v", first, err)
	}
	wantFile(t, path, data)
	if exists(part) {
		t.Error("partial kept")
	}

	// A partial of other bytes, or longer than the file, starts again.
	for _, junk := range [][]byte{bytes.Repeat([]byte("x"), 500), bytes.Repeat([]byte("x"), len(data)+1)} {
		os.WriteFile(filepath.Join(dir, partialName("c.bin", "c1")), junk, 0o644)
		path, _, first, err = put(local, sid, dir, "c.bin", "c1", "overwrite", data)
		if err != nil || first != 0 {
			t.Fatalf("corrupt partial resumed from %d: %v", first, err)
		}
		wantFile(t, path, data)
	}

	// A download goes on from its partial of the same file, and starts
	// again from one of another version.
	src := filepath.Join(dir, "r.bin")
	st, _ := os.Stat(src)
	dest := t.TempDir()
	download := func() int64 {
		t.Helper()
		first := int64(-1)
		got, err := local.DownloadFile(context.Background(), sid, src, dest, "overwrite", func(done, _ int64) {
			if first < 0 {
				first = done
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		wantFile(t, got, data)
		return first
	}
	partOf := func(content []byte, mtime time.Time) {
		p := filepath.Join(dest, partialName("r.bin", downloadID(sid, src)))
		os.WriteFile(p, content, 0o644)
		os.Chtimes(p, mtime, mtime)
	}
	partOf(data[:4000], st.ModTime())
	if first := download(); first != 4000 {
		t.Errorf("download resumed from %d, want 4000", first)
	}
	partOf(data[:4000], st.ModTime().Add(-time.Hour))
	if first := download(); first != 0 {
		t.Errorf("download of another version resumed from %d", first)
	}
}

func TestFileConflicts(t *testing.T) {
	s, _ := testServer(t)
	local := pipe(s, "")
	defer local.Close()
	dir := t.TempDir()
	sid := transferSession(t, local, local, dir)
	for i, want := range []string{"n.tar.gz", "n.tar (1).gz", "n.tar (2).gz"} {
		path, _, _, err := put(local, sid, dir, "n.tar.gz", "r", "rename", []byte{byte(i)})
		if err != nil || path != filepath.Join(dir, want) {
			t.Fatalf("rename %d: %q %v", i, path, err)
		}
		wantFile(t, path, []byte{byte(i)})
	}
	path, _, _, err := put(local, sid, dir, ".rc", "r", "", []byte("a"))
	if err == nil {
		path, _, _, err = put(local, sid, dir, ".rc", "r", "", []byte("b"))
	}
	if err != nil || path != filepath.Join(dir, ".rc (1)") {
		t.Errorf("rename of a dot file: %q %v", path, err)
	}
	path, skipped, _, err := put(local, sid, dir, "n.tar.gz", "o", "overwrite", []byte("new"))
	if err != nil || skipped || path != filepath.Join(dir, "n.tar.gz") {
		t.Fatalf("overwrite: %q %v %v", path, skipped, err)
	}
	wantFile(t, path, []byte("new"))
	path, skipped, _, err = put(local, sid, dir, "n.tar.gz", "s", "skip", []byte("skipped"))
	if err != nil || !skipped || path != filepath.Join(dir, "n.tar.gz") {
		t.Fatalf("skip: %q %v %v", path, skipped, err)
	}
	wantFile(t, path, []byte("new"))
	if _, _, _, err := put(local, sid, dir, "n", "x", "merge", nil); err == nil {
		t.Error("an unknown conflict rule")
	}
}

func TestFileBadNames(t *testing.T) {
	s, _ := testServer(t)
	local := pipe(s, "")
	defer local.Close()
	dir := t.TempDir()
	sid := transferSession(t, local, local, dir)
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "../a", "a\x00b", "a\nb", "a\x7fb", strings.Repeat("a", 256)} {
		if _, _, _, err := put(local, sid, dir, name, "id", "", []byte("a")); err == nil || !strings.Contains(err.Error(), "not a file name") {
			t.Errorf("name %q: %v", name, err)
		}
	}
	for _, id := range []string{"", "a b", "../x", "a/b", "é", strings.Repeat("a", 65)} {
		if _, _, _, err := put(local, sid, dir, "a", id, "", []byte("a")); err == nil || !strings.Contains(err.Error(), "not a transfer id") {
			t.Errorf("id %q: %v", id, err)
		}
	}
	if _, _, _, err := put(local, sid, filepath.Join(dir, "none"), "a", "id", "", []byte("a")); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("into no directory: %v", err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("refused puts left %v", ents)
	}
}

func TestFileEvents(t *testing.T) {
	s, _ := testServer(t)
	local := pipe(s, "")
	defer local.Close()
	sub, plain := pipe(s, ""), pipe(s, "")
	defer sub.Close()
	defer plain.Close()
	if err := sub.SubscribeTransfers(); err != nil {
		t.Fatal(err)
	}
	if err := plain.Subscribe(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sid := transferSession(t, local, local, dir)
	if _, _, _, err := put(local, sid, dir, "e.txt", "ev1", "", []byte("events")); err != nil {
		t.Fatal(err)
	}
	if _, err := local.DownloadFile(context.Background(), sid, "e.txt", t.TempDir(), "", nil); err != nil {
		t.Fatal(err)
	}
	final := map[string]Transfer{}
	timeout := time.After(3 * time.Second)
	for len(final) < 2 {
		select {
		case ev := <-sub.Events():
			if ev.Kind != "transfer" || ev.Transfer == nil {
				continue
			}
			if tr := *ev.Transfer; tr.State != "active" {
				final[tr.Direction] = tr
			}
		case <-timeout:
			t.Fatalf("final states: %+v", final)
		}
	}
	in, out := final["in"], final["out"]
	if in.ID != "ev1" || in.SID != sid || in.Name != "e.txt" || in.State != "done" || in.Done != 6 || in.Size != 6 || in.Path != filepath.Join(dir, "e.txt") {
		t.Errorf("put's event %+v", in)
	}
	if out.State != "done" || out.Done != 6 || out.Name != "e.txt" || out.Path == "" {
		t.Errorf("get's event %+v", out)
	}
	// A subscriber that did not ask is told of none.
	for {
		select {
		case ev := <-plain.Events():
			if ev.Kind == "transfer" {
				t.Fatalf("a plain subscriber got %+v", ev)
			}
			continue
		case <-time.After(300 * time.Millisecond):
		}
		break
	}
}

// TestFileNoCapability checks that a client refuses a server that does
// not transfer files.
func TestFileNoCapability(t *testing.T) {
	a, b := net.Pipe()
	go func() {
		r := bufio.NewReader(b)
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				return
			}
			var req Request
			json.Unmarshal(line, &req)
			res, _ := json.Marshal(Response{ID: req.ID, Data: json.RawMessage(`{"version":3,"capabilities":["layout.revision"]}`)})
			b.Write(append(res, '\n'))
		}
	}()
	c := newClient(a, func() (net.Conn, error) { return nil, errors.New("dialed") })
	defer c.Close()
	if _, _, _, err := put(c, "s", "", "a", "id", "", []byte("a")); !errors.Is(err, errNoFileTransfer) {
		t.Errorf("put: %v", err)
	}
	if _, err := c.DownloadFile(context.Background(), "s", "a", t.TempDir(), "", nil); !errors.Is(err, errNoFileTransfer) {
		t.Errorf("download: %v", err)
	}
}

// TestFileLongName sends a file of the longest name: its partial's name
// is no longer.
func TestFileLongName(t *testing.T) {
	s, _ := testServer(t)
	local := pipe(s, "")
	defer local.Close()
	dir := t.TempDir()
	sid := transferSession(t, local, local, dir)
	name := strings.Repeat("n", 251) + ".txt"
	path, _, _, err := put(local, sid, dir, name, strings.Repeat("i", 64), "", []byte("long"))
	if err != nil {
		t.Fatal(err)
	}
	wantFile(t, path, []byte("long"))
	if got, err := local.DownloadFile(context.Background(), sid, name, t.TempDir(), "", nil); err != nil {
		t.Fatal(err)
	} else {
		wantFile(t, got, []byte("long"))
	}
}

// TestFileOverwriteMode checks that a file overwritten keeps its
// permissions.
func TestFileOverwriteMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no permission bits")
	}
	s, _ := testServer(t)
	local := pipe(s, "")
	defer local.Close()
	dir := t.TempDir()
	sid := transferSession(t, local, local, dir)
	target := filepath.Join(dir, "run.sh")
	os.WriteFile(target, []byte("old"), 0o755)
	os.Chmod(target, 0o755)
	if _, _, _, err := put(local, sid, dir, "run.sh", "m", "overwrite", []byte("new")); err != nil {
		t.Fatal(err)
	}
	wantFile(t, target, []byte("new"))
	if st, _ := os.Stat(target); st.Mode().Perm() != 0o755 {
		t.Errorf("mode %v, want 0755", st.Mode().Perm())
	}
}

// TestFilePartialLink plants a link where a partial goes: the transfer
// fails, and the file linked to stays as it was.
func TestFilePartialLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need a privilege")
	}
	s, _ := testServer(t)
	local := pipe(s, "")
	defer local.Close()
	dir, other := t.TempDir(), t.TempDir()
	sid := transferSession(t, local, local, dir)
	victim := filepath.Join(other, "victim")
	os.WriteFile(victim, []byte("keep"), 0o644)
	if err := os.Symlink(victim, filepath.Join(dir, partialName("a.txt", "l"))); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := put(local, sid, dir, "a.txt", "l", "", []byte("evil")); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("put through a link: %v", err)
	}
	wantFile(t, victim, []byte("keep"))
	if exists(filepath.Join(dir, "a.txt")) {
		t.Error("put through a link made the file")
	}
}

// TestFilePutError checks that a client still sending reads the server's
// error, not a reset: over TCP, which resets a connection closed with
// bytes unread.
func TestFilePutError(t *testing.T) {
	s, _ := testServer(t)
	local := pipe(s, "")
	defer local.Close()
	dir := t.TempDir()
	sid := transferSession(t, local, local, dir)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(c, "")
		}
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	data := make([]byte, 8<<20)
	line, _ := json.Marshal(FilePut{Op: "file.put", SID: sid, Dir: dir, Name: "e.bin", Size: int64(len(data)), ID: "e"})
	conn.Write(append(line, '\n'))
	if a, _ := r.ReadString('\n'); !strings.Contains(a, `"ok":true`) {
		t.Fatalf("answer %q", a)
	}
	// An offset the server does not have fails the put, as the bytes
	// come.
	go func() {
		conn.Write([]byte(`{"offset":5}` + "\n"))
		conn.Write(data)
	}()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	a, err := r.ReadString('\n')
	if err != nil || !strings.Contains(a, "neither 0 nor the server's") {
		t.Fatalf("answer %q %v", a, err)
	}
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Errorf("after the error: %v", err)
	}
}

// TestFileAbort removes a put's partial: not while a transfer writes it,
// and once it lets it go.
func TestFileAbort(t *testing.T) {
	s, _ := testServer(t)
	local := pipe(s, "")
	defer local.Close()
	dir := t.TempDir()
	sid := transferSession(t, local, local, dir)
	f := FilePut{SID: sid, Dir: dir, Name: "a.bin", ID: "a1"}
	part := filepath.Join(dir, partialName("a.bin", "a1"))
	os.WriteFile(part, []byte("half"), 0o644)
	partials.Store(part, true)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := local.AbortFile(ctx, f); !errors.Is(err, context.DeadlineExceeded) || !exists(part) {
		t.Fatalf("abort of a partial being written: %v, kept %v", err, exists(part))
	}
	time.AfterFunc(100*time.Millisecond, func() { partials.Delete(part) })
	if err := local.AbortFile(context.Background(), f); err != nil || exists(part) {
		t.Fatalf("abort: %v, kept %v", err, exists(part))
	}
	// One of no partial is done already.
	if err := local.AbortFile(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("abort left %v", entries)
	}
}

// TestFileSweep removes the partials of a day ago no transfer writes as a
// put into their directory starts, and keeps newer ones.
func TestFileSweep(t *testing.T) {
	s, _ := testServer(t)
	local := pipe(s, "")
	defer local.Close()
	dir := t.TempDir()
	sid := transferSession(t, local, local, dir)
	old, busy, fresh := filepath.Join(dir, partialName("o", "1")), filepath.Join(dir, partialName("b", "1")), filepath.Join(dir, partialName("f", "1"))
	other := filepath.Join(dir, ".gorex-notes")
	day := time.Now().Add(-partialAge - time.Minute)
	for _, p := range []string{old, busy, fresh, other} {
		os.WriteFile(p, []byte("x"), 0o644)
		if p != fresh {
			os.Chtimes(p, day, day)
		}
	}
	partials.Store(busy, true)
	defer partials.Delete(busy)
	if _, _, _, err := put(local, sid, dir, "n.txt", "n1", "", []byte("n")); err != nil {
		t.Fatal(err)
	}
	if exists(old) || !exists(busy) || !exists(fresh) || !exists(other) {
		t.Errorf("after the sweep: old %v, busy %v, fresh %v, other %v", exists(old), exists(busy), exists(fresh), exists(other))
	}
}
