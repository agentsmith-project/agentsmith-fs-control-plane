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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operatorrepair"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

func TestStoreImplementsOperatorRepairStore(t *testing.T) {
	var _ store.OperatorRepairStore = (*Store)(nil)
}

func TestCommitOperatorRepairFailedUsesAtomicCASAndAuditOutbox(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	after := operatorRepairEligibleOperation()
	after.State = operations.OperationStateFailed
	after.Phase = "operator_repair_terminalized_failed"
	after.FinishedAt = &now
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(after)}}
	st := &Store{exec: exec}

	got, err := st.CommitOperatorRepairFailed(context.Background(), operatorrepair.CommitRequest{
		OperationID: "op_123",
		Before:      operatorRepairEligibleOperation(),
		After:       after,
		Event:       operatorRepairAuditEvent("audit_123", "op_123", now),
		Now:         now,
	})
	if err != nil {
		t.Fatalf("CommitOperatorRepairFailed: %v", err)
	}
	if got.ID != "op_123" || got.State != operations.OperationStateFailed {
		t.Fatalf("updated operation = %#v, want failed op_123", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"FROM operations",
		"operation_state = 'operator_intervention_required'",
		"error_json->>'code' = 'OPERATION_RECOVERY_REQUIRED'",
		"session_fence_id IS NULL",
		"FOR UPDATE",
		"UPDATE operations SET",
		"operation_state = $1",
		"failed",
		"INSERT INTO audit_outbox",
		"SELECT",
		"FROM updated_operation",
		"SELECT "+strings.Join(operationSelectColumns, ", "),
	)
	for _, forbidden := range []string{"UPDATE repo", "UPDATE restore_plans", "UPDATE export_sessions", "UPDATE workload_mount_bindings", "DELETE ", "repo_fences"} {
		if strings.Contains(strings.ToLower(exec.query), strings.ToLower(forbidden)) {
			t.Fatalf("operator repair SQL contains forbidden side-effect %q: %s", forbidden, exec.query)
		}
	}
	if len(exec.args) <= 12 {
		t.Fatalf("args = %#v, want operation update plus audit outbox args", exec.args)
	}
	renderedArgs := strings.ToLower(renderArgs(t, exec.args...))
	for _, forbidden := range []string{"bearer", "secret", "/var/lib", ".jvs"} {
		if strings.Contains(renderedArgs, forbidden) {
			t.Fatalf("operator repair args leaked %q in %s", forbidden, renderedArgs)
		}
	}
}

func TestCommitOperatorRepairFailedRequiresSafeInterventionShapeBeforeSQL(t *testing.T) {
	tests := []struct {
		name string
		edit func(*operations.OperationRecord)
	}{
		{name: "not intervention", edit: func(record *operations.OperationRecord) { record.State = operations.OperationStateRunning }},
		{name: "missing recovery marker", edit: func(record *operations.OperationRecord) { record.Error.Details["reason"] = "manual" }},
		{name: "session fence", edit: func(record *operations.OperationRecord) { record.SessionFenceID = "fence_123" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := operatorRepairEligibleOperation()
			tt.edit(&before)
			exec := &fakeExecutor{}
			st := &Store{exec: exec}

			after := before
			after.State = operations.OperationStateFailed
			_, err := st.CommitOperatorRepairFailed(context.Background(), operatorrepair.CommitRequest{
				OperationID: "op_123",
				Before:      before,
				After:       after,
				Event:       operatorRepairAuditEvent("audit_123", "op_123", time.Now()),
				Now:         time.Now(),
			})
			if !errors.Is(err, operatorrepair.ErrUnsafeIntervention) {
				t.Fatalf("CommitOperatorRepairFailed error = %v, want ErrUnsafeIntervention", err)
			}
			if exec.queryRowCalls != 0 {
				t.Fatalf("queryRowCalls = %d, want validation before SQL", exec.queryRowCalls)
			}
		})
	}
}

