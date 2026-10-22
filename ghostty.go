package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// ghosttyConfig is what GoRex takes from the user's Ghostty configuration.
type ghosttyConfig struct {
	Families    []string
	Size        float32
	Features    []string
	Thicken     bool
	LineHeight  float32
	Theme       string
	Background  *ui.Color
	Foreground  *ui.Color
	CursorColor *ui.Color
	CursorText  *ui.Color
	SelectionBg *ui.Color
	SelectionFg *ui.Color
	Palette     map[int]ui.Color
	Cursor      terminal.CursorStyle
	NoBlink     bool
	Scrollback  int
	OptionAsAlt bool
}

// ghostty is the configuration GoRex has read.
var ghostty ghosttyConfig

// ghosttyConfigPaths returns the files Ghostty reads, as it finds them, the
// later over the earlier. GOREX_GHOSTTY_CONFIG names the only one when set.
func ghosttyConfigPaths() []string {
	if p := os.Getenv("GOREX_GHOSTTY_CONFIG"); p != "" {
		return []string{p}
	}
	home, _ := os.UserHomeDir()
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" && runtime.GOOS == "windows" {
		base = os.Getenv("LOCALAPPDATA")
	}
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	dirs := []string{filepath.Join(base, "ghostty")}
	if runtime.GOOS == "darwin" {
		dirs = append(dirs, filepath.Join(home, "Library", "Application Support", "com.mitchellh.ghostty"))
	}
	var paths []string
	for _, d := range dirs {
		for _, name := range []string{"config.ghostty", "config"} {
			if st, err := os.Stat(filepath.Join(d, name)); err == nil && !st.IsDir() {
				paths = append(paths, filepath.Join(d, name))
				break
			}
		}
	}
	return paths
}

// loadGhostty reads the configuration anew.
func loadGhostty() {
	ghostty = *parseGhostty(ghosttyConfigPaths()...)
}

// parseGhostty reads configuration files in order, each over the last.
// Missing files are skipped.
func parseGhostty(paths ...string) *ghosttyConfig {
	c := &ghosttyConfig{}
	seen := map[string]bool{}
	for _, p := range paths {
		c.readFile(p, seen)
	}
	return c
}

// readFile applies a file's assignments, then the files it includes, as
// Ghostty does. A file is read once, which ends include cycles. An optional
// include (a "?" before the path) and a required one are alike here: a
// missing file is skipped.
func (c *ghosttyConfig) readFile(path string, seen map[string]bool) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if seen[path] {
		return
	}
	seen[path] = true
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var includes []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), unquote(val)
		if key != "config-file" {
			c.set(key, val)
			continue
		}
		val = unquote(strings.TrimPrefix(val, "?"))
		if rest, ok := strings.CutPrefix(val, "~/"); ok {
			home, _ := os.UserHomeDir()
			val = filepath.Join(home, rest)
		} else if val != "" && !filepath.IsAbs(val) {
			val = filepath.Join(filepath.Dir(path), val)
		}
		if val != "" {
			includes = append(includes, val)
		}
	}
	for _, p := range includes {
		c.readFile(p, seen)
	}
}

// unquote trims a value and the double quotes around it.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	return s
}

func parseBool(s string) bool {
	switch strings.ToLower(s) {
	case "true", "t", "yes", "1":
		return true
	}
	return false
}

// parseColor reads "#rgb" and "#rrggbb", with or without the #, and nil for
// other forms.
func parseColor(s string) *ui.Color {
	h := strings.TrimPrefix(s, "#")
	if len(h) != 3 && len(h) != 6 {
		return nil
	}
	if _, err := strconv.ParseUint(h, 16, 32); err != nil {
		return nil
	}
	c := ui.Hex("#" + h)
	return &c
}

// set applies one assignment, and ignores keys GoRex has no use for.
func (c *ghosttyConfig) set(key, val string) {
	switch key {
	case "font-family":
		if val == "" {
			c.Families = nil
		} else {
			c.Families = append(c.Families, val)
		}
	case "font-size":
		if f, err := strconv.ParseFloat(val, 32); err == nil && f >= 6 && f <= 40 {
			c.Size = float32(f)
		}
	case "font-feature":
		if val != "" {
			c.Features = append(c.Features, strings.Split(val, ",")...)
		}
	case "font-thicken":
		c.Thicken = parseBool(val)
	case "adjust-cell-height":
		if p, ok := strings.CutSuffix(val, "%"); ok {
			if f, err := strconv.ParseFloat(p, 32); err == nil && f > -100 {
				c.LineHeight = 1 + float32(f)/100
			}
		}
	case "theme":
		c.Theme = val
	case "background":
		c.Background = parseColor(val)
	case "foreground":
		c.Foreground = parseColor(val)
	case "cursor-color":
		c.CursorColor = parseColor(val)
	case "cursor-text":
		c.CursorText = parseColor(val)
	case "selection-background":
		c.SelectionBg = parseColor(val)
	case "selection-foreground":
		c.SelectionFg = parseColor(val)
	case "palette":
		n, col, ok := strings.Cut(val, "=")
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if pc := parseColor(strings.TrimSpace(col)); ok && err == nil && i >= 0 && i < 16 && pc != nil {
			if c.Palette == nil {
				c.Palette = map[int]ui.Color{}
			}
			c.Palette[i] = *pc
		}
	case "cursor-style":
		switch val {
		case "block", "block_hollow":
			c.Cursor = terminal.CursorBlock
		case "bar":
			c.Cursor = terminal.CursorBar
		case "underline":
			c.Cursor = terminal.CursorUnderline
		}
	case "cursor-style-blink":
		c.NoBlink = strings.ToLower(val) == "false"
	case "scrollback-limit":
		// Ghostty's 0 keeps no scrollback, which the terminal takes as negative.
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			c.Scrollback = n
		} else if err == nil && n == 0 {
			c.Scrollback = -1
		}
	case "macos-option-as-alt":
		c.OptionAsAlt = val == "left" || val == "right" || parseBool(val)
	}
}

