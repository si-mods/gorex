package tailnode

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestTSOmitTagsConsistent(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "packaging", "ts-omit-tags.txt"))
	if err != nil {
		t.Fatalf("read packaging/ts-omit-tags.txt: %v", err)
	}

	tagRe := regexp.MustCompile(`^ts_omit_[a-z0-9]+$`)
	var tags []string
	seen := make(map[string]bool)
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !tagRe.MatchString(line) {
			t.Errorf("packaging/ts-omit-tags.txt:%d: invalid tag %q", i+1, line)
		}
		if seen[line] {
			t.Errorf("packaging/ts-omit-tags.txt:%d: duplicate tag %q", i+1, line)
		}
		seen[line] = true
		tags = append(tags, line)
	}
	if len(tags) == 0 {
		t.Fatal("packaging/ts-omit-tags.txt has no tags")
	}
	if !slices.IsSorted(tags) {
		t.Error("packaging/ts-omit-tags.txt tags must be sorted alphabetically")
	}

	script, err := os.ReadFile(filepath.Join(root, "ios", "scripts", "build-tailnet"))
	if err != nil {
		t.Fatalf("read ios/scripts/build-tailnet: %v", err)
	}
	if !strings.Contains(string(script), "packaging/ts-omit-tags.txt") {
		t.Error("ios/scripts/build-tailnet must read packaging/ts-omit-tags.txt rather than duplicating the omit list")
	}
}
