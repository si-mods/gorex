//go:build !linux

package cli

import (
	"errors"
	"io"
)

func updateLocal(io.Writer) error {
	return errors.New("CLI updates are available for the Linux user install; use Check for Updates in the app on this platform")
}
