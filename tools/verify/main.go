// Command verify runs the cheap checks that the current diff needs before a
// push: the files changed since the merge base with origin/main, committed
// or not, select the checks. It stops at the first failure and ends with a
// one-line summary to paste into a PR as its Verified: line.
//
//	go run ./tools/verify          the checks for this diff
//	go run ./tools/verify -full    also the root package's (slow, UI) tests
//	go run ./tools/verify -hook    Claude Code PreToolUse hook (see .claude/settings.json):
//	                               verifies before a push or `gh pr create`; denies gh
//	                               writes without -R si-mods/gorex and local full test runs
//
// It needs no shell, so it runs the same on Windows, macOS and Linux.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Pinned tool versions. golangci-lint matches .github/workflows/ci.yml.
const (
	golangciLint = "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0"
	actionlint   = "github.com/rhysd/actionlint/cmd/actionlint@v1.7.7"
)

// buildTargets are CI's cross-builds; vetOS are CI's vet systems.
var (
	buildTargets = []string{"linux/amd64", "linux/arm64", "windows/amd64", "darwin/arm64"}
	vetOS        = []string{"linux", "windows", "darwin"}
)

// check is one step. A check with skip set does not run; skip says why.
// unverified marks a skip that leaves part of the diff unchecked.
type check struct {
	name       string
	dir        string // relative to the repo root
	args       []string
	env        []string
	tool       string   // a pinned `go install` module path, run from a cached binary
	pkgsFor    string   // GOOS whose buildable packages, from pkgsList, fill in "{pkgs}"
	pkgsList   []string // package directories for pkgsFor
	skip       string
	unverified bool
	fn         func(root string, log io.Writer) error // an in-process check; no command runs
}

// facts are what plan needs to know about the machine.
type facts struct {
	goos            string // host GOOS
	full            bool
	webNodeModules  bool
	iosToolsPresent bool // xcodegen, xcodebuild and ios/Frameworks
	readOnly        bool // the branch is not from si-mods: run no repo code
}

