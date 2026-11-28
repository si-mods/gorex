package main

// The whole-repo maintenance audit, `go run ./tools/verify -audit`:
// the checks that catch rot between diffs, which the per-diff plan misses
// because no PR touched the dead code. docs/maintenance.md says when to
// run it and what to do with what it finds.
//
// Today that is dead code. It compares every target, because a function
// unused on one GOOS may be live on another, and keeps only what no
// target reaches.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// deadcodeTool is pinned like golangci-lint in main.go. deadcode exits 0
// with findings, so its output, not its exit code, is the result.
const deadcodeTool = "golang.org/x/tools/cmd/deadcode@v0.53.0"

// deadcodeBlind are packages whose callers deadcode cannot see: gomobile
// binds ios/** for Swift, and web/{tailnet,tailcat,jsbridge,wsclient} run
// behind the web client's wasm engines (GOOS=js GOARCH=wasm). A function
// there with no native Go caller is not dead.
var deadcodeBlind = []string{"ios/", "web/tailnet/", "web/tailcat/", "web/jsbridge/", "web/wsclient/"}

// deadcodeFinding reads one `path:line:col: unreachable func: name` line.
var deadcodeFinding = regexp.MustCompile(`^(.+?):(\d+):(\d+): unreachable func: (.+)$`)

type deadFinding struct {
	path, line, name string
}

func (f deadFinding) String() string { return f.path + ":" + f.line + ": " + f.name }

// auditDeadcode returns the functions that no target can reach, sorted.
func auditDeadcode(root string, log io.Writer) ([]deadFinding, error) {
	bin, err := toolBin(deadcodeTool, log)
	if err != nil {
		return nil, err
	}
	seen := map[string][]deadFinding{} // key: path + name, one per target
	targets := []string{"linux", "windows", "darwin"}
	for _, goos := range targets {
		cmd := exec.Command(bin, "-test", "./...")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+auditArch(goos), "CGO_ENABLED=0")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("deadcode for %s: %v: %s", goos, err, tail(out.String(), 5))
		}
		for _, l := range strings.Split(out.String(), "\n") {
			m := deadcodeFinding.FindStringSubmatch(strings.TrimSpace(l))
			if m == nil {
				continue
			}
			f := deadFinding{path: filepathSlash(m[1]), line: m[2], name: m[4]}
			key := f.path + " " + f.name
			seen[key] = append(seen[key], f)
		}
	}
	var dead []deadFinding
	for _, fs := range seen {
		if len(fs) < len(targets) || blind(fs[0].path) {
			continue
		}
		dead = append(dead, fs[0])
	}
	sort.Slice(dead, func(i, j int) bool {
		return dead[i].path < dead[j].path || dead[i].path == dead[j].path && dead[i].name < dead[j].name
	})
	return dead, nil
}

// auditArch is the architecture CI builds for goos (see buildTargets).
func auditArch(goos string) string {
	if goos == "darwin" {
		return "arm64"
	}
	return "amd64"
}

func filepathSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// blind reports whether path is a package whose callers no Go analysis
// can see.
func blind(path string) bool {
	for _, p := range deadcodeBlind {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// audit runs the whole-repo checks and returns whether it found nothing,
// the failure text and the one-line summary.
func audit(dir string, log io.Writer) (good bool, failure, summary string, err error) {
	root, err := repoRoot(dir)
	if err != nil {
		return false, "", "", err
	}
	dead, err := auditDeadcode(root, log)
	if err != nil {
		return false, "", "", err
	}
	if len(dead) > 0 {
		var b strings.Builder
		for _, f := range dead {
			fmt.Fprintln(&b, "  "+f.String())
		}
		failure = fmt.Sprintf("dead code on every target:\n%s", b.String())
		fmt.Fprint(log, failure)
		return false, failure, "Audit: FAILED, dead code", nil
	}
	summary = "Audit: clean, no dead code on linux, windows or darwin"
	return true, "", summary, nil
}
