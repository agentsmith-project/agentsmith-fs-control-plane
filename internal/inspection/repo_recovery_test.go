package inspection

import (
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestInspectRepoRecoveryClassificationMatrix(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	liveLease := now.Add(30 * time.Minute)
	expiredLease := now.Add(-time.Minute)

	tests := []struct {
		name       string
		repo       resources.Repo
		fences     []fences.Fence
		operations map[string]operations.OperationRecord
		wantAction RecoveryAction
		wantReason string
	}{
		{
			name:       "stable active repo with no held fence noops",
			repo:       repoRecoveryFixture(resources.RepoStatusActive, ""),
			wantAction: RecoveryActionNoop,
			wantReason: "stable_repo_no_held_fence",
		},
		{
			name:       "stable archived repo with no held fence noops",
			repo:       repoRecoveryFixture(resources.RepoStatusArchived, "op_archive01"),
			wantAction: RecoveryActionNoop,
			wantReason: "stable_repo_no_held_fence",
		},
		{
			name:       "stable tombstoned repo with no held fence noops",
			repo:       repoRecoveryFixture(resources.RepoStatusTombstoned, "op_delete01"),
			wantAction: RecoveryActionNoop,
			wantReason: "stable_repo_no_held_fence",
		},
		{
			name:       "stable purged repo with no held fence noops",
			repo:       repoRecoveryFixture(resources.RepoStatusPurged, "op_purge01"),
			wantAction: RecoveryActionNoop,
			wantReason: "stable_repo_no_held_fence",
		},
		{
			name:       "non terminal lifecycle repo missing last lifecycle operation is manual",
			repo:       repoRecoveryFixture(resources.RepoStatusArchiving, ""),
			wantAction: RecoveryActionManualIntervention,
			wantReason: "missing_last_lifecycle_operation_id",
		},
		{
			name:       "lifecycle repo missing held lifecycle fence is manual",
			repo:       repoRecoveryFixture(resources.RepoStatusArchiving, "op_archive01"),
			wantAction: RecoveryActionManualIntervention,
			wantReason: "missing_lifecycle_fence",
		},
		{
			name: "lifecycle fence holder mismatch is manual",
			repo: repoRecoveryFixture(resources.RepoStatusArchiving, "op_archive01"),
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindLifecycle, fences.StatusActive, "op_other01", now),
			},
			operations: map[string]operations.OperationRecord{
				"op_archive01": repoRecoveryOperationFixture("op_archive01", operations.OperationRepoArchive, operations.OperationStateRunning, &liveLease),
				"op_other01":   repoRecoveryOperationFixture("op_other01", operations.OperationRepoArchive, operations.OperationStateRunning, &liveLease),
			},
			wantAction: RecoveryActionManualIntervention,
			wantReason: "lifecycle_fence_holder_mismatch",
		},
		{
			name: "active lifecycle fence with live running holder waits",
			repo: repoRecoveryFixture(resources.RepoStatusArchiving, "op_archive01"),
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindLifecycle, fences.StatusActive, "op_archive01", now),
			},
			operations: map[string]operations.OperationRecord{
				"op_archive01": repoRecoveryOperationFixture("op_archive01", operations.OperationRepoArchive, operations.OperationStateRunning, &liveLease),
			},
			wantAction: RecoveryActionWait,
			wantReason: "running_operation_live_lease",
		},
		{
			name: "expired lifecycle fence recovers",
			repo: repoRecoveryFixture(resources.RepoStatusArchiving, "op_archive01"),
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindLifecycle, fences.StatusExpired, "op_archive01", now),
			},
			operations: map[string]operations.OperationRecord{
				"op_archive01": repoRecoveryOperationFixture("op_archive01", operations.OperationRepoArchive, operations.OperationStateQueued, nil),
			},
			wantAction: RecoveryActionRecover,
			wantReason: "fence_held_recovery_required",
		},
		{
			name: "recovery required lifecycle fence recovers",
			repo: repoRecoveryFixture(resources.RepoStatusArchiving, "op_archive01"),
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindLifecycle, fences.StatusRecoveryRequired, "op_archive01", now),
			},
			operations: map[string]operations.OperationRecord{
				"op_archive01": repoRecoveryOperationFixture("op_archive01", operations.OperationRepoArchive, operations.OperationStateQueued, nil),
			},
			wantAction: RecoveryActionRecover,
			wantReason: "fence_held_recovery_required",
		},
		{
			name: "active lifecycle fence with expired running holder lease reclaims",
			repo: repoRecoveryFixture(resources.RepoStatusArchiving, "op_archive01"),
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindLifecycle, fences.StatusActive, "op_archive01", now),
			},
			operations: map[string]operations.OperationRecord{
				"op_archive01": repoRecoveryOperationFixture("op_archive01", operations.OperationRepoArchive, operations.OperationStateRunning, &expiredLease),
			},
			wantAction: RecoveryActionReclaim,
			wantReason: "running_operation_expired_lease",
		},
		{
			name: "terminal holder operation while fence still held is manual",
			repo: repoRecoveryFixture(resources.RepoStatusArchiving, "op_archive01"),
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindLifecycle, fences.StatusActive, "op_archive01", now),
			},
			operations: map[string]operations.OperationRecord{
				"op_archive01": repoRecoveryOperationFixture("op_archive01", operations.OperationRepoArchive, operations.OperationStateSucceeded, nil),
			},
			wantAction: RecoveryActionManualIntervention,
			wantReason: "holder_operation_terminal_while_fence_held",
		},
		{
			name: "terminal archived repo with held fence is manual",
			repo: repoRecoveryFixture(resources.RepoStatusArchived, "op_archive01"),
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindLifecycle, fences.StatusActive, "op_archive01", now),
			},
			operations: map[string]operations.OperationRecord{
				"op_archive01": repoRecoveryOperationFixture("op_archive01", operations.OperationRepoArchive, operations.OperationStateRunning, &liveLease),
			},
			wantAction: RecoveryActionManualIntervention,
			wantReason: "terminal_repo_has_held_fence",
		},
		{
			name: "terminal tombstoned repo with held fence is manual",
			repo: repoRecoveryFixture(resources.RepoStatusTombstoned, "op_delete01"),
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindLifecycle, fences.StatusActive, "op_delete01", now),
			},
			operations: map[string]operations.OperationRecord{
				"op_delete01": repoRecoveryOperationFixture("op_delete01", operations.OperationRepoDelete, operations.OperationStateRunning, &liveLease),
			},
			wantAction: RecoveryActionManualIntervention,
			wantReason: "terminal_repo_has_held_fence",
		},
		{
			name: "terminal purged repo with held fence is manual",
			repo: repoRecoveryFixture(resources.RepoStatusPurged, "op_purge01"),
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindLifecycle, fences.StatusActive, "op_purge01", now),
			},
			operations: map[string]operations.OperationRecord{
				"op_purge01": repoRecoveryOperationFixture("op_purge01", operations.OperationRepoPurge, operations.OperationStateRunning, &liveLease),
			},
			wantAction: RecoveryActionManualIntervention,
			wantReason: "terminal_repo_has_held_fence",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InspectRepoRecovery(tt.repo, tt.fences, tt.operations, RecoveryContext{Now: now})

			if got.Plan.Action != tt.wantAction || got.Plan.Reason != tt.wantReason {
				t.Fatalf("plan = %#v, want action %s reason %s", got.Plan, tt.wantAction, tt.wantReason)
			}
			if got.RepoID != tt.repo.ID || got.NamespaceID != tt.repo.NamespaceID || got.Status != tt.repo.Status {
				t.Fatalf("repo identity/status = %#v", got)
			}
			if len(got.Findings) == 0 {
				t.Fatalf("findings = %#v, want at least one stable finding", got.Findings)
			}
		})
	}
}

