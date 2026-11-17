package main

import (
	"io"
	"log"
	"os"
	"path/filepath"

	"gorex/internal/rex"
)

// appLogLimit is the size past which app.log is kept as app.log.1 at the
// next start, so that the log stays within about twice this.
const appLogLimit = 1 << 20

// rotateLog renames path to path+".1", replacing that, when it is larger
// than limit.
func rotateLog(path string, limit int64) {
	if fi, err := os.Stat(path); err == nil && fi.Size() > limit {
		os.Rename(path, path+".1")
	}
}

// startAppLog sends the app's log, the standard one, to app.log in its
// data directory, beside the server's server.log: an installed app has no
// terminal, so the log would go nowhere. Started from a terminal it
// goes to stderr too. It does nothing if the file cannot be opened.
func startAppLog() {
	if err := os.MkdirAll(rex.Dir(), 0o700); err != nil {
		return
	}
	path := filepath.Join(rex.Dir(), "app.log")
	rotateLog(path, appLogLimit)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	// The file first: MultiWriter stops at the first writer that fails,
	// and a GUI app's stderr on Windows is no handle at all.
	log.SetOutput(io.MultiWriter(f, os.Stderr))
}
