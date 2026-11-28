//go:build darwin || linux || windows

package rex

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// teleportLink is a hop of a teleport as a machine keeps it
// (teleports.json): the hop, and the work tree it left from or landed
// in; for one it left from, Departed is the work tree's fingerprint then
// (workState), else "". A teleport back takes the work to a work tree it
// left untouched since.
type teleportLink struct {
	Link     string    `json:"link"`
	Dir      string    `json:"dir"`
	Departed string    `json:"departed,omitempty"`
	At       time.Time `json:"at"`
}

// linksMu holds teleports.json while one changes it.
var linksMu sync.Mutex

// linksPath is the server's teleports.json, in its directory.
func (s *Server) linksPath() string {
	if s.linksFile != "" {
		return s.linksFile
	}
	return filepath.Join(Dir(), "teleports.json")
}

// readLinks reads the hops this machine keeps.
func (s *Server) readLinks() []teleportLink {
	var f struct {
		Links []teleportLink `json:"links"`
	}
	readJSON(s.linksPath(), &f)
	return f.Links
}

// updateLinks changes the hops this machine keeps, and forgets those of
// a directory gone.
func (s *Server) updateLinks(change func([]teleportLink) []teleportLink) error {
	linksMu.Lock()
	defer linksMu.Unlock()
	ls := slices.DeleteFunc(change(s.readLinks()), func(l teleportLink) bool { return !exists(l.Dir) })
	return writeJSON(s.linksPath(), struct {
		Links []teleportLink `json:"links"`
	}{ls})
}

// returnDir finds the work tree a teleport of m comes back to: the most
// recent that left this machine by a hop m names, still a work tree of
// the repo, untouched since it left; "" for none. It returns the name of
// its remote of m's, and the hop.
func (s *Server) returnDir(m *TeleportManifest, tmp string) (dir, remote string, link teleportLink) {
	for i, l := range s.readLinks() {
		if l.Departed == "" || !slices.Contains(m.Links, l.Link) || l.At.Before(link.At) {
			continue
		}
		top, err := gitOut(l.Dir, "rev-parse", "--show-toplevel")
		if err != nil || !samePath(filepath.FromSlash(top), l.Dir) {
			continue
		}
		_, common, ok := gitTop(l.Dir)
		r := ""
		if ok {
			r = matchRemote(common, m.Remotes)
		}
		if r == "" {
			continue
		}
		head, err := gitOut(l.Dir, "rev-parse", "--verify", "-q", "HEAD")
		state := filepath.Join(tmp, fmt.Sprintf("state-%d", i))
		if err != nil || os.MkdirAll(state, 0o700) != nil {
			continue
		}
		if _, fp, err := workState(l.Dir, head, state); err != nil || fp != l.Departed {
			continue // the user worked there since
		}
		dir, remote, link = l.Dir, r, l
	}
	return dir, remote, link
}

// errBehind tells that the work tree a teleport comes back to is not
// behind the incoming HEAD: a new work tree takes the work instead.
var errBehind = errors.New("the work tree went another way")

// backInto moves the work tree at dir, whose remote of the manifest is
// remote, to m's HEAD, from the bundle under tmp or the remote, for the
// changes to be applied on it: its branch, or its detached HEAD, fast
// forwards. First it keeps all it has, files untracked included, as a
// commit under refs/gorex/teleport-backup/<m.Link>, and its index: undo
// puts the work tree back as it was, and done drops the backup.
// errBehind when its HEAD is not an ancestor of m's.
func backInto(dir, remote string, m *TeleportManifest, tmp string) (undo []func(), done func(), err error) {
	done = func() {}
	if err := fetchHead(dir, remote, m.Head, filepath.Join(tmp, "repo.bundle")); err != nil {
		return nil, done, err
	}
	old, err := gitOut(dir, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return nil, done, err
	}
	if _, err := gitOut(dir, "merge-base", "--is-ancestor", old, m.Head); err != nil {
		return nil, done, errBehind
	}
	// The backup: the whole work tree, but what Git ignores, through an
	// index of its own; never the stash, which is the user's.
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "backup.index"),
		"GIT_AUTHOR_NAME=GoRex", "GIT_AUTHOR_EMAIL=gorex@localhost", "GIT_COMMITTER_NAME=GoRex", "GIT_COMMITTER_EMAIL=gorex@localhost"}
	if _, err := gitEnvOut(dir, env, "add", "-A"); err != nil {
		return nil, done, err
	}
	tree, err := gitEnvOut(dir, env, "write-tree")
	if err != nil {
		return nil, done, err
	}
	commit, err := gitEnvOut(dir, env, "commit-tree", "--no-gpg-sign", "-p", old, "-m", "GoRex: the work tree before a teleport came back", tree)
	if err != nil {
		return nil, done, err
	}
	ref := "refs/gorex/teleport-backup/" + m.Link
	if _, err := gitOut(dir, "update-ref", ref, commit); err != nil {
		return nil, done, err
	}
	index, err := gitOut(dir, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		gitOut(dir, "update-ref", "-d", ref)
		return nil, done, err
	}
	index = filepath.FromSlash(index)
	saved, _ := os.ReadFile(index)
	drop := func() { gitOut(dir, "update-ref", "-d", ref) }
	undo = append(undo, drop, func() {
		gitOut(dir, "reset", "-q", "--hard", old)
		removeUntracked(dir)
		gitOut(dir, "read-tree", "-u", "--reset", commit)
		if saved != nil {
			os.WriteFile(index, saved, 0o644)
		}
	})
	// Its untracked files go too: they are in the backup, and the
	// incoming ones take their place.
	if err := removeUntracked(dir); err != nil {
		return undo, done, err
	}
	if _, err := gitOut(dir, "reset", "-q", "--hard", m.Head); err != nil {
		return undo, done, err
	}
	return undo, drop, nil
}

// removeUntracked removes the files Git does not track and does not
// ignore from the work tree at dir; a repo of its own in it stays.
func removeUntracked(dir string) error {
	out, err := gitOut(dir, "ls-files", "-o", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	for _, name := range strings.Split(out, "\x00") {
		if name != "" && !strings.HasSuffix(name, "/") {
			if err := os.Remove(filepath.Join(dir, filepath.FromSlash(name))); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}
