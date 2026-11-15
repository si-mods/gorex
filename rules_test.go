package main

import (
	"go/ast"
	"go/parser"
	"go/token"
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
