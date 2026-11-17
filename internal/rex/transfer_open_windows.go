//go:build windows

package rex

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// isDevicePath reports whether path names a device or a named pipe, in
// either slash direction: `\\.\...`, `\\?\pipe\...`, `\\?\GLOBALROOT...`,
// or a UNC path whose share is "pipe" (`\\host\pipe\x`, also as
// `\\?\UNC\host\pipe\x`), which reaches the host's pipes.
func isDevicePath(path string) bool {
	norm := strings.ToLower(strings.ReplaceAll(path, "/", `\`))
	for _, p := range []string{`\\.\`, `\\?\pipe\`, `\\?\globalroot`} {
		if strings.HasPrefix(norm, p) {
			return true
		}
	}
	if rest, ok := strings.CutPrefix(norm, `\\?\unc\`); ok {
		norm = `\\` + rest
	}
	if rest, ok := strings.CutPrefix(norm, `\\`); ok {
		parts := strings.Split(rest, `\`)
		return len(parts) > 1 && parts[1] == "pipe"
	}
	return false
}

// openToSend opens path for a get, a regular file only. It refuses a
// device or pipe path, which opening would wait on, looks before it
// opens, and looks at the open file again, so that one swapped in
// between is refused too.
func openToSend(path string) (*os.File, fs.FileInfo, error) {
	if isDevicePath(path) {
		return nil, nil, fmt.Errorf("rex: %s %s", path, notRegular)
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if err := checkRegular(path, st); err != nil {
		return nil, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	if st, err = f.Stat(); err == nil {
		err = checkRegular(path, st)
	}
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, st, nil
}
