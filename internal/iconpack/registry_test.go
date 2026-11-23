package iconpack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFetchSearchAndInstallFromRegistry(t *testing.T) {
	// Create a bare/local git repo acting as a registry pack source.
	repoSrc := t.TempDir()
	subDir := filepath.Join(repoSrc, "packs", "starter-dev-tools")
	if err := os.MkdirAll(filepath.Join(subDir, "icons"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "icons", "tf.svg"), []byte(testSVG), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "pack.json"), []byte(`{
  "schema": 1,
  "id": "starter-dev-tools",
  "name": "Starter Dev Tools",
  "version": "0.1.0",
  "programs": { "terraform": { "icon": "icons/tf.svg", "bg": "#7b42bc" } }
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
		c := exec.Command("git", args...)
		c.Dir = repoSrc
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}

	idx := RegistryIndex{
		Schema: 1,
		Packs: []RegistryPack{
			{
				ID:          "starter-dev-tools",
				Name:        "Starter Dev Tools",
				Description: "Cloud & CLI icons",
				Author:      "si-mods",
				Version:     "0.1.0",
				Repo:        repoSrc,
				Subdir:      "packs/starter-dev-tools",
				Entries:     1,
			},
			{
				ID:          "retro-unix",
				Name:        "Retro Unix",
				Description: "Classic shells",
				Author:      "community",
				Version:     "1.0.0",
				Repo:        "https://example.invalid/retro.git",
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(idx)
	}))
	defer srv.Close()

	gotIdx, err := FetchRegistry(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if matches := SearchRegistry(gotIdx, "cloud"); len(matches) != 1 || matches[0].ID != "starter-dev-tools" {
		t.Fatalf("SearchRegistry = %+v", matches)
	}
	rp, err := LookupRegistry(gotIdx, "starter-dev-tools")
	if err != nil {
		t.Fatal(err)
	}
	packsDir := filepath.Join(t.TempDir(), "icon-packs")
	pk, err := InstallFromRegistry(context.Background(), rp, packsDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if pk.ID != "starter-dev-tools" || len(pk.entries) != 1 {
		t.Fatalf("installed pack = %+v", pk)
	}
}

func TestRegistrySecurityGuards(t *testing.T) {
	if _, err := InstallFromRegistry(context.Background(), RegistryPack{
		ID:   "evil",
		Repo: "-oProxyCommand=evil",
	}, t.TempDir(), ""); err == nil {
		t.Fatal("expected flag-like Repo to be rejected")
	}

	repoSrc := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		c := exec.Command("git", args...)
		c.Dir = repoSrc
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}
	if err := os.WriteFile(filepath.Join(repoSrc, "README"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "init"}} {
		c := exec.Command("git", args...)
		c.Dir = repoSrc
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}
	if _, err := InstallFromRegistry(context.Background(), RegistryPack{
		ID:     "escape",
		Repo:   repoSrc,
		Subdir: "../escape",
	}, t.TempDir(), ""); err == nil {
		t.Fatal("expected escaping Subdir to be rejected")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, maxRegistryBytes+10)
		_, _ = w.Write(buf)
	}))
	defer srv.Close()
	if _, err := FetchRegistry(context.Background(), srv.URL); err == nil {
		t.Fatal("expected oversized registry index to be rejected")
	}
}
