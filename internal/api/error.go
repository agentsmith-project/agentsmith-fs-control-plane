package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

const (
	HeaderCorrelationID = "X-Correlation-Id"

	redactedDetailValue = "[REDACTED]"
)

type ErrorCode string

const (
	CodeAuthenticationFailed      ErrorCode = "AUTHENTICATION_FAILED"
	CodeCallerNotAllowed          ErrorCode = "CALLER_NOT_ALLOWED"
	CodeRoleNotAllowed            ErrorCode = "ROLE_NOT_ALLOWED"
	CodeNamespaceNotFound         ErrorCode = "NAMESPACE_NOT_FOUND"
	CodeNamespaceDisabled         ErrorCode = "NAMESPACE_DISABLED"
	CodeResourceNamespaceMismatch ErrorCode = "RESOURCE_NAMESPACE_MISMATCH"
	CodeInvalidID                 ErrorCode = "INVALID_ID"
	CodePathDenied                ErrorCode = "PATH_DENIED"
	CodeCapabilityDenied          ErrorCode = "CAPABILITY_DENIED"
	CodeIdempotencyConflict       ErrorCode = "IDEMPOTENCY_CONFLICT"
	CodeRepoAlreadyExists         ErrorCode = "REPO_ALREADY_EXISTS"
	CodeRepoNotFound              ErrorCode = "REPO_NOT_FOUND"
	CodeVolumeNotFound            ErrorCode = "VOLUME_NOT_FOUND"
	CodeOperationNotFound         ErrorCode = "OPERATION_NOT_FOUND"
	CodeStorageUnavailable        ErrorCode = "STORAGE_UNAVAILABLE"
	CodeInternalError             ErrorCode = "INTERNAL_ERROR"
	CodeRepoJVSMutationInProgress ErrorCode = "REPO_JVS_MUTATION_IN_PROGRESS"

	CodeActiveWriterSessions        ErrorCode = "ACTIVE_WRITER_SESSIONS"
	CodeWriterSessionFenceHeld      ErrorCode = "WRITER_SESSION_FENCE_HELD"
	CodeStaleWriterSessionUncertain ErrorCode = "STALE_WRITER_SESSION_UNCERTAIN"
	CodeRestoreDirtyState           ErrorCode = "RESTORE_DIRTY_STATE"

	CodeJVSCommandFailed              ErrorCode = "JVS_COMMAND_FAILED"
	CodeJVSDoctorFailed               ErrorCode = "JVS_DOCTOR_FAILED"
	CodeSourceDirtyAfterTemplateSave  ErrorCode = "SOURCE_DIRTY_AFTER_TEMPLATE_SAVE"
	CodeVolumeMismatchRequiresImport  ErrorCode = "VOLUME_MISMATCH_REQUIRES_IMPORT"
	CodeExportExpired                 ErrorCode = "EXPORT_EXPIRED"
	CodeExportRevoked                 ErrorCode = "EXPORT_REVOKED"
	CodeMountBindingTerminal          ErrorCode = "MOUNT_BINDING_TERMINAL"
	CodeRepoLifecycleInvalidState     ErrorCode = "REPO_LIFECYCLE_INVALID_STATE"
	CodeRepoLifecycleFenceHeld        ErrorCode = "REPO_LIFECYCLE_FENCE_HELD"
	CodeActiveSessionsBlockLifecycle  ErrorCode = "ACTIVE_SESSIONS_BLOCK_LIFECYCLE"
	CodeStaleSessionBlocksLifecycle   ErrorCode = "STALE_SESSION_BLOCKS_LIFECYCLE"
	CodeRepoArchived                  ErrorCode = "REPO_ARCHIVED"
	CodeRepoTombstoned                ErrorCode = "REPO_TOMBSTONED"
	CodeRepoPurged                    ErrorCode = "REPO_PURGED"
	CodePurgeConfirmationRequired     ErrorCode = "PURGE_CONFIRMATION_REQUIRED"
	CodePurgeRetentionNotMet          ErrorCode = "PURGE_RETENTION_NOT_MET"
	CodePurgeRequiresOperatorApproval ErrorCode = "PURGE_REQUIRES_OPERATOR_APPROVAL"
	CodeOperationRecoveryRequired     ErrorCode = "OPERATION_RECOVERY_REQUIRED"
)

