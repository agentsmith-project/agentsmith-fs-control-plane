package inspection

import (
	"sort"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

type InspectionSurfaceStatus string

const (
	InspectionSurfaceNotImplemented InspectionSurfaceStatus = "not_implemented"
)

type InspectionSurface struct {
	Status InspectionSurfaceStatus
	Reason string
}

type RepoRecoveryFinding struct {
	Code   string
	Action RecoveryAction
	Reason string
}

type FenceInspection struct {
	FenceID             string
	Kind                fences.Kind
	HolderOperationID   string
	HolderOperationType operations.OperationType
	FencePlan           RecoveryPlan
	HolderOperationPlan *RecoveryPlan
}

type RepoRecoveryInspection struct {
	RepoID              string
	NamespaceID         string
	Status              resources.RepoStatus
	Plan                RecoveryPlan
	Findings            []RepoRecoveryFinding
	LifecycleFence      *FenceInspection
	WriterSessionFences []FenceInspection
	Sessions            InspectionSurface
	Exports             InspectionSurface
	Mounts              InspectionSurface
}

func InspectRepoRecovery(repo resources.Repo, heldFences []fences.Fence, operationsByID map[string]operations.OperationRecord, ctx RecoveryContext) RepoRecoveryInspection {
	inspection := RepoRecoveryInspection{
		RepoID:      repo.ID,
		NamespaceID: repo.NamespaceID,
		Status:      repo.Status,
		Sessions:    notInspectableSurface(),
		Exports:     notInspectableSurface(),
		Mounts:      notInspectableSurface(),
	}

	if err := repo.Validate(); err != nil {
		return inspection.withPlan(manual("invalid_repo"), "invalid_repo")
	}

	sortedFences := append([]fences.Fence(nil), heldFences...)
	sort.SliceStable(sortedFences, func(i, j int) bool {
		if sortedFences[i].RepoID != sortedFences[j].RepoID {
			return sortedFences[i].RepoID < sortedFences[j].RepoID
		}
		if !sortedFences[i].CreatedAt.Equal(sortedFences[j].CreatedAt) {
			return sortedFences[i].CreatedAt.Before(sortedFences[j].CreatedAt)
		}
		return sortedFences[i].ID < sortedFences[j].ID
	})

	var lifecycleFences []FenceInspection
	for _, fence := range sortedFences {
		if err := fences.ValidateFence(fence); err != nil {
			return inspection.withPlan(manual("invalid_fence"), "invalid_fence")
		}
		if fence.RepoID != repo.ID {
			return inspection.withPlan(manual("fence_repo_mismatch"), "fence_repo_mismatch")
		}
		if !fence.Held() {
			return inspection.withPlan(manual("fence_not_held"), "fence_not_held")
		}

		fenceInspection, fail := inspectHeldFence(fence, repo, operationsByID, ctx)
		if fail.Action == RecoveryActionManualIntervention {
			return inspection.withPlan(fail, fail.Reason)
		}

		switch fence.Kind {
		case fences.KindLifecycle:
			lifecycleFences = append(lifecycleFences, fenceInspection)
			if inspection.LifecycleFence == nil {
				copied := fenceInspection
				inspection.LifecycleFence = &copied
			}
		case fences.KindWriterSession:
			inspection.WriterSessionFences = append(inspection.WriterSessionFences, fenceInspection)
		}
	}

	if isTerminalRepoStatus(repo.Status) && len(sortedFences) > 0 {
		return inspection.withPlan(manual("terminal_repo_has_held_fence"), "terminal_repo_has_held_fence")
	}

	if repo.Status == resources.RepoStatusOperatorInterventionRequired {
		return inspection.withPlan(manual("operator_intervention_required"), "operator_intervention_required")
	}

	if isLifecycleInProgress(repo.Status) {
		if strings.TrimSpace(repo.Lifecycle.LastLifecycleOperationID) == "" {
			return inspection.withPlan(manual("missing_last_lifecycle_operation_id"), "missing_last_lifecycle_operation_id")
		}
		if len(lifecycleFences) == 0 {
			return inspection.withPlan(manual("missing_lifecycle_fence"), "missing_lifecycle_fence")
		}
		if len(lifecycleFences) > 1 {
			return inspection.withPlan(manual("multiple_lifecycle_fences"), "multiple_lifecycle_fences")
		}

		lifecycleFence := lifecycleFences[0]
		if lifecycleFence.HolderOperationID != repo.Lifecycle.LastLifecycleOperationID {
			return inspection.withPlan(manual("lifecycle_fence_holder_mismatch"), "lifecycle_fence_holder_mismatch")
		}
		if !lifecycleOperationTypeMatchesStatus(repo.Status, lifecycleFence.HolderOperationType) {
			return inspection.withPlan(manual("lifecycle_operation_type_mismatch"), "lifecycle_operation_type_mismatch")
		}
		if lifecycleFence.HolderOperationPlan != nil && lifecycleFence.HolderOperationPlan.Action == RecoveryActionNoop {
			return inspection.withPlan(manual("holder_operation_terminal_while_fence_held"), "holder_operation_terminal_while_fence_held")
		}
		if lifecycleFence.HolderOperationPlan != nil {
			if lifecycleFence.HolderOperationPlan.Action == RecoveryActionManualIntervention {
				return inspection.withPlan(*lifecycleFence.HolderOperationPlan, lifecycleFence.HolderOperationPlan.Reason)
			}
		}
		if len(inspection.WriterSessionFences) > 0 {
			return inspection.withPlan(manual("writer_session_fence_held_during_lifecycle"), "writer_session_fence_held_during_lifecycle")
		}
		if lifecycleFence.HolderOperationPlan != nil {
			if lifecycleFence.HolderOperationPlan.Action == RecoveryActionReclaim {
				return inspection.withPlan(*lifecycleFence.HolderOperationPlan, lifecycleFence.HolderOperationPlan.Reason)
			}
		}
		if lifecycleFence.FencePlan.Action == RecoveryActionRecover {
			return inspection.withPlan(lifecycleFence.FencePlan, lifecycleFence.FencePlan.Reason)
		}
		if lifecycleFence.HolderOperationPlan != nil {
			if lifecycleFence.HolderOperationPlan.Action != RecoveryActionNoop {
				return inspection.withPlan(*lifecycleFence.HolderOperationPlan, lifecycleFence.HolderOperationPlan.Reason)
			}
		}
		return inspection.withPlan(lifecycleFence.FencePlan, lifecycleFence.FencePlan.Reason)
	}

	if len(sortedFences) == 0 {
		return inspection.withPlan(plan(RecoveryActionNoop, "stable_repo_no_held_fence"), "stable_repo_no_held_fence")
	}

	if len(lifecycleFences) > 0 {
		return inspection.withPlan(manual("stable_repo_has_lifecycle_fence"), "stable_repo_has_lifecycle_fence")
	}

	best := plan(RecoveryActionNoop, "held_fences_inspected")
	for _, fenceInspection := range append(lifecycleFences, inspection.WriterSessionFences...) {
		if fenceInspection.HolderOperationPlan != nil && fenceInspection.HolderOperationPlan.Action == RecoveryActionNoop {
			return inspection.withPlan(manual("holder_operation_terminal_while_fence_held"), "holder_operation_terminal_while_fence_held")
		}
		candidate := fenceInspection.FencePlan
		if fenceInspection.HolderOperationPlan != nil {
			switch fenceInspection.HolderOperationPlan.Action {
			case RecoveryActionManualIntervention, RecoveryActionReclaim:
				candidate = *fenceInspection.HolderOperationPlan
			}
		}
		if fenceInspection.Kind == fences.KindWriterSession && candidate.Action == RecoveryActionWait {
			candidate = plan(RecoveryActionWait, "writer_session_fence_active_held")
		}
		best = moreUrgentPlan(best, candidate)
	}
	return inspection.withPlan(best, best.Reason)
}

func inspectHeldFence(fence fences.Fence, repo resources.Repo, operationsByID map[string]operations.OperationRecord, ctx RecoveryContext) (FenceInspection, RecoveryPlan) {
	inspection := FenceInspection{
		FenceID:           fence.ID,
		Kind:              fence.Kind,
		HolderOperationID: fence.HolderOperationID,
		FencePlan:         ClassifyFenceRecovery(fence),
	}
	record, ok := operationsByID[fence.HolderOperationID]
	if !ok {
		return inspection, manual("missing_holder_operation_record")
	}
	if record.ID != fence.HolderOperationID {
		return inspection, manual("holder_operation_id_mismatch")
	}
	if strings.TrimSpace(record.RepoID) == "" {
		return inspection, manual("holder_operation_missing_repo_id")
	}
	if record.RepoID != repo.ID {
		return inspection, manual("holder_operation_repo_mismatch")
	}
	if strings.TrimSpace(record.NamespaceID) == "" {
		return inspection, manual("holder_operation_missing_namespace_id")
	}
	if record.NamespaceID != repo.NamespaceID {
		return inspection, manual("holder_operation_namespace_mismatch")
	}
	if fence.Kind == fences.KindWriterSession && record.Type != operations.OperationRestore {
		return inspection, manual("writer_session_operation_type_mismatch")
	}

	holderPlan := ClassifyOperationRecovery(record, ctx)
	inspection.HolderOperationType = record.Type
	inspection.HolderOperationPlan = &holderPlan
	return inspection, plan(RecoveryActionNoop, "fence_inspected")
}

func (inspection RepoRecoveryInspection) withPlan(plan RecoveryPlan, findingCode string) RepoRecoveryInspection {
	inspection.Plan = plan
	inspection.Findings = append(inspection.Findings, RepoRecoveryFinding{
		Code:   findingCode,
		Action: plan.Action,
		Reason: plan.Reason,
	})
	return inspection
}

func notInspectableSurface() InspectionSurface {
	return InspectionSurface{
		Status: InspectionSurfaceNotImplemented,
		Reason: "not inspectable by repo recovery inspection",
	}
}

func isLifecycleInProgress(status resources.RepoStatus) bool {
	switch status {
	case resources.RepoStatusArchiving,
		resources.RepoStatusRestoringArchived,
		resources.RepoStatusDeleting,
		resources.RepoStatusRestoringTombstoned,
		resources.RepoStatusPurging:
		return true
	default:
		return false
	}
}

func isTerminalRepoStatus(status resources.RepoStatus) bool {
	switch status {
	case resources.RepoStatusArchived, resources.RepoStatusTombstoned, resources.RepoStatusPurged:
		return true
	default:
		return false
	}
}

func lifecycleOperationTypeMatchesStatus(status resources.RepoStatus, typ operations.OperationType) bool {
	expected, ok := lifecycleOperationTypeForStatus(status)
	return ok && typ == expected
}

func lifecycleOperationTypeForStatus(status resources.RepoStatus) (operations.OperationType, bool) {
	switch status {
	case resources.RepoStatusArchiving:
		return operations.OperationRepoArchive, true
	case resources.RepoStatusRestoringArchived:
		return operations.OperationRepoRestoreArchived, true
	case resources.RepoStatusDeleting:
		return operations.OperationRepoDelete, true
	case resources.RepoStatusRestoringTombstoned:
		return operations.OperationRepoRestoreTombstoned, true
	case resources.RepoStatusPurging:
		return operations.OperationRepoPurge, true
	default:
		return "", false
	}
}

func moreUrgentPlan(current, candidate RecoveryPlan) RecoveryPlan {
	if recoveryUrgency(candidate.Action) > recoveryUrgency(current.Action) {
		return candidate
	}
	return current
}

func recoveryUrgency(action RecoveryAction) int {
	switch action {
	case RecoveryActionManualIntervention:
		return 100
	case RecoveryActionRecover:
		return 80
	case RecoveryActionReclaim:
		return 70
	case RecoveryActionWait:
		return 60
	case RecoveryActionFinalizeCancellation:
		return 50
	case RecoveryActionRetry, RecoveryActionClaimable:
		return 40
	case RecoveryActionDeliver:
		return 30
	case RecoveryActionNoop:
		return 0
	default:
		return 90
	}
}
