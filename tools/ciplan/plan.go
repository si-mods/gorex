package main

import (
	"path"
	"slices"
	"strings"
)

// forceAll are the files a change to which runs everything: they reach every
// package (go.mod, go.sum), every lint (.golangci*), or the plan itself.
func forceAll(f string) bool {
	return f == "go.mod" || f == "go.sum" || strings.HasPrefix(f, ".golangci") ||
		f == ".github/workflows/ci.yml" || strings.HasPrefix(f, "tools/ciplan/")
}

// readsElsewhere are the files a package's tests, embeds or generators read
// outside the package's own directory, which the package graph cannot show.
// A path ending in "/" is a prefix. Add a test or generator that reads a repo
// path outside its package here (docs/lessons.md, "A test that reads a file
// outside its package"); otherwise CI skips it when only that file changes.
var readsElsewhere = []struct {
	path  string
	pkgs  []string
	build bool // the cross-build reads it too
}{
	// internal/rex/ghostty_test.go reads rex-terminal-stream.bin.
	{"internal/rexapi/testdata/", []string{"gorex/internal/rex"}, false},
	// The root embeds resources/icon.png; internal/rex/status_windows_test.go
	// reads resources/<platform>.
	{"resources/", []string{"gorex", "gorex/internal/rex"}, false},
	// internal/tailnode/ts_omit_tags_test.go reads both; CI's build step reads
	// the tags.
	{"packaging/ts-omit-tags.txt", []string{"gorex/internal/tailnode"}, true},
	{"ios/scripts/build-tailnet", []string{"gorex/internal/tailnode"}, false},
	// The root embeds assets/icons, assets/brands and assets/fonts;
	// program_icon_test.go reads assets/brands-extra.
	{"assets/", []string{"gorex"}, false},
	// i18n_test.go reads the catalogs (the locales package embeds them).
	{"locales/", []string{"gorex"}, false},
	// tools/genbrands' test checks its outputs against assets/brands.json.
	{"assets/brands.json", []string{"gorex/tools/genbrands"}, false},
	{"programs_brands.go", []string{"gorex/tools/genbrands"}, false},
	{"web/src/programs_brands.ts", []string{"gorex/tools/genbrands"}, false},
	{"ios/Shared/ProgramBrands.swift", []string{"gorex/tools/genbrands"}, false},
	// internal/rexproto's test checks its outputs against protocol.json.
	{"internal/layout/palette_gen.go", []string{"gorex/internal/rexproto"}, false},
	{"web/src/rex-protocol.gen.ts", []string{"gorex/internal/rexproto"}, false},
	{"ios/GoRex/Rex/RexProtocol.gen.swift", []string{"gorex/internal/rexproto"}, false},
}

// ruleTests are the root package's tests that read files across the repo.
// A change to such a file runs only these tests of the root package, when
// nothing else reaches it. TestNoNewRootInit, TestNoNewRootPanic and the
// tests that glob *.go read only the root's own files.
var ruleTests = []struct {
	name  string
	reads func(f string) bool
}{
	// Every .go, .ts and .swift file but its skipped directories.
	{"TestNoExcuseComments", func(f string) bool {
		switch path.Ext(f) {
		case ".go", ".ts", ".swift":
		default:
			return false
		}
		if strings.HasPrefix(f, "web/dist/") {
			return false
		}
		for _, part := range strings.Split(path.Dir(f), "/") {
			switch part {
			case ".git", ".claude", ".mygo", "build", "node_modules":
				return false
			}
		}
		return true
	}},
	// internal/'s non-test .go files.
	{"TestNoInternalInit", func(f string) bool {
		return strings.HasPrefix(f, "internal/") && strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, "_test.go")
	}},
	// .github/workflows/*.yml.
	{"TestWorkflowsTargetFork", func(f string) bool {
		return path.Dir(f) == ".github/workflows" && strings.HasSuffix(f, ".yml")
	}},
}

// result is what CI runs. Package lists are import paths; lines writes them
// as directories.
type result struct {
	all, goWork, build, root, web, webR2 bool
	rootRun                              string
	gofmt                                []string            // changed .go files that still exist
	vet                                  map[string][]string // GOOS → packages
	lint                                 []string
	test                                 map[string][]string // linux, windows → packages, the root not among them
}

