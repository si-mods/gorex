package main

import (
	"math"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
)

// spaceColor is a preset color of a space; Hex is its base color.
type spaceColor struct{ ID, Hex string }

// spacePalette is the order new spaces take their colors in.
var spacePalette = []spaceColor{
	{"rose", "#e54666"}, {"orange", "#f76b15"}, {"amber", "#ffb224"},
	{"green", "#30a46c"}, {"teal", "#12a594"}, {"blue", "#0090ff"},
	{"indigo", "#3e63dd"}, {"violet", "#8e4ec6"}, {"pink", "#d6409f"},
}

// spaceColorHex is the color c names: the base hex of a preset's id, or a
// custom "#rrggbb" of six hex digits, lowercased. It reports false for ""
// and for anything else.
func spaceColorHex(c string) (string, bool) {
	for _, p := range spacePalette {
		if p.ID == c {
			return p.Hex, true
		}
	}
	if len(c) != 7 || c[0] != '#' {
		return "", false
	}
	for _, r := range c[1:] {
		switch {
		case '0' <= r && r <= '9', 'a' <= r && r <= 'f', 'A' <= r && r <= 'F':
		default:
			return "", false
		}
	}
	return strings.ToLower(c), true
}

// nextSpaceColor is the color a new space takes: the first preset no space
// uses, or, every one used, the preset the fewest do, the palette's order
// settling ties.
func (st *store) nextSpaceColor() string {
	used := make(map[string]int, len(spacePalette))
	for _, w := range st.workspaces {
		used[w.Color]++
	}
	best, bestN := "", len(st.workspaces)+1
	for _, p := range spacePalette {
		n := used[p.ID]
		if n == 0 {
			return p.ID
		}
		if n < bestN {
			best, bestN = p.ID, n
		}
	}
	return best
}

// spaceColors are the colors of a window showing a space of color from
// base: base itself when color names no color, else a copy of base whose
// background gradient, tint and accent take the space's, so that a window
// tells its space apart at a glance.
func spaceColors(base *colors, color string, dark bool) *colors {
	hex, ok := spaceColorHex(color)
	if !ok {
		return base
	}
	key := spaceColorsKey{color: color, dark: dark}
	spaceColorsMu.RLock()
	k, cached := spaceColorsCache[key]
	spaceColorsMu.RUnlock()
	if cached {
		return k
	}
	h := spaceTint(ui.Hex(hex))
	k = new(colors)
	*k = *base
	if dark {
		k.bgTop = ui.Hex("#1c1c1f").Mix(h, 0.24)
		k.bgMid = ui.Hex("#1a1a1d").Mix(h, 0.14)
		k.bgLow = ui.Hex("#19191b").Mix(h, 0.09)
		k.bgBottom = ui.Hex("#1c1c1f").Mix(h, 0.16)
		k.bgTint = h.Alpha(0.12)
		k.tabActive = ui.Hex("#ffffff").Mix(h, 0.55).Alpha(0.16)
	} else {
		k.bgTop = ui.Hex("#ffffff").Mix(h, 0.20)
		k.bgMid = ui.Hex("#fbfbfb").Mix(h, 0.13)
		k.bgLow = ui.Hex("#f7f7f7").Mix(h, 0.09)
		k.bgBottom = ui.Hex("#f2f2f2").Mix(h, 0.15)
		k.bgTint = h.Alpha(0.10)
		k.tabActive = ui.Hex("#ffffff").Mix(h, 0.06).Alpha(0.97)
	}
	// accent stands out from the face of controls, step by step, so that
	// even a pale custom color is legible on them.
	to := ui.Hex("#000000")
	if dark {
		to = ui.Hex("#ffffff")
	}
	k.accent = h
	for range 20 {
		if contrast(k.accent, base.surface) >= 4.5 {
			break
		}
		k.accent = k.accent.Mix(to, 0.05)
	}
	k.accentHover = k.accent.Mix(to, 0.08)
	k.accentPressed = k.accent.Mix(to, 0.16)
	k.panelSel = k.accent.Alpha(0.14)
	if dark {
		k.panelSel = k.accent.Alpha(0.20)
	}
	spaceColorsMu.Lock()
	spaceColorsCache[key] = k
	spaceColorsMu.Unlock()
	return k
}

// spaceColorsKey names a space's colors: its color, in a window of a theme.
type spaceColorsKey struct {
	color string
	dark  bool
}

// spaceColorsCache keeps each space's colors, which a frame would else
// compute again for every window showing it.
var (
	spaceColorsMu    sync.RWMutex
	spaceColorsCache = map[spaceColorsKey]*colors{}
)

// colors are the window's: of the theme it shows in, tinted by the color
// of the space it shows.
func (a *App) colors(c *ui.Context) *colors {
	color := ""
	if a.ws != nil {
		color = a.ws.Color
	}
	return spaceColors(colorsOf(c), color, c.Theme().Dark)
}

// spaceTint is h made usable as a tint: its saturation at most 85% and
// its lightness between 35% and 65%, so that custom colors as #ffff00,
// #000000 and #ffffff still tint the window and leave its text readable.
func spaceTint(h ui.Color) ui.Color {
	hue, s, l := hslOf(h)
	return hslColor(hue, min(s, 0.85), min(max(l, 0.35), 0.65))
}

// hslOf returns the hue of c in degrees, its saturation and lightness.
func hslOf(c ui.Color) (h, s, l float64) {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	hi, lo := max(r, g, b), min(r, g, b)
	l = (hi + lo) / 2
	if hi == lo {
		return 0, 0, l
	}
	d := hi - lo
	if l > 0.5 {
		s = d / (2 - hi - lo)
	} else {
		s = d / (hi + lo)
	}
	switch hi {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h * 60, s, l
}

// hslColor returns the opaque color of hue h in degrees, saturation s and
// lightness l.
func hslColor(h, s, l float64) ui.Color {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	m := l - c/2
	channel := func(v float64) uint8 { return uint8(math.Round(min(max(v+m, 0), 1) * 255)) }
	return ui.RGB(channel(r), channel(g), channel(b))
}

// luminance is the relative luminance of c, as WCAG defines it: 0 for
// black, 1 for white, on linearized channels.
func luminance(c ui.Color) float64 {
	linear := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

// contrast is the contrast ratio of a and b, 1 for the same color and up
// to 21 for black and white: 4.5 and 7 are the levels WCAG asks of text.
func contrast(a, b ui.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}
