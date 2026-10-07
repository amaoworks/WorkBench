package modules_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Business tables use "<module_id>_" prefix (see doc/business-development.md and
// doc/data-model.md). Migrations and sqlc query SQL under each module must stay
// within that prefix; platform tables live in foundation and are out of scope.
var (
	createTableRE = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?["']?([a-zA-Z_][a-zA-Z0-9_]*)["']?`)
	alterTableRE  = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+["']?([a-zA-Z_][a-zA-Z0-9_]*)["']?`)
	dropTableRE   = regexp.MustCompile(`(?i)DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?["']?([a-zA-Z_][a-zA-Z0-9_]*)["']?`)
	// FROM / JOIN / INTO / UPDATE / TABLE for query SQL table refs (skip common SQL keywords).
	queryTableRE = regexp.MustCompile(`(?i)\b(?:FROM|JOIN|INTO|UPDATE|TABLE)\s+["']?([a-zA-Z_][a-zA-Z0-9_]*)["']?`)
)

// allowlistedTables are intentional exceptions. Empty: every current module table
// already follows the "<id>_" convention.
var allowlistedTables = map[string]string{
	// "example_shared": "cross-module read model documented in doc/data-model.md",
}

func TestModuleSQLTablesUseModulePrefix(t *testing.T) {
	modules, err := listBusinessModules()
	if err != nil {
		t.Fatal(err)
	}
	prefixByModule := map[string]string{}
	for _, module := range modules {
		prefixByModule[module] = module + "_"
	}

	for _, module := range modules {
		prefix := prefixByModule[module]
		otherPrefixes := otherModulePrefixes(module, prefixByModule)

		for _, subdir := range []string{"migrations", "migrations-postgres", "query"} {
			root := filepath.Join(module, subdir)
			if _, err := os.Stat(root); os.IsNotExist(err) {
				continue
			}
			err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() || !strings.HasSuffix(path, ".sql") {
					return nil
				}
				body, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				content := string(body)
				if subdir == "query" {
					assertQueryTables(t, path, module, prefix, otherPrefixes, content)
					return nil
				}
				assertMigrationTables(t, path, module, prefix, content)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func assertMigrationTables(t *testing.T, path, module, prefix, content string) {
	t.Helper()
	for _, re := range []*regexp.Regexp{createTableRE, alterTableRE, dropTableRE} {
		for _, match := range re.FindAllStringSubmatch(content, -1) {
			table := match[1]
			if reason, ok := allowlistedTables[table]; ok {
				t.Logf("%s: allowlisted table %s (%s)", path, table, reason)
				continue
			}
			if !strings.HasPrefix(table, prefix) {
				t.Errorf("%s: table %q must be prefixed with %q (module %s)", path, table, prefix, module)
			}
		}
	}
}

func assertQueryTables(t *testing.T, path, module, prefix string, otherPrefixes []string, content string) {
	t.Helper()
	for _, match := range queryTableRE.FindAllStringSubmatch(content, -1) {
		table := match[1]
		lower := strings.ToLower(table)
		switch lower {
		case "select", "set", "values", "where", "on", "as", "dual", "excluded":
			continue
		}
		if reason, ok := allowlistedTables[table]; ok {
			t.Logf("%s: allowlisted table %s (%s)", path, table, reason)
			continue
		}
		for _, other := range otherPrefixes {
			if strings.HasPrefix(table, other) {
				t.Errorf("%s: query references other module table %q (module %s owns prefix %q)", path, table, module, prefix)
			}
		}
		// Prefer own prefix; allow unqualified CTE/alias-like names without underscore
		// only when they do not match another module prefix (already checked).
		if strings.Contains(table, "_") && !strings.HasPrefix(table, prefix) {
			// Platform tables in query SQL would also fail here — modules should not
			// query platform tables from module query/ files.
			t.Errorf("%s: query table %q must use module prefix %q", path, table, prefix)
		}
	}
}

func listBusinessModules() ([]string, error) {
	entries, err := os.ReadDir(".")
	if err != nil {
		return nil, err
	}
	var modules []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		// Only directories that look like business modules (have migrations or go sources).
		if _, err := os.Stat(filepath.Join(name, "migrations")); err == nil {
			modules = append(modules, name)
			continue
		}
	}
	return modules, nil
}

func otherModulePrefixes(self string, prefixByModule map[string]string) []string {
	var out []string
	for module, prefix := range prefixByModule {
		if module == self {
			continue
		}
		out = append(out, prefix)
	}
	return out
}
