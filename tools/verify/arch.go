package main

// Package dependency boundary enforcement across internal/**, ios/**,
// web/**, locales/** and tools/**.
//
// Reads Go import statements with go/parser (pure read-only, no code
// execution) and fails when a package imports across an architectural
// layer boundary.

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const modulePrefix = "gorex/"

// leafPackages have zero internal GoRex dependencies so any layer (desktop,
// server, CLI, web wasm, iOS gomobile) can import them without pulling in
// runtime state.
var leafPackages = map[string]bool{
	"internal/atomicfile":  true,
	"internal/duotone":     true,
	"internal/ext":         true,
	"internal/homedir":     true,
	"internal/layout":      true,
	"internal/pairaddr":    true,
	"internal/rexproto":    true,
	"internal/tailcatpool": true,
	"internal/tailnode":    true,
	"internal/testrex":     true,
	"internal/theme":       true,
	"locales":              true,
	"web/tailnet/tags":     true,
	"web/wsclient":         true,
}

// forbiddenImports maps a package to the gorex/ prefixes it must never import.
var forbiddenImports = map[string][]string{
	"internal/config": {
		"internal/rex",
		"internal/rexapi",
		"internal/cli",
		"internal/hosts",
		"internal/push",
		"internal/omarchy",
		"internal/iconpack",
		"internal/reximp",
		"internal/rexpeer",
		"ios/",
		"web/",
		"locales",
		"tools/",
	},
	"internal/hosts": {
		"internal/rex",
		"internal/rexapi",
		"internal/cli",
		"internal/config",
		"internal/push",
		"internal/omarchy",
		"internal/iconpack",
		"internal/reximp",
		"internal/rexpeer",
		"ios/",
		"web/",
		"locales",
		"tools/",
	},
	"internal/rex": {
		"internal/rexapi",
		"internal/cli",
		"internal/config",
		"internal/push",
		"internal/omarchy",
		"internal/iconpack",
		"internal/reximp",
		"internal/rexpeer",
		"ios/",
		"web/",
		"locales",
		"tools/",
	},
	"internal/iconpack": {
		"internal/rexapi",
		"internal/cli",
		"internal/push",
		"internal/omarchy",
		"internal/reximp",
		"internal/rexpeer",
		"ios/",
		"web/",
		"locales",
		"tools/",
	},
	"internal/push": {
		"internal/rexapi",
		"internal/cli",
		"internal/omarchy",
		"internal/iconpack",
		"internal/reximp",
		"internal/rexpeer",
		"ios/",
		"web/",
		"locales",
		"tools/",
	},
	"internal/omarchy": {
		"internal/rexapi",
		"internal/cli",
		"internal/push",
		"internal/iconpack",
		"internal/reximp",
		"internal/rexpeer",
		"ios/",
		"web/",
		"locales",
		"tools/",
	},
	"internal/rexpeer": {
		"internal/rex",
		"internal/rexapi",
		"internal/cli",
		"internal/reximp",
		"ios/",
		"web/",
		"locales",
		"tools/",
	},
	"internal/rexapi": {
		"internal/cli",
		"internal/omarchy",
		"internal/iconpack",
		"internal/reximp",
		"internal/rexpeer",
		"ios/",
		"web/",
		"locales",
		"tools/",
	},
	"internal/reximp": {
		"internal/rexapi",
		"internal/cli",
		"internal/omarchy",
		"internal/iconpack",
		"ios/",
		"web/",
		"locales",
		"tools/",
	},
	"internal/cli": {
		"ios/",
		"web/",
		"locales",
		"tools/",
	},
	"ios/tailnet": {
		"internal/rex",
		"internal/rexapi",
		"internal/cli",
		"internal/config",
		"internal/hosts",
		"internal/push",
		"internal/omarchy",
		"internal/iconpack",
		"internal/reximp",
		"internal/rexpeer",
		"web/",
		"locales",
		"tools/",
	},
	"ios/share": {
		"internal/",
		"web/",
		"locales",
		"tools/",
	},
	"web/jsbridge": {
		"internal/",
		"ios/",
		"locales",
		"tools/",
		"web/tailnet",
		"web/tailcat",
	},
	"web/tailcat": {
		"internal/rex",
		"internal/rexapi",
		"internal/cli",
		"internal/config",
		"internal/hosts",
		"internal/push",
		"internal/omarchy",
		"internal/iconpack",
		"internal/reximp",
		"internal/rexpeer",
		"ios/",
		"locales",
		"tools/",
		"web/tailnet",
	},
	"web/tailnet": {
		"internal/rex",
		"internal/rexapi",
		"internal/cli",
		"internal/config",
		"internal/hosts",
		"internal/push",
		"internal/omarchy",
		"internal/iconpack",
		"internal/reximp",
		"internal/rexpeer",
		"ios/",
		"locales",
		"tools/",
		"web/tailcat",
	},
	"tools/verify": {
		"internal/",
		"ios/",
		"web/",
		"locales",
		"tools/",
	},
	"tools/genbrands": {
		"internal/",
		"ios/",
		"web/",
		"locales",
		"tools/",
	},
	"tools/mkicon": {
		"internal/",
		"ios/",
		"web/",
		"locales",
		"tools/",
	},
	"tools/cistats": {
		"internal/",
		"ios/",
		"web/",
		"locales",
		"tools/",
	},
	"tools/remotectl": {
		"internal/rexapi",
		"internal/cli",
		"ios/",
		"web/",
		"locales",
	},
	"tools/ciplan": {
		"internal/rexapi",
		"internal/cli",
		"ios/",
		"web/",
		"locales",
	},
}

