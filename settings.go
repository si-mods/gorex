package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/plugins/terminal"

	"gorex/internal/config"
	"gorex/internal/rex"
	"gorex/internal/theme"
)

// settings are the user's choices, kept in settings.json beside the
// server's state.
type settings struct {
	// Appearance is "light", "dark", or "" to follow the system.
	Appearance string `json:"appearance,omitempty"`
	// Theme names the chrome's theme (docs/DSK-008-theme.md): a manifest
	// <config>/themes/<name>.json, which a theme pack installs too, or ""
	// for the built-in palettes. Builds before it ignore the key.
	Theme string `json:"appearance.theme,omitempty"`
	// Ambience is how the theme's ambience shows
	// (docs/DSK-010-theme-ambience.md): "" moving, "still" or "off". Mascot
	// is "" to show the theme's mascot, or "off".
	Ambience string  `json:"appearance.ambience,omitempty"`
	Mascot   string  `json:"appearance.mascot,omitempty"`
	FontSize float32 `json:"fontSize,omitempty"`
	// Language is a tag of the app's languages, or "" to follow the
	// system's.
	Language string `json:"language,omitempty"`

	// TabPlacement is "left" or "right" for tabs in a sidebar, or "" for
	// tabs above the panes. The sidebar's width is SidebarWidth, 0 for
	// sidebarDefaultW, and SidebarHidden collapses it. HidePaneHeaders
	// hides the panes' headers while a left sidebar shows.
	TabPlacement    string  `json:"tabPlacement,omitempty"`
	SidebarWidth    float32 `json:"sidebarWidth,omitempty"`
	SidebarHidden   bool    `json:"sidebarHidden,omitempty"`
	HidePaneHeaders bool    `json:"hidePaneHeaders,omitempty"`
	// The sidebar.* keys style the sidebar (docs/DSK-007-sidebar-style.md):
	// flat dotted keys as VS Code's settings are. The metrics clamp to a
	// range, 0 for the default; sidebar.colorCustomizations maps one of the
	// sidebar's palette tokens to "#rrggbb[aa]" or a {"light","dark"} pair.
	SidebarRowHeight float32                    `json:"sidebar.rowHeight,omitempty"`
	SidebarIndent    float32                    `json:"sidebar.indent,omitempty"`
	SidebarFontSize  float32                    `json:"sidebar.fontSize,omitempty"`
	SidebarGroupPad  float32                    `json:"sidebar.groupPadding,omitempty"`
	SidebarGroupBy   string                     `json:"sidebar.groupBy,omitempty"`
	SidebarIcons     string                     `json:"sidebar.icons,omitempty"`
	SidebarSelection string                     `json:"sidebar.selection,omitempty"`
	SidebarColors    map[string]json.RawMessage `json:"sidebar.colorCustomizations,omitempty"`
	// What this client keeps of the projects of its hosts
	// (docs/MOD-002-projects.md, "The desktop window"): the space shown, by name,
	// "" for Default; the spaces' colors and order, and those made here
	// that no project carries yet; the rows the sidebar opened or closed;
	// the project each window shows, by its place; and the renames of
	// spaces that wait for a host to connect (spaces.go).
	Space        string                   `json:"space,omitempty"`
	Spaces       []clientSpace            `json:"spaces,omitempty"`
	Open         map[string]bool          `json:"sidebarOpen,omitempty"`
	Shown        []string                 `json:"shownProjects,omitempty"`
	SpaceRenames map[string][]spaceRename `json:"spaceRenames,omitempty"`
	// HideDockBadge turns off the count of the panes that need the user on
	// the app icon.
	HideDockBadge bool `json:"hideDockBadge,omitempty"`
	// FoldedHosts are the hosts whose tabs the sidebar folds under their
	// name: "local" for this machine, else a host's id.
	FoldedHosts []string `json:"foldedHosts,omitempty"`
	// Animation is how the panes and the sidebar move: "" springs snappily,
	// "natural" more softly, and "off" not at all.
	Animation string `json:"animation,omitempty"`
	// NoGlass turns off the glass, on until then: the windows' material
	// behind their pages, and the glass of what floats over them.
	// TermOpacity is how opaque the terminals' background is over the
	// window's glass, from 0.3 to 1, or 0 for defaultTermOpacity.
	NoGlass     bool    `json:"noGlass,omitempty"`
	TermOpacity float32 `json:"terminal.opacity,omitempty"`
	// Canvas lays every tab out as an infinite canvas of free panes
	// (canvas.go), an experiment, off by default.
	Canvas bool `json:"canvas,omitempty"`

	// The terminal.* keys, when set, win over Ghostty's configuration, which
	// wins over GoRex's defaults (docs/DSK-009-terminal-settings.md). Empty
	// and nil follow Ghostty. terminal.opacity is TermOpacity, above.
	TermFont         string `json:"terminal.font,omitempty"`   // a font family
	TermTheme        string `json:"terminal.theme,omitempty"`  // a Ghostty theme's name
	TermCursor       string `json:"terminal.cursor,omitempty"` // "block", "bar" or "underline"
	TermCursorBlink  *bool  `json:"terminal.cursorBlink,omitempty"`
	TermCopyOnSelect *bool  `json:"terminal.copyOnSelect,omitempty"`
	// TermMinContrast is the least contrast of text with its background,
	// from 1 (no correction) to 21; 0 follows Ghostty's minimum-contrast,
	// else defaultMinContrast.
	TermMinContrast float32 `json:"terminal.minimumContrast,omitempty"`

	// IconPacks lists active community program icon pack IDs in priority
	// order; IconPackPriority is "pack" (default) or "osc".
	IconPacks        []string `json:"iconPacks,omitempty"`
	IconPackPriority string   `json:"iconPackPriority,omitempty"`
}

