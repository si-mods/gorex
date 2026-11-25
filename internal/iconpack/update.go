package iconpack

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gorex/internal/ext"
	"gorex/internal/theme"
)

// RecordFile is the install record of a pack installed from the registry
// (docs/MOD-012-program-icon-packs.md, Updates). It lives in the pack's
// own folder: <icon-packs>/<id>/ for an icon pack, <themes>/<id>.assets/
// for a theme. So every build that replaces or removes the pack replaces
// or removes the record with it, and no build lists it as a pack.
const RecordFile = ".gorex-install.json"

// Record is what an install from the registry wrote: the registry entry's
// ID, kind, version and pinned ref. Each field is optional. A pack with no
// record has an unknown version.
type Record struct {
	ID      string `json:"id"`
	Kind    string `json:"kind,omitempty"` // "icon-pack", "theme" or "extension"
	Version string `json:"version,omitempty"`
	Ref     string `json:"ref,omitempty"`
}

// recordKindIconPack is the Kind a record gives an icon pack.
const recordKindIconPack = "icon-pack"

// RecordDir is the folder that holds the record of the installed pack id
// of kind (KindTheme, KindExtension, or "" for an icon pack).
func RecordDir(kind, packsDir, themesDir, extsDir, id string) (string, error) {
	if kind == KindTheme {
		return theme.AssetFolder(themesDir, id)
	}
	dir := packsDir
	if kind == KindExtension {
		dir = extsDir
	}
	if dir == "" {
		return "", fmt.Errorf("the %q packs directory is not configured", cmp.Or(kind, recordKindIconPack))
	}
	if !ValidID(id) {
		return "", fmt.Errorf("invalid pack id %q", id)
	}
	return filepath.Join(dir, id), nil
}

// ReadRecord reads the record in dir, or returns nil when there is none or
// it does not decode: the pack then has an unknown version.
func ReadRecord(dir string) *Record {
	b, err := os.ReadFile(filepath.Join(dir, RecordFile))
	if err != nil {
		return nil
	}
	var r Record
	if json.Unmarshal(b, &r) != nil || r.ID == "" {
		return nil
	}
	return &r
}

// writeRecord writes the record of rp into dir, which it makes when a
// theme has no assets.
func writeRecord(dir string, rp RegistryPack) error {
	kind := cmp.Or(rp.Kind, recordKindIconPack)
	b, err := json.MarshalIndent(Record{ID: rp.ID, Kind: kind, Version: rp.Version, Ref: rp.Ref}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, RecordFile), append(b, '\n'), 0o644)
}

// HasUpdate reports whether rp differs from what rec says was installed:
// by ref when the registry pins one, else by version. A pack with no
// record has an update, so that one install records it.
func HasUpdate(rec *Record, rp RegistryPack) bool {
	if rec == nil {
		return true
	}
	if rp.Ref != "" {
		return rec.Ref != rp.Ref
	}
	return rec.Version != rp.Version
}

// LookupKind finds the registry entry id of kind (KindTheme,
// KindExtension, or "" for an icon pack) in idx, with its Kind set.
func LookupKind(idx *RegistryIndex, kind, id string) (RegistryPack, bool) {
	if idx == nil {
		return RegistryPack{}, false
	}
	list := idx.Packs
	switch kind {
	case KindTheme:
		list = idx.Themes
	case KindExtension:
		list = idx.Extensions
	}
	for _, p := range list {
		if strings.EqualFold(p.ID, id) {
			p.Kind = kind
			return p, true
		}
	}
	return RegistryPack{}, false
}

// Install installs the registry entry rp as id (rp.ID when ""): an icon
// pack into packsDir, a theme into themesDir, an extension pack into
// extsDir, and records what it installed. It returns a theme's warnings.
// A theme in use stays in use: its file keeps its name. An extension
// pack's id is its manifest's; an id passed for one is ignored.
func Install(ctx context.Context, rp RegistryPack, packsDir, themesDir, extsDir, id string) (warn []string, err error) {
	id = cmp.Or(id, rp.ID)
	switch rp.Kind {
	case KindTheme:
		err = Checkout(ctx, rp, func(dir string) (err error) {
			warn, err = theme.Install(dir, themesDir, id)
			return err
		})
	case KindExtension:
		var pk *ext.Pack
		err = Checkout(ctx, rp, func(dir string) (err error) {
			pk, err = ext.Install(dir, extsDir)
			return err
		})
		if pk != nil {
			id = pk.Manifest.ID // the folder names itself by the manifest
		}
	default:
		_, err = InstallFromRegistry(ctx, rp, packsDir, id)
	}
	if err != nil {
		return warn, err
	}
	dir, err := RecordDir(rp.Kind, packsDir, themesDir, extsDir, id)
	if err == nil {
		err = writeRecord(dir, rp)
	}
	if err != nil {
		return warn, fmt.Errorf("installed %s, but did not record its version: %w", id, err)
	}
	return warn, nil
}
