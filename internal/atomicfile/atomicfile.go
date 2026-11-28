// Package atomicfile writes files atomically through a temporary file in the
// destination directory followed by a rename, with a retry loop on Windows for
// transient sharing violations while a reader has the target open.
package atomicfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Write writes data to path through a temporary file beside it and renames it
// into place. A crash or failure leaves either the previous file or the new
// content, never a partial write. The parent directory must already exist.
func Write(path string, data []byte, perm os.FileMode) error {
	return write(path, data, perm, false)
}

// WriteSync behaves like Write and calls Sync on the temporary file before
// closing and renaming it.
func WriteSync(path string, data []byte, perm os.FileMode) error {
	return write(path, data, perm, true)
}

// WriteJSON marshals v as indented JSON with a trailing newline, creates parent
// directories if needed (0o700 for user-only perm, else 0o755), and writes path
// atomically.
func WriteJSON(path string, v any, perm os.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dirPerm := os.FileMode(0o755)
	if perm&0o077 == 0 {
		dirPerm = 0o700
	}
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return err
	}
	return write(path, b, perm, false)
}

func write(path string, data []byte, perm os.FileMode, syncFile bool) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	_, err = f.Write(data)
	if err == nil && syncFile {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, perm)
	}
	if err == nil {
		err = rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

func rename(src, dst string) error {
	err := os.Rename(src, dst)
	// On Windows the rename fails while a reader (such as the app's poll of
	// lan-status.json) has dst open: retry briefly rather than lose the write.
	for i := 0; err != nil && runtime.GOOS == "windows" && i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		err = os.Rename(src, dst)
	}
	return err
}
