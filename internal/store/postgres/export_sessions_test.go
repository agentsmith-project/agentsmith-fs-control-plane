package postgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

func TestStoreImplementsExportAccessStore(t *testing.T) {
	var _ store.ExportStore = (*Store)(nil)
	var _ store.ExportAccessStore = (*Store)(nil)
}

func TestCreateOrReuseExportSQLCommitsSessionOperationAndAuditInOneBoundary(t *testing.T) {
	sql := exportCreateOrReuseSQL()

	assertSQLContainsAll(t, sql,
		"WITH existing_operation AS (",
		"operation_type = 'export_create'",
		"active_namespace AS (",
		"FROM namespaces",
		"status = 'active'",
		"active_binding AS (",
		"webdav_enabled",
		"active_repo AS (",
		"repo_kind = 'repo'",
		"lifecycle_status = 'active'",
		"FOR UPDATE",
		"active_volume AS (",
		"webdav_export",
		"held_lifecycle_fence AS (",
		"fence_kind = 'lifecycle'",
		"held_writer_fence AS (",
		"fence_kind = 'writer_session'",
		"inserted_operation AS (",
		"INSERT INTO operations",
		"operation_state",
		"'succeeded'",
		"resource_type",
		"'export'",
		"inserted_session AS (",
		"INSERT INTO export_sessions",
		"verifier_algorithm",
		"verifier_hash",
		"verifier_salt",
		"inserted_audit AS (",
		"INSERT INTO audit_outbox",
		"UNION ALL",
	)
	for _, forbidden := range []string{"raw_path", "metadata_url", "secret_ref", "password"} {
		if strings.Contains(strings.ToLower(sql), forbidden) {
			t.Fatalf("export create SQL leaked forbidden term %q: %s", forbidden, sql)
		}
	}
}

func TestCreateOrReuseExportSQLPredicatesMatchOperationAndSessionArgs(t *testing.T) {
	sql := exportCreateOrReuseSQL()

	operationPlaceholder := placeholderForColumn(t, operationColumns, 1)
	sessionPlaceholder := placeholderForColumn(t, exportSessionPersistColumns, len(operationColumns)+1)

	wantOperationPredicate := "AND " +
		operationPlaceholder("operation_state") + " = 'succeeded' AND " +
		operationPlaceholder("operation_type") + " = 'export_create' AND " +
		operationPlaceholder("phase") + " = 'export_create_committed' AND " +
		operationPlaceholder("resource_type") + " = 'export' AND " +
		operationPlaceholder("resource_id") + " = " + operationPlaceholder("export_id") + " AND " +
		operationPlaceholder("export_id") + " = " + sessionPlaceholder("export_id") + " AND " +
		operationPlaceholder("namespace_id") + " = " + sessionPlaceholder("namespace_id") + " AND " +
		operationPlaceholder("repo_id") + " = " + sessionPlaceholder("repo_id")

	assertSQLContainsAll(t, sql,
		wantOperationPredicate,
		"export_sessions.export_id = existing_operation.export_id AND export_sessions.namespace_id = existing_operation.namespace_id AND export_sessions.repo_id = existing_operation.repo_id",
		"WHERE namespace_id = "+operationPlaceholder("namespace_id")+" AND status = 'active'",
		"namespace_id = "+operationPlaceholder("namespace_id")+" AND status = 'active' AND COALESCE((export_policy->>'webdav_enabled')::boolean, false) = true",
		"repos.namespace_id = "+operationPlaceholder("namespace_id")+" AND repos.repo_id = "+operationPlaceholder("repo_id"),
		"repo_fences.repo_id = active_repo.repo_id AND repo_fences.fence_kind = 'lifecycle'",
		"repo_fences.repo_id = active_repo.repo_id AND repo_fences.fence_kind = 'writer_session'",
		"("+sessionPlaceholder("access_mode")+" = 'read_only' OR NOT EXISTS (SELECT 1 FROM held_writer_fence))",
	)

	for _, forbidden := range []string{
		operationPlaceholder("operation_type") + " = 'export_create_committed'",
		operationPlaceholder("template_id") + " = " + operationPlaceholder("export_id"),
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("export create SQL contains wrong operation predicate %q: %s", forbidden, sql)
		}
	}
}

