package migrations

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed *.sql
var files embed.FS

type Migration struct {
	Name string
	SQL  string
}

func List() ([]Migration, error) {
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("migrations/*.sql must include at least one migration")
	}
	sort.Strings(names)

	out := make([]Migration, 0, len(names))
	for _, name := range names {
		body, err := files.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		out = append(out, Migration{Name: name, SQL: string(body)})
	}
	return out, nil
}

func RequiredTables() []string {
	return []string{
		"operations",
		"audit_outbox",
		"repo_fences",
		"volumes",
		"namespaces",
		"namespace_volume_bindings",
		"repos",
		"restore_plans",
		"export_sessions",
		"export_runtime_requests",
		"workload_mount_bindings",
		"restore_reconciliation_runs",
		"restore_reconciliation_targets",
		"restore_reconciliation_observations",
	}
}
