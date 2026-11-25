package iconpack

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gorex/internal/theme"
)

// TestHasUpdate: the registry's ref decides when it pins one, else its
// version; a pack with no record (an older build's install) has an update.
func TestHasUpdate(t *testing.T) {
	refA, refB := "da1d34e000000000000000000000000000000000", "b938738000000000000000000000000000000000"
	for _, c := range []struct {
		name string
		rec  *Record
		rp   RegistryPack
		want bool
	}{
		{"no record", nil, RegistryPack{ID: "p", Version: "0.1.0"}, true},
		{"same ref", &Record{ID: "p", Version: "0.1.0", Ref: refA}, RegistryPack{ID: "p", Version: "0.1.0", Ref: refA}, false},
		{"new ref", &Record{ID: "p", Version: "0.1.0", Ref: refA}, RegistryPack{ID: "p", Version: "0.2.0", Ref: refB}, true},
		{"new ref, same version", &Record{ID: "p", Version: "0.1.0", Ref: refA}, RegistryPack{ID: "p", Version: "0.1.0", Ref: refB}, true},
		{"ref over version", &Record{ID: "p", Version: "0.1.0", Ref: refB}, RegistryPack{ID: "p", Version: "0.2.0", Ref: refB}, false},
		{"same version, no ref", &Record{ID: "p", Version: "1.0.0"}, RegistryPack{ID: "p", Version: "1.0.0"}, false},
		{"new version, no ref", &Record{ID: "p", Version: "1.0.0"}, RegistryPack{ID: "p", Version: "1.1.0"}, true},
		{"no version either side", &Record{ID: "p"}, RegistryPack{ID: "p"}, false},
	} {
		if got := HasUpdate(c.rec, c.rp); got != c.want {
			t.Errorf("%s: HasUpdate = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestReadRecordOld: a pack an older build installed has no record, and
// a record that does not decode counts as none; neither fails.
func TestReadRecordOld(t *testing.T) {
	dir := t.TempDir()
	if ReadRecord(dir) != nil {
		t.Error("an empty folder has a record")
	}
	os.WriteFile(filepath.Join(dir, RecordFile), []byte("{not json"), 0o644)
	if ReadRecord(dir) != nil {
		t.Error("a bad record decoded")
	}
	// A newer build's record with a field this build does not know.
	os.WriteFile(filepath.Join(dir, RecordFile), []byte(`{"id":"p","version":"1","later":true}`), 0o644)
	if r := ReadRecord(dir); r == nil || r.ID != "p" || r.Version != "1" {
		t.Errorf("record %+v", r)
	}
}

// TestInstallRecords: Install of either kind writes the record of the
// registry entry beside the pack; the pack still loads and lists as
// before; a local install over it drops the record, and Remove removes it.
func TestInstallRecords(t *testing.T) {
	repo := t.TempDir()
	iconDir := filepath.Join(repo, "packs", "tools")
	themeDir := filepath.Join(repo, "themes", "whale")
	for _, d := range []string{filepath.Join(iconDir, "icons"), themeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(iconDir, "icons", "tf.svg"), []byte(testSVG), 0o644)
	os.WriteFile(filepath.Join(iconDir, "pack.json"), []byte(`{"schema":1,"id":"tools","name":"Tools","version":"0.1.0","programs":{"terraform":{"icon":"icons/tf.svg","bg":"#7b42bc"}}}`), 0o644)
	os.WriteFile(filepath.Join(themeDir, "theme.json"), []byte(`{"name":"Whale","seeds":{"dark":{"accent":"#5b8def"}}}`), 0o644)
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
	ref := headOf(t, repo)
	cfg := t.TempDir()
	packsDir, themesDir := filepath.Join(cfg, "icon-packs"), theme.Dir(cfg)

	icon := RegistryPack{ID: "tools", Version: "0.1.0", Repo: repo, Ref: ref, Subdir: "packs/tools"}
	if _, err := Install(context.Background(), icon, packsDir, themesDir, "", ""); err != nil {
		t.Fatal(err)
	}
	if r := ReadRecord(filepath.Join(packsDir, "tools")); r == nil || *r != (Record{ID: "tools", Kind: "icon-pack", Version: "0.1.0", Ref: ref}) {
		t.Errorf("icon pack record %+v", r)
	}
	if list, err := ListPacks(packsDir); err != nil || len(list) != 1 || list[0].Error != "" {
		t.Errorf("ListPacks = %+v, %v", list, err)
	}

	th := RegistryPack{ID: "whale", Kind: KindTheme, Version: "0.2.0", Repo: repo, Ref: ref, Subdir: "themes/whale"}
	if _, err := Install(context.Background(), th, packsDir, themesDir, "", ""); err != nil {
		t.Fatal(err)
	}
	folder, _ := theme.AssetFolder(themesDir, "whale")
	if r := ReadRecord(folder); r == nil || *r != (Record{ID: "whale", Kind: KindTheme, Version: "0.2.0", Ref: ref}) {
		t.Errorf("theme record %+v", r)
	}
	if list := theme.List(themesDir); len(list) != 1 || list[0].ID != "whale" || list[0].Error != "" {
		t.Errorf("theme.List = %+v: the record shows as a theme or breaks one", list)
	}

	// A local install of the same IDs leaves no record: its version is not
	// the registry's. The icon pack's copy skips a record in its source.
	if _, err := InstallLocal(filepath.Join(packsDir, "tools"), packsDir, "copy"); err != nil {
		t.Fatal(err)
	}
	if ReadRecord(filepath.Join(packsDir, "copy")) != nil {
		t.Error("a local install copied the record")
	}
	if _, err := theme.Install(themeDir, themesDir, "whale"); err != nil {
		t.Fatal(err)
	}
	if ReadRecord(folder) != nil {
		t.Error("a local theme install kept the registry's record")
	}

	if _, err := Install(context.Background(), th, packsDir, themesDir, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := theme.Remove(themesDir, "whale"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Errorf("Remove left the record's folder: %v", err)
	}
}
