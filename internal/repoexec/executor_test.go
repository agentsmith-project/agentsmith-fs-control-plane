package repoexec

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
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
	if strings.Join(runner.calls, ",") != "init,doctor" {
		t.Fatalf("JVS calls = %#v, want init then doctor", runner.calls)
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

func TestSavePointExecutorPersistsPreSaveMarkerThenSavesAndCommits(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		historySummary: jvsrunner.HistorySummary{Workspace: "main", NewestSavePointID: "sp_before", SavePoints: []jvsrunner.SavePointSummary{{SavePointID: "sp_before", Message: "before", CreatedAt: "2026-05-05T11:00:00Z"}}},
		saveSummary:    jvsrunner.SaveSummary{SavePointID: "sp_after", NewestSavePointID: "sp_after", Workspace: "main", CreatedAt: "2026-05-05T12:00:00Z"},
	}
	executor := newTestSavePointExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), savePointLeasedRecord(now, operations.OperationPhaseSavePointCreateValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "history,save" {
		t.Fatalf("JVS calls = %#v, want history then save", runner.calls)
	}
	if store.progressUpdates != 1 {
		t.Fatalf("progress updates = %d, want pre-save marker persisted", store.progressUpdates)
	}
	if store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseSavePointCreateCommitted {
		t.Fatalf("operation = %#v, want succeeded committed", store.operation)
	}
	result := store.operation.VerificationResult.(map[string]any)
	if result["pre_save_newest_save_point_id"] != "sp_before" || result["save_point_id"] != "sp_after" || result["unsaved_changes"] != false {
		t.Fatalf("verification = %#v, want pre marker and save result", result)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
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

func TestSavePointExecutorAdoptsCrashAfterSaveWithoutCallingSaveAgain(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{historySummary: jvsrunner.HistorySummary{Workspace: "main", NewestSavePointID: "sp_after", SavePoints: []jvsrunner.SavePointSummary{
		{SavePointID: "sp_after", Message: "checkpoint", CreatedAt: "2026-05-05T12:00:00Z"},
		{SavePointID: "sp_before", Message: "before", CreatedAt: "2026-05-05T11:00:00Z"},
	}}}
	executor := newTestSavePointExecutor(t, store, runner, now)
	record := savePointLeasedRecord(now, operations.OperationPhaseSavePointCreatePrepared)
	record.VerificationResult = map[string]any{"pre_save_history_captured": true, "pre_save_newest_save_point_id": "sp_before"}

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionReclaim}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "history" {
		t.Fatalf("JVS calls = %#v, want history only adoption", runner.calls)
	}
	verification := store.operation.VerificationResult.(map[string]any)
	if verification["adopted"] != true || verification["unsaved_changes_known"] != false {
		t.Fatalf("verification = %#v, want adopted", store.operation.VerificationResult)
	}
	if _, ok := verification["unsaved_changes"]; ok {
		t.Fatalf("verification = %#v, adopted save must not claim unsaved_changes", verification)
	}
	jvsOutput := store.operation.JVSJSONOutput.(map[string]any)
	if jvsOutput["unsaved_changes_known"] != false {
		t.Fatalf("jvs output = %#v, want unknown unsaved_changes", jvsOutput)
	}
	if _, ok := jvsOutput["unsaved_changes"]; ok {
		t.Fatalf("jvs output = %#v, adopted save must not claim unsaved_changes", jvsOutput)
	}
}

func TestSavePointExecutorAmbiguousHistoryRequiresOperatorIntervention(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{historySummary: jvsrunner.HistorySummary{Workspace: "main", NewestSavePointID: "sp_two", SavePoints: []jvsrunner.SavePointSummary{
		{SavePointID: "sp_two", Message: "two", CreatedAt: "2026-05-05T12:01:00Z"},
		{SavePointID: "sp_one", Message: "one", CreatedAt: "2026-05-05T12:00:00Z"},
		{SavePointID: "sp_before", Message: "before", CreatedAt: "2026-05-05T11:00:00Z"},
	}}}
	executor := newTestSavePointExecutor(t, store, runner, now)
	record := savePointLeasedRecord(now, operations.OperationPhaseSavePointCreatePrepared)
	record.VerificationResult = map[string]any{"pre_save_history_captured": true, "pre_save_newest_save_point_id": "sp_before"}

	err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionReclaim})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired {
		t.Fatalf("operation state = %s, want operator intervention", store.operation.State)
	}
	if strings.Contains(strings.Join(runner.calls, ","), "save") {
		t.Fatalf("JVS calls = %#v, want no save on ambiguity", runner.calls)
	}
}

