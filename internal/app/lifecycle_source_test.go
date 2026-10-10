package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRuntimePathsDoNotSpecialCaseInvestmentID(t *testing.T) {
	needles := []string{`id == "investment"`, `ModuleID("investment")`, `Gate("investment"`}
	roots := []string{".", filepath.Join("..", "foundation", "modules")}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(root, entry.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			source := string(raw)
			for _, needle := range needles {
				if strings.Contains(source, needle) {
					t.Errorf("%s contains investment ID special case %q", path, needle)
				}
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, raw, 0)
			if err != nil {
				t.Fatal(err)
			}
			businessImports := make(map[string]bool)
			for _, spec := range file.Imports {
				importPath, _ := strconv.Unquote(spec.Path.Value)
				if strings.HasPrefix(importPath, "workbench/internal/modules/") {
					name := filepath.Base(importPath)
					if spec.Name != nil {
						name = spec.Name.Name
					}
					businessImports[name] = true
				}
			}
			for _, decl := range file.Decls {
				if function, ok := decl.(*ast.FuncDecl); ok && function.Name.Name == "New" && function.Recv == nil {
					continue
				}
				ast.Inspect(decl, func(node ast.Node) bool {
					if selector, ok := node.(*ast.SelectorExpr); ok {
						if pkg, ok := selector.X.(*ast.Ident); ok && businessImports[pkg.Name] {
							t.Errorf("%s uses business implementation %s.%s outside construction", path, pkg.Name, selector.Sel.Name)
						}
					}
					return true
				})
			}
		}
	}
}
