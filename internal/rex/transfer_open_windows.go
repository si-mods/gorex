//go:build windows

package rex

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// openToSend opens path for a get, a regular file only. It refuses a
// device or pipe path, which opening would wait on, looks before it
// opens, and looks at the open file again, so that one swapped in
// between is refused too.
func openToSend(path string) (*os.File, fs.FileInfo, error) {
	for _, p := range []string{`\.\`, `\?\pipe\`, `\?\GLOBALROOT`} {
		if strings.HasPrefix(strings.ToLower(path), strings.ToLower(p)) {
			return nil, nil, fmt.Errorf("rex: %s %s", path, notRegular)
		}
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
