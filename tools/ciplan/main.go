// Command ciplan picks the CI checks and Go packages a change can reach, so
// CI runs those and nothing else. It never skips a test the change can break:
// any doubt runs everything.
//
// It reads BASE (the commit to diff against) and GITHUB_EVENT_NAME, diffs
// BASE..HEAD and writes key=value lines to $GITHUB_OUTPUT (and stdout); see
// result.lines for the keys. .github/workflows/ci.yml reads them.
//
// A run with no usable base (a manual run, an empty or all-zero BASE, a base
// that is not an ancestor of HEAD), an empty diff, or a change to go.mod,
// go.sum, the lint config, ci.yml or this tool runs everything. Otherwise
// plan maps each changed file to the packages that read it, and the tests of
// every package that imports one of those, on each GOOS.
//
//	BASE=origin/main go run ./tools/ciplan
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ciplan:", err)
		os.Exit(1)
	}
}

func run() error {
	top, err := git(".", "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	root := strings.TrimSpace(top)
	g, err := load(root)
	if err != nil {
		return err
	}
	base := strings.TrimSpace(os.Getenv("BASE"))
	ancestor := false
	if validBase(base) {
		_, err := git(root, "merge-base", "--is-ancestor", base, "HEAD")
		ancestor = err == nil
	}
	var r result
	if why := needAll(os.Getenv("GITHUB_EVENT_NAME"), base, ancestor); why != "" {
		fmt.Println("ciplan: everything runs:", why)
		r = everything(g)
	} else {
		out, err := git(root, "diff", "-z", "--name-only", "--no-renames", base, "HEAD")
		if err != nil {
			return err
		}
		var files []string
		for _, f := range strings.Split(out, "\x00") {
			if f != "" {
				files = append(files, f)
			}
		}
		fmt.Printf("ciplan: %d files changed since %s\n", len(files), base)
		r = plan(files, g)
	}
	text := strings.Join(r.lines(), "\n") + "\n"
	fmt.Print(text)
	if p := os.Getenv("GITHUB_OUTPUT"); p != "" {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		if _, err := f.WriteString(text); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	}
	return nil
}

// validBase reports whether base can name a commit: not empty and not the
// all-zero SHA a push that creates a branch sends.
func validBase(base string) bool {
	return base != "" && strings.Trim(base, "0") != ""
}

// needAll says why everything runs, or "" when the diff decides.
func needAll(event, base string, ancestor bool) string {
	switch {
	case event == "workflow_dispatch":
		return "manual run"
	case !validBase(base):
		return "no base commit"
	case !ancestor:
		return "base " + base + " is not an ancestor of HEAD"
	}
	return ""
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// existsIn reports whether a repo-relative path is in the checkout at root.
func existsIn(root string) func(string) bool {
	return func(p string) bool {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
		return err == nil
	}
}
