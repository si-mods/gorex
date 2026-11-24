package main

import (
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/iconpack"
	"gorex/locales"
)

// TestIconPacksSettingsView: the Extensions page lists the registry's icon
// packs and themes, each with its kind; Install installs either kind (a
// theme as gorex pack install does); Use makes an installed theme the
// window theme at once and saves appearance.theme, and Built-in goes back
// (docs/DSK-008-theme.md). Each wait is for the install's own result, so
// no install still runs when the test's folders go.
func TestIconPacksSettingsView(t *testing.T) {
	a, _ := newTestApp(t)
	cfgDir := t.TempDir()
	t.Setenv("GOREX_CONFIG", filepath.Join(cfgDir, "init.lua"))
	oldPrefs := prefs
	prefs = settings{}
	oldSource, oldLocale := setThemeSource, setLocale
	setThemeSource, setLocale = func(mygo.ThemeSource) {}, func(string) {}
	t.Cleanup(func() {
		prefs = oldPrefs
		setThemeSource, setLocale = oldSource, oldLocale
		setActiveIconPacks(nil)
		resetIconPacksUI()
		applyTheme(nil)
	})
	resetIconPacksUI()

	// A local git repository with a starter icon pack and a theme.
	repoSrc := t.TempDir()
	subDir := filepath.Join(repoSrc, "packs", "starter-dev-tools")
	themeDir := filepath.Join(repoSrc, "themes", "slate")
	for _, d := range []string{filepath.Join(subDir, "icons"), themeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M2 2h20v20H2z"/></svg>`
	for path, body := range map[string]string{
		filepath.Join(subDir, "icons", "tf.svg"): svg,
		filepath.Join(subDir, "pack.json"): `{
  "schema": 1,
  "id": "starter-dev-tools",
  "name": "Starter Dev Tools",
  "version": "0.1.0",
  "programs": { "terraform": { "icon": "icons/tf.svg", "bg": "#7b42bc", "fg": "#ffffff" } }
}`,
		filepath.Join(themeDir, "theme.json"): `{"name": "Slate", "seeds": {"light": {"accent": "#3b6ea8"}, "dark": {"accent": "#7fa7d9"}}}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "."},
		{"commit", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoSrc
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}

	head, err := exec.Command("git", "-C", repoSrc, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	ref := strings.TrimSpace(string(head))
	idx := iconpack.RegistryIndex{
		Schema: 1,
		Packs: []iconpack.RegistryPack{{
			ID: "starter-dev-tools", Name: "Starter Dev Tools", Description: "Cloud & CLI tools",
			Author: "si-mods", Version: "0.1.0", Repo: repoSrc, Ref: ref, Subdir: "packs/starter-dev-tools", Entries: 1,
		}},
		Themes: []iconpack.RegistryPack{{
			ID: "slate", Kind: iconpack.KindTheme, Name: "Slate", Description: "Cool slate grays",
			Author: "si-mods", Version: "0.1.0", Repo: repoSrc, Ref: ref, Subdir: "themes/slate",
		}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(idx)
	}))
	defer srv.Close()
	t.Setenv("GOREX_PACK_REGISTRY", srv.URL)

	settingsUI.section = "icons"
	tt := ui.NewTester(a.settingsView, 780, 900)
	tt.Frame()
	wait := func(what string, done func() bool) {
		t.Helper()
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			tt.Frame()
			if done() {
				return
			}
		}
		t.Fatalf("%s; texts=%q", what, tt.Texts())
	}
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatalf("click %q: %v; texts=%q", label, err, tt.Texts())
		}
		tt.Frame()
	}

	icons, themeKind := locales.SettingsExtensionKindIcons(), locales.SettingsExtensionKindTheme()
	wait("the registry lists both kinds", func() bool {
		return tt.HasText(icons+" · starter-dev-tools · si-mods") && tt.HasText(themeKind+" · slate · si-mods")
	})
	if !tt.HasText(locales.SettingsIconPacks()) || !tt.HasText(locales.SettingsWindowTheme()) {
		t.Fatalf("page title or theme group missing; texts=%q", tt.Texts())
	}

	click(locales.SettingsIconPacksInstall() + " Starter Dev Tools")
	wait("the icon pack installs", func() bool {
		iconPacksUI.mu.Lock()
		defer iconPacksUI.mu.Unlock()
		return len(iconPacksUI.installing) == 0 && tt.HasText("starter-dev-tools · 0.1.0 · 1 icon")
	})
	click(locales.SettingsIconPacksInstall() + " Slate")
	wait("the theme installs", func() bool {
		iconPacksUI.mu.Lock()
		defer iconPacksUI.mu.Unlock()
		return len(iconPacksUI.installing) == 0 && len(iconPacksUI.themes) == 1 && iconPacksUI.themes[0].ID == "slate"
	})
	tt.Frame()
	if _, err := os.Stat(filepath.Join(cfgDir, "icon-packs", "slate")); err == nil {
		t.Error("the theme installed as an icon pack")
	}
	if prefs.Theme != "" {
		t.Error("installing a theme used it")
	}

	click("Slate")
	if prefs.Theme != "slate" || paletteOf(true).accent != ui.Hex("#7fa7d9") || paletteOf(false).accent != ui.Hex("#3b6ea8") {
		t.Fatalf("Use: appearance.theme %q, dark accent %v", prefs.Theme, paletteOf(true).accent)
	}
	if string(settingsOnDisk(t)["appearance.theme"]) != `"slate"` {
		t.Error("Use did not save appearance.theme")
	}
	// Remove is its own Tab stop, and the arrows on a theme's radio do not
	// reach it: a theme is removed by a click on Remove alone.
	click("Slate")
	tt.Key(0, ui.KeyTab)
	if !tt.Focused(locales.SettingsIconPacksRemove() + " Slate") {
		t.Errorf("Tab from the Slate radio did not reach its Remove button")
	}
	click("Slate")
	for _, key := range []ui.Key{ui.KeyDown, ui.KeyUp, ui.KeyDown, ui.KeyDown, ui.KeyUp} {
		tt.Key(0, key)
		tt.Frame()
	}
	iconPacksUI.mu.Lock()
	left := len(iconPacksUI.themes)
	iconPacksUI.mu.Unlock()
	if left != 1 || prefs.Theme != "slate" || !tt.HasText("Slate") {
		t.Errorf("arrow keys changed the themes: %d installed, in use %q", left, prefs.Theme)
	}
	click(locales.SettingsThemeBuiltIn())
	if prefs.Theme != "" || *paletteOf(true) != darkColors {
		t.Fatalf("Built-in: appearance.theme %q", prefs.Theme)
	}

	shotDir := os.Getenv("GOREX_SHOT_DIR")
	for _, dark := range []bool{false, true} {
		tt.SetDark(dark)
		tt.Frame()
		img := tt.Image()
		if img == nil || img.Bounds().Empty() {
			t.Fatalf("empty settings image for dark=%v", dark)
		}
		if shotDir != "" {
			name := "settings-light.png"
			if dark {
				name = "settings-dark.png"
			}
			f, err := os.Create(filepath.Join(shotDir, name))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, img)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

// TestRegistryEntriesOldIndex: an index from before theme packs, with no
// themes list, lists its icon packs; one with themes lists them after,
// each with its kind.
func TestRegistryEntriesOldIndex(t *testing.T) {
	var old iconpack.RegistryIndex
	if err := json.Unmarshal([]byte(`{"schema":1,"packs":[{"id":"a","name":"A","repo":"x"}]}`), &old); err != nil {
		t.Fatal(err)
	}
	if got := registryEntries(&old, ""); len(got) != 1 || got[0].ID != "a" || got[0].Kind != "" {
		t.Errorf("old index: %+v", got)
	}
	old.Themes = []iconpack.RegistryPack{{ID: "a", Name: "A"}}
	got := registryEntries(&old, "")
	if len(got) != 2 || got[1].Kind != iconpack.KindTheme || registryKey(got[0]) == registryKey(got[1]) {
		t.Errorf("with themes: %+v", got)
	}
	if registryEntries(nil, "") != nil {
		t.Error("no index listed entries")
	}
}
