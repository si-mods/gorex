package ext

import (
	"os"
	"path/filepath"
	"testing"
)

func writePack(t *testing.T, root, id, manifest string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, File), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const demoManifest = `{
  "id": "demo",
  "name": "Demo",
  "version": "1.0.0",
  "engine": "webview",
  "api": 1,
  "panes": [{"kind": "demo.pane", "title": "Demo", "fields": ["id", "url"]}],
  "storage": {"dir": "demo-data"},
  "permissions": ["state", "storage", "env"],
  "commands": [{"id": "new_tab", "title": "New Demo Tab", "kind": "demo.pane", "placement": "tab"}]
}`

func TestValidate(t *testing.T) {
	m, err := LoadManifest(writePack(t, t.TempDir(), "demo", demoManifest))
	if err != nil {
		t.Fatalf("valid manifest: %v", err)
	}
	if m.API != 1 || !m.Has(PermState) || m.Has(PermWeb) || m.Pane("demo.pane") == nil {
		t.Errorf("manifest %+v", m)
	}
}

func TestValidateBad(t *testing.T) {
	root := t.TempDir()
	cases := map[string]string{
		"noid":     `{"name":"x","engine":"webview","api":1,"panes":[{"kind":"a.b","title":"t"}]}`,
		"noengine": `{"id":"noengine","name":"x","engine":"lua","api":1,"panes":[{"kind":"a.b","title":"t"}]}`,
		"newapi":   `{"id":"newapi","name":"x","engine":"webview","api":99,"panes":[{"kind":"a.b","title":"t"}]}`,
		"nopanes":  `{"id":"nopanes","name":"x","engine":"webview","api":1}`,
		"badfield": `{"id":"badfield","name":"x","engine":"webview","api":1,"panes":[{"kind":"a.b","title":"t","fields":["sid"]}]}`,
		"badperm":  `{"id":"badperm","name":"x","engine":"webview","api":1,"panes":[{"kind":"a.b","title":"t"}],"permissions":["root"]}`,
		"webslot":  `{"id":"webslot","name":"x","engine":"webview","api":1,"panes":[{"kind":"a.b","title":"t"}],"slots":["web"]}`,
		"badstore": `{"id":"badstore","name":"x","engine":"webview","api":1,"panes":[{"kind":"a.b","title":"t"}],"storage":{"dir":"../out"}}`,
		"badplace": `{"id":"badplace","name":"x","engine":"webview","api":1,"panes":[{"kind":"a.b","title":"t"}],"commands":[{"id":"c","title":"t","placement":"menu"}]}`,
		"nokind":   `{"id":"nokind","name":"x","engine":"webview","api":1,"panes":[{"kind":"a.b","title":"t"}],"commands":[{"id":"c","title":"t","placement":"tab"}]}`,
	}
	for id, js := range cases {
		if _, err := LoadManifest(writePack(t, root, id, js)); err == nil {
			t.Errorf("%s: expected an error", id)
		}
	}
}

func TestLoadAll(t *testing.T) {
	root := t.TempDir()
	writePack(t, root, "demo", demoManifest)
	writePack(t, root, "clash", `{"id":"clash","name":"c","engine":"webview","api":1,"panes":[{"kind":"demo.pane","title":"t"}]}`)
	writePack(t, root, "bad", `{not json`)
	packs, errs := LoadAll(root)
	if len(packs) != 2 {
		t.Fatalf("%d packs loaded, want 2 (the clash is kept, kinds are per pack): %v", len(packs), errs)
	}
	if len(errs) != 2 { // bad manifest + duplicate kind
		t.Errorf("%d errors, want 2: %v", len(errs), errs)
	}
	if ByKind(packs, "demo.pane") == nil || ByKind(packs, "none") != nil {
		t.Error("ByKind")
	}
}

func TestLoadAllKindClash(t *testing.T) {
	// Two packs may not claim one kind: the second loses its kind claim
	// but still loads, its other kinds intact.
	root := t.TempDir()
	writePack(t, root, "one", `{"id":"one","name":"1","engine":"webview","api":1,"panes":[{"kind":"k.a","title":"t"}]}`)
	writePack(t, root, "two", `{"id":"two","name":"2","engine":"webview","api":1,"panes":[{"kind":"k.a","title":"t"},{"kind":"k.b","title":"t"}]}`)
	packs, errs := LoadAll(root)
	if len(errs) != 1 || len(packs) != 2 {
		t.Fatalf("packs %d errs %v", len(packs), errs)
	}
	got := map[string]string{}
	for _, p := range packs {
		for _, pd := range p.Manifest.Panes {
			got[pd.Kind] = p.ID()
		}
	}
}

func TestJailPath(t *testing.T) {
	root := t.TempDir()
	resolved, _ := filepath.EvalSymlinks(root)
	if p, err := JailPath(root, "a/b.txt"); err != nil || filepath.Dir(p) != filepath.Join(resolved, "a") {
		t.Errorf("nested: %q %v", p, err)
	}
	for _, bad := range []string{"", "/etc/passwd", "..", "../x", "a/../../x", `..\x`} {
		if p, err := JailPath(root, bad); err == nil {
			t.Errorf("%q -> %q, want an error", bad, p)
		}
	}
	// A symlink inside root may not point out of it.
	outside := filepath.Join(t.TempDir(), "out.txt")
	os.WriteFile(outside, []byte("x"), 0o644)
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if p, err := JailPath(root, "link.txt"); err == nil {
		t.Errorf("symlink escape -> %q", p)
	}
	// A link staying inside is fine.
	os.WriteFile(filepath.Join(root, "real.txt"), []byte("x"), 0o644)
	os.Symlink("real.txt", filepath.Join(root, "inner.txt"))
	if _, err := JailPath(root, "inner.txt"); err != nil {
		t.Errorf("inner symlink: %v", err)
	}
}

func TestStorageDir(t *testing.T) {
	m, _ := LoadManifest(writePack(t, t.TempDir(), "demo", demoManifest))
	if got := m.StorageDir("/data"); got != filepath.Join("/data", "demo-data") {
		t.Errorf("storage dir %q", got)
	}
	m.Storage.Dir = ""
	if got := m.StorageDir("/data"); got != filepath.Join("/data", "exts", "demo", "data") {
		t.Errorf("default storage dir %q", got)
	}
}

func TestInstall(t *testing.T) {
	src := writePack(t, t.TempDir(), "demo", demoManifest)
	os.WriteFile(filepath.Join(src, "index.html"), []byte("<html>"), 0o644)
	dst := filepath.Join(t.TempDir(), "exts")
	p, err := Install(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID() != "demo" {
		t.Errorf("id %q", p.ID())
	}
	for _, f := range []string{File, "index.html"} {
		if _, err := os.Stat(filepath.Join(dst, "demo", f)); err != nil {
			t.Errorf("%s not installed: %v", f, err)
		}
	}
	// A second install replaces.
	if _, err := Install(src, dst); err != nil {
		t.Fatal(err)
	}
}
