//go:build darwin || linux || windows

package rex

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

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