func TestInspectRepoRecoveryOperatorInterventionRequiredIsManual(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	liveLease := now.Add(30 * time.Minute)

	tests := []struct {
		name            string
		fences          []fences.Fence
		operations      map[string]operations.OperationRecord
		wantLifecycle   bool
		wantWriterCount int
	}{
		{name: "no last operation and no held fence"},
		{
			name: "held lifecycle fence still final manual",
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindLifecycle, fences.StatusActive, "op_archive01", now),
			},
			operations: map[string]operations.OperationRecord{
				"op_archive01": repoRecoveryOperationFixture("op_archive01", operations.OperationRepoArchive, operations.OperationStateRunning, &liveLease),
			},
			wantLifecycle: true,
		},
		{
			name: "held writer fence still final manual",
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindWriterSession, fences.StatusActive, "op_restore01", now),
			},
			operations: map[string]operations.OperationRecord{
				"op_restore01": repoRecoveryOperationFixture("op_restore01", operations.OperationRestore, operations.OperationStateRunning, &liveLease),
			},
			wantWriterCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InspectRepoRecovery(
				repoRecoveryFixture(resources.RepoStatusOperatorInterventionRequired, ""),
				tt.fences,
				tt.operations,
				RecoveryContext{Now: now},
			)

			if got.Plan.Action != RecoveryActionManualIntervention || got.Plan.Reason != "operator_intervention_required" {
				t.Fatalf("plan = %#v, want manual operator_intervention_required", got.Plan)
			}
			if (got.LifecycleFence != nil) != tt.wantLifecycle {
				t.Fatalf("lifecycle fence = %#v, want present %v", got.LifecycleFence, tt.wantLifecycle)
			}
			if len(got.WriterSessionFences) != tt.wantWriterCount {
				t.Fatalf("writer fences = %#v, want count %d", got.WriterSessionFences, tt.wantWriterCount)
			}
			for _, surface := range []InspectionSurface{got.Sessions, got.Exports, got.Mounts} {
				if surface.Status != InspectionSurfaceNotImplemented || !strings.Contains(surface.Reason, "not inspectable") {
					t.Fatalf("surface = %#v, want not implemented/not inspectable", surface)
				}
			}
		})
	}
}

