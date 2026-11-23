package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
}
