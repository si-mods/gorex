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
	"testing"

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
		`"assets":{"logo":"logo.svg"},"brand":{"icon":"logo","wordmark":"Slate"}}`
	if err := os.WriteFile(filepath.Join(sub, "theme.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	// The pack's assets: the one the manifest names, and one it does not.
	os.MkdirAll(filepath.Join(sub, "assets"), 0o755)
	for _, f := range []string{"logo.svg", "extra.svg"} {
		os.WriteFile(filepath.Join(sub, "assets", f), []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 4 4"/>`), 0o644)
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
	if got := assets("slate"); len(got) != 1 || got[0] != "logo.svg" {
		t.Errorf("installed assets %q, want the one the manifest names", got)
	}

	out.Reset()
	errOut.Reset()
	if code, _ := Main([]string{"pack", "install", "--name", "local", sub}, &out, &errOut, ""); code != ExitOK {
		t.Fatalf("local install code=%d err=%s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "themes", "local.json")); err != nil {
		t.Errorf("local theme: %v", err)
	}
	if got := assets("local"); len(got) != 1 || got[0] != "logo.svg" {
		t.Errorf("local theme's assets %q", got)
	}
}