// archScannedDirs are the top-level trees whose Go packages archBoundariesCheck inspects.
var archScannedDirs = []string{"internal", "ios", "web", "locales", "tools"}

// archBoundariesCheck parses non-test Go files under archScannedDirs and
// verifies that no package violates the layer dependency rules.
func archBoundariesCheck(root string, log io.Writer) error {
	var violations []string
	unclassified := map[string]bool{}
	fset := token.NewFileSet()

	for _, top := range archScannedDirs {
		base := filepath.Join(root, top)
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				name := d.Name()
				if name == "testdata" || name == "Frameworks" || name == "node_modules" || name == "dist" || strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			pkg := filepath.ToSlash(filepath.Dir(rel))
			if _, ok := forbiddenImports[pkg]; !ok && !leafPackages[pkg] && !unclassified[pkg] {
				unclassified[pkg] = true
				violations = append(violations, fmt.Sprintf("%s: package %s must be classified in leafPackages or forbiddenImports in tools/verify/arch.go", rel, pkg))
			}

			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return fmt.Errorf("parse %s: %w", rel, err)
			}
			for _, spec := range f.Imports {
				imp, err := strconv.Unquote(spec.Path.Value)
				if err != nil || !strings.HasPrefix(imp, modulePrefix) {
					continue
				}
				target := strings.TrimPrefix(imp, modulePrefix)
				if target == pkg {
					// go:generate scripts (//go:build ignore) inside a package may import their own package.
					continue
				}
				if reason := checkImportRule(pkg, target); reason != "" {
					violations = append(violations, fmt.Sprintf("%s: %s imports %s (%s)", rel, pkg, target, reason))
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		msg := "architecture boundary violations:\n  " + strings.Join(violations, "\n  ") + "\n"
		fmt.Fprint(log, msg)
		return fmt.Errorf("%d architecture boundary violation(s)", len(violations))
	}
	return nil
}

func checkImportRule(pkg, target string) string {
	if target == "internal/testrex" {
		return "non-test code must not import internal/testrex"
	}
	if leafPackages[pkg] {
		return "leaf package must not import other gorex packages"
	}
	for _, banned := range forbiddenImports[pkg] {
		if strings.HasSuffix(banned, "/") {
			if strings.HasPrefix(target, banned) {
				return "forbidden dependency on " + banned + "*"
			}
		} else if target == banned || strings.HasPrefix(target, banned+"/") {
			return "forbidden dependency on " + banned
		}
	}
	return ""
}
