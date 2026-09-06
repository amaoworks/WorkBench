package modules_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Production business code may depend on its own implementation, contracts and public foundation helpers.
func TestBusinessModulesDoNotImportOtherBusinessOrCapabilityImplementations(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(path), "/")
		if len(parts) < 2 {
			return nil
		}
		module := parts[0]
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imported, _ := strconv.Unquote(spec.Path.Value)
			if strings.HasPrefix(imported, "workbench/internal/modules/") && !strings.HasPrefix(imported, "workbench/internal/modules/"+module+"/") {
				t.Errorf("%s imports another business: %s", path, imported)
			}
			if strings.HasPrefix(imported, "workbench/internal/capabilities/") || strings.HasPrefix(imported, "workbench/internal/app") {
				t.Errorf("%s imports implementation instead of injected contract: %s", path, imported)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