func TestInspectRepoRecoveryOperatorInterventionRequiredStillFailsClosedForMalformedHeldFences(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	liveLease := now.Add(30 * time.Minute)

	tests := []struct {
		name       string
		fence      fences.Fence
		operations map[string]operations.OperationRecord
		wantReason string
	}{
		{
			name: "invalid held fence",
			fence: func() fences.Fence {
				fence := repoRecoveryFenceFixture(fences.KindWriterSession, fences.StatusActive, "op_restore01", now)
				fence.ExpiresAt = time.Time{}
				return fence
			}(),
			operations: map[string]operations.OperationRecord{
				"op_restore01": repoRecoveryOperationFixture("op_restore01", operations.OperationRestore, operations.OperationStateRunning, &liveLease),
			},
			wantReason: "invalid_fence",
		},
		{
			name: "held fence repo mismatch",
			fence: func() fences.Fence {
				fence := repoRecoveryFenceFixture(fences.KindWriterSession, fences.StatusActive, "op_restore01", now)
				fence.RepoID = "repo_other01"
				return fence
			}(),
			operations: map[string]operations.OperationRecord{
				"op_restore01": repoRecoveryOperationFixture("op_restore01", operations.OperationRestore, operations.OperationStateRunning, &liveLease),
			},
			wantReason: "fence_repo_mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InspectRepoRecovery(
				repoRecoveryFixture(resources.RepoStatusOperatorInterventionRequired, ""),
				[]fences.Fence{tt.fence},
				tt.operations,
				RecoveryContext{Now: now},
			)

			if got.Plan.Action != RecoveryActionManualIntervention || got.Plan.Reason != tt.wantReason {
				t.Fatalf("plan = %#v, want manual %s", got.Plan, tt.wantReason)
			}
		})
	}
}

