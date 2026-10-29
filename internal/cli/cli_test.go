package cli

import (
	"bytes"
	"testing"
)

// TestWriteJSON pins the shape scripts and agents read: indented, one
// object, a newline at the end, and no HTML escaping.
func TestWriteJSON(t *testing.T) {
	var b bytes.Buffer
	if err := WriteJSON(&b, map[string]any{
		"session": "abc",
		"status":  "blocked",
		"title":   "<build> & test",
	}); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"session\": \"abc\",\n  \"status\": \"blocked\",\n  \"title\": \"<build> & test\"\n}\n"
	if b.String() != want {
		t.Errorf("JSON:\n%s\nwant:\n%s", b.String(), want)
	}
}
