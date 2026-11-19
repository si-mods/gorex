//go:build darwin || linux || windows

package rex

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// sessionReadFlag is the mode a server reads the file system in as the
// user its sessions run as (EnvSessionUser), before -server and the app's
// own arguments:
//
//	gorex -session-read open|list
//
// with a sessionRead as JSON on its input: the path stays out of the
// helper's arguments, which every user of the machine may read.
// The server starts it as that user, as it starts -session-exec, and it
// clears the capabilities the server's ambient set leaves on it first.
// "open" opens the path as file.get sends it (openToSend) and hands the
// descriptor back over the socket at descriptor 3; "list" lists it as
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

// sessionRead is what the helper reads, as JSON on its input.
type sessionRead struct {
	Path   string `json:"path"`
	Home   string `json:"home,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
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
	var r sessionRead
	if len(args) != 1 || (args[0] != "open" && args[0] != "list") {
		return errors.New("gorex: " + sessionReadFlag + ": open or list, with what to read on the input")
	}
	if err := json.NewDecoder(os.Stdin).Decode(&r); err != nil {
		return fmt.Errorf("gorex: %s: %w", sessionReadFlag, err)
	}
	if args[0] == "open" {
		return sendOpened(r.Path)
	}
	var a listAnswer
	var err error
	if a.Result, err = listDir(r.Home, r.Path, r.Hidden); err != nil {
		a.Error = err.Error()
	}
	return json.NewEncoder(os.Stdout).Encode(a)
}