func TestInspectRepoRecoveryLifecycleOperationMustMatchRepoStatus(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	liveLease := now.Add(30 * time.Minute)

	tests := []struct {
		status resources.RepoStatus
		typ    operations.OperationType
		opID   string
	}{
		{status: resources.RepoStatusArchiving, typ: operations.OperationRepoArchive, opID: "op_archive01"},
		{status: resources.RepoStatusRestoringArchived, typ: operations.OperationRepoRestoreArchived, opID: "op_restore01"},
		{status: resources.RepoStatusDeleting, typ: operations.OperationRepoDelete, opID: "op_delete01"},
		{status: resources.RepoStatusRestoringTombstoned, typ: operations.OperationRepoRestoreTombstoned, opID: "op_restore01"},
		{status: resources.RepoStatusPurging, typ: operations.OperationRepoPurge, opID: "op_purge01"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status)+" accepts matching operation type", func(t *testing.T) {
			got := InspectRepoRecovery(
				repoRecoveryFixture(tt.status, tt.opID),
				[]fences.Fence{repoRecoveryFenceFixture(fences.KindLifecycle, fences.StatusActive, tt.opID, now)},
				map[string]operations.OperationRecord{
					tt.opID: repoRecoveryOperationFixture(tt.opID, tt.typ, operations.OperationStateRunning, &liveLease),
				},
				RecoveryContext{Now: now},
			)

			if got.Plan.Action != RecoveryActionWait || got.Plan.Reason != "running_operation_live_lease" {
				t.Fatalf("plan = %#v, want wait for matching lifecycle operation type", got.Plan)
			}
		})

		t.Run(string(tt.status)+" rejects mismatched operation type", func(t *testing.T) {
			got := InspectRepoRecovery(
				repoRecoveryFixture(tt.status, tt.opID),
				[]fences.Fence{repoRecoveryFenceFixture(fences.KindLifecycle, fences.StatusActive, tt.opID, now)},
				map[string]operations.OperationRecord{
					tt.opID: repoRecoveryOperationFixture(tt.opID, operations.OperationExportCreate, operations.OperationStateRunning, &liveLease),
				},
				RecoveryContext{Now: now},
			)

			if got.Plan.Action != RecoveryActionManualIntervention || got.Plan.Reason != "lifecycle_operation_type_mismatch" {
				t.Fatalf("plan = %#v, want manual lifecycle_operation_type_mismatch", got.Plan)
			}
		})
	}
}

func TestInspectRepoRecoveryLifecycleInProgressFailsClosedOnWriterSessionFence(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	liveLease := now.Add(30 * time.Minute)
	expiredLease := now.Add(-time.Minute)

	tests := []struct {
		name           string
		status         resources.RepoStatus
		opID           string
		typ            operations.OperationType
		lifecycleLease *time.Time
	}{
		{name: "archiving live lifecycle holder", status: resources.RepoStatusArchiving, opID: "op_archive01", typ: operations.OperationRepoArchive, lifecycleLease: &liveLease},
		{name: "archiving expired lifecycle holder", status: resources.RepoStatusArchiving, opID: "op_archive01", typ: operations.OperationRepoArchive, lifecycleLease: &expiredLease},
		{name: "deleting live lifecycle holder", status: resources.RepoStatusDeleting, opID: "op_delete01", typ: operations.OperationRepoDelete, lifecycleLease: &liveLease},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writerFence := repoRecoveryFenceFixture(fences.KindWriterSession, fences.StatusRecoveryRequired, "op_restore01", now)
			writerFence.ID = "fence_writer01"
			got := InspectRepoRecovery(
				repoRecoveryFixture(tt.status, tt.opID),
				[]fences.Fence{
					repoRecoveryFenceFixture(fences.KindLifecycle, fences.StatusActive, tt.opID, now),
					writerFence,
				},
				map[string]operations.OperationRecord{
					tt.opID:        repoRecoveryOperationFixture(tt.opID, tt.typ, operations.OperationStateRunning, tt.lifecycleLease),
					"op_restore01": repoRecoveryOperationFixture("op_restore01", operations.OperationRestore, operations.OperationStateRunning, &liveLease),
				},
				RecoveryContext{Now: now},
			)

			if got.Plan.Action != RecoveryActionManualIntervention || got.Plan.Reason != "writer_session_fence_held_during_lifecycle" {
				t.Fatalf("plan = %#v, want manual writer_session_fence_held_during_lifecycle", got.Plan)
			}
			if len(got.WriterSessionFences) != 1 || got.WriterSessionFences[0].FencePlan.Action != RecoveryActionRecover {
				t.Fatalf("writer fence inspection = %#v, want recovery-required writer fence detail", got.WriterSessionFences)
			}
		})
	}
}

