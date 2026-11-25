package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"gorex/internal/homedir"
	"gorex/internal/iconpack"
)

func TestPackCmd(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("GOREX_CONFIG", filepath.Join(cfgDir, "init.lua"))

	srcDir := filepath.Join(t.TempDir(), "my-pack")
	if err := os.MkdirAll(filepath.Join(srcDir, "icons"), 0o755); err != nil {
		t.Fatal(err)
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M0 0h24v24H0z"/></svg>`
	if err := os.WriteFile(filepath.Join(srcDir, "icons", "app.svg"), []byte(svg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "pack.json"), []byte(`{
  "schema": 1,
  "id": "my-pack",
  "name": "My Pack",
  "version": "0.1.0",
  "programs": { "mycli": { "icon": "icons/app.svg", "bg": "#112233" } }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code, _ := Main([]string{"pack", "install", srcDir}, &out, &errOut, "")
	if code != ExitOK {
		t.Fatalf("install code=%d err=%s", code, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	code, _ = Main([]string{"pack", "ls", "--json"}, &out, &errOut, "")
	if code != ExitOK {
		t.Fatalf("ls code=%d err=%s", code, errOut.String())
	}
	var list []iconpack.Summary
	if err := json.Unmarshal(out.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal ls: %v (%s)", err, out.String())
	}
	if len(list) != 1 || list[0].ID != "my-pack" || list[0].Entries != 1 {
		t.Fatalf("unexpected list: %+v", list)
	}

	out.Reset()
	errOut.Reset()
	code, _ = Main([]string{"pack", "rm", "my-pack"}, &out, &errOut, "")
	if code != ExitOK {
		t.Fatalf("rm code=%d err=%s", code, errOut.String())
	}

	idx := iconpack.RegistryIndex{
		Schema: 1,
		Packs: []iconpack.RegistryPack{
			{ID: "starter-dev-tools", Name: "Starter Dev Tools", Version: "0.1.0", Description: "Cloud icons", Repo: "https://example.invalid/repo.git"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(idx)
	}))
	defer srv.Close()
	t.Setenv("GOREX_PACK_REGISTRY", srv.URL)

	out.Reset()
	errOut.Reset()
	code, _ = Main([]string{"pack", "search", "--json", "cloud"}, &out, &errOut, "")
	if code != ExitOK {
		t.Fatalf("search code=%d err=%s", code, errOut.String())
	}
	var found []iconpack.RegistryPack
	if err := json.Unmarshal(out.Bytes(), &found); err != nil {
		t.Fatalf("unmarshal search: %v", err)
	}
	if len(found) != 1 || found[0].ID != "starter-dev-tools" {
		t.Fatalf("search got %+v", found)
	}
}

// TestPackInstallTheme: a theme pack, listed in the registry's themes and
// cloned as an icon pack is, installs its theme.json as
// <config>/themes/<id>.json, which appearance.theme names
// (docs/DSK-008-theme.md); a local directory with a theme.json does too.
// Icon pack search keeps its packs, and the theme shows with its kind.
func TestPackInstallTheme(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("GOREX_CONFIG", filepath.Join(cfgDir, "init.lua"))

	repo := t.TempDir()
	sub := filepath.Join(repo, "themes", "slate")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"Slate","seeds":{"dark":{"accent":"#5b8def"}},"roles":{"sideBar.tabActiveBackground":"#5b8def33"},` +
		`"assets":{"bg":"bg.kage"},"shaders":{"api":1,"background":{"source":"bg"}}}`
	if err := os.WriteFile(filepath.Join(sub, "theme.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	// The pack's assets: the one the manifest names, and one it does not.
	os.MkdirAll(filepath.Join(sub, "assets"), 0o755)
	for _, f := range []string{"bg.kage", "extra.kage"} {
		os.WriteFile(filepath.Join(sub, "assets", f), []byte("package main\n"), 0o644)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "."},
		{"commit", "-m", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	idx := iconpack.RegistryIndex{
		Schema: 1,
		Packs:  []iconpack.RegistryPack{{ID: "starter-dev-tools", Name: "Starter", Description: "slate-gray icons", Repo: "https://example.invalid/x.git"}},
		Themes: []iconpack.RegistryPack{{ID: "slate", Kind: "theme", Name: "Slate", Description: "Cool slate grays", Repo: repo, Subdir: "themes/slate", Ref: strings.TrimSpace(string(head))}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(idx)
	}))
	defer srv.Close()
	t.Setenv("GOREX_PACK_REGISTRY", srv.URL)

	var out, errOut bytes.Buffer
	if code, _ := Main([]string{"pack", "search", "--json", "slate"}, &out, &errOut, ""); code != ExitOK {
		t.Fatalf("search code=%d err=%s", code, errOut.String())
	}
	var found []iconpack.RegistryPack
	if err := json.Unmarshal(out.Bytes(), &found); err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].ID != "starter-dev-tools" || found[0].Kind != "" || found[1].ID != "slate" || found[1].Kind != iconpack.KindTheme {
		t.Fatalf("search got %+v", found)
	}

	out.Reset()
	errOut.Reset()
	if code, _ := Main([]string{"pack", "install", "slate"}, &out, &errOut, ""); code != ExitOK {
		t.Fatalf("install code=%d err=%s", code, errOut.String())
	}
	got, err := os.ReadFile(filepath.Join(cfgDir, "themes", "slate.json"))
	if err != nil || string(got) != manifest {
		t.Fatalf("installed %q, %v", got, err)
	}
	if !strings.Contains(out.String(), `"appearance.theme": "slate"`) {
		t.Errorf("install said %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "icon-packs", "slate")); err == nil {
		t.Error("a theme pack installed as an icon pack")
	}
	assets := func(id string) []string {
		des, _ := os.ReadDir(filepath.Join(cfgDir, "themes", id+".assets"))
		var names []string
		for _, de := range des {
			names = append(names, de.Name())
		}
		return names
	}
	// Beside them, the install record (docs/MOD-012-program-icon-packs.md, Updates).
	if got := assets("slate"); len(got) != 2 || got[0] != iconpack.RecordFile || got[1] != "bg.kage" {
		t.Errorf("installed assets %q, want the record and the one the manifest names", got)
	}

	out.Reset()
	errOut.Reset()
	if code, _ := Main([]string{"pack", "install", "--name", "local", sub}, &out, &errOut, ""); code != ExitOK {
		t.Fatalf("local install code=%d err=%s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "themes", "local.json")); err != nil {
		t.Errorf("local theme: %v", err)
	}
	if got := assets("local"); len(got) != 1 || got[0] != "bg.kage" {
		t.Errorf("local theme's assets %q", got)
	}

	// ls lists the themes with their kind; rm removes one.
	out.Reset()
	errOut.Reset()
	if code, _ := Main([]string{"pack", "ls", "--json"}, &out, &errOut, ""); code != ExitOK {
		t.Fatalf("ls code=%d err=%s", code, errOut.String())
	}
	var listed []iconpack.Summary
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].ID != "local" || listed[1].ID != "slate" ||
		listed[0].Kind != iconpack.KindTheme || listed[1].Name != "Slate" {
		t.Fatalf("ls got %+v", listed)
	}
	out.Reset()
	if code, _ := Main([]string{"pack", "ls"}, &out, &errOut, ""); code != ExitOK || !strings.Contains(out.String(), "slate	Slate			theme") {
		t.Errorf("ls text %q", out.String())
	}
	if code, _ := Main([]string{"pack", "rm", "local"}, &out, &errOut, ""); code != ExitOK {
		t.Fatalf("rm code=%d err=%s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "themes", "local.json")); err == nil {
		t.Error("rm left the theme")
	}
	if got := assets("local"); len(got) != 0 {
		t.Errorf("rm left assets %q", got)
	}
	errOut.Reset()
	if code, _ := Main([]string{"pack", "rm", "local"}, &out, &errOut, ""); code != ExitFailed {
		t.Errorf("rm of a missing pack code=%d", code)
	}
}

// TestPackInstallBeforeMove: on a machine whose data is still in the
// legacy data directory, with no init.lua anywhere, a pack installs into
// the legacy data directory and does not make ~/.gorex, which would take
// every process away from the legacy data (docs/MOD-013-home-dir.md).
func TestPackInstallBeforeMove(t *testing.T) {
	home, plat := t.TempDir(), t.TempDir()
	oldHome, oldCfg := homedir.UserHomeDir, homedir.UserConfigDir
	homedir.UserHomeDir = func() (string, error) { return home, nil }
	homedir.UserConfigDir = func() (string, error) { return plat, nil }
	t.Cleanup(func() { homedir.UserHomeDir, homedir.UserConfigDir = oldHome, oldCfg })
	for _, v := range []string{"GOREX_DIR", "GOREX_CONFIG", "XDG_CONFIG_HOME"} {
		t.Setenv(v, "")
	}
	t.Setenv("HOME", home)
	legacy := filepath.Join(plat, "GoRex")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "settings.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	srcDir := filepath.Join(t.TempDir(), "my-pack")
	if err := os.MkdirAll(filepath.Join(srcDir, "icons"), 0o755); err != nil {
		t.Fatal(err)
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M0 0h24v24H0z"/></svg>`
	if err := os.WriteFile(filepath.Join(srcDir, "icons", "app.svg"), []byte(svg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "pack.json"), []byte(`{"schema": 1, "id": "my-pack", "name": "My Pack", "version": "0.1.0",
  "programs": { "mycli": { "icon": "icons/app.svg" } }}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code, _ := Main([]string{"pack", "install", srcDir}, &out, &errOut, ""); code != ExitOK {
		t.Fatalf("install code=%d err=%s", code, errOut.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".gorex")); err == nil {
		t.Fatal("pack install made ~/.gorex before the move")
	}
	if _, err := os.Stat(filepath.Join(legacy, "icon-packs", "my-pack", "pack.json")); err != nil {
		t.Fatalf("pack not in the legacy data directory: %v", err)
	}
}

// TestPackUpdate: gorex pack update installs again each pack whose
// registry entry moved past its install record, keeps the pack's name,
// and records the new version; a pack with no record (an older build's
// install) has an unknown version and gets the update; --check installs
// nothing (docs/MOD-012-program-icon-packs.md, Updates).
func TestPackUpdate(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("GOREX_CONFIG", filepath.Join(cfgDir, "init.lua"))

	repo := t.TempDir()
	iconDir := filepath.Join(repo, "packs", "tools")
	themeDir := filepath.Join(repo, "themes", "whale")
	for _, d := range []string{filepath.Join(iconDir, "icons"), themeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M0 0h24v24H0z"/></svg>`
	os.WriteFile(filepath.Join(iconDir, "icons", "app.svg"), []byte(svg), 0o644)
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	theme1 := `{"name":"Whale","seeds":{"dark":{"accent":"#111111"}}}`
	theme2 := `{"name":"Whale","seeds":{"dark":{"accent":"#222222"}}}`
	commit := func(version, manifest string) string {
		os.WriteFile(filepath.Join(iconDir, "pack.json"), []byte(`{"schema":1,"id":"tools","name":"Tools","version":"`+version+`","programs":{"mycli":{"icon":"icons/app.svg","bg":"#112233"}}}`), 0o644)
		os.WriteFile(filepath.Join(themeDir, "theme.json"), []byte(manifest), 0o644)
		git("add", ".")
		git("commit", "-q", "-m", version)
		return git("rev-parse", "HEAD")
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	ref1 := commit("0.1.0", theme1)
	ref2 := commit("0.2.0", theme2)

	entries := func(version, ref string) iconpack.RegistryIndex {
		return iconpack.RegistryIndex{
			Schema: 1,
			Packs:  []iconpack.RegistryPack{{ID: "tools", Name: "Tools", Version: version, Repo: repo, Ref: ref, Subdir: "packs/tools"}},
			Themes: []iconpack.RegistryPack{{ID: "whale", Kind: "theme", Name: "Whale", Version: version, Repo: repo, Ref: ref, Subdir: "themes/whale"}},
		}
	}
	var idx atomic.Pointer[iconpack.RegistryIndex]
	idx.Store(new(entries("0.1.0", ref1)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(idx.Load())
	}))
	defer srv.Close()
	t.Setenv("GOREX_PACK_REGISTRY", srv.URL)

	var out, errOut bytes.Buffer
	run := func(want int, args ...string) string {
		t.Helper()
		out.Reset()
		errOut.Reset()
		if code, _ := Main(append([]string{"pack"}, args...), &out, &errOut, ""); code != want {
			t.Fatalf("pack %v: code %d, want %d; out=%q err=%q", args, code, want, out.String(), errOut.String())
		}
		return out.String()
	}
	run(ExitOK, "install", "tools")
	run(ExitOK, "install", "whale")
	themeFile := filepath.Join(cfgDir, "themes", "whale.json")
	// An older build wrote no record.
	if err := os.Remove(filepath.Join(cfgDir, "icon-packs", "tools", iconpack.RecordFile)); err != nil {
		t.Fatal(err)
	}

	if got := run(ExitOK, "update", "whale"); !strings.Contains(got, "whale: up to date (0.1.0)") {
		t.Errorf("update of a current pack said %q", got)
	}
	if got := run(ExitOK, "update"); got != "updated tools: version unknown -> 0.1.0\n" {
		t.Errorf("update of an unrecorded pack said %q", got)
	}
	if got := run(ExitOK, "update"); got != "every installed pack is up to date\n" {
		t.Errorf("update with none said %q", got)
	}

	idx.Store(new(entries("0.2.0", ref2)))
	if got := run(ExitOK, "update", "--check"); got != "tools\t0.1.0 -> 0.2.0\nwhale\t0.1.0 -> 0.2.0\n" {
		t.Errorf("--check said %q", got)
	}
	if b, _ := os.ReadFile(themeFile); string(b) != theme1 {
		t.Errorf("--check installed: %s", b)
	}
	if got := run(ExitOK, "update", "whale"); got != "updated whale: 0.1.0 -> 0.2.0\n" {
		t.Errorf("update whale said %q", got)
	}
	if b, _ := os.ReadFile(themeFile); string(b) != theme2 {
		t.Errorf("updated theme %s", b)
	}
	if got := run(ExitOK, "ls"); !strings.Contains(got, "whale\tWhale\t0.2.0\t\ttheme") || !strings.Contains(got, "tools\tTools\t0.1.0") {
		t.Errorf("ls after the theme's update %q", got)
	}
	if got := run(ExitOK, "update"); got != "updated tools: 0.1.0 -> 0.2.0\n" {
		t.Errorf("update all said %q", got)
	}
	if got := run(ExitOK, "ls"); !strings.Contains(got, "tools\tTools\t0.2.0") {
		t.Errorf("ls after the icon pack's update %q", got)
	}
	run(ExitFailed, "update", "nope")
	run(ExitUsage, "update", "a", "b")
}
