package main

import (
	"context"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
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

	click(locales.SettingsThemeUse() + " Slate")
	if prefs.Theme != "slate" || paletteOf(true).accent != ui.Hex("#7fa7d9") || paletteOf(false).accent != ui.Hex("#3b6ea8") {
		t.Fatalf("Use: appearance.theme %q, dark accent %v", prefs.Theme, paletteOf(true).accent)
	}
	if string(settingsOnDisk(t)["appearance.theme"]) != `"slate"` {
		t.Error("Use did not save appearance.theme")
	}
	click(locales.SettingsThemeUse() + " " + locales.SettingsThemeBuiltIn())
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

// TestExtensionsUpdate: with the registry loaded, an installed entry whose
// registry ref moved past its install record, or that has no record (an
// older build's install), shows Update in place of Installed, with the
// installed version under it. Update installs it again: the theme in use
// stays in use and shows the new colors at once, and an icon pack keeps
// its switch (docs/MOD-012-program-icon-packs.md, Updates).
func TestExtensionsUpdate(t *testing.T) {
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

	repo := t.TempDir()
	iconDir := filepath.Join(repo, "packs", "starter-dev-tools")
	themeDir := filepath.Join(repo, "themes", "harbor-whale")
	for _, d := range []string{filepath.Join(iconDir, "icons"), themeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(iconDir, "icons", "tf.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M2 2h20v20H2z"/></svg>`), 0o644)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(version, accent string) string {
		os.WriteFile(filepath.Join(iconDir, "pack.json"), []byte(`{"schema":1,"id":"starter-dev-tools","name":"Starter Dev Tools","version":"`+version+`",`+
			`"programs":{"terraform":{"icon":"icons/tf.svg","bg":"#7b42bc","fg":"#ffffff"}}}`), 0o644)
		os.WriteFile(filepath.Join(themeDir, "theme.json"), []byte(`{"name":"Harbor Whale","description":"Deep-sea blues","seeds":{"light":{"accent":"`+accent+`"},"dark":{"accent":"`+accent+`"}}}`), 0o644)
		git("add", ".")
		git("commit", "-q", "-m", version)
		return git("rev-parse", "HEAD")
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	ref1 := commit("0.1.0", "#3b6ea8")
	ref2 := commit("0.2.0", "#2a9d8f")
	entries := func(version, ref string) *iconpack.RegistryIndex {
		return &iconpack.RegistryIndex{
			Schema: 1,
			Packs: []iconpack.RegistryPack{{
				ID: "starter-dev-tools", Name: "Starter Dev Tools", Description: "Cloud & CLI tools",
				Author: "alice", Version: version, Repo: repo, Ref: ref, Subdir: "packs/starter-dev-tools", Entries: 1,
			}},
			Themes: []iconpack.RegistryPack{{
				ID: "harbor-whale", Kind: iconpack.KindTheme, Name: "Harbor Whale", Description: "Deep-sea blues",
				Author: "alice", Version: version, Repo: repo, Ref: ref, Subdir: "themes/harbor-whale",
			}},
		}
	}
	var idx atomic.Pointer[iconpack.RegistryIndex]
	idx.Store(entries("0.1.0", ref1))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(idx.Load())
	}))
	defer srv.Close()
	t.Setenv("GOREX_PACK_REGISTRY", srv.URL)

	// The theme installs with its record; the icon pack as an older build
	// installs it, with none. The theme is in use; the icon pack is off.
	packsDir, themesDir := iconpack.Dir(), filepath.Join(cfgDir, "themes")
	old := idx.Load()
	themeEntry := old.Themes[0]
	themeEntry.Kind = iconpack.KindTheme
	if _, err := iconpack.Install(context.Background(), themeEntry, packsDir, themesDir, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := iconpack.InstallFromRegistry(context.Background(), old.Packs[0], packsDir, ""); err != nil {
		t.Fatal(err)
	}
	a.useTheme("harbor-whale")

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
	updateTheme := locales.SettingsIconPacksUpdate() + " Harbor Whale"
	updateIcons := locales.SettingsIconPacksUpdate() + " Starter Dev Tools"

	// The registry has the installed theme's ref: Installed. The icon pack
	// has no record: Update, with the version of its own pack.json.
	wait("the registry loads", func() bool { return tt.HasText(updateIcons) })
	if tt.HasText(updateTheme) || !tt.HasText(locales.SettingsIconPacksInstalledVersion("0.1.0")) || tt.HasText(locales.SettingsIconPacksVersionUnknown()) {
		t.Fatalf("before the registry moves; texts=%q", tt.Texts())
	}

	// The registry moves to the next ref; Refresh shows the update.
	idx.Store(entries("0.2.0", ref2))
	click(locales.SettingsIconPacksRefresh())
	wait("Refresh shows the theme's update", func() bool { return tt.HasText(updateTheme) })
	if !tt.HasText(locales.SettingsIconPacksInstalledVersion("0.1.0")) {
		t.Errorf("no installed version under the update; texts=%q", tt.Texts())
	}
	if shotDir := os.Getenv("GOREX_SHOT_DIR"); shotDir != "" {
		for _, dark := range []bool{false, true} {
			tt.SetDark(dark)
			tt.Frame()
			name := map[bool]string{false: "extensions-update-light.png", true: "extensions-update-dark.png"}[dark]
			f, err := os.Create(filepath.Join(shotDir, name))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, tt.Image())
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		tt.SetDark(false)
	}

	inProgress := func() bool {
		iconPacksUI.mu.Lock()
		defer iconPacksUI.mu.Unlock()
		return len(iconPacksUI.installing) != 0
	}
	click(updateTheme)
	wait("the theme updates", func() bool { return !inProgress() && !tt.HasText(updateTheme) })
	if prefs.Theme != "harbor-whale" || paletteOf(true).accent != ui.Hex("#2a9d8f") {
		t.Errorf("after the update: theme %q, dark accent %v", prefs.Theme, paletteOf(true).accent)
	}
	click(updateIcons)
	wait("the icon pack updates", func() bool { return !inProgress() && !tt.HasText(updateIcons) })
	if slices.Contains(prefs.IconPacks, "starter-dev-tools") {
		t.Error("the update turned on an icon pack that was off")
	}
	if !tt.HasText("starter-dev-tools · 0.2.0 · 1 icon") {
		t.Errorf("the icon pack did not update; texts=%q", tt.Texts())
	}
	if rec := iconpack.ReadRecord(filepath.Join(packsDir, "starter-dev-tools")); rec == nil || rec.Ref != ref2 {
		t.Errorf("icon pack record %+v", rec)
	}
}

// TestInstalledVersion: the hint under an update names the record's
// version, else its ref; with no record, the pack's own version (an icon
// pack's pack.json), else "version unknown" (a theme, which declares none).
func TestInstalledVersion(t *testing.T) {
	ref := "b938738000000000000000000000000000000000"
	for _, c := range []struct {
		rec  *iconpack.Record
		own  string
		want string
	}{
		{&iconpack.Record{ID: "p", Version: "0.2.0", Ref: ref}, "0.1.0", locales.SettingsIconPacksInstalledVersion("0.2.0")},
		{&iconpack.Record{ID: "p", Ref: ref}, "0.1.0", locales.SettingsIconPacksInstalledVersion("b938738")},
		{nil, "0.1.0", locales.SettingsIconPacksInstalledVersion("0.1.0")},
		{nil, "", locales.SettingsIconPacksVersionUnknown()},
	} {
		if got := installedVersion(c.rec, c.own); got != c.want {
			t.Errorf("installedVersion(%+v, %q) = %q, want %q", c.rec, c.own, got, c.want)
		}
	}
}
