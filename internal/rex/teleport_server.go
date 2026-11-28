//go:build darwin || linux || windows

package rex

import (
	"archive/tar"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// teleport serves a teleport or teleport.pack connection of peer, whose
// first line is first: as a file transfer's (transfer), one of a remote
// peer that takes or gives nothing for writeTimeout is dropped.
func (s *Server) teleport(conn net.Conn, r *bufio.Reader, first []byte, op, peer string) {
	defer conn.Close()
	var req Request
	if err := json.Unmarshal(first, &req); err != nil {
		writeAnswer(conn, Response{Error: err.Error()})
		return
	}
	touch := func() {}
	if peer != "" {
		touch = func() { conn.SetDeadline(time.Now().Add(writeTimeout.get())) }
	}
	touch()
	if op == "teleport.pack" {
		s.teleportPack(conn, req.SID)
		return
	}
	s.teleportIn(conn, r, req.Teleport, peer, touch)
}

// teleportPack packs session sid for a teleport, on a connection of this
// machine: {"ok":true}, a line of the manifest, then the package, Size
// bytes; or the error.
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
	if _, err := conn.Write(append([]byte(`{"ok":true}`+"\n"), append(data, '\n')...)); err != nil {
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
		m.Program, m.Session = strings.ToLower(in.Program), in.AgentSession
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
	if m.Program != "shell" {
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

// packConversation adds the conversation of in's agent under its part of
// the package: Claude Code's (claude/), and the directory of its
// subagents and tool results, or omp's (packOmp).
func (s *Server) packConversation(add func(name, path string) error, in SessionInfo, root string) error {
	if strings.EqualFold(in.Program, "omp") {
		return s.packOmp(add, in)
	}
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
func (s *Server) teleportIn(conn net.Conn, r *bufio.Reader, m *TeleportManifest, peer string, touch func()) {
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
		touch()
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
	agent := teleportAgents[m.Program]
	if agent != nil {
		var err error
		if config, err = agent.dataDir(s); err != nil {
			fail(err)
			return
		}
		// A shell GoRex cannot type the resume into would show a shell
		// alone: refused before anything is made.
		if _, _, shell, err := shellCommand(nil, s.spawner().shell()); err != nil {
			fail(err)
			return
		} else if _, ok := agent.resume(shell, m.Session); !ok {
			fail(fmt.Errorf("GoRex cannot type %s's resume into %s, the shell here", m.Program, shell))
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
	err = receivePackage(io.LimitReader(touching{r, touch}, m.Size), m.Size, tmp, m.Program, m.Session)
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
	if agent != nil {
		home, _ := s.homeDir()
		placed, done, err := placeConversation(filepath.Join(tmp, m.Program), config, m, tree, dir, home)
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
	if agent != nil {
		// The shell takes it: teleportIn checked it would before.
		cmd, _ := agent.resume(ss.shell, m.Session)
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
	touch()
	if connGone(conn, r) || writeAnswer(conn, Response{Data: data}) != nil {
		s.ops.RLock()
		s.end(ss)
		s.ops.RUnlock()
		return
	}
	undo = nil
	keep()
	keepTree()
	if peer != "" {
		s.place(ss)
	}
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
	case m.Program != "shell" && teleportAgents[m.Program] == nil:
		return errors.New("an unknown program")
	case m.Program != "shell" && !IsUUID(m.Session), m.Program == "shell" && m.Session != "":
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

// packOmp adds omp's conversation of in under omp/: the file, the
// directory of the same name beside it (its artifacts), and the blobs it
// quotes, from omp's data dir.
func (s *Server) packOmp(add func(name, path string) error, in SessionInfo) error {
	file := in.agentFile
	if file == "" {
		found, _ := filepath.Glob(filepath.Join(in.AgentConfigDir, "sessions", "*", "*_"+in.AgentSession+".jsonl"))
		if len(found) == 0 {
			return errors.New("omp's conversation file is not found")
		}
		file = found[0]
	}
	name := filepath.Base(file)
	if ompID(name) != in.AgentSession {
		return errors.New("omp's conversation file is not its session's")
	}
	if err := add("omp/"+name, file); err != nil {
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
		return add("omp/"+strings.TrimSuffix(name, ".jsonl")+"/"+filepath.ToSlash(rel), path)
	})
	if err != nil {
		return err
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	blobs := filepath.Join(in.AgentConfigDir, "blobs")
	seen := map[string]bool{}
	for _, m := range blobRef.FindAllSubmatch(b, -1) {
		h := string(m[1])
		if seen[h] {
			continue
		}
		seen[h] = true
		found, _ := filepath.Glob(filepath.Join(blobs, h+"*"))
		for _, f := range found {
			if st, err := os.Lstat(f); err == nil && st.Mode().IsRegular() && blobName(filepath.Base(f)) {
				if err := add("omp/blobs/"+filepath.Base(f), f); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// blobRef is a reference to one of omp's blobs in its conversation.
var blobRef = regexp.MustCompile(`blob:sha256:([0-9a-f]{64})`)

// teleportAgent is what a teleport knows of a coding agent's
// conversations, by its program, which names its part of the package.
type teleportAgent struct {
	// dataDir is where the agent keeps them on this machine; files the
	// glob under it of conversation id's files, and project the directory
	// under it of those started in dir, in the home the server's sessions
	// run as.
	dataDir func(s *Server) (string, error)
	files   func(id string) string
	project func(dir, home string) string
	// idField names a message of a conversation's lines, and turns are
	// the types of the lines that are the conversation: what the model
	// reads, not what the agent notes of itself, as omp's session_exit.
	idField string
	turns   []string
	// shared are the directories, of the package's part and of the data
	// dir, whose files merge (placeHistory): Claude Code's file history,
	// omp's blobs.
	shared func(id string) [2]string
	// resume is the command that resumes conversation id in a shell.
	resume func(shell, id string) (string, bool)
}

// teleportAgents are the agents of agentPrograms.
var teleportAgents = map[string]*teleportAgent{
	"claude": {
		dataDir: (*Server).claudeConfigDir,
		files:   func(id string) string { return filepath.Join("projects", "*", id+".jsonl") },
		project: func(dir, _ string) string { return filepath.Join("projects", claudeSlug(dir)) },
		idField: "uuid",
		turns:   []string{"user", "assistant"},
		shared:  func(id string) [2]string { return [2]string{"file-history/" + id, "file-history/" + id} },
		resume:  func(shell, id string) (string, bool) { return ClaudeResume(shell, id, "") },
	},
	"omp": {
		dataDir: (*Server).ompDataDir,
		files:   func(id string) string { return filepath.Join("sessions", "*", "*_"+id+".jsonl") },
		project: func(dir, home string) string {
			if home == "" {
				home, _ = os.UserHomeDir()
			}
			return filepath.Join("sessions", ompSlug(dir, home))
		},
		idField: "id",
		// The entries omp makes the model's context of (xy in its
		// branch-summarization.ts); custom, model_change and the like not.
		turns:  []string{"message", "custom_message", "branch_summary", "compaction"},
		shared: func(string) [2]string { return [2]string{"blobs", "blobs"} },
		resume: ompResume,
	},
}

// errDiverged refuses a conversation that has messages here the incoming
// one lacks: both went on.
var errDiverged = errors.New("the conversation went on here too")

// messageIDs are the field fields of a conversation's lines of the types
// turns, its messages; other lines, as summaries or what the agent noted
// as it exited, do not count.
func messageIDs(path, field string, turns []string) (map[string]bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, line := range bytes.Split(b, []byte("\n")) {
		var f map[string]json.RawMessage
		var id, typ string
		if json.Unmarshal(line, &f) == nil && json.Unmarshal(f[field], &id) == nil && id != "" &&
			json.Unmarshal(f["type"], &typ) == nil && slices.Contains(turns, typ) {
			ids[id] = true
		}
	}
	return ids, nil
}

// receivePackage reads a package of size bytes from r into dir, at the
// names it gives: those of its parts only, each a regular file, the
// conversation's under its agent's part, program, for conversation id.
func receivePackage(r io.Reader, size int64, dir, program, id string) error {
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
		if h.Typeflag != tar.TypeReg || !packageName(h.Name, program, id) {
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

// touching reads r, touching the connection's deadline as it reads, as a
// file transfer's progress does.
type touching struct {
	r     io.Reader
	touch func()
}

func (t touching) Read(p []byte) (int, error) {
	t.touch()
	return t.r.Read(p)
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
// id of agent program.
func packageName(name, program, id string) bool {
	switch name {
	case "repo.bundle", "worktree.patch", "untracked.tar", "scrollback.txt":
		return true
	}
	if id == "" {
		return false
	}
	if program == "omp" {
		rest, ok := strings.CutPrefix(name, "omp/")
		if b, isBlob := strings.CutPrefix(rest, "blobs/"); ok && isBlob {
			return blobName(b)
		}
		stem, sub, _ := strings.Cut(rest, "/")
		if sub == "" {
			return ok && ompID(rest) == id
		}
		return ok && ompID(stem+".jsonl") == id && localName(sub)
	}
	if program != "claude" {
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

// placeConversation writes the conversation of m, unpacked under from,
// into Claude Code's config dir config, in the project of dir, its cwd
// moved from the source's work tree to tree. A copy this machine has of
// it, as one a teleport back left here, gives way when the incoming one
// has all its messages, the same or ahead: it moves to a backup, which
// undo puts back and done removes; one with messages the incoming lacks
// is refused (errDiverged), nothing changed. It returns how to undo what
// it did, and how to keep it.
func placeConversation(from, config string, m *TeleportManifest, tree, dir, home string) (undo []func(), done func(), err error) {
	done = func() {}
	agent := teleportAgents[m.Program]
	if agent == nil {
		return nil, done, errors.New("an unknown program")
	}
	// The conversation's file, as the agent names it.
	found, _ := filepath.Glob(filepath.Join(from, "*.jsonl"))
	found = slices.DeleteFunc(found, func(f string) bool {
		return filepath.Base(f) != m.Session+".jsonl" && ompID(filepath.Base(f)) != m.Session
	})
	if len(found) != 1 {
		return nil, done, errors.New("no conversation in the package")
	}
	file := found[0]
	old, _ := filepath.Glob(filepath.Join(config, agent.files(m.Session)))
	if len(old) > 0 {
		incoming, err := messageIDs(file, agent.idField, agent.turns)
		if err != nil {
			return nil, done, err
		}
		for _, path := range old {
			here, err := messageIDs(path, agent.idField, agent.turns)
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
	shared := agent.shared(m.Session)
	added, err := placeHistory(filepath.Join(from, filepath.FromSlash(shared[0])), filepath.Join(config, filepath.FromSlash(shared[1])))
	undo = append(undo, added...)
	if err != nil {
		return undo, done, err
	}
	project := filepath.Join(config, agent.project(dir, home))
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
	name := filepath.Base(file)
	if err := write(file, filepath.Join(project, name)); err != nil {
		return undo, done, err
	}
	// The directory the old copies had went with them; this one is new.
	stem := strings.TrimSuffix(name, ".jsonl")
	sub := filepath.Join(from, stem)
	undo = append(undo, func() { os.RemoveAll(filepath.Join(project, stem)) })
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
