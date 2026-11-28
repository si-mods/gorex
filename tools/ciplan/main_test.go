package main

import (
	"encoding/json"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeGraph: the root imports a, a imports b, c's tests import b, d embeds
// its testdata, the root embeds assets/icons; b has a Windows-only file.
// rex and rexapi stand in for the real pair that shares testdata.
func fakeGraph(gone ...string) graph {
	g := graph{pkgs: map[string]map[string]*pkg{}, exists: func(f string) bool { return !slices.Contains(gone, f) }}
	for _, goos := range goosList {
		mk := func(dir string, files []string, imports, testImports []string, embeds ...embed) *pkg {
			p := &pkg{Path: importPath(dir), Dir: dir, Files: map[string]bool{}, Imports: imports, TestImports: testImports, Embeds: embeds}
			for _, f := range files {
				p.Files[path.Join(dir, f)] = true
			}
			return p
		}
		bFiles := []string{"b.go", "b_test.go"}
		if goos == "windows" {
			bFiles = append(bFiles, "b_windows.go")
		}
		pkgs := []*pkg{
			mk(".", []string{"main.go", "main_test.go"}, []string{"gorex/internal/a"}, nil, embed{pattern: "assets/icons/*.svg"}),
			mk("internal/a", []string{"a.go"}, []string{"gorex/internal/b"}, nil),
			mk("internal/b", bFiles, nil, nil),
			mk("internal/c", []string{"c.go", "c_test.go"}, nil, []string{"gorex/internal/b"}),
			mk("internal/d", []string{"d.go", "d_test.go"}, nil, nil, embed{pattern: "internal/d/testdata/x.json", test: true}),
			mk("internal/rex", []string{"rex.go"}, nil, nil),
			mk("internal/rexapi", []string{"rexapi.go"}, nil, nil),
		}
		g.pkgs[goos] = map[string]*pkg{}
		for _, p := range pkgs {
			g.pkgs[goos][p.Path] = p
		}
	}
	return g
}

func outputs(r result) map[string]string {
	m := map[string]string{}
	for _, l := range r.lines() {
		k, v, _ := strings.Cut(l, "=")
		m[k] = v
	}
	return m
}

func TestPlan(t *testing.T) {
	none := map[string]string{
		"all": "false", "go": "false", "gofmt_files": "", "vet_linux": "", "vet_windows": "", "vet_darwin": "",
		"lint": "", "build": "false", "test_linux": "", "root": "false", "root_run": "", "test_windows": "",
		"web": "false", "web_r2": "false",
	}
	all := map[string]string{
		"all": "true", "go": "true", "gofmt_files": "", "vet_linux": "./...", "vet_windows": "./...", "vet_darwin": "./...",
		"lint": "./...", "build": "true",
		"test_linux": "./internal/a ./internal/b ./internal/c ./internal/d ./internal/rex ./internal/rexapi",
		"root":       "true", "root_run": "",
		"test_windows": "./internal/a ./internal/b ./internal/c ./internal/d ./internal/rex ./internal/rexapi",
		"web":          "true", "web_r2": "true",
	}
	with := func(base map[string]string, kv ...string) map[string]string {
		m := maps.Clone(base)
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	for _, c := range []struct {
		name  string
		files []string
		gone  []string
		want  map[string]string
	}{
		{"empty diff", nil, nil, all},
		{"docs only", []string{"docs/DOC-001-design-docs.md", "README.md", "ios/WhatToTest.md"}, nil, none},
		{"ios swift only", []string{"ios/GoRex/App.swift", "ios/project.yml"}, nil,
			with(none, "go", "true", "root_run", "^(TestNoExcuseComments)$")},
		{"workflow other than ci.yml", []string{".github/workflows/release.yml"}, nil,
			with(none, "go", "true", "root_run", "^(TestWorkflowsTargetFork)$")},
		{"web source", []string{"web/src/x.ts"}, nil,
			with(none, "go", "true", "root_run", "^(TestNoExcuseComments)$", "web", "true", "web_r2", "true")},
		{"leaf go file", []string{"internal/b/b.go"}, nil,
			with(none, "go", "true", "gofmt_files", "internal/b/b.go", "build", "true",
				"vet_linux", "./internal/b", "vet_windows", "./internal/b", "vet_darwin", "./internal/b", "lint", "./internal/b",
				"test_linux", "./internal/a ./internal/b ./internal/c", "root", "true",
				"test_windows", "./internal/a ./internal/b ./internal/c")},
		{"deleted go file", []string{"internal/a/old.go"}, []string{"internal/a/old.go"},
			with(none, "go", "true", "build", "true",
				"vet_linux", "./internal/a", "vet_windows", "./internal/a", "vet_darwin", "./internal/a", "lint", "./internal/a",
				"test_linux", "./internal/a", "root", "true", "test_windows", "./internal/a")},
		{"windows-only go file", []string{"internal/b/b_windows.go"}, nil,
			with(none, "go", "true", "gofmt_files", "internal/b/b_windows.go", "build", "true",
				"vet_windows", "./internal/b", "root_run", "^(TestNoExcuseComments|TestNoInternalInit)$",
				"test_windows", "./internal/a ./internal/b ./internal/c")},
		{"test-only importer", []string{"internal/c/c_test.go"}, nil,
			with(none, "go", "true", "gofmt_files", "internal/c/c_test.go", "build", "true",
				"vet_linux", "./internal/c", "vet_windows", "./internal/c", "vet_darwin", "./internal/c", "lint", "./internal/c",
				"test_linux", "./internal/c", "root_run", "^(TestNoExcuseComments)$", "test_windows", "./internal/c")},
		{"testdata file", []string{"internal/c/testdata/in.txt"}, nil,
			with(none, "go", "true", "test_linux", "./internal/c", "test_windows", "./internal/c")},
		{"embedded test file", []string{"internal/d/testdata/x.json"}, nil,
			with(none, "go", "true", "test_linux", "./internal/d", "test_windows", "./internal/d")},
		{"rexapi testdata", []string{"internal/rexapi/testdata/rex-terminal-stream.bin"}, nil,
			with(none, "go", "true", "test_linux", "./internal/rex ./internal/rexapi", "test_windows", "./internal/rex ./internal/rexapi")},
		{"embedded asset", []string{"assets/icons/new.svg"}, nil,
			with(none, "go", "true", "build", "true", "root", "true", "web_r2", "true")},
		{"ts omit tags", []string{"packaging/ts-omit-tags.txt"}, nil,
			with(none, "go", "true", "build", "true")},
		{"root-level file", []string{"mygo.json"}, nil, with(none, "go", "true", "root", "true")},
		{"go.mod", []string{"go.mod"}, nil, all},
		{"ci.yml", []string{"docs/x.md", ".github/workflows/ci.yml"}, nil, all},
		{"ciplan", []string{"tools/ciplan/plan.go"}, nil, all},
		{"lint config", []string{".golangci.yml"}, nil, all},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := outputs(plan(c.files, fakeGraph(c.gone...)))
			for k, want := range c.want {
				if got[k] != want {
					t.Errorf("%s = %q, want %q", k, got[k], want)
				}
			}
		})
	}
}

