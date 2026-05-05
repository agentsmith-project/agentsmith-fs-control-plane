package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func TestEnsureVolumeHandlerCreatesOperationIntake(t *testing.T) {
	now := fixedNamespaceNow()
	store := &fakeOperationIntakeStore{}
	handler := ensureVolumeHandlerForTest(store, func() string { return "op_volume" }, func() time.Time { return now }, volumeAdminAllowedPolicy())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, ensureVolumeRequest("/internal/v1/volumes/vol_123:ensure", ensureVolumeRequestBody("vol_123")))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("intake calls = %d, want 1", store.calls)
	}
	spec := store.spec
	wantScope := operations.NewIdempotencyScope("agentsmith-api", "", operations.OperationVolumeEnsure, "idem_volume")
	if spec.OperationID != "op_volume" || spec.Scope != wantScope {
		t.Fatalf("spec op/scope = %q/%#v, want op_volume/%#v", spec.OperationID, spec.Scope, wantScope)
	}
	if spec.Phase != operations.OperationPhaseVolumeEnsureValidate {
		t.Fatalf("phase = %q, want %s", spec.Phase, operations.OperationPhaseVolumeEnsureValidate)
	}
	if spec.NamespaceID != "" || spec.Resource.Type != "volume" || spec.Resource.ID != "vol_123" {
		t.Fatalf("namespace/resource = %q/%#v", spec.NamespaceID, spec.Resource)
	}
	if spec.InputSummary["volume_id"] != "vol_123" || spec.InputSummary["backend"] != "juicefs" {
		t.Fatalf("input summary = %#v, want volume metadata", spec.InputSummary)
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_volume" || env.OperationState != OperationStateQueued || env.Resource.Type != "volume" || env.Resource.ID != "vol_123" {
		t.Fatalf("envelope = %#v, want queued volume op", env)
	}
	assertOperationEnvelopeDoesNotLeakInternalFields(t, env)
}

func TestEnsureVolumeHandlerValidationDeniesBeforeIntake(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		body     string
		wantCode ErrorCode
	}{
		{name: "invalid path volume", path: "/internal/v1/volumes/bad_vol:ensure", body: ensureVolumeRequestBody("bad_vol"), wantCode: CodeInvalidID},
		{name: "body mismatch", path: "/internal/v1/volumes/vol_123:ensure", body: ensureVolumeRequestBody("vol_456"), wantCode: CodeInvalidID},
		{name: "unknown field", path: "/internal/v1/volumes/vol_123:ensure", body: strings.Replace(ensureVolumeRequestBody("vol_123"), `"status":"active"`, `"status":"active","raw_path":"/secret"`, 1), wantCode: CodeInvalidID},
		{name: "secret capability", path: "/internal/v1/volumes/vol_123:ensure", body: strings.Replace(ensureVolumeRequestBody("vol_123"), `"directory_quota":false`, `"directory_quota":false,"metadata_url":"secret"`, 1), wantCode: CodeInvalidID},
		{name: "malformed", path: "/internal/v1/volumes/vol_123:ensure", body: `{"volume_id":`, wantCode: CodeInvalidID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			sink := &fakeAuditSink{}
			handler := ensureVolumeHandlerForTestWithAudit(store, func() string { return "op_volume" }, fixedNamespaceNow, volumeAdminAllowedPolicy(), sink)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, ensureVolumeRequest(tt.path, tt.body))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
			}
			if store.calls != 0 {
				t.Fatalf("intake calls = %d, want 0", store.calls)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode {
				t.Fatalf("error code = %s, want %s", env.Error.Code, tt.wantCode)
			}
			if len(sink.events) != 1 {
				t.Fatalf("audit events = %#v, want validation denial", sink.events)
			}
			if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "raw_path") {
				t.Fatalf("validation leaked sensitive detail: %s", rec.Body.String())
			}
		})
	}
}

func TestEnsureVolumeHandlerUsesDeploymentGlobalPolicy(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	handler := ensureVolumeHandlerForTest(store, func() string { return "op_volume" }, fixedNamespaceNow, volumeAdminAllowedPolicy())
	req := ensureVolumeRequest("/internal/v1/volumes/vol_123:ensure", ensureVolumeRequestBody("vol_123"))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200 without namespace header", rec.Code, rec.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("intake calls = %d, want 1", store.calls)
	}
}

func TestEnsureVolumeHandlerRejectsNamespaceHeaderBeforeIntake(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	sink := &fakeAuditSink{}
	handler := ensureVolumeHandlerForTestWithAudit(store, func() string { return "op_volume" }, fixedNamespaceNow, volumeAdminAllowedPolicy(), sink)
	req := ensureVolumeRequest("/internal/v1/volumes/vol_123:ensure", ensureVolumeRequestBody("vol_123"))
	req.Header.Set(auth.HeaderNamespaceID, "ns_ignored")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	if store.calls != 0 {
		t.Fatalf("intake calls = %d, want 0", store.calls)
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeResourceNamespaceMismatch {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeResourceNamespaceMismatch)
	}
	if len(sink.events) != 1 {
		t.Fatalf("audit events = %#v, want validation denial", sink.events)
	}
	if strings.Contains(rec.Body.String(), "ns_ignored") {
		t.Fatalf("response leaked namespace header value: %s", rec.Body.String())
	}
}

func TestEnsureVolumeHandlerMapsIntakeErrorsWithoutLeakingStoreDetail(t *testing.T) {
	store := &fakeOperationIntakeStore{err: errors.New("postgres password=secret failed")}
	handler := ensureVolumeHandlerForTest(store, func() string { return "op_volume" }, fixedNamespaceNow, volumeAdminAllowedPolicy())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, ensureVolumeRequest("/internal/v1/volumes/vol_123:ensure", ensureVolumeRequestBody("vol_123")))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s, want 503", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "postgres") {
		t.Fatalf("error leaked raw detail: %s", rec.Body.String())
	}
}

func ensureVolumeHandlerForTest(store OperationIntakeStore, generate OperationIDGenerator, now func() time.Time, policy AllowedCallerPolicy) http.Handler {
	return ensureVolumeHandlerForTestWithAudit(store, generate, now, policy, nil)
}

func ensureVolumeHandlerForTestWithAudit(store OperationIntakeStore, generate OperationIDGenerator, now func() time.Time, policy AllowedCallerPolicy, sink *fakeAuditSink) http.Handler {
	var auditSink audit.Sink
	if sink != nil {
		auditSink = sink
	}
	return EnsureVolumeHandler(EnsureVolumeHandlerConfig{
		IntakeStore:       store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		DeploymentPolicy:  policy,
		OperationID:       generate,
		Now:               now,
		AuditSink:         auditSink,
	})
}

func ensureVolumeRequest(path, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_volume")
	req.Header.Set(auth.HeaderCallerService, "agentsmith-api")
	req.Header.Set(auth.HeaderIdempotencyKey, "idem_volume")
	req.Header.Set(auth.HeaderActorType, "system")
	req.Header.Set(auth.HeaderActorID, "svc-volume")
	return req
}

func ensureVolumeRequestBody(volumeID string) string {
	return `{"volume_id":"` + volumeID + `","backend":"juicefs","isolation_class":"shared","status":"active","capabilities":{"webdav_export":true,"workload_mount":true,"jvs_external_control_root":true,"directory_quota":false}}`
}

func volumeAdminAllowedPolicy() AllowedCallerPolicy {
	return fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{
		CallerService: "agentsmith-api",
		Kind:          auth.CallerKindAdmin,
		Roles:         []auth.Role{auth.RoleVolumeAdmin},
	}}}
}
