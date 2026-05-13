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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestCommitNamespaceVolumeBindingPutWithLeaseAtomicallyUpsertsBindingUpdatesOperationAndAppendsAudit(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	binding := bindingFixture()
	binding.CreatedAt = now
	binding.UpdatedAt = now
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-binding"
	record.Type = operations.OperationNamespaceVolumeBindingPut
	record.State = operations.OperationStateSucceeded
	record.Phase = operations.OperationPhaseNamespaceVolumeBindingPutCommitted
	record.NamespaceID = binding.NamespaceID
	record.Resource = operations.ResourceRef{Type: "namespace_volume_binding", ID: binding.NamespaceID}
	record.LeaseOwner = ""
	record.LeaseExpiresAt = nil
	record.FinishedAt = &now

	exec := &fakeExecutor{row: fakeRow{values: append(bindingRowValues(binding), operationRowValues(record.SanitizedForPersistence().Record())...)}}
	st := &Store{exec: exec}

	gotBinding, gotOperation, err := st.CommitNamespaceVolumeBindingPutWithLease(context.Background(), binding, record.SanitizedForPersistence(), "worker-a", now, namespaceVolumeBindingPutAuditEvent("audit-binding", "op-binding", binding.NamespaceID, now))
	if err != nil {
		t.Fatalf("CommitNamespaceVolumeBindingPutWithLease: %v", err)
	}
	if gotBinding.NamespaceID != binding.NamespaceID || gotBinding.DefaultVolumeID != binding.DefaultVolumeID {
		t.Fatalf("binding = %#v, want committed fixture", gotBinding)
	}
	if gotOperation.ID != "op-binding" || gotOperation.State != operations.OperationStateSucceeded || gotOperation.LeaseOwner != "" {
		t.Fatalf("operation = %#v, want succeeded with cleared lease", gotOperation)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH active_namespace AS (",
		"SELECT namespace_id FROM namespaces WHERE namespace_id = $14 AND status = 'active'",
		"), active_volume AS (",
		"SELECT volume_id FROM volumes WHERE volume_id = $20 AND status = 'active'",
		"), updated_operation AS (",
		"UPDATE operations SET",
		"WHERE operation_id = $12",
		"operation_state = 'running'",
		"lease_owner = $13",
		"lease_expires_at IS NOT NULL",
		"lease_expires_at > $11",
		"EXISTS (SELECT 1 FROM active_namespace)",
		"EXISTS (SELECT 1 FROM active_volume)",
		"operation_type = 'namespace_volume_binding_put'",
		"phase = 'validate_namespace_volume_binding_put'",
		"namespace_id = $14",
		"resource_type = 'namespace_volume_binding'",
		"resource_id = $14",
		"caller_service = $15",
		"correlation_id = $16",
		"authorized_actor_type = $17",
		"authorized_actor_id = $18",
		"RETURNING",
		"), upserted_binding AS (",
		"INSERT INTO namespace_volume_bindings",
		"SELECT $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29 FROM updated_operation, active_namespace, active_volume",
		"ON CONFLICT (namespace_id) DO UPDATE SET",
		"RETURNING",
		"), inserted_audit AS (",
		"INSERT INTO audit_outbox",
		"SELECT",
		"FROM updated_operation, upserted_binding",
		"RETURNING audit_event_id",
		") SELECT",
		"FROM upserted_binding, updated_operation",
		"WHERE EXISTS (SELECT 1 FROM inserted_audit)",
	)
	if exec.queryRowCalls != 1 || exec.execCalls != 0 || exec.queryCalls != 0 {
		t.Fatalf("executor calls queryRow/exec/query = %d/%d/%d, want 1/0/0", exec.queryRowCalls, exec.execCalls, exec.queryCalls)
	}
	wantArgs := len(operationLeaseFencedUpdateArgsForTest(t, record, "worker-a", now)) + len(namespaceVolumeBindingPutStoredPredicateArgs(record)) + len(namespaceVolumeBindingArgsForTest(t, binding)) + len(auditOutboxColumns)
	if len(exec.args) != wantArgs {
		t.Fatalf("arg count = %d, want %d", len(exec.args), wantArgs)
	}
	rendered := strings.ToLower(renderArgs(t, exec.args...))
	for _, forbidden := range []string{"password", "secret-token", "authorization"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("CommitNamespaceVolumeBindingPutWithLease args leaked %q in %s", forbidden, rendered)
		}
	}
}

