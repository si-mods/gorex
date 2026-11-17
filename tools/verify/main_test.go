package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
