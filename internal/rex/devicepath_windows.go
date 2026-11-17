//go:build windows

package rex

import "strings"

// devicePrefixes start a device or pipe path, in lower case with every
// slash a backslash: the device namespace, and the pipe and GLOBALROOT
// names of the `\\?\` one. `\\?\C:\x` and `\\?\UNC\server\share` name
// files and are not here.
var devicePrefixes = []string{`\\.\`, `\\?\pipe\`, `\\?\globalroot`}

// isDevicePath tells a path of a device or a pipe, which opening or
// reading may wait on. Windows takes "/" for `\`, so both are read.
func isDevicePath(path string) bool {
	norm := strings.ToLower(strings.ReplaceAll(path, "/", `\`))
	for _, p := range devicePrefixes {
		if strings.HasPrefix(norm, p) {
			return true
		}
	}
	return false
}
