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
	// mygo outlines a focused text input in the theme's Accent, its stock
	// blue (ui/form.go inputBorder), which the field's own border cannot
	// replace: that style function sets the border as it paints. Its
	// outline closure keeps the theme it was made under, so a theme of the
	// field's own, the space's accent, gives it the app's color without
	// restyling the widgets around it.
	base := c.Theme()
	t := *base
	t.Accent = k.accent
	c.SetTheme(&t)
	in := ui.TextInput(c, value)
	c.SetTheme(base)
	// Selected text is the theme's Selection as the frame paints, not as
	// the field was made, and there is no scoped theme then: the field
	// takes the accent itself.
	in.PaddingY(0).TextColor(k.text).SelectionColor(k.accent.Alpha(0.3)).Background(k.field).Border(1, k.fieldBorder)
	switch size {
	case fieldCompact:
		in.Height(22).FontSize(13).PaddingX(6)
	default:
		in.Height(28).FontSize(13).PaddingX(8)
	}
	return in
}