// plan maps the changed files to the checks to run, cheapest first.
func plan(files []string, f facts) []check {
	var goFiles, goDirs []string
	goAll, web, ios, gh, docs, arch := false, false, false, false, false, false
	seen := map[string]bool{}
	addDir := func(d string) {
		if !seen[d] {
			seen[d] = true
			goDirs = append(goDirs, d)
		}
	}
	for _, p := range files {
		p = filepath.ToSlash(p)
		switch {
		case p == "go.mod" || p == "go.sum":
			goAll = true
			arch = true
		case strings.HasSuffix(p, ".go"):
			goFiles = append(goFiles, p)
			addDir(pathDir(p))
			if strings.HasPrefix(p, "internal/") || strings.HasPrefix(p, "ios/") || strings.HasPrefix(p, "web/") || strings.HasPrefix(p, "locales/") || strings.HasPrefix(p, "tools/") {
				arch = true
			}
		}
		// Trigger generated-SSOT drift tests when any input or cross-platform output changes.
		switch p {
		case "assets/brands.json", "programs_brands.go", "web/src/programs_brands.ts", "ios/Shared/ProgramBrands.swift":
			addDir("./tools/genbrands")
		case "internal/rexproto/protocol.json", "internal/layout/palette_gen.go", "web/src/rex-protocol.gen.ts", "ios/GoRex/Rex/RexProtocol.gen.swift":
			addDir("./internal/rexproto")
		case "locales/en.json", "locales/zh-Hans.json", "locales/catalog_gen.go":
			addDir("./locales")
		}
		web = web || strings.HasPrefix(p, "web/")
		ios = ios || strings.HasPrefix(p, "ios/")
		gh = gh || strings.HasPrefix(p, ".github/")
		docs = docs || strings.HasPrefix(p, "docs/")
	}
	goChanged := goAll || len(goDirs) > 0
	pkgs := goDirs
	if goAll {
		pkgs = []string{"./..."}
	}

	var cs []check
	if len(goFiles) > 0 {
		cs = append(cs, check{name: "gofmt", args: append([]string{"gofmt", "-l"}, goFiles...)})
	}
	if arch {
		cs = append(cs, check{name: "arch boundaries", fn: archBoundariesCheck})
	}
	if docs {
		cs = append(cs, check{name: "design docs", fn: designDocsCheck})
	}
	if gh {
		// The self-hosted runners' labels are not ones actionlint knows.
		cs = append(cs, check{name: "actionlint", tool: actionlint, args: []string{"-ignore", `label ".+" is unknown`}})
	}
	if goChanged {
		for _, os := range vetOS {
			cs = append(cs, check{name: "vet " + os, args: []string{"go", "vet", "{pkgs}"},
				env: []string{"GOOS=" + os, "CGO_ENABLED=0"}, pkgsFor: os, pkgsList: pkgs})
		}
		for _, t := range buildTargets {
			os, arch, _ := strings.Cut(t, "/")
			cs = append(cs, check{name: "build " + t, args: []string{"go", "build", "./..."},
				env: []string{"GOOS=" + os, "GOARCH=" + arch, "CGO_ENABLED=0"}})
		}
	}
	webSkip := ""
	if web && !f.webNodeModules {
		webSkip = "web/node_modules missing; run `npm ci` in web/"
	}
	if web {
		cs = append(cs, check{name: "web typecheck", dir: "web", args: []string{"npm", "run", "typecheck"}, skip: webSkip, unverified: webSkip != ""})
	}
	if goChanged {
		// Only the packages that build for linux: web/tailnet builds for js/wasm only.
		cs = append(cs, check{name: "golangci-lint", tool: golangciLint,
			args: []string{"run", "{pkgs}"}, env: []string{"GOOS=linux", "CGO_ENABLED=0"}, pkgsFor: "linux", pkgsList: pkgs})
	}
	if web {
		cs = append(cs, check{name: "web test", dir: "web", args: []string{"npm", "test"}, skip: webSkip, unverified: webSkip != ""})
	}
	if goChanged {
		var sub []string
		root := goAll
		for _, d := range pkgs {
			if d == "." {
				root = true
			} else if d != "./..." {
				sub = append(sub, d)
			}
		}
		if len(sub) > 0 {
			cs = append(cs, check{name: "go test", args: []string{"go", "test", "-count=1", "{pkgs}"}, pkgsFor: f.goos, pkgsList: sub})
		}
		if root {
			c := check{name: "go test root", args: []string{"go", "test", "-count=1", "-timeout", "40m", "."}}
			if !f.full {
				c.skip = "slow UI tests; run with -full (CI runs them)"
			}
			cs = append(cs, c)
		}
	}
	if ios {
		c := check{name: "ios xcodebuild", dir: "ios"}
		switch {
		case f.goos != "darwin":
			c.skip = "NOT VERIFIED: ios changed; build with xcodebuild on the Mac (see the gorex-ui-testing skill)"
			c.unverified = true
		case !f.iosToolsPresent:
			c.skip = "NOT VERIFIED: ios changed; needs xcodegen, xcodebuild and ios/Frameworks/Tailnet.xcframework (scripts/build-tailnet; see the gorex-ui-testing skill)"
			c.unverified = true
		default:
			c.args = []string{"sh", "-c", `xcodegen generate && xcodebuild -scheme GoRex -destination "generic/platform=iOS Simulator" -derivedDataPath "${TMPDIR:-/tmp}/gorex-verify-dd" CODE_SIGNING_ALLOWED=NO build -quiet`}
		}
		cs = append(cs, c)
	}
	if f.readOnly {
		cs = readOnlyPlan(cs)
	}
	return cs
}

// readOnlyPlan keeps the checks that only read and compile the branch
// (gofmt, actionlint, design docs, vet, build) and skips the rest. Tests, npm scripts and
// xcodebuild run the branch's code or scripts; golangci-lint reads the
// branch's own config. A skip counts as NOT VERIFIED.
func readOnlyPlan(cs []check) []check {
	for i, c := range cs {
		safe := c.name == "gofmt" || c.name == "arch boundaries" || c.name == "actionlint" || c.name == "design docs" ||
			strings.HasPrefix(c.name, "vet ") || strings.HasPrefix(c.name, "build ")
		if !safe && c.skip == "" {
			cs[i].skip = "NOT VERIFIED: branch is not from si-mods; read-only checks only"
			cs[i].unverified = true
		}
	}
	return cs
}

var siModsURL = regexp.MustCompile(`(?i)^(?:(?:https?|ssh|git)://)?(?:[^@/\s]+@)?github\.com[:/]si-mods/gorex(?:\.git)?/?$`)

