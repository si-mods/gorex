package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoNewRootInit enforces TECH_DEBT.md #8: package main must not add new
// package-level init() functions beyond the existing baseline.
func TestNoNewRootInit(t *testing.T) {
	allowed := map[string]bool{
		"commands.go":      true,
		"settingsview.go":  true,
		"shortcutsview.go": true,
		"workspace.go":     true,
		"workspaceview.go": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if allowed[name] {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && fn.Name.Name == "init" {
				t.Errorf("%s: new package-level init() is forbidden in package main (see TECH_DEBT.md #8)", name)
			}
		}
	}
}

// TestNoInternalInit forbids new package-level init() functions in internal/*
// production code beyond the existing baseline so initialization stays explicit.
func TestNoInternalInit(t *testing.T) {
	allowed := map[string]bool{
		"internal/rex/ownrelay.go":     true,
		"internal/rex/voice.go":        true,
		"internal/rexapi/help.go":      true,
		"internal/rexapi/revisions.go": true,
	}
	fset := token.NewFileSet()
	err := filepath.WalkDir("internal", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(path)
		if d.IsDir() || !strings.HasSuffix(slash, ".go") || strings.HasSuffix(slash, "_test.go") || allowed[slash] {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && fn.Name.Name == "init" {
				t.Errorf("%s: package-level init() is forbidden in internal/", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestNoNewRootPanic forbids adding new panic() calls in package main
// production files beyond the existing startup/invariant baseline.
func TestNoNewRootPanic(t *testing.T) {
	allowed := map[string]bool{
		"commands.go": true,
		"host.go":     true,
		"note.go":     true,
		"programs.go": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || allowed[name] {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "panic" {
				pos := fset.Position(call.Pos())
				t.Errorf("%s:%d: new panic() is forbidden in package main; return or log errors instead", name, pos.Line)
			}
			return true
		})
	}
}

// TestWorkflowsTargetFork enforces CLAUDE.md: CI/release workflows must not
// reference upstream egoist/gorex.
func TestWorkflowsTargetFork(t *testing.T) {
	files, err := filepath.Glob(".github/workflows/*.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "egoist/gorex") {
			t.Errorf("%s: must not reference upstream egoist/gorex", path)
		}
	}
}
