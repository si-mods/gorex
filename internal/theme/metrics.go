package theme

import (
	"encoding/json"
	"strings"
)

// Metrics holds the metrics key of docs/DSK-012-theme-shaders.md: the sizes
// a theme sets. Parse fills each key the manifest leaves out with the
// built-in value, so that a theme with a metrics key draws as the app does
// where it says nothing.
type Metrics struct {
	// Radius scales each corner radius of the chrome: 0 square, 1 the
	// built-in corners.
	Radius float32
	// Border is the width of the border of a pane card, a panel and a
	// field, in DIPs: 0, 1 or 2.
	Border float32
	// Font is the family list of the chrome's text, first choice first: up
	// to MaxFonts names, among them the words "system", "rounded" and
	// "mono". The app takes the first one installed; nil keeps the
	// built-in family.
	Font []string
	// Shadow scales and hardens the shadows of raised layers.
	Shadow ShadowMetric
}

// ShadowMetric is the shadow key of Metrics.
type ShadowMetric struct {
	// Scale multiplies a shadow's offset and blur: 0 to 2.
	Scale float32
	// Hard sets the blur to 0, so that a shadow is an offset copy, as paper
	// on paper.
	Hard bool
}

// DefaultMetrics are the built-in sizes: what a missing metrics key, or a
// missing key in it, means.
var DefaultMetrics = Metrics{Radius: 1, Border: 1, Shadow: ShadowMetric{Scale: 1}}

// MaxFonts is the most family names metrics.font takes.
const MaxFonts = 4

// maxFontName is the longest family name metrics.font takes.
const maxFontName = 64

// parseMetrics reads the metrics key. A value out of range clamps, a value
// of the wrong kind drops to the default, and an unknown key drops, each
// with a warning.
func parseMetrics(raw json.RawMessage, w *warner) *Metrics {
	m, ok := w.fields(raw, "metrics", "radius", "border", "font", "shadow")
	if !ok {
		return nil
	}
	out := DefaultMetrics
	if v, ok := w.num(m, "radius", "metrics", 0, 1, DefaultMetrics.Radius); ok {
		out.Radius = v
	}
	if v, ok := w.num(m, "border", "metrics", 0, 2, DefaultMetrics.Border); ok {
		if r := float32(int(v + 0.5)); r != v {
			w.add("metrics.border: %g rounded to %g", v, r)
			v = r
		}
		out.Border = v
	}
	if raw, ok := m["font"]; ok {
		out.Font = parseFonts(raw, w)
	}
	if raw, ok := m["shadow"]; ok {
		out.Shadow = parseShadow(raw, w)
	}
	return &out
}

// parseFonts reads metrics.font: a list of family names. A name that is
// empty, too long, or has a comma, a quote or a control character drops;
// past MaxFonts names the rest drop.
func parseFonts(raw json.RawMessage, w *warner) []string {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		w.add("metrics.font: not a list")
		return nil
	}
	var out []string
	for i, v := range list {
		if len(out) == MaxFonts {
			w.add("metrics.font: more than %d names, the rest dropped", MaxFonts)
			break
		}
		var name string
		if json.Unmarshal(v, &name) != nil || !validFamily(name) {
			w.add("metrics.font[%d]: bad family name", i)
			continue
		}
		out = append(out, strings.TrimSpace(name))
	}
	return out
}

// validFamily reports whether name may name a font family: not empty, not
// too long, and with no comma, which would make it a list, no quote and no
// control character.
func validFamily(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxFontName {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`,;"'\`, r) {
			return false
		}
	}
	return true
}

// parseShadow reads metrics.shadow.
func parseShadow(raw json.RawMessage, w *warner) ShadowMetric {
	out := DefaultMetrics.Shadow
	m, ok := w.fields(raw, "metrics.shadow", "scale", "hard")
	if !ok {
		return out
	}
	if v, ok := w.num(m, "scale", "metrics.shadow", 0, 2, out.Scale); ok {
		out.Scale = v
	}
	if raw, ok := m["hard"]; ok {
		if json.Unmarshal(raw, &out.Hard) != nil {
			w.add("metrics.shadow.hard: not true or false")
		}
	}
	return out
}

// parseGlass reads the glass key: true or false, or nil with a warning.
func parseGlass(raw json.RawMessage, w *warner) *bool {
	var on bool
	if json.Unmarshal(raw, &on) != nil {
		w.add("glass: not true or false")
		return nil
	}
	return &on
}
