//go:build darwin || linux || windows

package rex

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// asSessionUser makes s run its sessions as another user from now on, as
// EnvSessionUser does, and replaces the helper that reads as that user
// with open and list: no test may switch users without root.
func asSessionUser(t *testing.T, s *Server, open func(spawner, string) (*os.File, fs.FileInfo, error), list func(spawner, string, string, bool) (DirListResult, error)) *sessionUser {
	t.Helper()
	u := &sessionUser{name: "alice", uid: 4242, gid: 4242, groups: []uint32{4242}, home: t.TempDir(), shell: "/bin/sh"}
	oldOpen, oldList, oldHome := openAsUser, listDirAsUser, s.home
	openAsUser, listDirAsUser = open, list
	s.setSessionUser(u)
	t.Cleanup(func() {
		s.sessUser, s.home = nil, oldHome
		openAsUser, listDirAsUser = oldOpen, oldList
	})
	return u
}

// TestFileGetReadsAsSessionUser: a server whose sessions run as another
// user opens the file a get names as that user, and sends nothing that
// user may not read, though the server may (#720). A server of no session
// user opens it itself, as before.
func TestFileGetReadsAsSessionUser(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("the server may read this"), 0o600); err != nil {
		t.Fatal(err)
	}
	sid := transferSession(t, c, c, shellDir(t))
	var asked []string
	var as *sessionUser
	u := asSessionUser(t, s, func(sp spawner, p string) (*os.File, fs.FileInfo, error) {
		as = sp.user
		asked = append(asked, p)
		return nil, nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrPermission}
	}, nil)
	_, _, err := c.GetFile(context.Background(), sid, path)
	if err == nil || !strings.HasSuffix(err.Error(), "permission denied") {
		t.Errorf("get as the session user: %v, want permission denied", err)
	}
	if len(asked) != 1 || asked[0] != path || as != u {
		t.Errorf("the helper opened %q as %+v, want %q as %+v", asked, as, path, u)
	}
	s.sessUser = nil
	rc, _, err := c.GetFile(context.Background(), sid, path)
	if err != nil {
		t.Fatalf("get with no session user: %v", err)
	}
	rc.Close()
	if len(asked) != 1 {
		t.Errorf("the helper opened %q, want only the get as the session user", asked)
	}
}

// TestDirListReadsAsSessionUser: a server whose sessions run as another
// user lists a directory as that user, in that user's home (#720); a
// server of no session user lists it itself.
func TestDirListReadsAsSessionUser(t *testing.T) {
	s, c, home := dirListFixture(t)
	locked := filepath.Join(home, "locked")
	mkdirs(t, filepath.Join(locked, "inner"))
	type call struct {
		user       *sessionUser
		home, path string
		hidden     bool
	}
	var calls []call
	u := asSessionUser(t, s, nil, func(sp spawner, h, p string, hidden bool) (DirListResult, error) {
		calls = append(calls, call{sp.user, h, p, hidden})
		return DirListResult{Path: p, Home: h, Dirs: []string{}, Code: DirDenied}, nil
	})
	if r := dirList(t, c, locked, true); r.Code != DirDenied || len(r.Dirs) != 0 {
		t.Errorf("list as the session user: %+v, want %s", r, DirDenied)
	}
	if want := (call{u, u.home, locked, true}); len(calls) != 1 || calls[0] != want {
		t.Errorf("the helper listed %+v, want %+v", calls, want)
	}
	s.sessUser, s.home = nil, ""
	if r := dirList(t, c, locked, false); r.Code != "" || len(r.Dirs) != 1 || r.Dirs[0] != "inner" {
		t.Errorf("list with no session user: %+v", r)
	}
	if len(calls) != 1 {
		t.Errorf("the helper listed %+v, want only the listing as the session user", calls)
	}
}
