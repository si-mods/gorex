package main

import (
	"errors"
	"go/build"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const module = "gorex"

// goosList are the systems CI vets; linux and windows also run tests.
var goosList = []string{"linux", "windows", "darwin"}

// archs are CI's build architectures. A package's files and imports on a GOOS
// are the union over them.
var archs = []string{"amd64", "arm64"}

// pkg is one module package as one GOOS builds it (CGO_ENABLED=0).
type pkg struct {
	Path        string          // import path
	Dir         string          // repo-relative, slash-separated; "." for the root
	Files       map[string]bool // repo-relative .go files built on this GOOS, tests included
	Imports     []string        // module packages its non-test files import
	TestImports []string        // module packages its _test.go files import (both test packages)
	Embeds      []embed         // its //go:embed patterns
}

// embed is a //go:embed pattern, repo-relative.
type embed struct {
	pattern string
	test    bool // in a _test.go file
}

// graph is the module's packages on each GOOS.
type graph struct {
	pkgs   map[string]map[string]*pkg // GOOS → import path → package
	exists func(path string) bool     // whether a repo-relative file is in the checkout
}

// load reads the package graph of the module at root with go/build, as
// `go list ./...` would see it on each GOOS, but from the import lines alone:
// it resolves no import, so it needs no module download and runs before the
// Go caches are restored.
func load(root string) (graph, error) {
	g := graph{pkgs: map[string]map[string]*pkg{}, exists: existsIn(root)}
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		// The go command's ./... skips these; node_modules holds no package of ours.
		if p != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "node_modules") {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		dirs = append(dirs, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return g, err
	}
	for _, goos := range goosList {
		pkgs := map[string]*pkg{}
		for _, dir := range dirs {
			p, err := loadDir(root, dir, goos)
			if err != nil {
				return g, err
			}
			if p != nil {
				pkgs[p.Path] = p
			}
		}
		g.pkgs[goos] = pkgs
	}
	return g, nil
}

// loadDir reads one directory for goos. It returns nil for a directory with
// no .go file; a package with no file for goos has empty Files.
func loadDir(root, dir, goos string) (*pkg, error) {
	p := &pkg{Path: importPath(dir), Dir: dir, Files: map[string]bool{}}
	any := false
	imports, testImports := map[string]bool{}, map[string]bool{}
	embeds := map[embed]bool{}
	for _, arch := range archs {
		ctxt := build.Default
		ctxt.GOOS, ctxt.GOARCH, ctxt.CgoEnabled = goos, arch, false
		ctxt.Dir = root
		bp, err := ctxt.ImportDir(filepath.Join(root, filepath.FromSlash(dir)), build.ImportComment)
		var noGo *build.NoGoError
		var multi *build.MultiplePackageError
		switch {
		case errors.As(err, &noGo):
			if len(bp.IgnoredGoFiles) == 0 && len(bp.InvalidGoFiles) == 0 {
				continue // no .go file at all: not a package
			}
		case errors.As(err, &multi):
			// A //go:build ignore generator beside the package (package main).
		case err != nil && bp == nil:
			return nil, err
		}
		any = true
		for _, list := range [][]string{bp.GoFiles, bp.TestGoFiles, bp.XTestGoFiles} {
			for _, f := range list {
				p.Files[path.Join(dir, f)] = true
			}
		}
		for _, ip := range bp.Imports {
			imports[ip] = true
		}
		for _, ip := range append(slices.Clone(bp.TestImports), bp.XTestImports...) {
			testImports[ip] = true
		}
		for _, pat := range bp.EmbedPatterns {
			embeds[embed{pattern: path.Join(dir, strings.TrimPrefix(pat, "all:"))}] = true
		}
		for _, pat := range append(slices.Clone(bp.TestEmbedPatterns), bp.XTestEmbedPatterns...) {
			embeds[embed{pattern: path.Join(dir, strings.TrimPrefix(pat, "all:")), test: true}] = true
		}
	}
	if !any {
		return nil, nil
	}
	p.Imports = moduleOnly(imports)
	p.TestImports = moduleOnly(testImports)
	for e := range embeds {
		p.Embeds = append(p.Embeds, e)
	}
	slices.SortFunc(p.Embeds, func(a, b embed) int { return strings.Compare(a.pattern, b.pattern) })
	return p, nil
}

func moduleOnly(set map[string]bool) []string {
	var out []string
	for ip := range set {
		if ip == module || strings.HasPrefix(ip, module+"/") {
			out = append(out, ip)
		}
	}
	slices.Sort(out)
	return out
}

func importPath(dir string) string {
	if dir == "." || dir == "" {
		return module
	}
	return module + "/" + dir
}