// fontSize is the size of the terminals' text: the user's own, from
// settings.json, when there is one, else Ghostty's font-size, else GoRex's.
func (c *ghosttyConfig) fontSize(pref float32) float32 {
	switch {
	case pref > 0:
		return pref
	case c.Size > 0:
		return c.Size
	}
	return defaultFontSize
}

// font returns the terminals' font at a size, with the user's families
// before GoRex's own.
func (c *ghosttyConfig) font(size float32) terminal.Font {
	f := terminal.Font{Family: defaultFontFamily, Size: size, LineHeight: 1.0}
	if len(c.Families) > 0 {
		f.Family = strings.Join(append(slices.Clone(c.Families), f.Family), ", ")
	}
	f.Features = c.Features
	f.Thicken = c.Thicken
	if c.LineHeight > 0 {
		f.LineHeight = c.LineHeight
	}
	return f
}

// themes returns the colors of terminals in light and dark windows: GoRex's
// own, or the configured theme's, with the configured colors over them.
// Opaque tells whether the panes must draw the theme's background, as they
// must when the configuration sets a theme or a background.
func (c *ghosttyConfig) themes() (light, dark *terminal.Theme, opaque bool) {
	l, d := *lightTerm, *darkTerm
	if c.Theme != "" {
		lname, dname := c.Theme, c.Theme
		if strings.Contains(c.Theme, "light:") || strings.Contains(c.Theme, "dark:") {
			lname, dname = "", ""
			for _, part := range strings.Split(c.Theme, ",") {
				part = strings.TrimSpace(part)
				if n, ok := strings.CutPrefix(part, "light:"); ok {
					lname = unquote(n)
				} else if n, ok := strings.CutPrefix(part, "dark:"); ok {
					dname = unquote(n)
				}
			}
		}
		if lname != "" {
			if t, err := terminal.GhosttyTheme(lname); err == nil {
				l = *t
			}
		}
		if dname != "" {
			if t, err := terminal.GhosttyTheme(dname); err == nil {
				d = *t
			}
		}
		opaque = true
	}
	for _, t := range []*terminal.Theme{&l, &d} {
		if c.Background != nil {
			t.Background = *c.Background
		}
		if c.Foreground != nil {
			t.Foreground = *c.Foreground
		}
		if c.CursorColor != nil {
			t.Cursor = *c.CursorColor
		}
		if c.CursorText != nil {
			t.CursorText = *c.CursorText
		}
		if c.SelectionBg != nil {
			t.Selection = *c.SelectionBg
		}
		if c.SelectionFg != nil {
			t.SelectionText = *c.SelectionFg
		}
		for i, col := range c.Palette {
			t.Palette[i] = col
		}
	}
	return &l, &d, opaque || c.Background != nil
}

// The terminals' font and colors, from GoRex's own and the configuration.
var (
	termFont            = ghostty.font(defaultFontSize)
	termLight, termDark = lightTerm, darkTerm
	termOpaque          bool
)

// applyGhostty computes the terminals' font and colors from the
// configuration and the user's text size.
func applyGhostty() {
	termFont = ghostty.font(ghostty.fontSize(prefs.FontSize))
	termLight, termDark, termOpaque = ghostty.themes()
}

// termOptions are the options of every terminal. The cursor, scrollback,
// and option-as-alt are set only when a terminal is made, so a
// reload applies them to new panes; fonts and themes it applies to all.
func termOptions() terminal.Options {
	return terminal.Options{
		Font:        termFont,
		Theme:       termLight,
		DarkTheme:   termDark,
		Transparent: true,
		Cursor:      ghostty.Cursor,
		NoBlink:     ghostty.NoBlink,
		Scrollback:  ghostty.Scrollback,
		OptionAsAlt: ghostty.OptionAsAlt,
	}
}

// reloadGhostty reads the configuration again and applies the font and
// colors to every pane.
func (a *App) reloadGhostty() {
	loadGhostty()
	applyGhostty()
	for _, t := range a.tabs {
		for _, p := range t.panes() {
			if p.term != nil {
				p.term.SetFont(termFont)
				p.term.SetTheme(termLight, termDark)
			}
		}
	}
}