// trustedBranch decides whether the checkout at root holds only code that
// came from si-mods, so its tests and scripts may run. It reads only the
// repository's own state (git config, refs, the HEAD reflog), never a file
// of the working tree, so a pushed branch cannot grant itself trust. Rule:
// some remote points at github.com/si-mods/gorex, and every commit of HEAD
// that is not on that remote's main was made here by a plain `git commit`
// (per the HEAD reflog). Other remote branches do not count: a fork branch
// pushed to the remote would turn into "si-mods" code. Commits fetched from a
// fork, cherry-picked, rebased, merged or amended do not qualify. The rule
// judges where commits came from, not where their content came from. Any
// doubt returns false.
func trustedBranch(root string) (bool, string) {
	out, err := git(root, "config", "--get-regexp", `^remote\..*\.url$`)
	if err != nil {
		return false, "no remote configured"
	}
	var args []string
	for _, l := range strings.Split(out, "\n") {
		key, url, _ := strings.Cut(strings.TrimSpace(l), " ")
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		if name != key && siModsURL.MatchString(strings.TrimSpace(url)) {
			ref := "refs/remotes/" + name + "/main"
			if _, err := git(root, "rev-parse", "--verify", "-q", ref); err == nil {
				args = append(args, ref)
			}
		}
	}
	if len(args) == 0 {
		return false, "no si-mods remote with a main branch (github.com/si-mods/gorex)"
	}
	out, err = git(root, append([]string{"rev-list", "HEAD", "--not"}, args...)...)
	if err != nil {
		return false, "cannot list commits: " + err.Error()
	}
	extra := strings.Fields(out)
	if len(extra) == 0 {
		return true, "HEAD is on si-mods main"
	}
	log, err := git(root, "log", "-g", "--format=%H %gs", "HEAD")
	if err != nil {
		return false, "cannot read the HEAD reflog"
	}
	local := map[string]bool{}
	for _, l := range strings.Split(log, "\n") {
		h, subj, _ := strings.Cut(strings.TrimSpace(l), " ")
		if kind, _, _ := strings.Cut(subj, ":"); kind == "commit" || kind == "commit (initial)" {
			local[h] = true
		}
	}
	for _, h := range extra {
		if !local[h] {
			return false, "commit " + h[:min(len(h), 12)] + " is neither on si-mods main nor made here by git commit"
		}
	}
	return true, fmt.Sprintf("%d local commits on top of si-mods", len(extra))
}

func pathDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return "./" + p[:i]
	}
	return "."
}

