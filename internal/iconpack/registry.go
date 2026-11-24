package iconpack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultRegistryURL is the static index published by si-mods/gorex-registry.
const DefaultRegistryURL = "https://raw.githubusercontent.com/si-mods/gorex-registry/main/index.json"

const maxRegistryBytes = 2 << 20 // 2 MiB

// RegistryPack describes one community pack listed in index.json: an icon
// pack, or with Kind "theme" a theme pack (docs/DSK-008-theme.md).
type RegistryPack struct {
	ID          string `json:"id"`
	Kind        string `json:"kind,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	Version     string `json:"version,omitempty"`
	Repo        string `json:"repo"`
	Subdir      string `json:"subdir,omitempty"`
	// Ref is the full commit SHA of Repo to install, for a pack in a repo
	// of its own (docs/DSK-010-theme-ambience.md): a later push to the repo
	// changes nothing until the registry moves Ref. "" clones the default
	// branch, as for a pack in gorex-registry.
	Ref     string `json:"ref,omitempty"`
	Entries int    `json:"entries,omitempty"`
}

// KindTheme is the Kind of a theme pack: a directory with a theme.json.
const KindTheme = "theme"

// RegistryIndex is the schema of gorex-registry index.json. Theme packs
// are in Themes, a list builds before it ignore, so that they do not show
// them as icon packs.
type RegistryIndex struct {
	Schema    int            `json:"schema"`
	UpdatedAt string         `json:"updated_at,omitempty"`
	Packs     []RegistryPack `json:"packs"`
	Themes    []RegistryPack `json:"themes,omitempty"`
}

// RegistryURL returns GOREX_PACK_REGISTRY when set, else DefaultRegistryURL.
func RegistryURL() string {
	if u := strings.TrimSpace(os.Getenv("GOREX_PACK_REGISTRY")); u != "" {
		return u
	}
	return DefaultRegistryURL
}

// FetchRegistry downloads and parses index.json from indexURL (or RegistryURL() when empty).
func FetchRegistry(ctx context.Context, indexURL string) (*RegistryIndex, error) {
	if indexURL == "" {
		indexURL = RegistryURL()
	}
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, indexURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry %s: HTTP %d", indexURL, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRegistryBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxRegistryBytes {
		return nil, fmt.Errorf("registry index exceeds %d bytes", maxRegistryBytes)
	}
	var idx RegistryIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, err
	}
	if idx.Schema != 1 {
		return nil, fmt.Errorf("unsupported registry schema %d", idx.Schema)
	}
	return &idx, nil
}

// SearchRegistry filters idx.Packs by case-insensitive substring match across ID,
// Name, Description, and Author.
func SearchRegistry(idx *RegistryIndex, query string) []RegistryPack {
	if idx == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		out := make([]RegistryPack, len(idx.Packs))
		copy(out, idx.Packs)
		return out
	}
	var out []RegistryPack
	for _, p := range idx.Packs {
		if strings.Contains(strings.ToLower(p.ID), q) ||
			strings.Contains(strings.ToLower(p.Name), q) ||
			strings.Contains(strings.ToLower(p.Description), q) ||
			strings.Contains(strings.ToLower(p.Author), q) {
			out = append(out, p)
		}
	}
	return out
}

// InstallFromRegistry clones rp.Repo (and optional rp.Subdir) and installs the
// validated pack into packsDir.
func InstallFromRegistry(ctx context.Context, rp RegistryPack, packsDir, overrideID string) (*Pack, error) {
	id := overrideID
	if id == "" {
		id = rp.ID
	}
	if !ValidID(id) {
		return nil, fmt.Errorf("invalid pack id %q", id)
	}
	var pk *Pack
	err := Checkout(ctx, rp, func(dir string) (err error) {
		pk, err = InstallLocal(dir, packsDir, id)
		return err
	})
	return pk, err
}

// Checkout clones rp.Repo shallowly and calls install with the pack's
// directory, rp.Subdir inside the clone, which it then deletes. A theme
// pack installs through it as an icon pack does. With rp.Ref it fetches
// that commit alone and checks that the clone is at it; when the fetch
// fails, the install fails, with no fall back to the default branch.
func Checkout(ctx context.Context, rp RegistryPack, install func(dir string) error) error {
	if rp.Repo == "" || strings.HasPrefix(rp.Repo, "-") {
		return fmt.Errorf("invalid pack repository %q", rp.Repo)
	}
	if rp.Ref != "" && !validRef(rp.Ref) {
		return fmt.Errorf("invalid pack ref %q: want a full commit SHA", rp.Ref)
	}
	if rp.Ref == "" && !isRegistryRepo(rp.Repo) {
		return fmt.Errorf("pack repository %q is not gorex-registry: it needs a ref", rp.Repo)
	}
	tmp, err := os.MkdirTemp("", "gorex-reg-pack-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if canon, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = canon
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := fetch(cmdCtx, rp, tmp); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(tmp, ".git"))
	srcDir := tmp
	if rp.Subdir != "" {
		resolved, err := resolveInside(tmp, rp.Subdir)
		if err != nil {
			return fmt.Errorf("subdir %q: %w", rp.Subdir, err)
		}
		srcDir = resolved
	}
	return install(srcDir)
}

// LookupRegistry finds a pack by ID in idx: an icon pack, else a theme
// pack, whose Kind is then KindTheme.
func LookupRegistry(idx *RegistryIndex, id string) (RegistryPack, error) {
	if idx == nil {
		return RegistryPack{}, errors.New("empty registry")
	}
	for _, p := range idx.Packs {
		if strings.EqualFold(p.ID, id) {
			return p, nil
		}
	}
	for _, p := range idx.Themes {
		if strings.EqualFold(p.ID, id) {
			p.Kind = KindTheme
			return p, nil
		}
	}
	return RegistryPack{}, fmt.Errorf("pack %q not found in registry", id)
}

// validRef reports whether ref is a full commit SHA: 40 hex digits, so
// that it cannot be a branch or a tag, which the repo's owner can move.
func validRef(ref string) bool {
	if len(ref) != 40 {
		return false
	}
	for _, c := range []byte(ref) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// fetch puts rp.Repo into dir: a shallow clone of the default branch, or
// with rp.Ref a shallow fetch of that commit, checked out detached.
func fetch(ctx context.Context, rp RegistryPack, dir string) error {
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		if rp.Ref != "" {
			cmd.Dir = dir
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	}
	if rp.Ref == "" {
		_, err := git("clone", "--depth", "1", "--", rp.Repo, dir)
		return err
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", rp.Repo},
		{"fetch", "-q", "--depth", "1", "origin", rp.Ref},
		{"checkout", "-q", "--detach", "FETCH_HEAD"},
	} {
		if _, err := git(args...); err != nil {
			return err
		}
	}
	head, err := git("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != rp.Ref {
		return fmt.Errorf("git checkout: at %s, want %s", head, rp.Ref)
	}
	return nil
}

// RegistryRepo is the repo of gorex-registry, whose own packs install from
// its default branch, which the registry's review guards. A pack of any
// other repo installs only at a ref.
const RegistryRepo = "https://github.com/si-mods/gorex-registry.git"

// isRegistryRepo reports whether repo is gorex-registry's.
func isRegistryRepo(repo string) bool {
	return repo == RegistryRepo || repo == strings.TrimSuffix(RegistryRepo, ".git")
}