func TestCreateOrReuseExportSQLOnlyCreatesSessionAndAuditForNewOperation(t *testing.T) {
	sql := exportCreateOrReuseSQL()

	assertSQLContainsInOrder(t, sql,
		"inserted_operation AS (",
		"RETURNING "+strings.Join(operationSelectColumns, ", ")+", (xmax = 0) AS inserted",
		"inserted_session AS (",
		"INSERT INTO export_sessions",
		"FROM inserted_operation WHERE inserted_operation.inserted RETURNING",
		"inserted_audit AS (",
		"INSERT INTO audit_outbox",
		"FROM inserted_operation, inserted_session RETURNING audit_event_id",
	)
}

func TestCreateOrReuseExportClassifiesReplayAndRejectsHashConflict(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	request := exportCreateRequestFixture(t, now)
	record := request.Operation

	tests := []struct {
		name    string
		row     fakeRow
		reused  bool
		wantErr bool
	}{
		{name: "inserted", row: fakeRow{values: append(append(exportSessionRowValues(request.Session), operationRowValues(record)...), true)}},
		{name: "reused", row: fakeRow{values: append(append(exportSessionRowValues(request.Session), operationRowValues(record)...), false)}, reused: true},
		{name: "hash conflict", row: func() fakeRow {
			conflict := record
			conflict.RequestHash = "sha256:different"
			return fakeRow{values: append(append(exportSessionRowValues(request.Session), operationRowValues(conflict)...), false)}
		}(), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{row: tt.row}
			st := &Store{exec: exec}

			got, err := st.CreateOrReuseExport(context.Background(), request)
			if tt.wantErr {
				if !errors.Is(err, operations.ErrIdempotencyConflict) {
					t.Fatalf("error = %v, want idempotency conflict", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateOrReuseExport: %v", err)
			}
			if got.Reused != tt.reused || got.Session.ID != request.Session.ID || got.Operation.ID != record.ID {
				t.Fatalf("result = %#v, want reused=%v matching session/op", got, tt.reused)
			}
			renderedArgs := strings.ToLower(renderArgs(t, exec.args...))
			for _, forbidden := range []string{"export-password-once", "raw_path", "metadata_url", "secret_ref"} {
				if strings.Contains(renderedArgs, forbidden) {
					t.Fatalf("CreateOrReuseExport args leaked %q: %s", forbidden, renderedArgs)
				}
			}
			if !strings.Contains(renderedArgs, strings.ToLower(request.Verifier.Hash)) {
				t.Fatalf("CreateOrReuseExport args did not include verifier hash")
			}
		})
	}
}

func TestCreateOrReuseExportFallsBackToCommittedReplayWhenInsertRaceReturnsNoRows(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	request := exportCreateRequestFixture(t, now)
	record := request.Operation
	exec := &exportSequentialQueryRowExecutor{
		rows: []fakeRow{
			{err: sql.ErrNoRows},
			{values: append(append(exportSessionRowValues(request.Session), operationRowValues(record)...), false)},
		},
	}
	st := &Store{exec: exec}

	got, err := st.CreateOrReuseExport(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateOrReuseExport: %v", err)
	}
	if !got.Reused || got.Session.ID != request.Session.ID || got.Operation.ID != record.ID {
		t.Fatalf("result = %#v, want reused session/op from fallback", got)
	}
	if exec.queryRowCalls != 2 {
		t.Fatalf("query row calls = %d, want primary create plus committed replay fallback", exec.queryRowCalls)
	}
	assertSQLContainsAll(t, exec.queries[0], "INSERT INTO operations", "INSERT INTO export_sessions", "INSERT INTO audit_outbox")
	assertSQLContainsAll(t, exec.queries[1],
		"FROM operations JOIN export_sessions",
		"export_sessions.export_id = operations.export_id AND export_sessions.namespace_id = operations.namespace_id AND export_sessions.repo_id = operations.repo_id",
		"operation_type = 'export_create'",
		"false AS inserted",
	)
}

func TestGetExportSessionSelectsOnlyRedactedColumns(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	session := exportSessionFixtureForStore(now, sessionstate.ExportStatusActive)
	exec := &fakeExecutor{row: fakeRow{values: exportSessionRowValues(session)}}
	st := &Store{exec: exec}

	got, err := st.GetExportSession(context.Background(), "export_123")
	if err != nil {
		t.Fatalf("GetExportSession: %v", err)
	}
	if got.ID != "export_123" || got.Status != sessionstate.ExportStatusActive {
		t.Fatalf("session = %#v", got)
	}
	assertSQLContainsAll(t, exec.query,
		"SELECT export_id, namespace_id, repo_id, protocol, access_mode, status, expires_at, created_by_caller_service, created_by_actor_type, created_by_actor_id, revoked_at, last_accessed_at, active_request_count, active_write_count, last_observed_at, last_gateway_heartbeat_at, gateway_heartbeat_expires_at, write_drained_at, terminal_observed_at, status_reason, created_at, updated_at FROM export_sessions",
		"WHERE export_id = $1",
	)
	for _, forbidden := range []string{"verifier_hash", "verifier_salt", "secret", "password", "raw_path"} {
		if strings.Contains(strings.ToLower(exec.query), forbidden) {
			t.Fatalf("GetExportSession query leaked %q: %s", forbidden, exec.query)
		}
	}
}

func TestRevokeExportSQLUsesRevokingDrainStateNotTerminalRevoked(t *testing.T) {
	sql := exportRevokeSQL()

	assertSQLContainsInOrder(t, sql,
		"UPDATE export_sessions",
		"status = CASE",
		"WHEN status IN ('revoked','expired','failed') THEN status",
		"ELSE 'revoking' END",
		"revoked_at = COALESCE(revoked_at, $35)",
		"INSERT INTO audit_outbox",
	)
	if strings.Contains(sql, "ELSE 'revoked'") {
		t.Fatalf("revoke SQL terminalizes session: %s", sql)
	}
}

func TestRevokeExportClassifiesReplayConflictAndReturnsRevokingSession(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	request := exportRevokeRequestFixture(now)
	record := request.Operation
	session := exportSessionFixtureForStore(now, sessionstate.ExportStatusRevoking)
	session.RevokedAt = &now

	tests := []struct {
		name    string
		row     fakeRow
		reused  bool
		wantErr bool
	}{
		{name: "inserted", row: fakeRow{values: append(append(exportSessionRowValues(session), operationRowValues(record)...), true)}},
		{name: "reused", row: fakeRow{values: append(append(exportSessionRowValues(session), operationRowValues(record)...), false)}, reused: true},
		{name: "hash conflict", row: func() fakeRow {
			conflict := record
			conflict.RequestHash = "sha256:different"
			return fakeRow{values: append(append(exportSessionRowValues(session), operationRowValues(conflict)...), false)}
		}(), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{row: tt.row}
			st := &Store{exec: exec}

			got, err := st.RevokeExport(context.Background(), request)
			if tt.wantErr {
				if !errors.Is(err, operations.ErrIdempotencyConflict) {
					t.Fatalf("error = %v, want idempotency conflict", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("RevokeExport: %v", err)
			}
			if got.Reused != tt.reused || got.Session.Status != sessionstate.ExportStatusRevoking || got.Operation.ID != record.ID {
				t.Fatalf("result = %#v, want reused=%v revoking session/op", got, tt.reused)
			}
			renderedArgs := strings.ToLower(renderArgs(t, exec.args...))
			for _, forbidden := range []string{"export-password-once", "verifier_hash", "metadata_url", "raw_path"} {
				if strings.Contains(renderedArgs, forbidden) {
					t.Fatalf("RevokeExport args leaked %q: %s", forbidden, renderedArgs)
				}
			}
		})
	}
}

func TestGatewayCredentialReadsVerifierAndPayloadSubdirWithoutRawRoot(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	verifier, err := exportaccess.NewPasswordVerifier("export-password-once", []byte("fixed-test-salt-32-bytes-long!!"))
	if err != nil {
		t.Fatalf("NewPasswordVerifier: %v", err)
	}
	session := exportSessionFixtureForStore(now, sessionstate.ExportStatusActive)
	values := append(exportSessionRowValues(session), verifier.Algorithm, verifier.Hash, verifier.Salt, "vol_123", "afscp/namespaces/ns_123/repos/repo_123/payload")
	exec := &fakeExecutor{row: fakeRow{values: values}}
	st := &Store{exec: exec}

	got, err := st.GetExportGatewayCredential(context.Background(), "export_123")
	if err != nil {
		t.Fatalf("GetExportGatewayCredential: %v", err)
	}
	if !got.Verifier.Verify("export-password-once") || got.PayloadVolumeSubdir == "" || got.VolumeID != "vol_123" {
		t.Fatalf("gateway credential = %#v", got)
	}
	assertSQLContainsAll(t, exec.query,
		"JOIN repos",
		"verifier_hash",
		"verifier_salt",
		"payload_volume_subdir",
	)
	if strings.Contains(strings.ToLower(exec.query), "raw_path") || strings.Contains(strings.ToLower(exec.query), "metadata_url") {
		t.Fatalf("gateway credential SQL leaked raw storage term: %s", exec.query)
	}
}

func TestGatewayCredentialSQLFailsClosedOnInactiveNamespaceBindingOrSession(t *testing.T) {
	query := exportGatewayCredentialSQL()

	assertSQLContainsInOrder(t, query,
		"FROM export_sessions s",
		"JOIN namespaces ns ON ns.namespace_id = s.namespace_id AND ns.status = 'active'",
		"JOIN namespace_volume_bindings nvb ON nvb.namespace_id = s.namespace_id AND nvb.status = 'active'",
		"COALESCE((nvb.export_policy->>'webdav_enabled')::boolean, false) = true",
		"JOIN repos r ON r.namespace_id = s.namespace_id AND r.repo_id = s.repo_id",
		"JOIN volumes v ON v.volume_id = r.volume_id AND v.status = 'active'",
		"COALESCE((v.capabilities->>'webdav_export')::boolean, false) = true",
		"WHERE s.export_id = $1",
		"s.status = 'active'",
		"s.protocol = 'webdav'",
		"r.repo_kind = 'repo'",
		"r.status = 'active'",
		"r.lifecycle_status = 'active'",
	)

	st := &Store{exec: &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}}
	_, err := st.GetExportGatewayCredential(context.Background(), "export_123")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetExportGatewayCredential error = %v, want sql.ErrNoRows when predicates fail", err)
	}
}

