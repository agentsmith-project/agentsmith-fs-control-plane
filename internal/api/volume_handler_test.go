package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
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
	wantScope := operations.NewIdempotencyScope("product-caller", "", operations.OperationVolumeEnsure, "idem_volume")
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
		{name: "quota enforced is not GA capability", path: "/internal/v1/volumes/vol_123:ensure", body: strings.Replace(ensureVolumeRequestBody("vol_123"), `"directory_quota":false`, `"directory_quota":false,"quota_enforced":true`, 1), wantCode: CodeInvalidID},
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

func TestVolumeHealthHandlerReturnsHealthyOnlyWhenBackendProbePasses(t *testing.T) {
	now := fixedNamespaceNow()
	volume := healthyVolumeFixture(now)
	probe := &fakeVolumeBackendHealthProbe{result: VolumeBackendHealthResult{Healthy: true}}
	handler := VolumeHealthHandler(VolumeHealthHandlerConfig{
		Reader:            fakeVolumeHealthReader{volume: volume},
		BackendProbe:      probe,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		DeploymentPolicy:  volumeAdminAllowedPolicy(),
		Now:               func() time.Time { return now },
	})

	rec := serveVolumeHealth(handler)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	body := decodeVolumeHealthResponse(t, rec.Body.Bytes())
	if body.Status != "healthy" || body.VolumeID != "vol_123" || len(body.Findings) != 0 {
		t.Fatalf("body = %#v, want healthy without findings", body)
	}
	if probe.calls != 1 || probe.volumeID != "vol_123" {
		t.Fatalf("probe calls/volume = %d/%q, want one call for vol_123", probe.calls, probe.volumeID)
	}
}

func TestVolumeHealthHandlerMissingBackendProbeIsNotHealthy(t *testing.T) {
	now := fixedNamespaceNow()
	handler := VolumeHealthHandler(VolumeHealthHandlerConfig{
		Reader:            fakeVolumeHealthReader{volume: healthyVolumeFixture(now)},
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		DeploymentPolicy:  volumeAdminAllowedPolicy(),
		Now:               func() time.Time { return now },
	})

	rec := serveVolumeHealth(handler)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	body := decodeVolumeHealthResponse(t, rec.Body.Bytes())
	if body.Status == "healthy" {
		t.Fatalf("status = healthy, want missing backend probe to degrade health: %#v", body)
	}
	if !hasVolumeHealthFinding(body, "BACKEND_PROBE_MISSING") {
		t.Fatalf("findings = %#v, want BACKEND_PROBE_MISSING", body.Findings)
	}
}

func TestVolumeHealthHandlerBackendProbeFailureIsNotHealthyAndDoesNotLeakDetails(t *testing.T) {
	now := fixedNamespaceNow()
	tests := []struct {
		name     string
		probe    *fakeVolumeBackendHealthProbe
		wantCode string
	}{
		{
			name:     "probe reports failed",
			probe:    &fakeVolumeBackendHealthProbe{result: VolumeBackendHealthResult{Healthy: false}},
			wantCode: "BACKEND_PROBE_FAILED",
		},
		{
			name:     "probe returns error",
			probe:    &fakeVolumeBackendHealthProbe{err: errors.New("stat /srv/vol_123 failed with password=secret token=backend-token")},
			wantCode: "BACKEND_PROBE_ERROR",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := VolumeHealthHandler(VolumeHealthHandlerConfig{
				Reader:            fakeVolumeHealthReader{volume: healthyVolumeFixture(now)},
				BackendProbe:      tt.probe,
				PrincipalResolver: namespaceBindingPrincipalResolver(),
				DeploymentPolicy:  volumeAdminAllowedPolicy(),
				Now:               func() time.Time { return now },
			})

			rec := serveVolumeHealth(handler)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
			}
			body := decodeVolumeHealthResponse(t, rec.Body.Bytes())
			if body.Status == "healthy" {
				t.Fatalf("status = healthy, want backend probe failure to degrade health: %#v", body)
			}
			if !hasVolumeHealthFinding(body, tt.wantCode) {
				t.Fatalf("findings = %#v, want %s", body.Findings, tt.wantCode)
			}
			for _, forbidden := range []string{"/srv/", "password", "secret", "backend-token", "failed with"} {
				if strings.Contains(strings.ToLower(rec.Body.String()), strings.ToLower(forbidden)) {
					t.Fatalf("volume health leaked %q in %s", forbidden, rec.Body.String())
				}
			}
		})
	}
}

