package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotateLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	os.WriteFile(path, []byte("12345"), 0o600)
	rotateLog(path, 5)
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Fatal("a log at the limit was rotated")
	}
	rotateLog(path, 4)
	if _, err := os.Stat(path); err == nil {
		t.Error("a log over the limit stayed")
	}
	if b, _ := os.ReadFile(path + ".1"); string(b) != "12345" {
		t.Errorf("app.log.1 = %q, want the old log", b)
	}
}