// changedFiles lists the files that differ from the merge base with
// origin/main, committed, staged, unstaged or untracked.
func changedFiles(root string) ([]string, error) {
	base, err := git(root, "merge-base", "HEAD", "origin/main")
	if err != nil {
		return nil, fmt.Errorf("merge-base with origin/main: %v (run `git fetch origin`)", err)
	}
	diff, err := git(root, "diff", "--name-only", strings.TrimSpace(base))
	if err != nil {
		return nil, err
	}
	untracked, err := git(root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for _, l := range strings.Split(diff+"\n"+untracked, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out, nil
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

// existing keeps the paths that still exist (gofmt cannot read a deleted file).
func existing(root string, paths []string) []string {
	var out []string
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// buildable lists, for goos, the packages among dirs that have Go files for
// it, so `go vet` and `go test` do not fail on a darwin-only package on
// windows or on a deleted directory.
func buildable(root, goos string, dirs []string) ([]string, error) {
	var keep []string
	for _, d := range dirs {
		if d == "./..." {
			return dirs, nil
		}
		if st, err := os.Stat(filepath.Join(root, d)); err == nil && st.IsDir() {
			keep = append(keep, d)
		}
	}
	if len(keep) == 0 {
		return nil, nil
	}
	cmd := exec.Command("go", append([]string{"list", "-e", "-f", "{{if or .GoFiles .TestGoFiles .XTestGoFiles}}{{.Dir}}{{end}}"}, keep...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS="+goos, "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %v", err)
	}
	var pkgs []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			rel, err := filepath.Rel(root, l)
			if err != nil {
				return nil, err
			}
			pkgs = append(pkgs, "./"+filepath.ToSlash(rel))
		}
	}
	return pkgs, nil
}

// toolBin installs a pinned tool once into the user cache and returns it.
func toolBin(mod string, log io.Writer) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	name, ver, _ := strings.Cut(mod[strings.LastIndex(mod, "/")+1:], "@")
	bin := filepath.Join(cache, "gorex-verify", name+"-"+ver)
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if _, err := os.Stat(bin); err == nil {
		return bin, nil
	}
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return "", err
	}
	gobin, err := os.MkdirTemp(filepath.Dir(bin), "install-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(gobin)
	fmt.Fprintf(log, "installing %s (once)\n", mod)
	cmd := exec.Command("go", "install", mod)
	cmd.Env = append(os.Environ(), "GOBIN="+gobin, "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go install %s: %v", mod, err)
	}
	built := filepath.Join(gobin, name)
	if runtime.GOOS == "windows" {
		built += ".exe"
	}
	return bin, os.Rename(built, bin)
}

type result struct {
	name, state, note string
	unverified        bool
}

// run runs the checks in order and stops at the first failure. Output goes
// to log; the failing check's output is returned too.
func run(root string, cs []check, log io.Writer) ([]result, string) {
	var rs []result
	for _, c := range cs {
		switch {
		case c.skip != "":
			fmt.Fprintf(log, "== %s: skipped: %s\n", c.name, c.skip)
			rs = append(rs, result{name: c.name, state: "skipped", note: c.skip, unverified: c.unverified})
			continue
		}
		if c.fn != nil {
			fmt.Fprintf(log, "== %s\n", c.name)
			if err := c.fn(root, log); err != nil {
				fmt.Fprintf(log, "== %s: FAILED\n", c.name)
				rs = append(rs, result{name: c.name, state: "failed"})
				return finish(rs, cs[len(rs):]), fmt.Sprintf("%s failed:\n%v", c.name, err)
			}
			fmt.Fprintf(log, "== %s: passed\n", c.name)
			rs = append(rs, result{name: c.name, state: "passed"})
			continue
		}
		args := c.args
		if c.tool != "" {
			bin, err := toolBin(c.tool, log)
			if err != nil {
				rs = append(rs, result{name: c.name, state: "failed"})
				return finish(rs, cs[len(rs):]), err.Error()
			}
			args = append([]string{bin}, args...)
		}
		if c.pkgsFor != "" {
			pkgs, err := buildable(root, c.pkgsFor, c.pkgsList)
			if err != nil {
				rs = append(rs, result{name: c.name, state: "failed"})
				return finish(rs, cs[len(rs):]), err.Error()
			}
			if len(pkgs) == 0 {
				fmt.Fprintf(log, "== %s: skipped: no packages for %s\n", c.name, c.pkgsFor)
				rs = append(rs, result{name: c.name, state: "skipped", note: "no packages for " + c.pkgsFor})
				continue
			}
			args = expand(args, pkgs)
		}
		if c.name == "gofmt" {
			args = append(args[:2:2], existing(root, args[2:])...)
			if len(args) == 2 {
				rs = append(rs, result{name: c.name, state: "passed"})
				continue
			}
		}
		fmt.Fprintf(log, "== %s: %s\n", c.name, strings.Join(args, " "))
		start := time.Now()
		var out bytes.Buffer
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = filepath.Join(root, c.dir)
		cmd.Env = append(os.Environ(), c.env...)
		w := io.MultiWriter(log, &out)
		cmd.Stdout, cmd.Stderr = w, w
		err := cmd.Run()
		if err == nil && c.name == "gofmt" && strings.TrimSpace(out.String()) != "" {
			err = fmt.Errorf("files need gofmt -w")
		}
		if err != nil {
			fmt.Fprintf(log, "== %s: FAILED (%v)\n", c.name, err)
			rs = append(rs, result{name: c.name, state: "failed"})
			return finish(rs, cs[len(rs):]), fmt.Sprintf("%s failed: %v\n%s", c.name, err, tail(out.String(), 150))
		}
		fmt.Fprintf(log, "== %s: passed (%s)\n", c.name, time.Since(start).Round(100*time.Millisecond))
		rs = append(rs, result{name: c.name, state: "passed"})
	}
	return rs, ""
}

func finish(rs []result, rest []check) []result {
	for _, c := range rest {
		rs = append(rs, result{name: c.name, state: "not run"})
	}
	return rs
}

func expand(args, pkgs []string) []string {
	var out []string
	for _, a := range args {
		if a == "{pkgs}" {
			out = append(out, pkgs...)
		} else {
			out = append(out, a)
		}
	}
	return out
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append([]string{fmt.Sprintf("... (%d lines cut)", len(lines)-n)}, lines[len(lines)-n:]...)
	}
	return strings.Join(lines, "\n")
}

