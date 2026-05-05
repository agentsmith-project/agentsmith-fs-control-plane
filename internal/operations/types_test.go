package operations

import (
	"encoding/json"
	"testing"
	"time"
)

func TestOperationTypesAreStableAndComplete(t *testing.T) {
	got := operationTypeStringsForTest(OperationTypes())
	want := []string{
		"volume_ensure",
		"namespace_upsert",
		"namespace_disable",
		"namespace_volume_binding_put",
		"repo_create",
		"repo_archive",
		"repo_restore_archived",
		"repo_delete",
		"repo_restore_tombstoned",
		"repo_purge",
		"save_point_create",
		"restore_preview",
		"restore_run",
		"template_create",
		"template_clone",
		"export_create",
		"export_revoke",
		"export_session_reconcile",
		"mount_binding_create",
		"mount_binding_status_update",
		"mount_binding_heartbeat",
		"mount_binding_release",
		"mount_binding_revoke",
		"migration_cutover",
	}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("OperationTypes() = %#v, want %#v", got, want)
	}
}

func TestNamespaceUpsertPhasesAreStable(t *testing.T) {
	if OperationPhaseNamespaceUpsertValidate != "validate_namespace_upsert" {
		t.Fatalf("validate phase = %q, want validate_namespace_upsert", OperationPhaseNamespaceUpsertValidate)
	}
	if OperationPhaseNamespaceUpsertCommitted != "namespace_upsert_committed" {
		t.Fatalf("committed phase = %q, want namespace_upsert_committed", OperationPhaseNamespaceUpsertCommitted)
	}
}

func TestVolumeEnsurePhasesAreStable(t *testing.T) {
	if OperationPhaseVolumeEnsureValidate != "validate_volume_ensure" {
		t.Fatalf("validate phase = %q, want validate_volume_ensure", OperationPhaseVolumeEnsureValidate)
	}
	if OperationPhaseVolumeEnsureCommitted != "volume_ensure_committed" {
		t.Fatalf("committed phase = %q, want volume_ensure_committed", OperationPhaseVolumeEnsureCommitted)
	}
}

func TestNamespaceVolumeBindingPutPhasesAreStable(t *testing.T) {
	if OperationPhaseNamespaceVolumeBindingPutValidate != "validate_namespace_volume_binding_put" {
		t.Fatalf("validate phase = %q, want validate_namespace_volume_binding_put", OperationPhaseNamespaceVolumeBindingPutValidate)
	}
	if OperationPhaseNamespaceVolumeBindingPutCommitted != "namespace_volume_binding_put_committed" {
		t.Fatalf("committed phase = %q, want namespace_volume_binding_put_committed", OperationPhaseNamespaceVolumeBindingPutCommitted)
	}
}

func TestRepoCreatePhasesAreStable(t *testing.T) {
	if OperationPhaseRepoCreateValidate != "validate_repo_create" {
		t.Fatalf("validate phase = %q, want validate_repo_create", OperationPhaseRepoCreateValidate)
	}
}

func TestRouteOperationTypesReturnsDefensiveCopy(t *testing.T) {
	mapped := RouteOperationTypes()
	mapped["createRepo"] = OperationMigrationCutover

	got, ok := OperationTypeForRouteOperationID("createRepo")
	if !ok {
		t.Fatalf("createRepo route operation type missing")
	}
	if got != OperationRepoCreate {
		t.Fatalf("createRepo route operation type = %q, want %q", got, OperationRepoCreate)
	}
}

func TestOperationRecordJSONMatchesSchemaBoundaryShape(t *testing.T) {
	createdAt := time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)
	scope := NewIdempotencyScope("afscp-api", "ns_alpha", OperationRepoCreate, "client-key-1")
	record := OperationRecord{
		ID:                  "op_alpha",
		Type:                OperationRepoCreate,
		State:               OperationStateQueued,
		Phase:               "allocate_repo_path",
		Attempt:             1,
		IdempotencyScope:    scope.String(),
		IdempotencyKey:      "client-key-1",
		RequestHash:         RequestHash("sha256:hash"),
		CorrelationID:       "corr-1",
		CallerService:       "afscp-api",
		AuthorizedActor:     Actor{Type: "system", ID: "svc-1"},
		Resource:            ResourceRef{Type: "repo", ID: "repo_project"},
		NamespaceID:         "ns_alpha",
		RepoID:              "repo_project",
		TemplateID:          "tmpl_base",
		ExportID:            "export_snapshot",
		MountBindingID:      "wmb_worker",
		SessionFenceID:      "fence-1",
		ExternalResourceIDs: map[string]string{"jvs_repo_id": "jvs-secret"},
		InputSummary:        map[string]any{"safe": "kept"},
		VerificationResult:  map[string]any{"doctor": "passed"},
		CompensationStatus:  "not_required",
		Error:               nil,
		CreatedAt:           createdAt,
	}

	payload := marshalRecordForTest(t, record)

	for _, key := range []string{
		"operation_id",
		"operation_type",
		"operation_state",
		"phase",
		"attempt",
		"lease_owner",
		"lease_expires_at",
		"idempotency_scope",
		"idempotency_key",
		"request_hash",
		"correlation_id",
		"caller_service",
		"authorized_actor",
		"resource",
		"namespace_id",
		"repo_id",
		"template_id",
		"export_id",
		"mount_binding_id",
		"session_fence_id",
		"external_resource_ids",
		"input_summary",
		"jvs_json_output",
		"verification_result",
		"compensation_status",
		"error",
		"created_at",
		"started_at",
		"finished_at",
	} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("required key %q missing from operation record JSON: %#v", key, payload)
		}
	}
	for _, legacyKey := range []string{"caller", "resources"} {
		if _, ok := payload[legacyKey]; ok {
			t.Fatalf("legacy nested key %q leaked into operation record JSON", legacyKey)
		}
	}

	if got := jsonStringForTest(t, payload["caller_service"]); got != "afscp-api" {
		t.Fatalf("caller_service = %q, want afscp-api", got)
	}
	if got := jsonStringForTest(t, payload["idempotency_scope"]); got != scope.String() {
		t.Fatalf("idempotency_scope = %q, want %q", got, scope.String())
	}
	if got := jsonStringForTest(t, payload["request_hash"]); got != "sha256:hash" {
		t.Fatalf("request_hash = %q, want sha256:hash", got)
	}
}

