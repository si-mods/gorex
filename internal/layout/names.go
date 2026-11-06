package layout

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"
)

// Version is the version of Store; a layout without one is the tabs of a
// GoRex of one window.
const Version = 2

// Parse reads a layout the server keeps, a workspace for the tabs of an
// older GoRex. Workspaces without an id, or of an id already read, and
// windows of none, are dropped; a workspace without a name gets one, and
// a browser pane without an ID one of its place (Named tells it did), the
// same at every read of that layout.
func Parse(raw []byte) Store {
	s := parse(raw)
	for _, w := range s.Sessions {
		for i, t := range w.Tabs {
			tab := t.ID
			if tab == "" {
				tab = fmt.Sprintf("%s.%d", w.ID, i)
			}
			if nameBrowsers(t.Root, "web:"+tab+":r") {
				s.Named = true
			}
			for j, f := range t.Floats {
				if nameBrowsers(f.Pane, fmt.Sprintf("web:%s:f%d", tab, j)) {
					s.Named = true
				}
			}
		}
	}
	return s
}

// nameBrowsers gives each browser pane of n that has none the ID of its
// place in the tree, at, and reports whether it gave one.
func nameBrowsers(n *Node, at string) bool {
	if n == nil {
		return false
	}
	if n.URL != "" && n.ID == "" {
		n.ID = at
		return true
	}
	a := nameBrowsers(n.A, at+"a")
	return nameBrowsers(n.B, at+"b") || a
}

func parse(raw []byte) Store {
	var s Store
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return Store{Version: Version}
	}
	if s.Version < Version {
		if len(s.Tabs) == 0 {
			return Store{Version: Version}
		}
		w := Workspace{ID: NewSpaceID(), Name: SpaceName(nil), Layout: s.Layout}
		return Store{Version: Version, Sessions: []Workspace{w}, Windows: []string{w.ID}}
	}
	out := Store{Version: s.Version}
	ids := map[string]bool{}
	for _, w := range s.Sessions {
		if w.ID == "" || ids[w.ID] {
			continue
		}
		ids[w.ID] = true
		if strings.TrimSpace(w.Name) == "" {
			w.Name = SpaceName(nil)
		}
		out.Sessions = append(out.Sessions, w)
	}
	for _, id := range s.Windows {
		if ids[id] && !slices.Contains(out.Windows, id) {
			out.Windows = append(out.Windows, id)
		}
	}
	return out
}

// NewSpaceID returns a new workspace's id.
func NewSpaceID() string { return rand.Text()[:12] }

// NewTabID makes a tab's lasting ID, a UUID version 7, or a browser
// pane's.
func NewTabID() string {
	var b [16]byte
	ms := uint64(time.Now().UnixMilli())
	for i := range 6 {
		b[i] = byte(ms >> (40 - 8*i))
	}
	rand.Read(b[6:])
	b[6] = b[6]&0x0f | 0x70
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// The words of workspaces' names, as Rex's "icy-ridge".
var (
	nameAdjectives = []string{"amber", "brisk", "calm", "coral", "crisp", "dusky", "eager", "fern", "gentle", "golden", "hazy", "icy", "jade", "keen", "lucky", "lunar", "misty", "mossy", "noble", "quiet", "rapid", "rusty", "silent", "sunny", "swift", "tidal", "velvet", "vivid", "windy", "young"}
	nameNouns      = []string{"bay", "brook", "canyon", "cedar", "cliff", "comet", "delta", "dune", "ember", "fjord", "glade", "grove", "harbor", "island", "lagoon", "meadow", "mesa", "orbit", "peak", "pine", "reef", "ridge", "river", "shore", "summit", "tundra", "valley", "willow"}
)

// SpaceName returns a random name, as "icy-ridge", that taken does not
// report taken, if it finds one.
func SpaceName(taken func(string) bool) string {
	pick := func(words []string) string {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
		return words[n.Int64()]
	}
	name := ""
	for range 50 {
		name = pick(nameAdjectives) + "-" + pick(nameNouns)
		if taken == nil || !taken(name) {
			return name
		}
	}
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s-%d", name, i); !taken(n) {
			return n
		}
	}
}

// Color is a preset color of a space; Hex is its base color.
type Color struct{ ID, Hex string }

// Palette is the order new spaces take their colors in.
var Palette = []Color{
	{"rose", "#e54666"}, {"orange", "#f76b15"}, {"amber", "#ffb224"},
	{"green", "#30a46c"}, {"teal", "#12a594"}, {"blue", "#0090ff"},
	{"indigo", "#3e63dd"}, {"violet", "#8e4ec6"}, {"pink", "#d6409f"},
}

// NextColor is the color a new space takes, beside spaces of colors used:
// the first preset none uses, or, every one used, the preset the fewest
// do, the palette's order settling ties.
func NextColor(used []string) string {
	n := make(map[string]int, len(Palette))
	for _, c := range used {
		n[c]++
	}
	best, bestN := "", len(used)+1
	for _, p := range Palette {
		if n[p.ID] == 0 {
			return p.ID
		}
		if n[p.ID] < bestN {
			best, bestN = p.ID, n[p.ID]
		}
	}
	return best
}

// Label cleans a tab's or a space's name of a client: its control
// characters go, and it is trimmed and bounded to 128 characters.
func Label(s string) string { return clean(s, 128) }

// PaneName is the name a user gave a pane: trimmed, without control
// characters, and cut to 64 characters.
func PaneName(s string) string { return clean(s, 64) }

func clean(s string, most int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > most {
		return string(r[:most])
	}
	return s
}
