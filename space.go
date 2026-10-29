package main

import "strings"

// spaceColor is a preset color of a space; Hex is its base color.
type spaceColor struct{ ID, Hex string }

// spacePalette is the order new spaces take their colors in.
var spacePalette = []spaceColor{
	{"rose", "#e54666"}, {"orange", "#f76b15"}, {"amber", "#ffb224"},
	{"green", "#30a46c"}, {"teal", "#12a594"}, {"blue", "#0090ff"},
	{"indigo", "#3e63dd"}, {"violet", "#8e4ec6"}, {"pink", "#d6409f"},
}

// spaceColorHex is the color c names: the base hex of a preset's id, or a
// custom "#rrggbb" of six hex digits, lowercased. It reports false for ""
// and for anything else.
func spaceColorHex(c string) (string, bool) {
	for _, p := range spacePalette {
		if p.ID == c {
			return p.Hex, true
		}
	}
	if len(c) != 7 || c[0] != '#' {
		return "", false
	}
	for _, r := range c[1:] {
		switch {
		case '0' <= r && r <= '9', 'a' <= r && r <= 'f', 'A' <= r && r <= 'F':
		default:
			return "", false
		}
	}
	return strings.ToLower(c), true
}

// nextSpaceColor is the color a new space takes: the first preset no space
// uses, or, every one used, the preset the fewest do, the palette's order
// settling ties.
func (st *store) nextSpaceColor() string {
	used := make(map[string]int, len(spacePalette))
	for _, w := range st.workspaces {
		used[w.Color]++
	}
	best, bestN := "", len(st.workspaces)+1
	for _, p := range spacePalette {
		n := used[p.ID]
		if n == 0 {
			return p.ID
		}
		if n < bestN {
			best, bestN = p.ID, n
		}
	}
	return best
}
