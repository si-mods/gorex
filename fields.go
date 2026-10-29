package main

import "github.com/egoist/mygo/ui"

// fieldSize is how tall a text field is: compact where a name is edited in
// place, in a row with what it names, and regular in a form or a popover.
type fieldSize int

const (
	fieldCompact fieldSize = iota
	fieldRegular
)

// textField returns a text field of size, editing *value, in the app's
// look: mygo's TextInput takes the theme's face, outline and 6-DIP
// vertical padding, which leaves 8 DIPs of content box in a 22-DIP field,
// less than the 15-DIP line, and clips the glyphs. Its face and outline
// are opaque, and its text k.text, which the row of a selected tab would
// else color as its own. Callers chain what they need: Placeholder, Grow,
// AutoFocus, Label.
func textField(c *ui.Context, k *colors, value *string, size fieldSize) ui.Element {
	in := ui.TextInput(c, value).PaddingY(0).TextColor(k.text).Background(k.field).Border(1, k.fieldBorder)
	switch size {
	case fieldCompact:
		in.Height(22).FontSize(13).PaddingX(6)
	default:
		in.Height(28).FontSize(13).PaddingX(8)
	}
	return in
}
