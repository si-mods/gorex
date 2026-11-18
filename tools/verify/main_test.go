package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func names(cs []check) []string {
	var out []string
	for _, c := range cs {
		n := c.name
		if c.skip != "" {
			n += " (skip)"
		}
		out = append(out, n)
	}
	return out
}

func find(cs []check, name string) check {
	for _, c := range cs {
		if c.name == name {
			return c
		}
	}
	return check{}
}

func TestPlan(t *testing.T) {
	goChecks := []string{"vet linux", "vet windows", "vet darwin",
		"build linux/amd64", "build linux/arm64", "build windows/amd64", "build darwin/arm64"}
	win := facts{goos: "windows", webNodeModules: true}
	tests := []struct {
		name  string
		files []string
		f     facts
		want  []string
	}{
		{"nothing", nil, win, nil},
		{"docs only", []string{"docs/lessons.md", "README.md"}, win, nil},
		{"sub package", []string{"internal/rex/server.go"}, win,
			append(append([]string{"gofmt"}, goChecks...), "golangci-lint", "go test")},
		{"root package", []string{"app.go"}, win,
			append(append([]string{"gofmt"}, goChecks...), "golangci-lint", "go test root (skip)")},
		{"root package full", []string{"app.go"}, facts{goos: "linux", full: true},
			append(append([]string{"gofmt"}, goChecks...), "golangci-lint", "go test root")},
		{"go.mod only", []string{"go.mod"}, win,
			append(append([]string{}, goChecks...), "golangci-lint", "go test root (skip)")},
		{"web", []string{"web/src/a.ts"}, win, []string{"web typecheck", "web test"}},
		{"web no node_modules", []string{"web/src/a.ts"}, facts{goos: "windows"},
			[]string{"web typecheck (skip)", "web test (skip)"}},
		{"ios on windows", []string{"ios/GoRex/App.swift"}, win, []string{"ios xcodebuild (skip)"}},
		{"ios on mac without frameworks", []string{"ios/GoRex/App.swift"}, facts{goos: "darwin"}, []string{"ios xcodebuild (skip)"}},
		{"ios on mac", []string{"ios/GoRex/App.swift"}, facts{goos: "darwin", iosToolsPresent: true}, []string{"ios xcodebuild"}},
		{"workflow", []string{".github/workflows/ci.yml"}, win, []string{"actionlint"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := names(plan(tt.files, tt.f)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("plan(%v) =\n %q\nwant\n %q", tt.files, got, tt.want)
			}
		})
	}
}

func TestPlanPackages(t *testing.T) {
	cs := plan([]string{"internal/rex/a.go", "internal/rex/b_test.go", "app.go", "internal/cli/c.go", "web/x.ts"}, facts{goos: "windows"})
	if got := find(cs, "gofmt").args; !reflect.DeepEqual(got, []string{"gofmt", "-l", "internal/rex/a.go", "internal/rex/b_test.go", "app.go", "internal/cli/c.go"}) {
		t.Errorf("gofmt args = %q", got)
	}
	if got := find(cs, "vet darwin").pkgsList; !reflect.DeepEqual(got, []string{"./internal/rex", ".", "./internal/cli"}) {
		t.Errorf("vet pkgs = %q", got)
	}
	if got := find(cs, "go test").pkgsList; !reflect.DeepEqual(got, []string{"./internal/rex", "./internal/cli"}) {
		t.Errorf("test pkgs = %q (root must not be in it)", got)
	}
	if got := find(cs, "golangci-lint"); got.tool != golangciLint || !reflect.DeepEqual(got.args, []string{"run", "./internal/rex", ".", "./internal/cli"}) {
		t.Errorf("lint = %+v", got)
	}
	if !strings.Contains(find(cs, "web test").skip, "npm ci") {
		t.Errorf("web skip should tell to npm ci")
	}
}

func TestSummary(t *testing.T) {
	got := summary([]result{
		{name: "gofmt", state: "passed"},
		{name: "go test root", state: "skipped"},
		{name: "ios xcodebuild", state: "skipped", unverified: true},
		{name: "golangci-lint", state: "failed"},
		{name: "go test", state: "not run"},
	})
	want := "Verified: go run ./tools/verify: gofmt passed; go test root skipped; ios xcodebuild NOT VERIFIED; golangci-lint failed; go test not run"
	if got != want {
		t.Errorf("summary =\n %s\nwant\n %s", got, want)
	}
	if strings.Contains(got, "\n") {
		t.Error("summary must be one line")
	}
}

