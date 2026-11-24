package main

import (
	"cmp"
	"log"
	"maps"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/config"
	"gorex/internal/theme"
	"gorex/locales"
)

// ambience.go draws what a theme shows besides its colors
// (docs/DSK-010-theme-ambience.md): the glows of the window's gradient
// (layer 1), its still images (layer 2), its sprites, which move outside
// the pane cards (layer 3), and the brand row of the sidebar.
// The mascot and the ticker come in a later phase.

// glow is a theme's glow for one appearance, as the background draws it.
type glow struct {
	x, y, rx, ry float32
	color        ui.Color
}

// themeLook is what the selected theme draws besides its colors: its
// glows for each appearance, light then dark, its still images and its
// brand, and the assets decoded for them, nil until they load.
type themeLook struct {
	glows   [2][]glow
	images  []theme.Still
	sprites []theme.Sprite
	brand   theme.Brand
	art     *theme.Art
	// ambient tells that the theme has ambience, which the sidebar's
	// button and the command turn off and on.
	ambient bool
	// sideCap is the alpha that layer 3 paints at most behind the sidebar,
	// light then dark: sidebarCap, or lower where the theme's text needs it.
	sideCap [2]float32
	// id and assets are the theme and the files that art was decoded for.
	id     string
	assets map[string]theme.Asset
}

// look holds the selected theme's look, nil with no theme. applyTheme
// stores a new one, and so does the asset load when it is done; a window
// reads one whole value.
var look atomic.Pointer[themeLook]

// lookGen counts the theme loads, so that the asset load of a theme the
// user left does not store its look.
var lookGen atomic.Int64

// lookOf makes the look of m, without assets.
func lookOf(m *theme.Manifest) *themeLook {
	if m == nil || m.Ambience.Empty() && m.Brand == (theme.Brand{}) {
		return nil
	}
	l := &themeLook{images: m.Ambience.Images, sprites: m.Ambience.Sprites,
		brand: m.Brand, ambient: !m.Ambience.Empty(), sideCap: [2]float32{sidebarCap, sidebarCap}}
	for _, g := range m.Ambience.Glows {
		for i := range 2 {
			l.glows[i] = append(l.glows[i], glow{g.X, g.Y, g.RX, g.RY, g.Color[i]})
		}
	}
	return l
}

// ambienceShown reports whether the window draws the theme's ambience:
// not with appearance.ambience off.
func ambienceShown() bool { return prefs.Ambience != ambienceOff }

// themeGlows returns the glows of the window's background in an
// appearance: none with no theme, none that has glows, or ambience off.
func themeGlows(dark bool) []glow {
	l := look.Load()
	if l == nil || !ambienceShown() {
		return nil
	}
	if dark {
		return l.glows[1]
	}
	return l.glows[0]
}

// paintGlows paints the glows on the CPU renderer, where the background
// is gradients: each a radial gradient, stretched to its ellipse.
func paintGlows(p *ui.Painter, r ui.Rect, glows []glow, alpha float32) {
	for _, g := range glows {
		rx, ry := g.rx*r.W, g.ry*r.H
		if rx <= 0 || ry <= 0 {
			continue
		}
		cx, cy := r.X+g.x*r.W, r.Y+g.y*r.H
		col := g.color.Alpha(alpha)
		clear := col
		clear.A = 0
		p.Clip(r, 0, func() {
			p.Transform(ui.Scaling(1, ry/rx), cx, cy, func() {
				p.FillRadialGradient(ui.Rect{X: cx - rx, Y: cy - rx, W: 2 * rx, H: 2 * rx}, ui.RadialGradient{
					Stops:  []ui.GradientStop{{Color: col, At: 0}, {Color: clear, At: 1}},
					Center: ui.Point{X: 0.5, Y: 0.5},
					Radius: rx,
				}, 0)
			})
		})
	}
}