func TestStoreDoesNotExposeLegacyExportTerminalHelper(t *testing.T) {
	if method, ok := reflect.TypeOf((*Store)(nil)).MethodByName("MarkExportTerminal"); ok {
		t.Fatalf("postgres.Store exposes legacy export terminal helper: %s", method.Name)
	}
}

func TestRecordExportRuntimeObservationAppliesAtomicDeltasHeartbeatAndWriteDrain(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	session := exportSessionFixtureForStore(now, sessionstate.ExportStatusActive)
	session.ActiveRequestCount = 2
	session.ActiveWriteCount = 0
	session.LastObservedAt = &now
	session.LastGatewayHeartbeatAt = &now
	heartbeatExpires := now.Add(time.Minute)
	session.GatewayHeartbeatExpiresAt = &heartbeatExpires
	session.WriteDrainedAt = &now
	accessedAt := now.Add(-time.Second)
	exec := &fakeExecutor{row: fakeRow{values: exportSessionRowValues(session)}}
	st := &Store{exec: exec}

	got, err := st.RecordExportRuntimeObservation(context.Background(), exportaccess.RuntimeObservation{
		ExportID:                    "export_123",
		ObservedAt:                  now,
		ActiveRequestDelta:          -1,
		ActiveWriteDelta:            -1,
		GatewayHeartbeatAt:          &now,
		GatewayHeartbeatExpiresAt:   &heartbeatExpires,
		SuccessfulRequestAccessedAt: &accessedAt,
	})
	if err != nil {
		t.Fatalf("RecordExportRuntimeObservation: %v", err)
	}
	if got.ActiveRequestCount != 2 || got.ActiveWriteCount != 0 || got.WriteDrainedAt == nil {
		t.Fatalf("session runtime = %#v", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"UPDATE export_sessions",
		"active_request_count = active_request_count + $2",
		"active_write_count = active_write_count + $3",
		"last_observed_at = GREATEST(COALESCE(last_observed_at, $4), $4)",
		"last_gateway_heartbeat_at = CASE WHEN $5::timestamptz IS NULL THEN last_gateway_heartbeat_at ELSE GREATEST(COALESCE(last_gateway_heartbeat_at, $5), $5) END",
		"gateway_heartbeat_expires_at = CASE WHEN $6::timestamptz IS NULL THEN gateway_heartbeat_expires_at ELSE GREATEST(COALESCE(gateway_heartbeat_expires_at, $6), $6) END",
		"write_drained_at = CASE WHEN active_write_count + $3 = 0 THEN GREATEST(COALESCE(write_drained_at, $4), $4) ELSE NULL END",
		"last_accessed_at = CASE WHEN $7::timestamptz IS NULL THEN last_accessed_at ELSE GREATEST(COALESCE(last_accessed_at, $7), $7) END",
		"RETURNING",
	)
	assertSQLContainsAll(t, exec.query,
		"active_request_count + $2 >= 0",
		"active_write_count + $3 >= 0",
		"active_write_count + $3 <= active_request_count + $2",
	)
}

func TestRecordExportRuntimeObservationPositiveDeltaRequiresActiveUnexpiredAdmission(t *testing.T) {
	sql := exportRuntimeObservationSQL()

	assertSQLContainsAll(t, sql,
		"($2 <= 0 AND $3 <= 0) OR (status = 'active' AND expires_at > $4)",
		"active_request_count + $2 >= 0",
		"active_write_count + $3 >= 0",
		"active_write_count + $3 <= active_request_count + $2",
	)
}

func TestRecordExportRuntimeObservationRejectsInvalidCountsBeforeSQL(t *testing.T) {
	st := &Store{exec: &fakeExecutor{}}
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	for _, observation := range []exportaccess.RuntimeObservation{
		{ExportID: "export_123", ObservedAt: now, ActiveRequestDelta: 0, ActiveWriteDelta: 1},
		{ExportID: "export_123", ObservedAt: now, ActiveRequestDelta: -1, ActiveWriteDelta: 1},
	} {
		if _, err := st.RecordExportRuntimeObservation(context.Background(), observation); err == nil {
			t.Fatalf("RecordExportRuntimeObservation accepted invalid delta: %#v", observation)
		}
	}
}

func TestReconcileExportSessionTerminalSQLCommitsOperationSessionAndAudit(t *testing.T) {
	sql := exportReconcileTerminalSQL()
	assertSQLContainsInOrder(t, sql,
		"WITH existing_operation AS (",
		"operation_type = 'export_session_reconcile'",
		"eligible_session AS (",
		"FOR UPDATE",
		"inserted_operation AS (",
		"INSERT INTO operations",
		"AND EXISTS (SELECT 1 FROM eligible_session)",
		"updated_session AS (",
		"UPDATE export_sessions SET status = $33",
		"terminal_observed_at = $35",
		"active_request_count = 0",
		"active_write_count = 0",
		"INSERT INTO audit_outbox",
	)
	assertSQLContainsAll(t, sql,
		"active_request_count = 0",
		"active_write_count = 0",
		"$33 = 'revoked' AND status = 'revoking' AND revoked_at IS NOT NULL",
		"$33 = 'expired' AND status = 'active' AND expires_at <= $35",
		"$33 = 'failed' AND btrim($36) <> ''",
	)
	for _, forbidden := range []string{"gateway_heartbeat_expires_at >= $35", "last_observed_at IS NULL", "last_observed_at IS NOT NULL"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("terminal reconcile SQL still depends on heartbeat freshness: %s", sql)
		}
	}
}

