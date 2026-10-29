package main

import (
	"os"
	"path/filepath"
	"slices"
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
	l, _, opaque = parseGhostty(writeConf(t, dir, "c4", "theme = "+mine+"\nbackground = #123456\ncursor-color = abc\nselection-background = #000000\nsearch-background = #010101\nsearch-foreground = #020202\nsearch-selected-background = #030303\nsearch-selected-foreground = #040404\npalette = 0=#010203\n")).themes()
	if !opaque || l.Background != ui.Hex("#123456") || l.Foreground != ui.Hex("#eeeeee") ||
		l.Cursor != ui.Hex("#aabbcc") || l.Selection != ui.Hex("#000000") ||
		l.Search != ui.Hex("#010101") || l.SearchText != ui.Hex("#020202") ||
		l.SearchSelected != ui.Hex("#030303") || l.SearchSelectedText != ui.Hex("#040404") || l.Palette[0] != ui.Hex("#010203") {
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

func TestGhosttyInputOptions(t *testing.T) {
	for val, want := range map[string]terminal.CopyOnSelect{
		"clipboard": terminal.CopyOnSelectClipboard, "Clipboard": terminal.CopyOnSelectClipboard,
		"true": terminal.CopyOnSelectNone, "false": terminal.CopyOnSelectNone, "": terminal.CopyOnSelectNone,
	} {
		if c := parseGhostty(writeConf(t, t.TempDir(), "c", "copy-on-select = "+val+"\n")); c.CopyOnSelect != want {
			t.Errorf("copy-on-select %q = %v", val, c.CopyOnSelect)
		}
	}
	for val, want := range map[string]terminal.RightClickAction{
		"context-menu": terminal.RightClickContextMenu, "paste": terminal.RightClickPaste,
		"copy": terminal.RightClickCopy, "copy-or-paste": terminal.RightClickCopyOrPaste,
		"ignore": terminal.RightClickIgnore, "bogus": terminal.RightClickPaste,
	} {
		// An unknown value keeps the earlier one.
		c := parseGhostty(writeConf(t, t.TempDir(), "c", "right-click-action = paste\nright-click-action = "+val+"\n"))
		if c.RightClick != want {
			t.Errorf("right-click-action %q = %v", val, c.RightClick)
		}
	}
	for val, want := range map[string]float32{"4.5": 4.5, "1": 1, "21": 21, "30": 21, "0.5": 1, "abc": 7, "NaN": 7, "": 7} {
		c := parseGhostty(writeConf(t, t.TempDir(), "c", "minimum-contrast = 7\nminimum-contrast = "+val+"\n"))
		if c.MinimumContrast != want {
			t.Errorf("minimum-contrast %q = %v, want %v", val, c.MinimumContrast, want)
		}
	}
}

func TestGhosttyKeybind(t *testing.T) {
	parse := func(lines ...string) *ghosttyConfig {
		text := ""
		for _, l := range lines {
			text += "keybind = " + l + "\n"
		}
		return parseGhostty(writeConf(t, t.TempDir(), "c", text))
	}
	k := func(mods ui.Modifiers, key ui.Key, a terminal.Action) terminal.Keybind {
		return terminal.Keybind{Mods: mods, Key: key, Action: a}
	}

	c := parse(
		"ctrl+shift+c=copy_to_clipboard",
		"Cmd+Option+Control+Shift+F12=ignore",
		"performable:ctrl+c=copy_to_clipboard",
		"alt+arrow_left=unbind",
		"ctrl+z=increase_font_size:2",
		"ctrl+plus=increase_font_size:1",
		"super+grave_accent=scroll_to_top",
		"ctrl+9=scroll_to_bottom",
	)
	want := []terminal.Keybind{
		k(ui.Ctrl|ui.Shift, ui.KeyC, terminal.ActionCopy),
		k(ui.Super|ui.Alt|ui.Ctrl|ui.Shift, ui.KeyF12, terminal.ActionIgnore),
		{Mods: ui.Ctrl, Key: ui.KeyC, Action: terminal.ActionCopy, Performable: true},
		k(ui.Alt, ui.KeyLeft, terminal.ActionNone),
		k(ui.Ctrl, ui.KeyZ, terminal.ActionIncreaseFontSize),
		k(ui.Ctrl|ui.Shift, ui.KeyEqual, terminal.ActionIncreaseFontSize),
		k(ui.Super, ui.KeyBackquote, terminal.ActionScrollToTop),
		k(ui.Ctrl, ui.Key9, terminal.ActionScrollToBottom),
	}
	if !slices.Equal(c.Keybinds, want) || c.NoDefaultKeybinds {
		t.Errorf("keybinds = %+v", c.Keybinds)
	}

	// Lines GoRex cannot do are skipped.
	c = parse(
		"global:cmd+grave_accent=toggle_quick_terminal",
		"all:ctrl+a=copy_to_clipboard",
		"unconsumed:ctrl+b=copy_to_clipboard",
		"ctrl+a>c=copy_to_clipboard",
		"ctrl+n=new_window",
		"ctrl+kana=copy_to_clipboard",
		"ctrl+x+y=copy_to_clipboard",
		"ctrl+=copy_to_clipboard",
		"ctrl+c",
		"=copy_to_clipboard",
	)
	if len(c.Keybinds) != 0 {
		t.Errorf("skipped lines gave %+v", c.Keybinds)
	}

	// The last binding of a trigger wins, and clear drops the earlier ones.
	c = parse("ctrl+a=select_all", "ctrl+b=paste_from_clipboard", "CTRL+A=unbind")
	if want := []terminal.Keybind{k(ui.Ctrl, ui.KeyB, terminal.ActionPaste), k(ui.Ctrl, ui.KeyA, terminal.ActionNone)}; !slices.Equal(c.Keybinds, want) {
		t.Errorf("duplicates: %+v", c.Keybinds)
	}
	c = parse("ctrl+a=select_all", "clear", "ctrl+b=select_all")
	if !c.NoDefaultKeybinds || len(c.Keybinds) != 1 || c.Keybinds[0].Key != ui.KeyB {
		t.Errorf("clear: %+v", c)
	}
}

func TestGhosttyTermOptions(t *testing.T) {
	old := ghostty
	defer func() { ghostty = old }()
	ghostty = *parseGhostty(writeConf(t, t.TempDir(), "c",
		"copy-on-select = clipboard\nright-click-action = copy-or-paste\nminimum-contrast = 3\nkeybind = clear\nkeybind = ctrl+a=select_all\n"+
			"clipboard-read = allow\nclipboard-write = deny\nwindow-padding-x = 10\nwindow-padding-y = 5\nwindow-padding-color = extend-always\nimage-storage-limit = 1048576\n"))
	o := termOptions()
	if o.CopyOnSelect != terminal.CopyOnSelectClipboard || o.RightClick != terminal.RightClickCopyOrPaste ||
		o.MinimumContrast != 3 || !o.NoDefaultKeybinds || len(o.Keybinds) != 1 ||
		!o.NoClipboardWrite || o.ClipboardRead != terminal.ClipboardAllow ||
		o.Padding.X != 10 || o.Padding.Y != 5 || o.PaddingColor != terminal.PaddingExtendAlways || o.ImageLimit != 1048576 {
		t.Errorf("options = %+v", o)
	}
	// A reload starts from a fresh configuration: nothing is kept.
	ghostty = *parseGhostty(writeConf(t, t.TempDir(), "c2", "font-size = 12\n"))
	o = termOptions()
	if o.CopyOnSelect != 0 || o.RightClick != 0 || o.MinimumContrast != 0 || o.NoDefaultKeybinds || o.Keybinds != nil ||
		o.NoClipboardWrite || o.ClipboardRead != terminal.ClipboardAsk || o.Padding != (terminal.Padding{}) ||
		o.PaddingColor != terminal.PaddingBackground || o.ImageLimit != 0 {
		t.Errorf("options leaked: %+v", o)
	}
	// An explicit zero disables images; Ghostty's default asks for reads.
	ghostty = *parseGhostty(writeConf(t, t.TempDir(), "c3", "image-storage-limit = 0\n"))
	if o = termOptions(); o.ImageLimit >= 0 {
		t.Errorf("image limit 0: %d", o.ImageLimit)
	}
}