func TestCommitNamespaceVolumeBindingPutWithLeaseRejectsInvalidRequestBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	binding := bindingFixture()
	binding.CreatedAt = now
	binding.UpdatedAt = now
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-binding"
	record.Type = operations.OperationNamespaceVolumeBindingPut
	record.State = operations.OperationStateSucceeded
	record.Phase = operations.OperationPhaseNamespaceVolumeBindingPutCommitted
	record.NamespaceID = binding.NamespaceID
	record.Resource = operations.ResourceRef{Type: "namespace_volume_binding", ID: binding.NamespaceID}

	tests := []struct {
		name    string
		binding resources.NamespaceVolumeBinding
		record  operations.OperationRecord
		event   audit.Event
	}{
		{name: "invalid binding", binding: func() resources.NamespaceVolumeBinding {
			edited := binding
			edited.NamespaceID = "bad_ns"
			return edited
		}(), record: record, event: namespaceVolumeBindingPutAuditEvent("audit-binding", "op-binding", binding.NamespaceID, now)},
		{name: "wrong operation type", binding: binding, record: func() operations.OperationRecord {
			edited := record
			edited.Type = operations.OperationRepoCreate
			return edited
		}(), event: namespaceVolumeBindingPutAuditEvent("audit-binding", "op-binding", binding.NamespaceID, now)},
		{name: "operation not succeeded", binding: binding, record: func() operations.OperationRecord {
			edited := record
			edited.State = operations.OperationStateRunning
			return edited
		}(), event: namespaceVolumeBindingPutAuditEvent("audit-binding", "op-binding", binding.NamespaceID, now)},
		{name: "wrong terminal phase", binding: binding, record: func() operations.OperationRecord {
			edited := record
			edited.Phase = operations.OperationPhaseNamespaceVolumeBindingPutValidate
			return edited
		}(), event: namespaceVolumeBindingPutAuditEvent("audit-binding", "op-binding", binding.NamespaceID, now)},
		{name: "wrong resource", binding: binding, record: func() operations.OperationRecord {
			edited := record
			edited.Resource = operations.ResourceRef{Type: "repo", ID: binding.NamespaceID}
			return edited
		}(), event: namespaceVolumeBindingPutAuditEvent("audit-binding", "op-binding", binding.NamespaceID, now)},
		{name: "namespace mismatch", binding: binding, record: func() operations.OperationRecord { edited := record; edited.NamespaceID = "ns_other01"; return edited }(), event: namespaceVolumeBindingPutAuditEvent("audit-binding", "op-binding", binding.NamespaceID, now)},
		{name: "audit operation mismatch", binding: binding, record: record, event: namespaceVolumeBindingPutAuditEvent("audit-binding", "op-other", binding.NamespaceID, now)},
		{name: "audit resource mismatch", binding: binding, record: record, event: func() audit.Event {
			event := namespaceVolumeBindingPutAuditEvent("audit-binding", "op-binding", binding.NamespaceID, now)
			event.Resource.ID = "ns_other01"
			return event
		}()},
		{name: "audit outcome failed", binding: binding, record: record, event: func() audit.Event {
			event := namespaceVolumeBindingPutAuditEvent("audit-binding", "op-binding", binding.NamespaceID, now)
			event.Outcome = audit.OutcomeFailed
			return event
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}
			_, _, err := st.CommitNamespaceVolumeBindingPutWithLease(context.Background(), tt.binding, tt.record.SanitizedForPersistence(), "worker-a", now, tt.event)
			if err == nil {
				t.Fatal("CommitNamespaceVolumeBindingPutWithLease succeeded, want error")
			}
			if exec.query != "" {
				t.Fatalf("issued SQL for invalid request: %s", exec.query)
			}
		})
	}
}

func TestCommitNamespaceVolumeBindingPutWithLeaseActiveNamespaceAndVolumeAreUpdatePrerequisites(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	binding := bindingFixture()
	binding.CreatedAt = now
	binding.UpdatedAt = now
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-binding"
	record.Type = operations.OperationNamespaceVolumeBindingPut
	record.State = operations.OperationStateSucceeded
	record.Phase = operations.OperationPhaseNamespaceVolumeBindingPutCommitted
	record.NamespaceID = binding.NamespaceID
	record.Resource = operations.ResourceRef{Type: "namespace_volume_binding", ID: binding.NamespaceID}

	for _, name := range []string{
		"namespace missing",
		"namespace inactive",
		"volume missing",
		"volume inactive",
	} {
		t.Run(name, func(t *testing.T) {
			exec := &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}
			st := &Store{exec: exec}

			_, _, err := st.CommitNamespaceVolumeBindingPutWithLease(context.Background(), binding, record.SanitizedForPersistence(), "worker-a", now, namespaceVolumeBindingPutAuditEvent("audit-binding", "op-binding", binding.NamespaceID, now))
			if !errors.Is(err, operations.ErrLeaseUnavailable) || !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("CommitNamespaceVolumeBindingPutWithLease error = %v, want ErrLeaseUnavailable/sql.ErrNoRows", err)
			}
			assertSQLContainsInOrder(t, exec.query,
				"WITH active_namespace AS (",
				"SELECT namespace_id FROM namespaces WHERE namespace_id = $14 AND status = 'active'",
				"), active_volume AS (",
				"SELECT volume_id FROM volumes WHERE volume_id = $20 AND status = 'active'",
				"), updated_operation AS (",
				"UPDATE operations SET",
				"EXISTS (SELECT 1 FROM active_namespace)",
				"EXISTS (SELECT 1 FROM active_volume)",
				"RETURNING",
				"), upserted_binding AS (",
			)
		})
	}
}