func TestNeedAll(t *testing.T) {
	for _, c := range []struct {
		event, base string
		ancestor    bool
		all         bool
	}{
		{"pull_request", "abc123", true, false},
		{"push", "abc123", true, false},
		{"workflow_dispatch", "abc123", true, true},
		{"push", "0000000000000000000000000000000000000000", false, true},
		{"push", "", false, true},
		{"push", "abc123", false, true}, // a force-push: base is not an ancestor
	} {
		if got := needAll(c.event, c.base, c.ancestor) != ""; got != c.all {
			t.Errorf("needAll(%q, %q, %v) = %v, want %v", c.event, c.base, c.ancestor, got, c.all)
		}
	}
}

func repoGraph(t *testing.T) graph {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	g, err := load(root)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// TestRepoClosure checks the real graph: a leaf the root imports reaches
// the root's tests.
func TestRepoClosure(t *testing.T) {
	g := repoGraph(t)
	files, _ := filepath.Glob(filepath.Join("..", "..", "internal", "atomicfile", "*.go"))
	if len(files) == 0 {
		t.Fatal("no internal/atomicfile/*.go")
	}
	f := "internal/atomicfile/" + filepath.Base(files[0])
	r := plan([]string{f}, g)
	if !r.root {
		t.Errorf("a change to %s does not run the root package's tests", f)
	}
	if !slices.Contains(r.test["linux"], "gorex/internal/atomicfile") {
		t.Errorf("a change to %s does not test internal/atomicfile: %v", f, r.test["linux"])
	}
}

// TestGraphMatchesGoList checks load against go list, which resolves what
// go/build only reads: the same packages, files and module imports per GOOS.
func TestGraphMatchesGoList(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list three times")
	}
	g := repoGraph(t)
	for _, goos := range goosList {
		cmd := exec.Command("go", "list", "-e", "-json", "./...")
		cmd.Dir = filepath.Join("..", "..")
		cmd.Env = append(os.Environ(), "GOOS="+goos, "CGO_ENABLED=0", "GOFLAGS=")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list (%s): %v", goos, err)
		}
		dec := json.NewDecoder(strings.NewReader(string(out)))
		seen := map[string]bool{}
		for {
			var lp struct {
				ImportPath, Dir                    string
				GoFiles, TestGoFiles, XTestGoFiles []string
				Imports, TestImports, XTestImports []string
			}
			if err := dec.Decode(&lp); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal(err)
			}
			seen[lp.ImportPath] = true
			p := g.pkgs[goos][lp.ImportPath]
			if p == nil {
				t.Errorf("%s: load misses %s", goos, lp.ImportPath)
				continue
			}
			want := map[string]bool{}
			for _, f := range slices.Concat(lp.GoFiles, lp.TestGoFiles, lp.XTestGoFiles) {
				want[path.Join(p.Dir, f)] = true
			}
			// load also takes arm64's files; go list takes the host's arch.
			for f := range want {
				if !p.Files[f] {
					t.Errorf("%s: %s: load misses %s", goos, lp.ImportPath, f)
				}
			}
			if got, want := p.Imports, moduleOnly(set(lp.Imports)); !subset(want, got) {
				t.Errorf("%s: %s imports %v, go list %v", goos, lp.ImportPath, got, want)
			}
			if got, want := p.TestImports, moduleOnly(set(slices.Concat(lp.TestImports, lp.XTestImports))); !subset(want, got) {
				t.Errorf("%s: %s test imports %v, go list %v", goos, lp.ImportPath, got, want)
			}
		}
		for ip, p := range g.pkgs[goos] {
			// ./... leaves out a package with no file for GOOS (web/tailnet).
			if !seen[ip] && len(p.Files) > 0 {
				t.Errorf("%s: load has %s, go list does not", goos, ip)
			}
		}
	}
}

func set(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func subset(a, b []string) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}
