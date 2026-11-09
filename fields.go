package main

import (
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

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
	in := styledField(c, k, func(c *ui.Context) ui.Element { return ui.TextInput(c, value) }).PaddingY(0)
	switch size {
	case fieldCompact:
		in.Height(22).FontSize(13).PaddingX(6)
	default:
		in.Height(28).FontSize(13).PaddingX(8)
	}
	return in
}

// renameKeys ends the rename field in as macOS's rename fields do: the old
// text is selected as the field takes the focus, Enter or the focus going
// elsewhere keeps the new one, and Escape goes back. focused remembers
// that the field had the focus.
func renameKeys(in ui.Element, text string, focused *bool, done func(keep bool)) {
	switch {
	case in.Shortcut(0, ui.KeyEscape):
		done(false)
	case in.Submitted():
		done(true)
	case in.Focused():
		// The old name, selected as the field takes the focus, goes as
		// the new one is typed.
		if !*focused {
			in.SetTextSelection(0, utf8.RuneCountInString(text))
		}
		*focused = true
	case *focused:
		done(true) // the focus went elsewhere
	}
}

// styledField makes a text control with make, in the app's look: the
// space's accent for its outline, opaque field colors, and text of k.text.
func styledField(c *ui.Context, k *colors, make func(*ui.Context) ui.Element) ui.Element {
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
	in := make(c)
	c.SetTheme(base)
	// Selected text is the theme's Selection as the frame paints, not as
	// the field was made, and there is no scoped theme then: the field
	// takes the accent itself.
	return in.TextColor(k.text).SelectionColor(k.accent.Alpha(0.3)).Background(k.field).Border(1, k.fieldBorder)
}
