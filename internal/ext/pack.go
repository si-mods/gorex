package ext

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Pack is an installed extension pack: its directory under the exts root
// and the manifest it validated.
type Pack struct {
	Dir      string
	Manifest *Manifest
}

// ID is the pack's id.
func (p *Pack) ID() string { return p.Manifest.ID }

// LoadAll loads every pack of extsRoot: each subdirectory with an
// ext.json. A directory whose manifest fails to read or validate is
// skipped, with its error reported so the caller can say why.
func LoadAll(extsRoot string) (packs []*Pack, errs []error) {
	entries, err := os.ReadDir(extsRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{err}
	}
	seen := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(extsRoot, e.Name())
		m, err := LoadManifest(dir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if m.ID != e.Name() {
			errs = append(errs, fmt.Errorf("pack %q: manifest id %q differs from its directory", e.Name(), m.ID))
			continue
		}
		for _, pd := range m.Panes {
			if who, taken := seen[pd.Kind]; taken {
				errs = append(errs, fmt.Errorf("pack %q: pane kind %q is %q's already", m.ID, pd.Kind, who))
				continue
			}
			seen[pd.Kind] = m.ID
		}
		packs = append(packs, &Pack{Dir: dir, Manifest: m})
	}
	return packs, errs
}

// Dir is the packs' install root inside configDir: <config>/exts.
func Dir(configDir string) string {
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, "exts")
}

// Remove deletes the installed pack id's directory under extsRoot.
func Remove(extsRoot, id string) error {
	if !idRe.MatchString(id) {
		return fmt.Errorf("invalid pack id %q", id)
	}
	return os.RemoveAll(filepath.Join(extsRoot, id))
}

// Installed describes one directory under the packs' root: the pack it
// loads, or the Error that keeps it from loading.
type Installed struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Dir     string `json:"dir"`
	Error   string `json:"error,omitempty"`
}

// List returns every directory under extsRoot, loadable or not.
func List(extsRoot string) []Installed {
	entries, err := os.ReadDir(extsRoot)
	if err != nil {
		return nil
	}
	var out []Installed
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(extsRoot, e.Name())
		it := Installed{ID: e.Name(), Dir: dir}
		if m, err := LoadManifest(dir); err != nil {
			it.Error = err.Error()
		} else {
			it.ID, it.Name, it.Version = m.ID, m.Name, m.Version
		}
		out = append(out, it)
	}
	return out
}

// ByKind is the pack serving the pane kind, nil when none does.
func ByKind(packs []*Pack, kind string) *Pack {
	for _, p := range packs {
		if p.Manifest.Pane(kind) != nil {
			return p
		}
	}
	return nil
}

// JailPath is the path name inside root, an error when it would leave
// it: names are relative and clean, and the path they land on stays
// inside root once symlinks resolve.
func JailPath(root, name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return "", fmt.Errorf("invalid file name %q", name)
	}
	// The page names files with slashes, on every platform.
	name = filepath.FromSlash(name)
	if c := filepath.Clean(name); c != name || strings.HasPrefix(c, "..") ||
		filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", fmt.Errorf("file name %q leaves the storage directory", name)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootAbs = r
	}
	dst := filepath.Join(rootAbs, name)
	// Resolve the deepest ancestor that exists, then walk back down: a
	// symlink inside root may not take the file out of it.
	p := dst
	var tail []string
	for {
		r, err := filepath.EvalSymlinks(p)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				r = filepath.Join(r, tail[i])
			}
			rel, err := filepath.Rel(rootAbs, r)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return "", fmt.Errorf("file name %q leaves the storage directory", name)
			}
			return r, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(p)
		if parent == p || parent == "." || (len(parent) < len(rootAbs)) {
			return "", fmt.Errorf("file name %q leaves the storage directory", name)
		}
		tail = append(tail, filepath.Base(p))
		p = parent
	}
}

// Install copies the pack in src, which holds a valid ext.json, into
// extsRoot under its manifest id, replacing what was there.
func Install(src, extsRoot string) (*Pack, error) {
	m, err := LoadManifest(src)
	if err != nil {
		return nil, err
	}
	dst := filepath.Join(extsRoot, m.ID)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(dst); err != nil {
		return nil, err
	}
	if err := copyDir(src, dst); err != nil {
		return nil, err
	}
	return &Pack{Dir: dst, Manifest: m}, nil
}

// copyDir copies src into dst, created; symlinks are not followed, so a
// pack cannot point out of itself.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if e.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		if e.Type()&os.ModeSymlink != 0 || !e.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		info, err := e.Info()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm()&0o777)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		cerr := out.Close()
		if err != nil {
			return err
		}
		return cerr
	})
}
