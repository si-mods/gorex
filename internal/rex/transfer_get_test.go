//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestGetFile: GetFile answers a file's size, name and time, then reads
// its bytes; a folder and a missing file fail at once.
func TestGetFile(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	dir := t.TempDir()
	sid := transferSession(t, c, c, shellDir(t))
	data := bytes.Repeat([]byte("gorex "), 50_000)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	rc, a, err := c.GetFile(context.Background(), sid, filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || !bytes.Equal(got, data) || a.Size != int64(len(data)) || a.Name != "a.txt" || a.MTime.IsZero() {
		t.Fatalf("get: %d bytes, %v; answer %+v", len(got), err, a)
	}
	if _, _, err := c.GetFile(context.Background(), sid, dir); !Folder(err) || NotRegular(err) {
		t.Errorf("get of a folder: %v", err)
	}
	if _, _, err := c.GetFile(context.Background(), sid, filepath.Join(dir, "missing")); err == nil || Folder(err) || NotRegular(err) {
		t.Errorf("get of a missing file: %v", err)
	}
}

// shellDir is a directory for a session to start in, removed when the
// test ends as far as it can be: on Windows the shell may hold it a
// while after it is killed.
func shellDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gorex-shell")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for range 50 {
			if os.RemoveAll(dir) == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	return dir
}
