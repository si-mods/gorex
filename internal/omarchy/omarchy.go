// Package omarchy joins GoRex to an Omarchy desktop (docs/DSK-015-omarchy.md):
// it reads the palette of the desktop's current theme, makes a theme
// manifest from it in memory, and keeps the optional Hyprland binding.
// GoRex ships the reader, never a look (docs/DSK-008-theme.md, Non-goals).
package omarchy

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gorex/internal/atomicfile"
	"gorex/internal/homedir"
	"gorex/internal/rex"
	"gorex/internal/theme"
)

const (
	// Name is the name of the manifest GoRex makes from the desktop's
	// palette.
	Name = "Omarchy"
	// DescriptionPrefix starts that manifest's description, followed by
	// the desktop's theme name.
	DescriptionPrefix = "Omarchy theme "
	// BindingBegin and BindingEnd fence the block in the Hyprland bindings
	// file, so that a write replaces it wherever it is, and Unbind removes
	// only it.
	BindingBegin = "-- gorex omarchy: begin"
	BindingEnd   = "-- gorex omarchy: end"
	// DefaultBinding is the keys Bind uses unless the user names others.
	DefaultBinding = "SUPER + SHIFT + R"
)

// The actions of a Change.
const (
	ActionWrite     = "write"
	ActionRemove    = "remove"
	ActionUnchanged = "unchanged"
)

// Paths are the files the package reads and writes. Each is a field, so
// tests use temporary directories.
type Paths struct {
	// OmarchyRoot is the Omarchy installation: $OMARCHY_PATH, else
	// /usr/share/omarchy.
	OmarchyRoot string
	// StateDir is the desktop's state folder: $XDG_STATE_HOME/omarchy,
	// else ~/.local/state/omarchy.
	StateDir string
	// ConfigHome is $XDG_CONFIG_HOME, else ~/.config: the home of
	// Hyprland's bindings.
	ConfigHome string
	// SettingsPath is GoRex's settings.json, in rex.Dir().
	SettingsPath string
}

// DefaultPaths returns the paths of this machine, from the environment and
// GoRex's home directory.
func DefaultPaths() Paths {
	home, _ := os.UserHomeDir()
	configHome, _ := homedir.XDGConfigHome(home)
	return Paths{
		OmarchyRoot:  cmp.Or(strings.TrimSpace(os.Getenv("OMARCHY_PATH")), "/usr/share/omarchy"),
		StateDir:     filepath.Join(cmp.Or(strings.TrimSpace(os.Getenv("XDG_STATE_HOME")), filepath.Join(home, ".local", "state")), "omarchy"),
		ConfigHome:   cmp.Or(strings.TrimSpace(configHome), filepath.Join(home, ".config")),
		SettingsPath: filepath.Join(rex.Dir(), "settings.json"),
	}
}

// CurrentDir is the folder the desktop swaps its theme under. Omarchy
// renders the next theme in a sibling directory and renames it into place,
// so a watch of this folder survives the swap (docs/DSK-015-omarchy.md,
// Behavior).
func (p Paths) CurrentDir() string { return filepath.Join(p.StateDir, "current") }

// palettePath is the palette of the desktop's current theme.
func (p Paths) palettePath() string { return filepath.Join(p.CurrentDir(), "theme", "colors.toml") }

// themeNamePath is the name of the desktop's current theme.
func (p Paths) themeNamePath() string { return filepath.Join(p.CurrentDir(), "theme.name") }

// bindingPath is Hyprland's Lua bindings file.
func (p Paths) bindingPath() string { return filepath.Join(p.ConfigHome, "hypr", "bindings.lua") }

// Detect reports whether p.OmarchyRoot is an Omarchy installation: it holds
// version and bin/omarchy-theme-current.
func Detect(p Paths) error {
	for _, name := range []string{"version", filepath.Join("bin", "omarchy-theme-current")} {
		path := filepath.Join(p.OmarchyRoot, name)
		if st, err := os.Stat(path); err != nil || st.IsDir() {
			return fmt.Errorf("not an Omarchy system: %s is missing", path)
		}
	}
	return nil
}

