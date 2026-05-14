package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/schemamigration"
)

func TestRunVersionDoesNotConstructRunner(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	constructed := 0
	cmd := newCommand(&stdout, &stderr)
	cmd.newRunner = func(context.Context, string) (migrationRunner, error) {
		constructed += 1
		return &fakeMigrationRunner{}, nil
	}

	code := cmd.run([]string{"--version"})
	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if constructed != 0 {
		t.Fatalf("runner constructed %d times, want 0", constructed)
	}
}

func TestRunRequiresActionFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	constructed := 0
	cmd := newCommand(&stdout, &stderr)
	cmd.newRunner = func(context.Context, string) (migrationRunner, error) {
		constructed += 1
		return &fakeMigrationRunner{}, nil
	}

	code := cmd.run(nil)
	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if constructed != 0 {
		t.Fatalf("runner constructed %d times, want 0", constructed)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--apply") || !strings.Contains(stderr.String(), "--check") {
		t.Fatalf("stderr = %q, want action flag diagnostic", stderr.String())
	}
}

func TestRunCheckRequiresPostgresDSN(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newCommand(&stdout, &stderr)
	cmd.lookupEnv = func(string) (string, bool) { return "", false }

	code := cmd.run([]string{"--check"})
	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "AFSCP_MIGRATION_POSTGRES_DSN") {
		t.Fatalf("stderr = %q, want missing DSN diagnostic", stderr.String())
	}
}