// defaultMinContrast is the minimum contrast when neither the user nor
// Ghostty sets one. 3 lifts only text that is hard to read, as light
// themes' yellow and white at about 2:1 or dark themes' black at about
// 1:1, where 4.5 would recolor most light palettes. The correction keeps
// the hue, so a program's colors still tell errors from warnings.
const defaultMinContrast = 3

const defaultFontSize = 11.6

// The range of the text size, in points, of terminals and notes alike.
const (
	minTextSize = 8
	maxTextSize = 40
)

var prefs settings

func settingsPath() string { return filepath.Join(rex.Dir(), "settings.json") }

// settingsKeyRenames maps the keys settings.json had to the ones that
// replace them (docs/DSK-009-terminal-settings.md).
var settingsKeyRenames = map[string]string{
	"font":            "terminal.font",
	"theme":           "terminal.theme",
	"cursor":          "terminal.cursor",
	"cursorBlink":     "terminal.cursorBlink",
	"copyOnSelect":    "terminal.copyOnSelect",
	"minimumContrast": "terminal.minimumContrast",
	"terminalOpacity": "terminal.opacity",
}

// tokenRenames maps the color tokens of docs/DSK-007-sidebar-style.md to
// the role names that replace them (docs/DSK-008-theme.md). They live in
// the object of sidebarColorsKey.
var tokenRenames = map[string]string{
	"groupFace":  "sideBar.activeGroupBackground",
	"pillLift":   "sideBar.activeGroupTabBackground",
	"liftShadow": "sideBar.activeGroupTabShadow",
	"track":      "sideBar.trackBackground",
	"tabSep":     "sideBar.tabSeparator",
	"tabHover":   "sideBar.tabHoverBackground",
	"tabActive":  "sideBar.tabActiveBackground",
}

const sidebarColorsKey = "sidebar.colorCustomizations"

// renameJSONKeys renames the keys of raw by renames, keeping the values raw. A
// new key that is already there wins over its old key; the old key goes
// either way. It reports whether it changed anything.
func renameJSONKeys(raw map[string]json.RawMessage, renames map[string]string) bool {
	changed := false
	for old, key := range renames {
		v, ok := raw[old]
		if !ok {
			continue
		}
		if _, has := raw[key]; !has {
			raw[key] = v
		}
		delete(raw, old)
		changed = true
	}
	return changed
}

