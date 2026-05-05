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

func TestCommitNamespaceUpsertWithLeaseAtomicallyUpsertsNamespaceUpdatesOperationAndAppendsAudit(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	namespace := resources.Namespace{
		ID:        "ns_alpha01",
		Status:    resources.NamespaceStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	returnedOperation := operationFixture(now.Add(-time.Hour))
	returnedOperation.ID = "op-namespace"
	returnedOperation.Type = operations.OperationNamespaceUpsert
	returnedOperation.State = operations.OperationStateSucceeded
	returnedOperation.Phase = "namespace_upsert_committed"
	returnedOperation.NamespaceID = "ns_alpha01"
	returnedOperation.Resource = operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"}
	returnedOperation.LeaseOwner = ""
	returnedOperation.LeaseExpiresAt = nil
	returnedOperation.ExternalResourceIDs = map[string]string{"control_root": "jvs-control-secret"}
	returnedOperation.InputSummary = map[string]any{"namespace_id": "ns_alpha01", "token": "input-secret-token"}
	returnedOperation.JVSJSONOutput = map[string]any{"token": "jvs-output-secret"}
	returnedOperation.FinishedAt = &now

	exec := &fakeExecutor{row: fakeRow{values: append(namespaceRowValues(namespace), operationRowValues(returnedOperation.SanitizedForPersistence().Record())...)}}
	st := &Store{exec: exec}

	event := namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)
	event.Reason = "done token=audit-reason-secret"
	event.Resource.Path = "/control/root?token=audit-path-secret"
	event.Details = map[string]any{"authorization": "Bearer audit-detail-secret", "safe": "visible"}
	gotNamespace, gotOperation, err := st.CommitNamespaceUpsertWithLease(context.Background(), namespace, returnedOperation.SanitizedForPersistence(), "worker-a", now, event)
	if err != nil {
		t.Fatalf("CommitNamespaceUpsertWithLease: %v", err)
	}
	if gotNamespace.ID != "ns_alpha01" || gotNamespace.Status != resources.NamespaceStatusActive {
		t.Fatalf("namespace = %#v, want active ns_alpha01", gotNamespace)
	}
	if gotOperation.ID != "op-namespace" || gotOperation.State != operations.OperationStateSucceeded || gotOperation.LeaseOwner != "" {
		t.Fatalf("operation = %#v, want succeeded op-namespace with cleared lease", gotOperation)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH updated_operation AS (",
		"UPDATE operations SET",
		"WHERE operation_id = $12",
		"operation_state = 'running'",
		"lease_owner = $13",
		"lease_expires_at IS NOT NULL",
		"lease_expires_at > $11",
		"operation_type = 'namespace_upsert'",
		"namespace_id = $14",
		"resource_type = 'namespace'",
		"resource_id = $14",
		"caller_service = $15",
		"correlation_id = $16",
		"authorized_actor_type = $17",
		"authorized_actor_id = $18",
		"RETURNING",
		"), upserted_namespace AS (",
		"INSERT INTO namespaces",
		"SELECT $19, $20, $21, $22, $23, $24 FROM updated_operation",
		"ON CONFLICT (namespace_id) DO UPDATE SET",
		"status = CASE WHEN namespaces.status = 'disabled' THEN namespaces.status ELSE EXCLUDED.status END",
		"disabled_reason = CASE WHEN namespaces.status = 'disabled' THEN namespaces.disabled_reason ELSE EXCLUDED.disabled_reason END",
		"disabled_at = CASE WHEN namespaces.status = 'disabled' THEN namespaces.disabled_at ELSE EXCLUDED.disabled_at END",
		"RETURNING",
		"), inserted_audit AS (",
		"INSERT INTO audit_outbox",
		"SELECT",
		"FROM updated_operation, upserted_namespace",
		"RETURNING audit_event_id",
		") SELECT",
		"FROM upserted_namespace, updated_operation",
		"WHERE EXISTS (SELECT 1 FROM inserted_audit)",
	)
	if exec.queryRowCalls != 1 || exec.execCalls != 0 || exec.queryCalls != 0 {
		t.Fatalf("executor calls queryRow/exec/query = %d/%d/%d, want 1/0/0", exec.queryRowCalls, exec.execCalls, exec.queryCalls)
	}
	wantArgs := len(operationLeaseFencedUpdateArgsForTest(t, returnedOperation, "worker-a", now)) + len(namespaceUpsertStoredPredicateArgs(returnedOperation)) + len(namespaceArgs(namespace)) + len(auditOutboxColumns)
	if len(exec.args) != wantArgs {
		t.Fatalf("arg count = %d, want %d", len(exec.args), wantArgs)
	}
	rendered := strings.ToLower(renderArgs(t, exec.args...))
	for _, forbidden := range []string{"input-secret-token", "jvs-output-secret", "jvs-control-secret", "audit-reason-secret", "audit-path-secret", "audit-detail-secret"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("CommitNamespaceUpsertWithLease args leaked %q in %s", forbidden, rendered)
		}
	}
}