func TestSavePointExecutorPreparedRetryWithNoNewerSavePointRunsSave(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		historySummary: jvsrunner.HistorySummary{Workspace: "main", NewestSavePointID: "sp_before", SavePoints: []jvsrunner.SavePointSummary{{SavePointID: "sp_before", Message: "before", CreatedAt: "2026-05-05T11:00:00Z"}}},
		saveSummary:    jvsrunner.SaveSummary{SavePointID: "sp_after", NewestSavePointID: "sp_after", Workspace: "main", CreatedAt: "2026-05-05T12:00:00Z", UnsavedChanges: true},
	}
	executor := newTestSavePointExecutor(t, store, runner, now)
	record := savePointLeasedRecord(now, operations.OperationPhaseSavePointCreatePrepared)
	record.InputSummary["message"] = "rotate token docs"
	record.VerificationResult = map[string]any{"pre_save_history_captured": true, "pre_save_newest_save_point_id": "sp_before"}

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionReclaim}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "history,save" {
		t.Fatalf("JVS calls = %#v, want history then save", runner.calls)
	}
	if runner.saveMessage != "rotate token docs" {
		t.Fatalf("jvs save message = %q, want durable natural-language message", runner.saveMessage)
	}
	verification := store.operation.VerificationResult.(map[string]any)
	if verification["save_point_id"] != "sp_after" || verification["unsaved_changes_known"] != true || verification["unsaved_changes"] != true {
		t.Fatalf("verification = %#v, want fresh save with known unsaved_changes", verification)
	}
}

func TestSavePointExecutorAdoptsWhenPreSaveHistoryWasEmpty(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{historySummary: jvsrunner.HistorySummary{Workspace: "main", NewestSavePointID: "sp_after", SavePoints: []jvsrunner.SavePointSummary{
		{SavePointID: "sp_after", Message: "checkpoint", CreatedAt: "2026-05-05T12:00:00Z"},
	}}}
	executor := newTestSavePointExecutor(t, store, runner, now)
	record := savePointLeasedRecord(now, operations.OperationPhaseSavePointCreatePrepared)
	record.VerificationResult = map[string]any{"pre_save_history_captured": true, "pre_save_newest_save_point_id": ""}

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionReclaim}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "history" {
		t.Fatalf("JVS calls = %#v, want history only adoption", runner.calls)
	}
	verification := store.operation.VerificationResult.(map[string]any)
	if verification["save_point_id"] != "sp_after" || verification["adopted"] != true || verification["unsaved_changes_known"] != false {
		t.Fatalf("verification = %#v, want adopted save with unknown unsaved_changes", verification)
	}
}

