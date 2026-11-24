package theme

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

// The keys of docs/DSK-010-theme-ambience.md that this build reads:
// assets, the layers of ambience (glows, images and sprites) and the brand
// row. The mascot and the ticker come in a later phase; until then Parse
// drops them with a warning, as an older build does. GoRex has no
// particle look of its own: particles come from a theme's shader
// (docs/DSK-012-theme-shaders.md), and Parse drops ambience.particles.

// Asset is a file of the pack's assets folder that a manifest names: an
// image, or a sprite sheet when Frames is above 0.
type Asset struct {
	File    string
	Frames  int     // frames of a sprite sheet, 0 for an image
	Columns int     // columns of the sheet's grid
	FPS     float32 // frames a second of the sheet
}

// Anchor is the corner, or the center, of the window a still image keeps to.
type Anchor int

// The anchors.
const (
	BottomRight Anchor = iota
	BottomLeft
	TopRight
	TopLeft
	Center
)

var anchorNames = []string{"bottomRight", "bottomLeft", "topRight", "topLeft", "center"}

// Glow is a soft ellipse of color in the window's gradient. X and Y place
// its center, RX and RY its radii, in fractions of the window's width and
// height. Its color is light at index 0, dark at index 1.
type Glow struct {
	X, Y, RX, RY float32
	Color        [2]ui.Color
}

// Still is a still image of the window's background, as a watermark.
// Height is a fraction of the window's height up to 1, else DIPs.
type Still struct {
	Asset   string
	Anchor  Anchor
	Height  float32
	Opacity float32
}

// Direction is where a sprite swims.
type Direction int

// The directions.
const (
	Right Direction = iota
	Left
	Up
)

var directionNames = []string{"right", "left", "up"}

// Sprite is an image that swims across the window in Seconds a trip, and
// again: left or right at Y, a fraction of the window's height, or up at
// X, a fraction of its width. Height is in DIPs. One that swims left is
// mirrored, unless Mirror is false.
type Sprite struct {
	Asset   string
	X, Y    float32
	Height  float32
	Seconds float32
	Dir     Direction
	Opacity float32
	Mirror  bool
}

// Ambience is what a theme draws on the window's background.
type Ambience struct {
	Glows   []Glow
	Images  []Still
	Sprites []Sprite
}

// Brand is the row at the top of the sidebar: an icon asset and a wordmark.
type Brand struct {
	Icon     string
	Wordmark string
}

// The caps of DSK-010.
const (
	maxAssetNames    = 24 // DSK-012; 16 in DSK-010
	maxGlows         = 4
	maxGlowAlpha     = 0.4
	maxGlowAlphaByte = 102 // maxGlowAlpha of 255
	maxImages        = 2
	maxOpacity       = 0.25
	maxSheetFrame    = 64
	maxWordmark      = 24
	maxStillDIP      = 1024 // the largest height in DIPs a still image takes

	maxSprites       = 8
	maxSpriteOpacity = 0.3
)

// Empty reports whether the theme draws nothing on the background.
func (a Ambience) Empty() bool { return len(a.Glows) == 0 && len(a.Images) == 0 && !a.Moving() }

// Moving reports whether the theme has layers that move: sprites.
func (a Ambience) Moving() bool { return len(a.Sprites) > 0 }

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

// parseAssets reads the assets key: names to files of the assets folder,
// or to sprite sheets.
func parseAssets(raw json.RawMessage, w *warner) map[string]Asset {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		w.add("assets: not an object")
		return nil
	}
	names := slices.Sorted(func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	})
	out := map[string]Asset{}
	for _, name := range names {
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
			f, ok := w.fields(m[name], where, "file", "frames", "columns", "fps")
			if !ok {
				continue
			}
			if json.Unmarshal(f["file"], &a.File) != nil {
				w.add("%s.file: not a string", where)
				continue
			}
			frames, ok1 := w.num(f, "frames", where, 1, maxSheetFrame, 1)
			cols, ok2 := w.num(f, "columns", where, 1, maxSheetFrame, frames)
			fps, ok3 := w.num(f, "fps", where, 1, 30, 10)
			if !ok1 || !ok2 || !ok3 {
				continue
			}
			a.Frames, a.Columns, a.FPS = int(frames), int(cols), fps
		}
		if !validAssetFile(a.File) {
			w.add("%s: bad file name %q", where, a.File)
			continue
		}
		out[name] = a
	}
	return out
}

