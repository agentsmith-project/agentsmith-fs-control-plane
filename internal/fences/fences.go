package fences

import (
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
)

type Kind string

const (
	KindWriterSession Kind = "writer_session"
	KindLifecycle     Kind = "lifecycle"
)

func (kind Kind) String() string {
	return string(kind)
}

func (kind Kind) Valid() bool {
	switch kind {
	case KindWriterSession, KindLifecycle:
		return true
	default:
		return false
	}
}

type Status string

const (
	StatusActive           Status = "active"
	StatusExpired          Status = "expired"
	StatusRecoveryRequired Status = "recovery_required"
	StatusReleased         Status = "released"
	StatusRecovered        Status = "recovered"
)

func (status Status) String() string {
	return string(status)
}

func (status Status) Valid() bool {
	switch status {
	case StatusActive, StatusExpired, StatusRecoveryRequired, StatusReleased, StatusRecovered:
		return true
	default:
		return false
	}
}

type ErrorFamily string

const (
	ErrorFamilyInvalidID                 ErrorFamily = "INVALID_ID"
	ErrorFamilyWriterSessionFenceHeld    ErrorFamily = "WRITER_SESSION_FENCE_HELD"
	ErrorFamilyRepoLifecycleFenceHeld    ErrorFamily = "REPO_LIFECYCLE_FENCE_HELD"
	ErrorFamilyOperationRecoveryRequired ErrorFamily = "OPERATION_RECOVERY_REQUIRED"
)

type FenceError struct {
	Family ErrorFamily
	Field  string
	Reason string
}

func (err *FenceError) Error() string {
	if err == nil {
		return ""
	}
	if err.Field == "" {
		return fmt.Sprintf("%s: %s", err.Family, err.Reason)
	}
	return fmt.Sprintf("%s: %s: %s", err.Family, err.Field, err.Reason)
}

