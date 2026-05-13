package schemamigration

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/migrations"
)

const (
	ResultSchemaVersion = "afscp.schema-bootstrap.result/v1"
	ledgerTableName     = "afscp_schema_migrations"
	advisoryLockID      = int64(81924135043145001)
)

type RowScanner interface {
	Scan(dest ...any) error
}

type RowsScanner interface {
	Close() error
	Err() error
	Next() bool
	Scan(dest ...any) error
}

type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (RowsScanner, error)
	QueryRowContext(ctx context.Context, query string, args ...any) RowScanner
}

type Result struct {
	SchemaVersion         string   `json:"schema_version"`
	Status                string   `json:"status"`
	AppliedMigrations     []string `json:"applied_migrations"`
	SkippedMigrations     []string `json:"skipped_migrations"`
	PendingMigrations     []string `json:"pending_migrations"`
	MissingRequiredTables []string `json:"missing_required_tables"`
}

type Runner struct {
	DB             DB
	Migrations     []migrations.Migration
	RequiredTables []string
}

func NewRunner(db DB) (Runner, error) {
	embedded, err := migrations.List()
	if err != nil {
		return Runner{}, err
	}
	return Runner{
		DB:             db,
		Migrations:     embedded,
		RequiredTables: migrations.RequiredTables(),
	}, nil
}

func (runner Runner) Apply(ctx context.Context) (Result, error) {
	if runner.DB == nil {
		return Result{}, fmt.Errorf("database connection is required")
	}
	migrationSet, err := runner.migrationSet()
	if err != nil {
		return Result{}, err
	}
	if err := runner.acquireLock(ctx); err != nil {
		return Result{}, err
	}
	defer func() {
		_ = runner.releaseLock(context.Background())
	}()
	if err := runner.ensureLedger(ctx); err != nil {
		return Result{}, err
	}
	applied, err := runner.loadApplied(ctx)
	if err != nil {
		return Result{}, err
	}

	result := baseResult()
	for _, migration := range migrationSet {
		if applied[migration.Name] {
			result.SkippedMigrations = append(result.SkippedMigrations, migration.Name)
			continue
		}
		if _, err := runner.DB.ExecContext(ctx, migration.SQL); err != nil {
			return result, fmt.Errorf("apply migration %s: %w", migration.Name, err)
		}
		if _, err := runner.DB.ExecContext(
			ctx,
			"INSERT INTO afscp_schema_migrations (migration_name, applied_at) VALUES ($1, CURRENT_TIMESTAMP) ON CONFLICT (migration_name) DO NOTHING",
			migration.Name,
		); err != nil {
			return result, fmt.Errorf("record migration %s: %w", migration.Name, err)
		}
		applied[migration.Name] = true
		result.AppliedMigrations = append(result.AppliedMigrations, migration.Name)
	}

	result.PendingMigrations = pendingMigrationNames(migrationSet, applied)
	result.MissingRequiredTables, err = runner.missingRequiredTables(ctx)
	if err != nil {
		return result, err
	}
	result.Status = statusFor(result)
	return result, nil
}

func (runner Runner) Check(ctx context.Context) (Result, error) {
	if runner.DB == nil {
		return Result{}, fmt.Errorf("database connection is required")
	}
	migrationSet, err := runner.migrationSet()
	if err != nil {
		return Result{}, err
	}

	result := baseResult()
	ledgerExists, err := runner.tableExists(ctx, ledgerTableName)
	if err != nil {
		return result, err
	}
	applied := map[string]bool{}
	if ledgerExists {
		applied, err = runner.loadApplied(ctx)
		if err != nil {
			return result, err
		}
	}
	for _, migration := range migrationSet {
		if applied[migration.Name] {
			result.SkippedMigrations = append(result.SkippedMigrations, migration.Name)
		}
	}
	result.PendingMigrations = pendingMigrationNames(migrationSet, applied)
	result.MissingRequiredTables, err = runner.missingRequiredTables(ctx)
	if err != nil {
		return result, err
	}
	result.Status = statusFor(result)
	return result, nil
}

func (runner Runner) migrationSet() ([]migrations.Migration, error) {
	migrationSet := append([]migrations.Migration(nil), runner.Migrations...)
	if len(migrationSet) == 0 {
		var err error
		migrationSet, err = migrations.List()
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(migrationSet, func(i, j int) bool {
		return migrationSet[i].Name < migrationSet[j].Name
	})
	for _, migration := range migrationSet {
		if migration.Name == "" {
			return nil, fmt.Errorf("migration name is required")
		}
		if migration.SQL == "" {
			return nil, fmt.Errorf("migration %s SQL is required", migration.Name)
		}
	}
	return migrationSet, nil
}

func baseResult() Result {
	return Result{
		SchemaVersion: ResultSchemaVersion,
		Status:        "not_ready",
	}
}

func statusFor(result Result) string {
	if len(result.PendingMigrations) == 0 && len(result.MissingRequiredTables) == 0 {
		return "ready"
	}
	return "not_ready"
}

func pendingMigrationNames(migrationSet []migrations.Migration, applied map[string]bool) []string {
	var pending []string
	for _, migration := range migrationSet {
		if !applied[migration.Name] {
			pending = append(pending, migration.Name)
		}
	}
	return pending
}

func (runner Runner) ensureLedger(ctx context.Context) error {
	_, err := runner.DB.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS afscp_schema_migrations (
    migration_name text PRIMARY KEY,
    applied_at timestamp with time zone NOT NULL
)`)
	if err != nil {
		return fmt.Errorf("ensure schema migration ledger: %w", err)
	}
	return nil
}

func (runner Runner) loadApplied(ctx context.Context) (map[string]bool, error) {
	rows, err := runner.DB.QueryContext(ctx, "SELECT migration_name FROM afscp_schema_migrations ORDER BY migration_name")
	if err != nil {
		return nil, fmt.Errorf("read schema migration ledger: %w", err)
	}
	defer rows.Close()

	applied := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan schema migration ledger: %w", err)
		}
		applied[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan schema migration ledger: %w", err)
	}
	return applied, nil
}

func (runner Runner) missingRequiredTables(ctx context.Context) ([]string, error) {
	required := append([]string(nil), runner.RequiredTables...)
	if len(required) == 0 {
		required = migrations.RequiredTables()
	}
	sort.Strings(required)

	var missing []string
	for _, tableName := range required {
		exists, err := runner.tableExists(ctx, tableName)
		if err != nil {
			return nil, err
		}
		if !exists {
			missing = append(missing, tableName)
		}
	}
	return missing, nil
}

func (runner Runner) tableExists(ctx context.Context, tableName string) (bool, error) {
	var exists bool
	if err := runner.DB.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+tableName).Scan(&exists); err != nil {
		return false, fmt.Errorf("check required table %s: %w", tableName, err)
	}
	return exists, nil
}

func (runner Runner) acquireLock(ctx context.Context) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		var locked bool
		if err := runner.DB.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockID).Scan(&locked); err != nil {
			return fmt.Errorf("acquire schema migration lock: %w", err)
		}
		if locked {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (runner Runner) releaseLock(ctx context.Context) error {
	var released bool
	if err := runner.DB.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockID).Scan(&released); err != nil {
		return fmt.Errorf("release schema migration lock: %w", err)
	}
	return nil
}
