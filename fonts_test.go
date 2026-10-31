package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestFontKey checks the names a family and its files are matched by.
func TestFontKey(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Maple Mono NF CN", "maplemononfcn"},
		{"MapleMono-NF-CN-Regular.ttf", "maplemononfcnregularttf"},
		{"IBM Plex Mono", "ibmplexmono"},
		{"SF Mono", "sfmono"},
		{"  ", ""},
	} {
		if got := fontKey(c.in); got != c.want {
			t.Errorf("fontKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFamilyFaces checks that a family's faces are found by the name table
// of its files, once each: a family's name in a file's own name does not
// make it the family's, and a file named after the family that the table
// gives another family is not taken.
func TestFamilyFaces(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	writeTestFont(t, first, "JetBrainsMono-Bold.ttf", fontBold)
	writeTestFont(t, first, "JetBrainsMono-Regular.ttf", fontRegular)
	writeTestFont(t, second, "JetBrainsMono-Regular.ttf", fontRegular)
	writeTestFont(t, second, "MapleMono-NF-CN-Regular.ttf", fontItalic)

	faces := familyFaces([]string{first, second}, "JetBrains Mono")
	if len(faces) != 2 {
		t.Fatalf("%d faces, want 2", len(faces))
	}
	var regular, bold bool
	for _, f := range faces {
		regular = regular || bytes.Equal(f, fontRegular)
		bold = bold || bytes.Equal(f, fontBold)
	}
	if !regular || !bold {
		t.Error("the faces are not the regular and the bold")
	}

	if faces := familyFaces([]string{first, second}, "Maple Mono NF CN"); len(faces) != 0 {
		t.Errorf("%d faces under Maple Mono NF CN, a name no file's table gives", len(faces))
	}
	if faces := familyFaces([]string{first, second}, "JetBrains"); len(faces) != 0 {
		t.Errorf("%d faces under JetBrains, a name no file's table gives", len(faces))
	}
	if faces := familyFaces([]string{filepath.Join(first, "none")}, "JetBrains Mono"); len(faces) != 0 {
		t.Errorf("%d faces in a missing directory", len(faces))
	}
}

// writeTestFont writes data as a font file in dir.
func writeTestFont(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
