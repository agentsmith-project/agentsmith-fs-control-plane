package repoexec

import (
	"context"
	"errors"
	"fmt"
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
		directRestoreSummary: jvsrunner.DirectRestoreSummary{RestoredSavePointID: "sp_001", PreviousHeadID: "sp_002", NewHeadID: "sp_001"},
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
	if strings.Join(runner.calls, ",") != "direct_restore,direct_status" {
		t.Fatalf("JVS calls = %#v, want direct restore then status", runner.calls)
	}
	if runner.restoreSavePointID != "sp_001" {
		t.Fatalf("restore save point id = %q, want sp_001", runner.restoreSavePointID)
	}
	if !strings.HasSuffix(runner.directTarget.ControlRoot, "/afscp/namespaces/ns_alpha01/repos/repo_alpha01/control") ||
		!strings.HasSuffix(runner.directTarget.Home, "/afscp/namespaces/ns_alpha01/repos/repo_alpha01/payload") {
		t.Fatalf("direct target = %#v, want resolved control and payload roots", runner.directTarget)
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
	assertCloneEvidenceProjection(t, store.operation.JVSJSONOutput, "restore")
	assertCloneEvidenceProjection(t, store.operation.VerificationResult, "restore")
	assertCloneEvidenceProjection(t, store.auditEvents[0].Details, "restore")
	assertNoRestoreRunCommandLeak(t, store.operation, store.auditEvents)
}

func assertNoRestoreRunCommandLeak(t *testing.T, operation operations.OperationRecord, events []audit.Event) {
	t.Helper()
	rendered := strings.ToLower(fmt.Sprint(operation.InputSummary, operation.JVSJSONOutput, operation.VerificationResult, operation.Error))
	for _, event := range events {
		rendered += " " + strings.ToLower(fmt.Sprint(event.Details))
	}
	for _, leaked := range []string{"run_command", "recommended_next_command", "restore_command", "jvs restore --run"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("restore persisted raw command marker %q in operation/events: %#v %#v", leaked, operation, events)
		}
	}
	if strings.Contains(rendered, "discard_unsaved_changes_confirmed") {
		t.Fatalf("restore persisted legacy request confirmation in operation/events: %#v %#v", operation, events)
	}
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
	runner := &fakeJVSRunner{directRestoreErr: &jvsrunner.CommandError{Command: "afscp restore", ExitCode: 1, Code: "E_RESTORE_FAILED"}}
	executor := newTestRestoreExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), restoreLeasedRecord(now), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "direct_restore" {
		t.Fatalf("JVS calls = %#v, want direct restore only", runner.calls)
	}
	if store.operation.State != operations.OperationStateFailed || store.operation.Error == nil || store.operation.Error.Code != "JVS_RESTORE_FAILED" {
		t.Fatalf("operation = %#v, want failed JVS_RESTORE_FAILED", store.operation)
	}
}

func TestRestoreExecutorJVSRecoveryRequiredCommitsOperatorIntervention(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{directRestoreErr: &jvsrunner.CommandError{Command: "afscp restore", ExitCode: 3, Code: "JVS_JOURNAL_RECOVERY_REQUIRED"}}
	executor := newTestRestoreExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), restoreLeasedRecord(now), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if strings.Join(runner.calls, ",") != "direct_restore" {
		t.Fatalf("JVS calls = %#v, want direct restore only", runner.calls)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.operation.Error == nil || store.operation.Error.Code != "JVS_RESTORE_RECOVERY_REQUIRED" {
		t.Fatalf("operation = %#v, want operator intervention JVS_RESTORE_RECOVERY_REQUIRED", store.operation)
	}
	if store.releasedFenceID != "" || activeWriterFenceCount(store.fences, "op_restore") != 1 {
		t.Fatalf("released/active writer fence = %q/%#v, want retained fence for recovery required", store.releasedFenceID, store.fences)
	}
}