func TestOperationRecordRequiredNullableFieldsMarshalAsNull(t *testing.T) {
	record := OperationRecord{
		ID:               "op_alpha",
		Type:             OperationRepoCreate,
		State:            OperationStateQueued,
		Phase:            "queued",
		Attempt:          0,
		IdempotencyScope: NewIdempotencyScope("afscp-api", "ns_alpha", OperationRepoCreate, "client-key-1").String(),
		IdempotencyKey:   "client-key-1",
		RequestHash:      RequestHash("sha256:hash"),
		CorrelationID:    "corr-1",
		CallerService:    "afscp-api",
		AuthorizedActor:  Actor{Type: "system", ID: "svc-1"},
		Resource:         ResourceRef{Type: "repo", ID: "repo_project"},
		CreatedAt:        time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC),
	}

	payload := marshalRecordForTest(t, record)
	for _, key := range []string{
		"lease_owner",
		"lease_expires_at",
		"namespace_id",
		"repo_id",
		"template_id",
		"export_id",
		"mount_binding_id",
		"session_fence_id",
		"jvs_json_output",
		"verification_result",
		"compensation_status",
		"started_at",
		"finished_at",
	} {
		if got := string(payload[key]); got != "null" {
			t.Fatalf("%s JSON = %s, want null", key, got)
		}
	}
}

func TestOperationRecordRequiredObjectFieldsMarshalAsObjects(t *testing.T) {
	record := OperationRecord{
		ID:               "op_alpha",
		Type:             OperationRepoCreate,
		State:            OperationStateQueued,
		Phase:            "queued",
		Attempt:          0,
		IdempotencyScope: NewIdempotencyScope("afscp-api", "ns_alpha", OperationRepoCreate, "client-key-1").String(),
		IdempotencyKey:   "client-key-1",
		RequestHash:      RequestHash("sha256:hash"),
		CorrelationID:    "corr-1",
		CallerService:    "afscp-api",
		AuthorizedActor:  Actor{Type: "system", ID: "svc-1"},
		Resource:         ResourceRef{Type: "repo", ID: "repo_project"},
		Error:            nil,
		CreatedAt:        time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC),
	}

	payload := marshalRecordForTest(t, record)
	if got := string(payload["external_resource_ids"]); got != "{}" {
		t.Fatalf("external_resource_ids JSON = %s, want {}", got)
	}
	if got := string(payload["input_summary"]); got != "{}" {
		t.Fatalf("input_summary JSON = %s, want {}", got)
	}
	if got := string(payload["error"]); got != "null" {
		t.Fatalf("error JSON = %s, want null", got)
	}
}

func TestOperationRecordEnvelopeMirrorsSanitizedRecordAndTerminalError(t *testing.T) {
	record := OperationRecord{
		ID:    "op-1",
		Type:  OperationRepoDelete,
		State: OperationStateFailed,
		Error: &OperationError{
			Code:          "OPERATION_RECOVERY_REQUIRED",
			Message:       "delete failed",
			Retryable:     false,
			CorrelationID: "corr-1",
		},
	}

	envelope := NewOperationRecordEnvelope(record)
	if envelope.Operation.ID != "op-1" {
		t.Fatalf("operation not exposed in envelope: %#v", envelope)
	}
	if envelope.Error == nil || envelope.Error.Code != "OPERATION_RECOVERY_REQUIRED" {
		t.Fatalf("terminal error not mirrored in envelope: %#v", envelope.Error)
	}
}

func marshalRecordForTest(t *testing.T, record OperationRecord) map[string]json.RawMessage {
	t.Helper()

	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal operation record: %v", err)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("unmarshal operation record JSON: %v", err)
	}
	return payload
}

func jsonStringForTest(t *testing.T, raw json.RawMessage) string {
	t.Helper()

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("unmarshal JSON string %s: %v", raw, err)
	}
	return value
}

func operationTypeStringsForTest(types []OperationType) []string {
	values := make([]string, len(types))
	for i, typ := range types {
		values[i] = typ.String()
	}
	return values
}

func stringSlicesEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
