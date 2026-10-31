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
	Families      []string
	Size          float32
	Features      []string
	Thicken       bool
	LineHeight    float32
	Theme         string
	Background    *ui.Color
	Foreground    *ui.Color
	CursorColor   *ui.Color
	CursorText    *ui.Color
	SelectionBg   *ui.Color
	SelectionFg   *ui.Color
	Search        *ui.Color
	SearchText    *ui.Color
	SearchSel     *ui.Color
	SearchSelText *ui.Color
	Palette       map[int]ui.Color
	Cursor        terminal.CursorStyle
	NoBlink       bool
	Scrollback    int
	OptionAsAlt   bool

	CopyOnSelect      terminal.CopyOnSelect
	NoClipboardWrite  bool
	ClipboardRead     *terminal.ClipboardAccess
	RightClick        terminal.RightClickAction
	Padding           *terminal.Padding
	PaddingColor      *terminal.PaddingColor
	ImageLimit        *int64
	MinimumContrast   float32
	Keybinds          []terminal.Keybind
	NoDefaultKeybinds bool
	// Unknown are options GoRex has no mapping for, said by config check.
	Unknown []string
}

// ghosttyRaw is the configuration GoRex has read, and ghostty the one it
// uses: the user's choices from settings.json over it (see withPrefs).
var ghosttyRaw, ghostty ghosttyConfig

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

// ghosttyWarnings names the Ghostty options GoRex ignores.
func ghosttyWarnings() []string {
	c := parseGhostty(ghosttyConfigPaths()...)
	out := make([]string, 0, len(c.Unknown))
	for _, k := range c.Unknown {
		out = append(out, "Ghostty option "+strconv.Quote(k)+" is ignored")
	}
	return out
}

// loadGhostty reads the configuration anew.
func loadGhostty() {
	ghosttyRaw = *parseGhostty(ghosttyConfigPaths()...)
	ghostty = ghosttyRaw.withPrefs(prefs)
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
	case "search-background":
		c.Search = parseColor(val)
	case "search-foreground":
		c.SearchText = parseColor(val)
	case "search-selected-background":
		c.SearchSel = parseColor(val)
	case "search-selected-foreground":
		c.SearchSelText = parseColor(val)
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
	case "copy-on-select":
		// Ghostty's true copies to the selection clipboard of Linux, which the
		// terminal lacks, so only "clipboard" copies.
		c.CopyOnSelect = terminal.CopyOnSelectNone
		if strings.ToLower(val) == "clipboard" {
			c.CopyOnSelect = terminal.CopyOnSelectClipboard
		}
	case "clipboard-write":
		switch strings.ToLower(val) {
		case "deny", "ask": // the library has no write prompt: refuse
			c.NoClipboardWrite = true
		case "allow":
			c.NoClipboardWrite = false
		}
	case "clipboard-read":
		switch strings.ToLower(val) {
		case "allow":
			a := terminal.ClipboardAllow
			c.ClipboardRead = &a
		case "ask":
			a := terminal.ClipboardAsk
			c.ClipboardRead = &a
		case "deny":
			a := terminal.ClipboardDeny
			c.ClipboardRead = &a
		}
	case "window-padding-x", "window-padding-y":
		if f, ok := paddingValue(val); ok {
			if c.Padding == nil {
				c.Padding = &terminal.Padding{X: 2, Y: 2}
			}
			if strings.HasSuffix(key, "-x") {
				c.Padding.X = f
			} else {
				c.Padding.Y = f
			}
		}
	case "window-padding-color":
		switch strings.ToLower(val) {
		case "background":
			col := terminal.PaddingBackground
			c.PaddingColor = &col
		case "extend":
			col := terminal.PaddingExtend
			c.PaddingColor = &col
		case "extend-always":
			col := terminal.PaddingExtendAlways
			c.PaddingColor = &col
		}
	case "image-storage-limit":
		if n, err := strconv.ParseUint(val, 10, 64); err == nil && n <= 4<<30 {
			i := int64(n)
			if n == 0 {
				i = -1 // the library refuses images with a negative limit
			}
			c.ImageLimit = &i
		}
	case "right-click-action":
		if a, ok := rightClickActions[strings.ToLower(val)]; ok {
			c.RightClick = a
		}
	case "minimum-contrast":
		if f, err := strconv.ParseFloat(val, 32); err == nil && f == f {
			c.MinimumContrast = float32(min(max(f, 1), 21))
		}
	case "keybind":
		c.setKeybind(val)
	default:
		if key != "" && !slices.Contains(c.Unknown, key) {
			c.Unknown = append(c.Unknown, key)
		}
	}
}