// paintStills paints the theme's still images over the background (layer
// 2): each at its anchor, as tall as its height says, a fraction of r's or
// DIPs, and as opaque as its opacity. They do not move, and show through a
// translucent pane as the gradient does.
func paintStills(p *ui.Painter, r ui.Rect) {
	l := look.Load()
	if l == nil || l.art == nil || !ambienceShown() {
		return
	}
	for _, s := range l.images {
		bm := l.art.Bitmaps[s.Asset]
		if bm == nil {
			continue
		}
		bw, bh := bm.Size()
		h := s.Height
		if h <= 1 {
			h *= r.H
		}
		w := h * float32(bw) / float32(bh)
		dst := ui.Rect{X: r.X, Y: r.Y, W: w, H: h}
		switch s.Anchor {
		case theme.BottomRight, theme.TopRight:
			dst.X = r.X + r.W - w
		case theme.Center:
			dst.X = r.X + (r.W-w)/2
		}
		switch s.Anchor {
		case theme.BottomRight, theme.BottomLeft:
			dst.Y = r.Y + r.H - h
		case theme.Center:
			dst.Y = r.Y + (r.H-h)/2
		}
		p.ImageRegion(bm, ui.Rect{}, dst, ui.ImageOptions{Tint: ui.RGBA(255, 255, 255, s.Opacity)})
	}
}

// brandRowH is the height of the brand row; its icon is
// theme.BrandIconSize, with even room above and below it.
const brandRowH = 28

// brandGap is the room between the brand's icon and its wordmark: a step of
// the 4 pt grid, as the rows' tiles keep from their names.
const brandGap = 8

// brandRow is the theme's brand at the top of the sidebar, above the space
// picker: its icon, an SVG in the sidebar's text color, as a mask would
// paint it, or a bitmap as it is, and its wordmark, which ellipsizes. The
// icon starts where the rows' tiles do. With no brand it is nothing.
func brandRow(c *ui.Context, k *colors, padR, padL float32) {
	l := look.Load()
	if l == nil || l.brand == (theme.Brand{}) {
		return
	}
	const icon = theme.BrandIconSize
	ui.Row(c).Height(brandRowH).Shrink(0).Padding((brandRowH-icon)/2, padR, (brandRowH-icon)/2, padL+sidebarLead()).Gap(brandGap).AlignItems(ui.Center).
		Role(ui.RoleImage).Label(l.brand.Wordmark).Children(func() {
		if l.art != nil && l.brand.Icon != "" {
			if bm := l.art.Bitmaps[l.brand.Icon]; bm != nil {
				ui.Image(c, bm).Size(icon, icon).Shrink(0)
			} else if l.art.Icon != nil {
				ui.Icon(c, l.art.Icon).Size(icon, icon).Shrink(0).TextColor(k.text)
			}
		}
		if l.brand.Wordmark != "" {
			ui.Text(c, l.brand.Wordmark).FontSize(fontTitle).FontWeight(600).TextColor(k.text).SingleLine().Ellipsis("…").MinWidth(0)
		}
	})
}

// artScale is the screen scale the assets decode for: twice the DIPs of
// what they show at, at the scale of a Retina screen.
const artScale = 2

// setLook makes the look of m the selected theme's, without assets.
// The look keeps the assets of the one before while the theme is the same
// and names the same files, so that a reload of settings.json does not make
// the watermark and the brand's icon blink while loadArt decodes them again.
func setLook(m *theme.Manifest) {
	lookGen.Add(1)
	l := lookOf(m)
	if old := lastArt.Load(); l != nil && old != nil && old.id == prefs.Theme && maps.Equal(old.assets, m.Assets) {
		l.art = old.art
	}
	if l != nil {
		l.id, l.assets = prefs.Theme, m.Assets
		if pal := palettes.Load(); pal != nil && m.Ambience.Moving() {
			inks := movingInks(m.Ambience)
			for i, k := range []*colors{&pal.light, &pal.dark} {
				if c := sidebarCapFor(k, inks); c < sidebarCap {
					log.Printf("gorex settings: appearance.theme %q: the moving layers paint at %.2f behind the sidebar, not %.2f, so that its text reads (%s)",
						prefs.Theme, c, sidebarCap, [2]string{"light", "dark"}[i])
					l.sideCap[i] = c
				}
			}
		}
	} else if prefs.Theme == "" {
		lastArt.Store(nil) // loadTheme's applyTheme(nil) on the way to a theme keeps it
	}
	look.Store(l)
}

