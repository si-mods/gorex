// Package iconpack loads and resolves declarative community program icon
// packs (docs/MOD-012-program-icon-packs.md).
package iconpack

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gorex/internal/config"
	"gorex/internal/rex"
)

const (
	maxManifestBytes = 1 << 20   // 1 MiB
	maxAssetBytes    = 32 * 1024 // matches rex maxIconBytes
)

// ColorVariant overrides tile colors for light or dark appearance.
type ColorVariant struct {
	Bg string `json:"bg,omitempty"`
	Fg string `json:"fg,omitempty"`
}

// Entry maps one program or subcommand in pack.json to an icon asset.
type Entry struct {
	Icon   string        `json:"icon"`
	Format string        `json:"format,omitempty"`
	Mode   string        `json:"mode,omitempty"`
	Bg     string        `json:"bg,omitempty"`
	Fg     string        `json:"fg,omitempty"`
	Light  *ColorVariant `json:"light,omitempty"`
	Dark   *ColorVariant `json:"dark,omitempty"`
}

// Manifest is the pack.json schema.
type Manifest struct {
	Schema   int              `json:"schema"`
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Version  string           `json:"version,omitempty"`
	Author   string           `json:"author,omitempty"`
	Programs map[string]Entry `json:"programs"`
}

type resolvedPair struct {
	light *rex.ProgramIcon
	dark  *rex.ProgramIcon
}

func (p resolvedPair) forDark(dark bool) *rex.ProgramIcon {
	if dark && p.dark != nil {
		return p.dark
	}
	if !dark && p.light != nil {
		return p.light
	}
	if p.light != nil {
		return p.light
	}
	return p.dark
}

// Pack is a validated, loaded icon pack.
type Pack struct {
	Manifest
	Dir     string
	entries map[string]resolvedPair
}

// Summary describes an installed pack directory for CLI listing.
type Summary struct {
	ID      string `json:"id"`
	Kind    string `json:"kind,omitempty"` // "icon-pack" or "theme"; set by `gorex pack ls`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Author  string `json:"author,omitempty"`
	Entries int    `json:"entries"`
	Dir     string `json:"dir"`
	Error   string `json:"error,omitempty"`
}

// Registry resolves program icons across explicit Lua overrides, active icon
// packs, and runtime OSC 7502 icons.
type Registry struct {
	priority  string // "pack" (default) or "osc"
	overrides map[string]resolvedPair
	packs     []*Pack
}

// Dir returns the default directory holding installed icon packs, or "" when
// config.Dir() is unavailable.
func Dir() string {
	base := config.Dir()
	if base == "" {
		return ""
	}
	return filepath.Join(base, "icon-packs")
}

// ValidID reports whether id is safe as a pack directory name.
func ValidID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.'
		if !ok {
			return false
		}
	}
	return true
}

