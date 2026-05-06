package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func TestAcquireSavePointCreateOperationLeaseSerializesEarlierLifecycleAndJVSMutations(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := savePointOperationRecord(now, operations.OperationStateRunning, operations.OperationPhaseSavePointCreateValidate)
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.AcquireSavePointCreateOperationLease(context.Background(), record.ID, operations.LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now})
	if err != nil {
		t.Fatalf("AcquireSavePointCreateOperationLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'save_point_create'",
		"phase IN ('validate_save_point_create','save_point_create_prepared')",
		"earlier_jvs_mutation AS",
		"o.operation_id <> e.operation_id",
		"o.operation_type IN ('save_point_create', 'restore_preview', 'restore_preview_discard', 'restore_run', 'template_create', 'template_clone')",
		"earlier_repo_lifecycle AS",
		"o.operation_id <> e.operation_id",
		"o.operation_type IN ('repo_archive', 'repo_restore_archived', 'repo_delete', 'repo_restore_tombstoned', 'repo_purge')",
		"operation_state NOT IN ('succeeded','failed','cancelled')",
		"NOT EXISTS (SELECT 1 FROM earlier_jvs_mutation)",
		"NOT EXISTS (SELECT 1 FROM earlier_repo_lifecycle)",
		"RETURNING",
	)
	if strings.Contains(exec.query, "repo_fences") {
		t.Fatalf("save point acquire must not introduce a fence kind: %s", exec.query)
	}
}

func TestCommitSavePointCreateSucceededRequiresPreparedStoredMarkerBoundary(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := savePointOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseSavePointCreateCommitted)
	record.VerificationResult = map[string]any{
		"pre_save_history_captured":     true,
		"pre_save_newest_save_point_id": "",
		"save_point_id":                 "sp_001",
		"created_at":                    "2026-05-05T12:00:00Z",
	}
	record.ExternalResourceIDs = map[string]string{"save_point_id": "sp_001"}
	record.JVSJSONOutput = map[string]any{"save_point_id": "sp_001"}
	record.FinishedAt = &now
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.CommitSavePointCreateSucceededWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, savePointAudit(record, audit.OutcomeSucceeded, now))
	if err != nil {
		t.Fatalf("CommitSavePointCreateSucceededWithLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_state = 'running'",
		"operation_type = 'save_point_create'",
		"phase = 'save_point_create_prepared'",
		"verification_result->>'pre_save_history_captured' = 'true'",
		"FOR UPDATE",
		"updated_operation AS",
		"INSERT INTO audit_outbox",
	)
	if strings.Contains(exec.query, "phase IN ('validate_save_point_create','save_point_create_prepared')") {
		t.Fatalf("success commit SQL allows validate phase: %s", exec.query)
	}
}

func TestSavePointCreateSuccessValidatorRequiresSafePreSaveMarker(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	valid := savePointOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseSavePointCreateCommitted)
	valid.VerificationResult = map[string]any{"pre_save_history_captured": true, "pre_save_newest_save_point_id": ""}
	if err := validateSavePointCreateSuccessRecord(valid); err != nil {
		t.Fatalf("validateSavePointCreateSuccessRecord with captured empty pre-save pointer: %v", err)
	}

	for _, verification := range []any{
		nil,
		map[string]any{},
		map[string]any{"pre_save_history_captured": false},
		map[string]any{"pre_save_history_captured": "true"},
	} {
		t.Run("missing_marker", func(t *testing.T) {
			record := valid
			record.VerificationResult = verification
			if err := validateSavePointCreateSuccessRecord(record); err == nil {
				t.Fatalf("validateSavePointCreateSuccessRecord accepted verification %#v", verification)
			}
		})
	}
}

func TestCommitSavePointCreateFailedAllowsValidateOrPreparedStoredPhase(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := savePointOperationRecord(now, operations.OperationStateFailed, operations.OperationPhaseSavePointCreateValidate)
	record.Error = &operations.OperationError{Code: "SAVE_POINT_VALIDATION_FAILED", Message: "failed", CorrelationID: record.CorrelationID, OperationID: record.ID}
	record.FinishedAt = &now
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.CommitSavePointCreateFailedWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, savePointAudit(record, audit.OutcomeFailed, now))
	if err != nil {
		t.Fatalf("CommitSavePointCreateFailedWithLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"operation_type = 'save_point_create'",
		"phase IN ('validate_save_point_create','save_point_create_prepared')",
		"INSERT INTO audit_outbox",
	)
	if strings.Contains(exec.query, "pre_save_history_captured") {
		t.Fatalf("failure commit SQL must not require success marker: %s", exec.query)
	}
}

func TestCommitSavePointCreateSucceededRejectsMissingMarkerBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := savePointOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseSavePointCreateCommitted)
	exec := &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}
	st := &Store{exec: exec}

	_, err := st.CommitSavePointCreateSucceededWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, savePointAudit(record, audit.OutcomeSucceeded, now))
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("CommitSavePointCreateSucceededWithLease error = %v, want validation error before SQL", err)
	}
	if exec.query != "" {
		t.Fatalf("issued SQL for invalid success record: %s", exec.query)
	}
}

func savePointOperationRecord(now time.Time, state operations.OperationState, phase string) operations.OperationRecord {
	lease := now.Add(time.Minute)
	started := now.Add(-time.Minute)
	return operations.OperationRecord{
		ID:                  "op_savepoint",
		Type:                operations.OperationSavePointCreate,
		State:               state,
		Phase:               phase,
		LeaseOwner:          "worker-a",
		LeaseExpiresAt:      &lease,
		Attempt:             1,
		IdempotencyScope:    operations.NewIdempotencyScope("agentsmith-api", "ns_alpha", operations.OperationSavePointCreate, "idem").String(),
		IdempotencyKey:      "idem",
		RequestHash:         "sha256:savepoint",
		CorrelationID:       "corr-savepoint",
		CallerService:       "agentsmith-api",
		AuthorizedActor:     operations.Actor{Type: "user", ID: "user_123"},
		Resource:            operations.ResourceRef{Type: "repo", ID: "repo_alpha"},
		NamespaceID:         "ns_alpha",
		RepoID:              "repo_alpha",
		InputSummary:        map[string]any{"message": "checkpoint"},
		ExternalResourceIDs: map[string]string{},
		StartedAt:           &started,
		CreatedAt:           now.Add(-time.Hour),
	}
}

func savePointAudit(record operations.OperationRecord, outcome audit.Outcome, now time.Time) audit.Event {
	return audit.NewEvent(audit.Event{EventID: "evt_savepoint", Type: audit.EventTypeSavePointCreate, Time: now, CallerService: record.CallerService, AuthorizedActor: audit.Actor{Type: record.AuthorizedActor.Type, ID: record.AuthorizedActor.ID}, CorrelationID: record.CorrelationID, OperationID: record.ID, Resource: audit.Resource{Type: "repo", ID: record.RepoID, NamespaceID: record.NamespaceID}, Outcome: outcome, Reason: "save_point_create"})
}
