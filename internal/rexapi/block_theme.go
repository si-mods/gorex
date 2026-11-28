package rexapi

import (
	"strconv"
	"strings"

	"gorex/internal/layout"
	"gorex/internal/rex"
)

// theme is a block's default colors, as set_theme set them.
type theme struct {
	Foreground string   `json:"foreground,omitempty"`
	Background string   `json:"background,omitempty"`
	Cursor     string   `json:"cursor,omitempty"`
	Palette    []string `json:"palette,omitempty"`
	Scheme     string   `json:"scheme,omitempty"`
}

// blockSetTheme replaces the terminal's default colors, for the stream of
// the blocks that show it.
func blockSetTheme(c *control, s *mSession, b *mBlock, a args) (any, *apiError) {
	t, aerr := parseTheme(a)
	if aerr != nil {
		return nil, aerr
	}
	c.g.setTheme(b.id, t)
	return struct{}{}, nil
}

// parseTheme reads a theme as set_theme and a shell's theme option give
// it.
func parseTheme(a args) (theme, *apiError) {
	var t theme
	for _, name := range []string{"foreground", "background", "cursor"} {
		v, _, aerr := a.str(name)
		if aerr != nil {
			return t, aerr
		}
		if v != "" {
			var cerr *apiError
			v, cerr = normalizeThemeColor(name, v)
			if cerr != nil {
				return t, cerr
			}
		}
		switch name {
		case "foreground":
			t.Foreground = v
		case "background":
			t.Background = v
		case "cursor":
			t.Cursor = v
		}
	}
	palette, present, aerr := a.strs("palette")
	if aerr != nil {
		return t, aerr
	}
	if present && palette != nil {
		normPalette := make([]string, len(palette))
		for i, p := range palette {
			np, cerr := normalizePalette(p)
			if cerr != nil {
				return t, cerr
			}
			normPalette[i] = np
		}
		t.Palette = normPalette
	}
	scheme, _, aerr := a.str("scheme")
	if aerr != nil {
		return t, aerr
	}
	if scheme != "" && scheme != "light" && scheme != "dark" {
		return t, errBadRequest("scheme is light or dark")
	}
	t.Scheme = scheme
	return t, nil
}

// normalizeThemeColor checks a color of set_theme (RGB, #RGB, RRGGBB or
// #RRGGBB), expanding 3-digit shorthand to 6 digits while preserving existing
// 6-digit strings.
func normalizeThemeColor(name, v string) (string, *apiError) {
	hex := strings.TrimPrefix(v, "#")
	if len(hex) != 3 && len(hex) != 6 {
		return "", errBadRequest("%s is a color, RRGGBB or #RRGGBB", name)
	}
	norm, ok := layout.NormalizeHex6(hex)
	if !ok {
		return "", errBadRequest("%s is a color, RRGGBB or #RRGGBB", name)
	}
	if len(hex) == 6 {
		return v, nil
	}
	if strings.HasPrefix(v, "#") {
		return "#" + norm, nil
	}
	return norm, nil
}

// normalizePalette checks and normalizes a palette change: index=RRGGBB.
func normalizePalette(v string) (string, *apiError) {
	i := strings.IndexByte(v, '=')
	if i <= 0 {
		return "", errBadRequest("a palette change is index=RRGGBB, not %q", v)
	}
	n, err := strconv.Atoi(v[:i])
	if err != nil || n < 0 || n > 255 {
		return "", errBadRequest("a palette index is 0 to 255, not %q", v[:i])
	}
	col, aerr := normalizeThemeColor("palette", v[i+1:])
	if aerr != nil {
		return "", aerr
	}
	return v[:i+1] + col, nil
}

// setTheme stores a block's colors and tells the streams that show it.
func (g *Gateway) setTheme(block string, t theme) {
	g.mu.Lock()
	g.themes[block] = t
	streams := make([]*stream, 0, len(g.streams))
	for _, s := range g.streams {
		if s.block == block {
			streams = append(streams, s)
		}
	}
	g.mu.Unlock()
	for _, s := range streams {
		s.inject(t.message())
	}
}

// message is the theme message of a data stream.
func (t theme) message() map[string]any {
	m := map[string]any{"type": "theme"}
	set := func(k, v string) {
		if v != "" {
			m[k] = v
		}
	}
	set("foreground", t.Foreground)
	set("background", t.Background)
	set("cursor", t.Cursor)
	set("scheme", t.Scheme)
	if len(t.Palette) > 0 {
		m["palette"] = t.Palette
	}
	return m
}

// themeOf returns a block's default colors.
func (g *Gateway) themeOf(block string) (theme, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	t, ok := g.themes[block]
	return t, ok
}

// setHostTheme pushes the host's active terminal colors to every stream
// that has no block-level theme override.
func (g *Gateway) setHostTheme(t *rex.Theme) {
	if t == nil {
		return
	}
	th := theme{
		Foreground: t.Foreground,
		Background: t.Background,
		Cursor:     t.Cursor,
		Palette:    t.Palette,
	}
	msg := th.message()
	g.mu.Lock()
	streams := make([]*stream, 0, len(g.streams))
	for _, s := range g.streams {
		if _, overridden := g.themes[s.block]; !overridden {
			streams = append(streams, s)
		}
	}
	g.mu.Unlock()
	for _, s := range streams {
		s.inject(msg)
	}
}
