package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"
)

func TestWorkloadMountCreateCommitSQLHasDurableAdmissionGates(t *testing.T) {
	sql := workloadMountBindingCreateCommitSQL()

	assertSQLContainsInOrder(t, sql,
		"active_namespace AS (",
		"FROM namespaces",
		"namespace_id = $15",
		"status = 'active'",
		"active_binding AS (",
		"FROM namespace_volume_bindings",
		"namespace_id = $15",
		"status = 'active'",
		"workload_mount_enabled",
		"workload_mount_requires_jvs_external_control_root",
		"active_repo AS (",
		"FROM repos",
		"namespace_id = $15",
		"repo_id = $16",
		"volume_id = $17",
		"repo_kind = 'repo'",
		"status = 'active'",
		"lifecycle_status = 'active'",
		"FOR UPDATE",
		"active_volume AS (",
		"FROM volumes",
		"volume_id = $17",
		"status = 'active'",
		"workload_mount",
		"jvs_external_control_root",
		"held_lifecycle_fence AS (",
		"repo_fences.repo_id = active_repo.repo_id",
		"fence_kind = 'lifecycle'",
		"status IN ('active','expired','recovery_required')",
		"released_at IS NULL",
		"recovered_at IS NULL",
		"held_writer_fence AS (",
		"repo_fences.repo_id = active_repo.repo_id",
		"fence_kind = 'writer_session'",
		"status IN ('active','expired','recovery_required')",
		"released_at IS NULL",
		"recovered_at IS NULL",
		"updated_operation AS (",
		"namespace_id = $15",
		"repo_id = $16",
		"resource_type = 'workload_mount_binding'",
		"resource_id = $14",
		"input_summary @> jsonb_build_object",
		"'mount_binding_id', $14",
		"'namespace_id', $15",
		"'repo_id', $16",
		"'volume_id', $17",
		"'mount_path', $18",
		"'read_only', $19",
		"'lease_seconds', $21",
		"EXISTS (SELECT 1 FROM active_namespace)",
		"EXISTS (SELECT 1 FROM active_binding)",
		"EXISTS (SELECT 1 FROM active_repo)",
		"EXISTS (SELECT 1 FROM active_volume)",
		"NOT EXISTS (SELECT 1 FROM held_lifecycle_fence)",
		"($19 = true OR NOT EXISTS (SELECT 1 FROM held_writer_fence))",
	)
}

func TestWorkloadMountPlanAllowsDisabledNamespaceOnlyForReleaseTrack(t *testing.T) {
	sql := workloadMountPlanSelectSQL()

	assertSQLContainsInOrder(t, sql,
		"FROM workload_mount_bindings b JOIN repos r ON r.namespace_id = b.namespace_id AND r.repo_id = b.repo_id",
		"JOIN namespace_volume_bindings nvb ON nvb.namespace_id = b.namespace_id",
		"JOIN namespaces ns ON ns.namespace_id = b.namespace_id",
		"WHERE b.namespace_id = $1 AND b.mount_binding_id = $2",
		"nvb.status = 'active'",
		"((ns.status = 'active' AND b.status IN ('issued','pending','active','releasing')) OR (ns.status = 'disabled' AND b.status = 'releasing'))",
	)
}

func TestWorkloadMountUpdateCommitSQLBindsOperationAndBindingBoundary(t *testing.T) {
	for _, sql := range []string{
		workloadMountBindingStatusCommitSQL(),
		workloadMountBindingHeartbeatCommitSQL(),
		workloadMountBindingReleaseCommitSQL(),
		workloadMountBindingRevokeCommitSQL(),
	} {
		assertSQLContainsInOrder(t, sql,
			"updated_operation AS (",
			"mount_binding_id = $14",
			"resource_type = 'workload_mount_binding'",
			"resource_id = $14",
			"UPDATE workload_mount_bindings SET",
			"FROM updated_operation",
			"workload_mount_bindings.mount_binding_id = $14",
			"workload_mount_bindings.namespace_id = updated_operation.namespace_id",
			"workload_mount_bindings.repo_id = updated_operation.repo_id",
		)
	}
}