func TestInspectRepoRecoveryActiveRepoWithHeldLifecycleFenceIsManual(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	liveLease := now.Add(30 * time.Minute)

	got := InspectRepoRecovery(
		repoRecoveryFixture(resources.RepoStatusActive, ""),
		[]fences.Fence{repoRecoveryFenceFixture(fences.KindLifecycle, fences.StatusActive, "op_archive01", now)},
		map[string]operations.OperationRecord{
			"op_archive01": repoRecoveryOperationFixture("op_archive01", operations.OperationRepoArchive, operations.OperationStateRunning, &liveLease),
		},
		RecoveryContext{Now: now},
	)

	if got.Plan.Action != RecoveryActionManualIntervention || got.Plan.Reason != "stable_repo_has_lifecycle_fence" {
		t.Fatalf("plan = %#v, want manual stable_repo_has_lifecycle_fence", got.Plan)
	}
	if got.LifecycleFence == nil || got.LifecycleFence.FencePlan.Action != RecoveryActionWait {
		t.Fatalf("lifecycle fence detail = %#v, want inspected active lifecycle fence", got.LifecycleFence)
	}
}

func TestInspectRepoRecoveryActiveWriterSessionFenceIsNotInspectable(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	liveLease := now.Add(30 * time.Minute)
	repo := repoRecoveryFixture(resources.RepoStatusActive, "")
	writerFence := repoRecoveryFenceFixture(fences.KindWriterSession, fences.StatusActive, "op_restore01", now)
	operationsByID := map[string]operations.OperationRecord{
		"op_restore01": repoRecoveryOperationFixture("op_restore01", operations.OperationRestore, operations.OperationStateRunning, &liveLease),
	}

	got := InspectRepoRecovery(repo, []fences.Fence{writerFence}, operationsByID, RecoveryContext{Now: now})

	if got.Plan.Action != RecoveryActionWait || got.Plan.Reason != "writer_session_fence_active_held" {
		t.Fatalf("plan = %#v, want wait on active writer session fence", got.Plan)
	}
	if len(got.WriterSessionFences) != 1 {
		t.Fatalf("writer fences = %#v, want one inspected writer fence", got.WriterSessionFences)
	}
	if got.WriterSessionFences[0].FencePlan.Action != RecoveryActionWait ||
		got.WriterSessionFences[0].HolderOperationPlan == nil ||
		got.WriterSessionFences[0].HolderOperationPlan.Action != RecoveryActionWait {
		t.Fatalf("writer fence inspection = %#v, want fence and holder op wait", got.WriterSessionFences[0])
	}
	for _, surface := range []InspectionSurface{got.Sessions, got.Exports, got.Mounts} {
		if surface.Status != InspectionSurfaceNotImplemented || !strings.Contains(surface.Reason, "not inspectable") {
			t.Fatalf("surface = %#v, want not implemented/not inspectable", surface)
		}
	}
}

