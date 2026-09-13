package app

import (
	"os"
	"path/filepath"
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
		}
	}
}
