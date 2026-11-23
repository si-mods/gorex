package iconpack

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"gorex/internal/config"
	"gorex/internal/rex"
)

const testSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M2 2h20v20H2z"/></svg>`

func writePackFixture(t *testing.T, root, id, manifestJSON string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(filepath.Join(dir, "icons"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifestJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "icons", "tf.svg"), []byte(testSVG), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadPackAndPathTraversalRejected(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "secret.svg")
	if err := os.WriteFile(outside, []byte(testSVG), 0o644); err != nil {
		t.Fatal(err)
	}
	badDir := writePackFixture(t, root, "bad-pack", `{
  "schema": 1,
  "id": "bad-pack",
  "name": "Bad",
  "programs": {
    "evil": { "icon": "../secret.svg" }
  }
}`)
	if _, err := LoadPack(badDir); err == nil {
		t.Fatal("expected path traversal to fail")
	}

	goodDir := writePackFixture(t, root, "good-pack", `{
  "schema": 1,
  "id": "good-pack",
  "name": "Good",
  "programs": {
    "terraform": {
      "icon": "icons/tf.svg",
      "bg": "#7b42bc",
      "fg": "#ffffff",
      "dark": { "bg": "#5b2a96" }
    }
  }
}`)
	pk, err := LoadPack(goodDir)
	if err != nil {
		t.Fatal(err)
	}
	if pk.ID != "good-pack" || len(pk.entries) != 1 {
		t.Fatalf("unexpected pack: %+v", pk)
	}
}

func TestRegistryPrecedenceAndSubcommand(t *testing.T) {
	packsDir := t.TempDir()
	cfgDir := t.TempDir()
	writePackFixture(t, packsDir, "community", `{
  "schema": 1,
  "id": "community",
  "name": "Community",
  "programs": {
    "terraform": { "icon": "icons/tf.svg", "bg": "#7b42bc", "fg": "#ffffff", "dark": { "bg": "#331155" } },
    "docker compose": { "icon": "icons/tf.svg", "bg": "#2496ed", "fg": "#ffffff" }
  }
}`)
	overridePath := filepath.Join(cfgDir, "override.svg")
	if err := os.WriteFile(overridePath, []byte(testSVG), 0o644); err != nil {
		t.Fatal(err)
	}

	osc := &rex.ProgramIcon{
		Format: "svg",
		Mode:   "mono",
		Data:   base64.StdEncoding.EncodeToString([]byte(testSVG)),
		Bg:     "#000000",
		Hash:   "osc12345",
	}

	// Default: pack beats OSC 7502, explicit override beats pack.
	reg, warns := LoadRegistry(packsDir, cfgDir, []string{"community"}, []config.ProgramIconRule{
		{Name: "mytool", Icon: "override.svg", Bg: "#112233"},
	}, "")
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if got := reg.Resolve("terraform", nil, osc, false); got == nil || got.Bg != "#7b42bc" {
		t.Fatalf("light pack resolve = %+v, want #7b42bc", got)
	}
	if got := reg.Resolve("terraform", nil, osc, true); got == nil || got.Bg != "#331155" {
		t.Fatalf("dark pack resolve = %+v, want #331155", got)
	}
	if got := reg.Resolve("docker", []string{"docker", "compose", "up"}, nil, false); got == nil || got.Bg != "#2496ed" {
		t.Fatalf("subcommand resolve = %+v, want #2496ed", got)
	}
	if got := reg.Resolve("mytool", nil, osc, false); got == nil || got.Bg != "#112233" {
		t.Fatalf("explicit override resolve = %+v, want #112233", got)
	}

	// Priority "osc": OSC 7502 beats pack, explicit override still wins.
	regOSC, _ := LoadRegistry(packsDir, cfgDir, []string{"community"}, []config.ProgramIconRule{
		{Name: "mytool", Icon: "override.svg", Bg: "#112233"},
	}, "osc")
	if got := regOSC.Resolve("terraform", nil, osc, false); got != osc {
		t.Fatalf("osc priority got %+v, want osc", got)
	}
	if got := regOSC.Resolve("mytool", nil, osc, false); got == nil || got.Bg != "#112233" {
		t.Fatalf("explicit override under osc priority = %+v", got)
	}
}

func TestInstallListAndRemove(t *testing.T) {
	srcRoot := t.TempDir()
	packsDir := filepath.Join(t.TempDir(), "icon-packs")
	src := writePackFixture(t, srcRoot, "demo-pack", `{
  "schema": 1,
  "id": "demo-pack",
  "name": "Demo Pack",
  "version": "1.0.0",
  "programs": { "demo": { "icon": "icons/tf.svg", "bg": "#123456" } }
}`)
	pk, err := InstallLocal(src, packsDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if pk.ID != "demo-pack" {
		t.Fatalf("installed id = %q", pk.ID)
	}
	list, err := ListPacks(packsDir)
	if err != nil || len(list) != 1 || list[0].Entries != 1 {
		t.Fatalf("ListPacks = %+v, err=%v", list, err)
	}
	if err := Remove(packsDir, "demo-pack"); err != nil {
		t.Fatal(err)
	}
	list, err = ListPacks(packsDir)
	if err != nil || len(list) != 0 {
		t.Fatalf("expected empty after remove, got %+v, err=%v", list, err)
	}
}
