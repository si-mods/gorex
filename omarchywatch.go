package main

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"gorex/internal/omarchy"
	"gorex/internal/theme"
)

// The watch of an Omarchy desktop (docs/DSK-015-omarchy.md): the palette of
// the desktop's current theme is read when the app starts, and after a
// theme change the windows repaint when the resolved palette differs from
// the one in use. GoRex writes nothing into the desktop's configuration.

// omarchyDebounce is how long a change of the theme folder waits for more
// before the palette is read: Omarchy writes a theme in several files.
const omarchyDebounce = 200 * time.Millisecond

// omarchyMu guards the palette the desktop last showed and whether it read;
// omarchyWarned keeps the log to one line for a theme that does not read.
var (
	omarchyMu        sync.Mutex
	omarchyPalette   omarchy.Palette
	omarchyPaletteOK bool
	omarchyWarned    bool
)

// omarchyRefresh reads the desktop's palette into memory, at startup and
// after the settings change.
func omarchyRefresh() {
	pal, ok := omarchyPaletteAt(omarchy.DefaultPaths())
	omarchyMu.Lock()
	omarchyPalette, omarchyPaletteOK = pal, ok
	omarchyMu.Unlock()
}

// omarchyManifest is the theme the desktop's palette makes, or nil when the
// app does not follow the desktop or has no palette to follow.
func omarchyManifest() *theme.Manifest {
	if !omarchyFollowing() {
		return nil
	}
	omarchyMu.Lock()
	pal, ok := omarchyPalette, omarchyPaletteOK
	omarchyMu.Unlock()
	if !ok {
		return nil
	}
	return omarchy.Manifest(pal)
}

// omarchyFollowing reports whether the settings ask for the desktop's
// palette: no appearance.theme, and appearance.omarchy not false. An
// explicit appearance.theme wins (docs/DSK-015-omarchy.md, Behavior).
func omarchyFollowing() bool {
	return omarchy.Settings{Theme: prefs.Theme, Omarchy: prefs.Omarchy}.Follows()
}

// omarchyPaletteAt reads the palette of the desktop at p, or reports that
// there is none to read: no Omarchy installation, or a theme that does not
// read. A theme that does not read is one line in the log.
func omarchyPaletteAt(p omarchy.Paths) (omarchy.Palette, bool) {
	if omarchy.Detect(p) != nil {
		return omarchy.Palette{}, false
	}
	pal, err := omarchy.ReadPalette(p)
	if err != nil {
		omarchyMu.Lock()
		first := !omarchyWarned
		omarchyWarned = true
		omarchyMu.Unlock()
		if first {
			log.Printf("gorex omarchy: %v; keeping the built-in palettes", err)
		}
		return omarchy.Palette{}, false
	}
	omarchyMu.Lock()
	omarchyWarned = false
	omarchyMu.Unlock()
	return pal, true
}

// omarchyEvent reads the palette again after a change of the theme folder,
// and reports it with whether it differs from (prev, prevOK), the palette in
// use.
func omarchyEvent(p omarchy.Paths, prev omarchy.Palette, prevOK bool) (omarchy.Palette, bool, bool) {
	pal, ok := omarchyPaletteAt(p)
	return pal, ok, ok != prevOK || pal != prev
}

// watchOmarchy follows the desktop's theme folder for the app's lifetime —
// mygo stops its watches when it quits. It needs the app, which start
// reaches ready; tests, which start without one, skip it.
func (st *store) watchOmarchy() {
	if !mygo.App.IsReady() {
		return
	}
	p := omarchy.DefaultPaths()
	if omarchy.Detect(p) != nil {
		return
	}
	if _, err := os.Stat(p.CurrentDir()); err != nil {
		return // no theme rendered yet: the next start follows it
	}
	omarchyMu.Lock()
	prev, prevOK := omarchyPalette, omarchyPaletteOK
	omarchyMu.Unlock()
	if err := watchOmarchyDir(context.Background(), p, prev, prevOK, st.omarchyChanged); err != nil {
		log.Printf("gorex omarchy: watch: %v", err)
	}
}

// watchOmarchyDir watches the folder the desktop swaps its theme under and
// calls changed with each palette that differs from the one before, once
// per burst of changes: the swap of the theme records a different palette
// once, and a write of the same colors records nothing.
func watchOmarchyDir(ctx context.Context, p omarchy.Paths, prev omarchy.Palette, prevOK bool, changed func(omarchy.Palette, bool)) error {
	w, err := mygo.WatchFiles(ctx, p.CurrentDir(), mygo.FileWatchOptions{Debounce: omarchyDebounce})
	if err != nil {
		return err
	}
	go func() {
		for {
			if _, err := w.Next(ctx); err != nil {
				return // the watch closed, or its folder went away
			}
			pal, ok, differs := omarchyEvent(p, prev, prevOK)
			if !differs {
				continue
			}
			prev, prevOK = pal, ok
			changed(pal, ok)
		}
	}()
	return nil
}

// omarchyChanged takes a palette the desktop changed to, on the main
// thread, off the watcher's goroutine.
func (st *store) omarchyChanged(pal omarchy.Palette, ok bool) {
	postToMain(func() { st.applyOmarchyPalette(pal, ok) })
}

// applyOmarchyPalette keeps the palette the desktop changed to, and when
// the app follows the desktop, applies its theme and draws the windows
// again, as a settings change does.
func (st *store) applyOmarchyPalette(pal omarchy.Palette, ok bool) {
	omarchyMu.Lock()
	omarchyPalette, omarchyPaletteOK = pal, ok
	omarchyMu.Unlock()
	if !omarchyFollowing() {
		return
	}
	_ = loadTheme()
	if len(st.apps) > 0 {
		st.apps[0].applyPrefs()
		st.apps[0].applyGlass()
	} else {
		ghostty = ghosttyRaw.withPrefs(prefs)
		applyGhostty()
		applyAppearance()
	}
	for _, a := range st.apps {
		a.syncSidebarItem()
		a.do(func() {})
	}
	if w := settingsUI.win; w != nil {
		w.Update(func() {})
	}
}
