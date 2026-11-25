package theme

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The assets key of a theme pack (docs/DSK-010-theme-ambience.md, phase
// 1): names for the files of the pack's assets folder, which the theme's
// shaders read (docs/DSK-012-theme-shaders.md). GoRex draws no look of its
// own from a pack (docs/DSK-008-theme.md, Non-goals): Parse drops the
// ambience and brand keys of DSK-010, as it drops mascot and ticker, with
// an unknown key warning.

// Asset is a file of the pack's assets folder that a manifest names.
type Asset struct {
	File string
}

// maxAssetNames is the most names the assets key keeps: 24 by DSK-012, 16
// in DSK-010.
const maxAssetNames = 24

// validAssetName reports whether s may name an asset: letters, digits, '-'
// and '_'.
func validAssetName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// validAssetFile reports whether s may name a file of the assets folder:
// a theme name (ValidName), so no path separator, drive or "..".
func validAssetFile(s string) bool {
	return ValidName(s) && !strings.HasPrefix(s, ".")
}

// isBidiControl reports whether r is a bidirectional mark, embedding,
// override or isolate, which could make text read otherwise than it is.
func isBidiControl(r rune) bool {
	return r == 0x061c || r == 0x200e || r == 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069
}

// cleanText removes control characters and bidirectional controls from s
// and keeps its first max characters. changed reports whether it did any.
func cleanText(s string, max int) (out string, changed bool) {
	if !utf8.ValidString(s) {
		s, changed = strings.ToValidUTF8(s, ""), true
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) || isBidiControl(r) {
			changed = true
			continue
		}
		if n == max {
			changed = true
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String(), changed
}

// warner collects Parse's warnings.
type warner []string

func (w *warner) add(format string, a ...any) { *w = append(*w, fmt.Sprintf(format, a...)) }

// fields reads raw as an object, and warns for each key not in known.
func (w *warner) fields(raw json.RawMessage, where string, known ...string) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		w.add("%s: not an object", where)
		return nil, false
	}
	for k := range m {
		if !slices.Contains(known, k) {
			w.add("%s: unknown key %q", where, k)
		}
	}
	return m, true
}

// num reads the number at key of m into lo–hi: def when it is missing, a
// clamped value with a warning when it is out of range. ok is false, with
// a warning, when the value is not a number.
func (w *warner) num(m map[string]json.RawMessage, key, where string, lo, hi, def float32) (float32, bool) {
	raw, ok := m[key]
	if !ok {
		return def, true
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil {
		w.add("%s.%s: not a number", where, key)
		return 0, false
	}
	v := float32(f)
	if v < lo || v > hi {
		w.add("%s.%s: %g clamped to %g–%g", where, key, f, lo, hi)
		v = min(max(v, lo), hi)
	}
	return v, true
}

// parseAssets reads the assets key: names to files of the assets folder.
// A sprite sheet object of DSK-010 is no file name, and drops.
func parseAssets(raw json.RawMessage, w *warner) map[string]Asset {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		w.add("assets: not an object")
		return nil
	}
	out := map[string]Asset{}
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}) {
		where := "assets." + name
		if !validAssetName(name) {
			w.add("assets: bad name %q", name)
			continue
		}
		if len(out) == maxAssetNames {
			w.add("%s: more than %d assets", where, maxAssetNames)
			continue
		}
		var a Asset
		if json.Unmarshal(m[name], &a.File) != nil {
			w.add("%s: not a file name", where)
			continue
		}
		if !validAssetFile(a.File) {
			w.add("%s: bad file name %q", where, a.File)
			continue
		}
		out[name] = a
	}
	return out
}
