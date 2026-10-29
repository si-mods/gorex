//go:build windows

package rex

import "os"

// openImageFile opens path; the regular-file check in readImageFile
// rejects devices and pipes before reading.
func openImageFile(path string) (*os.File, error) { return os.Open(path) }