func TestInspectRepoRecoveryWriterSessionFenceRequiresRestoreHolder(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	liveLease := now.Add(30 * time.Minute)
	repo := repoRecoveryFixture(resources.RepoStatusActive, "")
	writerFence := repoRecoveryFenceFixture(fences.KindWriterSession, fences.StatusActive, "op_export01", now)
	operationsByID := map[string]operations.OperationRecord{
		"op_export01": repoRecoveryOperationFixture("op_export01", operations.OperationExportCreate, operations.OperationStateRunning, &liveLease),
	}

	got := InspectRepoRecovery(repo, []fences.Fence{writerFence}, operationsByID, RecoveryContext{Now: now})

	if got.Plan.Action != RecoveryActionManualIntervention || got.Plan.Reason != "writer_session_operation_type_mismatch" {
		t.Fatalf("plan = %#v, want manual writer_session_operation_type_mismatch", got.Plan)
	}
}

func TestInspectRepoRecoveryFailsClosedForMalformedInputs(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	liveLease := now.Add(30 * time.Minute)

	tests := []struct {
		name       string
		repo       resources.Repo
		fences     []fences.Fence
		operations map[string]operations.OperationRecord
		wantReason string
	}{
		{
			name:       "invalid repo",
			repo:       resources.Repo{ID: "repo_alpha01"},
			wantReason: "invalid_repo",
		},
		{
			name: "fence repo mismatch",
			repo: repoRecoveryFixture(resources.RepoStatusActive, ""),
			fences: []fences.Fence{
				func() fences.Fence {
					fence := repoRecoveryFenceFixture(fences.KindWriterSession, fences.StatusActive, "op_restore01", now)
					fence.RepoID = "repo_other01"
					return fence
				}(),
			},
			operations: map[string]operations.OperationRecord{
				"op_restore01": repoRecoveryOperationFixture("op_restore01", operations.OperationRestore, operations.OperationStateRunning, &liveLease),
			},
			wantReason: "fence_repo_mismatch",
		},
		{
			name: "missing holder operation record",
			repo: repoRecoveryFixture(resources.RepoStatusActive, ""),
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindWriterSession, fences.StatusActive, "op_restore01", now),
			},
			wantReason: "missing_holder_operation_record",
		},
		{
			name: "holder operation id mismatch",
			repo: repoRecoveryFixture(resources.RepoStatusActive, ""),
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindWriterSession, fences.StatusActive, "op_restore01", now),
			},
			operations: map[string]operations.OperationRecord{
				"op_restore01": repoRecoveryOperationFixture("op_other01", operations.OperationRestore, operations.OperationStateRunning, &liveLease),
			},
			wantReason: "holder_operation_id_mismatch",
		},
		{
			name: "holder operation repo mismatch",
			repo: repoRecoveryFixture(resources.RepoStatusActive, ""),
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindWriterSession, fences.StatusActive, "op_restore01", now),
			},
			operations: map[string]operations.OperationRecord{
				"op_restore01": func() operations.OperationRecord {
					record := repoRecoveryOperationFixture("op_restore01", operations.OperationRestore, operations.OperationStateRunning, &liveLease)
					record.RepoID = "repo_other01"
					return record
				}(),
			},
			wantReason: "holder_operation_repo_mismatch",
		},
		{
			name: "holder operation missing repo id",
			repo: repoRecoveryFixture(resources.RepoStatusActive, ""),
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindWriterSession, fences.StatusActive, "op_restore01", now),
			},
			operations: map[string]operations.OperationRecord{
				"op_restore01": func() operations.OperationRecord {
					record := repoRecoveryOperationFixture("op_restore01", operations.OperationRestore, operations.OperationStateRunning, &liveLease)
					record.RepoID = ""
					return record
				}(),
			},
			wantReason: "holder_operation_missing_repo_id",
		},
		{
			name: "holder operation missing namespace id",
			repo: repoRecoveryFixture(resources.RepoStatusActive, ""),
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindWriterSession, fences.StatusActive, "op_restore01", now),
			},
			operations: map[string]operations.OperationRecord{
				"op_restore01": func() operations.OperationRecord {
					record := repoRecoveryOperationFixture("op_restore01", operations.OperationRestore, operations.OperationStateRunning, &liveLease)
					record.NamespaceID = ""
					return record
				}(),
			},
			wantReason: "holder_operation_missing_namespace_id",
		},
		{
			name: "invalid holder operation state",
			repo: repoRecoveryFixture(resources.RepoStatusArchiving, "op_archive01"),
			fences: []fences.Fence{
				repoRecoveryFenceFixture(fences.KindLifecycle, fences.StatusExpired, "op_archive01", now),
			},
			operations: map[string]operations.OperationRecord{
				"op_archive01": repoRecoveryOperationFixture("op_archive01", operations.OperationRepoArchive, operations.OperationState("wedged"), nil),
			},
			wantReason: "invalid_operation_state:wedged",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InspectRepoRecovery(tt.repo, tt.fences, tt.operations, RecoveryContext{Now: now})

			if got.Plan.Action != RecoveryActionManualIntervention || got.Plan.Reason != tt.wantReason {
				t.Fatalf("plan = %#v, want manual %s", got.Plan, tt.wantReason)
			}
		})
	}
}

