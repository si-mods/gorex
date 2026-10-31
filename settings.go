package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/plugins/terminal"

	"gorex/internal/rex"
)

// settings are the user's choices, kept in settings.json beside the
// server's state.
type settings struct {
	// Appearance is "light", "dark", or "" to follow the system.
	Appearance string  `json:"appearance,omitempty"`
	FontSize   float32 `json:"fontSize,omitempty"`
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
	// Animation is how the panes and the sidebar move: "" springs snappily,
	// "natural" more softly, and "off" not at all.
	Animation string `json:"animation,omitempty"`

	// The rest, when set, win over Ghostty's configuration, which wins over
	// GoRex's defaults. Empty and nil follow Ghostty.
	Font         string `json:"font,omitempty"`   // a font family
	Theme        string `json:"theme,omitempty"`  // a Ghostty theme's name
	Cursor       string `json:"cursor,omitempty"` // "block", "bar" or "underline"
	CursorBlink  *bool  `json:"cursorBlink,omitempty"`
	CopyOnSelect *bool  `json:"copyOnSelect,omitempty"`
}

const defaultFontSize = 11.6

var prefs settings

func settingsPath() string { return filepath.Join(rex.Dir(), "settings.json") }

func loadSettings() {
	if b, err := os.ReadFile(settingsPath()); err == nil {
		json.Unmarshal(b, &prefs)
	}
	// A size out of range counts as none: Ghostty's font-size, or the default.
	if prefs.FontSize < 6 || prefs.FontSize > 40 {
		prefs.FontSize = 0
	}
	if !slices.Contains(placements, prefs.TabPlacement) {
		prefs.TabPlacement = ""
	}
	if !slices.Contains(animations, prefs.Animation) {
		prefs.Animation = ""
	}
	if prefs.SidebarWidth < sidebarMinW || prefs.SidebarWidth > sidebarMaxW {
		prefs.SidebarWidth = 0
	}
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

func saveSettings() {
	b, _ := json.MarshalIndent(prefs, "", "  ")
	os.MkdirAll(rex.Dir(), 0o700)
	os.WriteFile(settingsPath(), b, 0o600)
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

// withPrefs returns the configuration with the user's choices over it. The
// configuration is Ghostty's as it was read, so a reload does not lose them.
// A theme of the user's replaces the configuration's colors too, as they
// were meant for the other theme.
func (c ghosttyConfig) withPrefs(p settings) ghosttyConfig {
	if p.Font != "" {
		c.Families = []string{p.Font}
	}
	if p.Theme != "" {
		c.Theme = p.Theme
		c.Background, c.Foreground, c.CursorColor, c.CursorText = nil, nil, nil, nil
		c.SelectionBg, c.SelectionFg, c.Palette = nil, nil, nil
	}
	if s, ok := cursorStyles[p.Cursor]; ok {
		c.Cursor = s
	}
	if p.CursorBlink != nil {
		c.NoBlink = !*p.CursorBlink
	}
	if p.CopyOnSelect != nil {
		c.CopyOnSelect = terminal.CopyOnSelectNone
		if *p.CopyOnSelect {
			c.CopyOnSelect = terminal.CopyOnSelectClipboard
		}
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
	a.syncSidebarItem()
	for _, o := range a.peers() {
		o.do(func() {})
	}
}

// setFontSize changes the size of the terminals' text, in every pane. Zero
// goes back to the default size, which Ghostty's font-size sets.
func (a *App) setFontSize(size float32) {
	if size > 0 {
		size = min(max(size, 8), 32)
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
