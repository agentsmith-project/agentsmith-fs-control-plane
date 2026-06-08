package repoexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

func TestExecutorFirstAttemptInitializesDoctorsAndCommitsRepo(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	runner := &fakeJVSRunner{initSummary: jvsrunner.InitSummary{RepoID: "jvs_repo_alpha", Workspace: "main"}, doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
	executor := newTestExecutor(t, store, runner, now)
	record := repoCreateLeasedRecord(now, 1)

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "init,direct_doctor" {
		t.Fatalf("JVS calls = %#v, want init then direct doctor", runner.calls)
	}
	if !strings.HasSuffix(runner.payloadRoot, "/afscp/namespaces/ns_alpha01/repos/repo_alpha01/payload") || !strings.HasSuffix(runner.controlRoot, "/afscp/namespaces/ns_alpha01/repos/repo_alpha01/control") {
		t.Fatalf("roots = payload %q control %q", runner.payloadRoot, runner.controlRoot)
	}
	if store.repo.ID != "repo_alpha01" || store.repo.VolumeID != "vol_123" || store.repo.JVSRepoID != "jvs_repo_alpha" || store.repo.Status != resources.RepoStatusActive {
		t.Fatalf("repo commit = %#v", store.repo)
	}
	if store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseRepoCreateCommitted {
		t.Fatalf("operation = %#v, want succeeded committed", store.operation)
	}
	if store.releasedFenceID != "fence_op_repo" {
		t.Fatalf("released fence = %q, want created fence", store.releasedFenceID)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Outcome != audit.OutcomeSucceeded {
		t.Fatalf("audit events = %#v, want succeeded", store.auditEvents)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestExecutorFirstAttemptRequiresReadyMetadataAfterInit(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	runner := &fakeJVSRunner{
		initSummary: jvsrunner.InitSummary{RepoID: "jvs_repo_alpha", Workspace: "main"},
		directDoctorSummary: jvsrunner.DirectDoctorSummary{
			RepoID:        "jvs_repo_alpha",
			Healthy:       true,
			FindingCount:  0,
			MetadataState: "uninitialized",
			Journal:       "clean",
			Recovery:      "none",
		},
	}
	executor := newTestExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "init,direct_doctor" {
		t.Fatalf("JVS calls = %#v, want init then direct doctor", runner.calls)
	}
	if store.repo.ID != "" {
		t.Fatalf("repo commit = %#v, want no active repo when doctor metadata is not ready", store.repo)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.operation.Error == nil || store.operation.Error.Code != "JVS_DOCTOR_FAILED" {
		t.Fatalf("operation = %#v, want doctor intervention for non-ready metadata", store.operation)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestExecutorSuccessCommitSurvivesCallerContextCancellationAfterJVS(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.failOnCanceledCommitContext = true
	ctx, cancel := context.WithCancel(context.Background())
	runner := &fakeJVSRunner{
		initSummary:   jvsrunner.InitSummary{RepoID: "jvs_repo_alpha", Workspace: "main"},
		doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"},
		afterDoctor: func(context.Context) {
			cancel()
		},
	}
	executor := newTestExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(ctx, repoCreateLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("test context was not cancelled after JVS success")
	}
	if store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseRepoCreateCommitted {
		t.Fatalf("operation = %#v, want succeeded committed despite caller context cancellation", store.operation)
	}
	if store.releasedFenceID != "fence_op_repo" {
		t.Fatalf("released fence = %q, want created fence", store.releasedFenceID)
	}
}

func TestSavePointExecutorSavesWithoutPreSaveHistoryList(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		saveSummary: jvsrunner.SaveSummary{SavePointID: "sp_after", NewestSavePointID: "sp_after", Workspace: "main", CreatedAt: "2026-05-05T12:00:00Z"},
	}
	executor := newTestSavePointExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), savePointLeasedRecord(now, operations.OperationPhaseSavePointCreateValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "direct_save,direct_list" {
		t.Fatalf("JVS calls = %#v, want direct_save,direct_list", runner.calls)
	}
	if store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseSavePointCreateCommitted {
		t.Fatalf("operation = %#v, want succeeded committed", store.operation)
	}
	result := store.operation.VerificationResult.(map[string]any)
	if _, exists := result["pre_save_newest_save_point_id"]; exists {
		t.Fatalf("verification = %#v, want no pre-save marker", result)
	}
	if result["save_point_id"] != "sp_after" || result["unsaved_changes_known"] != false || result["history_visible"] != true || result["history_head_id"] != "sp_after" {
		t.Fatalf("verification = %#v, want list-visible save result", result)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestSavePointExecutorUsesDirectSaveTarget(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		directSaveSummary: jvsrunner.DirectSaveSummary{SavePointID: "sp_after", HistoryHeadID: "sp_after", Message: "checkpoint", CreatedAt: "2026-05-05T12:00:00Z"},
	}
	executor := newTestSavePointExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), savePointLeasedRecord(now, operations.OperationPhaseSavePointCreateValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "direct_save,direct_list" {
		t.Fatalf("JVS calls = %#v, want direct_save,direct_list", runner.calls)
	}
	if !strings.HasSuffix(runner.directTarget.ControlRoot, "/afscp/namespaces/ns_alpha01/repos/repo_alpha01/control") ||
		!strings.HasSuffix(runner.directTarget.Home, "/afscp/namespaces/ns_alpha01/repos/repo_alpha01/payload") {
		t.Fatalf("direct target = %#v, want resolved control and payload roots", runner.directTarget)
	}
	if runner.saveMessage != "checkpoint" {
		t.Fatalf("direct save message = %q, want durable normalized message", runner.saveMessage)
	}
	verification := store.operation.VerificationResult.(map[string]any)
	if verification["save_point_id"] != "sp_after" {
		t.Fatalf("verification = %#v, want direct save result", verification)
	}
	assertCloneEvidenceProjection(t, store.operation.JVSJSONOutput, "save")
	assertCloneEvidenceProjection(t, store.operation.VerificationResult, "save")
	assertCloneEvidenceProjection(t, store.auditEvents[0].Details, "save")
	if _, exists := verification["pre_save_history_captured"]; exists {
		t.Fatalf("verification = %#v, want no pre-save history marker", verification)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestSavePointExecutorDoesNotCommitSuccessUntilDirectSaveIsListVisible(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		directSaveSummary: jvsrunner.DirectSaveSummary{SavePointID: "sp_after", HistoryHeadID: "sp_after", Message: "checkpoint", CreatedAt: "2026-05-05T12:00:00Z"},
		directListSummary: jvsrunner.DirectListSummary{
			HistoryHeadID: "sp_before",
			SavePoints: []jvsrunner.DirectSavePointSummary{{
				SavePointID: "sp_before",
				Message:     "before",
				CreatedAt:   "2026-05-05T11:00:00Z",
				HistoryHead: true,
			}},
		},
	}
	executor := newTestSavePointExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), savePointLeasedRecord(now, operations.OperationPhaseSavePointCreateValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "direct_save,direct_list" {
		t.Fatalf("JVS calls = %#v, want direct_save,direct_list", runner.calls)
	}
	if store.operation.State != operations.OperationStateFailed || store.operation.Phase != operations.OperationPhaseSavePointCreateValidate {
		t.Fatalf("operation = %#v, want failed validate operation", store.operation)
	}
	if store.operation.Error == nil || store.operation.Error.Code != "SAVE_POINT_NOT_VISIBLE" || store.operation.Error.Retryable {
		t.Fatalf("operation error = %#v, want non-retryable SAVE_POINT_NOT_VISIBLE", store.operation.Error)
	}
	if store.operation.Error.Details["save_point_id"] != "sp_after" || store.operation.Error.Details["history_head_id"] != "sp_before" {
		t.Fatalf("operation error details = %#v, want redacted visibility failure facts", store.operation.Error.Details)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Reason != "save_point_create_failed" {
		t.Fatalf("audit events = %#v, want failed save point audit", store.auditEvents)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestSavePointExecutorRequiresDirectListEvidenceToMatchDirectSaveSummary(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		directSaveSummary: jvsrunner.DirectSaveSummary{SavePointID: "sp_after", HistoryHeadID: "sp_after", Message: "checkpoint", CreatedAt: "2026-05-05T12:00:00Z"},
		directListSummary: jvsrunner.DirectListSummary{
			HistoryHeadID: "sp_after",
			SavePoints: []jvsrunner.DirectSavePointSummary{{
				SavePointID: "sp_after",
				Message:     "different message",
				CreatedAt:   "2026-05-05T12:00:00Z",
				HistoryHead: true,
			}},
		},
	}
	executor := newTestSavePointExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), savePointLeasedRecord(now, operations.OperationPhaseSavePointCreateValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.operation.State != operations.OperationStateFailed || store.operation.Error == nil || store.operation.Error.Code != "SAVE_POINT_NOT_VISIBLE" {
		t.Fatalf("operation = %#v, want failed SAVE_POINT_NOT_VISIBLE", store.operation)
	}
	if store.operation.Error.Details["message_match"] != false || store.operation.Error.Details["created_at_match"] != true || store.operation.Error.Details["purpose_match"] != true {
		t.Fatalf("operation error details = %#v, want list evidence mismatch facts", store.operation.Error.Details)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestSavePointExecutorBlocksUndrainedWriterBeforeDirectSave(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	store.mounts = []sessionstate.WorkloadMountBinding{
		savePointRepoExecMountFixture(now, false, sessionstate.MountStatusReleasing, nil, nil),
	}
	if decision := sessionstate.RestoreWriterGate(sessionstate.GateRequest{NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", Now: now, Mounts: store.mounts}); decision.Allowed {
		t.Fatalf("test fixture writer gate decision = %#v, want denied", decision)
	}
	runner := &fakeJVSRunner{
		directSaveSummary: jvsrunner.DirectSaveSummary{SavePointID: "sp_after", HistoryHeadID: "sp_after", Message: "checkpoint", CreatedAt: "2026-05-05T12:00:00Z"},
	}
	executor := newTestSavePointExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), savePointLeasedRecord(now, operations.OperationPhaseSavePointCreateValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if err != nil {
		t.Fatalf("ExecuteOperationRecovery error = %v, want pending marker without recovery error", err)
	}
	if store.listExportSessionCalls != 1 || store.listMountSessionCalls != 1 {
		t.Fatalf("session state calls export/mount = %d/%d, want 1/1", store.listExportSessionCalls, store.listMountSessionCalls)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("JVS calls = %#v, want writer drain gate before DirectSave", runner.calls)
	}
	if store.operation.State != operations.OperationStateRunning || store.operation.Phase != operations.OperationPhaseSavePointCreateValidate || store.operation.Error == nil || store.operation.Error.Code != "SAVE_POINT_WRITER_DRAIN_PENDING" || !store.operation.Error.Retryable {
		t.Fatalf("operation = %#v, want running writer-drain pending", store.operation)
	}
	if store.operation.FinishedAt != nil || store.operation.LeaseExpiresAt == nil || !store.operation.LeaseExpiresAt.Equal(now) {
		t.Fatalf("operation lease/finish = %v/%v, want lease expired at now and no finish", store.operation.LeaseExpiresAt, store.operation.FinishedAt)
	}
	if got := store.operation.VerificationResult.(map[string]any)["writer_drain_status"]; got != "pending" {
		t.Fatalf("verification = %#v, want writer_drain_status pending", store.operation.VerificationResult)
	}
	if got := store.operation.VerificationResult.(map[string]any)["writer_gate_error_family"]; got == "" {
		t.Fatalf("verification = %#v, want writer_gate_error_family", store.operation.VerificationResult)
	}
	if len(store.auditEvents) != 0 {
		t.Fatalf("audit events = %#v, want no terminal audit for pending writer drain", store.auditEvents)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestSavePointExecutorReclaimsWriterDrainPendingAfterDrain(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		directSaveSummary: jvsrunner.DirectSaveSummary{SavePointID: "sp_after", HistoryHeadID: "sp_after", Message: "checkpoint", CreatedAt: "2026-05-05T12:00:00Z"},
	}
	executor := newTestSavePointExecutor(t, store, runner, now)
	record := savePointLeasedRecord(now, operations.OperationPhaseSavePointCreateValidate)
	record.Error = &operations.OperationError{Code: "SAVE_POINT_WRITER_DRAIN_PENDING", Message: "save point writer drain is pending", Retryable: true, CorrelationID: record.CorrelationID, OperationID: record.ID}

	err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionReclaim})
	if err != nil {
		t.Fatalf("ExecuteOperationRecovery reclaim: %v", err)
	}
	if strings.Join(runner.calls, ",") != "direct_save,direct_list" {
		t.Fatalf("JVS calls = %#v, want direct_save,direct_list after writer drain", runner.calls)
	}
	if store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseSavePointCreateCommitted {
		t.Fatalf("operation = %#v, want succeeded after reclaim", store.operation)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Reason != "save_point_create_committed" {
		t.Fatalf("audit events = %#v, want committed save point audit", store.auditEvents)
	}
}

func TestSavePointExecutorDirectSaveRepoBusyFailsWithoutLegacyRepair(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		directListSummary: jvsrunner.DirectListSummary{
			HistoryHeadID: "sp_before",
			SavePoints:    []jvsrunner.DirectSavePointSummary{{SavePointID: "sp_before", Message: "before", CreatedAt: "2026-05-05T11:00:00Z", HistoryHead: true}},
		},
		directSaveErr: &jvsrunner.CommandError{Command: "afscp save", ExitCode: 1, Code: "E_REPO_BUSY"},
	}
	executor := newTestSavePointExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), savePointLeasedRecord(now, operations.OperationPhaseSavePointCreateValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "direct_save" {
		t.Fatalf("JVS calls = %#v, want direct save without list or doctor repair runtime", runner.calls)
	}
	if store.operation.State != operations.OperationStateFailed || store.operation.Error == nil || store.operation.Error.Code != "JVS_COMMAND_FAILED" || !store.operation.Error.Retryable {
		t.Fatalf("operation = %#v, want retryable failed JVS_COMMAND_FAILED", store.operation)
	}
	if _, exists := store.operation.VerificationResult.(map[string]any)["jvs_repair_attempted"]; exists {
		t.Fatalf("verification = %#v, direct save must not record repair runtime attempt", store.operation.VerificationResult)
	}
}

func TestSavePointExecutorDirectSaveCommandErrorFailsTerminallyWithoutManualIntervention(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		directSaveErr: &jvsrunner.CommandError{Command: "afscp save", ExitCode: 1, Code: "E_PERMISSION_DENIED"},
	}
	executor := newTestSavePointExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), savePointLeasedRecord(now, operations.OperationPhaseSavePointCreateValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if err != nil {
		t.Fatalf("ExecuteOperationRecovery error = %v, want terminal failed commit without recovery error", err)
	}
	if errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want no manual intervention", err)
	}
	if strings.Join(runner.calls, ",") != "direct_save" {
		t.Fatalf("JVS calls = %#v, want direct_save only", runner.calls)
	}
	if store.operation.State != operations.OperationStateFailed || store.operation.Phase != operations.OperationPhaseSavePointCreateValidate {
		t.Fatalf("operation = %#v, want failed validate operation", store.operation)
	}
	if store.operation.Error == nil || store.operation.Error.Code != "JVS_COMMAND_FAILED" || store.operation.Error.Retryable {
		t.Fatalf("operation error = %#v, want non-retryable JVS_COMMAND_FAILED", store.operation.Error)
	}
	if store.operation.Error.Details["jvs_error_code"] != "E_PERMISSION_DENIED" || store.operation.Error.Details["jvs_command"] != "afscp save" || store.operation.Error.Details["jvs_exit_code"] != 1 {
		t.Fatalf("operation error details = %#v, want safe direct command details", store.operation.Error.Details)
	}
	if store.operation.VerificationResult.(map[string]any)["jvs_error_code"] != "E_PERMISSION_DENIED" {
		t.Fatalf("verification = %#v, want safe direct command details", store.operation.VerificationResult)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Reason != "save_point_create_failed" {
		t.Fatalf("audit events = %#v, want ordinary failed save point audit", store.auditEvents)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestSavePointExecutorDirectSaveAmbiguousFailuresRequireManualIntervention(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name string
		edit func(*fakeJVSRunner)
	}{
		{name: "plain error", edit: func(runner *fakeJVSRunner) {
			runner.directSaveErr = errors.New("direct save process failed after possible write")
		}},
		{name: "context deadline", edit: func(runner *fakeJVSRunner) {
			runner.directSaveErr = fmt.Errorf("%w: afscp save: %w", jvsrunner.ErrCommandFailed, context.DeadlineExceeded)
		}},
		{name: "invalid envelope", edit: func(runner *fakeJVSRunner) {
			runner.directSaveErr = fmt.Errorf("%w: afscp save", jvsrunner.ErrInvalidEnvelope)
		}},
		{name: "command error with side effect evidence", edit: func(runner *fakeJVSRunner) {
			runner.directSaveSummary = jvsrunner.DirectSaveSummary{SavePointID: "sp_possible", HistoryHeadID: "sp_possible", CreatedAt: "2026-05-05T12:00:00Z"}
			runner.directSaveErr = &jvsrunner.CommandError{Command: "afscp save", ExitCode: 1, Code: "E_OUTPUT_MISMATCH"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			store.repo = activeRepoResource(now)
			runner := &fakeJVSRunner{}
			tt.edit(runner)
			executor := newTestSavePointExecutor(t, store, runner, now)

			err := executor.ExecuteOperationRecovery(context.Background(), savePointLeasedRecord(now, operations.OperationPhaseSavePointCreateValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
			if !errors.Is(err, recovery.ErrOperationManualIntervention) {
				t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
			}
			if strings.Join(runner.calls, ",") != "direct_save" {
				t.Fatalf("JVS calls = %#v, want direct_save only", runner.calls)
			}
			if store.operation.State != operations.OperationStateOperatorInterventionRequired {
				t.Fatalf("operation = %#v, want operator intervention", store.operation)
			}
			if len(store.auditEvents) != 1 || store.auditEvents[0].Reason != "save_point_create_operator_intervention_required" {
				t.Fatalf("audit events = %#v, want operator intervention audit", store.auditEvents)
			}
			assertNoRepoExecLeak(t, store.operation, store.auditEvents)
		})
	}
}

func TestSavePointExecutorUsesFreshTerminalTimestampAfterSlowJVSSave(t *testing.T) {
	started := repoExecNow()
	terminal := started.Add(118 * time.Second)
	clockNow := started
	store := newFakeStore()
	store.repo = activeRepoResource(started)
	runner := &fakeJVSRunner{
		historySummary: jvsrunner.HistorySummary{Workspace: "main", NewestSavePointID: "sp_before", SavePoints: []jvsrunner.SavePointSummary{{SavePointID: "sp_before", Message: "before", CreatedAt: "2026-05-05T11:00:00Z"}}},
		saveSummary:    jvsrunner.SaveSummary{SavePointID: "sp_after", NewestSavePointID: "sp_after", Workspace: "main", CreatedAt: "2026-05-05T12:01:58Z"},
		afterSave: func() {
			clockNow = terminal
		},
	}
	executor, err := NewSavePointExecutor(SavePointConfig{
		Store:        store,
		JVSRunner:    runner,
		Owner:        "worker-a",
		Clock:        func() time.Time { return clockNow },
		AuditEventID: func() string { return "audit_savepoint" },
		VolumeRoots:  store.volumeRoots,
	})
	if err != nil {
		t.Fatalf("NewSavePointExecutor: %v", err)
	}

	if err := executor.ExecuteOperationRecovery(context.Background(), savePointLeasedRecord(started, operations.OperationPhaseSavePointCreateValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}

	if store.operation.FinishedAt == nil || !store.operation.FinishedAt.Equal(terminal) {
		t.Fatalf("finished_at = %v, want terminal time %v", store.operation.FinishedAt, terminal)
	}
	if len(store.auditEvents) != 1 || !store.auditEvents[0].Time.Equal(terminal) {
		t.Fatalf("audit time = %#v, want terminal time %v", store.auditEvents, terminal)
	}
	if !store.savePointSuccessCommitAt.Equal(terminal) {
		t.Fatalf("success commit time = %v, want terminal time %v", store.savePointSuccessCommitAt, terminal)
	}
	if store.operation.FinishedAt.Equal(started) || store.auditEvents[0].Time.Equal(started) || store.savePointSuccessCommitAt.Equal(started) {
		t.Fatalf("terminal timestamps reused started time %v: operation=%#v audit=%#v commit=%v", started, store.operation, store.auditEvents, store.savePointSuccessCommitAt)
	}
}

func TestSavePointExecutorRepoBusyFailsTerminallyWithoutManualIntervention(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		historySummary: jvsrunner.HistorySummary{Workspace: "main", NewestSavePointID: "sp_before", SavePoints: []jvsrunner.SavePointSummary{{SavePointID: "sp_before", Message: "before", CreatedAt: "2026-05-05T11:00:00Z"}}},
		saveErr:        &jvsrunner.CommandError{Command: "save", ExitCode: 1, Code: "E_REPO_BUSY"},
	}
	executor := newTestSavePointExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), savePointLeasedRecord(now, operations.OperationPhaseSavePointCreateValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}

	if strings.Join(runner.calls, ",") != "direct_save" {
		t.Fatalf("JVS calls = %#v, want direct save without list or repair runtime", runner.calls)
	}
	if store.operation.State != operations.OperationStateFailed {
		t.Fatalf("operation state = %s, want failed terminal state", store.operation.State)
	}
	if store.operation.Error == nil || store.operation.Error.Code != "JVS_COMMAND_FAILED" || !store.operation.Error.Retryable {
		t.Fatalf("operation error = %#v, want retryable JVS_COMMAND_FAILED", store.operation.Error)
	}
	if store.operation.Error.Details["jvs_error_code"] != "E_REPO_BUSY" || store.operation.Error.Details["repo_id"] != "repo_alpha01" {
		t.Fatalf("operation error details = %#v, want safe repo-busy details", store.operation.Error.Details)
	}
	if _, exists := store.operation.Error.Details["jvs_repair_attempted"]; exists {
		t.Fatalf("operation error details = %#v, direct save must not attempt repair runtime", store.operation.Error.Details)
	}
	if store.operation.VerificationResult.(map[string]any)["jvs_error_code"] != "E_REPO_BUSY" {
		t.Fatalf("verification = %#v, want repo-busy marker for product projection", store.operation.VerificationResult)
	}
	if _, exists := store.operation.VerificationResult.(map[string]any)["jvs_repair_error_code"]; exists {
		t.Fatalf("verification = %#v, direct save must not record repair failure marker", store.operation.VerificationResult)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Reason != "save_point_create_failed" {
		t.Fatalf("audit events = %#v, want ordinary failed save point audit", store.auditEvents)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestSavePointExecutorDoesNotRepairRetryTransientRepoBusySave(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		historySummary: jvsrunner.HistorySummary{Workspace: "main", NewestSavePointID: "sp_before", SavePoints: []jvsrunner.SavePointSummary{{SavePointID: "sp_before", Message: "before", CreatedAt: "2026-05-05T11:00:00Z"}}},
		saveSummary:    jvsrunner.SaveSummary{SavePointID: "sp_after", NewestSavePointID: "sp_after", Workspace: "main", CreatedAt: "2026-05-05T12:00:00Z", UnsavedChanges: true},
		saveErrs:       []error{&jvsrunner.CommandError{Command: "save", ExitCode: 1, Code: "E_REPO_BUSY"}, nil},
	}
	executor := newTestSavePointExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), savePointLeasedRecord(now, operations.OperationPhaseSavePointCreateValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}

	if strings.Join(runner.calls, ",") != "direct_save" {
		t.Fatalf("JVS calls = %#v, want direct save without list or repair retry", runner.calls)
	}
	if store.operation.State != operations.OperationStateFailed || store.operation.Error == nil || store.operation.Error.Code != "JVS_COMMAND_FAILED" {
		t.Fatalf("operation = %#v, want failed JVS_COMMAND_FAILED without repair retry", store.operation)
	}
	result := store.operation.VerificationResult.(map[string]any)
	if result["jvs_error_code"] != "E_REPO_BUSY" {
		t.Fatalf("verification = %#v, want repo-busy marker", result)
	}
	if _, exists := result["jvs_repair_attempted"]; exists {
		t.Fatalf("verification = %#v, direct save must not record repair evidence", result)
	}
}

func TestSavePointExecutorUsesDurableNaturalLanguageMessageWithoutRedaction(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		historySummary: jvsrunner.HistorySummary{Workspace: "main", NewestSavePointID: "sp_before", SavePoints: []jvsrunner.SavePointSummary{{SavePointID: "sp_before", Message: "before", CreatedAt: "2026-05-05T11:00:00Z"}}},
		saveSummary:    jvsrunner.SaveSummary{SavePointID: "sp_after", NewestSavePointID: "sp_after", Workspace: "main", CreatedAt: "2026-05-05T12:00:00Z"},
	}
	executor := newTestSavePointExecutor(t, store, runner, now)
	record := savePointLeasedRecord(now, operations.OperationPhaseSavePointCreateValidate)
	record.InputSummary["message"] = "fix secret handling"

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if runner.saveMessage != "fix secret handling" {
		t.Fatalf("jvs save message = %q, want original natural-language message", runner.saveMessage)
	}
	if got := store.operation.InputSummary["message"]; got != "fix secret handling" {
		t.Fatalf("persisted input message = %#v, want original natural-language message", got)
	}
	jvsOutput := store.operation.JVSJSONOutput.(map[string]any)
	if got := jvsOutput["message"]; got != "fix secret handling" {
		t.Fatalf("persisted jvs message = %#v, want original natural-language message", got)
	}
}

func TestSavePointExecutorRejectsSecretShapedMessageBeforeJVS(t *testing.T) {
	for _, message := range []string{"token=savepoint-message-secret", "[REDACTED]"} {
		t.Run(message, func(t *testing.T) {
			now := repoExecNow()
			store := newFakeStore()
			store.repo = activeRepoResource(now)
			runner := &fakeJVSRunner{}
			executor := newTestSavePointExecutor(t, store, runner, now)
			record := savePointLeasedRecord(now, operations.OperationPhaseSavePointCreateValidate)
			record.InputSummary["message"] = message

			if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err == nil {
				t.Fatal("ExecuteOperationRecovery succeeded, want invalid message error")
			}
			if len(runner.calls) != 0 {
				t.Fatalf("JVS calls = %#v, want none for invalid message", runner.calls)
			}
			if store.operation.ID != "" {
				t.Fatalf("operation was committed unexpectedly: %#v", store.operation)
			}
		})
	}
}

func TestSavePointExecutorPreparedRecoveryRequiresManualInterventionWithoutJVS(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{}
	executor := newTestSavePointExecutor(t, store, runner, now)
	record := savePointLeasedRecord(now, operations.OperationPhaseSavePointCreatePrepared)
	record.VerificationResult = map[string]any{"pre_save_history_captured": true, "pre_save_newest_save_point_id": "sp_before"}

	err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionReclaim})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("JVS calls = %#v, want no save/list for obsolete prepared recovery", runner.calls)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired {
		t.Fatalf("operation state = %s, want operator intervention", store.operation.State)
	}
}

func TestSavePointExecutorRetryOrUnmarkedReclaimRequiresManualInterventionWithoutJVS(t *testing.T) {
	for _, action := range []recovery.RecoveryAction{recovery.RecoveryActionRetry, recovery.RecoveryActionReclaim} {
		t.Run(string(action), func(t *testing.T) {
			now := repoExecNow()
			store := newFakeStore()
			store.repo = activeRepoResource(now)
			runner := &fakeJVSRunner{}
			executor := newTestSavePointExecutor(t, store, runner, now)

			err := executor.ExecuteOperationRecovery(context.Background(), savePointLeasedRecord(now, operations.OperationPhaseSavePointCreateValidate), recovery.RecoveryPlan{Action: action})
			if !errors.Is(err, recovery.ErrOperationManualIntervention) {
				t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("JVS calls = %#v, want no save/list for %s recovery", runner.calls, action)
			}
			if store.operation.State != operations.OperationStateOperatorInterventionRequired {
				t.Fatalf("operation state = %s, want operator intervention", store.operation.State)
			}
		})
	}
}

func TestExecutorRetryWithSameOperationFenceAdoptsHealthyDoctorWithoutInit(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.fences = []fences.Fence{repoCreateFence(now, "fence_existing", "op_repo")}
	runner := &fakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
	executor := newTestExecutor(t, store, runner, now)
	record := repoCreateLeasedRecord(now, 2)

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "direct_doctor" {
		t.Fatalf("JVS calls = %#v, want direct doctor-only adoption", runner.calls)
	}
	if store.operation.State != operations.OperationStateSucceeded || store.operation.VerificationResult.(map[string]any)["adopted"] != true {
		t.Fatalf("operation = %#v, want adopted success", store.operation)
	}
	if store.createFenceCalls != 0 || store.releasedFenceID != "fence_existing" {
		t.Fatalf("create/release fence = %d/%q, want reuse existing", store.createFenceCalls, store.releasedFenceID)
	}
}

func TestExecutorStoresSafeJVSErrorDetails(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	runner := &fakeJVSRunner{initErr: &jvsrunner.CommandError{Command: "init", ExitCode: 17, Code: "E_SOURCE_DIRTY"}}
	executor := newTestExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.operation.Error == nil {
		t.Fatalf("operation = %#v, want operator intervention error", store.operation)
	}
	details := store.operation.Error.Details
	if details["jvs_error_code"] != "E_SOURCE_DIRTY" || details["jvs_command"] != "init" || details["jvs_exit_code"] != 17 {
		t.Fatalf("operation error details = %#v, want safe JVS command error details", details)
	}
	if _, exists := details["jvs_message"]; exists {
		t.Fatalf("operation error details leaked message field: %#v", details)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestExecutorFirstAttemptDoesNotAdoptOccupiedHealthyRoots(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	runner := &fakeJVSRunner{
		initErr:       errors.New("E_TARGET_ROOT_OCCUPIED /srv/afscp/secret"),
		doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"},
	}
	executor := newTestExecutor(t, store, runner, now)
	record := repoCreateLeasedRecord(now, 1)

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired {
		t.Fatalf("operation state = %q, want operator intervention", store.operation.State)
	}
	if store.releasedFenceID != "" {
		t.Fatalf("released fence = %q, want kept fence", store.releasedFenceID)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestExecutorPreJVSValidationFailureFailsWithoutJVSOrFenceLeak(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name string
		edit func(*fakeRepoCreateStore)
	}{
		{name: "inactive namespace", edit: func(store *fakeRepoCreateStore) {
			store.namespace.Status = resources.NamespaceStatusDisabled
			disabledAt := now
			store.namespace.DisabledAt = &disabledAt
			store.namespace.DisabledReason = "disabled"
		}},
		{name: "inactive binding", edit: func(store *fakeRepoCreateStore) { store.binding.Status = resources.NamespaceStatusDisabled }},
		{name: "inactive volume", edit: func(store *fakeRepoCreateStore) { store.volume.Status = resources.VolumeStatusDisabled }},
		{name: "missing capability", edit: func(store *fakeRepoCreateStore) { store.volume.Capabilities["jvs_external_control_root"] = false }},
		{name: "missing volume root", edit: func(store *fakeRepoCreateStore) { store.volumeRoots = map[string]string{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			tt.edit(store)
			runner := &fakeJVSRunner{}
			executor := newTestExecutor(t, store, runner, now)

			if err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
				t.Fatalf("ExecuteOperationRecovery: %v", err)
			}
			if len(runner.calls) != 0 || store.createFenceCalls != 0 {
				t.Fatalf("JVS/fence calls = %#v/%d, want none", runner.calls, store.createFenceCalls)
			}
			if store.operation.State != operations.OperationStateFailed {
				t.Fatalf("operation state = %q, want failed", store.operation.State)
			}
			assertNoRepoExecLeak(t, store.operation, store.auditEvents)
		})
	}
}

func TestExecutorMetadataFailureWithSameOperationFenceRequiresIntervention(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.fences = []fences.Fence{repoCreateFence(now, "fence_existing", "op_repo")}
	store.volume.Status = resources.VolumeStatusDisabled
	runner := &fakeJVSRunner{}
	executor := newTestExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 2), recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if len(runner.calls) != 0 || store.createFenceCalls != 0 {
		t.Fatalf("JVS/fence calls = %#v/%d, want none", runner.calls, store.createFenceCalls)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired {
		t.Fatalf("operation state = %q, want operator intervention", store.operation.State)
	}
	if store.releasedFenceID != "" {
		t.Fatalf("released fence = %q, want kept fence", store.releasedFenceID)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestExecutorNonActiveSameOperationFenceRequiresInterventionWithoutJVS(t *testing.T) {
	now := repoExecNow()
	for _, status := range []fences.Status{fences.StatusExpired, fences.StatusRecoveryRequired} {
		t.Run(string(status), func(t *testing.T) {
			store := newFakeStore()
			fence := repoCreateFence(now, "fence_existing", "op_repo")
			fence.Status = status
			store.fences = []fences.Fence{fence}
			runner := &fakeJVSRunner{}
			executor := newTestExecutor(t, store, runner, now)

			if err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 2), recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry}); err != nil {
				t.Fatalf("ExecuteOperationRecovery: %v", err)
			}
			if len(runner.calls) != 0 || store.createFenceCalls != 0 {
				t.Fatalf("JVS/fence calls = %#v/%d, want none", runner.calls, store.createFenceCalls)
			}
			if store.operation.State != operations.OperationStateOperatorInterventionRequired {
				t.Fatalf("operation state = %q, want operator intervention", store.operation.State)
			}
			if store.releasedFenceID != "" {
				t.Fatalf("released fence = %q, want kept fence", store.releasedFenceID)
			}
		})
	}
}

func TestExecutorRetryAfterSuccessCommitFailureAndMetadataInvalidRequiresIntervention(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.successErr = errors.New("commit failed")
	runner := &fakeJVSRunner{initSummary: jvsrunner.InitSummary{RepoID: "jvs_repo_alpha", Workspace: "main"}, doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
	executor := newTestExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if err == nil {
		t.Fatal("ExecuteOperationRecovery succeeded, want commit error")
	}
	if store.createFenceCalls != 1 || len(store.fences) != 1 {
		t.Fatalf("fences = calls %d list %#v, want created same-op fence", store.createFenceCalls, store.fences)
	}

	store.successErr = nil
	store.volume.Status = resources.VolumeStatusDisabled
	runner.calls = nil
	if err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 2), recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry}); err != nil {
		t.Fatalf("retry ExecuteOperationRecovery: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("JVS calls on retry = %#v, want none", runner.calls)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired {
		t.Fatalf("operation state = %q, want operator intervention", store.operation.State)
	}
	if store.releasedFenceID != "" {
		t.Fatalf("released fence = %q, want kept fence", store.releasedFenceID)
	}
}

func TestExecutorInitDoctorRepoIDMismatchRequiresIntervention(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	runner := &fakeJVSRunner{initSummary: jvsrunner.InitSummary{RepoID: "jvs_repo_alpha", Workspace: "main"}, doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_other", Healthy: true, Workspace: "main"}}
	executor := newTestExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.releasedFenceID != "" {
		t.Fatalf("operation/release = %#v/%q, want intervention keeping fence", store.operation, store.releasedFenceID)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestExecutorPropagatesCommitErrorsSafely(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.successErr = fmt.Errorf("%w: postgres password=secret failed", operations.ErrLeaseUnavailable)
	runner := &fakeJVSRunner{initSummary: jvsrunner.InitSummary{RepoID: "jvs_repo_alpha", Workspace: "main"}, doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
	executor := newTestExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if err == nil {
		t.Fatal("ExecuteOperationRecovery succeeded, want commit error")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/srv/afscp") {
		t.Fatalf("error leaked sensitive detail: %v", err)
	}
	if !errors.Is(err, operations.ErrLeaseUnavailable) {
		t.Fatalf("error = %v, want wrapped ErrLeaseUnavailable for recovery classification", err)
	}
}

func newTestExecutor(t *testing.T, store *fakeRepoCreateStore, runner *fakeJVSRunner, now time.Time) *Executor {
	t.Helper()
	executor, err := NewExecutor(Config{
		Store:        store,
		JVSRunner:    runner,
		Owner:        "worker-a",
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "audit_repo" },
		VolumeRoots:  store.volumeRoots,
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	return executor
}

func newTestSavePointExecutor(t *testing.T, store *fakeRepoCreateStore, runner *fakeJVSRunner, now time.Time) *SavePointExecutor {
	t.Helper()
	executor, err := NewSavePointExecutor(SavePointConfig{
		Store:        store,
		JVSRunner:    runner,
		Owner:        "worker-a",
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "audit_savepoint" },
		VolumeRoots:  store.volumeRoots,
	})
	if err != nil {
		t.Fatalf("NewSavePointExecutor: %v", err)
	}
	return executor
}

func TestTemplateCreateExecutorSavesSourceThenClonesAndCommitsTemplate(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.volumeRoots = map[string]string{"vol_123": t.TempDir()}
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		saveSummary:        jvsrunner.SaveSummary{SavePointID: "sp_template01", NewestSavePointID: "sp_template01", Workspace: "main", CreatedAt: "2026-05-05T12:00:00Z"},
		directCloneSummary: jvsrunner.DirectCloneSummary{SourceRepoID: "jvs_repo_alpha", TargetRepoID: "jvs_template_alpha", SavePointsMode: "main", SavePointsCopiedCount: 1, RuntimeStateCopied: false, Workspace: "main"},
		doctorSummary:      jvsrunner.DoctorSummary{RepoID: "jvs_template_alpha", Healthy: true, Workspace: "main"},
	}
	executor, err := NewTemplateCreateExecutor(TemplateConfig{Store: store, JVSRunner: runner, Owner: "worker-a", Clock: func() time.Time { return now }, AuditEventID: func() string { return "audit_template_create" }, VolumeRoots: store.volumeRoots})
	if err != nil {
		t.Fatalf("NewTemplateCreateExecutor: %v", err)
	}

	if err := executor.ExecuteOperationRecovery(context.Background(), templateCreateLeasedRecord(now), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}

	if store.templateCreateWriterFenceMarks != 1 {
		t.Fatalf("writer fence marks = %d, want 1 before JVS", store.templateCreateWriterFenceMarks)
	}
	if got := strings.Join(runner.calls, ","); got != "direct_save,direct_clone" {
		t.Fatalf("jvs calls = %s, want direct_save,direct_clone", got)
	}
	if runner.cloneSavePointID != "sp_template01" {
		t.Fatalf("direct clone save point = %q, want fresh template source save point", runner.cloneSavePointID)
	}
	if runner.savePurpose != "template_source" {
		t.Fatalf("direct save purpose = %q, want template_source", runner.savePurpose)
	}
	verification := asStringAnyMap(store.operation.VerificationResult)
	if store.repo.ID != "tmpl_base01" || store.repo.Kind != resources.RepoKindTemplate || verification["source_save_point_id"] != "sp_template01" {
		t.Fatalf("committed template/operation = %#v %#v", store.repo, store.operation)
	}
	assertCloneEvidenceProjection(t, store.operation.JVSJSONOutput, "save", "clone")
	assertCloneEvidenceProjection(t, store.operation.VerificationResult, "save", "clone")
	assertCloneEvidenceProjection(t, store.auditEvents[0].Details, "save", "clone")
	if store.releasedFenceID != "fence_op_template_create" || activeWriterFenceCount(store.fences, "op_template_create") != 0 {
		t.Fatalf("released/active writer fence = %q/%#v, want released source writer fence", store.releasedFenceID, store.fences)
	}
}

func TestTemplateCreateExecutorUsesFreshTerminalTimestampAfterSlowJVSSaveAndClone(t *testing.T) {
	started := repoExecNow()
	afterSave := started.Add(85 * time.Second)
	terminal := started.Add(118 * time.Second)
	clockNow := started
	store := newFakeStore()
	store.volumeRoots = map[string]string{"vol_123": t.TempDir()}
	store.repo = activeRepoResource(started)
	runner := &fakeJVSRunner{
		saveSummary:        jvsrunner.SaveSummary{SavePointID: "sp_template01", NewestSavePointID: "sp_template01", Workspace: "main", CreatedAt: "2026-05-05T12:01:25Z"},
		directCloneSummary: jvsrunner.DirectCloneSummary{SourceRepoID: "jvs_repo_alpha", TargetRepoID: "jvs_template_alpha", SavePointsMode: "main", SavePointsCopiedCount: 1, RuntimeStateCopied: false, Workspace: "main"},
		doctorSummary:      jvsrunner.DoctorSummary{RepoID: "jvs_template_alpha", Healthy: true, Workspace: "main"},
		afterSave: func() {
			clockNow = afterSave
		},
		afterDirectClone: func() {
			clockNow = terminal
		},
	}
	executor, err := NewTemplateCreateExecutor(TemplateConfig{Store: store, JVSRunner: runner, Owner: "worker-a", Clock: func() time.Time { return clockNow }, AuditEventID: func() string { return "audit_template_create" }, VolumeRoots: store.volumeRoots})
	if err != nil {
		t.Fatalf("NewTemplateCreateExecutor: %v", err)
	}

	if err := executor.ExecuteOperationRecovery(context.Background(), templateCreateLeasedRecord(started), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}

	if store.operation.FinishedAt == nil || !store.operation.FinishedAt.Equal(terminal) {
		t.Fatalf("finished_at = %v, want terminal time %v", store.operation.FinishedAt, terminal)
	}
	if len(store.auditEvents) != 1 || !store.auditEvents[0].Time.Equal(terminal) {
		t.Fatalf("audit time = %#v, want terminal time %v", store.auditEvents, terminal)
	}
	if !store.templateCreateSuccessCommitAt.Equal(terminal) {
		t.Fatalf("success commit time = %v, want terminal time %v", store.templateCreateSuccessCommitAt, terminal)
	}
	if store.repo.UpdatedAt.Equal(started) || store.operation.FinishedAt.Equal(started) || store.auditEvents[0].Time.Equal(started) || store.templateCreateSuccessCommitAt.Equal(started) {
		t.Fatalf("terminal timestamps reused started time %v: repo=%#v operation=%#v audit=%#v commit=%v", started, store.repo, store.operation, store.auditEvents, store.templateCreateSuccessCommitAt)
	}
}

func TestTemplateCreateExecutorPreparesDirectCloneParentWithoutOccupyingTargetRoots(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.volumeRoots = map[string]string{"vol_123": t.TempDir()}
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		saveSummary:        jvsrunner.SaveSummary{SavePointID: "1778487604491-0f57855a", NewestSavePointID: "1778487604491-0f57855a", Workspace: "main", CreatedAt: "2026-05-05T12:00:00Z"},
		directCloneSummary: jvsrunner.DirectCloneSummary{SourceRepoID: "jvs_repo_alpha", TargetRepoID: "jvs_template_alpha", SavePointsMode: "main", SavePointsCopiedCount: 1, RuntimeStateCopied: false, Workspace: "main"},
		doctorSummary:      jvsrunner.DoctorSummary{RepoID: "jvs_template_alpha", Healthy: true, Workspace: "main"},
		beforeDirectClone: func(_ string, targetPayloadRoot string, targetControlRoot string) {
			parent := filepath.Dir(targetPayloadRoot)
			if filepath.Dir(targetControlRoot) != parent {
				t.Fatalf("target parents differ: payload=%q control=%q", parent, filepath.Dir(targetControlRoot))
			}
			if info, err := os.Stat(parent); err != nil || !info.IsDir() {
				t.Fatalf("target parent not prepared: info=%#v err=%v", info, err)
			}
			for _, targetRoot := range []string{targetPayloadRoot, targetControlRoot} {
				if _, err := os.Lstat(targetRoot); !os.IsNotExist(err) {
					t.Fatalf("target root %q existed before JVS direct clone: %v", targetRoot, err)
				}
			}
		},
	}
	executor, err := NewTemplateCreateExecutor(TemplateConfig{Store: store, JVSRunner: runner, Owner: "worker-a", Clock: func() time.Time { return now }, AuditEventID: func() string { return "audit_template_create" }, VolumeRoots: store.volumeRoots})
	if err != nil {
		t.Fatalf("NewTemplateCreateExecutor: %v", err)
	}

	if err := executor.ExecuteOperationRecovery(context.Background(), templateCreateLeasedRecord(now), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "direct_save,direct_clone" {
		t.Fatalf("jvs calls = %#v, want direct_save,direct_clone", runner.calls)
	}
}

func TestTemplateCreateExecutorPreJVSWriterSessionDenialReleasesFence(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name string
		edit func(*fakeRepoCreateStore)
	}{
		{
			name: "active rw export",
			edit: func(store *fakeRepoCreateStore) {
				store.exports = []sessionstate.ExportSession{freshExportSession(now, "export_alpha", sessionstate.AccessModeReadWrite, sessionstate.ExportStatusActive, now.Add(time.Hour))}
			},
		},
		{
			name: "stale rw export",
			edit: func(store *fakeRepoCreateStore) {
				session := freshExportSession(now, "export_alpha", sessionstate.AccessModeReadWrite, sessionstate.ExportStatusActive, now.Add(time.Hour))
				staleHeartbeat := now.Add(-time.Minute)
				session.GatewayHeartbeatExpiresAt = &staleHeartbeat
				store.exports = []sessionstate.ExportSession{session}
			},
		},
		{
			name: "active rw workload mount",
			edit: func(store *fakeRepoCreateStore) {
				store.mounts = []sessionstate.WorkloadMountBinding{{ID: "wmb_alpha", NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", ReadOnly: false, Status: sessionstate.MountStatusActive, LeaseExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Hour), UpdatedAt: now}}
			},
		},
		{
			name: "stale rw workload mount",
			edit: func(store *fakeRepoCreateStore) {
				store.mounts = []sessionstate.WorkloadMountBinding{{ID: "wmb_alpha", NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", ReadOnly: false, Status: sessionstate.MountStatusActive, LeaseExpiresAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour), UpdatedAt: now}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			store.repo = activeRepoResource(now)
			tt.edit(store)
			runner := &fakeJVSRunner{}
			executor, err := NewTemplateCreateExecutor(TemplateConfig{Store: store, JVSRunner: runner, Owner: "worker-a", Clock: func() time.Time { return now }, AuditEventID: func() string { return "audit_template_create" }, VolumeRoots: store.volumeRoots})
			if err != nil {
				t.Fatalf("NewTemplateCreateExecutor: %v", err)
			}

			err = executor.ExecuteOperationRecovery(context.Background(), templateCreateLeasedRecord(now), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
			if err != nil {
				t.Fatalf("ExecuteOperationRecovery: %v", err)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("jvs calls = %#v, want none", runner.calls)
			}
			if store.operation.State != operations.OperationStateFailed || store.operation.Error == nil || store.operation.Error.Code != "SOURCE_DIRTY_AFTER_TEMPLATE_SAVE" {
				t.Fatalf("operation = %#v, want fail-closed dirty source", store.operation)
			}
			if store.releasedFenceID != "fence_op_template_create" || activeWriterFenceCount(store.fences, "op_template_create") != 0 {
				t.Fatalf("released/active writer fence = %q/%#v, want released pre-JVS writer fence", store.releasedFenceID, store.fences)
			}
		})
	}
}

func TestTemplateCreateExecutorJVSRecoveryBlockingReleasesWriterFence(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.volumeRoots = map[string]string{"vol_123": t.TempDir()}
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		saveErr: &jvsrunner.CommandError{Command: "save", ExitCode: 1, Code: "E_RECOVERY_BLOCKING"},
	}
	executor, err := NewTemplateCreateExecutor(TemplateConfig{Store: store, JVSRunner: runner, Owner: "worker-a", Clock: func() time.Time { return now }, AuditEventID: func() string { return "audit_template_create" }, VolumeRoots: store.volumeRoots})
	if err != nil {
		t.Fatalf("NewTemplateCreateExecutor: %v", err)
	}

	if err := executor.ExecuteOperationRecovery(context.Background(), templateCreateLeasedRecord(now), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}

	if store.templateCreateWriterFenceMarks != 1 || strings.Join(runner.calls, ",") != "direct_save" {
		t.Fatalf("writer fence/JVS calls = %d/%#v, want fence then direct save only", store.templateCreateWriterFenceMarks, runner.calls)
	}
	if store.operation.State != operations.OperationStateFailed || store.operation.Phase != operations.OperationPhaseTemplateCreateWriterFenced || store.operation.Error == nil || store.operation.Error.Code != "TEMPLATE_CREATE_RESTORE_BLOCKED" || !store.operation.Error.Retryable {
		t.Fatalf("operation = %#v, want retryable restore-blocked failed operation", store.operation)
	}
	verification := asStringAnyMap(store.operation.VerificationResult)
	if verification["jvs_recovery_blocking"] != true {
		t.Fatalf("verification = %#v, want controlled JVS recovery-blocking evidence", verification)
	}
	if _, ok := verification["jvs_error_code"]; ok {
		t.Fatalf("verification leaked raw JVS error details: %#v", verification)
	}
	if store.operation.Error.Details["jvs_error_code"] != nil || store.operation.Error.Details["jvs_command"] != nil || store.operation.Error.Details["jvs_exit_code"] != nil {
		t.Fatalf("error details leaked raw JVS details: %#v", store.operation.Error.Details)
	}
	if store.releasedFenceID != "fence_op_template_create" || activeWriterFenceCount(store.fences, "op_template_create") != 0 {
		t.Fatalf("released/active writer fence = %q/%#v, want released writer fence after controlled blocked failure", store.releasedFenceID, store.fences)
	}
}

func TestTemplateCreateExecutorJVSFailureAfterSaveRetainsWriterFence(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.volumeRoots = map[string]string{"vol_123": t.TempDir()}
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		saveSummary:  jvsrunner.SaveSummary{SavePointID: "sp_template01", NewestSavePointID: "sp_template01", Workspace: "main", CreatedAt: "2026-05-05T12:00:00Z"},
		repoCloneErr: errors.New("clone failed"),
	}
	executor, err := NewTemplateCreateExecutor(TemplateConfig{Store: store, JVSRunner: runner, Owner: "worker-a", Clock: func() time.Time { return now }, AuditEventID: func() string { return "audit_template_create" }, VolumeRoots: store.volumeRoots})
	if err != nil {
		t.Fatalf("NewTemplateCreateExecutor: %v", err)
	}

	err = executor.ExecuteOperationRecovery(context.Background(), templateCreateLeasedRecord(now), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if strings.Join(runner.calls, ",") != "direct_save,direct_clone" {
		t.Fatalf("jvs calls = %#v, want direct_save,direct_clone", runner.calls)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.releasedFenceID != "" || activeWriterFenceCount(store.fences, "op_template_create") != 1 {
		t.Fatalf("operation/release/fences = %#v/%q/%#v, want retained writer fence after uncertain JVS side effect", store.operation, store.releasedFenceID, store.fences)
	}
}

func TestTemplateCloneExecutorClonesTemplateToRepoWithoutSave(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.volumeRoots = map[string]string{"vol_123": t.TempDir()}
	store.repo = templateResource(now)
	runner := &fakeJVSRunner{
		directCloneSummary: jvsrunner.DirectCloneSummary{SourceRepoID: "jvs_template_alpha", TargetRepoID: "jvs_repo_clone", SavePointsMode: "main", SavePointsCopiedCount: 1, RuntimeStateCopied: false, Workspace: "main"},
		doctorSummary:      jvsrunner.DoctorSummary{RepoID: "jvs_repo_clone", Healthy: true, Workspace: "main"},
	}
	executor, err := NewTemplateCloneExecutor(TemplateConfig{Store: store, JVSRunner: runner, Owner: "worker-a", Clock: func() time.Time { return now }, AuditEventID: func() string { return "audit_template_clone" }, VolumeRoots: store.volumeRoots})
	if err != nil {
		t.Fatalf("NewTemplateCloneExecutor: %v", err)
	}

	if err := executor.ExecuteOperationRecovery(context.Background(), templateCloneLeasedRecord(now), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}

	if got := strings.Join(runner.calls, ","); got != "direct_clone" {
		t.Fatalf("jvs calls = %s, want direct_clone", got)
	}
	if store.repo.ID != "repo_clone01" || store.repo.Kind != resources.RepoKindRepo || store.operation.Phase != operations.OperationPhaseTemplateCloneCommitted {
		t.Fatalf("committed repo/operation = %#v %#v", store.repo, store.operation)
	}
	assertCloneEvidenceProjection(t, store.operation.JVSJSONOutput, "clone")
	assertCloneEvidenceProjection(t, store.operation.VerificationResult, "clone")
	assertCloneEvidenceProjection(t, store.auditEvents[0].Details, "clone")
}

func TestTemplateCloneExecutorUsesFreshTerminalTimestampAfterSlowJVSClone(t *testing.T) {
	started := repoExecNow()
	terminal := started.Add(95 * time.Second)
	clockNow := started
	store := newFakeStore()
	store.volumeRoots = map[string]string{"vol_123": t.TempDir()}
	store.repo = templateResource(started)
	runner := &fakeJVSRunner{
		directCloneSummary: jvsrunner.DirectCloneSummary{SourceRepoID: "jvs_template_alpha", TargetRepoID: "jvs_repo_clone", SavePointsMode: "main", SavePointsCopiedCount: 1, RuntimeStateCopied: false, Workspace: "main"},
		doctorSummary:      jvsrunner.DoctorSummary{RepoID: "jvs_repo_clone", Healthy: true, Workspace: "main"},
		afterDirectClone: func() {
			clockNow = terminal
		},
	}
	executor, err := NewTemplateCloneExecutor(TemplateConfig{Store: store, JVSRunner: runner, Owner: "worker-a", Clock: func() time.Time { return clockNow }, AuditEventID: func() string { return "audit_template_clone" }, VolumeRoots: store.volumeRoots})
	if err != nil {
		t.Fatalf("NewTemplateCloneExecutor: %v", err)
	}

	if err := executor.ExecuteOperationRecovery(context.Background(), templateCloneLeasedRecord(started), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}

	if store.operation.FinishedAt == nil || !store.operation.FinishedAt.Equal(terminal) {
		t.Fatalf("finished_at = %v, want terminal time %v", store.operation.FinishedAt, terminal)
	}
	if len(store.auditEvents) != 1 || !store.auditEvents[0].Time.Equal(terminal) {
		t.Fatalf("audit time = %#v, want terminal time %v", store.auditEvents, terminal)
	}
	if !store.templateCloneSuccessCommitAt.Equal(terminal) {
		t.Fatalf("success commit time = %v, want terminal time %v", store.templateCloneSuccessCommitAt, terminal)
	}
	if store.repo.UpdatedAt.Equal(started) || store.operation.FinishedAt.Equal(started) || store.auditEvents[0].Time.Equal(started) || store.templateCloneSuccessCommitAt.Equal(started) {
		t.Fatalf("terminal timestamps reused started time %v: repo=%#v operation=%#v audit=%#v commit=%v", started, store.repo, store.operation, store.auditEvents, store.templateCloneSuccessCommitAt)
	}
}

func TestTemplateCloneExecutorSuccessCommitFailurePreservesCause(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.volumeRoots = map[string]string{"vol_123": t.TempDir()}
	store.repo = templateResource(now)
	commitErr := errors.New("postgres template clone commit detail")
	store.templateCloneSuccessErr = commitErr
	runner := &fakeJVSRunner{
		directCloneSummary: jvsrunner.DirectCloneSummary{SourceRepoID: "jvs_template_alpha", TargetRepoID: "jvs_repo_clone", SavePointsMode: "main", SavePointsCopiedCount: 1, RuntimeStateCopied: false, Workspace: "main"},
		doctorSummary:      jvsrunner.DoctorSummary{RepoID: "jvs_repo_clone", Healthy: true, Workspace: "main"},
	}
	executor, err := NewTemplateCloneExecutor(TemplateConfig{Store: store, JVSRunner: runner, Owner: "worker-a", Clock: func() time.Time { return now }, AuditEventID: func() string { return "audit_template_clone" }, VolumeRoots: store.volumeRoots})
	if err != nil {
		t.Fatalf("NewTemplateCloneExecutor: %v", err)
	}

	err = executor.ExecuteOperationRecovery(context.Background(), templateCloneLeasedRecord(now), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, commitErr) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want wrapped commit cause", err)
	}
	if err == nil || !strings.Contains(err.Error(), "template clone success commit failed") {
		t.Fatalf("ExecuteOperationRecovery error = %v, want commit failure context", err)
	}
	if strings.Join(runner.calls, ",") != "direct_clone" {
		t.Fatalf("jvs calls = %#v, want direct_clone before durable commit failure", runner.calls)
	}
}

func repoCreateLeasedRecord(now time.Time, attempt int) operations.OperationRecord {
	leaseExpiresAt := now.Add(time.Minute)
	startedAt := now.Add(-time.Minute)
	return operations.OperationRecord{
		ID:                  "op_repo",
		Type:                operations.OperationRepoCreate,
		State:               operations.OperationStateRunning,
		Phase:               operations.OperationPhaseRepoCreateValidate,
		Attempt:             attempt,
		LeaseOwner:          "worker-a",
		LeaseExpiresAt:      &leaseExpiresAt,
		IdempotencyScope:    operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationRepoCreate, "idem_repo").String(),
		IdempotencyKey:      "idem_repo",
		RequestHash:         "sha256:repo",
		CorrelationID:       "corr-alpha",
		CallerService:       "product-caller",
		AuthorizedActor:     operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:            operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:         "ns_alpha01",
		RepoID:              "repo_alpha01",
		ExternalResourceIDs: map[string]string{},
		InputSummary:        map[string]any{"namespace_id": "ns_alpha01", "target_repo_id": "repo_alpha01"},
		CreatedAt:           now.Add(-time.Hour),
		StartedAt:           &startedAt,
	}
}

func savePointLeasedRecord(now time.Time, phase string) operations.OperationRecord {
	record := repoCreateLeasedRecord(now, 1)
	record.ID = "op_savepoint"
	record.Type = operations.OperationSavePointCreate
	record.Phase = phase
	record.IdempotencyScope = operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationSavePointCreate, "idem_savepoint").String()
	record.IdempotencyKey = "idem_savepoint"
	record.RequestHash = "sha256:savepoint"
	record.InputSummary = map[string]any{"message": "checkpoint"}
	return record
}

func templateCreateLeasedRecord(now time.Time) operations.OperationRecord {
	record := repoCreateLeasedRecord(now, 1)
	record.ID = "op_template_create"
	record.Type = operations.OperationTemplateCreate
	record.Phase = operations.OperationPhaseTemplateCreateValidate
	record.IdempotencyScope = operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationTemplateCreate, "idem_template").String()
	record.IdempotencyKey = "idem_template"
	record.RequestHash = "sha256:template-create"
	record.Resource = operations.ResourceRef{Type: "repo_template", ID: "tmpl_base01"}
	record.RepoID = "repo_alpha01"
	record.TemplateID = "tmpl_base01"
	record.InputSummary = map[string]any{"source_repo_id": "repo_alpha01", "target_template_id": "tmpl_base01", "clone_history_mode": "main"}
	return record
}

func templateCloneLeasedRecord(now time.Time) operations.OperationRecord {
	record := repoCreateLeasedRecord(now, 1)
	record.ID = "op_template_clone"
	record.Type = operations.OperationTemplateClone
	record.Phase = operations.OperationPhaseTemplateCloneValidate
	record.IdempotencyScope = operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationTemplateClone, "idem_template_clone").String()
	record.IdempotencyKey = "idem_template_clone"
	record.RequestHash = "sha256:template-clone"
	record.Resource = operations.ResourceRef{Type: "repo", ID: "repo_clone01"}
	record.RepoID = "repo_clone01"
	record.TemplateID = "tmpl_base01"
	record.InputSummary = map[string]any{"template_id": "tmpl_base01", "target_repo_id": "repo_clone01", "clone_history_mode": "main"}
	return record
}

func activeRepoResource(now time.Time) resources.Repo {
	return resources.Repo{
		ID:                  "repo_alpha01",
		NamespaceID:         "ns_alpha01",
		VolumeID:            "vol_123",
		JVSRepoID:           "jvs_repo_alpha",
		Kind:                resources.RepoKindRepo,
		Status:              resources.RepoStatusActive,
		ControlVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/control",
		PayloadVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/payload",
		Lifecycle:           resources.RepoLifecycle{Status: resources.RepoStatusActive, LastLifecycleOperationID: "op_repo"},
		CreatedAt:           now.Add(-time.Hour),
		UpdatedAt:           now,
	}
}

func templateResource(now time.Time) resources.Repo {
	return resources.Repo{
		ID:                  "tmpl_base01",
		NamespaceID:         "ns_alpha01",
		VolumeID:            "vol_123",
		JVSRepoID:           "jvs_template_alpha",
		Kind:                resources.RepoKindTemplate,
		Status:              resources.RepoStatusActive,
		ControlVolumeSubdir: "afscp/namespaces/ns_alpha01/templates/tmpl_base01/control",
		PayloadVolumeSubdir: "afscp/namespaces/ns_alpha01/templates/tmpl_base01/payload",
		Lifecycle:           resources.RepoLifecycle{Status: resources.RepoStatusActive},
		CreatedAt:           now.Add(-time.Hour),
		UpdatedAt:           now,
	}
}

func repoExecNow() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}

func repoExecTimePtr(t time.Time) *time.Time {
	return &t
}

func savePointRepoExecMountFixture(now time.Time, readOnly bool, status sessionstate.MountStatus, confirmedUnmountedAt, unableToWriteAt *time.Time) sessionstate.WorkloadMountBinding {
	return sessionstate.WorkloadMountBinding{
		ID:                   "wmb_savepoint",
		NamespaceID:          "ns_alpha01",
		RepoID:               "repo_alpha01",
		ReadOnly:             readOnly,
		Status:               status,
		LeaseExpiresAt:       now.Add(time.Hour),
		ConfirmedUnmountedAt: confirmedUnmountedAt,
		UnableToWriteAt:      unableToWriteAt,
		CreatedAt:            now.Add(-time.Minute),
		UpdatedAt:            now,
	}
}

func newFakeStore() *fakeRepoCreateStore {
	now := repoExecNow()
	return &fakeRepoCreateStore{
		namespace: resources.Namespace{ID: "ns_alpha01", Status: resources.NamespaceStatusActive, CreatedAt: now.Add(-24 * time.Hour), UpdatedAt: now},
		binding: resources.NamespaceVolumeBinding{
			NamespaceID:       "ns_alpha01",
			DefaultVolumeID:   "vol_123",
			AllowedCallers:    []resources.AllowedCaller{{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin}}},
			QuotaBytesDefault: 4096,
			ExportPolicy:      map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600)},
			LifecyclePolicy:   map[string]any{"tombstone_retention_seconds": float64(604800), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
			MountPolicy:       map[string]any{"workload_mount_enabled": true, "workload_mount_requires_external_control_root": true, "allow_privileged_workload": false},
			TemplatePolicy:    map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
			Status:            resources.NamespaceStatusActive,
			CreatedAt:         now.Add(-24 * time.Hour),
			UpdatedAt:         now,
		},
		volume: resources.Volume{
			ID:             "vol_123",
			Backend:        resources.VolumeBackendJuiceFS,
			IsolationClass: resources.VolumeIsolationShared,
			Status:         resources.VolumeStatusActive,
			Capabilities:   map[string]any{"webdav_export": true, "workload_mount": true, "jvs_external_control_root": true, "directory_quota": false},
			CreatedAt:      now.Add(-24 * time.Hour),
			UpdatedAt:      now,
		},
		volumeRoots: map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
	}
}

type fakeRepoCreateStore struct {
	namespace                      resources.Namespace
	binding                        resources.NamespaceVolumeBinding
	volume                         resources.Volume
	volumeRoots                    map[string]string
	fences                         []fences.Fence
	repo                           resources.Repo
	operation                      operations.OperationRecord
	auditEvents                    []audit.Event
	exports                        []sessionstate.ExportSession
	mounts                         []sessionstate.WorkloadMountBinding
	createFenceCalls               int
	restoreWriterFenceMarks        int
	templateCreateWriterFenceMarks int
	releasedFenceID                string
	successErr                     error
	lifecycleSuccessErr            error
	templateCloneSuccessErr        error
	failOnCanceledCommitContext    bool
	blockingLifecycle              []operations.OperationRecord
	savePointSuccessCommitAt       time.Time
	templateCreateSuccessCommitAt  time.Time
	templateCloneSuccessCommitAt   time.Time
	beforeListSessions             func()
	listExportSessionCalls         int
	listMountSessionCalls          int
}

func (store *fakeRepoCreateStore) GetNamespace(context.Context, string) (resources.Namespace, error) {
	return store.namespace, nil
}
func (store *fakeRepoCreateStore) GetNamespaceVolumeBinding(context.Context, string) (resources.NamespaceVolumeBinding, error) {
	return store.binding, nil
}
func (store *fakeRepoCreateStore) GetVolume(context.Context, string) (resources.Volume, error) {
	return store.volume, nil
}
func (store *fakeRepoCreateStore) GetRepoInNamespace(context.Context, string, string) (resources.Repo, error) {
	return store.repo, nil
}
func (store *fakeRepoCreateStore) ListHeldRepoFences(context.Context, string) ([]fences.Fence, error) {
	return append([]fences.Fence(nil), store.fences...), nil
}
func (store *fakeRepoCreateStore) CreateRepoFence(_ context.Context, fence fences.Fence) error {
	store.createFenceCalls++
	store.fences = append(store.fences, fence)
	return nil
}
func (store *fakeRepoCreateStore) ReleaseRepoFence(context.Context, string, string) error { return nil }
func (store *fakeRepoCreateStore) CommitRepoCreateSucceededWithLease(ctx context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
	if store.failOnCanceledCommitContext {
		if err := ctx.Err(); err != nil {
			return resources.Repo{}, operations.OperationRecord{}, err
		}
	}
	if store.successErr != nil {
		return resources.Repo{}, operations.OperationRecord{}, store.successErr
	}
	store.repo = repo
	store.operation = record.Record()
	store.releasedFenceID = fenceID
	store.auditEvents = append(store.auditEvents, event)
	return repo, store.operation, nil
}
func (store *fakeRepoCreateStore) CommitRepoCreateFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error) {
	if store.failOnCanceledCommitContext {
		if err := ctx.Err(); err != nil {
			return operations.OperationRecord{}, err
		}
	}
	store.operation = record.Record()
	store.releasedFenceID = releaseFenceID
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}
func (store *fakeRepoCreateStore) ListExportSessionsByRepo(context.Context, string) ([]sessionstate.ExportSession, error) {
	store.listExportSessionCalls++
	if store.beforeListSessions != nil {
		store.beforeListSessions()
	}
	return append([]sessionstate.ExportSession(nil), store.exports...), nil
}
func (store *fakeRepoCreateStore) ListWorkloadMountBindingsByRepo(context.Context, string) ([]sessionstate.WorkloadMountBinding, error) {
	store.listMountSessionCalls++
	if store.beforeListSessions != nil {
		store.beforeListSessions()
	}
	return append([]sessionstate.WorkloadMountBinding(nil), store.mounts...), nil
}
func (store *fakeRepoCreateStore) ListEarlierNonTerminalRepoLifecycleOperations(context.Context, string, string, time.Time) ([]operations.OperationRecord, error) {
	return append([]operations.OperationRecord(nil), store.blockingLifecycle...), nil
}
func (store *fakeRepoCreateStore) CommitRepoLifecycleSucceededWithLease(_ context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
	if store.lifecycleSuccessErr != nil {
		return resources.Repo{}, operations.OperationRecord{}, store.lifecycleSuccessErr
	}
	store.repo = repo
	store.operation = record.Record()
	store.releasedFenceID = fenceID
	store.auditEvents = append(store.auditEvents, event)
	return repo, store.operation, nil
}
func (store *fakeRepoCreateStore) CommitRepoLifecycleFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error) {
	store.operation = record.Record()
	store.releasedFenceID = releaseFenceID
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}
func (store *fakeRepoCreateStore) CommitRepoPurgeSucceededWithLease(_ context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
	store.repo = repo
	store.operation = record.Record()
	store.releasedFenceID = fenceID
	store.auditEvents = append(store.auditEvents, event)
	return repo, store.operation, nil
}
func (store *fakeRepoCreateStore) CommitRepoPurgeFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error) {
	store.operation = record.Record()
	store.releasedFenceID = releaseFenceID
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}

func (store *fakeRepoCreateStore) CommitSavePointCreateSucceededWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	store.operation = record.Record()
	store.savePointSuccessCommitAt = now
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}

func (store *fakeRepoCreateStore) CommitSavePointCreateFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (operations.OperationRecord, error) {
	store.operation = record.Record()
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}

func (store *fakeRepoCreateStore) MarkSavePointCreateWriterDrainPendingWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, now time.Time) (operations.OperationRecord, error) {
	store.operation = record.Record()
	store.operation.LeaseOwner = "worker-a"
	store.operation.LeaseExpiresAt = &now
	return store.operation, nil
}

func (store *fakeRepoCreateStore) CommitTemplateCreateSucceededWithLease(_ context.Context, template resources.Repo, _ string, _ string, _ string, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (resources.Repo, operations.OperationRecord, error) {
	store.repo = template
	store.operation = record.Record()
	store.templateCreateSuccessCommitAt = now
	store.releaseWriterFence(store.operation.SessionFenceID, now)
	store.auditEvents = append(store.auditEvents, event)
	return template, store.operation, nil
}

func (store *fakeRepoCreateStore) MarkTemplateCreateWriterFencedWithLease(_ context.Context, fence fences.Fence, record operations.SanitizedOperationRecord, _ string, _ time.Time) (fences.Fence, operations.OperationRecord, error) {
	store.templateCreateWriterFenceMarks++
	store.operation = record.Record()
	for _, existing := range store.fences {
		if existing.ID == fence.ID && existing.Kind == fences.KindWriterSession && existing.HolderOperationID == store.operation.ID && existing.Status == fences.StatusActive && existing.ReleasedAt == nil && existing.RecoveredAt == nil {
			return existing, store.operation, nil
		}
	}
	store.fences = append(store.fences, fence)
	return fence, store.operation, nil
}

func (store *fakeRepoCreateStore) CommitTemplateCreateFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	store.operation = record.Record()
	if store.operation.Phase == operations.OperationPhaseTemplateCreateWriterFenced && store.operation.State == operations.OperationStateFailed {
		store.releaseWriterFence(store.operation.SessionFenceID, now)
	}
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}

func (store *fakeRepoCreateStore) CommitTemplateCloneSucceededWithLease(_ context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (resources.Repo, operations.OperationRecord, error) {
	store.repo = repo
	store.operation = record.Record()
	store.templateCloneSuccessCommitAt = now
	store.auditEvents = append(store.auditEvents, event)
	if store.templateCloneSuccessErr != nil {
		return resources.Repo{}, operations.OperationRecord{}, store.templateCloneSuccessErr
	}
	return repo, store.operation, nil
}

func (store *fakeRepoCreateStore) CommitTemplateCloneFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (operations.OperationRecord, error) {
	store.operation = record.Record()
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}

func (store *fakeRepoCreateStore) MarkRestoreWriterFencedWithLease(_ context.Context, fence fences.Fence, record operations.SanitizedOperationRecord, _ string, _ time.Time) (fences.Fence, operations.OperationRecord, error) {
	store.restoreWriterFenceMarks++
	store.operation = record.Record()
	for _, existing := range store.fences {
		if existing.ID == fence.ID && existing.Kind == fences.KindWriterSession && existing.HolderOperationID == store.operation.ID && existing.Status == fences.StatusActive && existing.ReleasedAt == nil && existing.RecoveredAt == nil {
			return existing, store.operation, nil
		}
	}
	store.fences = append(store.fences, fence)
	return fence, store.operation, nil
}

func (store *fakeRepoCreateStore) CommitRestoreSucceededWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	store.operation = record.Record()
	store.releaseWriterFence(store.operation.SessionFenceID, now)
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}

func (store *fakeRepoCreateStore) CommitRestoreFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	store.operation = record.Record()
	if store.operation.Phase == operations.OperationPhaseRestoreWriterFenced && store.operation.State == operations.OperationStateFailed {
		store.releaseWriterFence(store.operation.SessionFenceID, now)
	}
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}

func activeWriterFenceCount(existing []fences.Fence, operationID string) int {
	count := 0
	for _, fence := range existing {
		if fence.Kind == fences.KindWriterSession && fence.HolderOperationID == operationID && fence.Status == fences.StatusActive && fence.ReleasedAt == nil && fence.RecoveredAt == nil {
			count++
		}
	}
	return count
}

func (store *fakeRepoCreateStore) releaseWriterFence(fenceID string, now time.Time) {
	for idx, fence := range store.fences {
		if fence.ID == fenceID && fence.Kind == fences.KindWriterSession && fence.Status == fences.StatusActive && fence.ReleasedAt == nil && fence.RecoveredAt == nil {
			releasedAt := now
			store.fences[idx].Status = fences.StatusReleased
			store.fences[idx].ReleasedAt = &releasedAt
			store.fences[idx].UpdatedAt = now
			store.releasedFenceID = fenceID
			return
		}
	}
}

type fakeJVSRunner struct {
	calls                []string
	payloadRoot          string
	controlRoot          string
	saveMessage          string
	savePurpose          string
	initSummary          jvsrunner.InitSummary
	doctorSummary        jvsrunner.DoctorSummary
	saveSummary          jvsrunner.SaveSummary
	saveErrs             []error
	historySummary       jvsrunner.HistorySummary
	directTarget         jvsrunner.DirectTarget
	directSaveSummary    jvsrunner.DirectSaveSummary
	directListSummary    jvsrunner.DirectListSummary
	directRestoreSummary jvsrunner.DirectRestoreSummary
	directStatusSummary  jvsrunner.DirectStatusSummary
	directDoctorSummary  jvsrunner.DirectDoctorSummary
	directCloneSummary   jvsrunner.DirectCloneSummary
	beforeDirectClone    func(sourceControlRoot, targetPayloadRoot, targetControlRoot string)
	afterSave            func()
	afterDirectClone     func()
	beforeRestore        func()
	initErr              error
	doctorErr            error
	saveErr              error
	directSaveErr        error
	directListErr        error
	directRestoreErr     error
	directStatusErr      error
	directDoctorErr      error
	historyErr           error
	repoCloneErr         error
	restoreSavePointID   string
	cloneSavePointID     string
	afterDoctor          func(context.Context)
}

func (runner *fakeJVSRunner) Init(_ context.Context, payloadRoot, controlRoot string) (jvsrunner.InitSummary, error) {
	runner.calls = append(runner.calls, "init")
	runner.payloadRoot = payloadRoot
	runner.controlRoot = controlRoot
	return runner.initSummary, runner.initErr
}
func (runner *fakeJVSRunner) DirectSave(ctx context.Context, target jvsrunner.DirectTarget, message string) (jvsrunner.DirectSaveSummary, error) {
	return runner.DirectSaveWithPurpose(ctx, target, message, "")
}

func (runner *fakeJVSRunner) DirectSaveWithPurpose(_ context.Context, target jvsrunner.DirectTarget, message string, purpose string) (jvsrunner.DirectSaveSummary, error) {
	runner.calls = append(runner.calls, "direct_save")
	runner.directTarget = target
	runner.controlRoot = target.ControlRoot
	runner.payloadRoot = target.Home
	runner.saveMessage = message
	runner.savePurpose = purpose
	if runner.afterSave != nil {
		defer runner.afterSave()
	}
	if runner.directSaveSummary.SavePointID != "" && len(runner.directSaveSummary.CloneEvidence) == 0 {
		runner.directSaveSummary.CloneEvidence = fakeCloneEvidence("save", "save_point_payload", 42)
	}
	if len(runner.saveErrs) > 0 {
		err := runner.saveErrs[0]
		runner.saveErrs = runner.saveErrs[1:]
		return runner.directSaveSummary, err
	}
	if runner.directSaveErr != nil {
		return runner.directSaveSummary, runner.directSaveErr
	}
	if runner.saveErr != nil {
		return runner.directSaveSummary, runner.saveErr
	}
	if runner.directSaveSummary.SavePointID == "" && runner.saveSummary.SavePointID != "" {
		runner.directSaveSummary = jvsrunner.DirectSaveSummary{SavePointID: runner.saveSummary.SavePointID, HistoryHeadID: runner.saveSummary.NewestSavePointID, Message: message, Purpose: purpose, CreatedAt: runner.saveSummary.CreatedAt}
	}
	if runner.directSaveSummary.SavePointID != "" {
		if runner.directSaveSummary.HistoryHeadID == "" {
			runner.directSaveSummary.HistoryHeadID = runner.directSaveSummary.SavePointID
		}
		if runner.directSaveSummary.Message == "" {
			runner.directSaveSummary.Message = message
		}
	}
	if runner.directSaveSummary.SavePointID != "" && len(runner.directSaveSummary.CloneEvidence) == 0 {
		runner.directSaveSummary.CloneEvidence = fakeCloneEvidence("save", "save_point_payload", 42)
	}
	return runner.directSaveSummary, nil
}

func (runner *fakeJVSRunner) DirectList(_ context.Context, target jvsrunner.DirectTarget) (jvsrunner.DirectListSummary, error) {
	runner.calls = append(runner.calls, "direct_list")
	runner.directTarget = target
	runner.controlRoot = target.ControlRoot
	runner.payloadRoot = target.Home
	if runner.directListSummary.HistoryHeadID == "" && len(runner.directListSummary.SavePoints) == 0 && runner.directSaveSummary.SavePointID != "" {
		runner.directListSummary = jvsrunner.DirectListSummary{
			HistoryHeadID: runner.directSaveSummary.SavePointID,
			SavePoints: []jvsrunner.DirectSavePointSummary{{
				SavePointID: runner.directSaveSummary.SavePointID,
				Message:     runner.directSaveSummary.Message,
				Purpose:     runner.directSaveSummary.Purpose,
				CreatedAt:   runner.directSaveSummary.CreatedAt,
				HistoryHead: true,
			}},
		}
	}
	if runner.directListSummary.HistoryHeadID == "" && len(runner.directListSummary.SavePoints) == 0 {
		savePoints := make([]jvsrunner.DirectSavePointSummary, 0, len(runner.historySummary.SavePoints))
		for _, savePoint := range runner.historySummary.SavePoints {
			savePoints = append(savePoints, jvsrunner.DirectSavePointSummary{SavePointID: savePoint.SavePointID, Message: savePoint.Message, Purpose: savePoint.Purpose, CreatedAt: savePoint.CreatedAt, HistoryHead: savePoint.SavePointID == runner.historySummary.NewestSavePointID})
		}
		runner.directListSummary = jvsrunner.DirectListSummary{HistoryHeadID: runner.historySummary.NewestSavePointID, SavePoints: savePoints}
	}
	if runner.directListErr != nil {
		return jvsrunner.DirectListSummary{}, runner.directListErr
	}
	return runner.directListSummary, runner.historyErr
}

func (runner *fakeJVSRunner) DirectRestore(_ context.Context, target jvsrunner.DirectTarget, savePointID string) (jvsrunner.DirectRestoreSummary, error) {
	runner.calls = append(runner.calls, "direct_restore")
	runner.directTarget = target
	runner.controlRoot = target.ControlRoot
	runner.payloadRoot = target.Home
	runner.restoreSavePointID = savePointID
	if runner.beforeRestore != nil {
		runner.beforeRestore()
	}
	if runner.directRestoreSummary.RestoredSavePointID != "" && len(runner.directRestoreSummary.CloneEvidence) == 0 {
		runner.directRestoreSummary.CloneEvidence = fakeCloneEvidence("restore", "restore_staging", 17)
	}
	if runner.directRestoreErr != nil {
		return runner.directRestoreSummary, runner.directRestoreErr
	}
	return runner.directRestoreSummary, nil
}

func (runner *fakeJVSRunner) DirectStatus(_ context.Context, target jvsrunner.DirectTarget) (jvsrunner.DirectStatusSummary, error) {
	runner.calls = append(runner.calls, "direct_status")
	runner.directTarget = target
	runner.controlRoot = target.ControlRoot
	runner.payloadRoot = target.Home
	if runner.directStatusSummary.HistoryHeadID == "" {
		runner.directStatusSummary = jvsrunner.DirectStatusSummary{HistoryHeadID: runner.restoreSavePointID, MetadataState: "ready", ActiveOperation: "none", Recovery: "none"}
	}
	return runner.directStatusSummary, runner.directStatusErr
}

func (runner *fakeJVSRunner) DirectDoctor(ctx context.Context, target jvsrunner.DirectTarget) (jvsrunner.DirectDoctorSummary, error) {
	runner.calls = append(runner.calls, "direct_doctor")
	runner.directTarget = target
	runner.controlRoot = target.ControlRoot
	runner.payloadRoot = target.Home
	if runner.afterDoctor != nil {
		runner.afterDoctor(ctx)
	}
	if runner.directDoctorSummary.RepoID == "" && runner.directDoctorSummary.MetadataState == "" {
		repoID := runner.doctorSummary.RepoID
		if repoID == "" {
			repoID = "jvs_repo_alpha"
		}
		healthy := runner.doctorSummary.Healthy
		if runner.doctorSummary == (jvsrunner.DoctorSummary{}) {
			healthy = true
		}
		runner.directDoctorSummary = jvsrunner.DirectDoctorSummary{RepoID: repoID, Healthy: healthy, FindingCount: 0, MetadataState: "ready", Journal: "clean", Recovery: "none"}
	}
	if runner.directDoctorErr != nil {
		return runner.directDoctorSummary, runner.directDoctorErr
	}
	return runner.directDoctorSummary, runner.doctorErr
}

func (runner *fakeJVSRunner) DirectClone(_ context.Context, source jvsrunner.DirectTarget, target jvsrunner.DirectTarget, savePointID string) (jvsrunner.DirectCloneSummary, error) {
	runner.calls = append(runner.calls, "direct_clone")
	runner.controlRoot = target.ControlRoot
	runner.payloadRoot = target.Home
	runner.cloneSavePointID = savePointID
	if runner.beforeDirectClone != nil {
		runner.beforeDirectClone(source.ControlRoot, target.Home, target.ControlRoot)
	}
	if runner.directCloneSummary.SourceRepoID == "" {
		runner.directCloneSummary.SourceRepoID = "jvs_repo_alpha"
	}
	if len(runner.directCloneSummary.CloneEvidence) == 0 {
		runner.directCloneSummary.CloneEvidence = fakeCloneEvidence("clone", "clone_target_home", 23)
	}
	if runner.afterDirectClone != nil {
		runner.afterDirectClone()
	}
	summary := runner.directCloneSummary
	summary.SavePointID = savePointID
	return summary, runner.repoCloneErr
}

func fakeCloneEvidence(operation, phase string, durationMs int64) []jvsrunner.CloneEvidence {
	return []jvsrunner.CloneEvidence{{
		Operation:  operation,
		Phase:      phase,
		Engine:     "juicefs_clone",
		Status:     "succeeded",
		StartedAt:  "2026-05-05T12:00:00Z",
		FinishedAt: "2026-05-05T12:00:01Z",
		DurationMs: durationMs,
	}}
}

func assertCloneEvidenceProjection(t *testing.T, container any, operations ...string) {
	t.Helper()
	values, ok := container.(map[string]any)
	if !ok {
		t.Fatalf("clone evidence container = %#v, want map", container)
	}
	raw, ok := values[cloneEvidenceOutputField]
	if !ok {
		t.Fatalf("clone evidence missing from %#v", values)
	}
	items, ok := raw.([]map[string]any)
	if !ok {
		t.Fatalf("clone evidence = %#v, want []map[string]any", raw)
	}
	if len(items) != len(operations) {
		t.Fatalf("clone evidence count = %d, want %d: %#v", len(items), len(operations), items)
	}
	for idx, item := range items {
		if item["operation"] != operations[idx] || item["engine"] != "juicefs_clone" || item["status"] != "succeeded" {
			t.Fatalf("clone evidence[%d] = %#v, want safe %s evidence", idx, item, operations[idx])
		}
		if _, ok := item["duration_ms"].(int64); !ok {
			t.Fatalf("clone evidence[%d] duration = %#v, want int64 duration", idx, item["duration_ms"])
		}
	}
	rendered := strings.ToLower(fmt.Sprint(items))
	for _, leaked := range []string{"/srv/afscp", "secret", "password", "raw_argv", "raw_command", "internal_path"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("clone evidence leaked %q: %#v", leaked, items)
		}
	}
}

func repoCreateFence(now time.Time, fenceID, operationID string) fences.Fence {
	return fences.Fence{ID: fenceID, RepoID: "repo_alpha01", Kind: fences.KindLifecycle, HolderOperationID: operationID, Status: fences.StatusActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
}

func assertNoRepoExecLeak(t *testing.T, operation operations.OperationRecord, events []audit.Event) {
	t.Helper()
	rendered := strings.ToLower(strings.Join([]string{operation.ID, operation.Phase, operation.CorrelationID, operation.CallerService, fmt.Sprint(operation.Error), fmt.Sprint(operation.JVSJSONOutput), fmt.Sprint(operation.VerificationResult)}, " "))
	rendered += strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(strings.Join(func() []string {
		out := []string{}
		for _, event := range events {
			out = append(out, event.Reason)
		}
		return out
	}(), " ")), "\n", " "), "\t", " "))
	for _, leaked := range []string{"/srv/afscp", "secret", "password", "payload_root", "control_root", "payload_volume_subdir", "control_volume_subdir"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("repoexec leaked %q in operation/events: %#v %#v", leaked, operation, events)
		}
	}
}
