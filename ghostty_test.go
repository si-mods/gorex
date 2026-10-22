package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

func writeConf(t *testing.T, dir, name, text string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGhosttyParse(t *testing.T) {
	p := writeConf(t, t.TempDir(), "config", `
# a comment
font-family = "Fira Code"
font-family = Noto Sans Mono
font-size = 14
font-size = 15
font-feature = -calt
font-feature = ss01
font-thicken = true
this line is malformed
unknown-key = 1
adjust-cell-height = 20%
cursor-style = bar
cursor-style-blink = false
scrollback-limit = 1000
macos-option-as-alt = left
palette = 1=#ff0000
palette = 2=00ff00
palette = 20=#ffffff
palette = 3=rgb(1,2,3)
`)
	c := parseGhostty(p)
	if len(c.Families) != 2 || c.Families[0] != "Fira Code" || c.Families[1] != "Noto Sans Mono" {
		t.Errorf("families = %q", c.Families)
	}
	if c.Size != 15 || !c.Thicken || len(c.Features) != 2 || c.LineHeight != 1.2 {
		t.Errorf("config = %+v", c)
	}
	if c.Cursor != terminal.CursorBar || !c.NoBlink || c.Scrollback != 1000 || !c.OptionAsAlt {
		t.Errorf("config = %+v", c)
	}
	if len(c.Palette) != 2 || c.Palette[1] != ui.Hex("#ff0000") || c.Palette[2] != ui.Hex("#00ff00") {
		t.Errorf("palette = %v", c.Palette)
	}
	f := c.font(12)
	if f.Family != "Fira Code, Noto Sans Mono, "+defaultFontFamily || f.LineHeight != 1.2 {
		t.Errorf("font = %+v", f)
	}

	r := parseGhostty(writeConf(t, t.TempDir(), "c", "font-family = A\nfont-family =\nfont-family = B\n"))
	if len(r.Families) != 1 || r.Families[0] != "B" {
		t.Errorf("reset families = %q", r.Families)
	}
	if none := parseGhostty(filepath.Join(t.TempDir(), "missing")); none.Families != nil || none.Size != 0 {
		t.Errorf("missing file gave %+v", none)
	}
}

func TestGhosttyIncludes(t *testing.T) {
	dir := t.TempDir()
	writeConf(t, dir, "extra", "font-size = 18\nconfig-file = main\nfont-family = X\n")
	writeConf(t, dir, "opt", "background = #101010\n")
	main := writeConf(t, dir, "main", "config-file = ?missing\nconfig-file = \"extra\"\nconfig-file = ?opt\nfont-size = 12\nfont-family = M\n")
	c := parseGhostty(main)
	// Includes apply after the including file, and a cycle ends.
	if c.Size != 18 {
		t.Errorf("size = %v, want 18", c.Size)
	}
	if len(c.Families) != 2 || c.Families[0] != "M" || c.Families[1] != "X" {
		t.Errorf("families = %q", c.Families)
	}
	if c.Background == nil || *c.Background != ui.Hex("#101010") {
		t.Errorf("background = %v", c.Background)
	}
}

func TestGhosttyEnvPath(t *testing.T) {
	p := writeConf(t, t.TempDir(), "x", "font-size = 21\n")
	t.Setenv("GOREX_GHOSTTY_CONFIG", p)
	old := ghostty
	defer func() { ghostty = old }()
	loadGhostty()
	if ghostty.Size != 21 {
		t.Errorf("size = %v", ghostty.Size)
	}
}

func TestGhosttyThemes(t *testing.T) {
	dir := t.TempDir()
	writeConf(t, dir, "Mine", "background = #111111\nforeground = #eeeeee\n")
	writeConf(t, dir, "Other", "background = #fafafa\nforeground = #222222\n")
	mine, other := filepath.Join(dir, "Mine"), filepath.Join(dir, "Other")

	// No theme or colors: GoRex's own, with transparency.
	l, d, opaque := (&ghosttyConfig{}).themes()
	if opaque || *l != *lightTerm || *d != *darkTerm {
		t.Error("empty config changed the themes")
	}

	l, d, opaque = parseGhostty(writeConf(t, dir, "c1", "theme = light:"+other+",dark:"+mine+"\n")).themes()
	if !opaque || l.Background != ui.Hex("#fafafa") || d.Background != ui.Hex("#111111") {
		t.Errorf("split theme: light %v dark %v opaque %v", l.Background, d.Background, opaque)
	}
	l, d, _ = parseGhostty(writeConf(t, dir, "c2", "theme = "+mine+"\n")).themes()
	if l.Background != ui.Hex("#111111") || d.Background != ui.Hex("#111111") {
		t.Error("plain theme should set both sides")
	}
	// A failing theme keeps GoRex's own on that side.
	l, d, _ = parseGhostty(writeConf(t, dir, "c3", "theme = light:"+other+",dark:no-such-theme-x\n")).themes()
	if l.Background != ui.Hex("#fafafa") || *d != *darkTerm {
		t.Error("failed dark theme should keep darkTerm")
	}

	// Colors go over the theme, and never change lightTerm or darkTerm.
	before := *lightTerm
	l, _, opaque = parseGhostty(writeConf(t, dir, "c4", "theme = "+mine+"\nbackground = #123456\ncursor-color = abc\nselection-background = #000000\npalette = 0=#010203\n")).themes()
	if !opaque || l.Background != ui.Hex("#123456") || l.Foreground != ui.Hex("#eeeeee") ||
		l.Cursor != ui.Hex("#aabbcc") || l.Selection != ui.Hex("#000000") || l.Palette[0] != ui.Hex("#010203") {
		t.Errorf("overrides: %+v", l)
	}
	if *lightTerm != before {
		t.Error("lightTerm was changed")
	}
	// A background alone makes the terminal opaque.
	_, _, opaque = parseGhostty(writeConf(t, dir, "c5", "background = #000\n")).themes()
	if !opaque {
		t.Error("background should make terminals opaque")
	}
}

func TestGhosttyFontSize(t *testing.T) {
	g := &ghosttyConfig{}
	if g.fontSize(0) != defaultFontSize {
		t.Error("default size")
	}
	g.Size = 15
	if g.fontSize(0) != 15 || g.fontSize(20) != 20 {
		t.Error("settings.json size should win over font-size")
	}
	if l := parseGhostty(writeConf(t, t.TempDir(), "c", "adjust-cell-height = 10\n")).LineHeight; l != 0 {
		t.Errorf("pixel adjust gave %v", l)
	}
}

func TestGhosttyAppearance(t *testing.T) {
	defer func() { ghostty = ghosttyConfig{}; applyGhostty() }()
	for _, tc := range []struct{ conf, want string }{
		{"", ""},
		{"theme = Gruvbox Dark\n", "dark"},
		{"theme = Gruvbox Light\n", "light"},
		{"background = #101010\n", "dark"},
		{"theme = light:Gruvbox Light,dark:Gruvbox Dark\n", ""},
		{"theme = No Such Theme\n", ""},
	} {
		ghostty = *parseGhostty(writeConf(t, t.TempDir(), "c", tc.conf))
		applyGhostty()
		if termAppearance != tc.want {
			t.Errorf("%q: appearance %q, want %q", tc.conf, termAppearance, tc.want)
		}
	}
}
