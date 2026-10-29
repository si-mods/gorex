//go:build windows

package rex

import (
	"io/fs"
	"os"
	"strings"
)

// openImageFile refuses a device or pipe path, which opening would wait
// on, then opens path; the regular-file check in readImageFile rejects
// the rest before reading.
func openImageFile(path string) (*os.File, error) {
	switch {
	case strings.HasPrefix(path, `\\.\`),
		strings.HasPrefix(path, `\\?\pipe\`),
		strings.HasPrefix(path, `\\?\GLOBALROOT`):
		return nil, fs.ErrInvalid
	}
	return os.Open(path)
}
