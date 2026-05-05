package operations

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidLeaseRequest = errors.New("invalid operation lease request")
	ErrInvalidLeaseRecord  = errors.New("invalid operation lease record")
	ErrLeaseUnavailable    = errors.New("operation lease unavailable")
)

type OperationLeaseErrorCode string

const (
	OperationLeaseErrorInvalidRequest    OperationLeaseErrorCode = "OPERATION_LEASE_INVALID_REQUEST"
	OperationLeaseErrorInvalidRecord     OperationLeaseErrorCode = "OPERATION_LEASE_INVALID_RECORD"
	OperationLeaseErrorUnavailable       OperationLeaseErrorCode = "OPERATION_LEASE_UNAVAILABLE"
	OperationLeaseErrorInvalidTransition OperationLeaseErrorCode = "OPERATION_LEASE_INVALID_TRANSITION"
)

type OperationLeaseError struct {
	Code   OperationLeaseErrorCode
	Field  string
	Reason string
	Cause  error
}

func (err *OperationLeaseError) Error() string {
	if err == nil {
		return ""
	}
	if err.Field == "" {
		return fmt.Sprintf("%s: %s", err.Code, err.Reason)
	}
	return fmt.Sprintf("%s: %s: %s", err.Code, err.Field, err.Reason)
}