var rightClickActions = map[string]terminal.RightClickAction{
	"context-menu":  terminal.RightClickContextMenu,
	"paste":         terminal.RightClickPaste,
	"copy":          terminal.RightClickCopy,
	"copy-or-paste": terminal.RightClickCopyOrPaste,
	"ignore":        terminal.RightClickIgnore,
}

var keybindMods = map[string]ui.Modifiers{
	"ctrl": ui.Ctrl, "control": ui.Ctrl,
	"shift": ui.Shift,
	"alt":   ui.Alt, "opt": ui.Alt, "option": ui.Alt,
	"super": ui.Super, "cmd": ui.Super, "command": ui.Super,
}

// keybindKeys maps Ghostty's key names, besides a to z and 0 to 9, to keys.
var keybindKeys = map[string]ui.Key{
	"equal": ui.KeyEqual, "minus": ui.KeyMinus,
	"page_up": ui.KeyPageUp, "page_down": ui.KeyPageDown,
	"home": ui.KeyHome, "end": ui.KeyEnd,
	"insert": ui.KeyInsert, "delete": ui.KeyDelete,
	"enter": ui.KeyEnter, "tab": ui.KeyTab, "escape": ui.KeyEscape,
	"space": ui.KeySpace, "backspace": ui.KeyBackspace,
	"arrow_up": ui.KeyUp, "arrow_down": ui.KeyDown,
	"arrow_left": ui.KeyLeft, "arrow_right": ui.KeyRight,
	"grave_accent": ui.KeyBackquote, "bracket_left": ui.KeyBracketLeft,
	"bracket_right": ui.KeyBracketRight, "comma": ui.KeyComma,
	"period": ui.KeyPeriod, "slash": ui.KeySlash,
	"semicolon": ui.KeySemicolon, "quote": ui.KeyQuote,
	"backslash": ui.KeyBackslash,
}

var keybindActions = map[string]terminal.Action{
	"unbind":               terminal.ActionNone,
	"ignore":               terminal.ActionIgnore,
	"copy_to_clipboard":    terminal.ActionCopy,
	"paste_from_clipboard": terminal.ActionPaste,
	"select_all":           terminal.ActionSelectAll,
	"clear_screen":         terminal.ActionClearScreen,
	"increase_font_size":   terminal.ActionIncreaseFontSize,
	"decrease_font_size":   terminal.ActionDecreaseFontSize,
	"reset_font_size":      terminal.ActionResetFontSize,
	"scroll_page_up":       terminal.ActionScrollPageUp,
	"scroll_page_down":     terminal.ActionScrollPageDown,
	"scroll_to_top":        terminal.ActionScrollToTop,
	"scroll_to_bottom":     terminal.ActionScrollToBottom,
}

func init() {
	for i := range 26 {
		keybindKeys[string(rune('a'+i))] = ui.KeyA + ui.Key(i)
	}
	for i := range 10 {
		keybindKeys[string(rune('0'+i))] = ui.Key0 + ui.Key(i)
	}
	for i := range 12 {
		keybindKeys["f"+strconv.Itoa(i+1)] = ui.KeyF1 + ui.Key(i)
	}
}