func TestRunApplyThenCheckReadyKeepsApplyEvidence(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runner := &fakeMigrationRunner{
		applyResult: schemamigration.Result{
			SchemaVersion:     schemamigration.ResultSchemaVersion,
			Status:            "ready",
			AppliedMigrations: []string{"0001.sql"},
			SkippedMigrations: []string{"0000.sql"},
		},
		checkResult: schemamigration.Result{
			SchemaVersion:     schemamigration.ResultSchemaVersion,
			Status:            "ready",
			SkippedMigrations: []string{"0000.sql", "0001.sql"},
		},
	}
	cmd := newCommand(&stdout, &stderr)
	cmd.lookupEnv = func(string) (string, bool) { return "postgres://user:secret@db/afscp", true }
	cmd.newRunner = func(context.Context, string) (migrationRunner, error) {
		return runner, nil
	}

	code := cmd.run([]string{"--apply", "--check"})
	if code != 0 {
		t.Fatalf("run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	if runner.applyCalls != 1 || runner.checkCalls != 1 || runner.closeCalls != 1 {
		t.Fatalf("runner calls apply/check/close = %d/%d/%d, want 1/1/1", runner.applyCalls, runner.checkCalls, runner.closeCalls)
	}
	result := decodeSingleResult(t, stdout.String())
	if result.Status != "ready" {
		t.Fatalf("status = %q, want ready", result.Status)
	}
	if !reflect.DeepEqual(result.AppliedMigrations, []string{"0001.sql"}) {
		t.Fatalf("applied_migrations = %#v, want apply evidence", result.AppliedMigrations)
	}
	if !reflect.DeepEqual(result.SkippedMigrations, []string{"0000.sql"}) {
		t.Fatalf("skipped_migrations = %#v, want apply semantics", result.SkippedMigrations)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunApplyThenCheckOutputsApplyEvidenceAndFinalReadiness(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runner := &fakeMigrationRunner{
		applyResult: schemamigration.Result{
			SchemaVersion:     schemamigration.ResultSchemaVersion,
			Status:            "ready",
			AppliedMigrations: []string{"0001.sql"},
			SkippedMigrations: []string{"0000.sql"},
		},
		checkResult: schemamigration.Result{
			SchemaVersion:         schemamigration.ResultSchemaVersion,
			Status:                "not_ready",
			SkippedMigrations:     []string{"0000.sql", "0001.sql"},
			PendingMigrations:     []string{"0002.sql"},
			MissingRequiredTables: []string{"export_runtime_requests"},
		},
	}
	cmd := newCommand(&stdout, &stderr)
	cmd.lookupEnv = func(key string) (string, bool) {
		if key == "AFSCP_POSTGRES_DSN" {
			return "postgres://user:secret@db/afscp", true
		}
		return "", false
	}
	cmd.newRunner = func(_ context.Context, dsn string) (migrationRunner, error) {
		if dsn != "postgres://user:secret@db/afscp" {
			t.Fatalf("dsn = %q, want env dsn", dsn)
		}
		return runner, nil
	}

	code := cmd.run([]string{"--apply", "--check"})
	if code != 1 {
		t.Fatalf("run returned %d, want 1; stderr=%q", code, stderr.String())
	}
	if runner.applyCalls != 1 || runner.checkCalls != 1 || runner.closeCalls != 1 {
		t.Fatalf("runner calls apply/check/close = %d/%d/%d, want 1/1/1", runner.applyCalls, runner.checkCalls, runner.closeCalls)
	}
	result := decodeSingleResult(t, stdout.String())
	if result.Status != "not_ready" {
		t.Fatalf("status = %q, want final check status", result.Status)
	}
	if !reflect.DeepEqual(result.AppliedMigrations, []string{"0001.sql"}) {
		t.Fatalf("applied_migrations = %#v, want apply evidence", result.AppliedMigrations)
	}
	if !reflect.DeepEqual(result.SkippedMigrations, []string{"0000.sql"}) {
		t.Fatalf("skipped_migrations = %#v, want apply semantics", result.SkippedMigrations)
	}
	if !reflect.DeepEqual(result.PendingMigrations, []string{"0002.sql"}) {
		t.Fatalf("pending_migrations = %#v, want final check readiness", result.PendingMigrations)
	}
	if !reflect.DeepEqual(result.MissingRequiredTables, []string{"export_runtime_requests"}) {
		t.Fatalf("missing_required_tables = %#v, want final check readiness", result.MissingRequiredTables)
	}
}

func TestRunApplyThenCheckErrorOutputsMergedApplyEvidence(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runner := &fakeMigrationRunner{
		applyResult: schemamigration.Result{
			SchemaVersion:     schemamigration.ResultSchemaVersion,
			Status:            "ready",
			AppliedMigrations: []string{"0001.sql"},
			SkippedMigrations: []string{"0000.sql"},
		},
		checkResult: schemamigration.Result{
			SchemaVersion:         schemamigration.ResultSchemaVersion,
			Status:                "not_ready",
			SkippedMigrations:     []string{"0000.sql", "0001.sql"},
			PendingMigrations:     []string{"0002.sql"},
			MissingRequiredTables: []string{"export_runtime_requests"},
		},
		checkErr: errors.New("read schema readiness failed"),
	}
	cmd := newCommand(&stdout, &stderr)
	cmd.lookupEnv = func(string) (string, bool) { return "postgres://user:secret@db/afscp", true }
	cmd.newRunner = func(context.Context, string) (migrationRunner, error) {
		return runner, nil
	}

	code := cmd.run([]string{"--apply", "--check"})
	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	result := decodeSingleResult(t, stdout.String())
	if !reflect.DeepEqual(result.AppliedMigrations, []string{"0001.sql"}) {
		t.Fatalf("applied_migrations = %#v, want apply evidence", result.AppliedMigrations)
	}
	if !reflect.DeepEqual(result.SkippedMigrations, []string{"0000.sql"}) {
		t.Fatalf("skipped_migrations = %#v, want apply semantics", result.SkippedMigrations)
	}
	if !reflect.DeepEqual(result.PendingMigrations, []string{"0002.sql"}) {
		t.Fatalf("pending_migrations = %#v, want final check readiness", result.PendingMigrations)
	}
	if !reflect.DeepEqual(result.MissingRequiredTables, []string{"export_runtime_requests"}) {
		t.Fatalf("missing_required_tables = %#v, want final check readiness", result.MissingRequiredTables)
	}
	if !strings.Contains(stderr.String(), "check schema readiness") {
		t.Fatalf("stderr = %q, want check diagnostic", stderr.String())
	}
}

func TestRunCheckNotReadyExitsOne(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newCommand(&stdout, &stderr)
	cmd.lookupEnv = func(string) (string, bool) { return "postgres://user:secret@db/afscp", true }
	cmd.newRunner = func(context.Context, string) (migrationRunner, error) {
		return &fakeMigrationRunner{
			checkResult: schemamigration.Result{
				SchemaVersion:         schemamigration.ResultSchemaVersion,
				Status:                "not_ready",
				PendingMigrations:     []string{"0002.sql"},
				MissingRequiredTables: []string{"export_runtime_requests"},
			},
		}, nil
	}

	code := cmd.run([]string{"--check"})
	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), `"pending_migrations":["0002.sql"]`) {
		t.Fatalf("stdout = %q, want pending JSON", stdout.String())
	}
	if !strings.Contains(stderr.String(), "schema is not ready") {
		t.Fatalf("stderr = %q, want not ready diagnostic", stderr.String())
	}
}

func TestRunRedactsConfigureErrors(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newCommand(&stdout, &stderr)
	cmd.lookupEnv = func(string) (string, bool) { return "postgres://user:secret-token@db/afscp", true }
	cmd.newRunner = func(context.Context, string) (migrationRunner, error) {
		return nil, errors.New("connect postgres://user:secret-token@db/afscp failed")
	}

	code := cmd.run([]string{"--check"})
	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if strings.Contains(stderr.String(), "secret-token") {
		t.Fatalf("stderr leaked secret: %q", stderr.String())
	}
}

type fakeMigrationRunner struct {
	applyCalls  int
	checkCalls  int
	closeCalls  int
	applyResult schemamigration.Result
	checkResult schemamigration.Result
	applyErr    error
	checkErr    error
	closeErr    error
}

func (runner *fakeMigrationRunner) Apply(context.Context) (schemamigration.Result, error) {
	runner.applyCalls += 1
	return runner.applyResult, runner.applyErr
}

func (runner *fakeMigrationRunner) Check(context.Context) (schemamigration.Result, error) {
	runner.checkCalls += 1
	return runner.checkResult, runner.checkErr
}

func (runner *fakeMigrationRunner) Close() error {
	runner.closeCalls += 1
	return runner.closeErr
}

func decodeSingleResult(t *testing.T, output string) schemamigration.Result {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(output))
	var result schemamigration.Result
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("decode result: %v; output=%q", err, output)
	}
	var extra schemamigration.Result
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("decode extra result err = %v, want EOF; output=%q", err, output)
	}
	return result
}