// Palette is the desktop's current theme: its name, its mode, and the six
// colors the manifest takes, as `omarchy-theme-color --all` resolves them
// (docs/DSK-015-omarchy.md, Model). A color the palette does not resolve is
// empty, and the built-in seed stands.
type Palette struct {
	Name              string
	Mode              string
	Background        string
	DarkerBackground  string
	LighterBackground string
	Foreground        string
	Muted             string
	Accent            string
	Selection         string
	Red               string
	Green             string
	Yellow            string
}

// ReadPalette reads the desktop's current theme: colors.toml and the theme
// name beside it, with the cascade of docs/DSK-015-omarchy.md, Model.
func ReadPalette(p Paths) (Palette, error) {
	b, err := os.ReadFile(p.palettePath())
	if err != nil {
		return Palette{}, fmt.Errorf("reading the Omarchy palette: %w", err)
	}
	name, err := os.ReadFile(p.themeNamePath())
	if err != nil {
		return Palette{}, fmt.Errorf("reading the Omarchy theme name: %w", err)
	}
	pal := Palette{Name: strings.TrimSpace(string(name))}
	if pal.Name == "" {
		return Palette{}, errors.New("the Omarchy theme has no name")
	}
	vals := parseColors(b)
	pal.Background = firstHex(vals, "background", "bg", "color0")
	pal.LighterBackground = firstHex(vals, "lighter_background", "lighter_bg")
	if pal.LighterBackground == "" {
		pal.LighterBackground = cmp.Or(firstHex(vals, "color0"), pal.Background)
	}
	pal.Foreground = firstHex(vals, "foreground", "fg", "color7")
	pal.Muted = firstHex(vals, "muted", "color8", "dark_foreground")
	if pal.Muted == "" {
		pal.Muted = pal.Foreground
	}
	pal.Accent = firstHex(vals, "accent", "blue", "color4")
	if pal.Accent == "" {
		pal.Accent = pal.Foreground
	}
	pal.Selection = firstHex(vals, "selection", "selection_background", "color8", "color0")
	if pal.Selection == "" {
		pal.Selection = pal.Background
	}
	pal.DarkerBackground = firstHex(vals, "darker_background", "darker_bg")
	if pal.DarkerBackground == "" {
		pal.DarkerBackground = mix(pal.Background, "#000000", 0.5)
	}
	for _, f := range []struct {
		names []string
		dst   *string
	}{
		{[]string{"red", "color1"}, &pal.Red},
		{[]string{"green", "color2"}, &pal.Green},
		{[]string{"yellow", "color3"}, &pal.Yellow},
	} {
		*f.dst = firstHex(vals, f.names...)
		if *f.dst == "" {
			*f.dst = pal.Accent
		}
	}
	pal.Mode = paletteMode(vals, p.palettePath(), pal.Background)
	return pal, nil
}

// firstHex returns the first of names whose value is a color, or "" when
// none is. A value that is not a hex color is dropped, and the next
// fallback is tried.
func firstHex(vals map[string]string, names ...string) string {
	for _, name := range names {
		if _, ok := theme.Hex(vals[name]); ok {
			return vals[name]
		}
	}
	return ""
}

// mix blends one color toward another, as omarchy-theme-color does: amount
// runs from 0 (start) to 1 (end), and the channel values round.
func mix(start, end string, amount float64) string {
	a, ok := theme.Hex(start)
	b, ok2 := theme.Hex(end)
	if !ok || !ok2 {
		return start
	}
	if amount < 0 {
		amount = 0
	}
	if amount > 1 {
		amount = 1
	}
	blend := func(x, y uint8) uint8 {
		return uint8(float64(x)*(1-amount) + float64(y)*amount + 0.5)
	}
	return fmt.Sprintf("#%02x%02x%02x", blend(a.R, b.R), blend(a.G, b.G), blend(a.B, b.B))
}

