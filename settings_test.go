package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// isolateSettings gives a test settings of its own, and a Ghostty
// configuration it can write, and restores the globals after it.
func isolateSettings(t *testing.T, config string) (write func(string)) {
	t.Helper()
	oldPrefs, oldRaw, oldGhostty, oldFont := prefs, ghosttyRaw, ghostty, termFont
	t.Cleanup(func() { prefs, ghosttyRaw, ghostty, termFont = oldPrefs, oldRaw, oldGhostty, oldFont })
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	path := filepath.Join(dir, "ghostty")
	t.Setenv("GOREX_GHOSTTY_CONFIG", path)
	prefs = settings{}
	oldSource := setThemeSource
	setThemeSource = func(mygo.ThemeSource) {}
	t.Cleanup(func() { setThemeSource = oldSource })
	write = func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(config)
	return write
}

func TestWithPrefsPrecedence(t *testing.T) {
	bg := ui.Hex("#102030")
	raw := ghosttyConfig{
		Families: []string{"Fira Code"}, Theme: "Dracula", Background: &bg,
		Cursor: terminal.CursorBar, NoBlink: true, CopyOnSelect: terminal.CopyOnSelectClipboard,
	}
	// No choices: Ghostty's config as it is.
	if got := raw.withPrefs(settings{}); !reflect.DeepEqual(got, raw) {
		t.Errorf("no prefs changed the config: %+v", got)
	}
	yes, no := true, false
	got := raw.withPrefs(settings{
		Font: "Cascadia Mono", Theme: "Nord", Cursor: "underline", CursorBlink: &yes, CopyOnSelect: &no,
	})
	if !reflect.DeepEqual(got.Families, []string{"Cascadia Mono"}) || got.Theme != "Nord" ||
		got.Cursor != terminal.CursorUnderline || got.NoBlink || got.CopyOnSelect != terminal.CopyOnSelectNone {
		t.Errorf("prefs did not win: %+v", got)
	}
	if got.Background != nil {
		t.Error("the config's background stayed over the user's theme")
	}
	// A false choice is a choice: blink off wins over Ghostty's blinking.
	if got := (ghosttyConfig{}).withPrefs(settings{CursorBlink: &no}); !got.NoBlink {
		t.Error("blink off was lost")
	}
	// The raw configuration is not touched, nor is an unknown cursor taken.
	if raw.Theme != "Dracula" || raw.Background == nil || raw.Families[0] != "Fira Code" {
		t.Error("withPrefs changed the raw config")
	}
	if got := raw.withPrefs(settings{Cursor: "wavy"}); got.Cursor != terminal.CursorBar {
		t.Error("an unknown cursor style was taken")
	}
}

// TestReloadKeepsPrefs checks that reloading Ghostty's config takes its
// changes but not the user's choices from them.
func TestReloadKeepsPrefs(t *testing.T) {
	write := isolateSettings(t, "cursor-style = bar\nfont-family = Fira Code\ncursor-style-blink = false\n")
	loadGhostty()
	a := &App{}
	a.setCursor(func(s *settings) { s.Cursor = "underline" })
	if ghostty.Cursor != terminal.CursorUnderline || !ghostty.NoBlink {
		t.Fatalf("cursor %v, no blink %v: the pref should win, and Ghostty's blink stay", ghostty.Cursor, ghostty.NoBlink)
	}
	write("cursor-style = block\nfont-family = Hack\ncursor-style-blink = true\n")
	a.reloadGhostty()
	if ghostty.Cursor != terminal.CursorUnderline {
		t.Error("reloading lost the user's cursor")
	}
	if ghostty.NoBlink || ghosttyRaw.Cursor != terminal.CursorBlock {
		t.Error("reloading did not take Ghostty's other changes")
	}
	if !strings.HasPrefix(termFont.Family, "Hack") {
		t.Errorf("font %q: Ghostty's new family was not taken", termFont.Family)
	}
	// A font of the user's wins over the reload too.
	a.setPref(func(s *settings) { s.Font = "Cascadia Mono" })
	a.reloadGhostty()
	if !strings.HasPrefix(termFont.Family, "Cascadia Mono") {
		t.Errorf("font %q: the user's family was lost", termFont.Family)
	}
	// Back to Ghostty's.
	a.setPref(func(s *settings) { s.Font = "" })
	if !strings.HasPrefix(termFont.Family, "Hack") {
		t.Errorf("font %q: clearing the pref did not follow Ghostty", termFont.Family)
	}
}

