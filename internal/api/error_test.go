package api

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
)

func TestErrorCodesExposeStableSchemaEnumOrder(t *testing.T) {
	want := []ErrorCode{
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
		CodeFileLibraryOperationPending,
		CodeFileLibraryOperationRequiresRecovery,
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
		CodeRestoreReconciliationActive,
	}

	got := ErrorCodes()
	if !slices.Equal(got, want) {
		t.Fatalf("ErrorCodes() = %#v, want %#v", got, want)
	}

	got[0] = CodeCapabilityDenied
	if ErrorCodes()[0] != CodeAuthenticationFailed {
		t.Fatal("ErrorCodes returned mutable backing storage")
	}
}

func TestFenceErrorFamiliesMapToStableAPICodes(t *testing.T) {
	families := map[fences.ErrorFamily]ErrorCode{
		fences.ErrorFamilyInvalidID:                 CodeInvalidID,
		fences.ErrorFamilyWriterSessionFenceHeld:    CodeWriterSessionFenceHeld,
		fences.ErrorFamilyRepoLifecycleFenceHeld:    CodeRepoLifecycleFenceHeld,
		fences.ErrorFamilyOperationRecoveryRequired: CodeOperationRecoveryRequired,
	}
	for family, code := range families {
		if string(family) != string(code) {
			t.Fatalf("family %q does not map to API code %q", family, code)
		}
	}
}

func TestErrorEnvelopeJSONStableAndComplete(t *testing.T) {
	operationID := "op_123"
	env := NewErrorEnvelope(
		CodeCapabilityDenied,
		"storage-backed capabilities are disabled",
		false,
		"corr_123",
		&operationID,
		map[string]any{"repo_id": "repo_123"},
	)

	got, err := MarshalErrorEnvelope(env)
	if err != nil {
		t.Fatalf("MarshalErrorEnvelope returned error: %v", err)
	}

	want := `{"error":{"code":"CAPABILITY_DENIED","message":"storage-backed capabilities are disabled","retryable":false,"correlation_id":"corr_123","operation_id":"op_123","details":{"repo_id":"repo_123"}}}`
	if string(got) != want {
		t.Fatalf("unexpected JSON\nwant: %s\n got: %s", want, string(got))
	}
}

func TestErrorEnvelopeIncludesNullOperationAndEmptyDetails(t *testing.T) {
	env := NewErrorEnvelope(CodePathDenied, "route is not available", false, "corr_456", nil, nil)

	got, err := MarshalErrorEnvelope(env)
	if err != nil {
		t.Fatalf("MarshalErrorEnvelope returned error: %v", err)
	}

	want := `{"error":{"code":"PATH_DENIED","message":"route is not available","retryable":false,"correlation_id":"corr_456","operation_id":null,"details":{}}}`
	if string(got) != want {
		t.Fatalf("unexpected JSON\nwant: %s\n got: %s", want, string(got))
	}
}

func TestErrorEnvelopeRedactsCredentialLikeDetails(t *testing.T) {
	details := map[string]any{
		"repo_id":    "repo_123",
		"password":   "plain-password",
		"secret_ref": "vault://storage/root",
		"headers": map[string]any{
			"Authorization": "Bearer top-secret-token",
			"X-Trace":       "trace-ok",
		},
		"nested": []any{
			map[string]any{"api_key": "api-key-value"},
		},
	}

	env := NewErrorEnvelope(CodeCapabilityDenied, "denied", false, "corr_redact", nil, details)
	gotBytes, err := MarshalErrorEnvelope(env)
	if err != nil {
		t.Fatalf("MarshalErrorEnvelope returned error: %v", err)
	}
	got := string(gotBytes)

	for _, leaked := range []string{
		"plain-password",
		"vault://storage/root",
		"Bearer top-secret-token",
		"api-key-value",
	} {
		if strings.Contains(got, leaked) {
			t.Fatalf("credential-like value leaked in error envelope: %q in %s", leaked, got)
		}
	}

	for _, expected := range []string{
		`"password":"[REDACTED]"`,
		`"secret_ref":"[REDACTED]"`,
		`"Authorization":"[REDACTED]"`,
		`"api_key":"[REDACTED]"`,
		`"repo_id":"repo_123"`,
		`"X-Trace":"trace-ok"`,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("expected redacted/safe detail %s in %s", expected, got)
		}
	}

	if details["password"] != "plain-password" {
		t.Fatalf("NewErrorEnvelope mutated caller-owned details map")
	}

	var decoded ErrorEnvelope
	if err := json.Unmarshal(gotBytes, &decoded); err != nil {
		t.Fatalf("error envelope JSON did not decode: %v", err)
	}
}