func TestTriggers(t *testing.T) {
	yes := []string{
		"git push",
		"git push -u origin feat/x",
		"cd /d/src/proj && git push origin HEAD",
		`git -C "D:/src/proj" push`,
		"gh pr create -R si-mods/gorex --title x",
		"git add -A; git commit -m x; git push",
		"(git push)",
	}
	no := []string{
		"git status",
		"go test ./...",
		"git push origin --delete feat/old",
		"echo pushed",
		"git pushx",
		"gh pr view 3",
		"mygit push",
	}
	for _, c := range yes {
		if !triggers(c) {
			t.Errorf("triggers(%q) = false", c)
		}
	}
	for _, c := range no {
		if triggers(c) {
			t.Errorf("triggers(%q) = true", c)
		}
	}
}

func TestTargetDir(t *testing.T) {
	cwd := filepath.FromSlash("/repo")
	if runtime.GOOS == "windows" {
		cwd = `C:\repo`
	}
	if got := targetDir(cwd, "git push"); got != cwd {
		t.Errorf("plain: %q", got)
	}
	if got := targetDir(cwd, "cd .claude/worktrees/x && git push"); got != filepath.Join(cwd, ".claude/worktrees/x") {
		t.Errorf("relative cd: %q", got)
	}
	if got := targetDir(cwd, `git -C "sub dir" push`); got != filepath.Join(cwd, "sub dir") {
		t.Errorf("git -C: %q", got)
	}
	if runtime.GOOS == "windows" {
		if got := targetDir(cwd, "cd /d/src/proj && git push"); got != "d:/src/proj" {
			t.Errorf("git bash drive path: %q", got)
		}
	}
}

func TestHookIgnores(t *testing.T) {
	cases := []string{
		`{"session_id":"x","tool_name":"Bash","tool_input":{"command":"ls -la"}}`,
		`not json`,
		`{}`,
	}
	for _, in := range cases {
		var errb, outb bytes.Buffer
		if code := hookMain(strings.NewReader(in), &errb, &outb); code != 0 || errb.Len() != 0 {
			t.Errorf("hookMain(%s) = %d, stderr %q", in, code, errb.String())
		}
	}
}

func TestHookOff(t *testing.T) {
	t.Setenv("GOREX_VERIFY", "off")
	in := `{"session_id":"x","cwd":".","tool_name":"Bash","tool_input":{"command":"git push"}}`
	if code := hookMain(strings.NewReader(in), &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Errorf("GOREX_VERIFY=off: exit %d", code)
	}
}

func TestHookNotGoRex(t *testing.T) {
	// A push from a directory outside any GoRex checkout passes untouched.
	in := `{"session_id":"x","cwd":` + quote(t.TempDir()) + `,"tool_name":"Bash","tool_input":{"command":"git push"}}`
	var errb bytes.Buffer
	if code := hookMain(strings.NewReader(in), &errb, &bytes.Buffer{}); code != 0 {
		t.Errorf("exit %d, stderr %q", code, errb.String())
	}
}

func TestHookEscapeHatch(t *testing.T) {
	sid := "test-" + strings.ReplaceAll(t.Name(), "/", "_")
	setBlocks(sid, maxBlocks)
	t.Cleanup(func() { setBlocks(sid, 0) })
	in := `{"session_id":"` + sid + `","cwd":` + quote(repoDirForTest(t)) + `,"tool_name":"Bash","tool_input":{"command":"git push"}}`
	var errb, outb bytes.Buffer
	if code := hookMain(strings.NewReader(in), &errb, &outb); code != 0 {
		t.Fatalf("after %d blocks: exit %d", maxBlocks, code)
	}
	if !strings.Contains(errb.String(), "UNVERIFIED") || !strings.Contains(outb.String(), "systemMessage") {
		t.Errorf("no loud warning: stderr %q stdout %q", errb.String(), outb.String())
	}
	if blocks(sid) != 0 {
		t.Errorf("counter not reset after the escape")
	}
}

