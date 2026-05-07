package mountbindingexec

import (
	"context"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"
)

func TestStatusUpdatePassesObservedAtToCommit(t *testing.T) {
	observedAt := time.Date(2026, 5, 5, 12, 34, 56, 0, time.UTC)
	orchestratorLeaseExpiresAt := observedAt.Add(time.Hour)
	serverNow := observedAt.Add(10 * time.Minute)
	store := &fakeMountCommitStore{}
	executor, err := NewExecutor(Config{
		CommitStore:  store,
		Owner:        "worker",
		Clock:        func() time.Time { return serverNow },
		AuditEventID: func() string { return "evt_mount" },
	})
	if err != nil {
		t.Fatal(err)
	}
	leaseExpiresAt := serverNow.Add(time.Minute)
	record := operations.OperationRecord{
		ID:              "op_mount",
		Type:            operations.OperationMountBindingStatusUpdate,
		State:           operations.OperationStateRunning,
		Phase:           operations.OperationPhaseMountBindingStatusValidate,
		LeaseOwner:      "worker",
		LeaseExpiresAt:  &leaseExpiresAt,
		CallerService:   "runtime-orchestrator",
		CorrelationID:   "corr_mount",
		AuthorizedActor: operations.Actor{Type: "service", ID: "runtime-orchestrator"},
		Resource:        operations.ResourceRef{Type: "workload_mount_binding", ID: "wmb_123"},
		NamespaceID:     "ns_123",
		RepoID:          "repo_123",
		MountBindingID:  "wmb_123",
		InputSummary:    map[string]any{"status": "active", "reason": "mounted", "observed_at": observedAt.Format(time.RFC3339), "lease_expires_at": orchestratorLeaseExpiresAt.Format(time.RFC3339)},
		CreatedAt:       observedAt.Add(-time.Minute),
	}

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if !store.observedAt.Equal(observedAt) {
		t.Fatalf("observedAt = %s, want %s", store.observedAt, observedAt)
	}
	if store.leaseExpiresAt == nil || !store.leaseExpiresAt.Equal(orchestratorLeaseExpiresAt) {
		t.Fatalf("leaseExpiresAt = %v, want %s", store.leaseExpiresAt, orchestratorLeaseExpiresAt)
	}
}

type fakeMountCommitStore struct {
	observedAt     time.Time
	leaseExpiresAt *time.Time
}

func (store *fakeMountCommitStore) CommitWorkloadMountBindingCreateWithLease(context.Context, workloadmount.Binding, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	return workloadmount.Binding{}, operations.OperationRecord{}, nil
}

func (store *fakeMountCommitStore) CommitWorkloadMountBindingStatusWithLease(_ context.Context, _ string, _ sessionstate.MountStatus, _ string, observedAt time.Time, leaseExpiresAt *time.Time, record operations.SanitizedOperationRecord, _ string, _ time.Time, _ audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	store.observedAt = observedAt
	store.leaseExpiresAt = leaseExpiresAt
	return workloadmount.Binding{}, record.Record(), nil
}

func (store *fakeMountCommitStore) CommitWorkloadMountBindingHeartbeatWithLease(context.Context, string, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	return workloadmount.Binding{}, operations.OperationRecord{}, nil
}

func (store *fakeMountCommitStore) CommitWorkloadMountBindingReleaseWithLease(context.Context, string, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	return workloadmount.Binding{}, operations.OperationRecord{}, nil
}

func (store *fakeMountCommitStore) CommitWorkloadMountBindingRevokeWithLease(context.Context, string, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	return workloadmount.Binding{}, operations.OperationRecord{}, nil
}