func TestSettingsPersist(t *testing.T) {
	isolateSettings(t, "")
	if b, _ := json.Marshal(settings{}); string(b) != "{}" {
		t.Errorf("zero settings wrote %s, not {}", b)
	}
	yes := true
	want := settings{Appearance: "dark", FontSize: 14, Font: "Hack", Theme: "Nord", Cursor: "bar", CursorBlink: &yes}
	prefs = want
	saveSettings()
	prefs = settings{}
	loadSettings()
	if !reflect.DeepEqual(prefs, want) {
		t.Errorf("loaded %+v, saved %+v", prefs, want)
	}
	if ghostty.Cursor != terminal.CursorBar || ghostty.Theme != "Nord" {
		t.Error("loading did not put the choices over Ghostty's config")
	}
	// A size out of range counts as none.
	os.WriteFile(settingsPath(), []byte(`{"fontSize": 99}`), 0o600)
	prefs = settings{}
	loadSettings()
	if prefs.FontSize != 0 {
		t.Errorf("size %v out of range was kept", prefs.FontSize)
	}
}

func TestUserThemeWinsOverGhosttyColors(t *testing.T) {
	isolateSettings(t, "theme = Dracula\nbackground = #123456\n")
	loadGhostty()
	applyGhostty()
	if termLight.Background != ui.Hex("#123456") {
		t.Fatal("Ghostty's background was not taken")
	}
	prefs.Theme = "Nord"
	ghostty = ghosttyRaw.withPrefs(prefs)
	applyGhostty()
	nord, err := terminal.GhosttyTheme("Nord")
	if err != nil {
		t.Skip(err)
	}
	if termLight.Background != nord.Background {
		t.Error("Ghostty's background stayed over the user's theme")
	}
}

func TestShortcutRows(t *testing.T) {
	rows := shortcutRows()
	keys := map[string]string{}
	for _, r := range rows {
		if r.Title == "" || r.Keys == "" {
			t.Errorf("empty row %+v", r)
		}
		keys[r.Title] = r.Keys
	}
	for _, want := range []string{"New Tab", "Settings…", "Show Tab 1…9"} {
		if keys[want] == "" {
			t.Errorf("no row for %s", want)
		}
	}
	if keys["Settings…"] != cmdSettings.Keys {
		t.Errorf("settings keys %q, the command's %q", keys["Settings…"], cmdSettings.Keys)
	}
}

func TestCursorSeq(t *testing.T) {
	for _, c := range []struct {
		s     terminal.CursorStyle
		blink bool
		want  string
	}{
		{terminal.CursorBlock, true, "\x1b[1 q"}, {terminal.CursorBlock, false, "\x1b[2 q"},
		{terminal.CursorUnderline, true, "\x1b[3 q"}, {terminal.CursorBar, false, "\x1b[6 q"},
	} {
		if got := cursorSeq(c.s, c.blink); got != c.want {
			t.Errorf("cursorSeq(%v, %v) = %q, want %q", c.s, c.blink, got, c.want)
		}
	}
}

func TestThemeNames(t *testing.T) {
	all := []string{"Dracula", "Nord", "Nord Light", "Rose Pine"}
	if got := themeNames(all, " nord "); !reflect.DeepEqual(got, []string{"Nord", "Nord Light"}) {
		t.Errorf("got %v", got)
	}
	if got := themeNames(all, ""); len(got) != len(all) {
		t.Errorf("an empty query listed %v", got)
	}
}