// LoadPack reads and validates a pack from dir.
func LoadPack(dir string) (*Pack, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(root, "pack.json")
	f, err := os.Open(manifestPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxManifestBytes {
		return nil, fmt.Errorf("pack.json exceeds %d bytes", maxManifestBytes)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m.Schema != 1 {
		return nil, fmt.Errorf("unsupported pack schema %d (want 1)", m.Schema)
	}
	if m.ID == "" {
		m.ID = filepath.Base(root)
	}
	if !ValidID(m.ID) {
		return nil, fmt.Errorf("invalid pack id %q", m.ID)
	}
	entries := make(map[string]resolvedPair, len(m.Programs))
	for rawKey, e := range m.Programs {
		key := normalizeKey(rawKey)
		if key == "" {
			return nil, fmt.Errorf("empty program key in %s", m.ID)
		}
		assetPath, err := resolveInside(root, e.Icon)
		if err != nil {
			return nil, fmt.Errorf("%s (%s): %w", m.ID, key, err)
		}
		iconBytes, err := readLimited(assetPath, maxAssetBytes)
		if err != nil {
			return nil, fmt.Errorf("%s (%s): %w", m.ID, key, err)
		}
		fmtName := detectFormat(e.Format, assetPath)
		pair, err := buildPair(fmtName, e.Mode, e.Bg, e.Fg, e.Light, e.Dark, iconBytes)
		if err != nil {
			return nil, fmt.Errorf("%s (%s): %w", m.ID, key, err)
		}
		entries[key] = pair
	}
	return &Pack{Manifest: m, Dir: root, entries: entries}, nil
}

// LoadRegistry loads active packs and explicit Lua overrides, returning any
// non-fatal warnings encountered.
func LoadRegistry(packsDir, configDir string, packIDs []string, overrides []config.ProgramIconRule, priority string) (*Registry, []string) {
	p := strings.ToLower(strings.TrimSpace(priority))
	if p != "osc" {
		p = "pack"
	}
	reg := &Registry{
		priority:  p,
		overrides: map[string]resolvedPair{},
	}
	var warnings []string
	for _, rule := range overrides {
		key := normalizeKey(rule.Name)
		if key == "" {
			continue
		}
		assetPath := rule.Icon
		if !filepath.IsAbs(assetPath) && configDir != "" {
			assetPath = filepath.Join(configDir, assetPath)
		}
		raw, err := readLimited(assetPath, maxAssetBytes)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("program_icon %q: %v", rule.Name, err))
			continue
		}
		fmtName := detectFormat(rule.Format, assetPath)
		ic, ok := rex.BuildProgramIcon(fmtName, rule.Mode, rule.Bg, rule.Fg, raw)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("program_icon %q: invalid icon asset %s", rule.Name, assetPath))
			continue
		}
		reg.overrides[key] = resolvedPair{light: ic, dark: ic}
	}
	if packsDir != "" {
		for _, id := range packIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if !ValidID(id) {
				warnings = append(warnings, fmt.Sprintf("icon pack %q: invalid id", id))
				continue
			}
			pk, err := LoadPack(filepath.Join(packsDir, id))
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("icon pack %q: %v", id, err))
				continue
			}
			reg.packs = append(reg.packs, pk)
		}
	}
	return reg, warnings
}

// Resolve picks the custom icon for a program, respecting explicit overrides,
// active packs, and runtime OSC 7502 icons according to configured priority.
func (r *Registry) Resolve(progName string, args []string, oscIcon *rex.ProgramIcon, dark bool) *rex.ProgramIcon {
	if r == nil {
		return oscIcon
	}
	keys := candidateKeys(progName, args)
	if ic := lookupPairs(r.overrides, keys, dark); ic != nil {
		return ic
	}
	if r.priority == "osc" && oscIcon != nil {
		return oscIcon
	}
	for _, pk := range r.packs {
		if ic := lookupPairs(pk.entries, keys, dark); ic != nil {
			return ic
		}
	}
	return oscIcon
}

