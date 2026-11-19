//go:build darwin || windows

package rex

import (
	"errors"
	"io/fs"
	"os"
)

// errReadAsUser is the error of a read as another user, which a server
// here never makes (sessionUserSupported): its sessions run as its own user.
var errReadAsUser = errors.New("rex: sessions run as another user on Linux only")

func openAsUserHelper(spawner, string) (*os.File, fs.FileInfo, error) {
	return nil, nil, errReadAsUser
}

func listDirAsUserHelper(spawner, string, string, bool) (DirListResult, error) {
	return DirListResult{}, errReadAsUser
}

func sendOpened(string) error { return errReadAsUser }
