package main

import (
	"log"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorex/internal/config"
	"gorex/internal/theme"
	"gorex/locales"
)

// ambience.go holds appearance.ambience, the user's control over what a
// theme's shaders draw and move (docs/DSK-012-theme-shaders.md), and the
// load of a theme's assets that they read. GoRex draws no look of its own
// from a theme (docs/DSK-008-theme.md, Non-goals): the glows, still
// images, sprites and brand row of docs/DSK-010-theme-ambience.md are
// gone.

// lookGen counts the theme loads, so that the asset load of a theme the
// user left does not store its shaders.
var lookGen atomic.Int64

// ambienceShown reports whether the window draws the theme's ambience:
// not with appearance.ambience off.
func ambienceShown() bool { return prefs.Ambience != ambienceOff }

// artLoads counts the decodes loadArt runs, so that a test can wait for
// them.
var artLoads sync.WaitGroup

// loadArt decodes the assets that the shaders of m, the theme name, read
// from the theme's folder, off the UI thread, and checks the shaders with
// them. When they are in, every window draws again.
func loadArt(name string, m *theme.Manifest) {
	gen := lookGen.Load()
	if m == nil || m.Shaders == nil || len(m.Assets) == 0 {
		return
	}
	dir, err := theme.AssetFolder(theme.Dir(config.Dir()), name)
	if err != nil {
		return
	}
	artLoads.Add(1)
	go func() {
		defer artLoads.Done()
		art, warn := theme.LoadAssets(dir, m)
		for _, w := range warn {
			log.Printf("gorex settings: appearance.theme %q: %s", name, w)
		}
		if lookGen.Load() != gen {
			return
		}
		storeThemeShaders(name, m, art, gen)
		for _, w := range mygo.Windows() {
			w.Update(func() {}) // posts the redraw, and does not wait for it
		}
	}()
}

// The values of appearance.ambience besides "", which is the default:
// the theme's shaders draw and move.
const (
	ambienceStill = "still"
	ambienceOff   = "off"
)

// themeAmbient reports whether the selected theme has ambience: what the
// sidebar's button and Toggle Theme Ambience turn off and on. That is a
// background shader, which Ambience Off takes away, or a shader that
// moves, which Ambience Off and Still stop.
func themeAmbient() bool {
	s := activeShaders()
	return s != nil && (s.background != nil || s.anyTime)
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
