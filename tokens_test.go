package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestDesignTokens checks DESIGN.md's layers in the root package's source:
// a type size, a radius or a shadow is a token from tokens.go, and spacing
// is a literal on the 2px grid (0, 2, 4, 6, 8, 10, 12, 14, 16, 20, 24, 32).
// Terminal-relative sizes and row heights keep their own rules.
func TestDesignTokens(t *testing.T) {
	grid := map[float64]bool{
		0: true, 2: true, 4: true, 6: true, 8: true, 10: true,
		12: true, 14: true, 16: true, 20: true, 24: true, 32: true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		// The pane's own shadows and insets, part of its drawing, are not
		// tokens: DESIGN.md, Spacing and Shadows, "Not on panes". The
		// insets size the terminal's grid.
		exempt := map[string]bool{"paneCard": true, "floatCell": true}
		var enclosing string
		ast.Inspect(f, func(n ast.Node) bool {
			if fn, ok := n.(*ast.FuncDecl); ok {
				enclosing = fn.Name.Name
				return true
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pos := fset.Position(call.Pos())
			at := func() string { return pos.String() }
			var num func(ast.Expr) (float64, bool)
			num = func(e ast.Expr) (float64, bool) {
				switch v := e.(type) {
				case *ast.BasicLit:
					if v.Kind == token.INT || v.Kind == token.FLOAT {
						x, err := strconv.ParseFloat(v.Value, 64)
						return x, err == nil
					}
				case *ast.UnaryExpr:
					if v.Op == token.SUB {
						if x, ok := num(v.X); ok {
							return -x, true
						}
					}
				}
				return 0, false
			}
			switch name := sel.Sel.Name; {
			case name == "FontSize":
				if x, ok := num(call.Args[0]); ok && x != 0 {
					t.Errorf("%s: FontSize(%v) is a literal, not a token from tokens.go", at(), x)
				}
			case name == "Radius":
				if x, ok := num(call.Args[0]); ok {
					t.Errorf("%s: Radius(%v) is a literal, not a token from tokens.go", at(), x)
				}
			case name == "Gap" || strings.HasPrefix(name, "Padding") || strings.HasPrefix(name, "Margin"):
				for _, a := range call.Args {
					if x, ok := num(a); ok && !grid[math.Abs(x)] && !exempt[enclosing] {
						t.Errorf("%s: %s(%v) is off the spacing grid", at(), name, x)
					}
				}
			case name == "Shadow":
				if path == "tokens.go" || path == "glass.go" || exempt[enclosing] {
					break
				}
				if len(call.Args) >= 4 {
					lits := true
					for _, a := range call.Args[:4] {
						if _, ok := num(a); !ok {
							lits = false
						}
					}
					if lits {
						t.Errorf("%s: Shadow's geometry is literal, not a token from tokens.go", at())
					}
				}
			}
			return true
		})
	}
}