type Fence struct {
	ID                  string
	RepoID              string
	Kind                Kind
	HolderOperationID   string
	Status              Status
	ExpiresAt           time.Time
	ReleasedAt          *time.Time
	RecoveryOperationID string
	RecoveryReason      string
	RecoveryStartedAt   *time.Time
	RecoveredAt         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Held mirrors the durable uniqueness contract: a fence is held until it has
// been explicitly released or completed recovery, regardless of active expiry.
func (fence Fence) Held() bool {
	return fence.ReleasedAt == nil && fence.RecoveredAt == nil
}

func ValidateFence(fence Fence) *FenceError {
	if strings.TrimSpace(fence.ID) == "" {
		return invalid("id", "missing fence id")
	}
	if err := pathresolver.ValidateID(pathresolver.RepoID, fence.RepoID); err != nil {
		return invalid("repo_id", err.Error())
	}
	if !fence.Kind.Valid() {
		return invalid("kind", fmt.Sprintf("unknown fence kind %q", fence.Kind))
	}
	if err := pathresolver.ValidateID(pathresolver.OperationID, fence.HolderOperationID); err != nil {
		return invalid("holder_operation_id", err.Error())
	}
	if !fence.Status.Valid() {
		return invalid("status", fmt.Sprintf("unknown fence status %q", fence.Status))
	}
	if fence.ExpiresAt.IsZero() {
		return invalid("expires_at", "missing fence expiration")
	}
	if fence.CreatedAt.IsZero() {
		return invalid("created_at", "missing fence creation time")
	}
	if fence.UpdatedAt.IsZero() {
		return invalid("updated_at", "missing fence update time")
	}
	if fence.RecoveryOperationID != "" {
		if err := pathresolver.ValidateID(pathresolver.OperationID, fence.RecoveryOperationID); err != nil {
			return invalid("recovery_operation_id", err.Error())
		}
	}

	switch fence.Status {
	case StatusActive, StatusExpired, StatusRecoveryRequired:
		if fence.ReleasedAt != nil {
			return invalid("released_at", fmt.Sprintf("%s fence must not have released_at", fence.Status))
		}
		if fence.RecoveredAt != nil {
			return invalid("recovered_at", fmt.Sprintf("%s fence must not have recovered_at", fence.Status))
		}
	case StatusReleased:
		if fence.ReleasedAt == nil {
			return invalid("released_at", "released fence must have released_at")
		}
		if fence.RecoveredAt != nil {
			return invalid("recovered_at", "released fence must not have recovered_at")
		}
	case StatusRecovered:
		if fence.ReleasedAt == nil {
			return invalid("released_at", "recovered fence must have released_at")
		}
		if fence.RecoveredAt == nil {
			return invalid("recovered_at", "recovered fence must have recovered_at")
		}
		if fence.RecoveryStartedAt == nil {
			return invalid("recovery_started_at", "recovered fence must have recovery_started_at")
		}
	}

	return nil
}

type ConflictMode string

const (
	ConflictModeFailClosed    ConflictMode = ""
	ConflictModeWaitOrRecover ConflictMode = "wait_or_recover"
)

func (mode ConflictMode) valid() bool {
	switch mode {
	case ConflictModeFailClosed, ConflictModeWaitOrRecover:
		return true
	default:
		return false
	}
}

type AcquisitionRequest struct {
	RepoID            string
	Kind              Kind
	HolderOperationID string
	ConflictMode      ConflictMode
}

type AcquisitionAction string

const (
	ActionAcquire       AcquisitionAction = "acquire"
	ActionDeny          AcquisitionAction = "deny"
	ActionWaitOrRecover AcquisitionAction = "wait_or_recover"
)

type AcquisitionDecision struct {
	Allowed       bool
	Action        AcquisitionAction
	BlockingFence *Fence
	Error         *FenceError
}

func CanAcquire(request AcquisitionRequest, existing []Fence) AcquisitionDecision {
	if err := validateRequest(request); err != nil {
		return deny(nil, err)
	}

	for _, fence := range existing {
		if err := validateFenceRepoID(fence); err != nil {
			return deny(&fence, &FenceError{
				Family: ErrorFamilyOperationRecoveryRequired,
				Field:  err.Field,
				Reason: "invalid existing fence state: " + err.Reason,
			})
		}
		if fence.RepoID != request.RepoID {
			continue
		}
		if err := ValidateFence(fence); err != nil {
			return deny(&fence, &FenceError{
				Family: ErrorFamilyOperationRecoveryRequired,
				Field:  err.Field,
				Reason: "invalid existing fence state: " + err.Reason,
			})
		}
		if !fence.Held() {
			continue
		}

		if fence.Status == StatusExpired || fence.Status == StatusRecoveryRequired {
			return deny(&fence, &FenceError{
				Family: ErrorFamilyOperationRecoveryRequired,
				Field:  "status",
				Reason: fmt.Sprintf("held %s fence is %s", fence.Kind, fence.Status),
			})
		}

		switch fence.Kind {
		case KindLifecycle:
			return deny(&fence, &FenceError{
				Family: ErrorFamilyRepoLifecycleFenceHeld,
				Field:  "fence_kind",
				Reason: "held lifecycle fence blocks repo acquisition",
			})
		case KindWriterSession:
			if request.Kind == KindLifecycle && request.ConflictMode == ConflictModeWaitOrRecover {
				return AcquisitionDecision{
					Allowed:       false,
					Action:        ActionWaitOrRecover,
					BlockingFence: copyFence(fence),
				}
			}
			return deny(&fence, &FenceError{
				Family: ErrorFamilyWriterSessionFenceHeld,
				Field:  "fence_kind",
				Reason: "held writer-session fence blocks repo acquisition",
			})
		default:
			return deny(&fence, &FenceError{
				Family: ErrorFamilyOperationRecoveryRequired,
				Field:  "fence_kind",
				Reason: fmt.Sprintf("unknown held fence kind %q", fence.Kind),
			})
		}
	}

	return AcquisitionDecision{
		Allowed: true,
		Action:  ActionAcquire,
	}
}

func validateRequest(request AcquisitionRequest) *FenceError {
	if err := pathresolver.ValidateID(pathresolver.RepoID, request.RepoID); err != nil {
		return invalid("repo_id", err.Error())
	}
	if !request.Kind.Valid() {
		return invalid("kind", fmt.Sprintf("unknown fence kind %q", request.Kind))
	}
	if err := pathresolver.ValidateID(pathresolver.OperationID, request.HolderOperationID); err != nil {
		return invalid("holder_operation_id", err.Error())
	}
	if !request.ConflictMode.valid() {
		return invalid("conflict_mode", fmt.Sprintf("unknown conflict mode %q", request.ConflictMode))
	}
	return nil
}

func validateFenceRepoID(fence Fence) *FenceError {
	if err := pathresolver.ValidateID(pathresolver.RepoID, fence.RepoID); err != nil {
		return invalid("repo_id", err.Error())
	}
	return nil
}

func deny(fence *Fence, err *FenceError) AcquisitionDecision {
	return AcquisitionDecision{
		Allowed:       false,
		Action:        ActionDeny,
		BlockingFence: copyFenceFromPointer(fence),
		Error:         err,
	}
}

func invalid(field, reason string) *FenceError {
	return &FenceError{
		Family: ErrorFamilyInvalidID,
		Field:  field,
		Reason: reason,
	}
}

func copyFenceFromPointer(fence *Fence) *Fence {
	if fence == nil {
		return nil
	}
	return copyFence(*fence)
}

func copyFence(fence Fence) *Fence {
	copied := fence
	return &copied
}