func TestListExportSessionsForTerminalReconcileFindsZeroCountRevokingAndExpiredWithoutHeartbeat(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	revoking := exportSessionFixtureForStore(now, sessionstate.ExportStatusRevoking)
	revoking.ExpiresAt = now.Add(time.Hour)
	expired := exportSessionFixtureForStore(now, sessionstate.ExportStatusActive)
	expired.ID = "export_456"
	expired.ExpiresAt = now.Add(-time.Second)
	exec := &fakeExecutor{
		rows: fakeRows{rows: []fakeRow{
			{values: exportSessionRowValues(revoking)},
			{values: exportSessionRowValues(expired)},
		}},
	}
	st := &Store{exec: exec}

	got, err := st.ListExportSessionsForTerminalReconcile(context.Background(), now, 50)
	if err != nil {
		t.Fatalf("ListExportSessionsForTerminalReconcile: %v", err)
	}
	if len(got) != 2 || got[0].Status != sessionstate.ExportStatusRevoking || got[1].ID != "export_456" {
		t.Fatalf("sessions = %#v, want revoking and expired active", got)
	}
	assertSQLContainsAll(t, exec.query,
		"FROM export_sessions",
		"WHERE active_request_count = 0 AND active_write_count = 0",
		"AND (status = 'revoking' OR (status = 'active' AND expires_at <= $1))",
		"ORDER BY updated_at, export_id LIMIT $2",
	)
	whereClause := exec.query
	if idx := strings.Index(whereClause, " WHERE "); idx >= 0 {
		whereClause = whereClause[idx:]
	}
	if strings.Contains(whereClause, "heartbeat") {
		t.Fatalf("terminalize listing predicate depends on heartbeat freshness: %s", exec.query)
	}
}

