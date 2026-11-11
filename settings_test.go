package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	oldLocale := setLocale
	setLocale = func(string) {}
	t.Cleanup(func() { setLocale = oldLocale })
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
		MinimumContrast: 4.5,
	}
	// No choices: Ghostty's config as it is (here with a minimum contrast,
	// which GoRex's default would fill in otherwise).
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

// TestMinimumContrastPrecedence checks that the user's minimum contrast
// wins over Ghostty's, which wins over GoRex's default, and that 1 stays
// off.
func TestMinimumContrastPrecedence(t *testing.T) {
	for _, tc := range []struct {
		ghostty, pref, want float32
	}{
		{0, 0, defaultMinContrast},
		{7, 0, 7},
		{7, 2, 2},
		{0, 1, 1},
		{1, 0, 1},
	} {
		got := ghosttyConfig{MinimumContrast: tc.ghostty}.withPrefs(settings{MinimumContrast: tc.pref})
		if got.MinimumContrast != tc.want {
			t.Errorf("ghostty %v, pref %v: %v, want %v", tc.ghostty, tc.pref, got.MinimumContrast, tc.want)
		}
	}
	// A ratio out of range in settings.json is clamped, as Ghostty's is.
	isolateSettings(t, "")
	if err := os.MkdirAll(filepath.Dir(settingsPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	for raw, want := range map[string]float32{
		`{"minimumContrast": 0.5}`: 1, `{"minimumContrast": 22}`: 21, `{"minimumContrast": 1}`: 1, `{}`: defaultMinContrast,
	} {
		if err := os.WriteFile(settingsPath(), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		prefs = settings{}
		loadSettings()
		if ghostty.MinimumContrast != want {
			t.Errorf("%s: applied %v, want %v", raw, ghostty.MinimumContrast, want)
		}
	}
}

func TestSettingsPersist(t *testing.T) {
	isolateSettings(t, "")
	if b, _ := json.Marshal(settings{}); string(b) != "{}" {
		t.Errorf("zero settings wrote %s, not {}", b)
	}
	yes := true
	want := settings{Appearance: "dark", FontSize: 14, Font: "Hack", Theme: "Nord", Cursor: "bar", CursorBlink: &yes,
		TabPlacement: "left", SidebarWidth: 280, SidebarHidden: true, HidePaneHeaders: true, SidebarView: "host", Animation: "natural"}
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
	// An animation style of none of the options is Snappy's.
	os.WriteFile(settingsPath(), []byte(`{"animation": "bouncy"}`), 0o600)
	prefs = settings{}
	loadSettings()
	if prefs.Animation != "" {
		t.Errorf("unknown animation style %q was kept", prefs.Animation)
	}
}

// TestTabPlacementSettings checks that settings from before the sidebar
// put the tabs above, and that unknown placements and widths out of range
// count as none.
func TestTabPlacementSettings(t *testing.T) {
	isolateSettings(t, "")
	os.WriteFile(settingsPath(), []byte(`{"appearance": "dark", "fontSize": 14}`), 0o600)
	loadSettings()
	if prefs.TabPlacement != "" || prefs.SidebarHidden || prefs.HidePaneHeaders || sidebarWidth() != sidebarDefaultW {
		t.Errorf("an old settings file loaded %+v, not tabs above", prefs)
	}
	if !paneHeaders() {
		t.Error("pane headers hidden by default")
	}
	os.WriteFile(settingsPath(), []byte(`{"tabPlacement": "bottom", "sidebarWidth": 9999, "sidebarView": "tree"}`), 0o600)
	prefs = settings{}
	loadSettings()
	if prefs.TabPlacement != "" || prefs.SidebarWidth != 0 || prefs.SidebarView != "" {
		t.Errorf("kept placement %q, width %v, view %q", prefs.TabPlacement, prefs.SidebarWidth, prefs.SidebarView)
	}
	// Coming back to a sidebar from above shows it; moving it does not.
	prefs = settings{SidebarHidden: true}
	setTabPlacement(&prefs, "left")
	if prefs.TabPlacement != "left" || prefs.SidebarHidden {
		t.Errorf("from above to the left: %+v", prefs)
	}
	prefs.SidebarHidden = true
	setTabPlacement(&prefs, "right")
	if !prefs.SidebarHidden {
		t.Error("moving the hidden sidebar to the right showed it")
	}
	if w := float32(560); sidebarMax(w) != w-panesMinW || sidebarMax(300) != sidebarMinW || sidebarMax(2000) != sidebarMaxW {
		t.Errorf("sidebar maxima %v %v %v", sidebarMax(560), sidebarMax(300), sidebarMax(2000))
	}

	// Pane headers hide only beside a left sidebar that shows.
	for _, c := range []struct {
		place  string
		hidden bool
		want   bool
	}{{"", false, true}, {"left", false, false}, {"left", true, true}, {"right", false, true}} {
		prefs = settings{TabPlacement: c.place, SidebarHidden: c.hidden, HidePaneHeaders: true}
		if got := paneHeaders(); got != c.want {
			t.Errorf("placement %q, sidebar hidden %v: headers %v, want %v", c.place, c.hidden, got, c.want)
		}
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

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	os.WriteFile(path, []byte("old"), 0o600)
	if err := writeFileAtomic(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Errorf("file has %q, want new", b)
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Errorf("dir has %d files, want the one", len(es))
	}
	if runtime.GOOS == "windows" {
		return // a directory's mode does not stop a write there
	}
	// A write that fails leaves the old content.
	os.Chmod(dir, 0o500)
	defer os.Chmod(dir, 0o700)
	if writeFileAtomic(path, []byte("newer"), 0o644) == nil {
		t.Skip("the directory is writable all the same (root)")
	}
	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Errorf("a failed write left %q, want new", b)
	}
}