// paletteMode is the mode of the palette: `mode`, else `theme_type`, else
// `light` when a light.mode file sits beside colors.toml, else the
// background's bytes sum above 382 mean light, else dark.
func paletteMode(vals map[string]string, palettePath, background string) string {
	for _, name := range []string{"mode", "theme_type"} {
		switch strings.ToLower(strings.TrimSpace(vals[name])) {
		case "light":
			return "light"
		case "dark":
			return "dark"
		}
	}
	if st, err := os.Stat(filepath.Join(filepath.Dir(palettePath), "light.mode")); err == nil && !st.IsDir() {
		return "light"
	}
	if c, ok := theme.Hex(background); ok && int(c.R)+int(c.G)+int(c.B) > 382 {
		return "light"
	}
	return "dark"
}

// parseColors reads the small TOML subset of colors.toml: a key and a quoted
// value on a line, with blank lines and # comment lines between. A key the
// palette does not use is ignored, as are lines of another shape.
func parseColors(b []byte) map[string]string {
	vals := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rest, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, rest = strings.TrimSpace(key), strings.TrimSpace(rest)
		if key == "" {
			continue
		}
		// A quoted value ends at its closing quote, which keeps an inline
		// comment out. An unquoted value is the rest of the line, as
		// omarchy-theme-color reads it.
		if i := strings.IndexByte(rest, '"'); i >= 0 {
			if j := strings.IndexByte(rest[i+1:], '"'); j >= 0 {
				vals[key] = rest[i+1 : i+1+j]
			}
			continue
		}
		if rest != "" {
			vals[key] = rest
		}
	}
	return vals
}

// The manifest's shape: one appearance, its seeds, and the selection as the
// sidebar's active tab. GoRex makes it in memory and never writes it.
type manifestFile struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Seeds       map[string]manifestSeeds `json:"seeds"`
	Roles       map[string]string        `json:"roles,omitempty"`
}

type manifestSeeds struct {
	BgTop     string `json:"bgTop,omitempty"`
	Panel     string `json:"panel,omitempty"`
	Text      string `json:"text,omitempty"`
	TextMuted string `json:"textMuted,omitempty"`
	TextFaint string `json:"textFaint,omitempty"`
	IconMuted string `json:"iconMuted,omitempty"`
	Accent    string `json:"accent,omitempty"`
	Danger    string `json:"danger,omitempty"`
	Done      string `json:"done,omitempty"`
	Attention string `json:"attention,omitempty"`
}

// Manifest makes the theme of pal in memory: the seeds of the palette's
// mode only, and the selection as the sidebar's active tab, in the shape of
// a theme pack (docs/DSK-015-omarchy.md, Model). A key the palette does not
// resolve is left out, and the built-in seed stands (docs/DSK-008-theme.md).
func Manifest(pal Palette) *theme.Manifest {
	seeds := manifestSeeds{}
	set := func(dst *string, v string) {
		if _, ok := theme.Hex(v); ok {
			*dst = v
		}
	}
	set(&seeds.BgTop, pal.Background)
	set(&seeds.Panel, pal.LighterBackground)
	set(&seeds.Text, pal.Foreground)
	set(&seeds.TextMuted, pal.Muted)
	set(&seeds.TextFaint, pal.Muted)
	set(&seeds.IconMuted, pal.Muted)
	set(&seeds.Accent, pal.Accent)
	set(&seeds.Danger, pal.Red)
	set(&seeds.Done, pal.Green)
	set(&seeds.Attention, pal.Yellow)
	mf := manifestFile{
		Name:        Name,
		Description: DescriptionPrefix + pal.Name + " (generated by gorex)",
		Seeds:       map[string]manifestSeeds{appearance(pal.Mode): seeds},
		Roles:       map[string]string{},
	}
	role := func(name, v string) {
		if _, ok := theme.Hex(v); ok {
			mf.Roles[name] = v
		}
	}
	role("sideBar.background", pal.Background)
	role("titleBar.background", pal.DarkerBackground)
	role("paneHeader.background", pal.LighterBackground)
	role("button.background", pal.LighterBackground)
	role("dropdown.background", pal.LighterBackground)
	role("windowControl.background", pal.LighterBackground)
	role("sideBar.tabActiveBackground", pal.Selection)
	role("sideBar.tabHoverBackground", pal.LighterBackground)
	role("sideBar.trackBackground", pal.LighterBackground)
	role("sideBar.tabSeparator", pal.DarkerBackground)
	role("pane.border", pal.Selection)
	b, err := json.Marshal(mf)
	if err != nil {
		return &theme.Manifest{Name: Name}
	}
	m, _, err := theme.Parse(b)
	if err != nil {
		return &theme.Manifest{Name: Name}
	}
	return m
}