func TestReconcileExportSessionTerminalRejectsActiveCountsBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	request := exportReconcileRequestFixture(now, sessionstate.ExportStatusRevoked)
	request.ActiveRequestCount = 1
	st := &Store{exec: &fakeExecutor{}}

	if _, err := st.ReconcileExportSessionTerminal(context.Background(), request); err == nil {
		t.Fatal("ReconcileExportSessionTerminal accepted active_request_count > 0")
	}
}

func TestReconcileExportSessionTerminalReturnsOperationAuditBoundary(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	request := exportReconcileRequestFixture(now, sessionstate.ExportStatusRevoked)
	session := exportSessionFixtureForStore(now, sessionstate.ExportStatusRevoked)
	session.ActiveRequestCount = 0
	session.ActiveWriteCount = 0
	session.TerminalObservedAt = &now
	session.StatusReason = "gateway drained after revoke"
	exec := &fakeExecutor{row: fakeRow{values: append(append(exportSessionRowValues(session), operationRowValues(request.Operation)...), true)}}
	st := &Store{exec: exec}

	got, err := st.ReconcileExportSessionTerminal(context.Background(), request)
	if err != nil {
		t.Fatalf("ReconcileExportSessionTerminal: %v", err)
	}
	if got.Session.Status != sessionstate.ExportStatusRevoked || got.Session.TerminalObservedAt == nil || got.Operation.ID != request.Operation.ID {
		t.Fatalf("result = %#v", got)
	}
	renderedArgs := strings.ToLower(renderArgs(t, exec.args...))
	for _, forbidden := range []string{"export-password-once", "verifier_hash", "metadata_url", "raw_path"} {
		if strings.Contains(renderedArgs, forbidden) {
			t.Fatalf("Reconcile args leaked %q: %s", forbidden, renderedArgs)
		}
	}
}