// plan maps the changed files to what CI runs.
func plan(files []string, g graph) result {
	if len(files) == 0 {
		return everything(g)
	}
	for _, f := range files {
		if forceAll(f) {
			return everything(g)
		}
	}
	r := result{vet: map[string][]string{}, test: map[string][]string{}}
	dirs := map[string]bool{} // package directories on any GOOS
	for _, pkgs := range g.pkgs {
		for _, p := range pkgs {
			dirs[p.Dir] = true
		}
	}
	// seed: per GOOS, the packages a change reaches directly. goSeed: those
	// whose own .go files changed, which vet and lint read.
	seed, goSeed := map[string]map[string]bool{}, map[string]map[string]bool{}
	for _, goos := range goosList {
		seed[goos], goSeed[goos] = map[string]bool{}, map[string]bool{}
	}
	addAll := func(ip string) {
		for _, goos := range goosList {
			seed[goos][ip] = true
		}
	}
	// nearest adds the package of the nearest directory at or above dir,
	// stopping before the root.
	nearest := func(dir string) {
		for ; dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
			if dirs[dir] {
				addAll(importPath(dir))
				return
			}
		}
	}
	rules := map[string]bool{}
	for _, f := range files {
		f = strings.TrimPrefix(path.Clean(strings.ReplaceAll(f, "\\", "/")), "./")
		if strings.HasPrefix(f, "web/") && !strings.HasSuffix(f, ".go") {
			r.web = true
		}
		if strings.HasPrefix(f, "web/") || strings.HasPrefix(f, "assets/") {
			r.webR2 = true // .github/workflows/web.yml's paths
		}
		for _, t := range ruleTests {
			if t.reads(f) {
				rules[t.name] = true
			}
		}
		dir := path.Dir(f)
		if strings.HasSuffix(f, ".go") {
			r.build = true
			if g.exists(f) {
				r.gofmt = append(r.gofmt, f)
			}
			ip := importPath(dir)
			for _, goos := range fileGOOS(f) {
				seed[goos][ip] = true
				goSeed[goos][ip] = true
			}
			if !dirs[dir] {
				nearest(dir) // testdata, or a package being deleted
			}
			// tools/ciplan's tests check its graph against go list on the
			// repo, so any Go change runs them.
			addAll(module + "/tools/ciplan")
			// tools/verify's TestArchBoundariesRepo reads every non-test .go
			// file under internal/ and ios/.
			if (strings.HasPrefix(f, "internal/") || strings.HasPrefix(f, "ios/")) && !strings.HasSuffix(f, "_test.go") {
				addAll(module + "/tools/verify")
			}
		} else if !strings.HasSuffix(f, ".md") {
			nearest(dir)
		}
		for _, pkgs := range g.pkgs {
			for _, p := range pkgs {
				for _, e := range p.Embeds {
					if embeds(e.pattern, f) {
						addAll(p.Path)
						r.build = r.build || !e.test
					}
				}
			}
		}
		for _, x := range readsElsewhere {
			if f == x.path || strings.HasSuffix(x.path, "/") && strings.HasPrefix(f, x.path) {
				for _, ip := range x.pkgs {
					addAll(ip)
				}
				r.build = r.build || x.build
			}
		}
		if !strings.Contains(f, "/") && !strings.HasSuffix(f, ".md") {
			addAll(module) // mygo.json and the like
		}
	}

	for _, goos := range goosList {
		pkgs := g.pkgs[goos]
		// vet compiles each package with its tests on this GOOS, so it runs on
		// the packages whose .go files changed and on every package whose code
		// or tests import one of them: a change can break a darwin-only test
		// of an importer, which build (no tests) and the Linux tests miss.
		r.vet[goos] = affected(pkgs, goSeed[goos])
		if goos == "darwin" {
			continue // CI runs no test on darwin
		}
		for _, ip := range affected(pkgs, seed[goos]) {
			if ip == module {
				r.root = r.root || goos == "linux" // CI never runs the root's tests on Windows
				continue
			}
			r.test[goos] = append(r.test[goos], ip)
		}
	}
	// Some staticcheck checks read a callee in another package, so lint
	// covers the importers too, like vet.
	r.lint = r.vet["linux"]
	if !r.root && len(rules) > 0 {
		names := make([]string, 0, len(rules))
		for n := range rules {
			names = append(names, n)
		}
		slices.Sort(names)
		r.rootRun = "^(" + strings.Join(names, "|") + ")$"
	}
	r.goWork = r.build || r.root || r.rootRun != "" || len(r.test["linux"]) > 0 || len(r.test["windows"]) > 0 ||
		len(r.vet["linux"]) > 0 || len(r.vet["windows"]) > 0 || len(r.vet["darwin"]) > 0
	return r
}