func TestCommitNamespaceUpsertWithLeaseRejectsInvalidRequestBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	namespace := resources.Namespace{ID: "ns_alpha01", Status: resources.NamespaceStatusActive, CreatedAt: now, UpdatedAt: now}
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-namespace"
	record.Type = operations.OperationNamespaceUpsert
	record.State = operations.OperationStateSucceeded
	record.NamespaceID = "ns_alpha01"
	record.Resource = operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"}
	tests := []struct {
		name      string
		namespace resources.Namespace
		record    operations.OperationRecord
		owner     string
		now       time.Time
		event     audit.Event
	}{
		{name: "invalid namespace", namespace: resources.Namespace{ID: "repo_wrong", Status: resources.NamespaceStatusActive, CreatedAt: now, UpdatedAt: now}, record: record, owner: "worker-a", now: now, event: namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)},
		{name: "disabled namespace input", namespace: func() resources.Namespace {
			disabledAt := now
			return resources.Namespace{ID: "ns_alpha01", Status: resources.NamespaceStatusDisabled, DisabledReason: "maintenance", DisabledAt: &disabledAt, CreatedAt: now, UpdatedAt: now}
		}(), record: record, owner: "worker-a", now: now, event: namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)},
		{name: "operation not succeeded", namespace: namespace, record: func() operations.OperationRecord {
			edited := record
			edited.State = operations.OperationStateRunning
			return edited
		}(), owner: "worker-a", now: now, event: namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)},
		{name: "wrong operation type", namespace: namespace, record: func() operations.OperationRecord {
			edited := record
			edited.Type = operations.OperationRepoCreate
			edited.NamespaceID = "ns_alpha01"
			edited.Resource = operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"}
			return edited
		}(), owner: "worker-a", now: now, event: namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)},
		{name: "missing record namespace", namespace: namespace, record: func() operations.OperationRecord {
			edited := record
			edited.NamespaceID = ""
			edited.Resource = operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"}
			return edited
		}(), owner: "worker-a", now: now, event: namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)},
		{name: "mismatched record namespace", namespace: namespace, record: func() operations.OperationRecord {
			edited := record
			edited.NamespaceID = "ns_other01"
			edited.Resource = operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"}
			return edited
		}(), owner: "worker-a", now: now, event: namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)},
		{name: "wrong resource type", namespace: namespace, record: func() operations.OperationRecord {
			edited := record
			edited.NamespaceID = "ns_alpha01"
			edited.Resource = operations.ResourceRef{Type: "repo", ID: "ns_alpha01"}
			return edited
		}(), owner: "worker-a", now: now, event: namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)},
		{name: "missing resource id", namespace: namespace, record: func() operations.OperationRecord {
			edited := record
			edited.NamespaceID = "ns_alpha01"
			edited.Resource = operations.ResourceRef{Type: "namespace", ID: ""}
			return edited
		}(), owner: "worker-a", now: now, event: namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)},
		{name: "mismatched resource id", namespace: namespace, record: func() operations.OperationRecord {
			edited := record
			edited.NamespaceID = "ns_alpha01"
			edited.Resource = operations.ResourceRef{Type: "namespace", ID: "ns_other01"}
			return edited
		}(), owner: "worker-a", now: now, event: namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)},
		{name: "audit outcome not succeeded", namespace: namespace, record: record, owner: "worker-a", now: now, event: func() audit.Event {
			event := namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)
			event.Outcome = audit.OutcomeFailed
			return event
		}()},
		{name: "audit resource type mismatch", namespace: namespace, record: record, owner: "worker-a", now: now, event: func() audit.Event {
			event := namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)
			event.Resource.Type = "repo"
			return event
		}()},
		{name: "audit resource id mismatch", namespace: namespace, record: record, owner: "worker-a", now: now, event: func() audit.Event {
			event := namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)
			event.Resource.ID = "ns_other01"
			return event
		}()},
		{name: "audit resource namespace mismatch", namespace: namespace, record: record, owner: "worker-a", now: now, event: func() audit.Event {
			event := namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)
			event.Resource.NamespaceID = "ns_other01"
			return event
		}()},
		{name: "audit caller mismatch", namespace: namespace, record: record, owner: "worker-a", now: now, event: func() audit.Event {
			event := namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)
			event.CallerService = "other-api"
			return event
		}()},
		{name: "audit correlation mismatch", namespace: namespace, record: record, owner: "worker-a", now: now, event: func() audit.Event {
			event := namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)
			event.CorrelationID = "corr-other"
			return event
		}()},
		{name: "audit actor mismatch", namespace: namespace, record: record, owner: "worker-a", now: now, event: func() audit.Event {
			event := namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)
			event.AuthorizedActor.ID = "svc-other"
			return event
		}()},
		{name: "blank owner", namespace: namespace, record: record, owner: " \t", now: now, event: namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)},
		{name: "zero now", namespace: namespace, record: record, owner: "worker-a", event: namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)},
		{name: "missing audit operation", namespace: namespace, record: record, owner: "worker-a", now: now, event: namespaceCommitAuditEvent("audit-namespace", "", now)},
		{name: "mismatched audit operation", namespace: namespace, record: record, owner: "worker-a", now: now, event: namespaceCommitAuditEvent("audit-namespace", "op-other", now)},
		{name: "event type mismatch", namespace: namespace, record: record, owner: "worker-a", now: now, event: func() audit.Event {
			event := namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)
			event.Type = audit.EventTypeRepoCreate
			return event
		}()},
		{name: "invalid audit payload", namespace: namespace, record: record, owner: "worker-a", now: now, event: func() audit.Event {
			event := namespaceCommitAuditEvent("audit-namespace", "op-namespace", now)
			event.Details = map[string]any{"bad": make(chan int)}
			return event
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}
			_, _, err := st.CommitNamespaceUpsertWithLease(context.Background(), tt.namespace, tt.record.SanitizedForPersistence(), tt.owner, tt.now, tt.event)
			if err == nil {
				t.Fatal("CommitNamespaceUpsertWithLease succeeded, want error")
			}
			if exec.query != "" {
				t.Fatalf("issued SQL for invalid request: %s", exec.query)
			}
		})
	}
}