func exportCreateRequestFixture(t *testing.T, now time.Time) exportaccess.CreateRequest {
	t.Helper()
	session := exportSessionFixtureForStore(now, sessionstate.ExportStatusActive)
	verifier, err := exportaccess.NewPasswordVerifier("export-password-once", []byte("fixed-test-salt-32-bytes-long!!"))
	if err != nil {
		t.Fatalf("NewPasswordVerifier: %v", err)
	}
	finished := now
	record := operations.OperationRecord{
		ID:                  "op_export",
		Type:                operations.OperationExportCreate,
		State:               operations.OperationStateSucceeded,
		Phase:               operations.OperationPhaseExportCreateCommitted,
		Attempt:             1,
		IdempotencyScope:    operations.NewIdempotencyScope("agentsmith-api", "ns_123", operations.OperationExportCreate, "idem_export").String(),
		IdempotencyKey:      "idem_export",
		RequestHash:         "sha256:export",
		CorrelationID:       "corr_export",
		CallerService:       "agentsmith-api",
		AuthorizedActor:     operations.Actor{Type: "user", ID: "user_123"},
		Resource:            operations.ResourceRef{Type: "export", ID: "export_123"},
		NamespaceID:         "ns_123",
		RepoID:              "repo_123",
		ExportID:            "export_123",
		ExternalResourceIDs: map[string]string{},
		InputSummary:        map[string]any{"export_id": "export_123", "namespace_id": "ns_123", "repo_id": "repo_123", "mode": "read_write"},
		CreatedAt:           now,
		StartedAt:           &finished,
		FinishedAt:          &finished,
	}
	event := audit.Event{
		EventID:         "evt_export",
		Type:            audit.EventTypeExportCreate,
		Time:            now,
		CallerService:   record.CallerService,
		AuthorizedActor: audit.Actor{Type: "user", ID: "user_123"},
		CorrelationID:   record.CorrelationID,
		OperationID:     record.ID,
		Resource:        audit.Resource{Type: "export", ID: "export_123", NamespaceID: "ns_123"},
		Outcome:         audit.OutcomeSucceeded,
		Reason:          "export credential issued",
		Details:         map[string]any{"export_id": "export_123"},
	}
	return exportaccess.CreateRequest{Session: session, Verifier: verifier, Operation: record, Audit: event}
}

