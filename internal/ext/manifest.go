// Package ext loads extension packs: folders with an ext.json manifest
// and the web page a pane of the pack shows (docs/EXT-001-extension-panes.md).
// The app registers each pack's pane kinds, hosts its page in a web view,
// and lets it call the scoped Extension bridge this manifest's
// Permissions allow.
package ext

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// File names the manifest inside a pack's directory.
const File = "ext.json"

// Engine is the only runtime a pack may declare: its page runs in a web
// view of the pane.
const Engine = "webview"

// APIVersion is the bridge this build speaks. A pack that asks for newer
// loads as disabled (docs/EXT-001, Open questions).
const APIVersion = 1

// Bridge permissions a manifest may ask for.
const (
	PermState   = "state"   // the pane's serialized fields
	PermStorage = "storage" // the pack's storage directory
	PermInvoke  = "invoke"  // the built-in actions the manifest lists
	PermWeb     = "web"     // the pane's untrusted web slot
	PermEnv     = "env"     // locale, appearance and surface colors
)

// SlotWeb is the only extra surface a pack may declare: a second,
// untrusted web view under the pack's page, for pages of the internet.
const SlotWeb = "web"

// StateFields are the layout-node fields a pane kind may declare as its
// state (docs/EXT-001, Serialization): the fields the note and the
// browser kinds use today. A generic state object is a later doc.
var StateFields = []string{"note", "noteSize", "notePreview", "url", "id"}

// CommandPlacements are where a pack command may offer a pane-spawning
// command: the New Tab menu and the Split menu.
var CommandPlacements = []string{"tab", "split"}

// Manifest is the ext.json of a pack (docs/EXT-001, The manifest).
type Manifest struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Version     string       `json:"version,omitempty"`
	Engine      string       `json:"engine"`
	API         int          `json:"api"`
	Panes       []PaneDef    `json:"panes"`
	Storage     StorageDef   `json:"storage,omitempty"`
	Slots       []string     `json:"slots,omitempty"`
	Permissions []string     `json:"permissions,omitempty"`
	Commands    []CommandDef `json:"commands,omitempty"`
}

// PaneDef is one pane kind the pack serves: kind is the Kind of a layout
// node; Fields, of StateFields, are the node's fields the pack reads and
// writes as its pane's state; Shareable lets the kind pass the layout
// filter to other clients, as a browser's url does (the filter applies
// it once packs can install); ChromeHeight is the page's height above a
// web slot, 0 for the pane header's height; OpaqueTiled is whether the
// pane draws opaque over the glass window, true unless the manifest
// says false; Files names the storage files a pane of the kind owns,
// patterns such as "{note}.txt" over its state fields, removed when the
// pane is closed for good.
type PaneDef struct {
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	Icon         string   `json:"icon,omitempty"`
	Shareable    bool     `json:"shareable,omitempty"`
	Fields       []string `json:"fields,omitempty"`
	ChromeHeight float32  `json:"chrome_height,omitempty"`
	OpaqueTiled  *bool    `json:"opaque_tiled,omitempty"`
	Files        []string `json:"files,omitempty"`
}

// StorageDef names the pack's data directory inside GoRex's data
// directory: "notes" for the notes of today, or the default
// exts/<id>/data when the manifest gives no dir.
type StorageDef struct {
	Dir string `json:"dir,omitempty"`
}

// CommandDef is a command the pack adds: a pane-spawning one in a menu
// (Placement and Kind), or one that runs a built-in action the bridge's
// Invoke allows (Action), as Lua's gorex.action does.
type CommandDef struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Kind      string `json:"kind,omitempty"`
	Placement string `json:"placement,omitempty"`
	Action    string `json:"action,omitempty"`
	// Event names a string sent to the focused pane's page as
	// "ext.command" when the command runs, for the commands a page
	// answers itself, as a preview toggle's.
	Event string `json:"event,omitempty"`
}

var (
	idRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	kindRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
)