// fileGOOS is the systems a change to the .go file f can affect. Only a
// GOOS file-name suffix (x_windows.go, x_darwin_test.go) fixes it: a
// //go:build line can be the very thing the change edits, and HEAD's graph
// no longer shows the system it dropped, so any other file counts for all.
func fileGOOS(f string) []string {
	name := strings.TrimSuffix(strings.TrimSuffix(path.Base(f), ".go"), "_test")
	parts := strings.Split(name, "_")
	for i := len(parts) - 1; i >= 1 && i >= len(parts)-2; i-- {
		if slices.Contains(goosList, parts[i]) {
			return []string{parts[i]}
		}
	}
	return goosList
}

// embeds reports whether the //go:embed pattern names f: f or a directory
// above it matches. It works for a file that was deleted too.
func embeds(pattern, f string) bool {
	for p := f; p != "." && p != "/" && p != ""; p = path.Dir(p) {
		if ok, _ := path.Match(pattern, p); ok {
			return true
		}
	}
	return false
}

// affected is the packages whose tests seed reaches: seed and every package
// that imports one of them, transitively, plus the packages whose tests
// import one of those. Sorted; only packages with files on this GOOS.
func affected(pkgs map[string]*pkg, seed map[string]bool) []string {
	importers := map[string][]string{}
	for _, p := range pkgs {
		for _, ip := range p.Imports {
			importers[ip] = append(importers[ip], p.Path)
		}
	}
	compiled := map[string]bool{}
	queue := make([]string, 0, len(seed))
	for ip := range seed {
		queue = append(queue, ip)
	}
	for len(queue) > 0 {
		ip := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if compiled[ip] {
			continue
		}
		compiled[ip] = true
		queue = append(queue, importers[ip]...)
	}
	var out []string
	for _, p := range pkgs {
		if len(p.Files) == 0 {
			continue
		}
		hit := compiled[p.Path]
		for _, ip := range p.TestImports {
			hit = hit || compiled[ip]
		}
		if hit {
			out = append(out, p.Path)
		}
	}
	slices.Sort(out)
	return out
}

// everything is the plan that runs all of CI.
func everything(g graph) result {
	r := result{all: true, goWork: true, build: true, root: true, web: true, webR2: true,
		vet: map[string][]string{}, test: map[string][]string{}}
	for _, goos := range []string{"linux", "windows"} {
		for ip, p := range g.pkgs[goos] {
			if ip != module && len(p.Files) > 0 {
				r.test[goos] = append(r.test[goos], ip)
			}
		}
		slices.Sort(r.test[goos])
	}
	return r
}

// lines are the outputs, as key=value. With all=true, gofmt_files is empty
// (CI formats every tracked file) and vet_* and lint are ./...
func (r result) lines() []string {
	list := func(ips []string) string {
		out := make([]string, len(ips))
		for i, ip := range ips {
			if ip == module {
				out[i] = "."
			} else {
				out[i] = "./" + strings.TrimPrefix(ip, module+"/")
			}
		}
		return strings.Join(out, " ")
	}
	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	vet := func(goos string) string {
		if r.all {
			return "./..."
		}
		return list(r.vet[goos])
	}
	lint := list(r.lint)
	if r.all {
		lint = "./..."
	}
	return []string{
		"all=" + b(r.all),
		"go=" + b(r.goWork),
		"gofmt_files=" + strings.Join(r.gofmt, " "),
		"vet_linux=" + vet("linux"),
		"vet_windows=" + vet("windows"),
		"vet_darwin=" + vet("darwin"),
		"lint=" + lint,
		"build=" + b(r.build),
		"test_linux=" + list(r.test["linux"]),
		"root=" + b(r.root),
		"root_run=" + r.rootRun,
		"test_windows=" + list(r.test["windows"]),
		"web=" + b(r.web),
		"web_r2=" + b(r.webR2),
	}
}
