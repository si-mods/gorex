//go:build darwin || linux || windows

package rex

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// teleportPack packs session sid for a teleport, on a connection of this
// machine: a Response line whose Data is the manifest, then the package,
// Size bytes; or a Response of the error.
func (s *Server) teleportPack(conn net.Conn, sid string) {
	m, pkg, err := s.pack(sid)
	if pkg != nil {
		defer os.Remove(pkg.Name())
		defer pkg.Close()
	}
	if err != nil {
		writeAnswer(conn, Response{Error: "teleport: " + err.Error()})
		return
	}
	data, _ := json.Marshal(m)
	if writeAnswer(conn, Response{Data: data}) != nil {
		return
	}
	if _, err := pkg.Seek(0, io.SeekStart); err == nil {
		io.Copy(conn, pkg)
	}
}

// writeAnswer writes res as a line.
func writeAnswer(conn net.Conn, res Response) error {
	b, _ := json.Marshal(res)
	_, err := conn.Write(append(b, '\n'))
	return err
}

// pack packs session sid into a temporary file, which the caller removes,
// and describes it.
func (s *Server) pack(sid string) (TeleportManifest, *os.File, error) {
	var m TeleportManifest
	ss, err := s.session(sid)
	if err != nil {
		return m, nil, err
	}
	in := s.info(ss)
	if code := TeleportRefusal(in); code != "" {
		return m, nil, errors.New(teleportReason(code, in.Program))
	}
	if in.Dir == "" {
		return m, nil, errors.New("the session's directory is not known")
	}
	root, err := gitOut(in.Dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return m, nil, errors.New("not a Git work tree")
	}
	prefix, _ := gitOut(in.Dir, "rev-parse", "--show-prefix")
	head, err := gitOut(root, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return m, nil, errors.New("the repository has no commit yet")
	}
	branch, _ := gitOut(root, "symbolic-ref", "--short", "-q", "HEAD")
	// Its name: what hostInfo writes before hostDone closes, a second at
	// most.
	<-s.hostDone
	m = TeleportManifest{
		Version: teleportVersion, Host: s.host.Name, Program: "shell",
		Remotes: remoteURLs(root), Branch: branch, Head: head, HomeRel: s.homeRel(filepath.FromSlash(root)),
		Root: filepath.FromSlash(root), Dir: strings.TrimSuffix(prefix, "/"),
		Cols: in.Cols, Rows: in.Rows,
	}
	if len(m.Remotes) == 0 {
		return m, nil, errors.New("the repository has no remote to clone it from")
	}
	if !in.Idle {
		m.Program, m.Session = "claude", in.AgentSession
	}

	tmp, err := os.MkdirTemp("", "gorex-teleport-")
	if err != nil {
		return m, nil, err
	}
	defer os.RemoveAll(tmp)
	pkg, err := os.CreateTemp("", "gorex-teleport-*.tar")
	if err != nil {
		return m, nil, err
	}
	tw := tar.NewWriter(pkg)
	add := func(name, path string) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: st.Size(), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		return err
	}
	// The commits no remote has: those the target cannot fetch.
	have := remoteHave(root)
	if n, err := gitIn(root, have, "rev-list", "--count", "HEAD", "--stdin"); err == nil && n != "0" {
		b := filepath.Join(tmp, "repo.bundle")
		if _, err := gitIn(root, have, "bundle", "create", b, "HEAD", "--stdin"); err != nil {
			return m, pkg, fmt.Errorf("bundling the commits: %w", err)
		}
		if err := add("repo.bundle", b); err != nil {
			return m, pkg, err
		}
	}
	n, fp, err := workState(root, head, tmp)
	if err != nil {
		return m, pkg, err
	}
	patch, untracked := filepath.Join(tmp, "worktree.patch"), filepath.Join(tmp, "untracked.tar")
	if st, err := os.Stat(patch); err == nil && st.Size() > 0 {
		if err := add("worktree.patch", patch); err != nil {
			return m, pkg, err
		}
	}
	if n > 0 {
		if err := add("untracked.tar", untracked); err != nil {
			return m, pkg, err
		}
	}
	if m.Program == "claude" {
		if err := s.packConversation(add, in, m.Root); err != nil {
			return m, pkg, err
		}
	}
	if f, err := ss.Format(FormatOptions{Kind: "text"}); err == nil {
		text := strings.TrimRight(string(f.Content), " \r\n")
		if len(text) > scrollbackMax {
			text = text[len(text)-scrollbackMax:]
			if i := strings.IndexByte(text, '\n'); i >= 0 {
				text = text[i+1:]
			}
		}
		if err := tw.WriteHeader(&tar.Header{Name: "scrollback.txt", Mode: 0o600, Size: int64(len(text)), Typeflag: tar.TypeReg}); err != nil {
			return m, pkg, err
		}
		if _, err := io.WriteString(tw, text); err != nil {
			return m, pkg, err
		}
	}
	if err := tw.Close(); err != nil {
		return m, pkg, err
	}
	st, err := pkg.Stat()
	if err != nil {
		return m, pkg, err
	}
	if st.Size() > teleportMax {
		return m, pkg, fmt.Errorf("the package is larger than %d MiB", teleportMax>>20)
	}
	m.Size = st.Size()
	// This hop, and those that came here before, of the work tree: a
	// teleport back names them. Kept now, as nothing tells this machine
	// of the hop's end.
	m.Link = newID()
	err = s.updateLinks(func(ls []teleportLink) []teleportLink {
		for _, l := range ls {
			if samePath(l.Dir, m.Root) {
				m.Links = append(m.Links, l.Link)
			}
		}
		return append(ls, teleportLink{Link: m.Link, Dir: m.Root, Departed: fp, At: time.Now()})
	})
	return m, pkg, err
}

