package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchBoundariesRepo(t *testing.T) {
	root, err := repoRoot(".")
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	var buf bytes.Buffer
	if err := archBoundariesCheck(root, &buf); err != nil {
		t.Fatalf("archBoundariesCheck failed: %v\n%s", err, buf.String())
	}
}

func TestArchBoundariesViolations(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, src string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Leaf package importing internal/rex -> forbidden
	write("internal/layout/bad.go", "package layout\nimport _ \"gorex/internal/rex\"\n")
	// Core runtime importing rexapi -> forbidden
	write("internal/rex/bad.go", "package rex\nimport _ \"gorex/internal/rexapi\"\n")
	// iOS tailnet importing internal/rex -> forbidden
	write("ios/tailnet/bad.go", "package tailnet\nimport _ \"gorex/internal/rex\"\n")
	// Non-test importing testrex -> forbidden
	write("internal/cli/bad.go", "package cli\nimport _ \"gorex/internal/testrex\"\n")
	// Test file importing testrex -> allowed
	write("internal/cli/ok_test.go", "package cli\nimport _ \"gorex/internal/testrex\"\n")

	var buf bytes.Buffer
	err := archBoundariesCheck(dir, &buf)
	if err == nil {
		t.Fatal("expected boundary violations, got nil")
	}
	out := buf.String()
	for _, want := range []string{
		"internal/layout/bad.go",
		"internal/rex/bad.go",
		"ios/tailnet/bad.go",
		"internal/cli/bad.go",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing expected violation %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ok_test.go") {
		t.Errorf("test file should not trigger violation:\n%s", out)
	}
}