func TestVolumeHealthHandlerMetadataFindingsAreNotOverriddenByBackendProbe(t *testing.T) {
	now := fixedNamespaceNow()
	tests := []struct {
		name       string
		editVolume func(*resources.Volume)
		wantCode   string
		wantStatus string
	}{
		{
			name:       "disabled",
			editVolume: func(volume *resources.Volume) { volume.Status = resources.VolumeStatusDisabled },
			wantCode:   "VOLUME_DISABLED",
			wantStatus: "unavailable",
		},
		{
			name:       "degraded",
			editVolume: func(volume *resources.Volume) { volume.Status = resources.VolumeStatusDegraded },
			wantCode:   "VOLUME_DEGRADED",
			wantStatus: "degraded",
		},
		{
			name: "required capability missing",
			editVolume: func(volume *resources.Volume) {
				volume.Capabilities["workload_mount"] = false
			},
			wantCode:   "CAPABILITY_NOT_READY",
			wantStatus: "degraded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			volume := healthyVolumeFixture(now)
			tt.editVolume(&volume)
			handler := VolumeHealthHandler(VolumeHealthHandlerConfig{
				Reader:            fakeVolumeHealthReader{volume: volume},
				BackendProbe:      &fakeVolumeBackendHealthProbe{result: VolumeBackendHealthResult{Healthy: true}},
				PrincipalResolver: namespaceBindingPrincipalResolver(),
				DeploymentPolicy:  volumeAdminAllowedPolicy(),
				Now:               func() time.Time { return now },
			})

			rec := serveVolumeHealth(handler)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
			}
			body := decodeVolumeHealthResponse(t, rec.Body.Bytes())
			if body.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q: %#v", body.Status, tt.wantStatus, body)
			}
			if !hasVolumeHealthFinding(body, tt.wantCode) {
				t.Fatalf("findings = %#v, want %s", body.Findings, tt.wantCode)
			}
			if body.Status == "healthy" {
				t.Fatalf("backend probe ok overrode metadata finding: %#v", body)
			}
		})
	}
}

func TestInternalAPIShellWiresVolumeBackendHealthProbe(t *testing.T) {
	now := fixedNamespaceNow()
	probe := &fakeVolumeBackendHealthProbe{result: VolumeBackendHealthResult{Healthy: true}}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:         namespaceBindingPrincipalResolver(),
		VolumeReader:              fakeVolumeHealthReader{volume: healthyVolumeFixture(now)},
		VolumeBackendHealthProbe:  probe,
		DeploymentGlobalPolicy:    volumeAdminAllowedPolicy(),
		DeploymentNamespacePolicy: namespaceBindingAllowedPolicy(auth.RoleNamespaceAdmin),
		NamespaceBindingReader:    &fakeNamespaceVolumeBindingReader{},
		Now:                       func() time.Time { return now },
	})

	rec := serveVolumeHealth(handler)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	body := decodeVolumeHealthResponse(t, rec.Body.Bytes())
	if body.Status != "healthy" || probe.calls != 1 {
		t.Fatalf("body/probe = %#v/%d, want healthy through shell-wired probe", body, probe.calls)
	}
}

func serveVolumeHealth(handler http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/volumes/vol_123/health", nil)
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_volume")
	req.Header.Set(auth.HeaderCallerService, "product-caller")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func healthyVolumeFixture(now time.Time) resources.Volume {
	return resources.Volume{
		ID:             "vol_123",
		Backend:        resources.VolumeBackendJuiceFS,
		IsolationClass: resources.VolumeIsolationShared,
		Status:         resources.VolumeStatusActive,
		Capabilities:   map[string]any{"webdav_export": true, "workload_mount": true, "jvs_external_control_root": true, "directory_quota": false},
		CreatedAt:      now.Add(-time.Hour),
		UpdatedAt:      now,
	}
}

func decodeVolumeHealthResponse(t *testing.T, body []byte) VolumeHealthResponse {
	t.Helper()
	var response VolumeHealthResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("volume health response did not decode: %v: %s", err, string(body))
	}
	return response
}

func hasVolumeHealthFinding(response VolumeHealthResponse, code string) bool {
	for _, finding := range response.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

type fakeVolumeBackendHealthProbe struct {
	result   VolumeBackendHealthResult
	err      error
	calls    int
	volumeID string
}

func (probe *fakeVolumeBackendHealthProbe) CheckVolumeBackendHealth(_ context.Context, volume resources.Volume) (VolumeBackendHealthResult, error) {
	probe.calls++
	probe.volumeID = volume.ID
	if probe.err != nil {
		return VolumeBackendHealthResult{}, probe.err
	}
	return probe.result, nil
}

type fakeVolumeHealthReader struct {
	volume resources.Volume
	err    error
}

func (reader fakeVolumeHealthReader) GetVolume(_ context.Context, _ string) (resources.Volume, error) {
	if reader.err != nil {
		return resources.Volume{}, reader.err
	}
	return reader.volume, nil
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
	req.Header.Set(auth.HeaderCallerService, "product-caller")
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
		CallerService: "product-caller",
		Kind:          auth.CallerKindAdmin,
		Roles:         []auth.Role{auth.RoleVolumeAdmin},
	}}}
}