var allErrorCodes = []ErrorCode{
	CodeAuthenticationFailed,
	CodeCallerNotAllowed,
	CodeRoleNotAllowed,
	CodeNamespaceNotFound,
	CodeNamespaceDisabled,
	CodeResourceNamespaceMismatch,
	CodeInvalidID,
	CodePathDenied,
	CodeCapabilityDenied,
	CodeIdempotencyConflict,
	CodeRepoAlreadyExists,
	CodeRepoNotFound,
	CodeVolumeNotFound,
	CodeOperationNotFound,
	CodeStorageUnavailable,
	CodeInternalError,
	CodeRepoJVSMutationInProgress,
	CodeActiveWriterSessions,
	CodeWriterSessionFenceHeld,
	CodeStaleWriterSessionUncertain,
	CodeRestoreDirtyState,
	CodeJVSCommandFailed,
	CodeJVSDoctorFailed,
	CodeSourceDirtyAfterTemplateSave,
	CodeVolumeMismatchRequiresImport,
	CodeExportExpired,
	CodeExportRevoked,
	CodeMountBindingTerminal,
	CodeRepoLifecycleInvalidState,
	CodeRepoLifecycleFenceHeld,
	CodeActiveSessionsBlockLifecycle,
	CodeStaleSessionBlocksLifecycle,
	CodeRepoArchived,
	CodeRepoTombstoned,
	CodeRepoPurged,
	CodePurgeConfirmationRequired,
	CodePurgeRetentionNotMet,
	CodePurgeRequiresOperatorApproval,
	CodeOperationRecoveryRequired,
}

func ErrorCodes() []ErrorCode {
	codes := make([]ErrorCode, len(allErrorCodes))
	copy(codes, allErrorCodes)
	return codes
}

type ErrorEnvelope struct {
	Error StandardError `json:"error"`
}

type StandardError struct {
	Code          ErrorCode      `json:"code"`
	Message       string         `json:"message"`
	Retryable     bool           `json:"retryable"`
	CorrelationID string         `json:"correlation_id"`
	OperationID   *string        `json:"operation_id"`
	Details       map[string]any `json:"details"`
}

func NewErrorEnvelope(code ErrorCode, message string, retryable bool, correlationID string, operationID *string, details map[string]any) ErrorEnvelope {
	if strings.TrimSpace(correlationID) == "" {
		correlationID = "missing"
	}

	return ErrorEnvelope{
		Error: StandardError{
			Code:          code,
			Message:       message,
			Retryable:     retryable,
			CorrelationID: correlationID,
			OperationID:   operationID,
			Details:       RedactDetails(details),
		},
	}
}

func MarshalErrorEnvelope(envelope ErrorEnvelope) ([]byte, error) {
	return json.Marshal(envelope)
}

func WriteErrorEnvelope(w http.ResponseWriter, status int, envelope ErrorEnvelope) error {
	body, err := MarshalErrorEnvelope(envelope)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err = w.Write(body)
	return err
}

func CorrelationIDFromRequest(r *http.Request) string {
	if r == nil {
		return "missing"
	}

	correlationID := strings.TrimSpace(r.Header.Get(HeaderCorrelationID))
	if correlationID == "" {
		return "missing"
	}
	return correlationID
}

func RedactDetails(details map[string]any) map[string]any {
	redacted := make(map[string]any)
	for key, value := range details {
		if credentialLikeKey(key) {
			redacted[key] = redactedDetailValue
			continue
		}
		redacted[key] = redactValue(value)
	}
	return redacted
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return RedactDetails(typed)
	case map[string]string:
		redacted := make(map[string]any, len(typed))
		for key, value := range typed {
			if credentialLikeKey(key) || bearerLikeValue(value) {
				redacted[key] = redactedDetailValue
				continue
			}
			redacted[key] = value
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for i, item := range typed {
			redacted[i] = redactValue(item)
		}
		return redacted
	case []map[string]any:
		redacted := make([]any, len(typed))
		for i, item := range typed {
			redacted[i] = RedactDetails(item)
		}
		return redacted
	case []string:
		redacted := make([]string, len(typed))
		for i, item := range typed {
			if bearerLikeValue(item) {
				redacted[i] = redactedDetailValue
				continue
			}
			redacted[i] = item
		}
		return redacted
	case string:
		if bearerLikeValue(typed) {
			return redactedDetailValue
		}
		return typed
	default:
		return value
	}
}

func credentialLikeKey(key string) bool {
	normalized := normalizeSensitiveKey(key)

	for _, marker := range []string{
		"metadataurl",
		"storagebucketurl",
		"objectstoreendpoint",
		"authorization",
		"cookie",
		"apikey",
		"accesskey",
		"secretaccesskey",
		"secretkey",
		"privatekey",
		"password",
		"passwd",
		"secret",
		"secretref",
		"k8ssecret",
		"kubernetessecret",
		"kubernetessecretref",
		"k8ssecretref",
		"kubesecretref",
		"webdavpassword",
		"token",
		"credential",
		"bearer",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func normalizeSensitiveKey(key string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r >= '0' && r <= '9':
			return r
		default:
			return -1
		}
	}, key)
}

func bearerLikeValue(value string) bool {
	fields := strings.Fields(value)
	if len(fields) < 2 {
		return false
	}

	for i := 0; i < len(fields)-1; i++ {
		if strings.EqualFold(strings.TrimSuffix(fields[i], ":"), "bearer") {
			return true
		}
	}
	return false
}