func TestCommitNamespaceUpsertWithLeaseDBPredicatesDenyStoredOperationMismatch(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	namespace := resources.Namespace{ID: "ns_alpha01", Status: resources.NamespaceStatusActive, CreatedAt: now, UpdatedAt: now}
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-namespace"
	record.Type = operations.OperationNamespaceUpsert
	record.State = operations.OperationStateSucceeded
	record.NamespaceID = "ns_alpha01"
	record.Resource = operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"}
	exec := &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}
	st := &Store{exec: exec}

	_, _, err := st.CommitNamespaceUpsertWithLease(context.Background(), namespace, record.SanitizedForPersistence(), "worker-a", now, namespaceCommitAuditEvent("audit-namespace", "op-namespace", now))
	if !errors.Is(err, operations.ErrLeaseUnavailable) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("CommitNamespaceUpsertWithLease error = %v, want ErrLeaseUnavailable/sql.ErrNoRows", err)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WHERE operation_id = $12",
		"operation_state = 'running'",
		"lease_owner = $13",
		"lease_expires_at IS NOT NULL",
		"lease_expires_at > $11",
		"operation_type = 'namespace_upsert'",
		"namespace_id = $14",
		"resource_type = 'namespace'",
		"resource_id = $14",
		"caller_service = $15",
		"correlation_id = $16",
		"authorized_actor_type = $17",
		"authorized_actor_id = $18",
		"), upserted_namespace AS (",
		"SELECT $19, $20, $21, $22, $23, $24 FROM updated_operation",
	)
}

