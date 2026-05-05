package repoaccess

import (
	"errors"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

type Intent string

const (
	IntentStorageSession             Intent = "storage_session"
	IntentStorageMutation            Intent = "storage_mutation"
	IntentExportCreate               Intent = "export_create"
	IntentWorkloadMount              Intent = "workload_mount"
	IntentSavePointCreate            Intent = "save_point_create"
	IntentRestoreRun                 Intent = "restore_run"
	IntentTemplateCreateFromRepo     Intent = "template_create_from_repo"
	IntentTemplateCloneIntoRepo      Intent = "template_clone_into_repo"
	IntentLifecycleArchive           Intent = "lifecycle_archive"
	IntentLifecycleRestoreArchived   Intent = "lifecycle_restore_archived"
	IntentLifecycleDelete            Intent = "lifecycle_delete"
	IntentLifecycleRestoreTombstoned Intent = "lifecycle_restore_tombstoned"
	IntentLifecyclePurge             Intent = "lifecycle_purge"
)

type Mode string

const (
	ModeReadOnly  Mode = "read_only"
	ModeReadWrite Mode = "read_write"
)

type Action string

const (
	ActionAllow Action = "allow"
	ActionDeny  Action = "deny"
)

type ErrorFamily string

const (
	ErrorFamilyInternalError             ErrorFamily = "INTERNAL_ERROR"
	ErrorFamilyNamespaceDisabled         ErrorFamily = "NAMESPACE_DISABLED"
	ErrorFamilyRepoArchived              ErrorFamily = "REPO_ARCHIVED"
	ErrorFamilyRepoTombstoned            ErrorFamily = "REPO_TOMBSTONED"
	ErrorFamilyRepoPurged                ErrorFamily = "REPO_PURGED"
	ErrorFamilyRepoLifecycleInvalidState ErrorFamily = "REPO_LIFECYCLE_INVALID_STATE"
	ErrorFamilyRepoLifecycleFenceHeld    ErrorFamily = "REPO_LIFECYCLE_FENCE_HELD"
	ErrorFamilyWriterSessionFenceHeld    ErrorFamily = "WRITER_SESSION_FENCE_HELD"
	ErrorFamilyOperationRecoveryRequired ErrorFamily = "OPERATION_RECOVERY_REQUIRED"
)

func (family ErrorFamily) String() string {
	return string(family)
}

type Request struct {
	Repo           resources.Repo
	Namespace      resources.Namespace
	Binding        resources.NamespaceVolumeBinding
	HeldRepoFences []fences.Fence
	Intent         Intent
	Mode           Mode
}

type Decision struct {
	Allowed           bool
	Action            Action
	ErrorFamily       ErrorFamily
	Reason            string
	BlockingFenceKind string
}

func Admit(request Request) Decision {
	if !request.Intent.valid() || !request.Mode.valid() {
		return deny(ErrorFamilyInternalError, "invalid repo access request", "")
	}
	if err := validateStoredState(request); err != nil {
		return deny(ErrorFamilyInternalError, "invalid stored control-plane state", "")
	}
	if request.Namespace.ID != request.Repo.NamespaceID || request.Binding.NamespaceID != request.Repo.NamespaceID {
		return deny(ErrorFamilyInternalError, "invalid stored control-plane state", "")
	}
	if request.Repo.Kind != resources.RepoKindRepo {
		return deny(ErrorFamilyInternalError, "invalid stored control-plane state", "")
	}

	if request.Namespace.Status != resources.NamespaceStatusActive || request.Binding.Status != resources.NamespaceStatusActive {
		return deny(ErrorFamilyNamespaceDisabled, "namespace or namespace binding is not active", "")
	}

	if decision, blocked := denyForHeldLifecycleFence(request.HeldRepoFences); blocked {
		return decision
	}

	if decision, blocked := denyForWriterFence(request); blocked {
		return decision
	}

	if isLifecycleIntent(request.Intent) {
		return admitLifecycleIntent(request)
	}

	if request.Repo.Status != resources.RepoStatusActive {
		return denyForRepoStatus(request.Repo.Status)
	}

	return allow()
}

func validateStoredState(request Request) error {
	if err := request.Repo.Validate(); err != nil {
		return err
	}
	if err := request.Namespace.Validate(); err != nil {
		return err
	}
	if err := request.Binding.Validate(); err != nil {
		return err
	}
	for _, fence := range request.HeldRepoFences {
		if err := fences.ValidateFence(fence); err != nil {
			return err
		}
		if fence.RepoID != request.Repo.ID {
			return errInvalidStoredState
		}
	}
	return nil
}

var errInvalidStoredState = errors.New("invalid stored state")

func denyForHeldLifecycleFence(existing []fences.Fence) (Decision, bool) {
	for _, fence := range existing {
		if !fence.Held() {
			continue
		}
		if fence.Kind == fences.KindLifecycle {
			if fence.Status == fences.StatusExpired || fence.Status == fences.StatusRecoveryRequired {
				return deny(ErrorFamilyOperationRecoveryRequired, "held lifecycle fence requires recovery", fence.Kind.String()), true
			}
			return deny(ErrorFamilyRepoLifecycleFenceHeld, "held lifecycle fence blocks repo access", fence.Kind.String()), true
		}
	}
	return Decision{}, false
}

func denyForWriterFence(request Request) (Decision, bool) {
	if !writerFenceBlocks(request.Intent, request.Mode) {
		return Decision{}, false
	}
	for _, fence := range request.HeldRepoFences {
		if !fence.Held() || fence.Kind != fences.KindWriterSession {
			continue
		}
		if fence.Status == fences.StatusExpired || fence.Status == fences.StatusRecoveryRequired {
			return deny(ErrorFamilyOperationRecoveryRequired, "held writer-session fence requires recovery", fence.Kind.String()), true
		}
		return deny(ErrorFamilyWriterSessionFenceHeld, "held writer-session fence blocks writer access", fence.Kind.String()), true
	}
	return Decision{}, false
}

func (intent Intent) valid() bool {
	switch intent {
	case IntentStorageSession,
		IntentStorageMutation,
		IntentExportCreate,
		IntentWorkloadMount,
		IntentSavePointCreate,
		IntentRestoreRun,
		IntentTemplateCreateFromRepo,
		IntentTemplateCloneIntoRepo,
		IntentLifecycleArchive,
		IntentLifecycleRestoreArchived,
		IntentLifecycleDelete,
		IntentLifecycleRestoreTombstoned,
		IntentLifecyclePurge:
		return true
	default:
		return false
	}
}

func (mode Mode) valid() bool {
	switch mode {
	case ModeReadOnly, ModeReadWrite:
		return true
	default:
		return false
	}
}

func writerFenceBlocks(intent Intent, mode Mode) bool {
	switch intent {
	case IntentExportCreate, IntentWorkloadMount, IntentStorageSession:
		return mode == ModeReadWrite
	case IntentStorageMutation, IntentRestoreRun:
		return true
	default:
		return isLifecycleIntent(intent)
	}
}

func admitLifecycleIntent(request Request) Decision {
	status := request.Repo.Status
	allowed := false
	switch request.Intent {
	case IntentLifecycleArchive:
		allowed = status == resources.RepoStatusActive
	case IntentLifecycleRestoreArchived:
		allowed = status == resources.RepoStatusArchived
	case IntentLifecycleDelete:
		allowed = status == resources.RepoStatusActive || status == resources.RepoStatusArchived
	case IntentLifecycleRestoreTombstoned, IntentLifecyclePurge:
		allowed = status == resources.RepoStatusTombstoned
	default:
		return deny(ErrorFamilyInternalError, "unknown repo access intent", "")
	}
	if allowed {
		return allow()
	}
	if isTransitionalStatus(status) {
		return deny(ErrorFamilyOperationRecoveryRequired, "repo lifecycle transition requires recovery", "")
	}
	return deny(ErrorFamilyRepoLifecycleInvalidState, "repo lifecycle source status is not allowed for intent", "")
}

func denyForRepoStatus(status resources.RepoStatus) Decision {
	switch status {
	case resources.RepoStatusArchived:
		return deny(ErrorFamilyRepoArchived, "repo is archived", "")
	case resources.RepoStatusTombstoned:
		return deny(ErrorFamilyRepoTombstoned, "repo is tombstoned", "")
	case resources.RepoStatusPurged:
		return deny(ErrorFamilyRepoPurged, "repo is purged", "")
	default:
		return deny(ErrorFamilyOperationRecoveryRequired, "repo lifecycle transition requires recovery", "")
	}
}

func isLifecycleIntent(intent Intent) bool {
	switch intent {
	case IntentLifecycleArchive,
		IntentLifecycleRestoreArchived,
		IntentLifecycleDelete,
		IntentLifecycleRestoreTombstoned,
		IntentLifecyclePurge:
		return true
	default:
		return false
	}
}

func isTransitionalStatus(status resources.RepoStatus) bool {
	switch status {
	case resources.RepoStatusArchiving,
		resources.RepoStatusRestoringArchived,
		resources.RepoStatusDeleting,
		resources.RepoStatusRestoringTombstoned,
		resources.RepoStatusPurging,
		resources.RepoStatusOperatorInterventionRequired:
		return true
	default:
		return false
	}
}

func allow() Decision {
	return Decision{Allowed: true, Action: ActionAllow}
}

func deny(family ErrorFamily, reason string, blockingFenceKind string) Decision {
	return Decision{
		Allowed:           false,
		Action:            ActionDeny,
		ErrorFamily:       family,
		Reason:            reason,
		BlockingFenceKind: blockingFenceKind,
	}
}