// workState writes, of the work tree at root whose HEAD is head, the
// changes from HEAD to dir/worktree.patch and its untracked files to
// dir/untracked.tar, which it returns the number of, and their
// fingerprint: what a teleport takes of the work tree, which tells
// whether it changed since.
func workState(root, head, dir string) (int, string, error) {
	patch, untracked := filepath.Join(dir, "worktree.patch"), filepath.Join(dir, "untracked.tar")
	if err := gitTo(root, patch, "diff", "--binary", "--no-color", "--no-ext-diff", "HEAD"); err != nil {
		return 0, "", fmt.Errorf("diffing the work tree: %w", err)
	}
	n, err := packUntracked(root, untracked)
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	io.WriteString(h, head+"\n")
	for _, path := range []string{patch, untracked} {
		f, err := os.Open(path)
		if err != nil {
			return 0, "", err
		}
		io.Copy(h, f)
		f.Close()
		h.Write([]byte{0})
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// packUntracked writes the files Git does not track and does not ignore,
// at root, to a tar at path, and returns how many. Only regular files go:
// a link would point anywhere on the target.
func packUntracked(root, path string) (int, error) {
	out, err := gitOut(root, "ls-files", "-o", "--exclude-standard", "-z")
	if err != nil {
		return 0, fmt.Errorf("listing the untracked files: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	n := 0
	var size int64
	for _, name := range strings.Split(out, "\x00") {
		if name == "" || strings.HasSuffix(name, "/") {
			continue // a repo of its own
		}
		st, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		if size += st.Size(); size > untrackedMax {
			return 0, fmt.Errorf("the untracked files are larger than %d MiB", untrackedMax>>20)
		}
		mode := int64(0o644)
		if st.Mode()&0o111 != 0 {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: st.Size(), Typeflag: tar.TypeReg}); err != nil {
			return 0, err
		}
		src, err := os.Open(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return 0, err
		}
		_, err = io.CopyN(tw, src, st.Size())
		src.Close()
		if err != nil {
			return 0, err
		}
		n++
	}
	return n, tw.Close()
}

// packConversation adds Claude Code's conversation of in, and the
// directory of its subagents and tool results, under claude/.
func (s *Server) packConversation(add func(name, path string) error, in SessionInfo, root string) error {
	config := in.AgentConfigDir
	if config == "" {
		var err error
		if config, err = s.claudeConfigDir(); err != nil {
			return err
		}
	}
	id := in.AgentSession
	// In its directory's project, or another's when Claude Code moved.
	file := filepath.Join(config, "projects", claudeSlug(in.Dir), id+".jsonl")
	if _, err := os.Stat(file); err != nil {
		found, _ := filepath.Glob(filepath.Join(config, "projects", "*", id+".jsonl"))
		if len(found) == 0 {
			return errors.New("Claude Code's conversation file is not found")
		}
		file = found[0]
	}
	if err := add("claude/"+id+".jsonl", file); err != nil {
		return err
	}
	dir := strings.TrimSuffix(file, ".jsonl")
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		return add("claude/"+id+"/"+filepath.ToSlash(rel), path)
	})
	if err != nil {
		return err
	}
	// The backups /rewind restores files from, flat, but those of files
	// outside the work tree, which the target drops: no rewind writes
	// outside the work tree on another machine.
	skip := outsideBackups(file, root)
	history := filepath.Join(config, "file-history", id)
	entries, err := os.ReadDir(history)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !backupName(e.Name()) || skip[e.Name()] {
			continue
		}
		if err := add("claude/file-history/"+id+"/"+e.Name(), filepath.Join(history, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// outsideBackups are the backups of the conversation at file that only
// files outside root, the work tree, have.
func outsideBackups(file, root string) map[string]bool {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	in, out := map[string]bool{}, map[string]bool{}
	for _, line := range bytes.Split(b, []byte("\n")) {
		for key, name := range trackedBackups(line) {
			if !absPath(key) {
				in[name] = true
			} else if _, ok := movePath(key, root, root); ok {
				in[name] = true
			} else {
				out[name] = true
			}
		}
	}
	for name := range in {
		delete(out, name)
	}
	return out
}

// teleportIn unpacks a package that follows on conn, of peer, and starts
// its session: it answers the manifest m at once, with {"ok":true} or the
// error, and once the package is in, with a Response of the session or of
// the error, which names the step that failed. Whatever it made before a
// failure goes.
func (s *Server) teleportIn(conn net.Conn, r *bufio.Reader, m *TeleportManifest, peer string) {
	// What a failure leaves goes before the answer tells of it.
	var undo []func()
	defer func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}()
	fail := func(err error) {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		undo = nil
		writeAnswer(conn, Response{Error: "teleport: " + err.Error()})
	}
	if m == nil {
		fail(errors.New("no manifest"))
		return
	}
	if err := m.check(); err != nil {
		fail(err)
		return
	}
	config := ""
	if m.Program == "claude" {
		var err error
		if config, err = s.claudeConfigDir(); err != nil {
			fail(err)
			return
		}
		// A shell GoRex cannot type the resume into would show a shell
		// alone: refused before anything is made.
		if _, _, shell, err := shellCommand(nil); err != nil {
			fail(err)
			return
		} else if _, ok := ClaudeResume(shell, m.Session, ""); !ok {
			fail(fmt.Errorf("GoRex cannot type Claude Code's resume into %s, the shell here", shell))
			return
		}
	}
	if _, err := conn.Write([]byte(`{"ok":true}` + "\n")); err != nil {
		return
	}
	tmp, err := os.MkdirTemp("", "gorex-teleport-")
	if err != nil {
		fail(err)
		return
	}
	defer os.RemoveAll(tmp)
	err = receivePackage(io.LimitReader(idleReader{conn, r}, m.Size), m.Size, tmp, m.Session)
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		fail(fmt.Errorf("receiving the package: %w", err))
		return
	}
	step := func(name string, err error) error {
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}

	// Back where the work came from, when it left it untouched; else a
	// new work tree of a clone.
	tree := ""
	var back teleportLink
	keepTree := func() {}
	if d, remote, l := s.returnDir(m, tmp); d != "" {
		u, done, err := backInto(d, remote, m, tmp)
		undo = append(undo, u...)
		switch {
		case err == nil:
			tree, back, keepTree = d, l, done
		case !errors.Is(err, errBehind):
			fail(step("going back to the work tree", err))
			return
		}
	}
	if tree == "" {
		clone, remote := s.findClone(m.Remotes)
		if clone == "" {
			var err error
			if clone, remote, err = s.cloneRepo(m); err != nil {
				fail(step("cloning the repo", err))
				return
			}
		}
		id := newID()[:8]
		tree = filepath.Join(clone, ".gorex", "teleport", id)
		branch := "teleport/" + id
		if err := step("fetching the commits", fetchHead(clone, remote, m.Head, filepath.Join(tmp, "repo.bundle"))); err != nil {
			fail(err)
			return
		}
		excludeTeleports(clone)
		if _, err := gitOut(clone, "worktree", "add", "-q", "-b", branch, tree, m.Head); err != nil {
			fail(step("making the work tree", err))
			return
		}
		undo = append(undo, func() {
			gitOut(clone, "worktree", "remove", "--force", tree)
			gitOut(clone, "branch", "-D", branch)
		})
	}
	if patch := filepath.Join(tmp, "worktree.patch"); exists(patch) {
		if _, err := gitOut(tree, "apply", "--binary", "--whitespace=nowarn", patch); err != nil {
			fail(step("applying the changes", err))
			return
		}
	}
	if err := step("unpacking the untracked files", unpackUntracked(filepath.Join(tmp, "untracked.tar"), tree)); err != nil {
		fail(err)
		return
	}
	dir := filepath.Join(tree, filepath.FromSlash(m.Dir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(step("making the directory", err))
		return
	}
	keep := func() {}
	if m.Program == "claude" {
		placed, done, err := placeConversation(filepath.Join(tmp, "claude"), config, m, tree, dir)
		undo, keep = append(undo, placed...), done
		if err != nil {
			fail(step("placing the conversation", err))
			return
		}
	}
	// A cancel closes the connection: nothing starts then.
	if connGone(conn, r) {
		return
	}
	// The session's id is the hop's, which the client knows before it
	// starts, and the source's window waits for.
	o := CreateOptions{Dir: dir, Cols: m.Cols, Rows: m.Rows, id: m.Link}
	if b, err := os.ReadFile(filepath.Join(tmp, "scrollback.txt")); err == nil && len(b) > 0 {
		o.prelude = []byte("\x1b[2m" + strings.ReplaceAll(plainText(string(b)), "\n", "\r\n") + "\x1b[0m\r\n")
	}
	ss, err := s.create(o, peer)
	if err != nil {
		fail(step("starting the session", err))
		return
	}
	if m.Program == "claude" {
		// The shell takes it: teleportIn checked it would before.
		cmd, _ := ClaudeResume(ss.shell, m.Session, "")
		// Typed once the shell printed, its prompt or what comes before
		// it: a console may drop what comes earlier.
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			ss.mu.Lock()
			out := ss.output
			ss.mu.Unlock()
			if out > 0 {
				break
			}
		}
		ss.inputProgram([]byte(cmd + "\r"))
	}
	// A cancel meanwhile, or a client gone, leaves no session: nothing
	// would show it.
	data, _ := json.Marshal(s.info(ss))
	if connGone(conn, r) || writeAnswer(conn, Response{Data: data}) != nil {
		s.ops.RLock()
		s.end(ss)
		s.ops.RUnlock()
		return
	}
	undo = nil
	keep()
	keepTree()
	// The hop landed here; the one it came back by left nothing to go
	// back to.
	s.updateLinks(func(ls []teleportLink) []teleportLink {
		for i := range ls {
			if ls[i].Link == back.Link {
				ls[i].Departed = ""
			}
		}
		return append(ls, teleportLink{Link: m.Link, Dir: tree, At: time.Now()})
	})
}

// check tells what of the manifest this machine does not take.
func (m *TeleportManifest) check() error {
	switch {
	case m.Version != teleportVersion:
		return fmt.Errorf("a package of version %d: GoRex differs between the machines", m.Version)
	case m.Program != "shell" && m.Program != "claude":
		return errors.New("an unknown program")
	case m.Program == "claude" && !IsUUID(m.Session), m.Program == "shell" && m.Session != "":
		// The id names files of the package and of Claude Code's.
		return errors.New("no conversation id GoRex takes")
	case !hexHead(m.Head):
		return errors.New("no commit")
	case len(m.Remotes) == 0:
		return errors.New("the repository has no remote to clone it from")
	case m.Dir != "" && !localName(m.Dir), m.HomeRel != "" && !localName(m.HomeRel):
		return errors.New("the directory is outside the work tree")
	case m.Size < 0 || m.Size > teleportMax:
		return fmt.Errorf("the package is larger than %d MiB", teleportMax>>20)
	}
	if !hexID(m.Link) || len(m.Links) > 1000 || slices.ContainsFunc(m.Links, func(l string) bool { return !hexID(l) }) {
		return errors.New("a link GoRex did not make")
	}
	for _, u := range m.Remotes {
		if u == "" || strings.HasPrefix(u, "-") || strings.ContainsAny(u, "\x00\r\n") {
			return errors.New("a remote URL Git would not take")
		}
	}
	return nil
}

// hexID tells whether s is an id of newID's.
func hexID(s string) bool {
	if len(s) != 12 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// hexHead tells whether s is a commit's full hash, SHA-1 or SHA-256.
func hexHead(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// errDiverged refuses a conversation that has messages here the incoming
// one lacks: both went on.
var errDiverged = errors.New("the conversation went on here too")

// messageIDs are the uuid fields of a conversation's lines, of its
// messages; lines without one, as summaries, have none.
func messageIDs(path string) (map[string]bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, line := range bytes.Split(b, []byte("\n")) {
		var f struct {
			UUID string `json:"uuid"`
		}
		if json.Unmarshal(line, &f) == nil && f.UUID != "" {
			ids[f.UUID] = true
		}
	}
	return ids, nil
}

// receivePackage reads a package of size bytes from r into dir, at the
// names it gives: those of its parts only, each a regular file, the
// conversation's under claude/ for conversation id.
func receivePackage(r io.Reader, size int64, dir, id string) error {
	cr := &countReader{r: r}
	tr := tar.NewReader(cr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || !packageName(h.Name, id) {
			return fmt.Errorf("an unexpected part %q", h.Name)
		}
		if h.Name == "untracked.tar" && h.Size > untrackedMax {
			return fmt.Errorf("the untracked files are larger than %d MiB", untrackedMax>>20)
		}
		path := filepath.Join(dir, filepath.FromSlash(h.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, tr)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	// What the tar does not read of its padding.
	io.Copy(io.Discard, cr)
	if cr.n != size {
		return errors.New("the package ended early")
	}
	return nil
}

// idleReader reads r, of conn, failing once conn sent nothing for a
// minute: a peer gone without closing it, over a network that dropped.
type idleReader struct {
	conn net.Conn
	r    io.Reader
}

func (i idleReader) Read(p []byte) (int, error) {
	i.conn.SetReadDeadline(time.Now().Add(time.Minute))
	return i.r.Read(p)
}

// countReader counts what it reads.
type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// packageName tells whether name is a part of a package of conversation
// id.
func packageName(name, id string) bool {
	switch name {
	case "repo.bundle", "worktree.patch", "untracked.tar", "scrollback.txt":
		return true
	}
	if id == "" {
		return false
	}
	if name == "claude/"+id+".jsonl" {
		return true
	}
	if b, ok := strings.CutPrefix(name, "claude/file-history/"+id+"/"); ok {
		return backupName(b)
	}
	rest, ok := strings.CutPrefix(name, "claude/"+id+"/")
	return ok && localName(rest)
}

// fetchHead gets commit head into clone: from the bundle, a file at path
// if any, and from the remote for what the bundle needs, or for head when
// there is no bundle.
func fetchHead(clone, remote, head, bundle string) error {
	has := func() bool {
		_, err := gitOut(clone, "cat-file", "-e", head+"^{commit}")
		return err == nil
	}
	if has() {
		return nil
	}
	if exists(bundle) {
		if _, err := gitOut(clone, "bundle", "verify", "-q", bundle); err != nil {
			if _, err := gitOut(clone, "fetch", "-q", remote); err != nil {
				return err
			}
		}
		if _, err := gitOut(clone, "fetch", "-q", bundle, "HEAD"); err != nil {
			return err
		}
	} else if _, err := gitOut(clone, "fetch", "-q", remote); err != nil {
		return err
	}
	if !has() {
		return fmt.Errorf("commit %s is neither on the remote nor in the package", head[:12])
	}
	return nil
}

// excludeTeleports keeps the teleports' work trees, under .gorex, out of
// the clone's own status.
func excludeTeleports(clone string) {
	dir, err := gitOut(clone, "rev-parse", "--git-common-dir")
	if err != nil {
		return
	}
	if dir = filepath.FromSlash(dir); !filepath.IsAbs(dir) {
		dir = filepath.Join(clone, dir)
	}
	path := filepath.Join(dir, "info", "exclude")
	b, _ := os.ReadFile(path)
	if bytes.Contains(b, []byte("/.gorex/")) {
		return
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if len(b) > 0 && b[len(b)-1] != '\n' {
		f.WriteString("\n")
	}
	f.WriteString("/.gorex/\n")
}

// unpackUntracked writes the files of the tar at path, if any, into the
// work tree at tree: each a new regular file under it, reached through no
// link.
func unpackUntracked(path, tree string) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || !localName(h.Name) {
			return fmt.Errorf("a file outside the work tree, %q", h.Name)
		}
		dst := filepath.Join(tree, filepath.FromSlash(h.Name))
		if err := noLinkUnder(tree, filepath.Dir(dst)); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if h.Mode&0o111 != 0 {
			mode = 0o755
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, tr)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
}

// noLinkUnder tells, by an error, that a directory between root and dir,
// dir included, is a link: what is written through it lands elsewhere.
func noLinkUnder(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return err
	}
	p := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		p = filepath.Join(p, part)
		st, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !st.IsDir() {
			return fmt.Errorf("%q is a link or a file", filepath.ToSlash(rel))
		}
	}
	return nil
}

// placeConversation writes the conversation of m, unpacked under from,
// into Claude Code's config dir config, in the project of dir, its cwd
// moved from the source's work tree to tree. A copy this machine has of
// it, as one a teleport back left here, gives way when the incoming one
// has all its messages, the same or ahead: it moves to a backup, which
// undo puts back and done removes; one with messages the incoming lacks
// is refused (errDiverged), nothing changed. It returns how to undo what
// it did, and how to keep it.
func placeConversation(from, config string, m *TeleportManifest, tree, dir string) (undo []func(), done func(), err error) {
	done = func() {}
	file := filepath.Join(from, m.Session+".jsonl")
	old, _ := filepath.Glob(filepath.Join(config, "projects", "*", m.Session+".jsonl"))
	if len(old) > 0 {
		incoming, err := messageIDs(file)
		if err != nil {
			return nil, done, err
		}
		for _, path := range old {
			here, err := messageIDs(path)
			if err != nil {
				return nil, done, err
			}
			for id := range here {
				if !incoming[id] {
					return nil, done, errDiverged
				}
			}
		}
		backup, err := os.MkdirTemp(config, ".teleport-backup-")
		if err != nil {
			return nil, done, err
		}
		undo = append(undo, func() { os.RemoveAll(backup) })
		done = func() { os.RemoveAll(backup) }
		for i, path := range old {
			// The file, and its directory when it has one.
			for _, src := range []string{path, strings.TrimSuffix(path, ".jsonl")} {
				if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
					continue
				}
				dst := filepath.Join(backup, fmt.Sprint(i), filepath.Base(src))
				if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
					return undo, done, err
				}
				if err := os.Rename(src, dst); err != nil {
					return undo, done, err
				}
				undo = append(undo, func() { os.Rename(dst, src) })
			}
		}
	}
	added, err := placeHistory(filepath.Join(from, "file-history", m.Session), filepath.Join(config, "file-history", m.Session))
	undo = append(undo, added...)
	if err != nil {
		return undo, done, err
	}
	project := filepath.Join(config, "projects", claudeSlug(dir))
	if err := os.MkdirAll(project, 0o700); err != nil {
		return undo, done, err
	}
	write := func(src, dst string) error {
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if strings.HasSuffix(dst, ".jsonl") {
			lines := bytes.SplitAfter(b, []byte("\n"))
			for i, line := range lines {
				body := bytes.TrimRight(line, "\r\n")
				lines[i] = append(rewriteSnapshot(rewriteCwd(body, m.Root, tree), m.Root, tree), line[len(body):]...)
			}
			b = bytes.Join(lines, nil)
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		undo = append(undo, func() { os.Remove(dst) })
		_, err = f.Write(b)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	}
	if err := write(file, filepath.Join(project, m.Session+".jsonl")); err != nil {
		return undo, done, err
	}
	// The directory the old copies had went with them; this one is new.
	sub := filepath.Join(from, m.Session)
	undo = append(undo, func() { os.RemoveAll(filepath.Join(project, m.Session)) })
	err = filepath.WalkDir(sub, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == sub && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		return write(path, filepath.Join(project, rel))
	})
	return undo, done, err
}

// placeHistory copies the file-history backups at src into dst, Claude
// Code's of the conversation: those dst lacks; one it has must be the
// same, else the history went on here too, and nothing is copied. It
// returns how to remove what it added.
func placeHistory(src, dst string) (undo []func(), err error) {
	entries, err := os.ReadDir(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var fresh []string
	for _, e := range entries {
		here, err := os.ReadFile(filepath.Join(dst, e.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			fresh = append(fresh, e.Name())
			continue
		}
		if err != nil {
			return nil, err
		}
		if there, err := os.ReadFile(filepath.Join(src, e.Name())); err != nil || !bytes.Equal(here, there) {
			return nil, errors.New("the file history went on here too")
		}
	}
	if len(fresh) == 0 {
		return nil, nil
	}
	if !exists(dst) {
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return nil, err
		}
		undo = append(undo, func() { os.Remove(dst) })
	}
	for _, name := range fresh {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			return undo, err
		}
		path := filepath.Join(dst, name)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return undo, err
		}
		undo = append(undo, func() { os.Remove(path) })
		_, err = f.Write(b)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return undo, err
		}
	}
	return undo, nil
}