// lastArt is the last look whose assets were decoded, for setLook.
var lastArt atomic.Pointer[themeLook]

// artLoads counts the decodes loadArt runs, so that a test can wait for
// them.
var artLoads sync.WaitGroup

// loadArt decodes the assets of the look of m, the theme name, from the
// theme's folder, off the UI thread. When they are in, every window draws
// again.
func loadArt(name string, m *theme.Manifest) {
	gen, l := lookGen.Load(), look.Load()
	if l == nil || len(m.Assets) == 0 {
		return
	}
	dir, err := theme.AssetFolder(theme.Dir(config.Dir()), name)
	if err != nil {
		return
	}
	artLoads.Add(1)
	go func() {
		defer artLoads.Done()
		art, warn := theme.LoadAssets(dir, m, artScale)
		for _, w := range warn {
			log.Printf("gorex settings: appearance.theme %q: %s", name, w)
		}
		withArt := *l
		withArt.art = art
		if !look.CompareAndSwap(l, &withArt) || lookGen.Load() != gen {
			return
		}
		lastArt.Store(&withArt)
		for _, w := range mygo.Windows() {
			w.Update(func() {}) // posts the redraw, and does not wait for it
		}
	}()
}

// The values of appearance.ambience and appearance.mascot besides "", which
// is the default: ambience moves, and the mascot shows.
const (
	ambienceStill = "still"
	ambienceOff   = "off"
	mascotOff     = "off"
)

// themeAmbient reports whether the selected theme has ambience: what the
// sidebar's button and Toggle Theme Ambience turn off and on.
func themeAmbient() bool {
	l := look.Load()
	return l != nil && l.ambient
}

// ambienceBack is the value of appearance.ambience that Toggle Theme
// Ambience brings back after off: moving, or still if it was.
var ambienceBack string

// toggleAmbience turns the theme's ambience off, or on again as it was,
// in appearance.ambience.
func (a *App) toggleAmbience() {
	a.setWindowPref(func(s *settings) {
		if s.Ambience == ambienceOff {
			s.Ambience = ambienceBack
			return
		}
		ambienceBack, s.Ambience = s.Ambience, ambienceOff
	})
}

// ambienceButton is the sidebar foot's button that turns the theme's
// ambience off and on, shown only while the theme has some.
func (a *App) ambienceButton(c *ui.Context, k *colors) {
	if !themeAmbient() {
		return
	}
	label := locales.SidebarAmbienceOff()
	if prefs.Ambience == ambienceOff {
		label = locales.SidebarAmbienceOn()
	}
	if iconButton(c, k, "sparkles", label, 30, 16).Tooltip(label).Clicked() {
		a.toggleAmbience()
	}
}

// Layer 3: the moving layers.

// sidebarCap is the alpha that a sprite paints at most
// behind the sidebar, whose rows have text on the window's gradient.
const sidebarCap = 0.12

// ambienceFPS is the rate the moving layers paint at while they move: 20
// a second, as phase 2's budget measured 30 at about 10% of a core.
const ambienceFPS = 20

// movingInks returns the colors the moving layers of a can paint, opaque:
// black and white for a sprite, whose pixels can be anything.
func movingInks(a theme.Ambience) []ui.Color {
	if len(a.Sprites) == 0 {
		return nil
	}
	return []ui.Color{ui.RGB(255, 255, 255), ui.RGB(0, 0, 0)}
}

