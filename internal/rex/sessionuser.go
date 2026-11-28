//go:build darwin || linux || windows

package rex

import (
	"fmt"
	"log"
	"os"
	"os/user"
	"strconv"
	"strings"
)

// EnvSessionUser names the user a server runs its sessions as, in place of
// the user it runs as. A server started as root with
// GOREX_SESSION_USER=me keeps its socket, layout and remote configuration
// to root, and runs every shell, and the programs in it, as me. Unset,
// sessions run as the server, as they always have.
const EnvSessionUser = "GOREX_SESSION_USER"

// sessionUser is the account sessions run as, when it is not the server's:
// its ids and groups (for the server to set the session's), and its home
// and login shell (for what the session starts with).
type sessionUser struct {
	name   string
	uid    uint32
	gid    uint32
	groups []uint32
	home   string
	shell  string
}

// sessionUserFromEnv resolves EnvSessionUser: nil when it is unset, or
// names the server's own user.
func sessionUserFromEnv() (*sessionUser, error) {
	name := strings.TrimSpace(os.Getenv(EnvSessionUser))
	if name == "" {
		return nil, nil
	}
	if !sessionUserSupported {
		if me, err := user.Current(); err == nil && strings.EqualFold(me.Username, name) {
			return nil, nil
		}
		if _, err := user.Lookup(name); err != nil {
			return nil, fmt.Errorf("rex: %s: %w", EnvSessionUser, err)
		}
		return nil, fmt.Errorf("rex: %s is supported on Linux only", EnvSessionUser)
	}
	u, err := lookupSessionUser(name)
	if err != nil {
		return nil, err
	}
	if u.uid == 0 {
		return nil, fmt.Errorf("rex: %s=%s: sessions must not run as root", EnvSessionUser, name)
	}
	if int(u.uid) == os.Geteuid() {
		return nil, nil
	}
	if os.Geteuid() != 0 {
		return nil, fmt.Errorf("rex: %s=%s: the server must run as root to run sessions as another user", EnvSessionUser, name)
	}
	return u, nil
}

// setSessionUser records the user the server runs its sessions as, and
// says so when a server that runs as root was given none: its sessions
// then run as root. A server that serves the handoff of an upgrade needs
// it as much as one that starts by itself, or the sessions it starts
// after the upgrade are the server's.
func (s *Server) setSessionUser(u *sessionUser) {
	s.sessUser = u
	if u != nil {
		s.home = u.home
	} else if os.Geteuid() == 0 {
		log.Printf("rex: the server runs as root: set %s to run sessions as a user", EnvSessionUser)
	}
}

// lookupSessionUser reads the account name, its ids and groups, and the
// home and login shell of its passwd entry.
func lookupSessionUser(name string) (*sessionUser, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("rex: %s: %w", EnvSessionUser, err)
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("rex: %s: %s: uid %q: %w", EnvSessionUser, name, u.Uid, err)
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("rex: %s: %s: gid %q: %w", EnvSessionUser, name, u.Gid, err)
	}
	if u.HomeDir == "" {
		return nil, fmt.Errorf("rex: %s: %s has no home directory", EnvSessionUser, name)
	}
	// The session's groups replace the server's: without them a session
	// keeps the server's, root's when it runs as root. The user's own
	// group is in the list, as setgid sets it too, harmlessly.
	groups := []uint32{uint32(gid)}
	if ids, err := u.GroupIds(); err == nil {
		seen := map[uint32]bool{uint32(gid): true}
		for _, id := range ids {
			g, err := strconv.ParseUint(id, 10, 32)
			if err != nil || seen[uint32(g)] {
				continue
			}
			seen[uint32(g)] = true
			groups = append(groups, uint32(g))
		}
	}
	return &sessionUser{
		name: u.Username, uid: uint32(uid), gid: uint32(gid), groups: groups,
		home: u.HomeDir, shell: loginShell(name),
	}, nil
}

// spawner starts the sessions of a server: the account they run as, nil
// for the server's own, and the executable whose -session-exec drops one
// to it.
type spawner struct {
	user *sessionUser
	exe  string
}

// home is where the sessions of sp start when neither the client nor the
// layout names a directory: the user's, or the server's.
func (sp spawner) home() string {
	if sp.user != nil {
		return sp.user.home
	}
	home, _ := os.UserHomeDir()
	return home
}

// shell is the login shell of the user sp starts sessions as, "" for the
// server's own, whose shell the environment names.
func (sp spawner) shell() string {
	if sp.user != nil {
		return sp.user.shell
	}
	return ""
}
