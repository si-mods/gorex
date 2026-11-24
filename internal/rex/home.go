package rex

import (
	"log"

	"gorex/internal/homedir"
)

// MigrateHome moves the legacy directories to the home directory
// (docs/MOD-013-home-dir.md). The app calls it before it names GOREX_DIR,
// and Serve and Spawn call it before they take the directory, so the move
// happens only where a session server starts.
func MigrateHome(dev bool) {
	moved, err := homedir.Migrate(dev, fixMovedJob)
	if err != nil {
		log.Printf("rex: the home directory did not move: %v", err)
	} else if moved {
		log.Printf("rex: the home directory is now %s", homedir.Root(dev))
	}
}

// DevDir returns the home directory of a development build, ~/.gorex-dev,
// resolved like Dir.
func DevDir() string {
	return homedir.DataDir(true)
}