// sidebarCapFor returns the alpha, sidebarCap or lower, at which each of
// inks over the top and the bottom of k's gradient keeps k's text at 4.5:1
// and its muted text at 3:1, or at the contrast it has with no ink, where
// that is less: the theme's own colors are not the ink's fault.
func sidebarCapFor(k *colors, inks []ui.Color) float32 {
	reads := func(alpha float32) bool {
		for _, bg := range []ui.Color{k.bgTop, k.bgBottom} {
			bg.A = 255
			for _, ink := range inks {
				over := ink.Alpha(alpha).Over(bg)
				for _, t := range []struct {
					c    ui.Color
					want float64
				}{{k.text, 4.5}, {k.textMuted, 3}} {
					fg := t.c.Over(bg)
					if contrast(fg, over) < min(t.want, contrast(fg, bg)) {
						return false
					}
				}
			}
		}
		return true
	}
	for c := float32(sidebarCap); c > 0; c -= 0.01 {
		if reads(c) {
			return c
		}
	}
	return 0
}

// band is a rectangle of the free region, the window less its pane cards,
// that layer 3 paints in; side tells that it is behind the sidebar.
type band struct {
	r    ui.Rect
	side bool
}

// freeRegion splits win less the cards into rectangles that do not
// overlap: horizontal bands, cut where a card or the sidebar starts or
// ends, and each band's run outside the cards, split at the sidebar's
// edges. Runs one above the other of the same span join.
func freeRegion(win ui.Rect, cards []ui.Rect, side ui.Rect) []band {
	right, bottom := win.X+win.W, win.Y+win.H
	clip := func(r ui.Rect) (ui.Rect, bool) {
		x0, y0 := max(r.X, win.X), max(r.Y, win.Y)
		x1, y1 := min(r.X+r.W, right), min(r.Y+r.H, bottom)
		return ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}, x1 > x0 && y1 > y0
	}
	var in []ui.Rect
	ys := []float32{win.Y, bottom}
	for _, c := range cards {
		if r, ok := clip(c); ok {
			in = append(in, r)
			ys = append(ys, r.Y, r.Y+r.H)
		}
	}
	side, hasSide := clip(side)
	if hasSide {
		ys = append(ys, side.Y, side.Y+side.H)
	}
	slices.Sort(ys)
	ys = slices.Compact(ys)
	var out []band
	open := map[[3]float32]int{} // a run's x, width and side to its band in out, while it can grow down
	for i := 0; i+1 < len(ys); i++ {
		y0, y1 := ys[i], ys[i+1]
		var taken [][2]float32
		for _, r := range in {
			if r.Y <= y0 && r.Y+r.H >= y1 {
				taken = append(taken, [2]float32{r.X, r.X + r.W})
			}
		}
		slices.SortFunc(taken, func(a, b [2]float32) int { return cmp.Compare(a[0], b[0]) })
		var runs [][2]float32
		x := win.X
		for _, t := range taken {
			if t[0] > x {
				runs = append(runs, [2]float32{x, t[0]})
			}
			x = max(x, t[1])
		}
		if x < right {
			runs = append(runs, [2]float32{x, right})
		}
		next := map[[3]float32]int{}
		add := func(x0, x1 float32, behind bool) {
			if x1 <= x0 {
				return
			}
			key := [3]float32{x0, x1 - x0, 0}
			if behind {
				key[2] = 1
			}
			if j, ok := open[key]; ok && out[j].r.Y+out[j].r.H == y0 {
				out[j].r.H = y1 - out[j].r.Y
				next[key] = j
				return
			}
			out = append(out, band{ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}, behind})
			next[key] = len(out) - 1
		}
		inSide := hasSide && side.Y <= y0 && side.Y+side.H >= y1
		for _, r := range runs {
			if !inSide {
				add(r[0], r[1], false)
				continue
			}
			sx0, sx1 := side.X, side.X+side.W
			add(r[0], min(r[1], sx0), false)
			add(max(r[0], sx0), min(r[1], sx1), true)
			add(max(r[0], sx1), r[1], false)
		}
		open = next
	}
	return out
}

// unit returns a number from 0 to 1 that stands for item i of layer n,
// the same each time: where in its trip a sprite starts, as k picks.
func unit(n, i, k int) float32 {
	h := uint32(n)*0x9e3779b1 ^ uint32(i)*0x85ebca77 ^ uint32(k)*0xc2b2ae3d
	h ^= h >> 15
	h *= 0x2c1b3c6d
	h ^= h >> 12
	h *= 0x297a2d39
	h ^= h >> 15
	return float32(h>>8) / (1 << 24)
}

