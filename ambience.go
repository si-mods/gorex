package main

import (
	"log"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/config"
	"gorex/internal/theme"
)

// ambience.go draws what a theme shows besides its colors
// (docs/DSK-010-theme-ambience.md): the glows of the window's gradient
// (layer 1), its still images (layer 2) and the brand row of the sidebar.
// Moving layers, the mascot and the ticker come in later phases.

// glow is a theme's glow for one appearance, as the background draws it.
type glow struct {
	x, y, rx, ry float32
	color        ui.Color
}

// themeLook is what the selected theme draws besides its colors: its
// glows for each appearance, light then dark, its still images and its
// brand, and the assets decoded for them, nil until they load.
type themeLook struct {
	glows  [2][]glow
	images []theme.Still
	brand  theme.Brand
	art    *theme.Art
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
	l := &themeLook{images: m.Ambience.Images, brand: m.Brand}
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
