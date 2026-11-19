//go:build darwin || linux || windows

package rex

import (
	"errors"
	"os"
	"syscall"
)

// sessionExecFlag is the mode a server starts a session of another user
// with, before -server and the app's own arguments:
//
//	gorex -session-exec PATH ARGV0 [ARGV1...]
//
// It runs PATH as ARGV, in this process, so that the session keeps the pid
// its server started.
const sessionExecFlag = "-session-exec"

// SessionExecMain runs sessionExecFlag when args, the program's
// arguments, ask for it, and tells whether they did. A server of another
// user's sessions starts it as that user: it clears the capabilities the
// server's ambient set leaves on a child, then runs the program.
func SessionExecMain(args []string) (bool, error) {
	// The helper that reads the file system as the session user
	// (sessionReadFlag) is started the same way.
	if len(args) > 0 && args[0] == sessionReadFlag {
		return true, sessionReadMain(args[1:])
	}
	if len(args) == 0 || args[0] != sessionExecFlag {
		return false, nil
	}
	path, argv, err := sessionExecArgs(args[1:])
	if err != nil {
		return true, err
	}
	if os.Geteuid() == 0 {
		return true, errors.New("gorex: " + sessionExecFlag + " runs as the session's user, not root")
	}
	if err := dropCapabilities(); err != nil {
		return true, err
	}
	return true, syscall.Exec(path, argv, os.Environ())
}

// sessionExecArgs reads the program and its arguments from the arguments
// of sessionExecFlag: the path, then argv as the program sees it.
func sessionExecArgs(args []string) (path string, argv []string, err error) {
	if len(args) == 0 || args[0] == "" {
		return "", nil, errors.New("gorex: " + sessionExecFlag + ": no program")
	}
	argv = args[1:]
	if len(argv) == 0 {
		argv = []string{args[0]}
	}
	return args[0], argv, nil
}