// phase is how far along its trip of secs seconds an item that starts at
// offset (0 to 1) is at time t: 0 to 1, again each trip.
func phase(t float64, secs, offset float32) float32 {
	f := t/float64(secs) + float64(offset)
	return float32(f - math.Floor(f))
}

// spriteAt returns where sprite i, s, of sw×sh DIPs, is at time t (in
// seconds) in a window of w×h DIPs, and whether it shows mirrored. It
// swims in from past one edge to past the other, again each trip, each
// sprite at its own point of the trip.
func spriteAt(s theme.Sprite, i int, sw, sh, w, h float32, t float64) (ui.Rect, bool) {
	f := phase(t, s.Seconds, unit(-1, i, 0))
	r := ui.Rect{W: sw, H: sh, Y: s.Y * (h - sh)}
	switch s.Dir {
	case theme.Left:
		r.X = w - f*(w+sw)
	case theme.Up:
		r.X, r.Y = s.X*(w-sw), h-f*(h+sh)
	default:
		r.X = -sw + f*(w+sw)
	}
	return r, s.Dir == theme.Left && s.Mirror
}

// sheetFrame returns the part of bitmap bm of w×h pixels that is frame
// a's at time t: all of it for an image, else the frame the sheet's rate
// is at, on whole pixels, so that a sheet decoded smaller, whose frames
// are no whole number of pixels wide, shows no edge of the next frame.
func sheetFrame(a theme.Asset, w, h int, t float64) ui.Rect {
	if a.Frames <= 0 {
		return ui.Rect{W: float32(w), H: float32(h)}
	}
	rows := (a.Frames + a.Columns - 1) / a.Columns
	n := int(math.Floor(t*float64(a.FPS))) % a.Frames
	if n < 0 {
		n += a.Frames
	}
	col, row := n%a.Columns, n/a.Columns
	x0, x1 := col*w/a.Columns, (col+1)*w/a.Columns
	y0, y1 := row*h/rows, (row+1)*h/rows
	return ui.Rect{X: float32(x0), Y: float32(y0), W: float32(x1 - x0), H: float32(y1 - y0)}
}

// ambienceFrame is a window's state of layer 3: the cards and the sidebar
// that the frame built, whose places it paints around and under, and its
// clock.
type ambienceFrame struct {
	cards []ui.Element
	side  ui.Element
	// room is the tab's room, which clips the cards: a float of a canvas
	// can reach past it, and the part past it does not show.
	room ui.Element
	// hasSide and hasRoom tell that the frame built a sidebar and a room.
	hasSide, hasRoom bool
	// The clock: off is the time the layers stood still, and still since
	// when they stand.
	off     time.Duration
	still   bool
	stillAt time.Time
}

// card counts e as a pane card or a float of the frame: layer 3 paints
// around it.
func (a *App) card(e ui.Element) { a.ambience.cards = append(a.ambience.cards, e) }

// clock returns the layers' time at now, in seconds: the frame's, less the
// time they stood still, so that they go on from where they stopped.
func (f *ambienceFrame) clock(now time.Time, moving bool) float64 {
	switch {
	case moving && f.still:
		f.off += now.Sub(f.stillAt)
		f.still = false
	case !moving && !f.still:
		f.still, f.stillAt = true, now
	}
	if f.still {
		now = f.stillAt
	}
	return float64(now.UnixNano()-int64(f.off)) / 1e9
}

// onBattery reports whether the computer runs on its battery, asked of
// the system at most every 10 s; a test sets it.
var onBattery = func() func() bool {
	var at time.Time
	var on bool
	return func() bool {
		if now := time.Now(); now.Sub(at) > 10*time.Second {
			at, on = now, mygo.Power.IsOnBattery()
		}
		return on
	}
}()

// gpuPaints reports whether p draws on a GPU; a test sets it.
var gpuPaints = (*ui.Painter).Accelerated

