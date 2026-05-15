package repoexec

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

func TestRestoreExecutorFencesWriterCallsDirectRestoreAndCommitsSucceeded(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		restoreSummary: jvsrunner.RestoreSummary{SourceSavePointID: "sp_001", RestoredSavePointID: "sp_001", Workspace: "main"},
		doctorSummary:  jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"},
	}
	store.beforeListSessions = func() {
		if store.restoreWriterFenceMarks != 1 || activeWriterFenceCount(store.fences, "op_restore") != 1 {
			t.Fatalf("session gate ran before durable writer fence, marks/fences = %d/%#v", store.restoreWriterFenceMarks, store.fences)
		}
	}
	executor := newTestRestoreExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), restoreLeasedRecord(now), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "restore,doctor" {
		t.Fatalf("JVS calls = %#v, want direct restore then doctor", runner.calls)
	}
	if runner.restoreSavePointID != "sp_001" {
		t.Fatalf("restore save point id = %q, want sp_001", runner.restoreSavePointID)
	}
	if store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseRestoreCommitted {
		t.Fatalf("operation = %#v, want succeeded restore_committed", store.operation)
	}
	if store.releasedFenceID != "fence_op_restore" || activeWriterFenceCount(store.fences, "op_restore") != 0 {
		t.Fatalf("released/active writer fence = %q/%#v, want released same-op fence", store.releasedFenceID, store.fences)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Type != audit.EventTypeRestore || store.auditEvents[0].Outcome != audit.OutcomeSucceeded {
		t.Fatalf("audit events = %#v, want restore success", store.auditEvents)
	}
	assertNoRestoreRunCommandLeak(t, store.operation, store.auditEvents)
}

func TestRestoreExecutorBlocksActiveWriterSessionsBeforeJVSRestore(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	store.exports = []sessionstate.ExportSession{freshExportSession(now, "export_alpha", sessionstate.AccessModeReadWrite, sessionstate.ExportStatusActive, now.Add(time.Hour))}
	runner := &fakeJVSRunner{}
	executor := newTestRestoreExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), restoreLeasedRecord(now), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("JVS calls = %#v, want active writer gate before JVS", runner.calls)
	}
	if store.operation.State != operations.OperationStateFailed || store.operation.Phase != operations.OperationPhaseRestoreWriterFenced {
		t.Fatalf("operation = %#v, want failed writer-fenced operation", store.operation)
	}
	if store.releasedFenceID != "fence_op_restore" {
		t.Fatalf("released fence = %q, want writer fence released after pre-JVS denial", store.releasedFenceID)
	}
}

func TestRestoreExecutorJVSFailureCommitsFailedWithoutPreviewOrRun(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{restoreErr: &jvsrunner.CommandError{Command: "restore", ExitCode: 1, Code: "E_RESTORE_FAILED"}}
	executor := newTestRestoreExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), restoreLeasedRecord(now), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "restore" {
		t.Fatalf("JVS calls = %#v, want direct restore only", runner.calls)
	}
	if store.operation.State != operations.OperationStateFailed || store.operation.Error == nil || store.operation.Error.Code != "JVS_RESTORE_FAILED" {
		t.Fatalf("operation = %#v, want failed JVS_RESTORE_FAILED", store.operation)
	}
	if store.previewOperation.ID != "" || store.restorePlan.ID != "" {
		t.Fatalf("direct restore created preview/plan state: preview=%#v plan=%#v", store.previewOperation, store.restorePlan)
	}
}

func TestRestoreExecutorSourceDoesNotCallPreviewRunOrPlanExecutors(t *testing.T) {
	body, err := os.ReadFile("restore_executor.go")
	if err != nil {
		t.Fatalf("read restore_executor.go: %v", err)
	}
	source := string(body)
	for _, forbidden := range []string{
		"RestorePreview(",
		"RestoreRun(",
		"RestoreDiscard(",
		"restoreplan.",
		"GetRestorePlanByPreviewOperation",
		"MarkRestoreRunConsumingWithLease",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("restore executor must not reference preview/run/plan flow %q", forbidden)
		}
	}
}

func newTestRestoreExecutor(t *testing.T, store *fakeRepoCreateStore, runner *fakeJVSRunner, now time.Time) *RestoreExecutor {
	t.Helper()
	executor, err := NewRestoreExecutor(RestoreConfig{
		Store:        store,
		JVSRunner:    runner,
		Owner:        "worker-a",
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "audit_restore" },
		VolumeRoots:  store.volumeRoots,
	})
	if err != nil {
		t.Fatalf("NewRestoreExecutor: %v", err)
	}
	return executor
}

func restoreLeasedRecord(now time.Time) operations.OperationRecord {
	leaseExpires := now.Add(time.Hour)
	return operations.OperationRecord{
		ID:                  "op_restore",
		Type:                operations.OperationRestore,
		State:               operations.OperationStateRunning,
		Phase:               operations.OperationPhaseRestoreValidate,
		Attempt:             1,
		LeaseOwner:          "worker-a",
		LeaseExpiresAt:      &leaseExpires,
		CorrelationID:       "corr_restore",
		CallerService:       "product-caller",
		AuthorizedActor:     operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:            operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:         "ns_alpha01",
		RepoID:              "repo_alpha01",
		InputSummary:        map[string]any{"save_point_id": "sp_001", "discard_unsaved_changes_confirmed": true},
		ExternalResourceIDs: map[string]string{},
		CreatedAt:           now.Add(-time.Hour),
		StartedAt:           &now,
	}
}