func TestBlockCounter(t *testing.T) {
	sid := "weird/../id:" + t.Name()
	t.Cleanup(func() { setBlocks(sid, 0) })
	if blocks(sid) != 0 {
		t.Fatal("fresh session has blocks")
	}
	setBlocks(sid, 1)
	setBlocks(sid, blocks(sid)+1)
	if got := blocks(sid); got != 2 {
		t.Errorf("blocks = %d", got)
	}
	if strings.Contains(filepath.Base(blockFile(sid)), "..") || filepath.Dir(blockFile(sid)) != filepath.Join(os.TempDir(), "gorex-verify-blocks") {
		t.Errorf("unsafe block file %q", blockFile(sid))
	}
}

func repoDirForTest(t *testing.T) string {
	root, err := repoRoot(".")
	if err != nil {
		t.Skip("not in a git checkout")
	}
	return root
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(filepath.ToSlash(s), `"`, `\"`) + `"`
}

// TestHookDenies runs the hook against a throwaway GoRex-like repo whose
// diff fails gofmt: two pushes are denied, the third goes through.
func TestHookDenies(t *testing.T) {
	dir := t.TempDir()
	gitT := func(args ...string) {
		t.Helper()
		if _, err := git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	gitT("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module gorex\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT("add", "go.mod")
	gitT("-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "init")
	gitT("update-ref", "refs/remotes/origin/main", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte("package gorex\nvar  x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sid := "test-deny"
	setBlocks(sid, 0)
	t.Cleanup(func() { setBlocks(sid, 0) })
	in := `{"session_id":"` + sid + `","cwd":` + quote(dir) + `,"tool_name":"Bash","tool_input":{"command":"git push -u origin HEAD"}}`
	for i := 1; i <= maxBlocks+1; i++ {
		var errb, outb bytes.Buffer
		if code := hookMain(strings.NewReader(in), &errb, &outb); code != 0 {
			t.Fatalf("push %d: exit %d", i, code)
		}
		denied := strings.Contains(outb.String(), `"permissionDecision":"deny"`)
		if i <= maxBlocks && (!denied || !strings.Contains(outb.String(), "bad.go")) {
			t.Errorf("push %d not denied with the failure: %s", i, outb.String())
		}
		if i == maxBlocks+1 && (denied || !strings.Contains(errb.String(), "UNVERIFIED")) {
			t.Errorf("push %d should pass with a warning: stdout %s stderr %s", i, outb.String(), errb.String())
		}
	}
}

// trustRepo makes a repo with one commit on a remote-tracking main, and
// returns a helper that commits and runs git in it. No network is used.
func trustRepo(t *testing.T, url string) (string, func(args ...string) string) {
	t.Helper()
	dir := t.TempDir()
	g := func(args ...string) string {
		t.Helper()
		out, err := git(dir, append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	g("init", "-q", "-b", "main")
	g("commit", "-q", "--allow-empty", "-m", "base")
	g("remote", "add", "origin", url)
	g("update-ref", "refs/remotes/origin/main", "HEAD")
	return dir, g
}

func TestTrustedBranch(t *testing.T) {
	const si = "https://github.com/si-mods/gorex.git"
	t.Run("own commits on si-mods", func(t *testing.T) {
		dir, g := trustRepo(t, si)
		g("commit", "-q", "--allow-empty", "-m", "mine")
		if ok, why := trustedBranch(dir); !ok {
			t.Errorf("untrusted: %s", why)
		}
	})
	t.Run("head on si-mods main", func(t *testing.T) {
		dir, _ := trustRepo(t, si)
		if ok, _ := trustedBranch(dir); !ok {
			t.Error("untrusted")
		}
	})
	t.Run("ssh remote", func(t *testing.T) {
		dir, _ := trustRepo(t, "git@github.com:si-mods/gorex.git")
		if ok, _ := trustedBranch(dir); !ok {
			t.Error("untrusted")
		}
	})
	t.Run("fork remote", func(t *testing.T) {
		for _, u := range []string{"https://github.com/someone/gorex.git", "https://github.com.evil.example/si-mods/gorex", "https://github.com/egoist/gorex", t.TempDir()} {
			dir, g := trustRepo(t, u)
			g("commit", "-q", "--allow-empty", "-m", "mine")
			if ok, _ := trustedBranch(dir); ok {
				t.Errorf("remote %q trusted", u)
			}
		}
	})
	t.Run("no remote", func(t *testing.T) {
		dir, g := trustRepo(t, si)
		g("remote", "remove", "origin")
		if ok, _ := trustedBranch(dir); ok {
			t.Error("trusted")
		}
	})
	t.Run("fetched commit not made here", func(t *testing.T) {
		// A fork's commit that reached the branch by fetch and checkout.
		dir, g := trustRepo(t, si)
		checkoutFork(t, dir, g)
		if ok, why := trustedBranch(dir); ok {
			t.Errorf("fork commit trusted (%s)", why)
		}
	})
	t.Run("own commit on top of a fork commit", func(t *testing.T) {
		dir, g := trustRepo(t, si)
		checkoutFork(t, dir, g)
		g("commit", "-q", "--allow-empty", "-m", "my fix")
		if ok, _ := trustedBranch(dir); ok {
			t.Error("trusted")
		}
	})
	t.Run("amend of a fork commit", func(t *testing.T) {
		dir, g := trustRepo(t, si)
		checkoutFork(t, dir, g)
		g("commit", "-q", "--amend", "--allow-empty", "-m", "looks mine")
		if ok, _ := trustedBranch(dir); ok {
			t.Error("amended fork commit trusted")
		}
	})
	t.Run("fork branch pushed to the si-mods remote", func(t *testing.T) {
		// The fork's commit now sits on refs/remotes/origin/pr.
		dir, g := trustRepo(t, si)
		checkoutFork(t, dir, g)
		g("update-ref", "refs/remotes/origin/pr", "HEAD")
		if ok, why := trustedBranch(dir); ok {
			t.Errorf("trusted (%s)", why)
		}
	})
	t.Run("config in the checkout is ignored", func(t *testing.T) {
		dir, g := trustRepo(t, "https://github.com/someone/gorex.git")
		g("commit", "-q", "--allow-empty", "-m", "mine")
		if err := os.WriteFile(filepath.Join(dir, ".verify-trusted"), []byte("si-mods"), 0o644); err != nil {
			t.Fatal(err)
		}
		if ok, _ := trustedBranch(dir); ok {
			t.Error("trusted")
		}
	})
}

func TestPlanReadOnly(t *testing.T) {
	files := []string{"internal/rex/a.go", "web/src/a.ts", "ios/App.swift", ".github/workflows/ci.yml"}
	for _, c := range plan(files, facts{goos: "darwin", webNodeModules: true, iosToolsPresent: true, readOnly: true}) {
		safe := c.name == "gofmt" || c.name == "actionlint" || strings.HasPrefix(c.name, "vet ") || strings.HasPrefix(c.name, "build ")
		if safe && c.skip != "" {
			t.Errorf("%s skipped in read-only mode: %s", c.name, c.skip)
		}
		if !safe && (c.skip == "" || !c.unverified) {
			t.Errorf("%s still runs in read-only mode", c.name)
		}
	}
}

func TestHookReadOnlyMode(t *testing.T) {
	dir, g := trustRepo(t, "https://github.com/someone/gorex.git")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module gorex\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package gorex\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g("add", "go.mod", "a.go")
	g("commit", "-q", "-m", "mod")
	g("update-ref", "refs/remotes/origin/main", "HEAD~1")
	setBlocks("test-ro", 0)
	t.Cleanup(func() { setBlocks("test-ro", 0) })
	in := `{"session_id":"test-ro","cwd":` + quote(dir) + `,"tool_name":"Bash","tool_input":{"command":"git push"}}`
	var errb, outb bytes.Buffer
	if code := hookMain(strings.NewReader(in), &errb, &outb); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(outb.String(), "deny") || !strings.Contains(errb.String(), "mode read-only") || !strings.Contains(outb.String(), "read-only") {
		t.Errorf("mode not reported: stderr %q stdout %q", errb.String(), outb.String())
	}
}

// checkoutFork fetches a commit made in another repo (a fork) on top of
// origin/main and checks it out as branch pr, like `gh pr checkout`.
func checkoutFork(t *testing.T, dir string, g func(args ...string) string) {
	t.Helper()
	fork := t.TempDir()
	for _, a := range [][]string{
		{"init", "-q", "-b", "main"},
		{"fetch", "-q", dir, "refs/remotes/origin/main"},
		{"checkout", "-q", "-b", "work", "FETCH_HEAD"},
		{"-c", "user.name=f", "-c", "user.email=f@example.com", "commit", "-q", "--allow-empty", "-m", "fork work"},
	} {
		if _, err := git(fork, a...); err != nil {
			t.Fatal(err)
		}
	}
	g("fetch", "-q", fork, "work")
	g("checkout", "-q", "-b", "pr", "FETCH_HEAD")
}

func TestGuardReason(t *testing.T) {
	t.Setenv("CI", "")
	deny := []string{
		`gh pr create --title x`,
		`gh pr merge 5 --squash`,
		`gh issue comment 3 --body hi`,
		`gh release create v1`,
		`gh pr create -R egoist/gorex`,
		`gh pr view 1 -R egoist/gorex`,
		`git push https://github.com/egoist/gorex main`,
		`git remote add up git@github.com:egoist/gorex.git`,
		`git push origin x && gh pr create --body "a; b"`,
		`go test ./...`,
		`go test ./internal/...`,
		`go test -v`,
		`go test .`,
		`go test -count=1 -timeout 5m ./...`,
		`cd x && go test ./internal/foo; go test ./...`,
		`FOO=1 go test`,
		`go run ./tools/verify -full`,
	}
	for _, c := range deny {
		if guardReason(c) == "" {
			t.Errorf("not denied: %s", c)
		}
	}
	allow := []string{
		`gh pr create -R si-mods/gorex --title x`,
		`gh pr create --title "a; b" --repo si-mods/gorex`,
		`gh pr merge 5 --repo=si-mods/gorex`,
		`gh pr view 5`,
		`gh pr list`,
		`gh pr checks 5`,
		`gh pr diff 5`,
		`gh run list`,
		`gh api repos/si-mods/gorex/pulls`,
		`git commit -m "doc: run go test ./... in CI, not egoist/gorex"`,
		`go test ./internal/foo`,
		`go test ./internal/foo -run X`,
		`go test . -run TestX`,
		`go test -run TestX`,
		`go test -count 1 ./internal/foo`,
		`go test ./... -run X`,
		`go vet ./...`,
		`go build ./...`,
		`go run ./tools/verify`,
		`GOREX_HEAVY_TESTS=1 go test ./...`,
		`ssh alice-mac "cd gorex && go test ./..."`,
		`ls`,
	}
	for _, c := range allow {
		if r := guardReason(c); r != "" {
			t.Errorf("denied %s: %s", c, r)
		}
	}
	t.Setenv("CI", "1")
	if r := guardReason(`go test ./...`); r != "" {
		t.Errorf("CI should allow: %s", r)
	}
	if guardReason(`gh pr create`) == "" {
		t.Error("CI must not lift the gh guard")
	}
}

func TestHookGuards(t *testing.T) {
	t.Setenv("CI", "")
	run := func(cwd, cmd string) string {
		sid := "test-guard"
		setBlocks(sid, 0)
		in := `{"session_id":"` + sid + `","cwd":` + quote(cwd) + `,"tool_name":"Bash","tool_input":{"command":` + strconv.Quote(cmd) + `}}`
		var outb bytes.Buffer
		if code := hookMain(strings.NewReader(in), &bytes.Buffer{}, &outb); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if blocks(sid) != 0 {
			t.Errorf("guard counted as a block: %s", cmd)
		}
		return outb.String()
	}
	gorex := repoDirForTest(t)
	for _, c := range []string{"gh pr create --title x", "go test ./...", "go run ./tools/verify -full"} {
		out := run(gorex, c)
		if !strings.Contains(out, `"permissionDecision":"deny"`) || strings.Contains(out, "tools/verify blocked") {
			t.Errorf("%s: %s", c, out)
		}
	}
	if out := run(gorex, "go test ./internal/foo -run X"); out != "" {
		t.Errorf("allowed command produced %s", out)
	}
	if out := run(t.TempDir(), "gh pr create && go test ./..."); out != "" {
		t.Errorf("non-GoRex cwd denied: %s", out)
	}
	t.Setenv("GOREX_VERIFY", "off")
	if out := run(gorex, "go test ./..."); out != "" {
		t.Errorf("GOREX_VERIFY=off denied: %s", out)
	}
}