func exportRevokeRequestFixture(now time.Time) exportaccess.RevokeRequest {
	finished := now
	record := operations.OperationRecord{
		ID:                  "op_revoke_export",
		Type:                operations.OperationExportRevoke,
		State:               operations.OperationStateSucceeded,
		Phase:               operations.OperationPhaseExportRevokeCommitted,
		Attempt:             1,
		IdempotencyScope:    operations.NewIdempotencyScope("agentsmith-api", "ns_123", operations.OperationExportRevoke, "idem_revoke_export").String(),
		IdempotencyKey:      "idem_revoke_export",
		RequestHash:         "sha256:export-revoke",
		CorrelationID:       "corr_export",
		CallerService:       "agentsmith-api",
		AuthorizedActor:     operations.Actor{Type: "user", ID: "user_123"},
		Resource:            operations.ResourceRef{Type: "export", ID: "export_123"},
		NamespaceID:         "ns_123",
		RepoID:              "repo_123",
		ExportID:            "export_123",
		ExternalResourceIDs: map[string]string{},
		InputSummary:        map[string]any{"export_id": "export_123", "repo_id": "repo_123", "target_status": "revoking"},
		CreatedAt:           now,
		StartedAt:           &finished,
		FinishedAt:          &finished,
	}
	event := audit.Event{
		EventID:         "evt_revoke_export",
		Type:            audit.EventTypeExportRevoke,
		Time:            now,
		CallerService:   record.CallerService,
		AuthorizedActor: audit.Actor{Type: "user", ID: "user_123"},
		CorrelationID:   record.CorrelationID,
		OperationID:     record.ID,
		Resource:        audit.Resource{Type: "export", ID: "export_123", NamespaceID: "ns_123"},
		Outcome:         audit.OutcomeSucceeded,
		Reason:          "export credential disabled",
		Details:         map[string]any{"export_id": "export_123", "target_status": "revoking"},
	}
	return exportaccess.RevokeRequest{ExportID: "export_123", NamespaceID: "ns_123", Operation: record, Audit: event, Now: now}
}

func exportReconcileRequestFixture(now time.Time, status sessionstate.ExportStatus) exportaccess.ReconcileRequest {
	finished := now
	reason := "gateway drained after revoke"
	record := operations.OperationRecord{
		ID:                  "op_reconcile_export",
		Type:                operations.OperationExportSessionReconcile,
		State:               operations.OperationStateSucceeded,
		Phase:               operations.OperationPhaseExportSessionReconcileCommitted,
		Attempt:             1,
		IdempotencyScope:    operations.NewIdempotencyScope("agentsmith-gateway", "ns_123", operations.OperationExportSessionReconcile, "idem_reconcile_export").String(),
		IdempotencyKey:      "idem_reconcile_export",
		RequestHash:         "sha256:export-reconcile",
		CorrelationID:       "corr_export",
		CallerService:       "agentsmith-gateway",
		AuthorizedActor:     operations.Actor{Type: "service", ID: "afscp-export-gateway"},
		Resource:            operations.ResourceRef{Type: "export", ID: "export_123"},
		NamespaceID:         "ns_123",
		RepoID:              "repo_123",
		ExportID:            "export_123",
		ExternalResourceIDs: map[string]string{},
		InputSummary:        map[string]any{"export_id": "export_123", "repo_id": "repo_123", "target_status": string(status)},
		CreatedAt:           now,
		StartedAt:           &finished,
		FinishedAt:          &finished,
	}
	event := audit.Event{
		EventID:         "evt_reconcile_export",
		Type:            audit.EventTypeExportSessionReconcile,
		Time:            now,
		CallerService:   record.CallerService,
		AuthorizedActor: audit.Actor{Type: "service", ID: "afscp-export-gateway"},
		CorrelationID:   record.CorrelationID,
		OperationID:     record.ID,
		Resource:        audit.Resource{Type: "export", ID: "export_123", NamespaceID: "ns_123"},
		Outcome:         audit.OutcomeSucceeded,
		Reason:          reason,
		Details:         map[string]any{"export_id": "export_123", "target_status": string(status)},
	}
	return exportaccess.ReconcileRequest{
		ExportID:           "export_123",
		NamespaceID:        "ns_123",
		TargetStatus:       status,
		ObservedAt:         now,
		StatusReason:       reason,
		ActiveRequestCount: 0,
		ActiveWriteCount:   0,
		Operation:          record,
		Audit:              event,
	}
}