// migrateSettings renames the old keys of a settings object to the new ones,
// and the old color tokens in its sidebar.colorCustomizations to role names,
// and reports whether it changed anything. A new name that is already there
// wins over its old one; the old one goes either way. The values stay raw,
// so keys it does not know keep theirs exactly.
func migrateSettings(raw map[string]json.RawMessage) bool {
	changed := renameJSONKeys(raw, settingsKeyRenames)
	var colors map[string]json.RawMessage
	if json.Unmarshal(raw[sidebarColorsKey], &colors) == nil && renameJSONKeys(colors, tokenRenames) {
		var buf bytes.Buffer
		writeRawObject(&buf, colors, "  ")
		raw[sidebarColorsKey] = buf.Bytes()
		changed = true
	}
	return changed
}

// writeRawObject writes a raw object one key to a line, in key order, with
// each value as it was: json.Marshal would compact it. indent is the
// object's own indentation.
func writeRawObject(buf *bytes.Buffer, raw map[string]json.RawMessage, indent string) {
	buf.WriteString("{")
	for i, k := range slices.Sorted(maps.Keys(raw)) {
		if i > 0 {
			buf.WriteString(",")
		}
		kb, _ := json.Marshal(k)
		buf.WriteString("\n" + indent + "  ")
		buf.Write(kb)
		buf.WriteString(": ")
		buf.Write(raw[k])
	}
	buf.WriteString("\n" + indent + "}")
}

// marshalRawObject writes a settings object as writeRawObject does, ending
// in a newline.
func marshalRawObject(raw map[string]json.RawMessage) []byte {
	var buf bytes.Buffer
	writeRawObject(&buf, raw, "")
	buf.WriteString("\n")
	return buf.Bytes()
}

// migrateSettingsFile applies migrateSettings to the file's bytes: it keeps
// the original in settings.json.bak and writes the migrated object. It
// returns the bytes to decode, which are the migrated ones even when a
// write fails.
func migrateSettingsFile(b []byte) []byte {
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil || !migrateSettings(raw) {
		return b
	}
	out := marshalRawObject(raw)
	path := settingsPath()
	if err := writeFileAtomic(path+".bak", b, 0o600); err != nil {
		log.Printf("gorex settings: backup: %v", err)
	} else if err := writeFileAtomic(path, out, 0o600); err != nil {
		log.Printf("gorex settings: migrate: %v", err)
	}
	return out
}

func loadSettings() {
	if b, err := os.ReadFile(settingsPath()); err == nil {
		json.Unmarshal(migrateSettingsFile(b), &prefs)
	}
	// A size out of range counts as none: Ghostty's font-size, or the default.
	if prefs.FontSize < 6 || prefs.FontSize > 40 {
		prefs.FontSize = 0
	}
	if !slices.Contains(placements, prefs.TabPlacement) {
		prefs.TabPlacement = ""
	}
	if prefs.Ambience != ambienceStill && prefs.Ambience != ambienceOff {
		prefs.Ambience = ""
	}
	if prefs.Mascot != mascotOff {
		prefs.Mascot = ""
	}
	if !slices.Contains(animations, prefs.Animation) {
		prefs.Animation = ""
	}
	if prefs.TermOpacity < minTermOpacity || prefs.TermOpacity > maxTermOpacity {
		prefs.TermOpacity = 0
	}
	// A ratio is clamped to 1–21 as Ghostty's is; 0 (or NaN) follows Ghostty.
	if c := prefs.TermMinContrast; c != c {
		prefs.TermMinContrast = 0
	} else if c != 0 {
		prefs.TermMinContrast = min(max(c, 1), 21)
	}
	if prefs.SidebarWidth < sidebarMinW || prefs.SidebarWidth > sidebarMaxW {
		prefs.SidebarWidth = 0
	}
	// The sidebar style metrics keep 0 for the default; a value outside its
	// range falls back to it, as the other metrics do.
	prefs.SidebarRowHeight = clampMetric(prefs.SidebarRowHeight, 24, 48)
	prefs.SidebarIndent = clampMetric(prefs.SidebarIndent, 0, 24)
	prefs.SidebarFontSize = clampMetric(prefs.SidebarFontSize, 10, 16)
	prefs.SidebarGroupPad = clampMetric(prefs.SidebarGroupPad, 0, 12)
	if prefs.SidebarGroupBy != "host" && prefs.SidebarGroupBy != "flat" {
		prefs.SidebarGroupBy = ""
	}
	if prefs.SidebarIcons != "tiles" && prefs.SidebarIcons != "text" {
		prefs.SidebarIcons = ""
	}
	if prefs.SidebarSelection != "pill" && prefs.SidebarSelection != "fill" {
		prefs.SidebarSelection = ""
	}
	checkSidebarColors()
	_ = loadTheme()
	// A language of the file may be spelled otherwise (zh-hans, zh-CN): keep
	// the app's own spelling of the one it matches, or none.
	if prefs.Language != "" {
		prefs.Language = i18n.Match([]string{prefs.Language}, mygo.App.Languages())
	}
	loadGhostty()
	applyGhostty()
	applyAppearance()
	// The file is the one place that says the language: "" follows the
	// system's, whatever the app kept before.
	setLocale(prefs.Language)
}

