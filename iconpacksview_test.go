package main

import (
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"gorex/internal/iconpack"
	"gorex/locales"
)

func TestIconPacksSettingsView(t *testing.T) {
	a, _ := newTestApp(t)
	cfgDir := t.TempDir()
	t.Setenv("GOREX_CONFIG", filepath.Join(cfgDir, "init.lua"))
	oldPrefs := prefs
	prefs = settings{}
	t.Cleanup(func() {
		prefs = oldPrefs
		setActiveIconPacks(nil)
		resetIconPacksUI()
	})
	resetIconPacksUI()

	// Prepare a local git repository serving a starter pack for one-click install.
	repoSrc := t.TempDir()
	subDir := filepath.Join(repoSrc, "packs", "starter-dev-tools")
	if err := os.MkdirAll(filepath.Join(subDir, "icons"), 0o755); err != nil {
		t.Fatal(err)
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M2 2h20v20H2z"/></svg>`
	if err := os.WriteFile(filepath.Join(subDir, "icons", "tf.svg"), []byte(svg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "pack.json"), []byte(`{
  "schema": 1,
  "id": "starter-dev-tools",
  "name": "Starter Dev Tools",
  "version": "0.1.0",
  "programs": { "terraform": { "icon": "icons/tf.svg", "bg": "#7b42bc", "fg": "#ffffff" } }
}`), 0o644); err != nil {
		t.Fatal(err)
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

	idx := iconpack.RegistryIndex{
		Schema: 1,
		Packs: []iconpack.RegistryPack{
			{
				ID:          "starter-dev-tools",
				Name:        "Starter Dev Tools",
				Description: "Cloud & CLI tools",
				Author:      "si-mods",
				Version:     "0.1.0",
				Repo:        repoSrc,
				Subdir:      "packs/starter-dev-tools",
				Entries:     1,
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(idx)
	}))
	defer srv.Close()
	t.Setenv("GOREX_PACK_REGISTRY", srv.URL)

	settingsUI.section = "icons"
	tt := ui.NewTester(a.settingsView, 780, 540)
	tt.Frame()

	// Wait for background fetch to populate the registry list.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		if tt.HasText("Starter Dev Tools") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !tt.HasText("Starter Dev Tools") {
		t.Fatalf("registry pack not shown; texts=%q", tt.Texts())
	}

	// Click Install and wait for installation + activation.
	tt.Click(locales.SettingsIconPacksInstall())
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		if tt.HasText(locales.SettingsIconPacksInstalledBadge()) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !tt.HasText(locales.SettingsIconPacksInstalledBadge()) {
		t.Fatalf("expected Installed badge after clicking Install; texts=%q", tt.Texts())
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
