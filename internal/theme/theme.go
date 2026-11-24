// Package theme reads a theme manifest of the desktop chrome
// (docs/DSK-008-theme.md): seeds for each appearance and role overrides,
// as data. It holds the one allowlist of seed and role names; the app maps
// each name to a field of its palette.
package theme

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

// Seed names a seed of the palette (DSK-008, Model).
type Seed int

// The seeds, in the order of the doc's seed table.
const (
	BgTop Seed = iota
	BgMid
	BgLow
	BgBottom
	BgTint
	Text
	TextMuted
	TextFaint
	IconMuted
	EmptyMark
	Busy
	Attention
	Done
	Exited
	Danger
	Accent
	AccentHover
	AccentPressed
	AccentText
	Panel
	Field
	FieldBorder
	Surface
	SurfaceHover
	SurfacePressed
	Card
	CardFocused
	Shadow
	ShadowFocused
	Backdrop
	PillTint
	NumSeeds
)

var seedNames = [NumSeeds]string{
	"bgTop", "bgMid", "bgLow", "bgBottom", "bgTint",
	"text", "textMuted", "textFaint", "iconMuted", "emptyMark",
	"busy", "attention", "done", "exited", "danger",
	"accent", "accentHover", "accentPressed", "accentText",
	"panel", "field", "fieldBorder", "surface", "surfaceHover", "surfacePressed",
	"card", "cardFocused", "shadow", "shadowFocused", "backdrop", "pillTint",
}

func (s Seed) String() string { return seedNames[s] }

// SeedByName returns the seed a manifest names.
func SeedByName(name string) (Seed, bool) {
	i := slices.Index(seedNames[:], name)
	return Seed(i), i >= 0
}

// Role names a role a manifest or a *.colorCustomizations key may set:
// the VS Code style names of DSK-008's Role names.
type Role int

// The roles.
const (
	ActiveGroupBackground    Role = iota // sideBar.activeGroupBackground
	ActiveGroupTabBackground             // sideBar.activeGroupTabBackground
	ActiveGroupTabShadow                 // sideBar.activeGroupTabShadow
	TrackBackground                      // sideBar.trackBackground
	TabSeparator                         // sideBar.tabSeparator
	TabHoverBackground                   // sideBar.tabHoverBackground
	TabActiveBackground                  // sideBar.tabActiveBackground
	NumRoles
)

var roleNames = [NumRoles]string{
	"sideBar.activeGroupBackground",
	"sideBar.activeGroupTabBackground",
	"sideBar.activeGroupTabShadow",
	"sideBar.trackBackground",
	"sideBar.tabSeparator",
	"sideBar.tabHoverBackground",
	"sideBar.tabActiveBackground",
}

func (r Role) String() string { return roleNames[r] }

// RoleByName returns the role a name stands for.
func RoleByName(name string) (Role, bool) {
	i := slices.Index(roleNames[:], name)
	return Role(i), i >= 0
}

// Hex parses "#rgb", "#rgba", "#rrggbb" or "#rrggbbaa", without ui.Hex's
// panic on other input.
func Hex(s string) (col ui.Color, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return ui.Hex(s), true
}

// ColorFor resolves a role value for one appearance: a hex string, or an
// object {"light","dark"} with a value for each.
func ColorFor(raw json.RawMessage, dark bool) (ui.Color, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return Hex(s)
	}
	var pair struct {
		Light string `json:"light"`
		Dark  string `json:"dark"`
	}
	if json.Unmarshal(raw, &pair) != nil {
		return ui.Color{}, false
	}
	s = pair.Light
	if dark {
		s = pair.Dark
	}
	return Hex(s)
}

// Manifest is a parsed theme: the colors it sets, light at index 0 and
// dark at index 1. A seed or role it does not set keeps the built-in one.
// Terminal names the Ghostty theme of each appearance, or "" for none.
type Manifest struct {
	Name     string
	Seeds    [2]map[Seed]ui.Color
	Roles    [2]map[Role]ui.Color
	Terminal [2]string
}

// TerminalThemeKnown reports whether Ghostty has a theme of the name. The
// app sets it; while it is nil, Parse checks only the form of a name.
var TerminalThemeKnown func(name string) bool

// maxTerminalName is the longest terminal theme name a manifest may give.
const maxTerminalName = 64

// validTerminalName reports whether name may name a Ghostty theme: no path,
// no comma or quote of Ghostty's light:<name>,dark:<name> syntax, and no
// control character.
func validTerminalName(name string) bool {
	if name == "" || len(name) > maxTerminalName || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`/\,"`, r) {
			return false
		}
	}
	return true
}

const maxManifestBytes = 64 << 10