func TestCommitNamespaceUpsertWithLeaseNoRowsWrapsLeaseUnavailable(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	namespace := resources.Namespace{ID: "ns_alpha01", Status: resources.NamespaceStatusActive, CreatedAt: now, UpdatedAt: now}
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-namespace"
	record.Type = operations.OperationNamespaceUpsert
	record.State = operations.OperationStateSucceeded
	record.NamespaceID = "ns_alpha01"
	record.Resource = operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"}
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}}

	_, _, err := st.CommitNamespaceUpsertWithLease(context.Background(), namespace, record.SanitizedForPersistence(), "worker-a", now, namespaceCommitAuditEvent("audit-namespace", "op-namespace", now))
	if !errors.Is(err, operations.ErrLeaseUnavailable) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("CommitNamespaceUpsertWithLease error = %v, want ErrLeaseUnavailable and sql.ErrNoRows", err)
	}
}

func TestCommitNamespaceUpsertWithLeaseAtomicBoundaryErrorsPropagate(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	namespace := resources.Namespace{ID: "ns_alpha01", Status: resources.NamespaceStatusActive, CreatedAt: now, UpdatedAt: now}
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-namespace"
	record.Type = operations.OperationNamespaceUpsert
	record.State = operations.OperationStateSucceeded
	record.NamespaceID = "ns_alpha01"
	record.Resource = operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"}
	insertErr := errors.New("audit insert failed")
	exec := &fakeExecutor{row: fakeRow{err: insertErr}}
	st := &Store{exec: exec}

	_, _, err := st.CommitNamespaceUpsertWithLease(context.Background(), namespace, record.SanitizedForPersistence(), "worker-a", now, namespaceCommitAuditEvent("audit-namespace", "op-namespace", now))
	if !errors.Is(err, insertErr) {
		t.Fatalf("CommitNamespaceUpsertWithLease error = %v, want insert error", err)
	}
	if !strings.Contains(exec.query, "WITH updated_operation AS") || !strings.Contains(exec.query, "upserted_namespace AS") || !strings.Contains(exec.query, "INSERT INTO audit_outbox") {
		t.Fatalf("commit did not use atomic CTE boundary: %s", exec.query)
	}
}

func namespaceCommitAuditEvent(eventID, operationID string, now time.Time) audit.Event {
	return audit.Event{
		EventID:         eventID,
		Type:            audit.EventTypeNamespaceUpsert,
		Time:            now,
		CallerService:   "afscp-api",
		AuthorizedActor: audit.Actor{Type: "system", ID: "svc-alpha"},
		CorrelationID:   "corr-alpha",
		OperationID:     operationID,
		Resource:        audit.Resource{Type: "namespace", ID: "ns_alpha01"},
		Outcome:         audit.OutcomeSucceeded,
		Reason:          "namespace upsert committed",
		Details:         map[string]any{"safe": "visible"},
	}
}