func (err *OperationLeaseError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

type LeaseRecoveryMode string

const (
	LeaseRecoveryNone           LeaseRecoveryMode = ""
	LeaseRecoveryExplicitAction LeaseRecoveryMode = "explicit_recovery_action"
)

func (mode LeaseRecoveryMode) valid() bool {
	switch mode {
	case LeaseRecoveryNone, LeaseRecoveryExplicitAction:
		return true
	default:
		return false
	}
}

type LeaseCancelPolicy string

const (
	LeaseCancelPolicyNone     LeaseCancelPolicy = ""
	LeaseCancelPolicyFinalize LeaseCancelPolicy = "finalize_cancellation"
)

func (policy LeaseCancelPolicy) valid() bool {
	switch policy {
	case LeaseCancelPolicyNone, LeaseCancelPolicyFinalize:
		return true
	default:
		return false
	}
}

type LeaseAction string

const (
	LeaseActionClaim                LeaseAction = "claim"
	LeaseActionReclaim              LeaseAction = "reclaim"
	LeaseActionRenew                LeaseAction = "renew"
	LeaseActionRecover              LeaseAction = "recover"
	LeaseActionFinalizeCancellation LeaseAction = "finalize_cancellation"
)

type LeaseRequest struct {
	Owner        string
	Duration     time.Duration
	Now          time.Time
	RecoveryMode LeaseRecoveryMode
	CancelPolicy LeaseCancelPolicy
}

type LeaseDecision struct {
	Allowed bool
	Action  LeaseAction
	Record  OperationRecord
	Error   error
}

func AcquireLease(record OperationRecord, request LeaseRequest) LeaseDecision {
	owner, err := validateLeaseRequest(request)
	if err != nil {
		return denyLease(record, err)
	}
	if err := validateLeaseRequestPolicies(request); err != nil {
		return denyLease(record, err)
	}
	if err := validateLeaseRecord(record); err != nil {
		return denyLease(record, err)
	}

	switch record.State {
	case OperationStateQueued:
		return applyLease(record, owner, request.Duration, request.Now, OperationStateRunning, LeaseActionClaim)
	case OperationStateRunning:
		if record.LeaseExpiresAt == nil {
			return denyLease(record, leaseUnavailable("lease_expires_at", "running operation has no lease expiry"))
		}
		if record.LeaseExpiresAt.After(request.Now) {
			return denyLease(record, leaseUnavailable("lease_expires_at", "running operation lease is still live"))
		}
		return applyLease(record, owner, request.Duration, request.Now, OperationStateRunning, LeaseActionReclaim)
	case OperationStateOperatorInterventionRequired:
		if request.RecoveryMode != LeaseRecoveryExplicitAction {
			return denyLease(record, leaseUnavailable("operation_state", "operator intervention requires explicit recovery action"))
		}
		return applyLease(record, owner, request.Duration, request.Now, OperationStateRunning, LeaseActionRecover)
	case OperationStateCancelRequested:
		if request.CancelPolicy != LeaseCancelPolicyFinalize {
			err := ValidateTransition(record.State, OperationStateRunning)
			return denyLease(record, leaseTransition("operation_state", err))
		}
		if record.LeaseExpiresAt != nil && record.LeaseExpiresAt.After(request.Now) {
			return denyLease(record, leaseUnavailable("lease_expires_at", "cancel requested operation lease is still live"))
		}
		return finalizeCancellation(record, request.Now)
	default:
		err := ValidateTransition(record.State, OperationStateRunning)
		return denyLease(record, leaseTransition("operation_state", err))
	}
}

func RenewLease(record OperationRecord, request LeaseRequest) LeaseDecision {
	owner, err := validateLeaseRequest(request)
	if err != nil {
		return denyLease(record, err)
	}
	if err := validateLeaseRequestPolicies(request); err != nil {
		return denyLease(record, err)
	}
	if err := validateLeaseRecord(record); err != nil {
		return denyLease(record, err)
	}
	if record.State != OperationStateRunning {
		err := ValidateTransition(record.State, OperationStateRunning)
		if err == nil {
			err = fmt.Errorf("%w: %q cannot renew a lease", ErrInvalidStateTransition, record.State)
		}
		return denyLease(record, leaseTransition("operation_state", err))
	}
	if record.LeaseOwner != owner {
		return denyLease(record, leaseUnavailable("lease_owner", "lease is owned by another worker"))
	}
	if record.LeaseExpiresAt == nil {
		return denyLease(record, leaseUnavailable("lease_expires_at", "running operation has no lease expiry"))
	}
	if !record.LeaseExpiresAt.After(request.Now) {
		return denyLease(record, leaseUnavailable("lease_expires_at", "lease has expired and must be reclaimed"))
	}

	updated := record
	expiresAt := request.Now.Add(request.Duration)
	if expiresAt.Before(*record.LeaseExpiresAt) {
		expiresAt = *record.LeaseExpiresAt
	}
	updated.LeaseExpiresAt = &expiresAt
	return LeaseDecision{
		Allowed: true,
		Action:  LeaseActionRenew,
		Record:  updated,
	}
}

func validateLeaseRequest(request LeaseRequest) (string, error) {
	owner := strings.TrimSpace(request.Owner)
	if owner == "" {
		return "", leaseInvalid("owner", "missing lease owner")
	}
	if request.Duration <= 0 {
		return "", leaseInvalid("duration", "lease duration must be positive")
	}
	if request.Now.IsZero() {
		return "", leaseInvalid("now", "lease decision time must be set")
	}
	return owner, nil
}

func validateLeaseRequestPolicies(request LeaseRequest) error {
	if !request.RecoveryMode.valid() {
		return leaseInvalid("recovery_mode", fmt.Sprintf("unknown recovery mode %q", request.RecoveryMode))
	}
	if !request.CancelPolicy.valid() {
		return leaseInvalid("cancel_policy", fmt.Sprintf("unknown cancel policy %q", request.CancelPolicy))
	}
	return nil
}

func validateLeaseRecord(record OperationRecord) error {
	if record.Attempt < 0 {
		return leaseInvalidRecord("attempt", "attempt cannot be negative")
	}
	if err := validateLeasePair(record); err != nil {
		return err
	}
	if !record.State.Valid() {
		err := ValidateTransition(record.State, OperationStateRunning)
		return leaseTransition("operation_state", err)
	}
	if record.State == OperationStateRunning && record.LeaseExpiresAt == nil {
		return leaseInvalidRecord("lease_expires_at", "running operation must carry a complete lease")
	}
	if record.State == OperationStateQueued && record.LeaseExpiresAt != nil {
		return leaseInvalidRecord("lease_expires_at", "queued operation must not carry a stale lease")
	}
	if record.State.IsTerminal() {
		err := ValidateTransition(record.State, OperationStateRunning)
		return leaseTransition("operation_state", err)
	}
	return nil
}

func validateLeasePair(record OperationRecord) error {
	owner := strings.TrimSpace(record.LeaseOwner)
	hasOwner := owner != ""
	hasExpiry := record.LeaseExpiresAt != nil

	if record.LeaseOwner != "" && !hasOwner {
		return leaseInvalidRecord("lease_owner", "lease owner cannot be blank when lease field is present")
	}
	if hasOwner != hasExpiry {
		return leaseInvalidRecord("lease_owner", "lease owner and lease expiry must both be set or both be empty")
	}
	return nil
}

func applyLease(record OperationRecord, owner string, duration time.Duration, now time.Time, state OperationState, action LeaseAction) LeaseDecision {
	if err := ValidateTransition(record.State, state); err != nil {
		return denyLease(record, leaseTransition("operation_state", err))
	}

	updated := record
	expiresAt := now.Add(duration)
	updated.State = state
	updated.LeaseOwner = owner
	updated.LeaseExpiresAt = &expiresAt
	updated.Attempt++
	if updated.StartedAt == nil {
		startedAt := now
		updated.StartedAt = &startedAt
	}

	return LeaseDecision{
		Allowed: true,
		Action:  action,
		Record:  updated,
	}
}

func finalizeCancellation(record OperationRecord, now time.Time) LeaseDecision {
	if err := ValidateTransition(record.State, OperationStateCancelled); err != nil {
		return denyLease(record, leaseTransition("operation_state", err))
	}

	updated := record
	updated.State = OperationStateCancelled
	updated.LeaseOwner = ""
	updated.LeaseExpiresAt = nil
	if updated.FinishedAt == nil {
		finishedAt := now
		updated.FinishedAt = &finishedAt
	}

	return LeaseDecision{
		Allowed: true,
		Action:  LeaseActionFinalizeCancellation,
		Record:  updated,
	}
}

func denyLease(record OperationRecord, err error) LeaseDecision {
	return LeaseDecision{
		Allowed: false,
		Record:  record,
		Error:   err,
	}
}

func leaseInvalid(field, reason string) *OperationLeaseError {
	return &OperationLeaseError{
		Code:   OperationLeaseErrorInvalidRequest,
		Field:  field,
		Reason: reason,
		Cause:  ErrInvalidLeaseRequest,
	}
}

func leaseInvalidRecord(field, reason string) *OperationLeaseError {
	return &OperationLeaseError{
		Code:   OperationLeaseErrorInvalidRecord,
		Field:  field,
		Reason: reason,
		Cause:  ErrInvalidLeaseRecord,
	}
}

func leaseUnavailable(field, reason string) *OperationLeaseError {
	return &OperationLeaseError{
		Code:   OperationLeaseErrorUnavailable,
		Field:  field,
		Reason: reason,
		Cause:  ErrLeaseUnavailable,
	}
}

func leaseTransition(field string, cause error) *OperationLeaseError {
	if cause == nil {
		cause = ErrInvalidStateTransition
	}
	return &OperationLeaseError{
		Code:   OperationLeaseErrorInvalidTransition,
		Field:  field,
		Reason: cause.Error(),
		Cause:  cause,
	}
}