// connGone tells whether the client closed conn, as a cancel does: it
// sends nothing after the package.
func connGone(conn net.Conn, r *bufio.Reader) bool {
	conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	defer conn.SetReadDeadline(time.Time{})
	_, err := r.Peek(1)
	var ne net.Error
	return err != nil && !(errors.As(err, &ne) && ne.Timeout())
}

// gitOut runs git in dir, and returns what it printed, trimmed; its error
// says the first line Git printed of it.
func gitOut(dir string, args ...string) (string, error) {
	var out bytes.Buffer
	err := runGit(dir, &out, args...)
	return strings.TrimSpace(out.String()), err
}

// gitTo runs git in dir, writing what it prints to a file at path.
func gitTo(dir, path string, args ...string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return runGit(dir, f, args...)
}

func runGit(dir string, out io.Writer, args ...string) error {
	return runGitEnv(dir, nil, out, args...)
}

// gitEnvOut is gitOut with env added to git's environment.
func gitEnvOut(dir string, env []string, args ...string) (string, error) {
	var out bytes.Buffer
	err := runGitEnv(dir, env, &out, args...)
	return strings.TrimSpace(out.String()), err
}

// gitIn is gitOut with stdin given git.
func gitIn(dir, stdin string, args ...string) (string, error) {
	var out bytes.Buffer
	err := runGitWith(context.Background(), dir, nil, strings.NewReader(stdin), &out, args...)
	return strings.TrimSpace(out.String()), err
}

