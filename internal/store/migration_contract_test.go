package store

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"
)

func TestPostgreSQLMigrationContractDefinesPersistencePrimitives(t *testing.T) {
	contract := loadMigrationContract(t)

	t.Run("operations table", func(t *testing.T) {
		table := contract.requireTable(t, "operations")

		table.requireColumn(t, "operation_id", "text", "primary key")
		table.requireColumn(t, "operation_type", "text", "not null")
		table.requireColumn(t, "operation_state", "text", "not null")
		table.requireColumn(t, "phase", "text", "not null")
		table.requireColumn(t, "attempt", "integer", "not null", "default 0")
		table.requireColumn(t, "lease_owner", "text")
		table.requireColumn(t, "lease_expires_at", "timestamp with time zone")
		table.requireColumn(t, "idempotency_scope", "text", "not null")
		table.requireColumn(t, "idempotency_key", "text", "not null")
		table.requireColumn(t, "request_hash", "text", "not null")
		table.requireColumn(t, "correlation_id", "text", "not null")
		table.requireColumn(t, "caller_service", "text", "not null")
		table.requireColumn(t, "authorized_actor_type", "text", "not null")
		table.requireColumn(t, "authorized_actor_id", "text", "not null")
		table.requireColumn(t, "resource_type", "text", "not null")
		table.requireColumn(t, "resource_id", "text", "not null")
		table.requireColumn(t, "namespace_id", "text", "not null")
		table.requireColumn(t, "repo_id", "text")
		table.requireColumn(t, "template_id", "text")
		table.requireColumn(t, "export_id", "text")
		table.requireColumn(t, "mount_binding_id", "text")
		table.requireColumn(t, "session_fence_id", "text")
		table.requireColumn(t, "external_resource_ids", "jsonb", "not null", "default '{}'::jsonb")
		table.requireColumn(t, "input_summary", "jsonb", "not null", "default '{}'::jsonb")
		table.requireColumn(t, "jvs_json_output", "jsonb")
		table.requireColumn(t, "verification_result", "jsonb")
		table.requireColumn(t, "compensation_status", "text")
		table.requireColumn(t, "error_json", "jsonb")
		table.requireColumn(t, "created_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "started_at", "timestamp with time zone")
		table.requireColumn(t, "finished_at", "timestamp with time zone")
		table.requireColumn(t, "updated_at", "timestamp with time zone", "not null")
		table.requireCheckMentions(t, "operation_type", expectedOperationTypeValuesForMigrationContract()...)
		table.requireCheckMentions(t, "operation_state", "queued", "running", "succeeded", "failed", "cancel_requested", "cancelled", "operator_intervention_required")
	})

	t.Run("idempotency is atomic and namespace explicit", func(t *testing.T) {
		table := contract.requireTable(t, "operations")
		table.requireUniqueColumns(t, "caller_service", "namespace_id", "operation_type", "idempotency_key")
		table.requireColumn(t, "namespace_id", "text", "not null")
	})

	t.Run("jvs mutation intake has index-backed concurrency gates", func(t *testing.T) {
		contract.requireRawFragments(t,
			"create unique index if not exists operations_one_non_terminal_jvs_mutation_per_repo_idx",
			"on operations (repo_id)",
			"where repo_id is not null",
			"'save_point_create'",
			"'restore_preview'",
			"'restore_preview_discard'",
			"'restore_run'",
			"'template_create'",
			"'template_clone'",
			"operation_state not in ('succeeded', 'failed', 'cancelled')",
			"create unique index if not exists operations_restore_run_one_per_preview_idx",
			"on operations (namespace_id, repo_id, (input_summary->>'preview_operation_id'))",
			"where operation_type = 'restore_run'",
			"operation_state not in ('failed', 'cancelled')",
			"(input_summary->>'preview_operation_id') is not null",
			"btrim(input_summary->>'preview_operation_id') <> ''",
		)
	})

	t.Run("audit outbox table", func(t *testing.T) {
		table := contract.requireTable(t, "audit_outbox")

		table.requireColumn(t, "audit_event_id", "text", "primary key")
		table.requireColumn(t, "event_type", "text", "not null")
		table.requireColumn(t, "event_time", "timestamp with time zone", "not null")
		table.requireColumn(t, "payload_json", "jsonb", "not null")
		table.requireColumn(t, "delivery_status", "text", "not null", "default 'pending'")
		table.requireColumn(t, "delivery_attempt", "integer", "not null", "default 0")
		table.requireColumn(t, "next_retry_at", "timestamp with time zone")
		table.requireColumn(t, "last_error", "text")
		table.requireColumn(t, "created_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "updated_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "delivered_at", "timestamp with time zone")
		table.requireCheckMentions(t, "delivery_status", "pending", "delivering", "delivered", "retry_wait", "failed")
	})

	t.Run("repo scoped fences", func(t *testing.T) {
		table := contract.requireTable(t, "repo_fences")

		table.requireColumn(t, "fence_id", "text", "primary key")
		table.requireColumn(t, "repo_id", "text", "not null")
		table.requireColumn(t, "fence_kind", "text", "not null")
		table.requireColumn(t, "holder_operation_id", "text", "not null", "references operations")
		table.requireColumn(t, "status", "text", "not null")
		table.requireColumn(t, "expires_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "released_at", "timestamp with time zone")
		table.requireColumn(t, "recovery_operation_id", "text", "references operations")
		table.requireColumn(t, "recovery_reason", "text")
		table.requireColumn(t, "recovery_started_at", "timestamp with time zone")
		table.requireColumn(t, "recovered_at", "timestamp with time zone")
		table.requireColumn(t, "created_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "updated_at", "timestamp with time zone", "not null")
		table.requireCheckMentions(t, "fence_kind", "writer_session", "lifecycle")
		table.requireCheckMentions(t, "status", "active", "released", "expired", "recovery_required", "recovered")
		table.requireBodyFragments(t,
			"status in ('active', 'expired', 'recovery_required') and released_at is null and recovered_at is null",
			"status = 'released' and released_at is not null and recovered_at is null",
			"status = 'recovered' and released_at is not null and recovered_at is not null",
		)
		contract.requirePartialUniqueIndex(t, "repo_fences", []string{"repo_id", "fence_kind"}, "released_at is null")
	})

	t.Run("volumes table", func(t *testing.T) {
		table := contract.requireTable(t, "volumes")

		table.requireColumn(t, "volume_id", "text", "primary key")
		table.requireColumn(t, "backend", "text", "not null")
		table.requireColumn(t, "isolation_class", "text", "not null")
		table.requireColumn(t, "status", "text", "not null")
		table.requireColumn(t, "capabilities", "jsonb", "not null", "default '{}'::jsonb")
		table.requireColumn(t, "created_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "updated_at", "timestamp with time zone", "not null")
		table.requireCheckMentions(t, "backend", "juicefs")
		table.requireCheckMentions(t, "isolation_class", "shared", "dedicated")
		table.requireCheckMentions(t, "status", "active", "disabled", "degraded")
		table.requireBodyFragments(t, "jsonb_typeof(capabilities) = 'object'")
	})

	t.Run("namespaces table", func(t *testing.T) {
		table := contract.requireTable(t, "namespaces")

		table.requireColumn(t, "namespace_id", "text", "primary key")
		table.requireColumn(t, "status", "text", "not null", "default 'active'")
		table.requireColumn(t, "disabled_reason", "text")
		table.requireColumn(t, "disabled_at", "timestamp with time zone")
		table.requireColumn(t, "created_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "updated_at", "timestamp with time zone", "not null")
		table.requireCheckMentions(t, "status", "active", "disabled")
		table.requireBodyFragments(t,
			"status = 'active' and disabled_at is null",
			"status = 'disabled' and disabled_at is not null",
		)
	})

	t.Run("namespace volume bindings table", func(t *testing.T) {
		table := contract.requireTable(t, "namespace_volume_bindings")

		table.requireColumn(t, "namespace_id", "text", "primary key", "references namespaces")
		table.requireColumn(t, "default_volume_id", "text", "not null", "references volumes")
		table.requireColumn(t, "allowed_callers", "jsonb", "not null", "default '[]'::jsonb")
		table.requireColumn(t, "quota_bytes_default", "bigint", "not null", "default 0")
		table.requireColumn(t, "export_policy", "jsonb", "not null", "default '{}'::jsonb")
		table.requireColumn(t, "lifecycle_policy", "jsonb", "not null", "default '{}'::jsonb")
		table.requireColumn(t, "mount_policy", "jsonb", "not null", "default '{}'::jsonb")
		table.requireColumn(t, "template_policy", "jsonb", "not null", "default '{}'::jsonb")
		table.requireColumn(t, "status", "text", "not null")
		table.requireColumn(t, "created_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "updated_at", "timestamp with time zone", "not null")
		table.requireCheckMentions(t, "status", "active", "disabled")
		table.requireBodyFragments(t,
			"jsonb_typeof(allowed_callers) = 'array'",
			"jsonb_typeof(export_policy) = 'object'",
			"jsonb_typeof(lifecycle_policy) = 'object'",
			"jsonb_typeof(mount_policy) = 'object'",
			"jsonb_typeof(template_policy) = 'object'",
			"quota_bytes_default >= 0",
		)
	})

	t.Run("repos table", func(t *testing.T) {
		table := contract.requireTable(t, "repos")

		table.requireColumn(t, "repo_id", "text", "primary key")
		table.requireColumn(t, "namespace_id", "text", "not null", "references namespaces")
		table.requireColumn(t, "volume_id", "text", "not null", "references volumes")
		table.requireColumn(t, "jvs_repo_id", "text", "not null")
		table.requireColumn(t, "repo_kind", "text", "not null")
		table.requireColumn(t, "status", "text", "not null")
		table.requireColumn(t, "control_volume_subdir", "text", "not null")
		table.requireColumn(t, "payload_volume_subdir", "text", "not null")
		table.requireColumn(t, "lifecycle_status", "text", "not null")
		table.requireColumn(t, "retention_expires_at", "timestamp with time zone")
		table.requireColumn(t, "last_lifecycle_operation_id", "text", "references operations")
		table.requireColumn(t, "pre_delete_status", "text")
		table.requireColumn(t, "created_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "updated_at", "timestamp with time zone", "not null")
		table.requireUniqueColumns(t, "namespace_id", "repo_id")
		table.requireCheckMentions(t, "repo_kind", "repo", "template")
		table.requireCheckMentions(t, "status", expectedRepoStatusValuesForMigrationContract()...)
		table.requireCheckMentions(t, "lifecycle_status", expectedRepoStatusValuesForMigrationContract()...)
		table.requireBodyFragments(t,
			"status = lifecycle_status",
			"repo_kind = 'repo' and control_volume_subdir = 'afscp/namespaces/' || namespace_id || '/repos/' || repo_id || '/control'",
			"repo_kind = 'template' and control_volume_subdir = 'afscp/namespaces/' || namespace_id || '/templates/' || repo_id || '/control'",
			"pre_delete_status is null or pre_delete_status in ( 'active', 'archived' )",
			"status in ('deleting', 'tombstoned', 'restoring_tombstoned', 'purging', 'purged') and pre_delete_status is not null",
			"status in ('active', 'archiving', 'archived', 'restoring_archived') and pre_delete_status is null",
			"status in ('tombstoned', 'restoring_tombstoned', 'purging') and retention_expires_at is not null",
			"status in ('active', 'archiving', 'archived', 'restoring_archived') and retention_expires_at is null",
			"status = 'operator_intervention_required'",
		)
	})

	t.Run("restore plans table", func(t *testing.T) {
		table := contract.requireTable(t, "restore_plans")

		table.requireColumn(t, "restore_plan_id", "text", "primary key")
		table.requireColumn(t, "namespace_id", "text", "not null")
		table.requireColumn(t, "repo_id", "text", "not null")
		table.requireColumn(t, "preview_operation_id", "text", "not null", "references operations")
		table.requireColumn(t, "source_save_point_id", "text", "not null")
		table.requireColumn(t, "status", "text", "not null")
		table.requireColumn(t, "created_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "updated_at", "timestamp with time zone", "not null")
		table.requireUniqueColumns(t, "preview_operation_id")
		table.requireCheckMentions(t, "status", expectedRestorePlanStatusValuesForMigrationContract()...)
		table.requireBodyFragments(t,
			"foreign key (namespace_id, repo_id) references repos (namespace_id, repo_id)",
		)
		contract.requirePartialUniqueIndex(t, "restore_plans", []string{"repo_id"}, "status in ('pending', 'consuming', 'discarding', 'operator_intervention_required')")
	})

	t.Run("export sessions table", func(t *testing.T) {
		table := contract.requireTable(t, "export_sessions")

		table.requireColumn(t, "export_id", "text", "primary key")
		table.requireColumn(t, "namespace_id", "text", "not null")
		table.requireColumn(t, "repo_id", "text", "not null")
		table.requireColumn(t, "protocol", "text", "not null")
		table.requireColumn(t, "access_mode", "text", "not null")
		table.requireColumn(t, "status", "text", "not null")
		table.requireColumn(t, "expires_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "created_by_caller_service", "text", "not null")
		table.requireColumn(t, "created_by_actor_type", "text", "not null")
		table.requireColumn(t, "created_by_actor_id", "text", "not null")
		table.requireColumn(t, "revoked_at", "timestamp with time zone")
		table.requireColumn(t, "last_accessed_at", "timestamp with time zone")
		table.requireColumn(t, "active_request_count", "integer", "not null")
		table.requireColumn(t, "active_write_count", "integer", "not null")
		table.requireColumn(t, "last_observed_at", "timestamp with time zone")
		table.requireColumn(t, "last_gateway_heartbeat_at", "timestamp with time zone")
		table.requireColumn(t, "gateway_heartbeat_expires_at", "timestamp with time zone")
		table.requireColumn(t, "write_drained_at", "timestamp with time zone")
		table.requireColumn(t, "terminal_observed_at", "timestamp with time zone")
		table.requireColumn(t, "status_reason", "text", "not null")
		table.requireColumn(t, "created_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "updated_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "verifier_algorithm", "text", "not null")
		table.requireColumn(t, "verifier_hash", "text", "not null")
		table.requireColumn(t, "verifier_salt", "text", "not null")
		table.requireCheckMentions(t, "protocol", "webdav")
		table.requireCheckMentions(t, "access_mode", "read_only", "read_write")
		table.requireCheckMentions(t, "status", "active", "revoking", "revoked", "expired", "failed")
		table.requireBodyFragments(t,
			"foreign key (namespace_id, repo_id) references repos (namespace_id, repo_id)",
			"active_request_count >= 0",
			"active_write_count >= 0",
			"active_write_count <= active_request_count",
			"status not in ('revoked', 'expired', 'failed') or ( active_request_count = 0 and active_write_count = 0 )",
			"btrim(verifier_algorithm) <> ''",
			"btrim(verifier_hash) <> ''",
			"btrim(verifier_salt) <> ''",
		)
		table.requireNoSensitiveColumns(t)
		contract.requireIndex(t, "export_sessions", []string{"repo_id", "created_at", "export_id"})
	})

	t.Run("export runtime request ledger table", func(t *testing.T) {
		table := contract.requireTable(t, "export_runtime_requests")

		table.requireColumn(t, "runtime_request_id", "text", "primary key")
		table.requireColumn(t, "export_id", "text", "not null")
		table.requireColumn(t, "namespace_id", "text", "not null")
		table.requireColumn(t, "repo_id", "text", "not null")
		table.requireColumn(t, "request_state", "text", "not null")
		table.requireColumn(t, "write_request", "boolean", "not null")
		table.requireColumn(t, "started_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "last_heartbeat_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "heartbeat_expires_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "closed_at", "timestamp with time zone")
		table.requireColumn(t, "close_reason", "text", "not null")
		table.requireColumn(t, "created_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "updated_at", "timestamp with time zone", "not null")
		table.requireCheckMentions(t, "request_state", "open", "closed", "recovered")
		table.requireBodyFragments(t,
			"foreign key (export_id, namespace_id, repo_id) references export_sessions (export_id, namespace_id, repo_id)",
			"foreign key (namespace_id, repo_id) references repos (namespace_id, repo_id)",
			"last_heartbeat_at >= started_at",
			"heartbeat_expires_at >= last_heartbeat_at",
			"closed_at is null or closed_at >= started_at",
			"closed_at is null or closed_at >= last_heartbeat_at",
			"request_state = 'open' and closed_at is null",
			"request_state in ('closed', 'recovered') and closed_at is not null",
		)
		table.requireNoSensitiveColumns(t)
		contract.requireIndex(t, "export_sessions", []string{"export_id", "namespace_id", "repo_id"})
		contract.requireIndex(t, "export_runtime_requests", []string{"export_id", "request_state"})
		contract.requireRawFragments(t,
			"create index if not exists export_runtime_requests_stale_open_idx",
			"on export_runtime_requests (heartbeat_expires_at, runtime_request_id)",
			"where request_state = 'open'",
		)
	})

	t.Run("export sessions terminal counts upgrade guard", func(t *testing.T) {
		contract.requireRawFragments(t,
			"pg_constraint",
			"conname = 'export_sessions_terminal_zero_counts_check'",
			"conrelid = 'export_sessions'::regclass",
			"alter table export_sessions",
			"add constraint export_sessions_terminal_zero_counts_check",
			"status not in ('revoked', 'expired', 'failed')",
			"active_request_count = 0",
			"active_write_count = 0",
		)
	})

	t.Run("workload mount bindings session table", func(t *testing.T) {
		table := contract.requireTable(t, "workload_mount_bindings")

		table.requireColumn(t, "mount_binding_id", "text", "primary key")
		table.requireColumn(t, "namespace_id", "text", "not null")
		table.requireColumn(t, "repo_id", "text", "not null")
		table.requireColumn(t, "volume_id", "text", "not null")
		table.requireColumn(t, "mount_path", "text", "not null")
		table.requireColumn(t, "read_only", "boolean", "not null")
		table.requireColumn(t, "status", "text", "not null")
		table.requireColumn(t, "lease_seconds", "integer", "not null")
		table.requireColumn(t, "lease_expires_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "last_heartbeat_at", "timestamp with time zone")
		table.requireColumn(t, "last_observed_at", "timestamp with time zone")
		table.requireColumn(t, "status_reason", "text", "not null")
		table.requireColumn(t, "created_at", "timestamp with time zone", "not null")
		table.requireColumn(t, "updated_at", "timestamp with time zone", "not null")
		table.requireCheckMentions(t, "status", "issued", "pending", "active", "releasing", "released", "revoked", "expired", "failed")
		table.requireBodyFragments(t,
			"foreign key (namespace_id, repo_id) references repos (namespace_id, repo_id)",
		)
		table.requireNoSensitiveColumnsExcept(t, "mount_path")
		contract.requireIndex(t, "workload_mount_bindings", []string{"repo_id", "created_at", "mount_binding_id"})
	})
}

func TestPostgreSQLMigrationsDoNotEncodeStorageMutationMaterial(t *testing.T) {
	contract := loadMigrationContract(t)

	// Protocol names and one-way verifier/hash/salt columns are safe schema
	// vocabulary. The migration must still never encode plaintext credentials,
	// reversible secrets, raw storage paths, or executable mutation material.
	forbidden := []string{
		"host_path",
		"credential",
		"secret",
		"token",
		"password",
		"private_key",
		"command",
		"argv",
		"subprocess",
		"storage_mutation",
	}
	for _, word := range forbidden {
		if strings.Contains(contract.raw, word) {
			t.Fatalf("migration SQL contains forbidden storage-mutation/material term %q", word)
		}
	}
}

func expectedOperationTypeValuesForMigrationContract() []string {
	return []string{
		"volume_ensure",
		"namespace_upsert",
		"namespace_disable",
		"namespace_volume_binding_put",
		"repo_create",
		"repo_archive",
		"repo_restore_archived",
		"repo_delete",
		"repo_restore_tombstoned",
		"repo_purge",
		"save_point_create",
		"restore_preview",
		"restore_preview_discard",
		"restore_run",
		"template_create",
		"template_clone",
		"export_create",
		"export_revoke",
		"export_session_reconcile",
		"mount_binding_create",
		"mount_binding_status_update",
		"mount_binding_heartbeat",
		"mount_binding_release",
		"mount_binding_revoke",
		"migration_cutover",
	}
}

func expectedRepoStatusValuesForMigrationContract() []string {
	return []string{
		"active",
		"archiving",
		"archived",
		"restoring_archived",
		"deleting",
		"tombstoned",
		"restoring_tombstoned",
		"purging",
		"purged",
		"operator_intervention_required",
	}
}

func expectedRestorePlanStatusValuesForMigrationContract() []string {
	return []string{
		"pending",
		"consuming",
		"consumed",
		"discarding",
		"discarded",
		"operator_intervention_required",
	}
}

type migrationContract struct {
	raw           string
	tables        map[string]tableContract
	indexes       []indexContract
	uniqueIndexes []indexContract
}

type tableContract struct {
	name    string
	body    string
	columns map[string]string
	entries []string
}

type indexContract struct {
	name    string
	table   string
	columns []string
	where   string
}

func loadMigrationContract(t *testing.T) migrationContract {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("no SQL migration files found in migrations/")
	}
	sort.Strings(paths)

	var builder strings.Builder
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		builder.WriteByte('\n')
		builder.Write(body)
	}

	raw := normalizeSQL(builder.String())
	return migrationContract{
		raw:           raw,
		tables:        parseTables(raw),
		indexes:       parseIndexes(raw),
		uniqueIndexes: parseUniqueIndexes(raw),
	}
}

func (contract migrationContract) requireTable(t *testing.T, name string) tableContract {
	t.Helper()

	table, ok := contract.tables[name]
	if !ok {
		t.Fatalf("missing table %q; found tables %v", name, sortedTableNames(contract.tables))
	}
	return table
}

func (contract migrationContract) requirePartialUniqueIndex(t *testing.T, tableName string, columns []string, whereFragment string) {
	t.Helper()

	wantColumns := normalizeColumnList(columns)
	wantWhere := compactSpace(strings.ToLower(whereFragment))
	for _, index := range contract.uniqueIndexes {
		if index.table != tableName {
			continue
		}
		if !sameStrings(index.columns, wantColumns) {
			continue
		}
		if strings.Contains(compactSpace(index.where), wantWhere) {
			return
		}
	}
	t.Fatalf("missing partial unique index on %s(%s) where %s", tableName, strings.Join(columns, ", "), whereFragment)
}

func (contract migrationContract) requireIndex(t *testing.T, tableName string, columns []string) {
	t.Helper()

	wantColumns := normalizeColumnList(columns)
	for _, index := range contract.indexes {
		if index.table == tableName && sameStrings(index.columns, wantColumns) {
			return
		}
	}
	t.Fatalf("missing index on %s(%s)", tableName, strings.Join(columns, ", "))
}

func (contract migrationContract) requireRawFragments(t *testing.T, fragments ...string) {
	t.Helper()

	for _, fragment := range fragments {
		if !strings.Contains(contract.raw, compactSpace(strings.ToLower(fragment))) {
			t.Fatalf("migration SQL missing fragment %q", fragment)
		}
	}
}

func (table tableContract) requireColumn(t *testing.T, name string, fragments ...string) {
	t.Helper()

	definition, ok := table.columns[name]
	if !ok {
		t.Fatalf("table %s missing column %q; found columns %v", table.name, name, sortedColumnNames(table.columns))
	}
	for _, fragment := range fragments {
		if !strings.Contains(definition, strings.ToLower(fragment)) {
			t.Fatalf("table %s column %s = %q, want fragment %q", table.name, name, definition, fragment)
		}
	}
}

func (table tableContract) requireUniqueColumns(t *testing.T, columns ...string) {
	t.Helper()

	want := normalizeColumnList(columns)
	for _, entry := range table.entries {
		if !strings.Contains(entry, "unique") {
			continue
		}
		got := columnsInsideFirstParens(entry)
		if sameStrings(got, want) {
			return
		}
	}
	t.Fatalf("table %s missing unique constraint on (%s)", table.name, strings.Join(columns, ", "))
}

func (table tableContract) requireCheckMentions(t *testing.T, column string, values ...string) {
	t.Helper()

	for _, value := range values {
		if !strings.Contains(table.body, column) || !strings.Contains(table.body, "'"+value+"'") {
			t.Fatalf("table %s missing check value %q for %s", table.name, value, column)
		}
	}
}

func (table tableContract) requireBodyFragments(t *testing.T, fragments ...string) {
	t.Helper()

	for _, fragment := range fragments {
		if !strings.Contains(table.body, compactSpace(fragment)) {
			t.Fatalf("table %s missing SQL fragment %q in %q", table.name, fragment, table.body)
		}
	}
}

func (table tableContract) requireNoSensitiveColumns(t *testing.T) {
	t.Helper()
	table.requireNoSensitiveColumnsExcept(t)
}

func (table tableContract) requireNoSensitiveColumnsExcept(t *testing.T, allowed ...string) {
	t.Helper()

	allowedSet := map[string]bool{}
	for _, column := range allowed {
		allowedSet[column] = true
	}

	forbidden := []string{
		"credential",
		"secret",
		"token",
		"password",
		"raw",
		"path",
		"plan",
		"storage",
	}
	for column := range table.columns {
		if allowedSet[column] {
			continue
		}
		for _, word := range forbidden {
			if strings.Contains(column, word) {
				t.Fatalf("table %s has sensitive/session-external column %q", table.name, column)
			}
		}
	}
}

func parseTables(sql string) map[string]tableContract {
	tablePattern := regexp.MustCompile(`(?is)create\s+table\s+(?:if\s+not\s+exists\s+)?([a-z_][a-z0-9_\.]*)\s*\((.*?)\);`)
	matches := tablePattern.FindAllStringSubmatch(sql, -1)
	tables := make(map[string]tableContract, len(matches))
	for _, match := range matches {
		name := unqualify(match[1])
		body := compactSpace(match[2])
		entries := splitTopLevelCommas(match[2])
		table := tableContract{
			name:    name,
			body:    body,
			columns: make(map[string]string),
			entries: entries,
		}
		for _, entry := range entries {
			column, ok := columnName(entry)
			if !ok {
				continue
			}
			table.columns[column] = compactSpace(entry)
		}
		tables[name] = table
	}
	return tables
}

func parseUniqueIndexes(sql string) []indexContract {
	uniquePattern := regexp.MustCompile(`(?is)create\s+unique\s+index\s+(?:concurrently\s+)?(?:if\s+not\s+exists\s+)?([a-z_][a-z0-9_]*)\s+on\s+([a-z_][a-z0-9_\.]*)\s*(?:using\s+[a-z_][a-z0-9_]*\s*)?\((.*?)\)(?:\s+where\s+(.*?))?;`)
	matches := uniquePattern.FindAllStringSubmatch(sql, -1)
	unique := make([]indexContract, 0, len(matches))
	for _, match := range matches {
		unique = append(unique, indexContract{
			name:    match[1],
			table:   unqualify(match[2]),
			columns: columnsInsideFirstParens("(" + match[3] + ")"),
			where:   compactSpace(match[4]),
		})
	}
	return unique
}

func parseIndexes(sql string) []indexContract {
	indexPattern := regexp.MustCompile(`(?is)create\s+(?:unique\s+)?index\s+(?:concurrently\s+)?(?:if\s+not\s+exists\s+)?([a-z_][a-z0-9_]*)\s+on\s+([a-z_][a-z0-9_\.]*)\s*(?:using\s+[a-z_][a-z0-9_]*\s*)?\((.*?)\)(?:\s+where\s+(.*?))?;`)
	matches := indexPattern.FindAllStringSubmatch(sql, -1)
	indexes := make([]indexContract, 0, len(matches))
	for _, match := range matches {
		indexes = append(indexes, indexContract{
			name:    match[1],
			table:   unqualify(match[2]),
			columns: columnsInsideFirstParens("(" + match[3] + ")"),
			where:   compactSpace(match[4]),
		})
	}
	return indexes
}

func normalizeSQL(sql string) string {
	lineComment := regexp.MustCompile(`--[^\n]*`)
	blockComment := regexp.MustCompile(`(?is)/\*.*?\*/`)
	sql = lineComment.ReplaceAllString(sql, "")
	sql = blockComment.ReplaceAllString(sql, "")
	return strings.ToLower(sql)
}

func splitTopLevelCommas(body string) []string {
	var entries []string
	start := 0
	depth := 0
	inSingleQuote := false
	for index, r := range body {
		switch {
		case r == '\'':
			inSingleQuote = !inSingleQuote
		case inSingleQuote:
		case r == '(':
			depth++
		case r == ')':
			if depth > 0 {
				depth--
			}
		case r == ',' && depth == 0:
			entries = append(entries, compactSpace(body[start:index]))
			start = index + len(string(r))
		}
	}
	if tail := compactSpace(body[start:]); tail != "" {
		entries = append(entries, tail)
	}
	return entries
}

func columnName(entry string) (string, bool) {
	fields := strings.Fields(entry)
	if len(fields) < 2 {
		return "", false
	}
	first := strings.Trim(fields[0], `"`)
	switch first {
	case "constraint", "primary", "unique", "foreign", "check", "exclude":
		return "", false
	default:
		return strings.ToLower(first), true
	}
}

func columnsInsideFirstParens(value string) []string {
	start := strings.Index(value, "(")
	end := strings.Index(value[start+1:], ")")
	if start < 0 || end < 0 {
		return nil
	}
	return normalizeColumnList(strings.Split(value[start+1:start+1+end], ","))
}

func normalizeColumnList(columns []string) []string {
	normalized := make([]string, 0, len(columns))
	for _, column := range columns {
		column = strings.TrimFunc(strings.ToLower(column), func(r rune) bool {
			return unicode.IsSpace(r) || r == '"'
		})
		if column != "" {
			normalized = append(normalized, column)
		}
	}
	return normalized
}

func sortedTableNames(tables map[string]tableContract) []string {
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedColumnNames(columns map[string]string) []string {
	names := make([]string, 0, len(columns))
	for name := range columns {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func unqualify(name string) string {
	parts := strings.Split(name, ".")
	return parts[len(parts)-1]
}

func compactSpace(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}