func TestWorkloadMountStatusCommitSQLPreservesRevokeIntent(t *testing.T) {
	sql := workloadMountBindingStatusCommitSQL()

	assertSQLContainsInOrder(t, sql,
		"status = CASE",
		"WHEN status IN ('released','revoked','expired','failed') THEN status",
		"WHEN status = 'releasing' AND $15 IN ('pending','active') THEN status",
		"ELSE $15 END",
		"last_observed_at = CASE",
		"WHEN status = 'releasing' AND $15 IN ('pending','active') THEN last_observed_at",
		"lease_expires_at = CASE",
		"WHEN status = 'releasing' AND $15 IN ('pending','active') THEN lease_expires_at",
	)
}

func TestWorkloadMountPlanSQLExcludesTerminalBindings(t *testing.T) {
	sql := workloadMountPlanSelectSQL()

	assertSQLContainsInOrder(t, sql,
		"WHERE b.namespace_id = $1",
		"b.mount_binding_id = $2",
		"b.status IN ('issued','pending','active','releasing')",
	)
}

func TestWorkloadMountCommitRejectsBadAuditBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	binding := workloadMountBindingFixture(now)

	tests := []struct {
		name  string
		typ   operations.OperationType
		phase string
		event audit.EventType
		call  func(*Store, operations.OperationRecord, audit.Event) error
	}{
		{name: "create", typ: operations.OperationMountBindingCreate, phase: operations.OperationPhaseMountBindingCreateCommitted, event: audit.EventTypeMountBindingCreate, call: func(store *Store, record operations.OperationRecord, event audit.Event) error {
			_, _, err := store.CommitWorkloadMountBindingCreateWithLease(context.Background(), binding, record.SanitizedForPersistence(), "worker-a", now, event)
			return err
		}},
		{name: "status", typ: operations.OperationMountBindingStatusUpdate, phase: operations.OperationPhaseMountBindingStatusCommitted, event: audit.EventTypeMountBindingStatusUpdate, call: func(store *Store, record operations.OperationRecord, event audit.Event) error {
			_, _, err := store.CommitWorkloadMountBindingStatusWithLease(context.Background(), "wmb_123", sessionstate.MountStatusActive, "mounted", now, nil, record.SanitizedForPersistence(), "worker-a", now, event)
			return err
		}},
		{name: "heartbeat", typ: operations.OperationMountBindingHeartbeat, phase: operations.OperationPhaseMountBindingHeartbeatCommitted, event: audit.EventTypeMountBindingHeartbeat, call: func(store *Store, record operations.OperationRecord, event audit.Event) error {
			_, _, err := store.CommitWorkloadMountBindingHeartbeatWithLease(context.Background(), "wmb_123", record.SanitizedForPersistence(), "worker-a", now, event)
			return err
		}},
		{name: "release", typ: operations.OperationMountBindingRelease, phase: operations.OperationPhaseMountBindingReleaseCommitted, event: audit.EventTypeMountBindingRelease, call: func(store *Store, record operations.OperationRecord, event audit.Event) error {
			_, _, err := store.CommitWorkloadMountBindingReleaseWithLease(context.Background(), "wmb_123", record.SanitizedForPersistence(), "worker-a", now, event)
			return err
		}},
		{name: "revoke", typ: operations.OperationMountBindingRevoke, phase: operations.OperationPhaseMountBindingRevokeCommitted, event: audit.EventTypeMountBindingRevoke, call: func(store *Store, record operations.OperationRecord, event audit.Event) error {
			_, _, err := store.CommitWorkloadMountBindingRevokeWithLease(context.Background(), "wmb_123", record.SanitizedForPersistence(), "worker-a", now, event)
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			store := &Store{exec: exec}
			record := workloadMountOperationRecordForTest(now, tt.typ, tt.phase)
			event := workloadMountAuditEventForTest(record, tt.event, now)
			event.OperationID = "op_other"

			err := tt.call(store, record, event)
			if err == nil {
				t.Fatal("commit err = nil, want invalid audit error")
			}
			if exec.queryRowCalls != 0 {
				t.Fatalf("query row calls = %d, want no SQL before audit validation passes", exec.queryRowCalls)
			}
		})
	}
}

