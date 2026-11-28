package iconpack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		{"commit", "--no-verify", "-m", "init"},
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
				Ref:         headOf(t, repoSrc),
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
	for _, args := range [][]string{{"add", "."}, {"commit", "--no-verify", "-m", "init"}} {
		c := exec.Command("git", args...)
		c.Dir = repoSrc
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, string(out))
		}
	}
	if _, err := InstallFromRegistry(context.Background(), RegistryPack{
		ID:     "escape",
		Repo:   repoSrc,
		Ref:    headOf(t, repoSrc),
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

// TestCheckoutRef: with a ref at the first of two commits, Checkout gives
// the first commit's files, not the default branch's. A ref that is not a
// full SHA, or that the repo does not have, fails, with no fall back to
// the default branch; so does no ref, for a repo that is not gorex-registry
// (docs/DSK-010-theme-ambience.md, Tests).
func TestCheckoutRef(t *testing.T) {
	repo := t.TempDir()
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
	git("init", "-q", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	commit := func(text string) string {
		if err := os.WriteFile(filepath.Join(repo, "theme.json"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", ".")
		git("commit", "--no-verify", "-q", "-m", text)
		return git("rev-parse", "HEAD")
	}
	first := commit(`{"name":"first"}`)
	commit(`{"name":"second"}`)

	read := func(ref string) (string, error) {
		var got string
		err := Checkout(context.Background(), RegistryPack{Repo: repo, Ref: ref}, func(dir string) error {
			b, err := os.ReadFile(filepath.Join(dir, "theme.json"))
			got = string(b)
			return err
		})
		return got, err
	}
	if got, err := read(first); err != nil || got != `{"name":"first"}` {
		t.Fatalf("ref at the first commit: %q, %v", got, err)
	}
	if got, err := read(""); err == nil {
		t.Fatalf("no ref, outside gorex-registry: installed %q", got)
	}
	for _, ref := range []string{
		first[:12],                    // short
		"main",                        // a branch
		strings.ToUpper(first),        // not as git prints it
		strings.Repeat("0", 40),       // not in the repo
		"-" + strings.Repeat("a", 39), // an option
	} {
		if got, err := read(ref); err == nil {
			t.Errorf("ref %q installed %q", ref, got)
		}
	}
}

// headOf is the commit the repo at dir is at.
func headOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}