// summary is the one-line Verified: line for a PR body.
func summary(rs []result) string {
	if len(rs) == 0 {
		return "Verified: go run ./tools/verify: no checks needed for this diff"
	}
	var parts []string
	for _, r := range rs {
		s := r.name + " " + r.state
		if r.unverified {
			s = r.name + " NOT VERIFIED"
		}
		parts = append(parts, s)
	}
	return "Verified: go run ./tools/verify: " + strings.Join(parts, "; ")
}

func ok(rs []result) bool {
	for _, r := range rs {
		if r.state == "failed" {
			return false
		}
	}
	return true
}

func repoRoot(dir string) (string, error) {
	out, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.FromSlash(strings.TrimSpace(out)), nil
}

func machineFacts(root string, full bool) facts {
	f := facts{goos: runtime.GOOS, full: full}
	if st, err := os.Stat(filepath.Join(root, "web", "node_modules")); err == nil && st.IsDir() {
		f.webNodeModules = true
	}
	_, e1 := exec.LookPath("xcodegen")
	_, e2 := exec.LookPath("xcodebuild")
	_, e3 := os.Stat(filepath.Join(root, "ios", "Frameworks", "Tailnet.xcframework"))
	f.iosToolsPresent = e1 == nil && e2 == nil && e3 == nil
	return f
}

// verify runs the checks for the repo at dir.
// The returned mode line says which mode ran and why.
func verify(dir string, full bool, log io.Writer) (good bool, sum, failure, mode string, err error) {
	root, err := repoRoot(dir)
	if err != nil {
		return false, "", "", "", err
	}
	files, err := changedFiles(root)
	if err != nil {
		return false, "", "", "", err
	}
	trusted, why := trustedBranch(root)
	mode = "verify: mode full (tests and scripts run): " + why
	if !trusted {
		mode = "verify: mode read-only (gofmt, vet, build; no tests, scripts or lint): " + why
	}
	fmt.Fprintln(log, mode)
	fmt.Fprintf(log, "verify: %d changed files vs merge-base with origin/main\n", len(files))
	f := machineFacts(root, full)
	f.readOnly = !trusted
	rs, failure := run(root, plan(files, f), log)
	return ok(rs), summary(rs), failure, mode, nil
}

func main() {
	full := flag.Bool("full", false, "also run the root package's tests (slow)")
	hook := flag.Bool("hook", false, "run as a Claude Code PreToolUse hook (reads its JSON on stdin)")
	whole := flag.Bool("audit", false, "run the whole-repo maintenance audit instead of the diff's checks")
	flag.Parse()
	if *hook {
		os.Exit(hookMain(os.Stdin, os.Stderr, os.Stdout))
	}
	if *whole {
		good, failure, sum, err := audit(".", os.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "audit:", err)
			os.Exit(1)
		}
		if !good {
			fmt.Fprintln(os.Stderr, "\n"+failure)
		}
		fmt.Println("\n" + sum)
		if !good {
			os.Exit(1)
		}
		return
	}
	good, sum, failure, _, err := verify(".", *full, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "verify:", err)
		os.Exit(1)
	}
	if !good {
		fmt.Fprintln(os.Stderr, "\n"+failure)
	}
	fmt.Println("\n" + sum)
	if !good {
		os.Exit(1)
	}
}

// ---- hook mode ----

type hookInput struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

var (
	pushRe   = regexp.MustCompile(`(^|[\s;&|(])git(\s+-C\s+("[^"]*"|'[^']*'|\S+))?\s+push\b|(^|[\s;&|(])gh\s+pr\s+create\b`)
	deleteRe = regexp.MustCompile(`\bgit\b.*\bpush\b.*(\s--delete\b|\s-d\s)`)
	cdRe     = regexp.MustCompile(`^\s*(?:cd|Set-Location)\s+("[^"]*"|'[^']*'|[^\s;&|]+)\s*(?:&&|;)`)
	gitCRe   = regexp.MustCompile(`\bgit\s+-C\s+("[^"]*"|'[^']*'|\S+)\s+push\b`)
	sidRe    = regexp.MustCompile(`[^A-Za-z0-9_-]`)
	driveRe  = regexp.MustCompile(`^/([a-zA-Z])(/|$)`)
)

// triggers reports whether a shell command pushes or opens a PR.
func triggers(cmd string) bool {
	return pushRe.MatchString(cmd) && !deleteRe.MatchString(cmd)
}