func exportSessionFixtureForStore(now time.Time, status sessionstate.ExportStatus) exportaccess.Session {
	return exportaccess.Session{
		ID:                     "export_123",
		NamespaceID:            "ns_123",
		RepoID:                 "repo_123",
		Protocol:               exportaccess.ProtocolWebDAV,
		Mode:                   sessionstate.AccessModeReadWrite,
		Status:                 status,
		ExpiresAt:              now.Add(time.Hour),
		CreatedByCallerService: "agentsmith-api",
		CreatedByActor:         exportaccess.Actor{Type: "user", ID: "user_123"},
		StatusReason:           "",
		CreatedAt:              now,
		UpdatedAt:              now,
	}
}

func exportSessionRowValues(session exportaccess.Session) []any {
	return []any{
		session.ID,
		session.NamespaceID,
		session.RepoID,
		string(session.Protocol),
		string(session.Mode),
		string(session.Status),
		session.ExpiresAt,
		session.CreatedByCallerService,
		session.CreatedByActor.Type,
		session.CreatedByActor.ID,
		timePtrArg(session.RevokedAt),
		timePtrArg(session.LastAccessedAt),
		session.ActiveRequestCount,
		session.ActiveWriteCount,
		timePtrArg(session.LastObservedAt),
		timePtrArg(session.LastGatewayHeartbeatAt),
		timePtrArg(session.GatewayHeartbeatExpiresAt),
		timePtrArg(session.WriteDrainedAt),
		timePtrArg(session.TerminalObservedAt),
		session.StatusReason,
		session.CreatedAt,
		session.UpdatedAt,
	}
}

func assertSQLContainsAll(t *testing.T, sql string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(sql, part) {
			t.Fatalf("SQL %q missing %q", sql, part)
		}
	}
}

func placeholderForColumn(t *testing.T, columns []string, start int) func(string) string {
	t.Helper()
	placeholdersByColumn := make(map[string]string, len(columns))
	for idx, column := range columns {
		placeholdersByColumn[column] = "$" + strconv.Itoa(start+idx)
	}
	return func(column string) string {
		t.Helper()
		placeholder, ok := placeholdersByColumn[column]
		if !ok {
			t.Fatalf("unknown column %q", column)
		}
		return placeholder
	}
}

type exportSequentialQueryRowExecutor struct {
	queries       []string
	args          [][]any
	rows          []fakeRow
	queryRowCalls int
}

func (exec *exportSequentialQueryRowExecutor) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return fakeResult{}, nil
}

func (exec *exportSequentialQueryRowExecutor) QueryContext(context.Context, string, ...any) (rowsScanner, error) {
	return &fakeRows{}, nil
}

func (exec *exportSequentialQueryRowExecutor) QueryRowContext(_ context.Context, query string, args ...any) rowScanner {
	exec.queries = append(exec.queries, query)
	exec.args = append(exec.args, args)
	call := exec.queryRowCalls
	exec.queryRowCalls++
	if call >= len(exec.rows) {
		return fakeRow{err: sql.ErrNoRows}
	}
	return exec.rows[call]
}
