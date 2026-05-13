package migrations

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestListEmbeddedMigrationsSorted(t *testing.T) {
	items, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("List returned no migrations")
	}
	if got, want := items[0].Name, "0001_control_plane_persistence.sql"; got != want {
		t.Fatalf("first migration = %q, want %q", got, want)
	}
	for i, item := range items {
		if item.Name == "" || item.SQL == "" {
			t.Fatalf("migration[%d] = %#v, want name and SQL", i, item)
		}
		if i > 0 && items[i-1].Name > item.Name {
			t.Fatalf("migrations not sorted: %q before %q", items[i-1].Name, item.Name)
		}
	}
}

func TestRequiredTablesCoverRuntimeReadinessSurfaces(t *testing.T) {
	tables := map[string]bool{}
	for _, table := range RequiredTables() {
		tables[table] = true
	}
	for _, table := range []string{
		"export_runtime_requests",
		"workload_mount_bindings",
		"operations",
		"repos",
	} {
		if !tables[table] {
			t.Fatalf("RequiredTables missing %s", table)
		}
	}
}

func TestRequiredTablesMatchEmbeddedCreateTables(t *testing.T) {
	created := embeddedCreateTables(t)
	required := sortedUniqueRequiredTables(t)

	if got, want := strings.Join(required, ","), strings.Join(created, ","); got != want {
		t.Fatalf("RequiredTables = [%s], want embedded CREATE TABLE set [%s]", got, want)
	}
}

func TestRequiredTablesExcludeJVSOwnedSavePointHistory(t *testing.T) {
	for _, table := range RequiredTables() {
		if table == "save_points" {
			t.Fatal("save_points is owned by JVS history/API JSON, not PostgreSQL readiness")
		}
	}
}

func embeddedCreateTables(t *testing.T) []string {
	t.Helper()

	items, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	re := regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(?:public\.)?([a-z_][a-z0-9_]*)\b`)
	tables := map[string]bool{}
	for _, item := range items {
		for _, match := range re.FindAllStringSubmatch(item.SQL, -1) {
			tables[strings.ToLower(match[1])] = true
		}
	}
	if len(tables) == 0 {
		t.Fatal("embedded migrations contain no CREATE TABLE statements")
	}
	return sortedKeys(tables)
}

func sortedUniqueRequiredTables(t *testing.T) []string {
	t.Helper()

	tables := map[string]bool{}
	for _, table := range RequiredTables() {
		normalized := strings.ToLower(strings.TrimSpace(table))
		if normalized == "" {
			t.Fatal("RequiredTables contains an empty table name")
		}
		if normalized != table {
			t.Fatalf("RequiredTables contains non-canonical table name %q", table)
		}
		if tables[normalized] {
			t.Fatalf("RequiredTables contains duplicate table name %q", table)
		}
		tables[normalized] = true
	}
	if len(tables) == 0 {
		t.Fatal("RequiredTables returned no tables")
	}
	return sortedKeys(tables)
}

func sortedKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