// loadTheme applies the theme prefs.Theme names, or the built-in palettes,
// and returns why a theme does not load. A theme that cannot load leaves
// the built-in ones; a bad seed or role in it is dropped. Either way the app
// goes on, and the log says why.
func loadTheme() error {
	applyTheme(nil)
	if prefs.Theme == "" {
		return nil
	}
	path, err := theme.Path(theme.Dir(config.Dir()), prefs.Theme)
	if err != nil {
		log.Printf("gorex settings: appearance.theme: %v", err)
		return err
	}
	m, warn, err := theme.Load(path)
	if err != nil {
		log.Printf("gorex settings: appearance.theme: %v", err)
		return err
	}
	for _, w := range warn {
		log.Printf("gorex settings: appearance.theme %q: %s (dropped)", prefs.Theme, w)
	}
	applyTheme(m)
	loadArt(prefs.Theme, m)
	return nil
}

// clampMetric zeroes a metric outside lo–hi, so 0 keeps meaning "the
// default" and an out-of-range value falls back to it.
func clampMetric(v, lo, hi float32) float32 {
	if v < lo || v > hi {
		return 0
	}
	return v
}

func saveSettings() {
	b, _ := json.MarshalIndent(prefs, "", "  ")
	os.MkdirAll(rex.Dir(), 0o700)
	if err := writeFileAtomic(settingsPath(), b, 0o600); err != nil {
		log.Printf("gorex settings: %v", err)
	}
}

// writeFileAtomic writes data to path through a file beside it, renamed
// over it: a crash leaves the old content or the new, never part of it.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), perm)
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

// setThemeSource sets the windows' theme; tests, which run no windows, take
// its place.
var setThemeSource = mygo.Theme.SetSource

// setLocale shows another language; tests, which run no app, take its place.
var setLocale = mygo.App.SetLocale

// applyAppearance sets the windows' appearance: the user's choice, else the
// one the terminals' colors call for, else the system's.
func applyAppearance() {
	v := prefs.Appearance
	if v == "" {
		v = termAppearance
	}
	switch v {
	case "light":
		setThemeSource(mygo.ThemeLight)
	case "dark":
		setThemeSource(mygo.ThemeDark)
	default:
		setThemeSource(mygo.ThemeSystem)
	}
}

// cursorStyles are the names of the cursor styles, as settings.json has them.
var cursorStyles = map[string]terminal.CursorStyle{
	"block": terminal.CursorBlock, "bar": terminal.CursorBar, "underline": terminal.CursorUnderline,
}