// remoteHave is what the remotes of the repo at root have that it has
// too, as rev-list reads it on stdin: a ^commit a line, of the heads and
// tags the remotes answer now. Its remote-tracking refs, which may be
// stale, a branch deleted there after a squash merge, or a remote's only
// of a fork, stand for them when no remote answers, as offline.
func remoteHave(root string) string {
	_, common, ok := gitTop(root)
	if !ok {
		return "--not\n--remotes\n"
	}
	names, _ := configRemotes(filepath.Join(common, "config"))
	var heads []string
	answered := false
	for _, name := range slices.Compact(slices.Sorted(slices.Values(names))) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		var out bytes.Buffer
		// No prompt: the server has no terminal, and git none to ask.
		err := runGitWith(ctx, root, nil, nil, &out, "ls-remote", "--heads", "--tags", "--", name)
		cancel()
		if err != nil {
			continue
		}
		answered = true
		for _, line := range strings.Split(out.String(), "\n") {
			if h, _, ok := strings.Cut(line, "\t"); ok && hexHead(h) {
				heads = append(heads, h)
			}
		}
	}
	if !answered {
		return "--not\n--remotes\n"
	}
	// Those this repo has: a commit it lacks cannot be named.
	var b strings.Builder
	if len(heads) > 0 {
		out, _ := gitIn(root, strings.Join(heads, "\n")+"\n", "cat-file", "--batch-check=%(objectname) %(objecttype)")
		for _, line := range strings.Split(out, "\n") {
			if h, typ, _ := strings.Cut(line, " "); typ == "commit" || typ == "tag" {
				b.WriteString("^" + h + "\n")
			}
		}
	}
	return b.String()
}

func runGitEnv(dir string, env []string, out io.Writer, args ...string) error {
	return runGitWith(context.Background(), dir, env, nil, out, args...)
}

func runGitWith(ctx context.Context, dir string, env []string, stdin io.Reader, out io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdin = stdin
	cmd.Dir = dir
	// No prompt that no one would answer, nor a pager.
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "LC_ALL=C"), env...)
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = out, &stderr
	hideConsole(cmd)
	if err := cmd.Run(); err != nil {
		msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git %s: %s", args[0], msg)
	}
	return nil
}

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