func TestCommitOperatorRepairFailedCASRejectsConcurrentAmbiguousPhase(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	before := operatorRepairEligibleOperation()
	after := before
	after.State = operations.OperationStateFailed
	after.Phase = "operator_repair_terminalized_failed"
	after.FinishedAt = &now
	exec := &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}
	st := &Store{exec: exec}

	_, err := st.CommitOperatorRepairFailed(context.Background(), operatorrepair.CommitRequest{
		OperationID: "op_123",
		Before:      before,
		After:       after,
		Event:       operatorRepairAuditEvent("audit_123", "op_123", now),
		Now:         now,
	})
	if !errors.Is(err, operations.ErrLeaseUnavailable) {
		t.Fatalf("CommitOperatorRepairFailed error = %v, want fail-closed lease unavailable on stale/unsafe phase", err)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_id = $12",
		"phase = $",
		"LOWER(phase) NOT LIKE '%writer_fenced%'",
		"LOWER(phase) NOT LIKE '%consuming%'",
		"LOWER(phase) NOT LIKE '%discarding%'",
		"LOWER(phase) NOT LIKE '%committed%'",
		"FOR UPDATE",
	)
	if strings.Contains(exec.query, "NOT IN ('writer_fenced','consuming','discarding','committed')") {
		t.Fatalf("operator repair CAS uses exact phase NOT IN instead of substring unsafe-phase rejection: %s", exec.query)
	}
	if !strings.Contains(renderArgs(t, exec.args...), before.Phase) {
		t.Fatalf("operator repair CAS args = %#v, want validated before phase %q", exec.args, before.Phase)
	}
}

func TestCommitOperatorRepairFailedNoRowsFailsClosed(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	before := operatorRepairEligibleOperation()
	after := before
	after.State = operations.OperationStateFailed
	exec := &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}
	st := &Store{exec: exec}

	_, err := st.CommitOperatorRepairFailed(context.Background(), operatorrepair.CommitRequest{
		OperationID: "op_123",
		Before:      before,
		After:       after,
		Event:       operatorRepairAuditEvent("audit_123", "op_123", now),
		Now:         now,
	})
	if !errors.Is(err, operations.ErrLeaseUnavailable) {
		t.Fatalf("CommitOperatorRepairFailed error = %v, want lease unavailable fail-closed", err)
	}
}

func operatorRepairEligibleOperation() operations.OperationRecord {
	record := operationFixture(time.Date(2026, 5, 8, 11, 0, 0, 0, time.UTC))
	record.ID = "op_123"
	record.Type = operations.OperationRepoCreate
	record.State = operations.OperationStateOperatorInterventionRequired
	record.Phase = operations.OperationPhaseRepoCreateValidate
	record.LeaseOwner = ""
	record.LeaseExpiresAt = nil
	record.SessionFenceID = ""
	record.Error = &operations.OperationError{
		Code:        "OPERATION_RECOVERY_REQUIRED",
		Message:     "operation recovery is unsupported; operator intervention required",
		OperationID: "op_123",
		Details: map[string]any{
			"reason":   "unsupported_operation_recovery",
			"evidence": "worker_recovery_disabled",
		},
	}
	record.VerificationResult = map[string]any{
		"reason":   "unsupported_operation_recovery",
		"evidence": "worker_recovery_disabled",
	}
	return record
}

func operatorRepairAuditEvent(eventID, operationID string, now time.Time) audit.Event {
	return audit.NewEvent(audit.Event{
		EventID:         eventID,
		Type:            audit.EventTypeRepoCreate,
		Time:            now,
		CallerService:   "ops-service",
		AuthorizedActor: audit.Actor{Type: "operator", ID: "ops-user"},
		CorrelationID:   "corr_123",
		OperationID:     operationID,
		Resource:        audit.Resource{Type: "repo", ID: "repo_123", NamespaceID: "ns_123"},
		Outcome:         audit.OutcomeFailed,
		Reason:          "operator_repair_terminalized_failed",
		Details: map[string]any{
			"repair_action": "terminalize_unsupported_intervention_as_failed",
			"evidence_ref":  "docs/runbooks/ga-runbooks.md#op-123",
		},
	})
}