func in(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Validate checks the manifest: a pack that fails loads not at all, and
// its caller reports the reason.
func (m *Manifest) Validate() error {
	if !idRe.MatchString(m.ID) {
		return fmt.Errorf("invalid pack id %q", m.ID)
	}
	if m.Name == "" {
		return fmt.Errorf("pack %q has no name", m.ID)
	}
	if m.Engine != Engine {
		return fmt.Errorf("pack %q: unknown engine %q", m.ID, m.Engine)
	}
	if m.API == 0 {
		m.API = 1
	}
	if m.API < 1 || m.API > APIVersion {
		return fmt.Errorf("pack %q asks for bridge api %d, this build speaks %d", m.ID, m.API, APIVersion)
	}
	if len(m.Panes) == 0 {
		return fmt.Errorf("pack %q declares no pane kind", m.ID)
	}
	seen := map[string]bool{}
	for i, p := range m.Panes {
		if !kindRe.MatchString(p.Kind) {
			return fmt.Errorf("pack %q: invalid pane kind %q", m.ID, p.Kind)
		}
		if seen[p.Kind] {
			return fmt.Errorf("pack %q: duplicate pane kind %q", m.ID, p.Kind)
		}
		seen[p.Kind] = true
		if p.Title == "" {
			return fmt.Errorf("pack %q kind %q has no title", m.ID, p.Kind)
		}
		for _, f := range p.Fields {
			if !in(StateFields, f) {
				return fmt.Errorf("pack %q kind %q: unknown state field %q", m.ID, p.Kind, f)
			}
		}
		for _, f := range p.Files {
			if err := checkPaneFile(f, p.Fields); err != nil {
				return fmt.Errorf("pack %q kind %q: %v", m.ID, p.Kind, err)
			}
		}
		m.Panes[i] = p
	}
	for _, s := range m.Slots {
		if s != SlotWeb {
			return fmt.Errorf("pack %q: unknown slot %q", m.ID, s)
		}
	}
	if in(m.Slots, SlotWeb) && !in(m.Permissions, PermWeb) {
		return fmt.Errorf("pack %q: slot %q needs permission %q", m.ID, SlotWeb, PermWeb)
	}
	for _, p := range m.Permissions {
		if !in([]string{PermState, PermStorage, PermInvoke, PermWeb, PermEnv}, p) {
			return fmt.Errorf("pack %q: unknown permission %q", m.ID, p)
		}
	}
	if err := m.checkStorage(); err != nil {
		return err
	}
	cmds := map[string]bool{}
	for _, c := range m.Commands {
		if c.ID == "" {
			return fmt.Errorf("pack %q: a command has no id", m.ID)
		}
		if cmds[c.ID] {
			return fmt.Errorf("pack %q: duplicate command %q", m.ID, c.ID)
		}
		cmds[c.ID] = true
		if c.Title == "" {
			return fmt.Errorf("pack %q command %q has no title", m.ID, c.ID)
		}
		if c.Placement != "" && !in(CommandPlacements, c.Placement) {
			return fmt.Errorf("pack %q command %q: unknown placement %q", m.ID, c.ID, c.Placement)
		}
		if c.Kind != "" {
			if !seen[c.Kind] {
				return fmt.Errorf("pack %q command %q: no pane kind %q", m.ID, c.ID, c.Kind)
			}
		} else if c.Placement != "" {
			return fmt.Errorf("pack %q command %q: placement needs a kind", m.ID, c.ID)
		}
		if c.Action != "" && !in(m.Permissions, PermInvoke) {
			return fmt.Errorf("pack %q command %q: action needs permission %q", m.ID, c.ID, PermInvoke)
		}
		if c.Event != "" && c.Action != "" {
			return fmt.Errorf("pack %q command %q: action and event are exclusive", m.ID, c.ID)
		}
	}
	return nil
}

// checkPaneFile vets a files pattern of the kind: "{field}" names a
// state field of it, and the rest is a clean relative path.
func checkPaneFile(pattern string, fields []string) error {
	rest := pattern
	for {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			break
		}
		j := strings.IndexByte(rest[i:], '}')
		if j < 0 {
			return fmt.Errorf("file pattern %q has an open brace", pattern)
		}
		if !in(fields, rest[i+1:i+j]) {
			return fmt.Errorf("file pattern %q names no state field of the kind", pattern)
		}
		rest = rest[i+j+1:]
	}
	if strings.Count(pattern, "{") != strings.Count(pattern, "}") {
		return fmt.Errorf("file pattern %q has a stray brace", pattern)
	}
	literal := strings.NewReplacer("{", "", "}", "").Replace(pattern)
	if filepath.IsAbs(literal) || strings.HasPrefix(filepath.Clean(literal), "..") {
		return fmt.Errorf("file pattern %q leaves the storage directory", pattern)
	}
	return nil
}

// checkStorage confines Storage.Dir under GoRex's data directory: a
// relative path of clean elements, never absolute and never escaping.
func (m *Manifest) checkStorage() error {
	d := m.Storage.Dir
	if d == "" {
		return nil
	}
	if filepath.IsAbs(d) {
		return fmt.Errorf("pack %q: storage dir %q is absolute", m.ID, d)
	}
	if c := filepath.Clean(d); c != d || strings.HasPrefix(c, "..") {
		return fmt.Errorf("pack %q: storage dir %q leaves the data directory", m.ID, d)
	}
	return nil
}

// Has reports whether the pack may call the bridge area p.
func (m *Manifest) Has(p string) bool { return in(m.Permissions, p) }

// HasSlot reports whether the pack declares the extra surface s.
func (m *Manifest) HasSlot(s string) bool { return in(m.Slots, s) }

// StorageDir is the pack's data directory inside dataDir, GoRex's data
// directory: the manifest's dir, or exts/<id>/data.
func (m *Manifest) StorageDir(dataDir string) string {
	if m.Storage.Dir != "" {
		return filepath.Join(dataDir, filepath.FromSlash(m.Storage.Dir))
	}
	return filepath.Join(dataDir, "exts", m.ID, "data")
}

// Pane is the kind's pane definition, nil for one the pack has no.
func (m *Manifest) Pane(kind string) *PaneDef {
	for i := range m.Panes {
		if m.Panes[i].Kind == kind {
			return &m.Panes[i]
		}
	}
	return nil
}

// LoadManifest reads and validates the ext.json of the pack in dir.
func LoadManifest(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, File))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, File), err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}