func TestErrorEnvelopeRedactsAFSCPForbiddenDetailsAndBearerValues(t *testing.T) {
	details := map[string]any{
		"metadata_url":          "http://metadata.internal/token",
		"metadataUrl":           "http://metadata.camel/token",
		"storage_bucket_url":    "s3://bucket/root",
		"storageBucketURL":      "https://bucket.camel/root",
		"object_store_endpoint": "https://object-store.internal",
		"objectStoreEndpoint":   "https://object-store.camel",
		"accessKey":             "access-key-value",
		"secretAccessKey":       "secret-access-key-value",
		"apiKey":                "api-key-value",
		"privateKey":            "private-key-value",
		"authorization":         "Bearer authorization-token",
		"cookie":                "session=secret",
		"token":                 "plain-token",
		"password":              "plain-password",
		"secret":                "plain-secret",
		"secret_ref":            "vault://secret/ref",
		"k8s secret":            "namespace/name",
		"webdav password":       "webdav-password",
		"status":                "Bearer bearer-value",
		"safe":                  "visible",
		"headers": map[string]string{
			"Cookie":    "nested=session",
			"X-Request": "request-id",
			"Message":   "Bearer nested-bearer",
		},
	}

	env := NewErrorEnvelope(CodeCapabilityDenied, "denied", false, "corr_redact", nil, details)

	for _, key := range []string{
		"metadata_url",
		"metadataUrl",
		"storage_bucket_url",
		"storageBucketURL",
		"object_store_endpoint",
		"objectStoreEndpoint",
		"accessKey",
		"secretAccessKey",
		"apiKey",
		"privateKey",
		"authorization",
		"cookie",
		"token",
		"password",
		"secret",
		"secret_ref",
		"k8s secret",
		"webdav password",
		"status",
	} {
		if got := env.Error.Details[key]; got != redactedDetailValue {
			t.Fatalf("%s = %#v, want %q", key, got, redactedDetailValue)
		}
	}

	headers, ok := env.Error.Details["headers"].(map[string]any)
	if !ok {
		t.Fatalf("headers redacted as %T, want map[string]any", env.Error.Details["headers"])
	}
	for _, key := range []string{"Cookie", "Message"} {
		if got := headers[key]; got != redactedDetailValue {
			t.Fatalf("headers[%s] = %#v, want %q", key, got, redactedDetailValue)
		}
	}
	if got, want := headers["X-Request"], "request-id"; got != want {
		t.Fatalf("headers[X-Request] = %#v, want %#v", got, want)
	}
	if got, want := env.Error.Details["safe"], "visible"; got != want {
		t.Fatalf("safe = %#v, want %#v", got, want)
	}
}

func TestErrorEnvelopeDropsJVSInternalDetailsFromProductSurface(t *testing.T) {
	details := map[string]any{
		"repo_id":        "repo_123",
		"checksum":       "sha256:internal-checksum",
		"digest":         "sha256:internal-digest",
		"capacity_bytes": 123456,
		"tree_scan":      "internal-tree-scan",
		"file_count":     42,
		"payload_tree":   map[string]any{"root": "internal-payload-tree"},
		"sync_state":     "internal-sync-state",
		"proof":          "internal-proof",
		"internal_path":  "/srv/afscp/internal/path",
		"control-root":   "/srv/afscp/control",
		"home_path":      "/home/afscp/runtime/repo_123",
		"raw command":    "jvs afscp --control-root raw-control --home raw-home restore",
		"nested": map[string]any{
			"safe":         "visible",
			"payload_hash": "internal-payload-hash",
		},
		"array": []any{
			map[string]any{"proof": "array-proof", "safe": "visible-array"},
		},
	}

	env := NewErrorEnvelope(CodeJVSCommandFailed, "failed", true, "corr_redact", nil, details)
	rendered := strings.ToLower(toJSONForTest(t, env.Error.Details))

	for _, forbidden := range []string{
		"checksum",
		"internal-checksum",
		"digest",
		"internal-digest",
		"capacity_bytes",
		"tree_scan",
		"file_count",
		"payload_tree",
		"sync_state",
		"proof",
		"internal_path",
		"control-root",
		"home_path",
		"raw command",
		"payload_hash",
		"array-proof",
		"/srv/afscp",
		"/home/afscp",
		"jvs afscp",
	} {
		if strings.Contains(rendered, strings.ToLower(forbidden)) {
			t.Fatalf("JVS/internal detail %q leaked into product error surface: %s", forbidden, rendered)
		}
	}
	if got := env.Error.Details["repo_id"]; got != "repo_123" {
		t.Fatalf("repo_id = %#v, want safe detail preserved", got)
	}
	nested, ok := env.Error.Details["nested"].(map[string]any)
	if !ok || nested["safe"] != "visible" {
		t.Fatalf("nested safe detail not preserved: %#v", env.Error.Details["nested"])
	}
}

func toJSONForTest(t *testing.T, value any) string {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test JSON: %v", err)
	}
	return string(encoded)
}
