package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestNoNewRootInit forbids package-level init() functions in package main so
// initialization stays explicit and cycle-free.
func TestNoNewRootInit(t *testing.T) {
	allowed := map[string]bool{}
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
				t.Errorf("%s: new package-level init() is forbidden in package main", name)
			}
		}
	}
}

// TestNoInternalInit forbids new package-level init() functions in internal/*
// production code beyond the existing baseline so initialization stays explicit.
func TestNoInternalInit(t *testing.T) {
	allowed := map[string]bool{
		"internal/rex/ownrelay.go": true,
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

// TestNoNewRootPanic forbids panic() calls in package main production files so
// runtime paths log and recover gracefully instead of crashing the app.
func TestNoNewRootPanic(t *testing.T) {
	allowed := map[string]bool{}
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

var excuseCommentRe = regexp.MustCompile(`(?i)(//\s*nolint\b|@ts-(?:ignore|expect-error|nocheck)\b|eslint-disable\b|\b(?:TODO|FIXME|XXX)\b|\b(?:workaround|hack)\b)`)

// TestNoExcuseComments forbids inline lint/type suppressions and shortcut
// excuse comments (TODO/FIXME/XXX/workaround/hack) across Go, TypeScript, and
// Swift sources so shortcuts are fixed rather than normalized.
func TestNoExcuseComments(t *testing.T) {
	skipDirs := map[string]bool{
		".git": true, ".claude": true, ".mygo": true,
		"build": true, "node_modules": true,
	}
	skipFiles := map[string]bool{
		"rules_test.go":          true,
		"locales/catalog_gen.go": true,
		"web/src/locales.gen.ts": true,
	}
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(path)
		if d.IsDir() {
			if skipDirs[d.Name()] || slash == "web/dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if skipFiles[slash] {
			return nil
		}
		switch filepath.Ext(slash) {
		case ".go":
			f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				return err
			}
			for _, cg := range f.Comments {
				for _, c := range cg.List {
					if m := excuseCommentRe.FindString(c.Text); m != "" {
						pos := fset.Position(c.Pos())
						t.Errorf("%s:%d: forbidden excuse/suppression comment %q", slash, pos.Line, m)
					}
				}
			}
		case ".ts", ".swift":
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				if !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "/*") && !strings.HasPrefix(trimmed, "*") {
					continue
				}
				if m := excuseCommentRe.FindString(trimmed); m != "" {
					t.Errorf("%s:%d: forbidden excuse/suppression comment %q", slash, i+1, m)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func validateIOSCatalog(path string, data []byte) ([]string, error) {
	var cat struct {
		SourceLanguage string `json:"sourceLanguage"`
		Strings        map[string]struct {
			ShouldTranslate *bool                      `json:"shouldTranslate"`
			Localizations   map[string]json.RawMessage `json:"localizations"`
		} `json:"strings"`
	}
	if err := json.Unmarshal(data, &cat); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON: %w", path, err)
	}
	var errs []string
	if cat.SourceLanguage != "en" {
		errs = append(errs, fmt.Sprintf("%s: sourceLanguage = %q, want \"en\"", path, cat.SourceLanguage))
	}
	if len(cat.Strings) == 0 {
		errs = append(errs, fmt.Sprintf("%s: empty strings catalog", path))
	}
	for key, entry := range cat.Strings {
		if strings.Contains(key, "...") {
			errs = append(errs, fmt.Sprintf("%s: key %q uses ASCII '...' instead of '…'", path, key))
		}
		if key != "" && (entry.ShouldTranslate == nil || *entry.ShouldTranslate) {
			if _, ok := entry.Localizations["zh-Hans"]; !ok {
				errs = append(errs, fmt.Sprintf("%s: key %q missing zh-Hans localization", path, key))
			}
		}
		for lang, raw := range entry.Localizations {
			if strings.Contains(string(raw), "...") {
				errs = append(errs, fmt.Sprintf("%s: key %q (%s) uses ASCII '...' instead of '…'", path, key, lang))
			}
		}
	}
	slices.Sort(errs)
	return errs, nil
}

// TestIOSCatalogIntegrity validates iOS string catalogs without requiring
// xcodebuild: valid JSON, English source language, zh-Hans parity for
// translatable entries, and typographical ellipsis ('…' instead of '...').
func TestIOSCatalogIntegrity(t *testing.T) {
	for _, path := range []string{
		"ios/GoRex/Localizable.xcstrings",
		"ios/GoRex/InfoPlist.xcstrings",
		"ios/GoRexWidgets/Localizable.xcstrings",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		errs, err := validateIOSCatalog(path, data)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range errs {
			t.Error(e)
		}
	}
}

func TestValidateIOSCatalogViolations(t *testing.T) {
	if _, err := validateIOSCatalog("bad.xcstrings", []byte("{not json")); err == nil {
		t.Fatal("expected invalid JSON error")
	}
	bad := []byte(`{
		"sourceLanguage": "fr",
		"strings": {
			"Copy...": {
				"localizations": {
					"zh-Hans": {"stringUnit": {"state": "translated", "value": "复制"}}
				}
			},
			"Untranslated": {},
			"BadZh": {
				"localizations": {
					"zh-Hans": {"stringUnit": {"state": "translated", "value": "等待中..."}}
				}
			},
			"BrandLiteral": {
				"shouldTranslate": false
			}
		}
	}`)
	errs, err := validateIOSCatalog("test.xcstrings", bad)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(errs, "\n")
	for _, want := range []string{
		`sourceLanguage = "fr", want "en"`,
		`key "Copy..." uses ASCII '...'`,
		`key "Untranslated" missing zh-Hans localization`,
		`key "BadZh" (zh-Hans) uses ASCII '...'`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing expected violation %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "BrandLiteral") {
		t.Errorf("shouldTranslate=false entry must not require zh-Hans:\n%s", got)
	}
}
