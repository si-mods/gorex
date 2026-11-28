package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// glassSurfaces are the calls that give a panel its glass (DESIGN.md,
// Glass): what is drawn in the function that calls one is drawn on glass.
// menuStyleOf is the menus' and the pickers' way to their glass panel.
var glassSurfaces = map[string]bool{"glassPanel": true, "frostPanel": true, "panelMaterial": true, "menuStyleOf": true, "dialogPanel": true}

// drawsWithColors tells that fn takes GoRex's colors, as what draws does:
// the calls TestGlassInk follows from a glass panel, and not the commands
// or the state a row's click runs.
func drawsWithColors(fn *ast.FuncDecl) bool {
	for _, f := range fn.Type.Params.List {
		if st, ok := f.Type.(*ast.StarExpr); ok {
			if id, ok := st.X.(*ast.Ident); ok && id.Name == "colors" {
				return true
			}
		}
	}
	return false
}

// TestGlassInk checks DESIGN.md's rule that text on a panel's glass reads
// whatever is behind it: no k.textFaint in what a glass panel draws. What
// it draws is the innermost function (a closure, as a popover's or a
// dialog's, or a declaration) that calls glassPanel, frostPanel or
// panelMaterial, and the functions of this package it calls that take *colors,
// followed through their calls in turn. A method is known by its name alone, so a
// name two types share is followed for both.
func TestGlassInk(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	decls := map[string][]*ast.FuncDecl{}
	pkgs := map[*ast.FuncDecl]map[string]bool{}
	var scopes []ast.Node
	scopePkgs := map[ast.Node]map[string]bool{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		imported := map[string]bool{}
		for _, im := range f.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			name := p[strings.LastIndex(p, "/")+1:]
			if im.Name != nil {
				name = im.Name.Name
			}
			imported[name] = true
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			decls[fn.Name.Name] = append(decls[fn.Name.Name], fn)
			pkgs[fn] = imported
			// The innermost function around each call of a glass surface.
			var stack []ast.Node
			ast.Inspect(fn, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				if call, ok := n.(*ast.CallExpr); ok {
					if id, ok := call.Fun.(*ast.Ident); ok && glassSurfaces[id.Name] {
						for i := len(stack) - 1; i >= 0; i-- {
							if _, ok := stack[i].(*ast.FuncLit); ok || i == 0 {
								if !glassSurfaces[fn.Name.Name] {
									scopes = append(scopes, stack[i])
									scopePkgs[stack[i]] = imported
								}
								break
							}
						}
					}
				}
				return true
			})
		}
	}
	// reach walks n: it reports k.textFaint, and walks the package's
	// functions n calls.
	seen := map[ast.Node]bool{}
	found := map[string]bool{}
	var reach func(n ast.Node, imported map[string]bool, from string)
	reach = func(n ast.Node, imported map[string]bool, from string) {
		if seen[n] {
			return
		}
		seen[n] = true
		ast.Inspect(n, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.SelectorExpr:
				if v.Sel.Name == "textFaint" {
					found[fset.Position(v.Pos()).String()+": k.textFaint on glass (from "+from+")"] = true
				}
			case *ast.CallExpr:
				var name string
				switch fun := v.Fun.(type) {
				case *ast.Ident:
					name = fun.Name
				case *ast.SelectorExpr:
					if id, ok := fun.X.(*ast.Ident); ok && imported[id.Name] {
						return true
					}
					name = fun.Sel.Name
				}
				for _, d := range decls[name] {
					if drawsWithColors(d) {
						reach(d, pkgs[d], from)
					}
				}
			}
			return true
		})
	}
	for _, s := range scopes {
		reach(s, scopePkgs[s], fset.Position(s.Pos()).String())
	}
	var out []string
	for s := range found {
		out = append(out, s)
	}
	sort.Strings(out)
	for _, s := range out {
		t.Error(s)
	}
}