// ListPacks inspects all subdirectories in packsDir.
func ListPacks(packsDir string) ([]Summary, error) {
	if packsDir == "" {
		return nil, nil
	}
	des, err := os.ReadDir(packsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Summary, 0, len(des))
	for _, de := range des {
		if !de.IsDir() {
			continue
		}
		dir := filepath.Join(packsDir, de.Name())
		pk, err := LoadPack(dir)
		if err != nil {
			out = append(out, Summary{ID: de.Name(), Dir: dir, Error: err.Error()})
			continue
		}
		out = append(out, Summary{
			ID:      pk.ID,
			Name:    pk.Name,
			Version: pk.Version,
			Author:  pk.Author,
			Entries: len(pk.entries),
			Dir:     dir,
		})
	}
	slices.SortFunc(out, func(a, b Summary) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// InstallLocal validates srcDir as a pack and copies it into packsDir.
func InstallLocal(srcDir, packsDir, overrideID string) (*Pack, error) {
	if packsDir == "" {
		return nil, errors.New("icon-packs directory is not configured")
	}
	pk, err := LoadPack(srcDir)
	if err != nil {
		return nil, err
	}
	id := pk.ID
	if overrideID != "" {
		if !ValidID(overrideID) {
			return nil, fmt.Errorf("invalid pack id %q", overrideID)
		}
		id = overrideID
	}
	dst := filepath.Join(packsDir, id)
	if err := os.MkdirAll(packsDir, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(packsDir, ".install-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := copyDir(pk.Dir, tmp); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(dst); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return nil, err
	}
	return LoadPack(dst)
}

// InstallGit clones repoURL shallowly, validates it as a pack, and installs it.
func InstallGit(repoURL, packsDir, overrideID string) (*Pack, error) {
	tmp, err := os.MkdirTemp("", "gorex-pack-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	cmd := exec.Command("git", "clone", "--depth", "1", "--", repoURL, tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git clone: %w: %s", err, strings.TrimSpace(string(out)))
	}
	_ = os.RemoveAll(filepath.Join(tmp, ".git"))
	return InstallLocal(tmp, packsDir, overrideID)
}

// Remove deletes an installed pack directory by id.
func Remove(packsDir, id string) error {
	if packsDir == "" {
		return errors.New("icon-packs directory is not configured")
	}
	if !ValidID(id) {
		return fmt.Errorf("invalid pack id %q", id)
	}
	dst := filepath.Join(packsDir, id)
	if _, err := os.Stat(dst); err != nil {
		return err
	}
	return os.RemoveAll(dst)
}

func buildPair(format, mode, bg, fg string, light, dark *ColorVariant, raw []byte) (resolvedPair, error) {
	lbg, lfg := bg, fg
	if light != nil {
		if light.Bg != "" {
			lbg = light.Bg
		}
		if light.Fg != "" {
			lfg = light.Fg
		}
	}
	dbg, dfg := bg, fg
	if dark != nil {
		if dark.Bg != "" {
			dbg = dark.Bg
		}
		if dark.Fg != "" {
			dfg = dark.Fg
		}
	}
	lic, ok := rex.BuildProgramIcon(format, mode, lbg, lfg, raw)
	if !ok {
		return resolvedPair{}, errors.New("invalid icon asset or color")
	}
	dic := lic
	if dbg != lbg || dfg != lfg {
		dic, ok = rex.BuildProgramIcon(format, mode, dbg, dfg, raw)
		if !ok {
			return resolvedPair{}, errors.New("invalid dark color override")
		}
	}
	return resolvedPair{light: lic, dark: dic}, nil
}

func resolveInside(root, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("empty icon path")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("icon path %q must be relative", rel)
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("icon path %q escapes pack root", rel)
	}
	joined := filepath.Join(root, clean)
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", err
	}
	within, err := filepath.Rel(root, resolved)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) || filepath.IsAbs(within) {
		return "", fmt.Errorf("icon path %q resolves outside pack root", rel)
	}
	return resolved, nil
}

func detectFormat(explicit, assetPath string) string {
	if explicit != "" {
		return strings.ToLower(strings.TrimSpace(explicit))
	}
	switch strings.ToLower(filepath.Ext(assetPath)) {
	case ".svg":
		return "svg"
	case ".png":
		return "png"
	default:
		return ""
	}
}

func normalizeKey(s string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(s)))
	return strings.Join(fields, " ")
}

func candidateKeys(progName string, args []string) []string {
	base := strings.ToLower(strings.TrimPrefix(path.Base(strings.ReplaceAll(progName, "\\", "/")), "-"))
	base = strings.TrimSuffix(base, ".exe")
	if base == "" {
		return nil
	}
	if len(args) > 1 {
		sub := strings.TrimSpace(args[1])
		if sub != "" && !strings.HasPrefix(sub, "-") {
			return []string{base + " " + strings.ToLower(sub), base}
		}
	}
	return []string{base}
}

func lookupPairs(m map[string]resolvedPair, keys []string, dark bool) *rex.ProgramIcon {
	for _, k := range keys {
		if pair, ok := m[k]; ok {
			if ic := pair.forDark(dark); ic != nil {
				return ic
			}
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func readLimited(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", maxBytes)
	}
	return b, nil
}
