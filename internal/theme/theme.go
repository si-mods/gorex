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
	"io/fs"
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
	// The roles of docs/DSK-012-theme-shaders.md: four faces and the pane's
	// border. The built-in palette has none of them: unset, each draws as
	// the app did before.
	TitleBarBackground           // titleBar.background
	SideBarBackground            // sideBar.background
	PaneHeaderBackground         // paneHeader.background
	ButtonBackground             // button.background
	DropdownBackground           // dropdown.background
	SegmentedBackground          // segmented.background
	WindowControlBackground      // windowControl.background
	WindowControlCloseBackground // windowControl.closeBackground
	PaneBorder                   // pane.border
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
	"titleBar.background",
	"sideBar.background",
	"paneHeader.background",
	"button.background",
	"dropdown.background",
	"segmented.background",
	"windowControl.background",
	"windowControl.closeBackground",
	"pane.border",
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
// Assets names the files of the pack that its shaders read
// (docs/DSK-010-theme-ambience.md, phase 1). Metrics, Glass and Shaders
// are the keys of docs/DSK-012-theme-shaders.md: nil when the manifest has
// none.
type Manifest struct {
	Name string
	// Description is a line about the theme that the Extensions page shows
	// under its name, "" for none (docs/DSK-012-theme-shaders.md).
	Description string
	Seeds       [2]map[Seed]ui.Color
	Roles       [2]map[Role]ui.Color
	Terminal    [2]string
	Sidebar     Sidebar
	Assets      map[string]Asset
	Metrics     *Metrics
	Glass       *bool
	Shaders     *Shaders
}

// Sidebar holds the sidebar.* settings of docs/DSK-007-sidebar-style.md
// that a theme suggests. A metric of 0 and an enum of "" is unset, as in
// settings.json. The user's settings.json wins over each value, as its
// sidebar.colorCustomizations win over the theme's roles.
type Sidebar struct {
	RowHeight float32 `json:"rowHeight"`
	Indent    float32 `json:"indent"`
	TabIndent float32 `json:"tabIndent"`
	FontSize  float32 `json:"fontSize"`
	GroupPad  float32 `json:"groupPadding"`
	GroupBy   string  `json:"groupBy"`
	GroupFace string  `json:"groupBackground"`
	Icons     string  `json:"icons"`
	Selection string  `json:"selection"`
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

// maxDescription is the most characters the description key keeps.
const maxDescription = 160

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
	known := []string{"name", "seeds", "roles", "terminal", "sidebar", "assets", "metrics", "glass", "shaders", "description"}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, nil, err
	}
	var warn []string
	for k := range top {
		if !slices.Contains(known, k) {
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
	w := warner(warn)
	if raw, ok := top["sidebar"]; ok {
		m.Sidebar = parseSidebar(raw, &w)
	}
	if raw, ok := top["assets"]; ok {
		m.Assets = parseAssets(raw, &w)
	}
	if raw, ok := top["metrics"]; ok {
		m.Metrics = parseMetrics(raw, &w)
	}
	if raw, ok := top["glass"]; ok {
		m.Glass = parseGlass(raw, &w)
	}
	if raw, ok := top["description"]; ok {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			w.add("description: not a string")
		} else {
			var changed bool
			if m.Description, changed = cleanText(s, maxDescription); changed {
				w.add("description: control characters removed or cut to %d characters", maxDescription)
			}
		}
	}
	if raw, ok := top["shaders"]; ok {
		m.Shaders = parseShaders(raw, m.Assets, m.Roles, &w)
	}
	warn = w
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
// '-', '_' and '.', not "." or "..", so that it never leaves Dir, and not
// ending in ".json", so that no theme's name is another one's file.
func ValidName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) >= 5 && strings.EqualFold(name[len(name)-5:], ".json") {
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
// dir as <name>.json, so that appearance.theme can name it, and each asset
// it names to the folder <dir>/<name> (docs/DSK-010-theme-ambience.md),
// which it writes as a new folder and renames. A manifest that is not a
// manifest fails; one with dropped entries or assets installs, with the
// warnings.
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
	m, warn, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", PackFile, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	// The assets go to a new folder first, so that a pack that cannot copy
	// them changes nothing; the manifest then, and the folder after it, so
	// that List never sees the folder without its manifest.
	folder, tmp := assetFolder(dst), ""
	if len(m.Assets) > 0 {
		var w []string
		if tmp, w, err = copyAssets(srcDir, dir, m); err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		warn = append(warn, w...)
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
	if tmp == "" {
		err = os.RemoveAll(folder)
	} else {
		err = replaceDir(tmp, folder)
	}
	if err != nil {
		return nil, err
	}
	removeOrphanAssets(dir)
	return warn, nil
}

// Summary is an installed theme as a list shows it.
type Summary struct {
	ID   string // the file's name, which appearance.theme takes
	Name string // the manifest's name, or "" when it has none
	// Description is the manifest's description, or "".
	Description string
	Error       string // why it does not load, or ""
}

// List returns the themes installed in dir, by ID. It changes nothing.
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
			s.Name, s.Description = m.Name, m.Description
		}
		out = append(out, s)
	}
	return out
}