func repoRecoveryFixture(status resources.RepoStatus, lastOperationID string) resources.Repo {
	now := time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC)
	repo := resources.Repo{
		ID:                  "repo_alpha01",
		NamespaceID:         "ns_alpha01",
		VolumeID:            "vol_shared01",
		JVSRepoID:           "jvs-alpha",
		Kind:                resources.RepoKindRepo,
		Status:              status,
		ControlVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/control",
		PayloadVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/payload",
		Lifecycle: resources.RepoLifecycle{
			Status:                   status,
			LastLifecycleOperationID: lastOperationID,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	switch status {
	case resources.RepoStatusDeleting, resources.RepoStatusTombstoned, resources.RepoStatusRestoringTombstoned, resources.RepoStatusPurging, resources.RepoStatusPurged:
		retention := now.Add(24 * time.Hour)
		repo.Lifecycle.RetentionExpiresAt = &retention
		repo.Lifecycle.PreDeleteStatus = resources.RepoStatusActive
	case resources.RepoStatusOperatorInterventionRequired:
		repo.Lifecycle.PreDeleteStatus = resources.RepoStatusActive
	}
	if status == resources.RepoStatusPurged {
		repo.Lifecycle.RetentionExpiresAt = nil
	}
	return repo
}

func repoRecoveryFenceFixture(kind fences.Kind, status fences.Status, holderOperationID string, now time.Time) fences.Fence {
	return fences.Fence{
		ID:                "fence_alpha",
		RepoID:            "repo_alpha01",
		Kind:              kind,
		HolderOperationID: holderOperationID,
		Status:            status,
		ExpiresAt:         now.Add(30 * time.Minute),
		CreatedAt:         now.Add(-time.Minute),
		UpdatedAt:         now.Add(-time.Minute),
	}
}

func repoRecoveryOperationFixture(id string, typ operations.OperationType, state operations.OperationState, leaseExpiresAt *time.Time) operations.OperationRecord {
	record := operations.OperationRecord{
		ID:                  id,
		Type:                typ,
		State:               state,
		Phase:               "inspect",
		Attempt:             1,
		CallerService:       "afscp-api",
		Resource:            operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:         "ns_alpha01",
		RepoID:              "repo_alpha01",
		ExternalResourceIDs: map[string]string{},
		InputSummary:        map[string]any{},
		CreatedAt:           time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC),
	}
	if leaseExpiresAt != nil {
		record.LeaseOwner = "worker-a"
		record.LeaseExpiresAt = leaseExpiresAt
	}
	return record
}