// ambienceMoves reports whether the moving layers move in the window being
// built (Motion and the budget): with a sprite whose image loaded, not
// with Ambience Still or Off, with Animation Off or less motion, nor in a
// window that is not active or is hidden. The painter adds the battery,
// which it asks of as it paints (frames that only paint build no view),
// and the CPU renderer.
func ambienceMoves(c *ui.Context) bool {
	l := look.Load()
	return l != nil && l.drawsSprites() && prefs.Ambience == "" &&
		motion(c) != nil && c.WindowActive() && !c.WindowHidden()
}

// drawsSprites reports whether a sprite of l has its image decoded.
func (l *themeLook) drawsSprites() bool {
	if l.art == nil {
		return false
	}
	for _, s := range l.sprites {
		if l.art.Bitmaps[s.Asset] != nil {
			return true
		}
	}
	return false
}

// movingLayers returns what paints the theme's sprites in a
// window's free region: none without them, with Ambience Off, or before
// the frame's cards are known. Moving, it asks for the next frame at
// ambienceFPS; still, it paints them where they stopped and asks for none.
func (a *App) movingLayers(c *ui.Context) func(p *ui.Painter, r ui.Rect) {
	l := look.Load()
	if l == nil || len(l.sprites) == 0 || !ambienceShown() {
		return nil
	}
	moves := ambienceMoves(c)
	dark := 0
	if c.Theme().Dark {
		dark = 1
	}
	f := &a.ambience
	return func(p *ui.Painter, r ui.Rect) {
		moving := moves && gpuPaints(p) && !onBattery()
		if moving {
			p.AnimationFrameRate(ambienceFPS)
		}
		t := f.clock(p.Now(), moving)
		cards := make([]ui.Rect, 0, len(f.cards))
		var room ui.Rect
		if f.hasRoom {
			room = f.room.Bounds()
		}
		for _, e := range f.cards {
			b := e.Bounds()
			if f.hasRoom {
				b = intersect(b, room)
			}
			cards = append(cards, b)
		}
		var side ui.Rect
		if f.hasSide {
			side = f.side.Bounds()
		}
		paintMoving(p, r, l, dark, freeRegion(r, cards, side), t)
	}
}

// paintMoving paints l's sprites at time t in r, the window, clipped to
// each band: behind the sidebar at its cap. A sprite is one ImageRegion
// for each band it crosses.
func paintMoving(p *ui.Painter, r ui.Rect, l *themeLook, dark int, bands []band, t float64) {
	type swim struct {
		bm       *ui.Bitmap
		src, dst ui.Rect
		flip     bool
		opacity  float32
	}
	var swims []swim
	if l.art != nil {
		for i, s := range l.sprites {
			bm := l.art.Bitmaps[s.Asset]
			if bm == nil {
				continue
			}
			bw, bh := bm.Size()
			src := sheetFrame(l.assets[s.Asset], bw, bh, t)
			sh := s.Height
			dst, flip := spriteAt(s, i, sh*src.W/src.H, sh, r.W, r.H, t)
			dst.X, dst.Y = dst.X+r.X, dst.Y+r.Y
			swims = append(swims, swim{bm, src, dst, flip, s.Opacity})
		}
	}
	for _, b := range bands {
		most := float32(1)
		if b.side {
			most = l.sideCap[dark]
			if most <= 0 {
				continue
			}
		}
		p.Clip(b.r, 0, func() {
			for _, s := range swims {
				if overlaps(s.dst, b.r) {
					p.ImageRegion(s.bm, s.src, s.dst, ui.ImageOptions{FlipX: s.flip, Tint: ui.RGBA(255, 255, 255, min(s.opacity, most))})
				}
			}
		})
	}
}

// intersect returns the area a and b share, empty when none.
func intersect(a, b ui.Rect) ui.Rect {
	x0, y0 := max(a.X, b.X), max(a.Y, b.Y)
	x1, y1 := min(a.X+a.W, b.X+b.W), min(a.Y+a.H, b.Y+b.H)
	if x1 <= x0 || y1 <= y0 {
		return ui.Rect{}
	}
	return ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// overlaps reports whether a and b share some area.
func overlaps(a, b ui.Rect) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}