// Remove deletes the installed theme id from dir, its assets too.
func Remove(dir, id string) error {
	p, err := Path(dir, id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		return err
	}
	if err := os.RemoveAll(assetFolder(p)); err != nil {
		return err
	}
	removeOrphanAssets(dir)
	return nil
}

// assetFolderSuffix ends the name of a theme's asset folder, <id>.assets,
// which no manifest's name, <id>.json, can be.
const assetFolderSuffix = ".assets"

// assetFolder is the asset folder of the theme whose manifest is at path.
func assetFolder(manifest string) string {
	return strings.TrimSuffix(manifest, ".json") + assetFolderSuffix
}

// removeOrphanAssets removes each asset folder in dir whose manifest is
// gone, as an older build's Remove leaves it. It touches only names that
// end in assetFolderSuffix, and is for Install and Remove, never List.
func removeOrphanAssets(dir string) {
	des, _ := os.ReadDir(dir)
	for _, de := range des {
		id, ok := strings.CutSuffix(de.Name(), assetFolderSuffix)
		if !ok || !de.IsDir() || !ValidName(id) || strings.HasPrefix(id, ".") {
			continue
		}
		if _, err := os.Lstat(filepath.Join(dir, id+".json")); errors.Is(err, fs.ErrNotExist) {
			os.RemoveAll(filepath.Join(dir, de.Name()))
		}
	}
}

// parseSidebar reads the sidebar key: the sidebar.* keys of
// docs/DSK-007-sidebar-style.md, with the ranges and values of
// settings.json's load. An out-of-range number and a bad enum drop, as
// settings.json's do.
func parseSidebar(raw json.RawMessage, w *warner) Sidebar {
	var s Sidebar
	m, ok := w.fields(raw, "sidebar", "rowHeight", "indent", "tabIndent", "fontSize", "groupPadding", "groupBy", "groupBackground", "icons", "selection")
	if !ok {
		return s
	}
	metric := func(key string, lo, hi float32) float32 {
		raw, ok := m[key]
		if !ok {
			return 0
		}
		var f float64
		if json.Unmarshal(raw, &f) != nil {
			w.add("sidebar.%s: not a number", key)
			return 0
		}
		if v := float32(f); v != 0 && (v < lo || v > hi) {
			w.add("sidebar.%s: %g outside %g–%g", key, f, lo, hi)
			return 0
		}
		return float32(f)
	}
	s.RowHeight = metric("rowHeight", 24, 48)
	s.Indent = metric("indent", 0, 24)
	s.TabIndent = metric("tabIndent", 0, 24)
	s.FontSize = metric("fontSize", 10, 16)
	s.GroupPad = metric("groupPadding", 0, 12)
	enum := func(key string, vals ...string) string {
		raw, ok := m[key]
		if !ok {
			return ""
		}
		var v string
		if json.Unmarshal(raw, &v) != nil || !slices.Contains(vals, v) {
			w.add("sidebar.%s: bad value", key)
			return ""
		}
		return v
	}
	s.GroupBy = enum("groupBy", "host", "flat")
	s.GroupFace = enum("groupBackground", "face", "none")
	s.Icons = enum("icons", "tiles", "text")
	s.Selection = enum("selection", "pill", "fill")
	return s
}