func TestRestoreExecutorStatusAllowsRealJVSCleanupPendingRecoveryAsNonBlockingEvidence(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		directRestoreSummary: jvsrunner.DirectRestoreSummary{RestoredSavePointID: "sp_001", PreviousHeadID: "sp_002", NewHeadID: "sp_001"},
		directStatusSummary:  jvsrunner.DirectStatusSummary{HistoryHeadID: "sp_001", MetadataState: "ready", ActiveOperation: "none", Recovery: "cleanup_pending"},
	}
	executor := newTestRestoreExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), restoreLeasedRecord(now), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "direct_restore,direct_status" {
		t.Fatalf("JVS calls = %#v, want direct restore then status", runner.calls)
	}
	if store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseRestoreCommitted {
		t.Fatalf("operation = %#v, want succeeded restore_committed", store.operation)
	}
	verification := asStringAnyMap(store.operation.VerificationResult)
	if verification["cleanup_pending"] != true || verification["recovery_required"] != false {
		t.Fatalf("verification = %#v, want non-blocking cleanup evidence", verification)
	}
}

func TestRestoreExecutorStatusRecoveryRetainsWriterFence(t *testing.T) {
	now := repoExecNow()
	for _, tt := range []struct {
		name   string
		status jvsrunner.DirectStatusSummary
	}{
		{name: "journal recovery", status: jvsrunner.DirectStatusSummary{HistoryHeadID: "sp_001", MetadataState: "ready", ActiveOperation: "none", Recovery: "journal_recovery_required"}},
		{name: "operator intervention", status: jvsrunner.DirectStatusSummary{HistoryHeadID: "sp_001", MetadataState: "ready", ActiveOperation: "none", Recovery: "operator_intervention_required"}},
		{name: "blocking cleanup", status: jvsrunner.DirectStatusSummary{HistoryHeadID: "sp_001", MetadataState: "ready", ActiveOperation: "cleanup_blocking", Recovery: "none"}},
		{name: "legacy recovery cleanup token", status: jvsrunner.DirectStatusSummary{HistoryHeadID: "sp_001", MetadataState: "ready", ActiveOperation: "none", Recovery: "cleanup_non_blocking"}},
		{name: "legacy active cleanup token", status: jvsrunner.DirectStatusSummary{HistoryHeadID: "sp_001", MetadataState: "ready", ActiveOperation: "cleanup_non_blocking", Recovery: "none"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			store.repo = activeRepoResource(now)
			runner := &fakeJVSRunner{
				directRestoreSummary: jvsrunner.DirectRestoreSummary{RestoredSavePointID: "sp_001", PreviousHeadID: "sp_002", NewHeadID: "sp_001"},
				directStatusSummary:  tt.status,
			}
			executor := newTestRestoreExecutor(t, store, runner, now)

			err := executor.ExecuteOperationRecovery(context.Background(), restoreLeasedRecord(now), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
			if !errors.Is(err, recovery.ErrOperationManualIntervention) {
				t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
			}
			if strings.Join(runner.calls, ",") != "direct_restore,direct_status" {
				t.Fatalf("JVS calls = %#v, want direct restore then status", runner.calls)
			}
			if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.operation.Error == nil || store.operation.Error.Code != "JVS_RESTORE_RECOVERY_REQUIRED" {
				t.Fatalf("operation = %#v, want recovery-required intervention", store.operation)
			}
			if store.releasedFenceID != "" || activeWriterFenceCount(store.fences, "op_restore") != 1 {
				t.Fatalf("released/active writer fence = %q/%#v, want retained writer fence", store.releasedFenceID, store.fences)
			}
			assertNoRestoreRunCommandLeak(t, store.operation, store.auditEvents)
		})
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
		"DirectDoctor(",
		"validateDirectRestoreStatus",
		"validateDirectRestoreDoctor",
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
		InputSummary:        map[string]any{"save_point_id": "sp_001"},
		ExternalResourceIDs: map[string]string{},
		CreatedAt:           now.Add(-time.Hour),
		StartedAt:           &now,
	}
}
