package schemamigration

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/migrations"
)

type fakeDB struct {
	applied        map[string]bool
	tables         map[string]bool
	tryLockResults []bool
	execQueries    []string
	execArgs       [][]any
}

func (db *fakeDB) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	db.execQueries = append(db.execQueries, compact(query))
	db.execArgs = append(db.execArgs, args)
	if strings.HasPrefix(compact(query), "INSERT INTO afscp_schema_migrations") && len(args) > 0 {
		if db.applied == nil {
			db.applied = map[string]bool{}
		}
		if name, ok := args[0].(string); ok {
			db.applied[name] = true
		}
	}
	if strings.HasPrefix(compact(query), "CREATE TABLE IF NOT EXISTS afscp_schema_migrations") {
		if db.tables == nil {
			db.tables = map[string]bool{}
		}
		db.tables[ledgerTableName] = true
	}
	return fakeResult(0), nil
}

func (db *fakeDB) QueryContext(_ context.Context, query string, _ ...any) (RowsScanner, error) {
	if compact(query) != "SELECT migration_name FROM afscp_schema_migrations ORDER BY migration_name" {
		return &fakeRows{}, nil
	}
	names := make([]string, 0, len(db.applied))
	for name := range db.applied {
		names = append(names, name)
	}
	return &fakeRows{values: names}, nil
}

func (db *fakeDB) QueryRowContext(_ context.Context, query string, args ...any) RowScanner {
	normalized := compact(query)
	if normalized == "SELECT pg_try_advisory_lock($1)" {
		locked := true
		if len(db.tryLockResults) > 0 {
			locked = db.tryLockResults[0]
			db.tryLockResults = db.tryLockResults[1:]
		}
		return fakeRow{value: locked}
	}
	if normalized == "SELECT pg_advisory_unlock($1)" {
		return fakeRow{value: true}
	}
	if normalized == "SELECT to_regclass($1) IS NOT NULL" && len(args) > 0 {
		tableName := strings.TrimPrefix(args[0].(string), "public.")
		return fakeRow{value: db.tables[tableName]}
	}
	return fakeRow{value: false}
}

type fakeRows struct {
	values []string
	index  int
}

func (rows *fakeRows) Close() error { return nil }
func (rows *fakeRows) Err() error   { return nil }
func (rows *fakeRows) Next() bool {
	return rows.index < len(rows.values)
}
func (rows *fakeRows) Scan(dest ...any) error {
	*dest[0].(*string) = rows.values[rows.index]
	rows.index += 1
	return nil
}

type fakeRow struct {
	value bool
}

func (row fakeRow) Scan(dest ...any) error {
	*dest[0].(*bool) = row.value
	return nil
}

type fakeResult int64

func (result fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (result fakeResult) RowsAffected() (int64, error) { return int64(result), nil }

func compact(query string) string {
	return strings.Join(strings.Fields(query), " ")
}

func TestApplyExecutesOnlyPendingMigrationsAndRecordsThem(t *testing.T) {
	db := &fakeDB{
		applied: map[string]bool{
			"0001_initial.sql": true,
		},
		tables: map[string]bool{
			ledgerTableName: true,
			"operations":    true,
		},
	}
	runner := Runner{
		DB: db,
		Migrations: []migrations.Migration{
			{Name: "0002_runtime.sql", SQL: "CREATE TABLE runtime_ready(id text)"},
			{Name: "0001_initial.sql", SQL: "CREATE TABLE initial(id text)"},
		},
		RequiredTables: []string{"operations"},
	}

	result, err := runner.Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if result.Status != "ready" {
		t.Fatalf("status = %q, want ready: %#v", result.Status, result)
	}
	if got := strings.Join(result.AppliedMigrations, ","); got != "0002_runtime.sql" {
		t.Fatalf("applied = %q, want 0002_runtime.sql", got)
	}
	if got := strings.Join(result.SkippedMigrations, ","); got != "0001_initial.sql" {
		t.Fatalf("skipped = %q, want 0001_initial.sql", got)
	}
	joinedExec := strings.Join(db.execQueries, "\n")
	if strings.Contains(joinedExec, "CREATE TABLE initial") {
		t.Fatalf("already-applied migration was executed:\n%s", joinedExec)
	}
	if !strings.Contains(joinedExec, "CREATE TABLE runtime_ready") {
		t.Fatalf("pending migration was not executed:\n%s", joinedExec)
	}
}

func TestCheckReportsPendingMigrationsAndMissingRequiredTables(t *testing.T) {
	db := &fakeDB{
		applied: map[string]bool{
			"0001_initial.sql": true,
		},
		tables: map[string]bool{
			ledgerTableName: true,
			"operations":    true,
		},
	}
	runner := Runner{
		DB: db,
		Migrations: []migrations.Migration{
			{Name: "0001_initial.sql", SQL: "CREATE TABLE initial(id text)"},
			{Name: "0002_runtime.sql", SQL: "CREATE TABLE runtime_ready(id text)"},
		},
		RequiredTables: []string{"operations", "export_runtime_requests"},
	}

	result, err := runner.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	if result.Status != "not_ready" {
		t.Fatalf("status = %q, want not_ready", result.Status)
	}
	if got := strings.Join(result.PendingMigrations, ","); got != "0002_runtime.sql" {
		t.Fatalf("pending = %q, want 0002_runtime.sql", got)
	}
	if got := strings.Join(result.MissingRequiredTables, ","); got != "export_runtime_requests" {
		t.Fatalf("missing tables = %q, want export_runtime_requests", got)
	}
}
