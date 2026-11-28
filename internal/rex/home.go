package rex

import (
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"

	"gorex/internal/homedir"
)

// MigrateHome moves the legacy directories to the home directory
// (docs/MOD-013-home-dir.md). The app calls it before it names GOREX_DIR,
// and Serve and Spawn call it before they take the directory, so the move
// happens only where a session server starts.
func MigrateHome(dev bool) {
	moved, err := homedir.Migrate(dev, fixMovedJob)
	if err != nil {
		log.Printf("rex: the home directory did not move: %v", err)
	} else if moved {
		log.Printf("rex: the home directory is now %s", homedir.Root(dev))
	}
}

// DevDir returns the home directory of a development build, ~/.gorex-dev,
// resolved like Dir.
func DevDir() string {
	return homedir.DataDir(true)
}

// ServerMain runs the server when args, the program's arguments, ask
// for it, and tells whether they did: -server serves, -server -resume
// PATH serves the sessions a server upgrading to this executable handed
// it (Resume), -server -handoff-version prints the version of the
// handoffs it reads, which an upgrading server asks first, and -server
// -protocol-version prints ProtocolVersion (SpeaksProtocol).
func ServerMain(args []string) (bool, error) {
	if len(args) == 0 || args[0] != "-server" {
		return false, nil
	}
	log.SetPrefix("[server] ")
	switch {
	case len(args) > 1 && args[1] == "-handoff-version":
		fmt.Fprintln(os.Stdout, handoffVersion)
		return true, nil
	case len(args) > 1 && args[1] == "-protocol-version":
		fmt.Fprintln(os.Stdout, ProtocolVersion)
		return true, nil
	case len(args) > 2 && args[1] == "-resume":
		return true, Resume(args[2])
	}
	return true, Serve()
}

// Dir returns the directory of the server's socket, log and state:
// the home directory's resolution of docs/MOD-013-home-dir.md.
func Dir() string {
	return homedir.DataDir(false)
}

// SocketPath returns the path of the server's socket: in Dir, unless that
// is too long a path for a socket, then in the temporary directory.
func SocketPath() string { return dirSocket("server.sock") }

// GatewaySocketPath returns the path of the Rex API gateway's socket for
// the connections to RexAPIPort, which the server dials (toGateway): as
// SocketPath's, in Dir or the temporary directory.
func GatewaySocketPath() string { return dirSocket("rexapi-tailcat.sock") }

// dirSocket is the socket name in Dir, or in the temporary directory, by
// a hash of that path, when it is too long a path for a socket.
func dirSocket(name string) string {
	p := filepath.Join(Dir(), name)
	if len(p) < 100 {
		return p
	}
	h := fnv.New32a()
	h.Write([]byte(p))
	name = fmt.Sprintf("gorex-%x.sock", h.Sum32())
	if uid := os.Getuid(); uid >= 0 { // Windows has none, and a temporary directory per user
		name = fmt.Sprintf("gorex-%d-%x.sock", uid, h.Sum32())
	}
	return filepath.Join(os.TempDir(), name)
}
