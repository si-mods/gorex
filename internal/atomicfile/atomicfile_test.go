package atomicfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "new" {
		t.Fatalf("file has %q (err=%v), want new", b, err)
	}
	if es, err := os.ReadDir(dir); err != nil || len(es) != 1 {
		t.Fatalf("dir has %d files (err=%v), want 1", len(es), err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()
	if Write(path, []byte("newer"), 0o644) == nil {
		t.Skip("directory is writable all the same (root)")
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "new" {
		t.Fatalf("failed write left %q (err=%v), want new", b, err)
	}
}

func TestWriteSync(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sync.txt")
	if err := WriteSync(path, []byte("synced"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "synced" {
		t.Fatalf("got %q, %v; want synced", b, err)
	}
}

func TestWriteJSON(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested")
	path := filepath.Join(dir, "cfg.json")
	in := map[string]int{"a": 1}
	if err := WriteJSON(path, in, 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]int
	if err := json.Unmarshal(b, &out); err != nil || out["a"] != 1 {
		t.Fatalf("got %q -> %v, %v", b, out, err)
	}
	// Unmarshalable value returns an error without creating a file.
	bad := filepath.Join(dir, "bad.json")
	if err := WriteJSON(bad, make(chan int), 0o600); err == nil {
		t.Fatal("expected marshal error, got nil")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatalf("bad.json should not exist, stat err = %v", err)
	}
}