func TestSavePointExecutorMissingPreSavePointerRequiresOperatorIntervention(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{historySummary: jvsrunner.HistorySummary{Workspace: "main", SavePoints: []jvsrunner.SavePointSummary{}}}
	executor := newTestSavePointExecutor(t, store, runner, now)
	record := savePointLeasedRecord(now, operations.OperationPhaseSavePointCreatePrepared)
	record.VerificationResult = map[string]any{"pre_save_history_captured": true, "pre_save_newest_save_point_id": "sp_missing"}

	err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionReclaim})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if strings.Contains(strings.Join(runner.calls, ","), "save") {
		t.Fatalf("JVS calls = %#v, want no save when marker is missing", runner.calls)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired {
		t.Fatalf("operation state = %s, want operator intervention", store.operation.State)
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
	if strings.Join(runner.calls, ",") != "doctor" {
		t.Fatalf("JVS calls = %#v, want doctor-only adoption", runner.calls)
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
	store.successErr = errors.New("postgres password=secret failed")
	runner := &fakeJVSRunner{initSummary: jvsrunner.InitSummary{RepoID: "jvs_repo_alpha", Workspace: "main"}, doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
	executor := newTestExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if err == nil {
		t.Fatal("ExecuteOperationRecovery succeeded, want commit error")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/srv/afscp") {
		t.Fatalf("error leaked sensitive detail: %v", err)
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
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		saveSummary:      jvsrunner.SaveSummary{SavePointID: "sp_template01", NewestSavePointID: "sp_template01", Workspace: "main", CreatedAt: "2026-05-05T12:00:00Z"},
		repoCloneSummary: jvsrunner.RepoCloneSummary{SourceRepoID: "jvs_repo_alpha", TargetRepoID: "jvs_template_alpha", SavePointsMode: "main", SavePointsCopiedCount: 1, RuntimeStateCopied: false, Workspace: "main"},
		doctorSummary:    jvsrunner.DoctorSummary{RepoID: "jvs_template_alpha", Healthy: true, Workspace: "main"},
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
	if got := strings.Join(runner.calls, ","); got != "save,repo_clone,doctor" {
		t.Fatalf("jvs calls = %s, want save,repo_clone,doctor", got)
	}
	verification := asStringAnyMap(store.operation.VerificationResult)
	if store.repo.ID != "tmpl_base01" || store.repo.Kind != resources.RepoKindTemplate || verification["source_save_point_id"] != "sp_template01" {
		t.Fatalf("committed template/operation = %#v %#v", store.repo, store.operation)
	}
	if store.releasedFenceID != "fence_op_template_create" || activeWriterFenceCount(store.fences, "op_template_create") != 0 {
		t.Fatalf("released/active writer fence = %q/%#v, want released source writer fence", store.releasedFenceID, store.fences)
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

func TestTemplateCreateExecutorJVSFailureAfterSaveRetainsWriterFence(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
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
	if strings.Join(runner.calls, ",") != "save,repo_clone" {
		t.Fatalf("jvs calls = %#v, want save,repo_clone", runner.calls)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.releasedFenceID != "" || activeWriterFenceCount(store.fences, "op_template_create") != 1 {
		t.Fatalf("operation/release/fences = %#v/%q/%#v, want retained writer fence after uncertain JVS side effect", store.operation, store.releasedFenceID, store.fences)
	}
}

func TestTemplateCloneExecutorClonesTemplateToRepoWithoutSave(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = templateResource(now)
	runner := &fakeJVSRunner{
		repoCloneSummary: jvsrunner.RepoCloneSummary{SourceRepoID: "jvs_template_alpha", TargetRepoID: "jvs_repo_clone", SavePointsMode: "main", SavePointsCopiedCount: 1, RuntimeStateCopied: false, Workspace: "main"},
		doctorSummary:    jvsrunner.DoctorSummary{RepoID: "jvs_repo_clone", Healthy: true, Workspace: "main"},
	}
	executor, err := NewTemplateCloneExecutor(TemplateConfig{Store: store, JVSRunner: runner, Owner: "worker-a", Clock: func() time.Time { return now }, AuditEventID: func() string { return "audit_template_clone" }, VolumeRoots: store.volumeRoots})
	if err != nil {
		t.Fatalf("NewTemplateCloneExecutor: %v", err)
	}

	if err := executor.ExecuteOperationRecovery(context.Background(), templateCloneLeasedRecord(now), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}

	if got := strings.Join(runner.calls, ","); got != "repo_clone,doctor" {
		t.Fatalf("jvs calls = %s, want repo_clone,doctor", got)
	}
	if store.repo.ID != "repo_clone01" || store.repo.Kind != resources.RepoKindRepo || store.operation.Phase != operations.OperationPhaseTemplateCloneCommitted {
		t.Fatalf("committed repo/operation = %#v %#v", store.repo, store.operation)
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
			MountPolicy:       map[string]any{"workload_mount_enabled": true, "workload_mount_requires_jvs_external_control_root": true, "allow_privileged_workload": false},
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
	namespace                            resources.Namespace
	binding                              resources.NamespaceVolumeBinding
	volume                               resources.Volume
	volumeRoots                          map[string]string
	fences                               []fences.Fence
	repo                                 resources.Repo
	restorePlan                          restoreplan.Plan
	previewOperation                     operations.OperationRecord
	operation                            operations.OperationRecord
	auditEvents                          []audit.Event
	exports                              []sessionstate.ExportSession
	mounts                               []sessionstate.WorkloadMountBinding
	createFenceCalls                     int
	progressUpdates                      int
	restorePreviewProgressUpdates        int
	restorePreviewDiscardProgressUpdates int
	restoreRunWriterFenceMarks           int
	restoreRunConsumingMarks             int
	templateCreateWriterFenceMarks       int
	releasedFenceID                      string
	successErr                           error
	blockingLifecycle                    []operations.OperationRecord
	beforeListSessions                   func()
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
func (store *fakeRepoCreateStore) CommitRepoCreateSucceededWithLease(_ context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
	if store.successErr != nil {
		return resources.Repo{}, operations.OperationRecord{}, store.successErr
	}
	store.repo = repo
	store.operation = record.Record()
	store.releasedFenceID = fenceID
	store.auditEvents = append(store.auditEvents, event)
	return repo, store.operation, nil
}
func (store *fakeRepoCreateStore) CommitRepoCreateFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error) {
	store.operation = record.Record()
	store.releasedFenceID = releaseFenceID
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}
func (store *fakeRepoCreateStore) ListExportSessionsByRepo(context.Context, string) ([]sessionstate.ExportSession, error) {
	if store.beforeListSessions != nil {
		store.beforeListSessions()
	}
	return append([]sessionstate.ExportSession(nil), store.exports...), nil
}
func (store *fakeRepoCreateStore) ListWorkloadMountBindingsByRepo(context.Context, string) ([]sessionstate.WorkloadMountBinding, error) {
	if store.beforeListSessions != nil {
		store.beforeListSessions()
	}
	return append([]sessionstate.WorkloadMountBinding(nil), store.mounts...), nil
}
func (store *fakeRepoCreateStore) ListEarlierNonTerminalRepoLifecycleOperations(context.Context, string, string, time.Time) ([]operations.OperationRecord, error) {
	return append([]operations.OperationRecord(nil), store.blockingLifecycle...), nil
}
func (store *fakeRepoCreateStore) CommitRepoLifecycleSucceededWithLease(_ context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
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

func (store *fakeRepoCreateStore) UpdateSavePointCreateProgressWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time) (operations.OperationRecord, error) {
	store.progressUpdates++
	store.operation = record.Record()
	return store.operation, nil
}

func (store *fakeRepoCreateStore) CommitSavePointCreateSucceededWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (operations.OperationRecord, error) {
	store.operation = record.Record()
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}

func (store *fakeRepoCreateStore) CommitSavePointCreateFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (operations.OperationRecord, error) {
	store.operation = record.Record()
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}

func (store *fakeRepoCreateStore) CommitTemplateCreateSucceededWithLease(_ context.Context, template resources.Repo, _ string, _ string, _ string, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (resources.Repo, operations.OperationRecord, error) {
	store.repo = template
	store.operation = record.Record()
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

func (store *fakeRepoCreateStore) CommitTemplateCloneSucceededWithLease(_ context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (resources.Repo, operations.OperationRecord, error) {
	store.repo = repo
	store.operation = record.Record()
	store.auditEvents = append(store.auditEvents, event)
	return repo, store.operation, nil
}

func (store *fakeRepoCreateStore) CommitTemplateCloneFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (operations.OperationRecord, error) {
	store.operation = record.Record()
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}

func (store *fakeRepoCreateStore) UpdateRestorePreviewPreflightWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time) (operations.OperationRecord, error) {
	store.restorePreviewProgressUpdates++
	store.operation = record.Record()
	return store.operation, nil
}

func (store *fakeRepoCreateStore) CommitRestorePreviewSucceededWithLease(_ context.Context, plan restoreplan.Plan, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	store.restorePlan = plan
	store.operation = record.Record()
	store.auditEvents = append(store.auditEvents, event)
	return plan, store.operation, nil
}

func (store *fakeRepoCreateStore) CommitRestorePreviewFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (operations.OperationRecord, error) {
	store.operation = record.Record()
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}

func (store *fakeRepoCreateStore) GetOperation(_ context.Context, operationID string) (operations.OperationRecord, error) {
	if store.previewOperation.ID == operationID {
		return store.previewOperation, nil
	}
	return operations.OperationRecord{}, errors.New("operation not found")
}

func (store *fakeRepoCreateStore) GetRestorePlanByPreviewOperation(_ context.Context, previewOperationID string) (restoreplan.Plan, error) {
	if store.restorePlan.PreviewOperationID == previewOperationID {
		return store.restorePlan, nil
	}
	return restoreplan.Plan{}, errors.New("restore plan not found")
}

func (store *fakeRepoCreateStore) GetActiveRestorePlanByRepo(context.Context, string) (restoreplan.Plan, error) {
	return store.restorePlan, nil
}

func (store *fakeRepoCreateStore) CreatePendingRestorePlan(_ context.Context, plan restoreplan.Plan) error {
	store.restorePlan = plan
	return nil
}

func (store *fakeRepoCreateStore) TransitionRestorePlanStatus(_ context.Context, _ string, _, to restoreplan.Status, now time.Time) (restoreplan.Plan, error) {
	store.restorePlan.Status = to
	store.restorePlan.UpdatedAt = now
	return store.restorePlan, nil
}

func (store *fakeRepoCreateStore) MarkRestorePreviewDiscardingWithLease(_ context.Context, plan restoreplan.Plan, record operations.SanitizedOperationRecord, _ string, now time.Time) (restoreplan.Plan, operations.OperationRecord, error) {
	store.restorePreviewDiscardProgressUpdates++
	plan.Status = restoreplan.StatusDiscarding
	plan.UpdatedAt = now
	store.restorePlan = plan
	store.operation = record.Record()
	return plan, store.operation, nil
}

func (store *fakeRepoCreateStore) CommitRestorePreviewDiscardSucceededWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	store.restorePlan.Status = restoreplan.StatusDiscarded
	store.restorePlan.UpdatedAt = now
	store.operation = record.Record()
	store.auditEvents = append(store.auditEvents, event)
	return store.restorePlan, store.operation, nil
}

func (store *fakeRepoCreateStore) CommitRestorePreviewDiscardFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	store.operation = record.Record()
	if store.operation.Phase == operations.OperationPhaseRestorePreviewDiscarding {
		store.restorePlan.Status = restoreplan.StatusOperatorInterventionRequired
		store.restorePlan.UpdatedAt = now
	}
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}

func (store *fakeRepoCreateStore) MarkRestoreRunWriterFencedWithLease(_ context.Context, fence fences.Fence, record operations.SanitizedOperationRecord, _ string, _ time.Time) (fences.Fence, operations.OperationRecord, error) {
	store.restoreRunWriterFenceMarks++
	store.operation = record.Record()
	for _, existing := range store.fences {
		if existing.ID == fence.ID && existing.Kind == fences.KindWriterSession && existing.HolderOperationID == store.operation.ID && existing.Status == fences.StatusActive && existing.ReleasedAt == nil && existing.RecoveredAt == nil {
			return existing, store.operation, nil
		}
	}
	store.fences = append(store.fences, fence)
	return fence, store.operation, nil
}

func (store *fakeRepoCreateStore) MarkRestoreRunConsumingWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, now time.Time) (restoreplan.Plan, operations.OperationRecord, error) {
	store.restoreRunConsumingMarks++
	if store.restorePlan.Stale {
		return restoreplan.Plan{}, operations.OperationRecord{}, errors.New("stale restore plan cannot be consumed")
	}
	store.operation = record.Record()
	store.restorePlan.Status = restoreplan.StatusConsuming
	store.restorePlan.UpdatedAt = now
	return store.restorePlan, store.operation, nil
}

func (store *fakeRepoCreateStore) CommitRestoreRunSucceededWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	store.operation = record.Record()
	store.restorePlan.Status = restoreplan.StatusConsumed
	store.restorePlan.UpdatedAt = now
	store.releaseWriterFence(store.operation.SessionFenceID, now)
	store.auditEvents = append(store.auditEvents, event)
	return store.restorePlan, store.operation, nil
}

func (store *fakeRepoCreateStore) CommitRestoreRunStalePreviewWithLease(_ context.Context, plan restoreplan.Plan, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	store.operation = record.Record()
	store.restorePlan.Stale = plan.Stale
	store.restorePlan.Blockers = append([]restoreplan.Blocker(nil), plan.Blockers...)
	store.restorePlan.UpdatedAt = now
	store.auditEvents = append(store.auditEvents, event)
	return store.restorePlan, store.operation, nil
}

func (store *fakeRepoCreateStore) CommitRestoreRunFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	store.operation = record.Record()
	switch store.operation.Phase {
	case operations.OperationPhaseRestoreRunWriterFenced:
		store.releaseWriterFence(store.operation.SessionFenceID, now)
	case operations.OperationPhaseRestoreRunConsuming:
		store.restorePlan.Status = restoreplan.StatusOperatorInterventionRequired
		store.restorePlan.UpdatedAt = now
	}
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
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
	calls                   []string
	payloadRoot             string
	controlRoot             string
	saveMessage             string
	initSummary             jvsrunner.InitSummary
	doctorSummary           jvsrunner.DoctorSummary
	saveSummary             jvsrunner.SaveSummary
	historySummary          jvsrunner.HistorySummary
	recoveryStatusSummary   jvsrunner.RecoveryStatusSummary
	recoveryStatusSummaries []jvsrunner.RecoveryStatusSummary
	restorePreviewSummary   jvsrunner.RestorePreviewSummary
	restoreRunSummary       jvsrunner.RestoreRunSummary
	restoreDiscardSummary   jvsrunner.RestoreDiscardSummary
	repoCloneSummary        jvsrunner.RepoCloneSummary
	beforeRestorePreview    func()
	beforeRestoreRun        func()
	beforeRestoreDiscard    func()
	initErr                 error
	doctorErr               error
	saveErr                 error
	historyErr              error
	recoveryStatusErr       error
	restorePreviewErr       error
	restoreRunErr           error
	restoreDiscardErr       error
	repoCloneErr            error
}

func (runner *fakeJVSRunner) Init(_ context.Context, payloadRoot, controlRoot string) (jvsrunner.InitSummary, error) {
	runner.calls = append(runner.calls, "init")
	runner.payloadRoot = payloadRoot
	runner.controlRoot = controlRoot
	return runner.initSummary, runner.initErr
}
func (runner *fakeJVSRunner) DoctorStrict(_ context.Context, controlRoot string) (jvsrunner.DoctorSummary, error) {
	runner.calls = append(runner.calls, "doctor")
	runner.controlRoot = controlRoot
	return runner.doctorSummary, runner.doctorErr
}

func (runner *fakeJVSRunner) Save(_ context.Context, controlRoot, message string) (jvsrunner.SaveSummary, error) {
	runner.calls = append(runner.calls, "save")
	runner.controlRoot = controlRoot
	runner.saveMessage = message
	return runner.saveSummary, runner.saveErr
}

func (runner *fakeJVSRunner) History(_ context.Context, controlRoot string) (jvsrunner.HistorySummary, error) {
	runner.calls = append(runner.calls, "history")
	runner.controlRoot = controlRoot
	return runner.historySummary, runner.historyErr
}

func (runner *fakeJVSRunner) RepoClone(_ context.Context, sourceControlRoot, targetPayloadRoot, targetControlRoot string) (jvsrunner.RepoCloneSummary, error) {
	runner.calls = append(runner.calls, "repo_clone")
	runner.controlRoot = targetControlRoot
	runner.payloadRoot = targetPayloadRoot
	if runner.repoCloneSummary.SourceRepoID == "" {
		runner.repoCloneSummary.SourceRepoID = "jvs_repo_alpha"
	}
	_ = sourceControlRoot
	return runner.repoCloneSummary, runner.repoCloneErr
}

func (runner *fakeJVSRunner) RecoveryStatus(_ context.Context, controlRoot string) (jvsrunner.RecoveryStatusSummary, error) {
	runner.calls = append(runner.calls, "recovery_status")
	runner.controlRoot = controlRoot
	if len(runner.recoveryStatusSummaries) > 0 {
		summary := runner.recoveryStatusSummaries[0]
		runner.recoveryStatusSummaries = runner.recoveryStatusSummaries[1:]
		return summary, runner.recoveryStatusErr
	}
	return runner.recoveryStatusSummary, runner.recoveryStatusErr
}

func (runner *fakeJVSRunner) RestorePreview(_ context.Context, controlRoot, savePointID string) (jvsrunner.RestorePreviewSummary, error) {
	runner.calls = append(runner.calls, "restore_preview")
	runner.controlRoot = controlRoot
	if runner.beforeRestorePreview != nil {
		runner.beforeRestorePreview()
	}
	return runner.restorePreviewSummary, runner.restorePreviewErr
}

func (runner *fakeJVSRunner) RestoreRun(_ context.Context, controlRoot, planID string) (jvsrunner.RestoreRunSummary, error) {
	runner.calls = append(runner.calls, "restore_run")
	runner.controlRoot = controlRoot
	if runner.beforeRestoreRun != nil {
		runner.beforeRestoreRun()
	}
	return runner.restoreRunSummary, runner.restoreRunErr
}

func (runner *fakeJVSRunner) RestoreDiscard(_ context.Context, controlRoot, planID string) (jvsrunner.RestoreDiscardSummary, error) {
	runner.calls = append(runner.calls, "restore_discard")
	runner.controlRoot = controlRoot
	if runner.beforeRestoreDiscard != nil {
		runner.beforeRestoreDiscard()
	}
	return runner.restoreDiscardSummary, runner.restoreDiscardErr
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