// parseAmbience reads the ambience key's still layers. An image whose asset
// the manifest does not name is dropped.
func parseAmbience(raw json.RawMessage, assets map[string]Asset, w *warner) Ambience {
	var amb Ambience
	m, ok := w.fields(raw, "ambience", "glows", "images", "particles", "sprites")
	if !ok {
		return amb
	}
	list := func(key string) []json.RawMessage {
		var l []json.RawMessage
		if raw, ok := m[key]; ok && json.Unmarshal(raw, &l) != nil {
			w.add("ambience.%s: not a list", key)
		}
		return l
	}
	glows, images := list("glows"), list("images")
	for i, g := range glows {
		where := fmt.Sprintf("ambience.glows[%d]", i)
		if len(amb.Glows) == maxGlows {
			w.add("%s: more than %d glows", where, maxGlows)
			break
		}
		f, ok := w.fields(g, where, "x", "y", "rx", "ry", "color")
		if !ok {
			continue
		}
		var gl Glow
		var oks [4]bool
		gl.X, oks[0] = w.num(f, "x", where, -0.5, 1.5, 0.5)
		gl.Y, oks[1] = w.num(f, "y", where, -0.5, 1.5, 0.5)
		gl.RX, oks[2] = w.num(f, "rx", where, 0.05, 1.5, 0.5)
		gl.RY, oks[3] = w.num(f, "ry", where, 0.05, 1.5, 0.5)
		if oks != [4]bool{true, true, true, true} {
			continue
		}
		light, lok := ColorFor(f["color"], false)
		dark, dok := ColorFor(f["color"], true)
		if !lok || !dok {
			w.add("%s.color: bad color", where)
			continue
		}
		gl.Color = [2]ui.Color{light, dark}
		if max(light.A, dark.A) > maxGlowAlphaByte {
			w.add("%s.color: alpha clamped to %g", where, maxGlowAlpha)
			gl.Color[0].A, gl.Color[1].A = min(light.A, maxGlowAlphaByte), min(dark.A, maxGlowAlphaByte)
		}
		amb.Glows = append(amb.Glows, gl)
	}
	for i, im := range images {
		where := fmt.Sprintf("ambience.images[%d]", i)
		if len(amb.Images) == maxImages {
			w.add("%s: more than %d images", where, maxImages)
			break
		}
		f, ok := w.fields(im, where, "asset", "anchor", "height", "opacity")
		if !ok {
			continue
		}
		var s Still
		if json.Unmarshal(f["asset"], &s.Asset) != nil {
			w.add("%s.asset: not a string", where)
			continue
		}
		if a, ok := assets[s.Asset]; !ok || a.Frames > 0 {
			w.add("%s.asset: no image asset %q", where, s.Asset)
			continue
		}
		if raw, ok := f["anchor"]; ok {
			var name string
			i := -1
			if json.Unmarshal(raw, &name) == nil {
				i = slices.Index(anchorNames, name)
			}
			if i < 0 {
				w.add("%s.anchor: unknown anchor", where)
				continue
			}
			s.Anchor = Anchor(i)
		}
		var ok1, ok2 bool
		s.Height, ok1 = w.num(f, "height", where, 0.05, maxStillDIP, 0.5)
		s.Opacity, ok2 = w.num(f, "opacity", where, 0, maxOpacity, 0.1)
		if !ok1 || !ok2 {
			continue
		}
		amb.Images = append(amb.Images, s)
	}
	if _, ok := m["particles"]; ok {
		w.add("ambience.particles: not drawn by this build")
	}
	amb.Sprites = parseSprites(list("sprites"), assets, w)
	return amb
}

// parseSprites reads ambience.sprites: at most eight images, or sprite
// sheets, that swim across the window. A sprite whose asset the manifest
// does not name is dropped.
func parseSprites(list []json.RawMessage, assets map[string]Asset, w *warner) []Sprite {
	var out []Sprite
	for i, raw := range list {
		where := fmt.Sprintf("ambience.sprites[%d]", i)
		if len(out) == maxSprites {
			w.add("%s: more than %d sprites", where, maxSprites)
			break
		}
		f, ok := w.fields(raw, where, "asset", "x", "y", "height", "seconds", "direction", "opacity", "mirror")
		if !ok {
			continue
		}
		s := Sprite{Mirror: true}
		if json.Unmarshal(f["asset"], &s.Asset) != nil {
			w.add("%s.asset: not a string", where)
			continue
		}
		if _, ok := assets[s.Asset]; !ok {
			w.add("%s.asset: no asset %q", where, s.Asset)
			continue
		}
		if raw, ok := f["direction"]; ok {
			var name string
			d := -1
			if json.Unmarshal(raw, &name) == nil {
				d = slices.Index(directionNames, name)
			}
			if d < 0 {
				w.add("%s.direction: unknown direction", where)
				continue
			}
			s.Dir = Direction(d)
		}
		if raw, ok := f["mirror"]; ok && json.Unmarshal(raw, &s.Mirror) != nil {
			w.add("%s.mirror: not true or false", where)
			continue
		}
		var oks [5]bool
		s.X, oks[0] = w.num(f, "x", where, 0, 1, 0.5)
		s.Y, oks[1] = w.num(f, "y", where, 0, 1, 0.5)
		s.Height, oks[2] = w.num(f, "height", where, 16, 200, 48)
		s.Seconds, oks[3] = w.num(f, "seconds", where, 8, 600, 60)
		s.Opacity, oks[4] = w.num(f, "opacity", where, 0, maxSpriteOpacity, 0.15)
		if oks != [5]bool{true, true, true, true, true} {
			continue
		}
		out = append(out, s)
	}
	return out
}

// parseBrand reads the brand key: an icon asset and a wordmark.
func parseBrand(raw json.RawMessage, assets map[string]Asset, w *warner) Brand {
	var b Brand
	m, ok := w.fields(raw, "brand", "icon", "wordmark")
	if !ok {
		return b
	}
	if raw, ok := m["icon"]; ok {
		var name string
		if json.Unmarshal(raw, &name) != nil {
			w.add("brand.icon: not a string")
		} else if a, ok := assets[name]; !ok || a.Frames > 0 {
			w.add("brand.icon: no image asset %q", name)
		} else {
			b.Icon = name
		}
	}
	if raw, ok := m["wordmark"]; ok {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			w.add("brand.wordmark: not a string")
		} else {
			var changed bool
			if b.Wordmark, changed = cleanText(s, maxWordmark); changed {
				w.add("brand.wordmark: control characters removed or cut to %d characters", maxWordmark)
			}
		}
	}
	return b
}
