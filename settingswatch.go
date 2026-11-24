package main

import (
	"context"
	"crypto/sha256"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/egoist/mygo"

	"gorex/internal/rex"
)

// The watch of settings.json (docs/DSK-011-live-settings.md): an edit
// made outside the app applies while it runs. A write of the app itself
// does not reload — saveSettings records the bytes it writes, and the
// watcher skips them by hash, so there is no loop and no double apply.

// postToMain runs fn on the main thread, as a.updater does for the
// hosts; tests, which run no event loop, take its place.
var postToMain = mygo.RunOnMain

// settingsMu guards settingsWritten and settingsBad: the hash of the
// settings.json bytes the app wrote or already decoded, whose events are
// skipped, and of the bytes last seen failing to decode, logged once.
var (
	settingsMu      sync.Mutex
	settingsWritten [sha256.Size]byte
	settingsBad     [sha256.Size]byte
)

// recordSettingsWritten notes b as the settings.json bytes accounted
// for: written by the app, or read and decoded.
func recordSettingsWritten(b []byte) {
	settingsMu.Lock()
	settingsWritten = sha256.Sum256(b)
	settingsMu.Unlock()
}

// settingsWrittenIs reports whether b is what recordSettingsWritten last
// recorded: the app's own write, or content already decoded.
func settingsWrittenIs(b []byte) bool {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	return settingsWritten == sha256.Sum256(b)
}

// watchSettings starts the watch of settings.json for the app's
// lifetime — mygo stops its watches when it quits. It needs the app,
// which start reaches ready; tests, which start without one, skip it.
func (st *store) watchSettings() {
	if !mygo.App.IsReady() {
		return
	}
	ctx := context.Background()
	// The watch is of the directory: one of the file could not report its
	// creation when it does not exist yet.
	os.MkdirAll(rex.Dir(), 0o700)
	w, err := mygo.WatchFiles(ctx, rex.Dir(), mygo.FileWatchOptions{Ignore: settingsWatchIgnore})
	if err != nil {
		log.Printf("gorex settings: watch: %v", err)
		return
	}
	go func() {
		for {
			events, err := w.Next(ctx)
			if err != nil {
				return // the watch closed, or its directory went away
			}
			for _, e := range events {
				// A rename onto settings.json is a write too; a hint,
				// which an overflow can send for the whole directory,
				// may be one.
				if e.Path == "settings.json" || e.Op == mygo.FileHint {
					st.settingsFileChanged()
					break
				}
			}
		}
	}()
}

// settingsWatchIgnore keeps the watch's snapshot to the settings file's
// own names: settings.json itself, its backup, and the temporary file of
// writeFileAtomic.
func settingsWatchIgnore(path string, isDir bool) bool {
	if isDir {
		return path != "."
	}
	return path != "settings.json" && path != "settings.json.bak" &&
		!(strings.HasPrefix(path, "settings.json.") && strings.HasSuffix(path, ".tmp"))
}

// settingsFileChanged applies a settings.json that changed on disk, when
// the change is not the app's own: it reads the file, skips it when its
// hash is a write's the app recorded, decodes on a copy and posts the
// result to the main thread. A file that does not decode keeps prefs,
// with one log line for its content.
func (st *store) settingsFileChanged() {
	b, err := os.ReadFile(settingsPath())
	if err != nil {
		return // it went away, or cannot be read: nothing to apply
	}
	if settingsWrittenIs(b) {
		return
	}
	next, ok := decodeSettings(b)
	if !ok {
		settingsMu.Lock()
		if h := sha256.Sum256(b); h != settingsBad {
			settingsBad = h
			log.Printf("gorex settings: settings.json does not decode; keeping the settings that run")
		}
		settingsMu.Unlock()
		return
	}
	postToMain(func() { st.applySettingsFile(next) })
}

// applySettingsFile takes next as the settings, on the main thread, as a
// load does after the decode: the named theme's manifest, Ghostty's
// configuration read again, the panes' font, theme and choices over it,
// the glass, the appearance, the language, and every window drawn again,
// the Settings window too.
func (st *store) applySettingsFile(next settings) {
	prefs = next
	_ = loadTheme()
	loadGhostty()
	if len(st.apps) > 0 {
		st.apps[0].applyPrefs()
		st.apps[0].applyGlass()
	} else {
		ghostty = ghosttyRaw.withPrefs(prefs)
		applyGhostty()
		applyAppearance()
	}
	setLocale(prefs.Language)
	st.syncBadge()
	for _, a := range st.apps {
		a.syncSidebarItem()
		a.do(func() {})
	}
	if w := settingsUI.win; w != nil {
		w.Update(func() {})
	}
	// The settings.json page's buffer follows the file this came from.
	syncSettingsJSONEditor()
}