func TestWorkloadMountStatusCommitRejectsNonOrchestratorStatusesBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	for _, status := range []sessionstate.MountStatus{sessionstate.MountStatusIssued, sessionstate.MountStatusReleasing} {
		t.Run(string(status), func(t *testing.T) {
			exec := &fakeExecutor{}
			store := &Store{exec: exec}
			record := workloadMountOperationRecordForTest(now, operations.OperationMountBindingStatusUpdate, operations.OperationPhaseMountBindingStatusCommitted)
			event := workloadMountAuditEventForTest(record, audit.EventTypeMountBindingStatusUpdate, now)

			_, _, err := store.CommitWorkloadMountBindingStatusWithLease(context.Background(), "wmb_123", status, "", now, nil, record.SanitizedForPersistence(), "worker-a", now, event)
			if err == nil {
				t.Fatal("CommitWorkloadMountBindingStatusWithLease err = nil, want invalid status")
			}
			if exec.queryRowCalls != 0 {
				t.Fatalf("query row calls = %d, want no SQL before status validation passes", exec.queryRowCalls)
			}
		})
	}
}

func TestWorkloadMountStatusCommitRejectsReasonOverMaxLengthBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	exec := &fakeExecutor{}
	store := &Store{exec: exec}
	record := workloadMountOperationRecordForTest(now, operations.OperationMountBindingStatusUpdate, operations.OperationPhaseMountBindingStatusCommitted)
	event := workloadMountAuditEventForTest(record, audit.EventTypeMountBindingStatusUpdate, now)

	_, _, err := store.CommitWorkloadMountBindingStatusWithLease(context.Background(), "wmb_123", sessionstate.MountStatusActive, strings.Repeat("x", workloadmount.MaxReasonLength+1), now, nil, record.SanitizedForPersistence(), "worker-a", now, event)
	if err == nil {
		t.Fatal("CommitWorkloadMountBindingStatusWithLease err = nil, want invalid reason")
	}
	if exec.queryRowCalls != 0 {
		t.Fatalf("query row calls = %d, want no SQL before reason validation passes", exec.queryRowCalls)
	}
}

func workloadMountBindingFixture(now time.Time) workloadmount.Binding {
	return workloadmount.Binding{
		ID:             "wmb_123",
		NamespaceID:    "ns_123",
		RepoID:         "repo_123",
		VolumeID:       "vol_123",
		MountPath:      "/mnt/repo",
		ReadOnly:       false,
		Status:         sessionstate.MountStatusIssued,
		LeaseSeconds:   120,
		LeaseExpiresAt: now.Add(120 * time.Second),
		CreatedAt:      now.Add(-time.Minute),
		UpdatedAt:      now,
	}
}

func workloadMountOperationRecordForTest(now time.Time, typ operations.OperationType, phase string) operations.OperationRecord {
	finishedAt := now
	leaseExpiresAt := now.Add(5 * time.Minute)
	return operations.OperationRecord{
		ID:               "op_mount",
		Type:             typ,
		State:            operations.OperationStateSucceeded,
		Phase:            phase,
		Attempt:          1,
		LeaseOwner:       "worker-a",
		LeaseExpiresAt:   &leaseExpiresAt,
		IdempotencyScope: operations.NewIdempotencyScope("agentsmith-api", "ns_123", typ, "idem_mount").String(),
		IdempotencyKey:   "idem_mount",
		RequestHash:      "sha256:mount",
		CorrelationID:    "corr_mount",
		CallerService:    "agentsmith-api",
		AuthorizedActor:  operations.Actor{Type: "service", ID: "agentsmith-api"},
		Resource:         operations.ResourceRef{Type: "workload_mount_binding", ID: "wmb_123"},
		NamespaceID:      "ns_123",
		RepoID:           "repo_123",
		MountBindingID:   "wmb_123",
		InputSummary:     map[string]any{"mount_binding_id": "wmb_123"},
		CreatedAt:        now.Add(-time.Minute),
		FinishedAt:       &finishedAt,
	}
}

func workloadMountAuditEventForTest(record operations.OperationRecord, typ audit.EventType, now time.Time) audit.Event {
	return audit.Event{
		EventID:         "evt_mount",
		Type:            typ,
		Time:            now,
		CallerService:   record.CallerService,
		AuthorizedActor: audit.Actor{Type: record.AuthorizedActor.Type, ID: record.AuthorizedActor.ID},
		CorrelationID:   record.CorrelationID,
		OperationID:     record.ID,
		Resource:        audit.Resource{Type: "workload_mount_binding", ID: record.MountBindingID, NamespaceID: record.NamespaceID},
		Outcome:         audit.OutcomeSucceeded,
		Reason:          record.Phase,
		Details:         map[string]any{"mount_binding_id": record.MountBindingID, "repo_id": record.RepoID},
	}
}
