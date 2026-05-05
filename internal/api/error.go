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
	CodeResourceNamespaceMismatch ErrorCode = "RESOURCE_NAMESPACE_MISMATCH"
	CodeCapabilityDenied          ErrorCode = "CAPABILITY_DENIED"
	CodePathDenied                ErrorCode = "PATH_DENIED"
)

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
