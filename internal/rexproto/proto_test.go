package rexproto

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedFilesUpToDate(t *testing.T) {
	files, err := GenerateAll()
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	root := filepath.Join("..", "..")
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v (run go generate ./internal/rexproto)", rel, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s is out of date with protocol.json; run go generate ./internal/rexproto", rel)
		}
	}
}