// Parse reads a manifest. It drops each bad color, unknown seed, unknown
// role and unknown key, with a warning for each; it fails only on input
// that is not a JSON object of the manifest's shape.
func Parse(b []byte) (*Manifest, []string, error) {
	if len(b) > maxManifestBytes {
		return nil, nil, fmt.Errorf("theme exceeds %d bytes", maxManifestBytes)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, nil, err
	}
	var raw struct {
		Name  string                                `json:"name"`
		Seeds map[string]map[string]json.RawMessage `json:"seeds"`
		Roles map[string]json.RawMessage            `json:"roles"`
		Term  map[string]json.RawMessage            `json:"terminal"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, nil, err
	}
	var warn []string
	for k := range top {
		if k != "name" && k != "seeds" && k != "roles" && k != "terminal" {
			warn = append(warn, fmt.Sprintf("unknown key %q", k))
		}
	}
	m := &Manifest{Name: raw.Name}
	for i, app := range [2]string{"light", "dark"} {
		m.Seeds[i] = map[Seed]ui.Color{}
		m.Roles[i] = map[Role]ui.Color{}
		for name, v := range raw.Seeds[app] {
			id, ok := SeedByName(name)
			if !ok {
				warn = append(warn, fmt.Sprintf("seeds.%s: unknown seed %q", app, name))
				continue
			}
			var s string
			col, ok := ui.Color{}, json.Unmarshal(v, &s) == nil
			if ok {
				col, ok = Hex(s)
			}
			if !ok {
				warn = append(warn, fmt.Sprintf("seeds.%s.%s: bad color", app, name))
				continue
			}
			m.Seeds[i][id] = col
		}
	}
	for app := range raw.Seeds {
		if app != "light" && app != "dark" {
			warn = append(warn, fmt.Sprintf("seeds: unknown appearance %q", app))
		}
	}
	for name, v := range raw.Roles {
		id, ok := RoleByName(name)
		if !ok {
			warn = append(warn, fmt.Sprintf("roles: unknown role %q", name))
			continue
		}
		light, lok := ColorFor(v, false)
		dark, dok := ColorFor(v, true)
		if !lok || !dok {
			warn = append(warn, fmt.Sprintf("roles.%s: bad color", name))
			continue
		}
		m.Roles[0][id], m.Roles[1][id] = light, dark
	}
	for app, v := range raw.Term {
		i := slices.Index([]string{"light", "dark"}, app)
		if i < 0 {
			warn = append(warn, fmt.Sprintf("terminal: unknown appearance %q", app))
			continue
		}
		var name string
		switch {
		case json.Unmarshal(v, &name) != nil || !validTerminalName(name):
			warn = append(warn, fmt.Sprintf("terminal.%s: bad theme name", app))
		case TerminalThemeKnown != nil && !TerminalThemeKnown(name):
			warn = append(warn, fmt.Sprintf("terminal.%s: unknown Ghostty theme %q", app, name))
		default:
			m.Terminal[i] = name
		}
	}
	slices.Sort(warn)
	return m, warn, nil
}

// Load reads the manifest at path, as Parse does.
func Load(path string) (*Manifest, []string, error) {
	b, err := readManifest(path)
	if err != nil {
		return nil, nil, err
	}
	return Parse(b)
}

// readManifest reads at most one byte more than a manifest may have, so
// that Parse refuses a larger file without reading all of it.
func readManifest(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
}

// Dir is the folder of the installed themes under the config directory:
// <config>/themes, or "" when there is no config directory.
func Dir(configDir string) string {
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, "themes")
}

// ValidName reports whether name may name a theme file: letters, digits,
// '-', '_' and '.', not "." or "..", so that it never leaves Dir.
func ValidName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

// Path is the file of the theme name in dir: <dir>/<name>.json.
func Path(dir, name string) (string, error) {
	if dir == "" {
		return "", errors.New("no config directory")
	}
	if !ValidName(name) {
		return "", fmt.Errorf("invalid theme name %q", name)
	}
	return filepath.Join(dir, name+".json"), nil
}

// PackFile is the manifest's file in a theme pack's directory.
const PackFile = "theme.json"

// Install validates the manifest of the pack in srcDir and copies it to
// dir as <name>.json, so that appearance.theme can name it. A manifest
// that is not a manifest fails; one with dropped entries installs, with
// the warnings.
func Install(srcDir, dir, name string) ([]string, error) {
	dst, err := Path(dir, name)
	if err != nil {
		return nil, err
	}
	src := filepath.Join(srcDir, PackFile)
	if st, err := os.Lstat(src); err != nil {
		return nil, err
	} else if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", PackFile)
	}
	b, err := readManifest(src)
	if err != nil {
		return nil, err
	}
	_, warn, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", PackFile, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, ".install-*")
	if err != nil {
		return nil, err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), dst)
	}
	if err != nil {
		os.Remove(f.Name())
		return nil, err
	}
	return warn, nil
}

// Summary is an installed theme as a list shows it.
type Summary struct {
	ID    string // the file's name, which appearance.theme takes
	Name  string // the manifest's name, or "" when it has none
	Error string // why it does not load, or ""
}

// List returns the themes installed in dir, by ID.
func List(dir string) []Summary {
	des, _ := os.ReadDir(dir)
	var out []Summary
	for _, de := range des {
		id, ok := strings.CutSuffix(de.Name(), ".json")
		if !ok || !de.Type().IsRegular() || !ValidName(id) {
			continue
		}
		s := Summary{ID: id}
		if m, _, err := Load(filepath.Join(dir, de.Name())); err != nil {
			s.Error = err.Error()
		} else {
			s.Name = m.Name
		}
		out = append(out, s)
	}
	return out
}

// Remove deletes the installed theme id from dir.
func Remove(dir, id string) error {
	p, err := Path(dir, id)
	if err != nil {
		return err
	}
	return os.Remove(p)
}