// targetDir is where the command runs: the hook's cwd, moved by a leading
// `cd DIR &&` or a `git -C DIR push`.
func targetDir(cwd, cmd string) string {
	dir := cwd
	for _, re := range []*regexp.Regexp{cdRe, gitCRe} {
		if m := re.FindStringSubmatch(cmd); m != nil {
			p := strings.Trim(m[1], `"'`)
			if runtime.GOOS == "windows" {
				p = driveRe.ReplaceAllString(p, "$1:/")
			}
			if !filepath.IsAbs(p) && !strings.HasPrefix(p, "~") {
				p = filepath.Join(dir, p)
			}
			dir = p
		}
	}
	return dir
}

func isGoRex(root string) bool {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	return err == nil && strings.HasPrefix(strings.TrimSpace(string(b)), "module gorex\n") ||
		err == nil && strings.HasPrefix(string(b), "module gorex\r\n")
}

func blockFile(session string) string {
	if session == "" {
		session = "none"
	}
	return filepath.Join(os.TempDir(), "gorex-verify-blocks", sidRe.ReplaceAllString(session, "_"))
}

func blocks(session string) int {
	b, _ := os.ReadFile(blockFile(session))
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func setBlocks(session string, n int) {
	f := blockFile(session)
	if n == 0 {
		os.Remove(f)
		return
	}
	_ = os.MkdirAll(filepath.Dir(f), 0o755)
	_ = os.WriteFile(f, []byte(strconv.Itoa(n)), 0o644)
}

// maxBlocks is how many pushes a session has blocked before the next one
// goes through unverified, so an agent cannot get stuck.
const maxBlocks = 2

// hookMain is the PreToolUse hook. It always exits 0 and blocks a call with
// a "deny" decision in JSON on stdout, not with exit code 2: `go run`
// reports any failing exit of its program as 1, which Claude Code does not
// treat as a block.
func hookMain(stdin io.Reader, stderr, stdout io.Writer) int {
	if strings.EqualFold(os.Getenv("GOREX_VERIFY"), "off") {
		return 0
	}
	var in hookInput
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return 0
	}
	cmd := in.ToolInput.Command
	dir := targetDir(in.Cwd, cmd)
	var root string
	var checked, rex bool
	where := func() (string, string, bool) {
		if !checked {
			checked = true
			r, err := repoRoot(dir)
			root, rex = r, err == nil && isGoRex(r)
		}
		return root, dir, rex
	}
	guard := guardReason(cmd, where)
	if guard == "" && !triggers(cmd) {
		return 0
	}
	if _, _, ok := where(); !ok {
		return 0
	}
	if guard != "" {
		fmt.Fprintln(stderr, guard)
		_ = json.NewEncoder(stdout).Encode(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":            "PreToolUse",
				"permissionDecision":       "deny",
				"permissionDecisionReason": guard,
			},
		})
		return 0
	}
	if n := blocks(in.SessionID); n >= maxBlocks {
		setBlocks(in.SessionID, 0)
		msg := fmt.Sprintf("WARNING: tools/verify blocked this push %d times in this session; letting it through UNVERIFIED. Say so in the PR body and fix the failing checks.", n)
		fmt.Fprintln(stderr, msg)
		_ = json.NewEncoder(stdout).Encode(map[string]any{
			"systemMessage": msg,
			"hookSpecificOutput": map[string]any{
				"hookEventName":     "PreToolUse",
				"additionalContext": msg,
			},
		})
		return 0
	}
	var log bytes.Buffer
	good, sum, failure, mode, err := verify(root, false, &log)
	if mode != "" {
		fmt.Fprintln(stderr, mode)
	}
	if err != nil {
		// A broken harness must not block work; say why and allow.
		fmt.Fprintln(stderr, "tools/verify could not run:", err)
		return 0
	}
	if good {
		setBlocks(in.SessionID, 0)
		if strings.Contains(mode, "read-only") {
			_ = json.NewEncoder(stdout).Encode(map[string]any{"systemMessage": mode})
		}
		return 0
	}
	n := blocks(in.SessionID) + 1
	setBlocks(in.SessionID, n)
	reason := fmt.Sprintf("tools/verify blocked this command (%d of %d; set GOREX_VERIFY=off to bypass). Fix the failure, then run `go run ./tools/verify` in %s.\n\n%s\n\n%s", n, maxBlocks, root, failure, sum+"\n"+mode)
	fmt.Fprintln(stderr, reason)
	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": reason,
		},
	})
	return 0
}