// withPrefs returns the configuration with the user's choices over it, and
// GoRex's defaults where neither sets one. The configuration is Ghostty's
// as it was read, so a reload does not lose them.
// A theme of the user's replaces the configuration's colors too, as they
// were meant for the other theme. With none, the window theme's terminal
// themes do the same (themeTerm).
func (c ghosttyConfig) withPrefs(p settings) ghosttyConfig {
	if p.TermFont != "" {
		c.Families = []string{p.TermFont}
	}
	term := p.TermTheme
	if term == "" {
		term = themeTermPair()
	}
	if term != "" {
		c.Theme = term
		c.Background, c.Foreground, c.CursorColor, c.CursorText = nil, nil, nil, nil
		c.SelectionBg, c.SelectionFg, c.Palette = nil, nil, nil
	}
	if s, ok := cursorStyles[p.TermCursor]; ok {
		c.Cursor = s
	}
	if p.TermCursorBlink != nil {
		c.NoBlink = !*p.TermCursorBlink
	}
	if p.TermCopyOnSelect != nil {
		c.CopyOnSelect = terminal.CopyOnSelectNone
		if *p.TermCopyOnSelect {
			c.CopyOnSelect = terminal.CopyOnSelectClipboard
		}
	}
	if p.TermMinContrast != 0 {
		c.MinimumContrast = p.TermMinContrast
	}
	if c.MinimumContrast == 0 {
		c.MinimumContrast = defaultMinContrast
	}
	return c
}

// applyPrefs makes the configuration, the user's choices over it, current
// in every pane of every window. The cursor is a pane's own once made, so
// it is applied by setCursor, not here.
func (a *App) applyPrefs() {
	ghostty = ghosttyRaw.withPrefs(prefs)
	applyGhostty()
	applyAppearance()
	for _, p := range a.allPanes() {
		if p.term != nil {
			p.term.SetFont(termFont)
			p.term.SetTheme(termLight, termDark)
			p.term.SetCopyOnSelect(ghostty.CopyOnSelect)
			p.term.SetMinimumContrast(ghostty.MinimumContrast)
		}
	}
}

// allPanes are the panes of every window.
func (a *App) allPanes() []*Pane {
	var out []*Pane
	for _, o := range a.peers() {
		for _, t := range o.tabs {
			out = append(out, t.allPanes()...)
		}
	}
	return out
}

// setPref changes the user's choices, keeps them, and applies them.
func (a *App) setPref(change func(*settings)) {
	change(&prefs)
	saveSettings()
	a.applyPrefs()
}

// cursorSeq is the sequence (DECSCUSR) that sets a cursor.
func cursorSeq(s terminal.CursorStyle, blink bool) string {
	n := map[terminal.CursorStyle]int{terminal.CursorBlock: 1, terminal.CursorUnderline: 3, terminal.CursorBar: 5}[s]
	if !blink {
		n++
	}
	return fmt.Sprintf("[%d q", n)
}

// setCursor changes the cursor of every pane, which the terminal has no
// call for, with the sequence a program would use.
func (a *App) setCursor(change func(*settings)) {
	a.setPref(change)
	seq := []byte(cursorSeq(ghostty.Cursor, !ghostty.NoBlink))
	for _, p := range a.allPanes() {
		if p.term != nil {
			p.term.Feed(seq)
		}
	}
}

func (a *App) setAppearance(v string) {
	prefs.Appearance = v
	applyAppearance()
	saveSettings()
}

// setLanguage shows the language of tag, "" for the system's. The app keeps
// it too (SetLocale), and says the menus again as it hears the change.
func (a *App) setLanguage(tag string) {
	prefs.Language = tag
	saveSettings()
	setLocale(tag)
}

// setWindowPref changes a choice of how the windows lay out, keeps it, and
// draws them again.
func (a *App) setWindowPref(change func(*settings)) {
	change(&prefs)
	saveSettings()
	a.syncBadge()
	a.syncSidebarItem()
	for _, o := range a.peers() {
		o.do(func() {})
	}
}

// setGlass turns the glass on or off, keeps it, and shows it in every
// window.
func (a *App) setGlass(on bool) {
	a.setWindowPref(func(s *settings) { s.NoGlass = !on })
	a.applyGlass()
}

// setFontSize changes the size of the terminals' text, in every pane. Zero
// goes back to the default size, which Ghostty's font-size sets.
func (a *App) setFontSize(size float32) {
	if size > 0 {
		size = min(max(size, minTextSize), maxTextSize)
	}
	prefs.FontSize = size
	termFont.Size = ghostty.fontSize(size)
	for _, p := range a.allPanes() {
		if p.term != nil {
			p.term.SetFont(termFont)
		}
	}
	saveSettings()
}