// setKeybind applies a keybind line. "clear" drops the bindings so far and
// the defaults. Lines GoRex cannot do are skipped: global, all and
// unconsumed ones (they act outside a pane), sequences, and actions or keys
// the terminal has none for. A later binding of a trigger replaces an
// earlier one.
func (c *ghosttyConfig) setKeybind(val string) {
	if strings.ToLower(val) == "clear" {
		c.Keybinds, c.NoDefaultKeybinds = nil, true
		return
	}
	var b terminal.Keybind
	for {
		prefix, rest, ok := strings.Cut(val, ":")
		if !ok || strings.ContainsAny(prefix, "=+>") {
			break
		}
		switch strings.ToLower(strings.TrimSpace(prefix)) {
		case "performable":
			b.Performable = true
		default:
			// Including global, all, and unconsumed.
			return
		}
		val = rest
	}
	trigger, action, ok := strings.Cut(val, "=")
	if !ok || strings.Contains(trigger, ">") {
		return
	}
	action, _, _ = strings.Cut(action, ":")
	a, ok := keybindActions[strings.ToLower(strings.TrimSpace(action))]
	if !ok {
		return
	}
	b.Action = a
	found := false
	for _, part := range strings.Split(strings.ToLower(strings.TrimSpace(trigger)), "+") {
		part = strings.TrimSpace(part)
		if m, ok := keybindMods[part]; ok {
			b.Mods |= m
		} else if part == "plus" && !found {
			// The defaults take plus as Shift and the equal key.
			b.Key, b.Mods, found = ui.KeyEqual, b.Mods|ui.Shift, true
		} else if k, ok := keybindKeys[part]; ok && !found {
			b.Key, found = k, true
		} else {
			return
		}
	}
	if !found {
		return
	}
	c.Keybinds = slices.DeleteFunc(c.Keybinds, func(o terminal.Keybind) bool {
		return o.Mods == b.Mods && o.Key == b.Key
	})
	c.Keybinds = append(c.Keybinds, b)
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
// must when the configuration sets a theme that loads or a background.
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
				l, opaque = *t, true
			}
		}
		if dname != "" {
			if t, err := terminal.GhosttyTheme(dname); err == nil {
				d, opaque = *t, true
			}
		}
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
		if c.Search != nil {
			t.Search = *c.Search
		}
		if c.SearchText != nil {
			t.SearchText = *c.SearchText
		}
		if c.SearchSel != nil {
			t.SearchSelected = *c.SearchSel
		}
		if c.SearchSelText != nil {
			t.SearchSelectedText = *c.SearchSelText
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
	// termAppearance is the windows' appearance the colors call for, as
	// Ghostty's window-theme = auto picks it: "dark" or "light" by the
	// background of one theme for both, or "" to follow the system.
	termAppearance string
)

// applyGhostty computes the terminals' font and colors from the
// configuration and the user's text size.
func applyGhostty() {
	registerFamilies(fontDirs(), ghostty.Families)
	termFont = ghostty.font(ghostty.fontSize(prefs.FontSize))
	termLight, termDark, termOpaque = ghostty.themes()
	termAppearance = ""
	if termOpaque && !strings.Contains(ghostty.Theme, "light:") && !strings.Contains(ghostty.Theme, "dark:") {
		termAppearance = "light"
		if b := termDark.Background; int(b.R)*299+int(b.G)*587+int(b.B)*114 < 128*1000 {
			termAppearance = "dark"
		}
	}
}

// termOptions are the options of every terminal. The cursor, scrollback,
// option-as-alt, right click, and keybinds are set only when a terminal is
// made, so a reload applies them to new panes; fonts, themes, copy on
// select, and minimum contrast it applies to all.
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

		CopyOnSelect:      ghostty.CopyOnSelect,
		NoClipboardWrite:  ghostty.NoClipboardWrite,
		ClipboardRead:     clipboardReadOrAsk(ghostty.ClipboardRead),
		RightClick:        ghostty.RightClick,
		Padding:           paddingOrZero(ghostty.Padding),
		PaddingColor:      paddingColorOrZero(ghostty.PaddingColor),
		ImageLimit:        imageLimitOrZero(ghostty.ImageLimit),
		MinimumContrast:   ghostty.MinimumContrast,
		Keybinds:          ghostty.Keybinds,
		NoDefaultKeybinds: ghostty.NoDefaultKeybinds,
	}
}

func paddingOrZero(p *terminal.Padding) terminal.Padding {
	if p == nil {
		return terminal.Padding{}
	}
	return *p
}

func clipboardReadOrAsk(a *terminal.ClipboardAccess) terminal.ClipboardAccess {
	if a == nil {
		return terminal.ClipboardAsk
	}
	return *a
}

func imageLimitOrZero(n *int64) int64 {
	if n == nil {
		return 0
	}
	return *n
}

// paddingValue reads Ghostty's window padding, a u32; the comma form's
// two sides are one value to the library, which takes the first.
func paddingValue(v string) (float32, bool) {
	v, _, _ = strings.Cut(v, ",")
	n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 32)
	if err != nil {
		return 0, false
	}
	return float32(n), true
}

func paddingColorOrZero(p *terminal.PaddingColor) terminal.PaddingColor {
	if p == nil {
		return terminal.PaddingBackground
	}
	return *p
}

// reloadGhostty reads the configuration again and applies the font, colors,
// copy on select, and minimum contrast to every pane.
func (a *App) reloadGhostty() {
	loadGhostty()
	a.applyPrefs()
}
