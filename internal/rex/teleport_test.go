//go:build darwin || linux || windows

package rex

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestClaudeSlug checks Claude Code's names of the project directories,
// of POSIX and Windows paths alike.
func TestClaudeSlug(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/yibo/repos/gorex":            "-Users-yibo-repos-gorex",
		"/tmp/claude-501/x_y.z":              "-tmp-claude-501-x-y-z",
		`C:\Users\yibo\repos\gorex`:          "C--Users-yibo-repos-gorex",
		`D:\work\.gorex\teleport\0a1b2c3d`:   "D--work--gorex-teleport-0a1b2c3d",
		"/home/u/projets/café":               "-home-u-projets-caf-",
		"/home/u/\U0001F600":                 "-home-u---",
		"/Users/yibo/repos/gorex/.claude/wt": "-Users-yibo-repos-gorex--claude-wt",
	} {
		if got := claudeSlug(in); got != want {
			t.Errorf("claudeSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestMovePath checks that a path of the source moves from under its work
// tree to the target's, whichever OS wrote it, and one outside stays.
func TestMovePath(t *testing.T) {
	to := filepath.Join(string(filepath.Separator)+"t", "wt")
	for _, c := range []struct {
		p, root, want string
		ok            bool
	}{
		{"/src/repo", "/src/repo", to, true},
		{"/src/repo/a/b", "/src/repo", filepath.Join(to, "a", "b"), true},
		{"/src/repo/", "/src/repo/", to, true},
		{"/src/repository", "/src/repo", "", false},
		{"/elsewhere", "/src/repo", "", false},
		{"/src/repo/../x", "/src/repo", "", false},
		{`C:\Users\me\repo`, `C:\Users\me\repo`, to, true},
		{`c:\users\ME\repo\sub\dir`, `C:\Users\me\repo`, filepath.Join(to, "sub", "dir"), true},
		{`C:\Users\me\repo2`, `C:\Users\me\repo`, "", false},
	} {
		got, ok := movePath(c.p, c.root, to)
		if ok != c.ok || got != c.want {
			t.Errorf("movePath(%q, %q) = %q, %v; want %q, %v", c.p, c.root, got, ok, c.want, c.ok)
		}
	}
}

// TestRewriteCwd checks that a conversation's lines get the target's cwd,
// the rest of each line as it was.
func TestRewriteCwd(t *testing.T) {
	to := filepath.Join(string(filepath.Separator)+"home", "u", "repos", "x", ".gorex", "teleport", "ab")
	want := jsonString(filepath.Join(to, "sub"))
	for _, c := range []struct{ line, root string }{
		{`{"type":"user","cwd":"/Users/me/x/sub","message":{"content":"<b>&"}}`, "/Users/me/x"},
		{`{"cwd":"C:\\Users\\me\\x\\sub","type":"user"}`, `C:\Users\me\x`},
	} {
		got := string(rewriteCwd([]byte(c.line), c.root, to))
		if !strings.Contains(got, `"cwd":`+want) {
			t.Errorf("rewriteCwd(%s) = %s, want cwd %s", c.line, got, want)
		}
		if strings.Contains(c.line, "<b>&") && !strings.Contains(got, `"content":"<b>&"`) {
			t.Errorf("the rest of the line changed: %s", got)
		}
	}
	for _, line := range []string{`{"type":"summary"}`, `{"cwd":"/elsewhere"}`, `not json`} {
		if got := string(rewriteCwd([]byte(line), "/Users/me/x", to)); got != line {
			t.Errorf("rewriteCwd(%s) = %s, want it as it was", line, got)
		}
	}
}

// TestRepoKey checks that the spellings of one remote match, and others
// do not.
func TestRepoKey(t *testing.T) {
	same := []string{
		"https://github.com/si-mods/gorex.git", "https://github.com/si-mods/gorex", "git@github.com:si-mods/gorex.git",
		"ssh://git@github.com/si-mods/gorex.git", "https://token@GitHub.com/si-mods/gorex/",
	}
	for _, u := range same {
		if repoKey(u) != repoKey(same[0]) {
			t.Errorf("repoKey(%q) = %q, want %q", u, repoKey(u), repoKey(same[0]))
		}
	}
	if repoKey("git@github.com:si-mods/mygo.git") == repoKey(same[0]) {
		t.Error("another repo matched")
	}
	if got := repoName("git@github.com:si-mods/gorex.git"); got != "gorex" {
		t.Errorf("repoName = %q", got)
	}
	if got := repoName("https://h/.."); got != "repo" {
		t.Errorf("repoName of .. = %q", got)
	}
}

// TestPackageNames checks the parts a target takes, and the paths it
// refuses: up, absolute, a drive, .git.
func TestPackageNames(t *testing.T) {
	const id = "5d454f1b-0c2e-4b8a-9f00-0123456789ab"
	for name, want := range map[string]bool{
		"repo.bundle": true, "scrollback.txt": true, "claude/" + id + ".jsonl": true,
		"claude/" + id + "/subagents/a.jsonl": true, "claude/" + id + "/../../x": false,
		"claude/other.jsonl": false, "../repo.bundle": false, "/etc/passwd": false, "notes.txt": false,
	} {
		if got := packageName(name, "claude", id); got != want {
			t.Errorf("packageName(%q) = %v, want %v", name, got, want)
		}
	}
	for name, want := range map[string]bool{
		"a/b.txt": true, ".env": true, "../x": false, "a/../../x": false, "/abs": false,
		".git/hooks/pre-commit": false, "a/.GIT/config": false, "": false,
	} {
		if got := localName(name); got != want {
			t.Errorf("localName(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestUnpackUntrackedRefuses checks that a tar of untracked files cannot
// write outside the work tree: up, absolute, nor through a link.
func TestUnpackUntrackedRefuses(t *testing.T) {
	outside := t.TempDir()
	for _, name := range []string{"../escape", "/abs/escape", "link/escape", ".git/config"} {
		tree := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(tree, "link")); err != nil {
			t.Skip(err)
		}
		path := filepath.Join(t.TempDir(), "untracked.tar")
		writeTar(t, path, map[string]string{name: "x"})
		if err := unpackUntracked(path, tree); err == nil {
			t.Errorf("unpacked %q", name)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) > 0 {
		t.Errorf("wrote outside: %v", entries)
	}
	// A link in the tar itself is no file.
	tree, path := t.TempDir(), filepath.Join(t.TempDir(), "untracked.tar")
	f, _ := os.Create(path)
	tw := tar.NewWriter(f)
	tw.WriteHeader(&tar.Header{Name: "l", Typeflag: tar.TypeSymlink, Linkname: outside})
	tw.Close()
	f.Close()
	if err := unpackUntracked(path, tree); err == nil {
		t.Error("unpacked a link")
	}
}

// TestReceivePackageRefuses checks that a package of a part the target
// does not know, or shorter than it said, is refused.
func TestReceivePackageRefuses(t *testing.T) {
	const id = "5d454f1b-0c2e-4b8a-9f00-0123456789ab"
	for _, parts := range []map[string]string{{"../x": "x"}, {"claude/" + id + "/../../x": "x"}, {".bashrc": "x"}} {
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		for name, body := range parts {
			tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg})
			io.WriteString(tw, body)
		}
		tw.Close()
		if err := receivePackage(bytes.NewReader(b.Bytes()), int64(b.Len()), t.TempDir(), "claude", id); err == nil {
			t.Errorf("received %v", parts)
		}
	}
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	tw.WriteHeader(&tar.Header{Name: "scrollback.txt", Mode: 0o600, Size: 2, Typeflag: tar.TypeReg})
	io.WriteString(tw, "hi")
	tw.Close()
	if err := receivePackage(bytes.NewReader(b.Bytes()), int64(b.Len())+512, t.TempDir(), "claude", id); err == nil {
		t.Error("received a package shorter than said")
	}
}

// TestTeleportRefusal checks what may teleport: a shell at its prompt, and
// Claude Code waiting for the user; not another program, nor Claude Code
// at work.
func TestTeleportRefusal(t *testing.T) {
	const id = "5d454f1b-0c2e-4b8a-9f00-0123456789ab"
	quiet := time.Now().Add(-time.Minute)
	for _, c := range []struct {
		in   SessionInfo
		want string
	}{
		{SessionInfo{Idle: true, LastOutput: time.Now()}, ""},
		{SessionInfo{Program: "claude", AgentSession: id, LastOutput: quiet}, ""},
		{SessionInfo{Program: "vim", LastOutput: quiet}, TeleportProgram},
		{SessionInfo{Program: "claude", LastOutput: quiet}, TeleportUnknown},
		{SessionInfo{Program: "claude", AgentSession: id, LastOutput: time.Now()}, TeleportWorking},
		{SessionInfo{Program: "claude", AgentSession: id, LastOutput: quiet, Status: &ProgramStatus{State: "working"}}, TeleportWorking},
		{SessionInfo{Idle: true, Exited: true}, TeleportEnded},
		{SessionInfo{Program: "omp", AgentSession: id, LastOutput: quiet}, ""},
		{SessionInfo{Program: "omp", AgentSession: id, LastOutput: time.Now()}, TeleportWorking},
		{SessionInfo{Program: "omp", LastOutput: quiet}, TeleportUnknown},
		// A session's own program, started as gorex new -- ping, though it
		// is its foreground; a shell started so is a shell.
		{SessionInfo{Command: true, Shell: "ping", Program: "ping", Idle: true}, TeleportProgram},
		{SessionInfo{Command: true, Shell: "omp", Program: "omp", Idle: true}, TeleportProgram},
		{SessionInfo{Command: true, Shell: "sh", Program: "sh", Idle: true}, ""},
		{SessionInfo{Command: true, Shell: "pwsh", Program: "pwsh", Idle: true}, ""},
	} {
		if got := TeleportRefusal(c.in); got != c.want {
			t.Errorf("TeleportRefusal(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// gitEnv gives git an identity and no configuration of the user's.
func gitEnv(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	os.WriteFile(cfg, []byte("[user]\n\tname = T\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n[commit]\n\tgpgsign = false\n"), 0o600)
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitOut(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// teleportRepo makes a bare remote and a clone of it, work, whose sub
// directory has every kind of change: a commit the remote lacks, a change
// staged and one not, a file untracked and one ignored.
func teleportRepo(t *testing.T) (bare, work string) {
	t.Helper()
	gitEnv(t)
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r // as git and the process's directory say it
	}
	bare, work = filepath.Join(base, "remote.git"), filepath.Join(base, "src", "proj")
	gitRun(t, base, "init", "-q", "--bare", bare)
	gitRun(t, base, "clone", "-q", bare, work)
	writeFile(t, filepath.Join(work, ".gitignore"), "*.log\n")
	writeFile(t, filepath.Join(work, "sub", "pushed.txt"), "pushed\n")
	writeFile(t, filepath.Join(work, "sub", "staged.txt"), "one\n")
	gitRun(t, work, "add", ".")
	gitRun(t, work, "commit", "-q", "-m", "pushed")
	gitRun(t, work, "push", "-q", "origin", "HEAD")
	writeFile(t, filepath.Join(work, "sub", "local.txt"), "local\n")
	gitRun(t, work, "add", ".")
	gitRun(t, work, "commit", "-q", "-m", "not pushed")
	writeFile(t, filepath.Join(work, "sub", "staged.txt"), "two\n")
	gitRun(t, work, "add", "sub/staged.txt")
	writeFile(t, filepath.Join(work, "sub", "pushed.txt"), "changed\n")
	writeFile(t, filepath.Join(work, "sub", "new", "untracked.txt"), "untracked\n")
	writeFile(t, filepath.Join(work, "sub", "build.log"), "ignored\n")
	return bare, work
}

func writeTar(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		io.WriteString(tw, body)
	}
	tw.Close()
}

// readTar returns the files of a tar, by name.
func readTar(t *testing.T, r io.Reader) map[string]string {
	t.Helper()
	out := map[string]string{}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		out[h.Name] = string(b)
	}
}

// shellIn starts a shell session of s in dir, as a pane of this machine.
func shellIn(t *testing.T, s *Server, dir string) (*Client, string) {
	t.Helper()
	c := pipe(s, "")
	t.Cleanup(func() {
		for _, in := range must(c.List()) {
			c.Kill(in.ID)
		}
		c.Close()
	})
	cmd, _ := testShell()
	in, err := c.Create(CreateOptions{Command: cmd, Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	waitInfo(t, c, in.ID, "the shell at its prompt", func(in SessionInfo) bool { return in.Idle && in.Dir != "" })
	return c, in.ID
}

// waitInfo waits for session sid of c to be as ok says.
func waitInfo(t *testing.T, c *Client, sid, what string, ok func(SessionInfo) bool) SessionInfo {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, in := range must(c.List()) {
			if in.ID == sid && ok(in) {
				return in
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s: %+v", what, must(c.List()))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestTeleportPack checks what a package holds: the commits no remote
// has, the changes staged and not, the untracked files but the ignored
// ones, and the scrollback.
func TestTeleportPack(t *testing.T) {
	_, work := teleportRepo(t)
	s, _ := testServer(t)
	c, sid := shellIn(t, s, filepath.Join(work, "sub"))
	// The marker shows only as printed, not as typed.
	echo := "echo tele''port-marker\n"
	if runtime.GOOS == "windows" {
		echo = "echo tele^port-marker\r"
	}
	c.Input(sid, []byte(echo))
	waitText(t, c, sid, "teleport-marker")
	m, pkg, err := s.pack(sid)
	if pkg != nil {
		defer os.Remove(pkg.Name())
		defer pkg.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	if m.Program != "shell" || m.Dir != "sub" || m.Branch != "main" || len(m.Remotes) != 1 || !hexHead(m.Head) || !samePath(m.Root, work) {
		t.Errorf("manifest %+v", m)
	}
	pkg.Seek(0, io.SeekStart)
	parts := readTar(t, pkg)
	if parts["repo.bundle"] == "" {
		t.Error("no bundle of the commit not pushed")
	}
	patch := parts["worktree.patch"]
	if !strings.Contains(patch, "+two") || !strings.Contains(patch, "+changed") {
		t.Errorf("patch %q", patch)
	}
	untracked := readTar(t, strings.NewReader(parts["untracked.tar"]))
	if untracked["sub/new/untracked.txt"] != "untracked\n" || len(untracked) != 1 {
		t.Errorf("untracked %v", untracked)
	}
	if !strings.Contains(parts["scrollback.txt"], "teleport-marker") {
		t.Errorf("scrollback %q", parts["scrollback.txt"])
	}

	// Pushed, nothing to bundle.
	gitRun(t, work, "push", "-q", "origin", "HEAD")
	_, pkg2, err := s.pack(sid)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(pkg2.Name())
	defer pkg2.Close()
	pkg2.Seek(0, io.SeekStart)
	if _, ok := readTar(t, pkg2)["repo.bundle"]; ok {
		t.Error("a bundle of a HEAD the remote has")
	}
}

// TestTeleportPackRefuses checks the panes that do not pack: out of a Git
// work tree, in a repo with no remote, with too many untracked bytes.
func TestTeleportPackRefuses(t *testing.T) {
	gitEnv(t)
	s, _ := testServer(t)
	refused := func(dir, want string) {
		t.Helper()
		_, sid := shellIn(t, s, dir)
		_, pkg, err := s.pack(sid)
		if pkg != nil {
			os.Remove(pkg.Name())
			pkg.Close()
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("packing in %s: %v, want %q", dir, err, want)
		}
	}
	refused(t.TempDir(), "not a Git work tree")
	// A session that runs a program, not a shell, in a work tree.
	_, work0 := teleportRepo(t)
	c := pipe(s, "")
	defer c.Close()
	prog := []string{"sleep", "30"}
	if runtime.GOOS == "windows" {
		prog = []string{"ping", "-n", "30", "127.0.0.1"}
	}
	in, err := c.Create(CreateOptions{Command: prog, Dir: work0, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Kill(in.ID)
	waitInfo(t, c, in.ID, "the program", func(in SessionInfo) bool { return in.Command && in.Dir != "" })
	if _, pkg, err := s.pack(in.ID); err == nil || !strings.Contains(err.Error(), prog[0]+" is running") {
		if pkg != nil {
			os.Remove(pkg.Name())
		}
		t.Errorf("packing a program's session: %v", err)
	}
	lone := t.TempDir()
	gitRun(t, lone, "init", "-q")
	writeFile(t, filepath.Join(lone, "a"), "a")
	gitRun(t, lone, "add", ".")
	gitRun(t, lone, "commit", "-q", "-m", "a")
	refused(lone, "no remote")
	_, work := teleportRepo(t)
	f, err := os.Create(filepath.Join(work, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(untrackedMax + 1) // sparse
	f.Close()
	refused(work, "larger than 64 MiB")
}

// waitText waits for session sid of c to show text.
func waitText(t *testing.T, c *Client, sid, text string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		f, err := c.Format(sid, "text")
		if err == nil && strings.Contains(string(f.Content), text) {
			return string(f.Content)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q in %q (%v)", text, f.Content, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestTeleport teleports a shell pane and a Claude Code pane, a stand-in
// that waits, from one server to another that connects as a remote
// client: the target clones the repo, makes a work tree of the source's
// state, places the conversation and resumes it, and refuses it a second
// time.
func TestTeleport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in for Claude Code is a POSIX program")
	}
	_, work := teleportRepo(t)
	head := gitRun(t, work, "rev-parse", "HEAD")
	// The target's home, where it clones, and its Claude Code's config.
	dstHome := resolved(t)
	dstConfig := filepath.Join(dstHome, ".claude")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("SHELL", "/bin/sh")
	stubClaude(t)
	src, _ := testServer(t)
	dst, _ := testServer(t)
	src.home, dst.home = resolved(t), dstHome
	remote := pipe(dst, testPeer)
	t.Cleanup(func() {
		for _, in := range must(remote.List()) {
			remote.Kill(in.ID)
		}
		remote.Close()
	})

	// A shell.
	local, sid := shellIn(t, src, filepath.Join(work, "sub"))
	local.Input(sid, []byte("echo before-tele''port\n"))
	waitText(t, local, sid, "before-teleport")
	var steps []string
	in, err := Teleport(context.Background(), local, sid, remote, func(s, _ string) { steps = append(steps, s) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(steps, ",") != "pack,send,start" {
		t.Errorf("steps %v", steps)
	}
	clone := filepath.Join(dstHome, "remote")
	trees, _ := filepath.Glob(filepath.Join(clone, ".gorex", "teleport", "*"))
	if len(trees) != 1 {
		t.Fatalf("work trees %v", trees)
	}
	tree := trees[0]
	if got := gitRun(t, tree, "rev-parse", "HEAD"); got != head {
		t.Errorf("work tree at %s, want %s", got, head)
	}
	if b := gitRun(t, tree, "symbolic-ref", "--short", "HEAD"); b != "teleport/"+filepath.Base(tree) {
		t.Errorf("branch %q", b)
	}
	for name, want := range map[string]string{
		"sub/staged.txt": "two\n", "sub/pushed.txt": "changed\n", "sub/local.txt": "local\n",
		"sub/new/untracked.txt": "untracked\n",
	} {
		if b, err := os.ReadFile(filepath.Join(tree, filepath.FromSlash(name))); string(b) != want {
			t.Errorf("%s: %q %v, want %q", name, b, err, want)
		}
	}
	if exists(filepath.Join(tree, "sub", "build.log")) {
		t.Error("the ignored file came")
	}
	if st := gitRun(t, clone, "status", "--porcelain"); st != "" {
		t.Errorf("the clone's own checkout changed: %q", st)
	}
	if in.Owner != testPeer {
		t.Errorf("the session is %q's", in.Owner)
	}
	waitText(t, remote, in.ID, "before-teleport")
	waitInfo(t, remote, in.ID, "the shell in the work tree", func(in SessionInfo) bool {
		return in.Idle && samePath(in.Dir, filepath.Join(tree, "sub"))
	})
	// The source is the caller's to end: it is as it was.
	waitInfo(t, local, sid, "the source session", func(in SessionInfo) bool { return !in.Exited })

	// Claude Code, which a stand-in plays: a script in a bin directory,
	// which the server names by the script's name.
	// Its config dir is the default, under a home of the test's: macOS
	// does not tell the environment of a system binary as sh.
	home := t.TempDir()
	t.Setenv("HOME", home)
	src.home = home
	srcConfig := filepath.Join(home, ".claude")
	fake := filepath.Join(t.TempDir(), "bin", "claude")
	writeFile(t, fake, "#!/bin/sh\nsleep 60\n")
	os.Chmod(fake, 0o755)
	local, sid = shellIn(t, src, filepath.Join(work, "sub"))
	local.Input(sid, []byte("'"+fake+"' 60\n"))
	cl := waitInfo(t, local, sid, "claude in the foreground", func(in SessionInfo) bool { return in.Program == "claude" })
	t.Cleanup(func() {
		if p, err := os.FindProcess(cl.Foreground); err == nil {
			p.Kill()
		}
	})
	const id = "5d454f1b-0c2e-4b8a-9f00-0123456789ab"
	writeFile(t, filepath.Join(srcConfig, "sessions", fmt.Sprint(cl.Foreground)+".json"),
		fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":%q}`, cl.Foreground, id, cl.Dir))
	project := filepath.Join(srcConfig, "projects", claudeSlug(cl.Dir))
	writeFile(t, filepath.Join(project, id+".jsonl"),
		fmt.Sprintf("{\"type\":\"user\",\"cwd\":%s}\n{\"type\":\"summary\"}\n{\"cwd\":%s}\n", jsonString(cl.Dir), jsonString(work)))
	writeFile(t, filepath.Join(project, id, "subagents", "agent-1.jsonl"), fmt.Sprintf("{\"cwd\":%s}\n", jsonString(cl.Dir)))
	waitInfo(t, local, sid, "the conversation, and Claude Code quiet", func(in SessionInfo) bool {
		return in.AgentSession == id && TeleportRefusal(in) == ""
	})
	in, err = Teleport(context.Background(), local, sid, remote, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if trees, _ = filepath.Glob(filepath.Join(clone, ".gorex", "teleport", "*")); len(trees) != 2 {
		t.Fatalf("work trees %v", trees)
	}
	dir := waitInfo(t, remote, in.ID, "the new session's directory", func(in SessionInfo) bool { return in.Dir != "" }).Dir
	if !strings.HasPrefix(dir, filepath.Join(clone, ".gorex", "teleport")) || filepath.Base(dir) != "sub" {
		t.Fatalf("the session is in %s", dir)
	}
	waitText(t, remote, in.ID, "stub-claude --resume "+id)
	conv, err := os.ReadFile(filepath.Join(dstConfig, "projects", claudeSlug(dir), id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("{\"type\":\"user\",\"cwd\":%s}\n{\"type\":\"summary\"}\n{\"cwd\":%s}\n", jsonString(dir), jsonString(filepath.Dir(dir)))
	if string(conv) != want {
		t.Errorf("conversation\n%s\nwant\n%s", conv, want)
	}
	if b, err := os.ReadFile(filepath.Join(dstConfig, "projects", claudeSlug(dir), id, "subagents", "agent-1.jsonl")); !strings.Contains(string(b), jsonString(dir)) {
		t.Errorf("subagent's %q %v", b, err)
	}

}

// TestTeleportCleansUp checks that a failure on the target leaves no work
// tree nor branch: a patch that does not apply.
func TestTeleportCleansUp(t *testing.T) {
	_, work := teleportRepo(t)
	roots := t.TempDir()
	s, _ := testServer(t)
	_, sid := shellIn(t, s, work)
	m, pkg, err := s.pack(sid)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(pkg.Name())
	defer pkg.Close()
	// The clone the target finds has the commits; the patch is another's.
	gitRun(t, work, "push", "-q", "origin", "HEAD")
	clone := filepath.Join(roots, "proj")
	gitRun(t, roots, "clone", "-q", m.Remotes[0], clone)
	pkg.Seek(0, io.SeekStart)
	parts := readTar(t, pkg)
	parts["worktree.patch"] = "diff --git a/nope b/nope\n--- a/nope\n+++ b/nope\n@@ -1 +1 @@\n-a\n+b\n"
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range parts {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg})
		io.WriteString(tw, body)
	}
	tw.Close()
	m.Size = int64(buf.Len())
	// The target knows the clone: a session of its is in it.
	dst, _ := testServer(t)
	dst.home = resolved(t)
	shellIn(t, dst, clone)
	c := pipe(dst, "")
	defer c.Close()
	conn, err := c.dial()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req, _ := json.Marshal(Request{Op: "teleport", Teleport: &m})
	conn.Write(append(req, '\n'))
	if err := readOK(conn); err != nil {
		t.Fatal(err)
	}
	go conn.Write(buf.Bytes())
	line := make([]byte, 4096)
	n, _ := io.ReadAtLeast(conn, line, 1)
	for n > 0 && line[n-1] != '\n' {
		k, err := conn.Read(line[n:])
		if err != nil {
			break
		}
		n += k
	}
	if !strings.Contains(string(line[:n]), "applying the changes") {
		t.Errorf("answer %s", line[:n])
	}
	if trees, _ := filepath.Glob(filepath.Join(clone, ".gorex", "teleport", "*")); len(trees) != 0 {
		t.Errorf("work trees left: %v", trees)
	}
	if b := gitRun(t, clone, "branch", "--list", "teleport/*"); b != "" {
		t.Errorf("branches left: %q", b)
	}
	if out := gitRun(t, clone, "worktree", "list"); strings.Count(out, "\n") != 0 {
		t.Errorf("worktrees: %q", out)
	}
}

// stubClaude puts a claude and an omp that only say their arguments
// first on PATH, which the sessions a teleport starts inherit: the resume
// they type must not start the real agent.
func stubClaude(t *testing.T) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "stub")
	for _, agent := range agentPrograms {
		writeFile(t, filepath.Join(bin, agent), "#!/bin/sh\necho stub-"+agent+" \"$@\"\n")
		os.Chmod(filepath.Join(bin, agent), 0o755)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The sessions' shell, a login one, would have /etc/profile put the
	// system's directories first, where a real agent may be: a shell
	// named sh that is none.
	shell := filepath.Join(t.TempDir(), "shell", "sh")
	writeFile(t, shell, "#!/bin/sh\nexec /bin/sh \"$@\"\n")
	os.Chmod(shell, 0o755)
	t.Setenv("SHELL", shell)
}

const convID = "5d454f1b-0c2e-4b8a-9f00-0123456789ab"

// conversation is a conversation's lines of messages ids, each with cwd,
// and a summary, which has no id.
func conversation(cwd string, ids ...string) string {
	var b strings.Builder
	for _, id := range ids {
		fmt.Fprintf(&b, "{\"type\":\"user\",\"uuid\":%q,\"cwd\":%s}\n", id, jsonString(cwd))
	}
	b.WriteString("{\"type\":\"summary\"}\n")
	return b.String()
}

// tree returns the files under dir, by slash path, with their contents.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			rel, _ := filepath.Rel(dir, path)
			b, _ := os.ReadFile(path)
			out[filepath.ToSlash(rel)] = string(b)
		}
		return nil
	})
	return out
}

// TestPlaceConversationOverOld checks a conversation that arrives where
// a copy of it is: one the incoming has every message of gives way, and
// comes back whole when what follows fails; one with a message the
// incoming lacks is refused, nothing changed.
func TestPlaceConversationOverOld(t *testing.T) {
	config, from := t.TempDir(), t.TempDir()
	m := &TeleportManifest{Program: "claude", Session: convID, Root: "/src/proj"}
	writeFile(t, filepath.Join(from, convID+".jsonl"), conversation("/src/proj/sub", "m1", "m2", "m3"))
	writeFile(t, filepath.Join(from, convID, "subagents", "a.jsonl"), conversation("/src/proj/sub", "s1"))
	tree0 := filepath.Join(string(filepath.Separator)+"t", "wt")
	dir := filepath.Join(tree0, "sub")
	oldProject := filepath.Join(config, "projects", "-old-proj-sub")
	writeFile(t, filepath.Join(oldProject, convID+".jsonl"), conversation("/old/proj/sub", "m1", "m2"))
	writeFile(t, filepath.Join(oldProject, convID, "tool-results", "r.txt"), "result")
	writeFile(t, filepath.Join(oldProject, "other.jsonl"), "other")
	before := tree(t, config)

	// Behind: it gives way, and comes back as it was on undo.
	undo, done, err := placeConversation(from, config, m, tree0, dir)
	if err != nil {
		t.Fatal(err)
	}
	after := tree(t, config)
	newFile := "projects/" + claudeSlug(dir) + "/" + convID + ".jsonl"
	if _, ok := after["projects/-old-proj-sub/"+convID+".jsonl"]; ok {
		t.Error("the old copy stayed")
	}
	if !strings.Contains(after[newFile], `"m3"`) || !strings.Contains(after[newFile], jsonString(dir)) {
		t.Errorf("the new copy %q", after[newFile])
	}
	if _, ok := after["projects/"+claudeSlug(dir)+"/"+convID+"/subagents/a.jsonl"]; !ok {
		t.Errorf("no subagent's file: %v", after)
	}
	for i := len(undo) - 1; i >= 0; i-- {
		undo[i]()
	}
	if got := tree(t, config); !maps.Equal(got, before) {
		t.Errorf("undone to %v, want %v", got, before)
	}
	if backups, _ := filepath.Glob(filepath.Join(config, ".teleport-backup-*")); len(backups) > 0 {
		t.Errorf("backups left: %v", backups)
	}

	// Kept: the old copy and the backup are gone.
	_, done, err = placeConversation(from, config, m, tree0, dir)
	if err != nil {
		t.Fatal(err)
	}
	done()
	found, _ := filepath.Glob(filepath.Join(config, "projects", "*", convID+".jsonl"))
	if len(found) != 1 || !strings.HasSuffix(filepath.ToSlash(found[0]), newFile) {
		t.Errorf("copies %v", found)
	}
	if backups, _ := filepath.Glob(filepath.Join(config, ".teleport-backup-*")); len(backups) > 0 || exists(filepath.Join(oldProject, convID)) {
		t.Errorf("backups %v, or the old directory, left", backups)
	}

	// Gone on here too: refused, and nothing changes.
	writeFile(t, filepath.Join(config, "projects", claudeSlug(dir), convID+".jsonl"), conversation(dir, "m1", "m2", "m3", "here"))
	before = tree(t, config)
	from2 := t.TempDir()
	writeFile(t, filepath.Join(from2, convID+".jsonl"), conversation("/src/proj/sub", "m1", "m2", "m3", "there"))
	if _, _, err := placeConversation(from2, config, m, tree0, filepath.Join(tree0, "other")); err != errDiverged {
		t.Errorf("a conversation gone on both sides: %v", err)
	}
	if got := tree(t, config); !maps.Equal(got, before) {
		t.Errorf("refused, but changed to %v", got)
	}
}

// claudeIn starts Claude Code's stand-in, a script in a bin directory, in
// a shell of c's server in dir, with config dir config, and the file
// Claude Code keeps there of its conversation convID.
func claudeIn(t *testing.T, c *Client, dir, config string) string {
	t.Helper()
	cmd, _ := testShell()
	in, err := c.Create(CreateOptions{Command: cmd, Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(t.TempDir(), "bin", "claude")
	writeFile(t, fake, "#!/bin/sh\nsleep 60\n")
	os.Chmod(fake, 0o755)
	// The config dir is HOME's too: macOS does not tell the environment of
	// a system binary, as sh, and the server looks there then.
	c.Input(in.ID, []byte("CLAUDE_CONFIG_DIR='"+config+"' '"+fake+"' 60\n"))
	cl := waitInfo(t, c, in.ID, "claude in the foreground", func(in SessionInfo) bool { return in.Program == "claude" })
	t.Cleanup(func() {
		c.Kill(in.ID)
		if p, err := os.FindProcess(cl.Foreground); err == nil {
			p.Kill()
		}
	})
	writeFile(t, filepath.Join(config, "sessions", fmt.Sprint(cl.Foreground)+".json"),
		fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":%q}`, cl.Foreground, convID, cl.Dir))
	return in.ID
}

// resolved is a new temporary directory as the system names it, links
// followed, as a process's directory says it.
func resolved(t *testing.T) string {
	d := t.TempDir()
	if r, err := filepath.EvalSymlinks(d); err == nil {
		return r
	}
	return d
}

// files returns the files of the work tree at dir, by slash path, with
// their contents, Git's own and the teleports' work trees left out.
func files(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".gorex") {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			b, _ := os.ReadFile(path)
			out[filepath.ToSlash(rel)] = string(b)
		}
		return nil
	})
	return out
}

// twoMachines are two in-process servers, A and B, each with its home,
// Claude Code's config dir and its clones' root, and clients of each:
// one of the machine, one of the other as a remote client.
type twoMachines struct {
	a, b                         *Server
	aLocal, aPeer, bLocal, bPeer *Client
	homeA, homeB                 string
}

func newTwoMachines(t *testing.T, work string) *twoMachines {
	t.Helper()
	m := &twoMachines{homeA: resolved(t), homeB: resolved(t)}
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	stubClaude(t)
	a, _ := testServer(t)
	b, _ := testServer(t)
	m.a, m.b = a, b
	a.home, b.home = m.homeA, m.homeB
	m.aLocal, m.aPeer, m.bLocal, m.bPeer = pipe(a, ""), pipe(a, testPeer), pipe(b, ""), pipe(b, testPeer)
	for _, c := range []*Client{m.aLocal, m.aPeer, m.bLocal, m.bPeer} {
		t.Cleanup(func() {
			for _, in := range must(c.List()) {
				c.Kill(in.ID)
			}
			c.Close()
		})
	}
	// A terminal first, which finds its library under the user's home,
	// before the homes of the machines take its place.
	shellIn(t, a, work)
	return m
}

// toB and toA make the hop's source machine the process's home: where
// the server finds Claude Code's file of its process when macOS does not
// tell the process's environment.
func (m *twoMachines) toB(t *testing.T) { t.Setenv("HOME", m.homeA) }

func (m *twoMachines) toA(t *testing.T) { t.Setenv("HOME", m.homeB) }

// TestTeleportRoundTrip teleports a Claude Code conversation from A's
// work tree P to B, which makes a work tree W of it, goes on there, and
// back: the work lands in P again, its branch fast forwarded, with B's
// changes, P's untracked files that B removed gone, the conversation in
// P's project, its copy there given way; and to B again, in W. No other
// work tree is made. A conversation gone on at both is refused.
func TestTeleportRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in for Claude Code is a POSIX program")
	}

	_, work := teleportRepo(t)
	m := newTwoMachines(t, work)
	cfgA, cfgB := filepath.Join(m.homeA, ".claude"), filepath.Join(m.homeB, ".claude")
	copies := func(config string) []string {
		found, _ := filepath.Glob(filepath.Join(config, "projects", "*", convID+".jsonl"))
		return found
	}
	trees := func(clone string) []string {
		found, _ := filepath.Glob(filepath.Join(clone, ".gorex", "teleport", "*"))
		return found
	}
	appendMessage := func(file, id, cwd string) {
		f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(f, "{\"type\":\"user\",\"uuid\":%q,\"cwd\":%s}\n", id, jsonString(cwd))
		f.Close()
	}

	// A to B.
	m.toB(t)
	dirP := filepath.Join(work, "sub")
	sidA := claudeIn(t, m.aLocal, dirP, cfgA)
	projectP := filepath.Join(cfgA, "projects", claudeSlug(dirP))
	writeFile(t, filepath.Join(projectP, convID+".jsonl"), conversation(dirP, "m1", "m2"))
	writeFile(t, filepath.Join(projectP, convID, "subagents", "a.jsonl"), conversation(dirP, "s1"))
	// Its file history: a backup of a file in the work tree, one of a file
	// outside it alone, one no snapshot names, and a name not a backup's.
	historyA, historyB := filepath.Join(cfgA, "file-history", convID), filepath.Join(cfgB, "file-history", convID)
	appendLine := func(file, line string) {
		f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(f, line)
		f.Close()
	}
	appendLine(filepath.Join(projectP, convID+".jsonl"), snapshot(map[string]string{filepath.Join(dirP, "x.go"): "aa@v1", "/etc/hosts": "bb@v1"}))
	for name, body := range map[string]string{"aa@v1": "x.go before", "bb@v1": "hosts", "cc@v2": "unnamed", "notes.txt": "no"} {
		writeFile(t, filepath.Join(historyA, name), body)
	}
	waitInfo(t, m.aLocal, sidA, "Claude Code quiet", func(in SessionInfo) bool { return in.AgentSession == convID && TeleportRefusal(in) == "" })
	in, err := Teleport(context.Background(), m.aLocal, sidA, m.bPeer, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	dirW := waitInfo(t, m.bPeer, in.ID, "B's directory", func(in SessionInfo) bool { return in.Dir != "" }).Dir
	cloneB := filepath.Join(m.homeB, "remote")
	if got := trees(cloneB); len(got) != 1 || filepath.Join(got[0], "sub") != dirW {
		t.Fatalf("B's work trees %v, the pane in %s", got, dirW)
	}
	if got := copies(cfgB); len(got) != 1 || filepath.Dir(got[0]) != filepath.Join(cfgB, "projects", claudeSlug(dirW)) {
		t.Fatalf("B's copies %v", got)
	}
	if got := tree(t, historyB); !maps.Equal(got, map[string]string{"aa@v1": "x.go before", "cc@v2": "unnamed"}) {
		t.Errorf("B's file history %v", got)
	}
	convB, _ := os.ReadFile(filepath.Join(cfgB, "projects", claudeSlug(dirW), convID+".jsonl"))
	var snapB map[string]string
	for _, line := range bytes.Split(convB, []byte("\n")) {
		if b := trackedBackups(line); b != nil {
			snapB = b
		}
	}
	if !maps.Equal(snapB, map[string]string{filepath.Join(dirW, "x.go"): "aa@v1"}) {
		t.Errorf("B's snapshot %v", snapB)
	}

	// Claude Code notes things of itself in A's copy, as it exits: no turn
	// of the conversation.
	appendLine(filepath.Join(projectP, convID+".jsonl"), `{"type":"system","subtype":"informational","uuid":"sys-exit","content":"bye"}`)

	// It goes on at B: a commit, a change, an untracked file of P's gone.
	treeW := filepath.Dir(dirW)
	writeFile(t, filepath.Join(dirW, "b.txt"), "from B\n")
	gitRun(t, treeW, "add", "sub/b.txt")
	gitRun(t, treeW, "commit", "-q", "-m", "on B")
	headB := gitRun(t, treeW, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(dirW, "pushed.txt"), "from B\n")
	os.Remove(filepath.Join(dirW, "new", "untracked.txt"))
	writeFile(t, filepath.Join(dirW, "fresh.txt"), "untracked on B\n")
	appendMessage(filepath.Join(cfgB, "projects", claudeSlug(dirW), convID+".jsonl"), "m3", dirW)
	appendLine(filepath.Join(cfgB, "projects", claudeSlug(dirW), convID+".jsonl"), snapshot(map[string]string{filepath.Join(dirW, "x.go"): "dd@v3"}))
	writeFile(t, filepath.Join(historyB, "dd@v3"), "x.go on B")

	// B to A: in P.
	m.toA(t)
	sidB := claudeIn(t, m.bLocal, dirW, cfgB)
	waitInfo(t, m.bLocal, sidB, "Claude Code quiet", func(in SessionInfo) bool { return in.AgentSession == convID && TeleportRefusal(in) == "" })
	in, err = Teleport(context.Background(), m.bLocal, sidB, m.aPeer, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitInfo(t, m.aPeer, in.ID, "A's directory", func(in SessionInfo) bool { return in.Dir != "" }).Dir; got != dirP {
		t.Errorf("A's pane is in %s, want %s", got, dirP)
	}
	if got := gitRun(t, work, "rev-parse", "main"); got != headB {
		t.Errorf("P's branch at %s, want B's %s", got, headB)
	}
	for name, want := range map[string]string{
		"sub/b.txt": "from B\n", "sub/pushed.txt": "from B\n", "sub/fresh.txt": "untracked on B\n",
		"sub/staged.txt": "two\n", "sub/build.log": "ignored\n",
	} {
		if b, err := os.ReadFile(filepath.Join(work, filepath.FromSlash(name))); string(b) != want {
			t.Errorf("P's %s: %q %v, want %q", name, b, err, want)
		}
	}
	if exists(filepath.Join(dirP, "new", "untracked.txt")) {
		t.Error("P's untracked file that B removed stayed")
	}
	if got := trees(work); len(got) != 0 {
		t.Errorf("A's work trees %v", got)
	}
	if refs := gitRun(t, work, "for-each-ref", "refs/gorex"); refs != "" {
		t.Errorf("backups left: %s", refs)
	}
	if got := copies(cfgA); len(got) != 1 || filepath.Dir(got[0]) != projectP {
		t.Fatalf("A's copies %v", got)
	}
	conv, _ := os.ReadFile(filepath.Join(projectP, convID+".jsonl"))
	if !strings.Contains(string(conv), `"m3"`) || strings.Contains(string(conv), jsonString(dirW)) {
		t.Errorf("A's conversation %s", conv)
	}
	if !exists(filepath.Join(projectP, convID, "subagents", "a.jsonl")) {
		t.Error("the subagents' directory is not in P's project")
	}
	if got := tree(t, historyA); !maps.Equal(got, map[string]string{"aa@v1": "x.go before", "bb@v1": "hosts", "cc@v2": "unnamed", "dd@v3": "x.go on B", "notes.txt": "no"}) {
		t.Errorf("A's file history %v", got)
	}
	if !strings.Contains(string(conv), jsonString(filepath.Join(dirP, "x.go"))+`:{"backupFileName":"dd@v3"`) {
		t.Errorf("A's last snapshot is not of P: %s", conv)
	}
	waitText(t, m.aPeer, in.ID, "stub-claude --resume "+convID)

	// It went on at both: refused, B as it was.
	appendMessage(filepath.Join(cfgB, "projects", claudeSlug(dirW), convID+".jsonl"), "only-on-B", dirW)
	beforeCfg, beforeW := tree(t, cfgB), files(t, treeW)
	m.toB(t)
	sidA = claudeIn(t, m.aLocal, dirP, cfgA)
	waitInfo(t, m.aLocal, sidA, "Claude Code quiet", func(in SessionInfo) bool { return in.AgentSession == convID && TeleportRefusal(in) == "" })
	if _, err := Teleport(context.Background(), m.aLocal, sidA, m.bPeer, func(string, string) {}); err == nil || !strings.Contains(err.Error(), "went on here too") {
		t.Fatalf("a conversation gone on at both: %v", err)
	}
	if !maps.Equal(tree(t, cfgB), beforeCfg) || !maps.Equal(files(t, treeW), beforeW) {
		t.Error("B changed")
	}
	if refs := gitRun(t, treeW, "for-each-ref", "refs/gorex"); refs != "" {
		t.Errorf("backups left: %s", refs)
	}

	// A to B again, B's copy as A's: in W.
	b, _ := os.ReadFile(filepath.Join(projectP, convID+".jsonl"))
	os.WriteFile(filepath.Join(cfgB, "projects", claudeSlug(dirW), convID+".jsonl"), b, 0o600)
	in, err = Teleport(context.Background(), m.aLocal, sidA, m.bPeer, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitInfo(t, m.bPeer, in.ID, "B's directory", func(in SessionInfo) bool { return in.Dir != "" }).Dir; got != dirW {
		t.Errorf("B's pane is in %s, want %s", got, dirW)
	}
	if got := trees(cloneB); len(got) != 1 {
		t.Errorf("B's work trees %v", got)
	}
	if got := copies(cfgB); len(got) != 1 {
		t.Errorf("B's copies %v", got)
	}
}

// TestTeleportBackFallbacks checks a teleport back that does not take the
// directory it came from: one that fails after moving it, which puts it
// back as it was, files, untracked ones and index alike; and one the user
// changed since, which a new work tree takes, it untouched.
func TestTeleportBackFallbacks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shells")
	}
	_, work := teleportRepo(t)
	m := newTwoMachines(t, work)
	ctx, none := context.Background(), func(string, string) {}
	m.toB(t)
	dirP := filepath.Join(work, "sub")
	local, sid := shellIn(t, m.a, dirP)
	in, err := Teleport(ctx, local, sid, m.bPeer, none)
	if err != nil {
		t.Fatal(err)
	}
	dirW := waitInfo(t, m.bPeer, in.ID, "B's directory", func(in SessionInfo) bool { return in.Dir != "" }).Dir
	treeW := filepath.Dir(dirW)
	state := func() []string {
		return []string{gitRun(t, work, "rev-parse", "HEAD"), strings.ReplaceAll(gitRun(t, work, "status", "--porcelain", "--ignored"), "!! .gorex/\n", ""),
			gitRun(t, work, "diff", "--cached"), gitRun(t, work, "for-each-ref", "refs/gorex")}
	}

	// B un-ignores *.log, and has an untracked build.log, which P has,
	// ignored: unpacking it fails once P moved.
	writeFile(t, filepath.Join(treeW, ".gitignore"), "")
	writeFile(t, filepath.Join(dirW, "build.log"), "from B\n")
	m.toA(t)
	remote, sidB := shellIn(t, m.b, dirW)
	beforeFiles, beforeState := files(t, work), state()
	if _, err := Teleport(ctx, remote, sidB, m.aPeer, none); err == nil || !strings.Contains(err.Error(), "untracked") {
		t.Fatalf("a teleport back that cannot unpack: %v", err)
	}
	if got := files(t, work); !maps.Equal(got, beforeFiles) {
		t.Errorf("P is\n%v\nwas\n%v", got, beforeFiles)
	}
	if got := state(); !slices.Equal(got, beforeState) {
		t.Errorf("P's state is\n%q\nwas\n%q", got, beforeState)
	}

	// P changed since it left: a new work tree takes the work.
	gitRun(t, treeW, "checkout", "--", ".gitignore")
	os.Remove(filepath.Join(dirW, "build.log"))
	writeFile(t, filepath.Join(dirP, "after.txt"), "A went on\n")
	beforeFiles, beforeState = files(t, work), state()
	in, err = Teleport(ctx, remote, sidB, m.aPeer, none)
	if err != nil {
		t.Fatal(err)
	}
	if dir := waitInfo(t, m.aPeer, in.ID, "A's directory", func(in SessionInfo) bool { return in.Dir != "" }).Dir; !strings.HasPrefix(dir, filepath.Join(work, ".gorex", "teleport")) {
		t.Errorf("A's pane is in %s", dir)
	}
	if got := files(t, work); !maps.Equal(got, beforeFiles) {
		t.Errorf("P is\n%v\nwas\n%v", got, beforeFiles)
	}
	if got := state(); !slices.Equal(got, beforeState) {
		t.Errorf("P's state is\n%q\nwas\n%q", got, beforeState)
	}
}

// TestFindClone checks where a target finds a clone of the repo: in the
// directories its sessions are in, a linked work tree's leading to its
// main one, and in Claude Code's projects; nowhere else.
func TestFindClone(t *testing.T) {
	bare, work := teleportRepo(t)
	linked := filepath.Join(resolved(t), "linked")
	gitRun(t, work, "worktree", "add", "-q", "-b", "other", linked)
	s, _ := testServer(t)
	s.home = resolved(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	urls := []string{"file://" + bare, bare}
	if clone, _ := s.findClone(urls); clone != "" {
		t.Fatalf("found %s with nothing to go by", clone)
	}
	c, sid := shellIn(t, s, filepath.Join(linked, "sub"))
	if clone, remote := s.findClone(urls); clone != work || remote != "origin" {
		t.Errorf("from a session: %q %q, want %q origin", clone, remote, work)
	}
	if clone, _ := s.findClone([]string{"https://example.com/other.git"}); clone != "" {
		t.Errorf("another repo's: %s", clone)
	}
	c.Kill(sid)
	waitFor := time.Now().Add(5 * time.Second)
	for len(must(c.List())) > 0 && time.Now().Before(waitFor) {
		time.Sleep(20 * time.Millisecond)
	}
	writeFile(t, filepath.Join(s.home, ".claude", "projects", "-x", "a.jsonl"),
		"{\"type\":\"summary\"}\n"+fmt.Sprintf("{\"cwd\":%s}\n", jsonString(filepath.Join(work, "sub"))))
	if clone, _ := s.findClone(urls); clone != work {
		t.Errorf("from Claude Code's projects: %q, want %q", clone, work)
	}
}

// TestCloneRepo checks where a target with no clone makes one: where the
// source has it under its home, that clone when there is one, else in the
// home under the repo's name, a number after it when taken.
func TestCloneRepo(t *testing.T) {
	bare, _ := teleportRepo(t)
	s, _ := testServer(t)
	s.home = resolved(t)
	m := &TeleportManifest{Remotes: []string{bare}, HomeRel: "code/proj"}
	for _, c := range []struct{ rel, want string }{
		{"code/proj", "code/proj"},
		{"code/proj", "code/proj"}, // there now: taken as it is
		{"taken", "remote"},
		{"taken", "remote-2"},
		{"", "remote-3"},
	} {
		if c.rel == "taken" {
			writeFile(t, filepath.Join(s.home, "taken", "notes.txt"), "not a clone")
		}
		m.HomeRel = c.rel
		clone, remote, err := s.cloneRepo(m)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(s.home, filepath.FromSlash(c.want)); clone != want || remote != "origin" {
			t.Errorf("HomeRel %q: %q %q, want %q", c.rel, clone, remote, want)
		}
		if !exists(filepath.Join(clone, ".git")) {
			t.Errorf("no clone at %s", clone)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(s.home, "taken", "notes.txt")); string(b) != "not a clone" {
		t.Error("the directory in the way changed")
	}
	if got := s.homeRel(filepath.Join(s.home, "a", "b")); got != "a/b" {
		t.Errorf("homeRel %q", got)
	}
	if got := s.homeRel(filepath.Dir(s.home)); got != "" {
		t.Errorf("homeRel outside home %q", got)
	}
}

// TestManifestCheck checks that a conversation id names no path: a shell
// has none, Claude Code a UUID.
func TestManifestCheck(t *testing.T) {
	ok := TeleportManifest{Version: teleportVersion, Program: "shell", Head: strings.Repeat("a", 40),
		Remotes: []string{"https://example.com/x.git"}, Link: "0123456789ab"}
	if err := ok.check(); err != nil {
		t.Fatal(err)
	}
	for _, m := range []TeleportManifest{
		func() TeleportManifest { m := ok; m.Session = "../../x"; return m }(),
		func() TeleportManifest { m := ok; m.Program, m.Session = "claude", "../../x"; return m }(),
		func() TeleportManifest { m := ok; m.Link = "../x"; return m }(),
		func() TeleportManifest { m := ok; m.HomeRel = "../x"; return m }(),
		func() TeleportManifest { m := ok; m.Remotes = []string{"--upload-pack=x"}; return m }(),
	} {
		if m.check() == nil {
			t.Errorf("took %+v", m)
		}
	}
	for _, name := range []string{"claude/../../x", "claude/../x.jsonl"} {
		if packageName(name, "claude", "") || packageName(name, "claude", convID) {
			t.Errorf("took part %q", name)
		}
	}
}

// teleportConn sends a teleport of m and the package pkg to s, as a
// remote client, and returns what s answered first, and its connection.
func teleportConn(t *testing.T, s *Server, m TeleportManifest) (net.Conn, error) {
	t.Helper()
	conn, err := pipe(s, testPeer).dial()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	req, _ := json.Marshal(Request{Op: "teleport", Teleport: &m})
	conn.Write(append(req, '\n'))
	return conn, readOK(conn)
}

// TestTeleportShellRefused checks that Claude Code does not go to a host
// whose shell GoRex cannot type its resume into: refused at once.
func TestTeleportShellRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SHELL is POSIX's")
	}
	t.Setenv("SHELL", "/usr/local/bin/nu")
	s, _ := testServer(t)
	s.home = resolved(t)
	m := TeleportManifest{Version: teleportVersion, Program: "claude", Session: convID, Head: strings.Repeat("a", 40),
		Remotes: []string{"https://example.com/x.git"}, Link: "0123456789ab"}
	if _, err := teleportConn(t, s, m); err == nil || !strings.Contains(err.Error(), "nu") {
		t.Errorf("answered %v", err)
	}
}

// TestTeleportClientGone checks that a client gone once it sent the
// package leaves nothing on the target: no session, no work tree.
func TestTeleportClientGone(t *testing.T) {
	_, work := teleportRepo(t)
	s, _ := testServer(t)
	_, sid := shellIn(t, s, work)
	m, pkg, err := s.pack(sid)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(pkg.Name())
	defer pkg.Close()
	dst, _ := testServer(t)
	dst.home = resolved(t)
	conn, err := teleportConn(t, dst, m)
	if err != nil {
		t.Fatal(err)
	}
	pkg.Seek(0, io.SeekStart)
	io.Copy(conn, pkg)
	conn.Close()
	deadline := time.Now().Add(10 * time.Second)
	c := pipe(dst, "")
	defer c.Close()
	for {
		trees, _ := filepath.Glob(filepath.Join(dst.home, "*", ".gorex", "teleport", "*"))
		if len(must(c.List())) == 0 && len(trees) == 0 && !exists(filepath.Join(dst.home, "remote", ".gorex", "teleport")) {
			// The clone may stay; what the teleport made of it goes.
			time.Sleep(200 * time.Millisecond)
			if len(must(c.List())) == 0 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("left sessions %v, work trees %v", must(c.List()), trees)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestBundleStaleRemote checks that a commit the remote-tracking refs
// still name, though the remote dropped it, as a branch deleted after a
// squash merge, is bundled; and that with no remote answering, they stand
// for the remote.
func TestBundleStaleRemote(t *testing.T) {
	bare, work := teleportRepo(t)
	gitRun(t, work, "push", "-q", "origin", "HEAD:refs/heads/feature")
	gitRun(t, work, "fetch", "-q", "origin")
	gitRun(t, bare, "branch", "-D", "feature")
	s, _ := testServer(t)
	_, sid := shellIn(t, s, work)
	bundled := func() bool {
		t.Helper()
		_, pkg, err := s.pack(sid)
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(pkg.Name())
		defer pkg.Close()
		pkg.Seek(0, io.SeekStart)
		_, ok := readTar(t, pkg)["repo.bundle"]
		return ok
	}
	if !bundled() {
		t.Error("the commit the remote dropped is not bundled")
	}
	gitRun(t, work, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	if bundled() {
		t.Error("offline, the remote-tracking ref that has the commit did not stand for the remote")
	}
}

// TestBackupName checks the names of Claude Code's file-history backups
// a package takes.
func TestBackupName(t *testing.T) {
	for name, want := range map[string]bool{
		"47973c9fed2c8b5b@v2": true, "a@v10": true, "x@v1": false, "aa@v": false, "aa@vx": false,
		"../a@v1": false, "notes.txt": false, "@v1": false, "AA@v1": false,
	} {
		if got := backupName(name); got != want {
			t.Errorf("backupName(%q) = %v", name, got)
		}
	}
}

// snapshot is a file-history snapshot line of backups by path.
func snapshot(backups map[string]string) string {
	var parts []string
	for path, name := range backups {
		b := "null"
		if name != "" {
			b = strconv.Quote(name)
		}
		parts = append(parts, fmt.Sprintf("%s:{\"backupFileName\":%s,\"version\":1,\"backupTime\":\"2026-11-05T00:00:00Z\"}", jsonString(path), b))
	}
	return `{"type":"file-history-snapshot","messageId":"mx","snapshot":{"messageId":"mx","trackedFileBackups":{` +
		strings.Join(parts, ",") + `},"timestamp":"2026-11-05T00:00:00Z"},"isSnapshotUpdate":false}`
}

// TestRewriteSnapshot checks that a snapshot's files move from under the
// source's work tree to the target's, of POSIX and Windows paths, that
// one outside it is dropped, and a relative one stays, the rest of the
// line as it was.
func TestRewriteSnapshot(t *testing.T) {
	to := filepath.Join(string(filepath.Separator)+"t", "wt")
	for _, c := range []struct {
		root string
		in   map[string]string
		want map[string]string
	}{
		{"/src/proj", map[string]string{"/src/proj/a/b.go": "aa@v1", "/etc/hosts": "bb@v1", "rel/c.go": ""},
			map[string]string{filepath.Join(to, "a", "b.go"): "aa@v1", "rel/c.go": ""}},
		{`C:\Users\me\x`, map[string]string{`C:\Users\me\x\a.go`: "aa@v2", `D:\y.go`: "cc@v1"},
			map[string]string{filepath.Join(to, "a.go"): "aa@v2"}},
	} {
		got := rewriteSnapshot([]byte(snapshot(c.in)), c.root, to)
		if b := trackedBackups(got); !maps.Equal(b, c.want) {
			t.Errorf("rewrote %v to %v, want %v", c.in, b, c.want)
		}
		for _, s := range []string{`"isSnapshotUpdate":false`, `"messageId":"mx"`, `"timestamp":"2026-11-05T00:00:00Z"`, `"version":1`} {
			if !strings.Contains(string(got), s) {
				t.Errorf("lost %s: %s", s, got)
			}
		}
		if _, ok := c.in["rel/c.go"]; ok && !strings.Contains(string(got), `"backupFileName":null`) {
			t.Errorf("a null backup changed: %s", got)
		}
	}
	for _, line := range []string{snapshot(map[string]string{"rel/c.go": "aa@v1"}), `{"type":"user","cwd":"/x"}`} {
		if got := string(rewriteSnapshot([]byte(line), "/src/proj", to)); got != line {
			t.Errorf("changed %s to %s", line, got)
		}
	}
}

// TestPlaceHistory checks that backups merge into the target's history:
// those it lacks are added, the same ones it has are fine, a different
// one is refused, nothing changed; undo removes only what it added.
func TestPlaceHistory(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "aa@v1"), "a")
	writeFile(t, filepath.Join(src, "bb@v1"), "b")
	dst := filepath.Join(t.TempDir(), "file-history", convID)
	undo, err := placeHistory(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if got := tree(t, dst); len(got) != 2 {
		t.Errorf("placed %v", got)
	}
	for i := len(undo) - 1; i >= 0; i-- {
		undo[i]()
	}
	if exists(dst) {
		t.Error("the directory it made stayed")
	}
	writeFile(t, filepath.Join(dst, "aa@v1"), "a")
	writeFile(t, filepath.Join(dst, "zz@v1"), "z")
	undo, err = placeHistory(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(undo) - 1; i >= 0; i-- {
		undo[i]()
	}
	if got := tree(t, dst); !maps.Equal(got, map[string]string{"aa@v1": "a", "zz@v1": "z"}) {
		t.Errorf("undone to %v", got)
	}
	writeFile(t, filepath.Join(dst, "aa@v1"), "changed")
	if _, err := placeHistory(src, dst); err == nil {
		t.Error("a backup that differs was taken")
	}
	if got := tree(t, dst); !maps.Equal(got, map[string]string{"aa@v1": "changed", "zz@v1": "z"}) {
		t.Errorf("refused, but changed to %v", got)
	}
}
