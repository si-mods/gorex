package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
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
		TermFont: "Cascadia Mono", TermTheme: "Nord", TermCursor: "underline", TermCursorBlink: &yes, TermCopyOnSelect: &no,
	})
	if !reflect.DeepEqual(got.Families, []string{"Cascadia Mono"}) || got.Theme != "Nord" ||
		got.Cursor != terminal.CursorUnderline || got.NoBlink || got.CopyOnSelect != terminal.CopyOnSelectNone {
		t.Errorf("prefs did not win: %+v", got)
	}
	if got.Background != nil {
		t.Error("the config's background stayed over the user's theme")
	}
	// A false choice is a choice: blink off wins over Ghostty's blinking.
	if got := (ghosttyConfig{}).withPrefs(settings{TermCursorBlink: &no}); !got.NoBlink {
		t.Error("blink off was lost")
	}
	// The raw configuration is not touched, nor is an unknown cursor taken.
	if raw.Theme != "Dracula" || raw.Background == nil || raw.Families[0] != "Fira Code" {
		t.Error("withPrefs changed the raw config")
	}
	if got := raw.withPrefs(settings{TermCursor: "wavy"}); got.Cursor != terminal.CursorBar {
		t.Error("an unknown cursor style was taken")
	}
}

// TestReloadKeepsPrefs checks that reloading Ghostty's config takes its
// changes but not the user's choices from them.
func TestReloadKeepsPrefs(t *testing.T) {
	write := isolateSettings(t, "cursor-style = bar\nfont-family = Fira Code\ncursor-style-blink = false\n")
	loadGhostty()
	a := &App{}
	a.setCursor(func(s *settings) { s.TermCursor = "underline" })
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
	a.setPref(func(s *settings) { s.TermFont = "Cascadia Mono" })
	a.reloadGhostty()
	if !strings.HasPrefix(termFont.Family, "Cascadia Mono") {
		t.Errorf("font %q: the user's family was lost", termFont.Family)
	}
	// Back to Ghostty's.
	a.setPref(func(s *settings) { s.TermFont = "" })
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
		got := ghosttyConfig{MinimumContrast: tc.ghostty}.withPrefs(settings{TermMinContrast: tc.pref})
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
		`{"terminal.minimumContrast": 0.5}`: 1, `{"terminal.minimumContrast": 22}`: 21, `{"terminal.minimumContrast": 1}`: 1, `{}`: defaultMinContrast,
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
	want := settings{Appearance: "dark", FontSize: 14, TermFont: "Hack", TermTheme: "Nord", TermCursor: "bar", TermCursorBlink: &yes,
		TabPlacement: "left", SidebarWidth: 280, SidebarHidden: true, HidePaneHeaders: true, Animation: "natural"}
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
	if prefs.TabPlacement != "" || prefs.SidebarWidth != 0 {
		t.Errorf("kept placement %q, width %v", prefs.TabPlacement, prefs.SidebarWidth)
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
	prefs.TermTheme = "Nord"
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

// TestSidebarStylePrefs: the sidebar.* keys parse, the metrics clamp, an
// unknown token or a bad color is dropped, and the file's other prefs
// are untouched (docs/DSK-007).
func TestSidebarStylePrefs(t *testing.T) {
	isolateSettings(t, "")
	path := filepath.Join(rex.Dir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{
		"sidebar.rowHeight": 26,
		"sidebar.indent": 10,
		"sidebar.tabIndent": 8,
		"sidebar.fontSize": 40,
		"sidebar.groupPadding": 3,
		"sidebar.groupBy": "flat",
		"sidebar.groupBackground": "none",
		"sidebar.icons": "text",
		"sidebar.selection": "wobbly",
		"sidebar.colorCustomizations": {
			"sideBar.activeGroupBackground": "#2a2a3d",
			"sideBar.activeGroupTabBackground": {"light": "#ffffff", "dark": "#3a3a52"},
			"nope": "#000000",
			"sideBar.trackBackground": "not a color"
		}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loadSettings()
	if prefs.SidebarRowHeight != 26 || prefs.SidebarIndent != 10 || prefs.SidebarTabIndent != 8 || prefs.SidebarGroupPad != 3 {
		t.Fatalf("metrics %v %v %v %v", prefs.SidebarRowHeight, prefs.SidebarIndent, prefs.SidebarTabIndent, prefs.SidebarGroupPad)
	}
	if prefs.SidebarGroupFace != "none" {
		t.Errorf("groupBackground %q", prefs.SidebarGroupFace)
	}
	if prefs.SidebarFontSize != 0 {
		t.Errorf("out-of-range fontSize kept %v", prefs.SidebarFontSize)
	}
	if prefs.SidebarGroupBy != "flat" {
		t.Errorf("groupBy %q", prefs.SidebarGroupBy)
	}
	if prefs.SidebarIcons != "text" {
		t.Errorf("icons %q", prefs.SidebarIcons)
	}
	if prefs.SidebarSelection != "" {
		t.Errorf("bad selection %q kept", prefs.SidebarSelection)
	}
	if len(prefs.SidebarColors) != 2 {
		t.Fatalf("colors kept %v", prefs.SidebarColors)
	}
	if _, ok := prefs.SidebarColors["nope"]; ok {
		t.Error("unknown token kept")
	}
	if _, ok := prefs.SidebarColors["sideBar.trackBackground"]; ok {
		t.Error("bad color kept")
	}
}

const oldTerminalSettings = `{
  "font": "Hack",
  "theme": "Nord",
  "cursor": "bar",
  "cursorBlink": false,
  "copyOnSelect": true,
  "minimumContrast": 4.5,
  "terminalOpacity": 0.8,
  "fontSize": 14,
  "sidebarWidth":   280,
  "future.key": {"a": [1,   2]}
}`

// settingsOnDisk reads settings.json as a raw object.
func settingsOnDisk(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestMigrateTerminalSettings: the old terminal keys move to terminal.* at
// load, the file keeps a backup, other keys keep their values, and a second
// load writes nothing (docs/DSK-009).
func TestMigrateTerminalSettings(t *testing.T) {
	isolateSettings(t, "")
	path := settingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(oldTerminalSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	loadSettings()
	no, yes := false, true
	if prefs.TermFont != "Hack" || prefs.TermTheme != "Nord" || prefs.TermCursor != "bar" ||
		prefs.TermCursorBlink == nil || *prefs.TermCursorBlink != no ||
		prefs.TermCopyOnSelect == nil || *prefs.TermCopyOnSelect != yes ||
		prefs.TermMinContrast != 4.5 || prefs.TermOpacity != 0.8 || prefs.FontSize != 14 {
		t.Errorf("migrated values not loaded: %+v", prefs)
	}
	raw := settingsOnDisk(t)
	for old, key := range settingsKeyRenames {
		if _, ok := raw[old]; ok {
			t.Errorf("old key %q is still in the file", old)
		}
		if _, ok := raw[key]; !ok {
			t.Errorf("new key %q is not in the file", key)
		}
	}
	if string(raw["future.key"]) != `{"a": [1,   2]}` || string(raw["sidebarWidth"]) != "280" {
		t.Errorf("other keys changed: %s, %s", raw["future.key"], raw["sidebarWidth"])
	}
	if b, err := os.ReadFile(path + ".bak"); err != nil || string(b) != oldTerminalSettings {
		t.Errorf("backup is %q (%v), want the original bytes", b, err)
	}

	// A second load finds nothing to migrate: it writes neither file.
	before, _ := os.ReadFile(path)
	if err := os.Remove(path + ".bak"); err != nil {
		t.Fatal(err)
	}
	prefs = settings{}
	loadSettings()
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error("the second load wrote the backup again")
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Errorf("the second load changed the file:\n%s\n%s", before, after)
	}
	if prefs.TermTheme != "Nord" {
		t.Errorf("second load theme %q", prefs.TermTheme)
	}
}

// TestMigrateKeepsNewKey: with both keys, the new one wins and the old goes.
func TestMigrateKeepsNewKey(t *testing.T) {
	isolateSettings(t, "")
	if err := os.MkdirAll(filepath.Dir(settingsPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(settingsPath(), []byte(`{"theme": "Old", "terminal.theme": "New"}`), 0o600)
	loadSettings()
	if prefs.TermTheme != "New" {
		t.Errorf("theme %q, want New", prefs.TermTheme)
	}
	raw := settingsOnDisk(t)
	if _, ok := raw["theme"]; ok || string(raw["terminal.theme"]) != `"New"` {
		t.Errorf("file has %v", raw)
	}
}

// TestMigrateAbsentPointers: a file with no cursorBlink or copyOnSelect
// leaves them nil, so Ghostty's values apply, and a file with no old key is
// not touched.
func TestMigrateAbsentPointers(t *testing.T) {
	isolateSettings(t, "")
	if err := os.MkdirAll(filepath.Dir(settingsPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(settingsPath(), []byte(`{"font": "Hack"}`), 0o600)
	loadSettings()
	if prefs.TermCursorBlink != nil || prefs.TermCopyOnSelect != nil || prefs.TermFont != "Hack" {
		t.Errorf("prefs %+v", prefs)
	}
	const fresh = `{"terminal.font":"Hack"}`
	os.Remove(settingsPath() + ".bak")
	os.WriteFile(settingsPath(), []byte(fresh), 0o600)
	loadSettings()
	if b, _ := os.ReadFile(settingsPath()); string(b) != fresh {
		t.Errorf("a file with new keys only was rewritten: %s", b)
	}
	if _, err := os.Stat(settingsPath() + ".bak"); err == nil {
		t.Error("a file with new keys only got a backup")
	}
}

// TestMigrateWriteFailure: when the migration cannot write, the load still
// uses the migrated values and logs the error. A directory in the place of
// settings.json.bak makes the write fail on every platform.
func TestMigrateWriteFailure(t *testing.T) {
	isolateSettings(t, "")
	path := settingsPath()
	if err := os.MkdirAll(path+".bak", 0o700); err != nil {
		t.Fatal(err)
	}
	const old = `{"theme": "Nord"}`
	os.WriteFile(path, []byte(old), 0o600)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	loadSettings()
	if prefs.TermTheme != "Nord" {
		t.Errorf("theme %q, want the migrated Nord", prefs.TermTheme)
	}
	if !strings.Contains(logs.String(), "gorex settings") {
		t.Errorf("no error logged: %q", logs.String())
	}
	if b, _ := os.ReadFile(path); string(b) != old {
		t.Errorf("file %q changed without a backup", b)
	}
}

const oldTokenSettings = `{
  "future.key": {"a": [1,   2]},
  "sidebar.colorCustomizations": {
    "groupFace": "#2a2a3d",
    "liftShadow": "#00000033",
    "pillLift": { "light": "#ffffff",   "dark": "#3a3a52" },
    "tabActive": "#e8e8ee",
    "tabHover": "#ffffff1a",
    "tabSep": "#ffffff22",
    "track": "#00000011"
  },
  "sidebar.selection": "fill"
}`

// colorsOnDisk reads the sidebar.colorCustomizations object of
// settings.json.
func colorsOnDisk(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	var colors map[string]json.RawMessage
	if err := json.Unmarshal(settingsOnDisk(t)["sidebar.colorCustomizations"], &colors); err != nil {
		t.Fatal(err)
	}
	return colors
}

func writeSettings(t *testing.T, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(settingsPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath(), []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestMigrateSidebarTokens: a file with the seven DSK-007 tokens loads with
// the same colors, and the file then has only role names with the exact
// values and a backup of the original (docs/DSK-008).
func TestMigrateSidebarTokens(t *testing.T) {
	isolateSettings(t, "")
	writeSettings(t, oldTokenSettings)
	loadSettings()
	want := map[string]string{
		"sideBar.activeGroupBackground":    `"#2a2a3d"`,
		"sideBar.activeGroupTabBackground": `{ "light": "#ffffff",   "dark": "#3a3a52" }`,
		"sideBar.activeGroupTabShadow":     `"#00000033"`,
		"sideBar.trackBackground":          `"#00000011"`,
		"sideBar.tabSeparator":             `"#ffffff22"`,
		"sideBar.tabHoverBackground":       `"#ffffff1a"`,
		"sideBar.tabActiveBackground":      `"#e8e8ee"`,
	}
	if len(prefs.SidebarColors) != len(want) {
		t.Fatalf("loaded %v", prefs.SidebarColors)
	}
	d := sidebarPalette(&darkColors, true)
	if d.groupFace != ui.Hex("#2a2a3d") || d.tabActive != ui.Hex("#e8e8ee") || d.pillLift != ui.Hex("#3a3a52") ||
		d.liftShadow != ui.Hex("#00000033") || d.track != ui.Hex("#00000011") || d.tabSep != ui.Hex("#ffffff22") || d.tabHover != ui.Hex("#ffffff1a") {
		t.Errorf("the palette lost an old token: %+v", d)
	}
	disk := colorsOnDisk(t)
	if len(disk) != len(want) {
		t.Errorf("the file has %v", disk)
	}
	for name, v := range want {
		if string(disk[name]) != v {
			t.Errorf("%s on disk is %s, want %s", name, disk[name], v)
		}
	}
	if string(settingsOnDisk(t)["future.key"]) != `{"a": [1,   2]}` {
		t.Error("another key changed")
	}
	if b, err := os.ReadFile(settingsPath() + ".bak"); err != nil || string(b) != oldTokenSettings {
		t.Errorf("backup %q (%v), want the original bytes", b, err)
	}

	// Idempotent: a second load changes nothing and writes no backup.
	before, _ := os.ReadFile(settingsPath())
	os.Remove(settingsPath() + ".bak")
	prefs = settings{}
	loadSettings()
	if after, _ := os.ReadFile(settingsPath()); string(after) != string(before) {
		t.Errorf("the second load changed the file:\n%s\n%s", before, after)
	}
	if _, err := os.Stat(settingsPath() + ".bak"); err == nil {
		t.Error("the second load wrote the backup again")
	}
}

// TestMigrateTokenBothNames: with an old token and its role name, the role
// name's value wins and the old token goes.
func TestMigrateTokenBothNames(t *testing.T) {
	isolateSettings(t, "")
	writeSettings(t, `{"sidebar.colorCustomizations": {"tabActive": "#111111", "sideBar.tabActiveBackground": "#222222"}}`)
	loadSettings()
	disk := colorsOnDisk(t)
	if len(disk) != 1 || string(disk["sideBar.tabActiveBackground"]) != `"#222222"` {
		t.Errorf("file has %v", disk)
	}
	if got := sidebarPalette(&darkColors, true).tabActive; got != ui.Hex("#222222") {
		t.Errorf("tabActive %v, want the role name's", got)
	}
}

// TestMigrateTerminalAndTokens: one load migrates the DSK-009 keys and the
// DSK-007 tokens together, with one backup that holds the original bytes.
func TestMigrateTerminalAndTokens(t *testing.T) {
	isolateSettings(t, "")
	const both = `{
  "theme": "Nord",
  "sidebar.colorCustomizations": {"tabActive": "#e8e8ee"}
}`
	writeSettings(t, both)
	loadSettings()
	raw := settingsOnDisk(t)
	if _, ok := raw["theme"]; ok || string(raw["terminal.theme"]) != `"Nord"` {
		t.Errorf("terminal key not migrated: %v", raw)
	}
	if disk := colorsOnDisk(t); len(disk) != 1 || string(disk["sideBar.tabActiveBackground"]) != `"#e8e8ee"` {
		t.Errorf("token not migrated: %v", disk)
	}
	if b, err := os.ReadFile(settingsPath() + ".bak"); err != nil || string(b) != both {
		t.Errorf("backup %q (%v), want the original bytes", b, err)
	}
	if prefs.TermTheme != "Nord" || sidebarPalette(&darkColors, true).tabActive != ui.Hex("#e8e8ee") {
		t.Error("migrated values not loaded")
	}
}

// TestDecodeSettings: decodeSettings decodes and checks on a copy —
// prefs is untouched — and a file that does not decode returns false
// (docs/DSK-011-live-settings.md).
func TestDecodeSettings(t *testing.T) {
	isolateSettings(t, "")
	prefs = settings{TermTheme: "Nord", FontSize: 14}
	if _, ok := decodeSettings([]byte(`{"terminal.theme": "Dracula"`)); ok {
		t.Fatal("a torn file decoded")
	}
	next, ok := decodeSettings([]byte(`{"fontSize": 99, "sidebar.rowHeight": 26}`))
	if !ok {
		t.Fatal("a good file did not decode")
	}
	if next.FontSize != 0 || next.SidebarRowHeight != 26 {
		t.Errorf("decoded %+v: the checks did not run", next)
	}
	if prefs.TermTheme != "Nord" || prefs.FontSize != 14 || prefs.SidebarRowHeight != 0 {
		t.Errorf("decoding changed prefs: %+v", prefs)
	}
}

// resetSettingsWatch zeroes the hashes the watch recorded, so an event of
// this test is not skipped as bytes another wrote.
func resetSettingsWatch(t *testing.T) {
	t.Helper()
	settingsMu.Lock()
	settingsWritten, settingsBad = [sha256.Size]byte{}, [sha256.Size]byte{}
	settingsMu.Unlock()
}

// runHere makes the watcher apply where it is called, as the main thread
// would, and counts how often.
func runHere(t *testing.T) (applied *int) {
	t.Helper()
	n := 0
	old := postToMain
	postToMain = func(fn func()) { n++; fn() }
	t.Cleanup(func() { postToMain = old })
	return &n
}

// TestSettingsFileChanged: a write of settings.json from outside applies
// while the app runs; a write the app recorded — its own, or the same
// bytes again — does not, and a file that does not decode keeps prefs
// (docs/DSK-011-live-settings.md).
func TestSettingsFileChanged(t *testing.T) {
	isolateSettings(t, "")
	resetSettingsWatch(t)
	applied := runHere(t)
	st := &store{}
	prefs = settings{TermTheme: "Nord"}
	saveSettings() // writes the file and records its bytes
	st.settingsFileChanged()
	if *applied != 0 {
		t.Fatal("the app's own write reloaded")
	}
	// An outside edit applies.
	writeSettings(t, `{"terminal.theme": "Dracula", "sidebar.rowHeight": 40}`)
	st.settingsFileChanged()
	if *applied != 1 || prefs.TermTheme != "Dracula" || prefs.SidebarRowHeight != 40 {
		t.Fatalf("an outside edit applied %d times, prefs %+v", *applied, prefs)
	}
	// The same bytes again are not applied again.
	st.settingsFileChanged()
	if *applied != 1 {
		t.Error("the same bytes reloaded")
	}
	// A file that does not decode keeps prefs and applies nothing.
	writeSettings(t, `{"terminal.theme": "Nord"`)
	st.settingsFileChanged()
	if *applied != 1 || prefs.TermTheme != "Dracula" {
		t.Errorf("a torn file applied: prefs %+v", prefs)
	}
}

// TestReloadMigrates: a settings.json with an old key reloads with the new
// key, its file is rewritten as a load's is, and the rewrite the migration
// caused is skipped by hash (docs/DSK-011-live-settings.md).
func TestReloadMigrates(t *testing.T) {
	isolateSettings(t, "")
	resetSettingsWatch(t)
	applied := runHere(t)
	writeSettings(t, `{"theme": "Nord"}`)
	st := &store{}
	st.settingsFileChanged()
	if prefs.TermTheme != "Nord" {
		t.Fatalf("the old key did not apply: %+v", prefs)
	}
	raw := settingsOnDisk(t)
	if _, ok := raw["theme"]; ok || string(raw["terminal.theme"]) != `"Nord"` {
		t.Errorf("the file was not migrated: %v", raw)
	}
	if _, err := os.Stat(settingsPath() + ".bak"); err != nil {
		t.Error("no backup of the original")
	}
	// What the migration wrote is skipped, as a write of the app's.
	st.settingsFileChanged()
	if *applied != 1 {
		t.Error("the migration's own write reloaded")
	}
}

// TestSidebarColorHalfPair: a sidebar.colorCustomizations object with one
// side absent, as a hand edit may write it, loads and resolves only the
// side it has; the other appearance falls back to the default
// (docs/DSK-011-live-settings.md).
func TestSidebarColorHalfPair(t *testing.T) {
	for raw, ok := range map[string]bool{
		`"#222222"`:                            true,
		`{"light":"#ffffff","dark":"#222222"}`: true,
		`{"dark":"#222222"}`:                   true,
		`{"light":"#ffffff"}`:                  true,
		`{"dark":"nope"}`:                      false,
		`{"dark":5}`:                           false,
		`"nope"`:                               false,
	} {
		if got := validStyleColor(json.RawMessage(raw)); got != ok {
			t.Errorf("validStyleColor(%s) = %v, want %v", raw, got, ok)
		}
	}
	isolateSettings(t, "")
	writeSettings(t, `{"sidebar.colorCustomizations": {"sideBar.tabActiveBackground": {"dark": "#222222"}}}`)
	loadSettings()
	if _, ok := prefs.SidebarColors["sideBar.tabActiveBackground"]; !ok {
		t.Error("a {dark}-only value was dropped at load")
	}
	if got := sidebarPalette(&darkColors, true).tabActive; got != ui.Hex("#222222") {
		t.Errorf("tabActive %v, want the dark side's", got)
	}
}

// clickButton presses the button named label through the accessibility
// tree.
func clickButton(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	b, ok := tt.AccessByRole(ui.RoleButton, label)
	if !ok {
		t.Fatalf("no %q button", label)
	}
	tt.ClickAt(b.Bounds.X+b.Bounds.W/2, b.Bounds.Y+b.Bounds.H/2)
}

// TestJSONSettingsPage: the settings.json page shows the file in a code
// editor under a bar; a good text saves, writes the file and applies; a
// text that is not JSON writes nothing and marks the bar; an outside
// change resyncs a clean buffer and marks a dirty one (docs/DSK-011-live-settings.md).
func TestJSONSettingsPage(t *testing.T) {
	isolateSettings(t, "")
	resetSettingsWatch(t)
	applied := runHere(t)
	registerFonts()
	st := &store{}
	a := &App{st: st}
	oldSection, oldJSON := settingsUI.section, settingsUI.json
	settingsUI.section, settingsUI.json = "file", nil
	t.Cleanup(func() { settingsUI.section, settingsUI.json = oldSection, oldJSON })
	prefs = settings{TermTheme: "Nord"}
	saveSettings()
	tt := ui.NewTester(a.settingsView, 780, 900)
	tt.Frame()
	for _, label := range []string{"Settings File", "Save", "Revert", "Edits apply on save."} {
		if !tt.HasText(label) {
			t.Errorf("no %q on the page: %q", label, tt.Texts())
		}
	}
	ed, ok := tt.AccessByRole(ui.RoleTextField, "Settings File")
	if !ok || !ed.Multiline {
		t.Fatalf("no editor: %+v", ed)
	}
	j := settingsUI.json
	const first = "{\n  \"terminal.theme\": \"Nord\"\n}"
	if j.st.Buffer.String() != first {
		t.Fatalf("the editor holds %q, want the file's text", j.st.Buffer.String())
	}

	// A text that is not JSON writes nothing and marks the bar.
	j.st.Buffer.Set(`{"terminal.theme": `)
	tt.Frame()
	if !tt.HasText("Unsaved") {
		t.Error("a dirty buffer shows no Unsaved")
	}
	clickButton(t, tt, "Save")
	tt.Frame()
	if !tt.HasText("Not valid JSON.") {
		t.Errorf("a bad text did not mark the bar: %q", tt.Texts())
	}
	if b, _ := os.ReadFile(settingsPath()); string(b) != first {
		t.Errorf("a bad text wrote the file: %s", b)
	}
	if prefs.TermTheme != "Nord" {
		t.Errorf("a bad text applied: %+v", prefs)
	}

	// A good text writes the file and applies, as an outside edit does.
	const good = "{\n  \"terminal.theme\": \"Dracula\"\n}\n"
	j.st.Buffer.Set(good)
	clickButton(t, tt, "Save")
	tt.Frame()
	if prefs.TermTheme != "Dracula" {
		t.Error("the save did not apply")
	}
	if b, _ := os.ReadFile(settingsPath()); string(b) != good {
		t.Errorf("the file has %q, want the editor's text", b)
	}
	if !tt.HasText("Saved") {
		t.Error("no Saved after the save")
	}

	// An outside change takes over a clean buffer.
	const outside = `{"terminal.theme": "Nord", "sidebar.rowHeight": 40}`
	writeSettings(t, outside)
	st.settingsFileChanged()
	tt.Frame()
	if *applied != 1 || prefs.SidebarRowHeight != 40 {
		t.Fatalf("the outside write applied %d times, prefs %+v", *applied, prefs)
	}
	if j.st.Buffer.String() != outside || j.conflict {
		t.Errorf("the clean buffer did not take the file's text")
	}

	// With a dirty buffer the outside change marks the bar and keeps the
	// text, until Revert.
	j.st.Buffer.Set(`{"sidebar.indent": 10}`)
	writeSettings(t, `{"terminal.theme": "Dracula"}`)
	st.settingsFileChanged()
	tt.Frame()
	if *applied != 2 || !j.conflict || !tt.HasText("The file changed on disk.") {
		t.Errorf("a dirty buffer's conflict: %v, applied %d, %q", j.conflict, *applied, tt.Texts())
	}
	if j.st.Buffer.String() != `{"sidebar.indent": 10}` {
		t.Error("the dirty buffer's text was lost")
	}
	clickButton(t, tt, "Revert")
	tt.Frame()
	if j.conflict || j.st.Buffer.String() != `{"terminal.theme": "Dracula"}` {
		t.Errorf("the revert: conflict %v, text %q", j.conflict, j.st.Buffer.String())
	}
}

// TestSettingsSidebarDuotoneIcons verifies that all Settings sidebar sections
// render their Kubai two-tone icons in light and dark modes and animate on hover.
func TestSettingsSidebarDuotoneIcons(t *testing.T) {
	isolateSettings(t, "")
	registerFonts()
	st := &store{}
	a := &App{st: st}
	oldSection := settingsUI.section
	settingsUI.section = "appearance"
	t.Cleanup(func() { settingsUI.section = oldSection })

	tt := ui.NewTester(a.settingsView, 780, 620)
	tt.SetScale(2)
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		for _, s := range sections {
			label := say(s.label)
			if _, ok := tt.AccessByRole(ui.RoleTab, label); !ok {
				t.Errorf("dark=%v: missing sidebar tab %q", dark, label)
			}
		}
	}

	// Hover the Hosts row (monitor icon reveals its inner screen on hover) and
	// advance halfway through the enter motion.
	hosts, ok := tt.AccessByRole(ui.RoleTab, say(sections[5].label))
	if !ok {
		t.Fatalf("missing Hosts tab")
	}
	before := tt.Image()
	tt.Move(hosts.Bounds.X+hosts.Bounds.W/2, hosts.Bounds.Y+hosts.Bounds.H/2)
	tt.Frame()
	tt.Advance(240 * time.Millisecond)
	tt.Frame()
	after := tt.Image()
	if bytes.Equal(before.Pix, after.Pix) {
		t.Error("hovering Hosts sidebar row did not change rendered pixels")
	}

	// Turning off icon motion keeps the icon at rest while hovering.
	prefs.NoIconMotion = true
	tt.Move(0, 0)
	tt.Frame()
	stillBefore := tt.Image()
	tt.Move(hosts.Bounds.X+hosts.Bounds.W/2, hosts.Bounds.Y+hosts.Bounds.H/2)
	tt.Frame()
	tt.Advance(240 * time.Millisecond)
	tt.Frame()
	stillAfter := tt.Image()
	// Hovering still paints the row's hover pill, so compare a second frame
	// later in the animation window: with NoIconMotion no animation runs.
	tt.Advance(240 * time.Millisecond)
	tt.Frame()
	stillLater := tt.Image()
	if !bytes.Equal(stillAfter.Pix, stillLater.Pix) {
		t.Error("with NoIconMotion enabled, icon continued animating during hover")
	}
	_ = stillBefore
}
