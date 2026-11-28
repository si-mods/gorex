package main

import (
	"github.com/egoist/mygo/ui"

	"gorex/locales"
)

// findOpen is how the find bar opens: unfolding down from nothing over
// the terminal, which takes its new size at once, as without it. It has
// no Exit: a bar going out goes under its siblings, and the terminal takes
// its room at once, over it.
var findOpen = &ui.Motion{Collapse: true, Opacity: 0}

// findBar is the find bar of pane p: its query, how the search goes, and
// the buttons that step through the matches and close it.
func (a *App) findBar(c *ui.Context, k *colors, p *Pane) ui.Element {
	row := ui.Row(c.Key("find")).Height(30).Shrink(0).Padding(0, 8).Gap(6).AlignItems(ui.Center).
		Background(k.hover).Radius(k.radius(radiusS))
	// Not in a pane that moves, as fadeColorsEnabled tells.
	if anim := motion(c); anim != nil && fadeColorsEnabled() {
		row.Transition(ui.ElementTransition{Animation: anim, Colors: true, Enter: findOpen})
	}
	row.Children(func() {
		in := textField(c.Key("query"), k, &a.findText, fieldCompact).Placeholder(locales.FindPlaceholder()).
			Grow(1).MinWidth(0)
		if a.findFocus {
			in.Focus()
			a.findFocus = false
		}
		in.OnChange(a.findQuery)
		in.OnShortcut(0, ui.KeyEscape, a.closeFind)
		in.OnTextKey(func(k ui.TextKey) bool {
			if k.Key != ui.KeyEnter || k.Mods&^ui.Shift != 0 {
				return false
			}
			if q := k.Text(); q != a.findText {
				a.findText = q
				a.findQuery()
			}
			if k.Mods&ui.Shift != 0 {
				a.findPrevious()
			} else {
				a.findNext()
			}
			return true
		})
		if count := a.findCount(); count != "" {
			ui.Text(c.Key("count"), count).FontSize(fontFootnote).TextColor(k.textMuted).Shrink(0)
		}
		if ui.Button(c.Key("prev"), "↑").Tooltip(locales.FindPrevious()).Clicked() {
			a.findPrevious()
			a.findFocus = true
			a.changed()
		}
		if ui.Button(c.Key("next"), "↓").Tooltip(locales.FindNext()).Clicked() {
			a.findNext()
			a.findFocus = true
			a.changed()
		}
		if ui.Button(c.Key("close"), "×").Tooltip(locales.FindClose()).Clicked() {
			a.closeFind()
		}
	})
	return row
}