func TestCommitNamespaceVolumeBindingPutFailedWithLeaseAtomicallyUpdatesOperationAndAppendsAudit(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-binding"
	record.Type = operations.OperationNamespaceVolumeBindingPut
	record.State = operations.OperationStateFailed
	record.Phase = operations.OperationPhaseNamespaceVolumeBindingPutValidate
	record.NamespaceID = "ns_alpha01"
	record.Resource = operations.ResourceRef{Type: "namespace_volume_binding", ID: "ns_alpha01"}
	record.Error = &operations.OperationError{
		Code:          "NAMESPACE_VOLUME_BINDING_VOLUME_NOT_ACTIVE",
		Message:       "namespace volume binding default volume is not active",
		Retryable:     false,
		CorrelationID: record.CorrelationID,
		OperationID:   record.ID,
		Details:       map[string]any{"namespace_id": record.NamespaceID, "default_volume_id": "vol_missing"},
	}
	record.FinishedAt = &now

	updated := record.SanitizedForPersistence().Record()
	updated.LeaseOwner = ""
	updated.LeaseExpiresAt = nil
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(updated)}}
	st := &Store{exec: exec}

	gotOperation, err := st.CommitNamespaceVolumeBindingPutFailedWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, namespaceVolumeBindingPutFailureAuditEvent("audit-binding", "op-binding", record.NamespaceID, now))
	if err != nil {
		t.Fatalf("CommitNamespaceVolumeBindingPutFailedWithLease: %v", err)
	}
	if gotOperation.ID != "op-binding" || gotOperation.State != operations.OperationStateFailed || gotOperation.LeaseOwner != "" {
		t.Fatalf("operation = %#v, want failed with cleared lease", gotOperation)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS (",
		"SELECT operation_id FROM operations",
		"operation_id = $12",
		"operation_state = 'running'",
		"lease_owner = $13",
		"operation_type = 'namespace_volume_binding_put'",
		"phase = 'validate_namespace_volume_binding_put'",
		"namespace_id = $14",
		"resource_type = 'namespace_volume_binding'",
		"resource_id = $14",
		"caller_service = $15",
		"correlation_id = $16",
		"authorized_actor_type = $17",
		"authorized_actor_id = $18",
		"FOR UPDATE",
		"), updated_operation AS (",
		"UPDATE operations SET",
		"FROM eligible_operation",
		"RETURNING",
		"), inserted_audit AS (",
		"INSERT INTO audit_outbox",
		"SELECT $19",
		"FROM updated_operation",
		") SELECT",
		"FROM updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)",
	)
	for _, forbidden := range []string{"active_volume", "active_namespace", "INSERT INTO namespace_volume_bindings"} {
		if strings.Contains(exec.query, forbidden) {
			t.Fatalf("failure commit query contains success prerequisite %q: %s", forbidden, exec.query)
		}
	}
	wantArgs := len(operationLeaseFencedUpdateArgsForTest(t, record, "worker-a", now)) + len(namespaceVolumeBindingPutStoredPredicateArgs(record)) + len(auditOutboxColumns)
	if len(exec.args) != wantArgs {
		t.Fatalf("arg count = %d, want %d", len(exec.args), wantArgs)
	}
}

func namespaceVolumeBindingPutAuditEvent(eventID, operationID, namespaceID string, now time.Time) audit.Event {
	return audit.NewEvent(audit.Event{
		EventID:         eventID,
		Type:            audit.EventTypeNamespaceVolumeBindingPut,
		Time:            now,
		CallerService:   "afscp-api",
		AuthorizedActor: audit.Actor{Type: "system", ID: "svc-alpha"},
		CorrelationID:   "corr-alpha",
		OperationID:     operationID,
		Resource:        audit.Resource{Type: "namespace_volume_binding", ID: namespaceID, NamespaceID: namespaceID},
		Outcome:         audit.OutcomeSucceeded,
		Reason:          "namespace_volume_binding_put_committed",
		Details:         map[string]any{"namespace_id": namespaceID},
	})
}

func namespaceVolumeBindingPutFailureAuditEvent(eventID, operationID, namespaceID string, now time.Time) audit.Event {
	return audit.NewEvent(audit.Event{
		EventID:         eventID,
		Type:            audit.EventTypeNamespaceVolumeBindingPut,
		Time:            now,
		CallerService:   "afscp-api",
		AuthorizedActor: audit.Actor{Type: "system", ID: "svc-alpha"},
		CorrelationID:   "corr-alpha",
		OperationID:     operationID,
		Resource:        audit.Resource{Type: "namespace_volume_binding", ID: namespaceID, NamespaceID: namespaceID},
		Outcome:         audit.OutcomeFailed,
		Reason:          "namespace_volume_binding_put_failed",
		Details:         map[string]any{"namespace_id": namespaceID, "default_volume_id": "vol_missing"},
	})
}

func namespaceVolumeBindingArgsForTest(t *testing.T, binding resources.NamespaceVolumeBinding) []any {
	t.Helper()
	args, err := namespaceVolumeBindingArgs(binding)
	if err != nil {
		t.Fatalf("namespaceVolumeBindingArgs: %v", err)
	}
	return args
}
