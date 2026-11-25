package main

import (
	"github.com/egoist/mygo/ui"
)

// empty.go is the empty states of DESIGN.md and docs/DSK-006-empty-states.md:
// one component in three sizes — page, row and menu — that says what a
// surface has none of, what the place is for, and the step that makes the
// first, where there is one.

// emptyAction is an empty state's button: the step the state offers.
type emptyAction struct {
	label string
	run   func()
}

// emptyPage is the empty state of a whole area: a wireframe of the place,
// a title, a note and up to two buttons, the group about 40% down the area.
// draw paints the picture with canvas operations, so it takes the window's
// colors; it is decorative and carries no label.
func emptyPage(c *ui.Context, k *colors, draw func(p *ui.Painter, r ui.Rect, k *colors), title, note string, primary, ghost emptyAction) {
	ui.Column(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Children(func() {
		ui.Spacer(c).Grow(2)
		ui.Column(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			if draw != nil {
				ui.Box(c).Size(emptySceneW, emptySceneH).Margin(0, 0, 10, 0).Draw(func(p *ui.Painter, r ui.Rect) { draw(p, r, k) })
			}
			ui.Text(c, title).FontSize(fontTitle).FontWeight(600).TextColor(k.text).Label(title).TextAlign(ui.Center)
			if note != "" {
				ui.Text(c, note).FontSize(fontBody).TextColor(k.textMuted).Label(note).TextAlign(ui.Center).MaxWidth(400)
			}
			if primary.label != "" || ghost.label != "" {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Margin(8, 0, 0, 0).Children(func() {
					if ghost.label != "" && emptyLink(c, k, ghost.label).Clicked() {
						ghost.run()
					}
					if primary.label != "" && ui.PrimaryButton(c, primary.label).Clicked() {
						primary.run()
					}
				})
			}
		})
		ui.Spacer(c).Grow(3)
	})
}

// emptyRow is the empty state of a list: one muted line, with a ghost
// button at its end where a next step exists.
func emptyRow(c *ui.Context, k *colors, key, title string, ghost emptyAction) ui.Element {
	row := ui.Row(c.Key(key)).Height(28).Padding(0, 10).Gap(8).AlignItems(ui.Center)
	row.Children(func() {
		ui.Text(c, title).FontSize(fontBody).TextColor(k.textMuted).Label(title).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
		if ghost.label != "" && emptyLink(c, k, ghost.label).Clicked() {
			ghost.run()
		}
	})
	return row
}

// emptyMenu is the empty state of a menu or a status line: one muted line
// at a drawn menu row's metrics, no picture and no button.
func emptyMenu(c *ui.Context, k *colors, key, text string) ui.Element {
	return ui.Row(c.Key(key)).Height(24).Padding(0, 10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, text).FontSize(fontBody).TextColor(k.textMuted).Label(text).SingleLine().Ellipsis("…")
	})
}

// emptyLink is an empty state's ghost button: accent text as the
// disclosure rows of remoteview.go draw it.
func emptyLink(c *ui.Context, k *colors, label string) ui.Element {
	e := ui.Row(c).Padding(4, 8).Radius(k.radius(radiusS)).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
	if e.Hovered() {
		e.Background(k.hover)
	}
	e.Children(func() {
		ui.Text(c, label).FontSize(fontBody).FontWeight(500).TextColor(k.accent)
	})
	return e
}

// The size of a page empty state's picture.
const (
	emptySceneW = 220
	emptySceneH = 148
)

// drawEmptyScene draws the picture of a page empty state into r: a mini
// window — a tab strip of bars and one card with placeholder bars — and,
// when the state has a next step, a round + button at the card's corner.
// It is canvas operations, not an SVG, so it takes the window's colors.
func drawEmptyScene(p *ui.Painter, r ui.Rect, k *colors, next, dark bool) {
	// The window's card: a surface shape with a soft shadow.
	p.Shadow(r, k.radius(radiusM), 0, 4, 16, 0, k.shadow)
	p.Fill(r, k.surface, k.radius(radiusM))
	p.Stroke(r, k.cardBorder, k.radius(radiusM), 1)
	// The tab strip: one chosen tab, one empty slot.
	p.Fill(ui.Rect{X: r.X + 12, Y: r.Y + 11, W: 46, H: 9}, k.emptyMark.Alpha(0.52), k.capsule(9))
	p.Fill(ui.Rect{X: r.X + 64, Y: r.Y + 11, W: 34, H: 9}, k.emptyMark.Alpha(0.14), k.capsule(9))
	// The card inside: a pane's, with a prompt line and body bars.
	card := ui.Rect{X: r.X + 12, Y: r.Y + 34, W: r.W - 24, H: r.H - 46}
	p.Fill(card, k.surface, concentric(k.radius(radiusM), 2))
	p.Stroke(card, k.cardBorder, concentric(k.radius(radiusM), 2), 1)
	p.Fill(ui.Rect{X: card.X + 12, Y: card.Y + 14, W: 52, H: 6}, k.emptyMark.Alpha(0.52), k.capsule(6))
	p.Fill(ui.Rect{X: card.X + 12, Y: card.Y + 28, W: card.W - 52, H: 5}, k.emptyMark.Alpha(0.28), k.capsule(5))
	p.Fill(ui.Rect{X: card.X + 12, Y: card.Y + 40, W: card.W - 90, H: 5}, k.emptyMark.Alpha(0.28), k.capsule(5))
	p.Fill(ui.Rect{X: card.X + 12, Y: card.Y + 58, W: card.W - 72, H: 5}, k.emptyMark.Alpha(0.14), k.capsule(5))
	if !next {
		return
	}
	// The one accent: a round + on the card's corner.
	d := float32(20)
	plus := ui.Rect{X: card.X + card.W - d - 4, Y: card.Y + card.H - d - 4, W: d, H: d}
	p.Fill(plus, k.emptyMark, k.capsule(d))
	// The plus is on emptyMark, not on the accent: its ink is not a theme's
	// accentText.
	ink := ui.Hex("#ffffff")
	if dark {
		ink = ui.Hex("#10151c")
	}
	cx, cy := plus.X+d/2, plus.Y+d/2
	p.Line(cx-4, cy, cx+4, cy, 1.6, ink)
	p.Line(cx, cy-4, cx, cy+4, 1.6, ink)
}
