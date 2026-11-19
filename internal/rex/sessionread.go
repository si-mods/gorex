//go:build darwin || linux || windows

package rex

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
)

// sessionReadFlag is the mode a server reads the file system in as the
// user its sessions run as (EnvSessionUser), before -server and the app's
// own arguments:
//
//	gorex -session-read open PATH
//	gorex -session-read list HOME PATH HIDDEN
//
// The server starts it as that user, as it starts -session-exec, and it
// clears the capabilities the server's ambient set leaves on it first.
// "open" opens PATH as file.get sends it (openToSend) and hands the
// descriptor back over the socket at descriptor 3; "list" lists PATH as
// dir.list does (listDir) and writes the result as JSON. So a server that
// runs as root sends and lists only what its session user may read: the
// system decides, with that user's groups and ACLs, not a look at the
// mode bits here.
const sessionReadFlag = "-session-read"

// openAsUser and listDirAsUser read as the session user, in the helper at
// the server's executable: tests replace them.
var (
	openAsUser    = openAsUserHelper
	listDirAsUser = listDirAsUserHelper
)

// openAsSession opens path to send, as openToSend does, as the user the
// sessions run as: the server's own user, or EnvSessionUser's.
func (s *Server) openAsSession(path string) (*os.File, fs.FileInfo, error) {
	if s.sessUser == nil {
		return openToSend(path)
	}
	return openAsUser(s.spawner(), path)
}

// listAsSession lists path, as listDirFunc does, as the user the sessions
// run as.
func (s *Server) listAsSession(home, path string, hidden bool) (DirListResult, error) {
	if s.sessUser == nil {
		return listDirFunc(home, path, hidden)
	}
	return listDirAsUser(s.spawner(), home, path, hidden)
}

// listAnswer is what "-session-read list" writes: the listing, or the
// error's text.
type listAnswer struct {
	Result DirListResult `json:"result"`
	Error  string        `json:"error,omitempty"`
}

// sessionReadMain runs sessionReadFlag's arguments, args, in the helper,
// which runs as the session user.
func sessionReadMain(args []string) error {
	if os.Geteuid() == 0 {
		return errors.New("gorex: " + sessionReadFlag + " runs as the session's user, not root")
	}
	if err := dropCapabilities(); err != nil {
		return err
	}
	switch {
	case len(args) == 2 && args[0] == "open":
		return sendOpened(args[1])
	case len(args) == 4 && args[0] == "list":
		var a listAnswer
		var err error
		if a.Result, err = listDir(args[1], args[2], args[3] == "1"); err != nil {
			a.Error = err.Error()
		}
		return json.NewEncoder(os.Stdout).Encode(a)
	}
	return errors.New("gorex: " + sessionReadFlag + ": open PATH, or list HOME PATH HIDDEN")
}
