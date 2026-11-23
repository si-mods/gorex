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

// RegistryPack describes one community icon pack listed in index.json.
type RegistryPack struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	Version     string `json:"version,omitempty"`
	Repo        string `json:"repo"`
	Subdir      string `json:"subdir,omitempty"`
	Entries     int    `json:"entries,omitempty"`
}

// RegistryIndex is the schema of gorex-registry index.json.
type RegistryIndex struct {
	Schema    int            `json:"schema"`
	UpdatedAt string         `json:"updated_at,omitempty"`
	Packs     []RegistryPack `json:"packs"`
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
	if rp.Repo == "" || strings.HasPrefix(rp.Repo, "-") {
		return nil, fmt.Errorf("invalid pack repository %q", rp.Repo)
	}
	id := overrideID
	if id == "" {
		id = rp.ID
	}
	if !ValidID(id) {
		return nil, fmt.Errorf("invalid pack id %q", id)
	}
	tmp, err := os.MkdirTemp("", "gorex-reg-pack-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if canon, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = canon
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "git", "clone", "--depth", "1", "--", rp.Repo, tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git clone: %w: %s", err, strings.TrimSpace(string(out)))
	}
	_ = os.RemoveAll(filepath.Join(tmp, ".git"))
	srcDir := tmp
	if rp.Subdir != "" {
		resolved, err := resolveInside(tmp, rp.Subdir)
		if err != nil {
			return nil, fmt.Errorf("subdir %q: %w", rp.Subdir, err)
		}
		srcDir = resolved
	}
	return InstallLocal(srcDir, packsDir, id)
}

// LookupRegistry finds a pack by ID in idx.
func LookupRegistry(idx *RegistryIndex, id string) (RegistryPack, error) {
	if idx == nil {
		return RegistryPack{}, errors.New("empty registry")
	}
	for _, p := range idx.Packs {
		if strings.EqualFold(p.ID, id) {
			return p, nil
		}
	}
	return RegistryPack{}, fmt.Errorf("pack %q not found in registry", id)
}