// appearance is the manifest's appearance for a mode: "dark" for a dark
// palette, else "light".
func appearance(mode string) string {
	if mode == "dark" {
		return "dark"
	}
	return "light"
}

// Change is what Bind or Unbind made of the bindings file.
type Change struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

// Bind adds the block of keys to Hyprland's bindings file, or replaces the
// one there, and keeps every other line. It refuses a symlink.
func Bind(p Paths, keys string) (Change, error) {
	if !ValidKeys(keys) {
		return Change{}, errors.New("the keys must not be empty, or hold a quote or a control character")
	}
	path := p.bindingPath()
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return Change{}, fmt.Errorf("%s is a symlink; not rewriting it", path)
	}
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Change{}, err
	}
	out, err := withBinding(string(old), keys)
	if err != nil {
		return Change{}, err
	}
	if out == string(old) {
		return Change{path, ActionUnchanged}, nil
	}
	if err := writeFileAtomic(path, []byte(out), bindingPerm(path)); err != nil {
		return Change{}, err
	}
	return Change{path, ActionWrite}, nil
}

// Unbind removes the block from Hyprland's bindings file, and keeps every
// other line. It refuses a symlink.
func Unbind(p Paths) (Change, error) {
	path := p.bindingPath()
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return Change{}, fmt.Errorf("%s is a symlink; not rewriting it", path)
	}
	old, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Change{path, ActionUnchanged}, nil
	}
	if err != nil {
		return Change{}, err
	}
	out, err := withoutBinding(string(old))
	if err != nil {
		return Change{}, err
	}
	if out == string(old) {
		return Change{path, ActionUnchanged}, nil
	}
	if err := writeFileAtomic(path, []byte(out), bindingPerm(path)); err != nil {
		return Change{}, err
	}
	return Change{path, ActionRemove}, nil
}

// ValidKeys reports whether keys may stand in a Lua string: not empty, and
// without a quote, a backslash or a control character.
func ValidKeys(keys string) bool {
	if keys == "" {
		return false
	}
	for _, r := range keys {
		if r == '"' || r == '\\' || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// bindingPerm is the mode the bindings file keeps, or 0644 for a new one.
func bindingPerm(path string) os.FileMode {
	if st, err := os.Stat(path); err == nil {
		return st.Mode().Perm()
	}
	return 0o644
}

// Report is what Status finds: the text and the JSON of
// `gorex omarchy status`.
type Report struct {
	Omarchy          bool     `json:"omarchy"`
	Root             string   `json:"root,omitempty"`
	Theme            string   `json:"theme,omitempty"`
	Mode             string   `json:"mode,omitempty"`
	Following        bool     `json:"following"`
	Binding          string   `json:"binding,omitempty"`
	BindingInstalled bool     `json:"bindingInstalled"`
	Warnings         []string `json:"warnings,omitempty"`
}

// Status reads the desktop and the binding, without changing anything.
// following says whether the settings ask GoRex to follow the desktop; a
// machine without Omarchy, or a palette that does not read, follows none.
func Status(p Paths, following bool) Report {
	r := Report{Root: p.OmarchyRoot}
	r.Omarchy = Detect(p) == nil
	if r.Omarchy {
		if pal, err := ReadPalette(p); err != nil {
			r.Warnings = append(r.Warnings, err.Error())
			following = false
		} else {
			r.Theme, r.Mode = pal.Name, pal.Mode
		}
	} else {
		following = false
	}
	r.Following = following
	r.Binding, r.BindingInstalled = readBinding(p.bindingPath())
	return r
}

// Settings are the two keys of GoRex's settings.json that decide whether it
// follows the desktop (docs/DSK-015-omarchy.md, Behavior).
type Settings struct {
	Theme   string `json:"appearance.theme"`
	Omarchy *bool  `json:"appearance.omarchy"`
}

// ReadSettings reads those keys at path. A missing or unreadable file is no
// settings, which follow the desktop.
func ReadSettings(path string) Settings {
	b, err := os.ReadFile(path)
	if err != nil {
		return Settings{}
	}
	var s Settings
	if json.Unmarshal(b, &s) != nil {
		return Settings{}
	}
	return s
}

// Follows reports whether the settings ask GoRex to follow the desktop: no
// appearance.theme, and appearance.omarchy not false. An absent
// appearance.omarchy means true.
func (s Settings) Follows() bool {
	return s.Theme == "" && (s.Omarchy == nil || *s.Omarchy)
}

// bindingBlock is the Lua of the key binding: Omarchy's o.bind takes the
// keys, a label and a command.
func bindingBlock(keys string) string {
	return BindingBegin + "\n" +
		fmt.Sprintf("o.bind(%q, %q, %q)\n", keys, "GoRex", "gorex") +
		BindingEnd
}

// splitLua splits a Lua file into lines, without the empty last line a
// trailing newline makes.
func splitLua(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// joinLua joins lines with one trailing newline, so a write leaves exactly
// one.
func joinLua(ls []string) string {
	if len(ls) == 0 {
		return ""
	}
	return strings.Join(ls, "\n") + "\n"
}

// findBlock returns the lines of the gorex omarchy block, or -1, -1 when
// there is none. A marker with no partner is an error: the file is not one
// this command wrote.
func findBlock(ls []string) (start, end int, err error) {
	start, end = -1, -1
	for i, l := range ls {
		switch strings.TrimSpace(l) {
		case BindingBegin:
			if start >= 0 {
				return 0, 0, errors.New("bindings.lua holds more than one gorex omarchy block")
			}
			start = i
		case BindingEnd:
			if start < 0 || end >= 0 {
				return 0, 0, errors.New("bindings.lua holds a gorex omarchy end marker with no begin before it")
			}
			end = i
		}
	}
	if start < 0 {
		return -1, -1, nil
	}
	if end < 0 {
		return 0, 0, errors.New("bindings.lua holds a gorex omarchy begin marker with no end after it")
	}
	return start, end, nil
}

// withBinding returns the Lua file with the block of keys in place: the
// block replaced where it is, else added at the end. Every other line stays.
func withBinding(existing, keys string) (string, error) {
	ls := splitLua(existing)
	block := strings.Split(bindingBlock(keys), "\n")
	start, end, err := findBlock(ls)
	if err != nil {
		return "", err
	}
	if start < 0 {
		return joinLua(append(ls, block...)), nil
	}
	out := make([]string, 0, len(ls)-(end-start+1)+len(block))
	out = append(out, ls[:start]...)
	out = append(out, block...)
	out = append(out, ls[end+1:]...)
	return joinLua(out), nil
}

// withoutBinding returns the Lua file without the block. It returns the
// file as it is when there is none.
func withoutBinding(existing string) (string, error) {
	ls := splitLua(existing)
	start, end, err := findBlock(ls)
	if err != nil || start < 0 {
		return existing, err
	}
	out := make([]string, 0, len(ls)-(end-start+1))
	out = append(out, ls[:start]...)
	out = append(out, ls[end+1:]...)
	return joinLua(out), nil
}

// readBinding returns the keys of the block in the Hyprland bindings file,
// and whether the block is there.
func readBinding(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	ls := splitLua(string(b))
	start, end, err := findBlock(ls)
	if err != nil || start < 0 {
		return "", false
	}
	for _, l := range ls[start : end+1] {
		if keys, ok := bindKeys(l); ok {
			return keys, true
		}
	}
	return "", true
}

// bindKeys reads the keys of one o.bind call.
func bindKeys(line string) (string, bool) {
	s := strings.TrimSpace(line)
	const call = "o.bind("
	if !strings.HasPrefix(s, call) {
		return "", false
	}
	rest := s[len(call):]
	if !strings.HasPrefix(rest, `"`) {
		return "", false
	}
	if i := strings.Index(rest[1:], `"`); i >= 0 {
		return rest[1 : 1+i], true
	}
	return "", false
}

// writeFileAtomic creates parent directories if needed and writes b to path
// atomically.
func writeFileAtomic(path string, b []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(path, b, perm)
}
