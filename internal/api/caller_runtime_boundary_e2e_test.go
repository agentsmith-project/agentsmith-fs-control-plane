package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"
)

func TestProductCallerOperationResponsesDoNotLeakStorageInternals(t *testing.T) {
	env := newCallerRuntimeBoundaryE2E(t, nil)

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		idem       string
		wantStatus int
		wantState  OperationState
		wantType   string
	}{
		{
			name:       "repo create",
			method:     http.MethodPost,
			path:       "/internal/v1/repos",
			body:       `{"namespace_id":"ns_123","target_repo_id":"repo_e2e_created"}`,
			idem:       "idem_repo_e2e",
			wantStatus: http.StatusOK,
			wantState:  OperationStateQueued,
			wantType:   "repo",
		},
		{
			name:       "webdav export create",
			method:     http.MethodPost,
			path:       "/internal/v1/repos/repo_123/exports",
			body:       `{"mode":"read_write","ttl_seconds":120}`,
			idem:       "idem_export_e2e",
			wantStatus: http.StatusAccepted,
			wantState:  OperationStateSucceeded,
			wantType:   "export",
		},
		{
			name:       "workload mount create",
			method:     http.MethodPost,
			path:       "/internal/v1/repos/repo_123/workload-mount-bindings",
			body:       `{"mount_path":"/mnt/repo","read_only":false,"lease_seconds":120}`,
			idem:       "idem_mount_e2e",
			wantStatus: http.StatusAccepted,
			wantState:  OperationStateQueued,
			wantType:   "workload_mount_binding",
		},
		{
			name:       "lifecycle delete",
			method:     http.MethodPost,
			path:       "/internal/v1/repos/repo_delete:delete",
			body:       `{"reason":"delete-reason-secret-canary"}`,
			idem:       "idem_delete_e2e",
			wantStatus: http.StatusAccepted,
			wantState:  OperationStateQueued,
			wantType:   "repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := env.serve(callerRuntimeBoundaryE2ERequest(tt.method, tt.path, tt.body, "product-caller", "ns_123", tt.idem))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.wantStatus)
			}
			envelope := decodeOperationEnvelope(t, rec.Body.Bytes())
			if envelope.OperationState != tt.wantState || envelope.Resource.Type != tt.wantType {
				t.Fatalf("operation envelope = %#v, want state %s resource type %s", envelope, tt.wantState, tt.wantType)
			}
			assertProductCallerResponseDoesNotLeakStorage(t, rec.Body.String())
			if strings.Contains(rec.Body.String(), "delete-reason-secret-canary") {
				t.Fatalf("product lifecycle response leaked raw reason: %s", rec.Body.String())
			}
		})
	}
}

func TestRuntimeOrchestratorMountPlanBoundary(t *testing.T) {
	planCalls := &fakeWorkloadMountPlanReaderCalls{}
	env := newCallerRuntimeBoundaryE2E(t, func(config *callerRuntimeBoundaryE2EConfig) {
		config.planCalls = planCalls
	})

	product := env.serve(callerRuntimeBoundaryE2ERequest(http.MethodGet, "/internal/v1/workload-mount-bindings/wmb_123/orchestrator-plan", "", "product-caller", "ns_123", ""))
	if product.Code != http.StatusForbidden {
		t.Fatalf("product plan status = %d body = %s, want 403", product.Code, product.Body.String())
	}
	if envelope := decodeErrorEnvelope(t, product.Body.Bytes()); envelope.Error.Code != CodeRoleNotAllowed {
		t.Fatalf("product plan error = %#v, want role denial", envelope.Error)
	}
	if planCalls.mountBindingID != "" || planCalls.namespaceID != "" {
		t.Fatalf("product caller reached plan reader: %#v", planCalls)
	}
	assertProductCallerResponseDoesNotLeakStorage(t, product.Body.String())

	orchestrator := env.serve(callerRuntimeBoundaryE2ERequest(http.MethodGet, "/internal/v1/workload-mount-bindings/wmb_123/orchestrator-plan", "", "runtime-orchestrator", "ns_123", ""))
	if orchestrator.Code != http.StatusOK {
		t.Fatalf("orchestrator plan status = %d body = %s, want 200", orchestrator.Code, orchestrator.Body.String())
	}
	var plan workloadmount.Plan
	if err := json.Unmarshal(orchestrator.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode orchestrator plan: %v body=%s", err, orchestrator.Body.String())
	}
	if plan.MountBindingID != "wmb_123" || plan.PayloadVolumeSubdir != "afscp/namespaces/ns_123/repos/repo_123/payload" {
		t.Fatalf("plan identity/payload = %#v", plan)
	}
	if plan.SecretRef.Namespace != "runtime-secret-namespace" || plan.SecretRef.Name != "runtime-secret-volume" {
		t.Fatalf("plan secret ref = %#v", plan.SecretRef)
	}
	if !plan.SecurityPolicy.RunAsNonRoot || plan.SecurityPolicy.AllowPrivileged || !plan.SecurityPolicy.JVSControlOutsidePayload {
		t.Fatalf("plan security policy = %#v", plan.SecurityPolicy)
	}
	if planCalls.namespaceID != "ns_123" || planCalls.mountBindingID != "wmb_123" {
		t.Fatalf("plan reader scope = %#v, want ns_123/wmb_123", planCalls)
	}
	assertRuntimeOrchestratorPlanDoesNotExposeControlRoot(t, orchestrator.Body.String())
}

func TestProductCallerAndRuntimeOrchestratorMountCallbackRoleBoundary(t *testing.T) {
	t.Run("product caller cannot spoof runtime callbacks", func(t *testing.T) {
		env := newCallerRuntimeBoundaryE2E(t, nil)
		tests := []struct {
			name   string
			method string
			path   string
			body   string
			idem   string
		}{
			{
				name:   "status",
				method: http.MethodPatch,
				path:   "/internal/v1/workload-mount-bindings/wmb_123/status",
				body:   `{"status":"active","observed_at":"2026-05-05T12:34:56Z","reason":"runtime callback should stay runtime-owned"}`,
				idem:   "idem_product_status_callback_e2e",
			},
			{
				name:   "heartbeat",
				method: http.MethodPost,
				path:   "/internal/v1/workload-mount-bindings/wmb_123:heartbeat",
				idem:   "idem_product_heartbeat_callback_e2e",
			},
			{
				name:   "release",
				method: http.MethodPost,
				path:   "/internal/v1/workload-mount-bindings/wmb_123:release",
				idem:   "idem_product_release_callback_e2e",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				rec := env.serve(callerRuntimeBoundaryE2ERequest(tt.method, tt.path, tt.body, "product-caller", "ns_123", tt.idem))

				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d body = %s, want 403", rec.Code, rec.Body.String())
				}
				if envelope := decodeErrorEnvelope(t, rec.Body.Bytes()); envelope.Error.Code != CodeRoleNotAllowed {
					t.Fatalf("error = %#v, want ROLE_NOT_ALLOWED", envelope.Error)
				}
				if env.operationStore.calls != 0 {
					t.Fatalf("operation intake calls = %d, want product callback denial before intake", env.operationStore.calls)
				}
				assertProductCallerResponseDoesNotLeakStorage(t, rec.Body.String())
			})
		}
	})

	t.Run("product caller can request revoke", func(t *testing.T) {
		env := newCallerRuntimeBoundaryE2E(t, nil)
		rec := env.serve(callerRuntimeBoundaryE2ERequest(http.MethodPost, "/internal/v1/workload-mount-bindings/wmb_123:revoke", "", "product-caller", "ns_123", "idem_product_revoke_e2e"))

		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
		}
		envelope := decodeOperationEnvelope(t, rec.Body.Bytes())
		if envelope.OperationState != OperationStateQueued || envelope.Resource.Type != "workload_mount_binding" || envelope.Resource.ID != "wmb_123" {
			t.Fatalf("operation envelope = %#v, want queued workload mount binding revoke", envelope)
		}
		if env.operationStore.calls != 1 || env.operationStore.spec.Scope.OperationType != operations.OperationMountBindingRevoke || env.operationStore.spec.Phase != operations.OperationPhaseMountBindingRevokeValidate {
			t.Fatalf("operation intake = calls %d spec %#v, want revoke intake", env.operationStore.calls, env.operationStore.spec)
		}
		assertProductCallerResponseDoesNotLeakStorage(t, rec.Body.String())
	})

	t.Run("runtime orchestrator owns runtime callbacks but not product revoke", func(t *testing.T) {
		env := newCallerRuntimeBoundaryE2E(t, nil)
		tests := []struct {
			name      string
			method    string
			path      string
			body      string
			idem      string
			wantType  operations.OperationType
			wantPhase string
		}{
			{
				name:      "status",
				method:    http.MethodPatch,
				path:      "/internal/v1/workload-mount-bindings/wmb_123/status",
				body:      `{"status":"active","observed_at":"2026-05-05T12:34:56Z","lease_expires_at":"2026-05-05T13:34:56Z","reason":"mounted"}`,
				idem:      "idem_orchestrator_status_callback_e2e",
				wantType:  operations.OperationMountBindingStatusUpdate,
				wantPhase: operations.OperationPhaseMountBindingStatusValidate,
			},
			{
				name:      "heartbeat",
				method:    http.MethodPost,
				path:      "/internal/v1/workload-mount-bindings/wmb_123:heartbeat",
				idem:      "idem_orchestrator_heartbeat_callback_e2e",
				wantType:  operations.OperationMountBindingHeartbeat,
				wantPhase: operations.OperationPhaseMountBindingHeartbeatValidate,
			},
			{
				name:      "release",
				method:    http.MethodPost,
				path:      "/internal/v1/workload-mount-bindings/wmb_123:release",
				idem:      "idem_orchestrator_release_callback_e2e",
				wantType:  operations.OperationMountBindingRelease,
				wantPhase: operations.OperationPhaseMountBindingReleaseValidate,
			},
		}

		for i, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				rec := env.serve(callerRuntimeBoundaryE2ERequest(tt.method, tt.path, tt.body, "runtime-orchestrator", "ns_123", tt.idem))

				if rec.Code != http.StatusAccepted {
					t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
				}
				envelope := decodeOperationEnvelope(t, rec.Body.Bytes())
				if envelope.OperationState != OperationStateQueued || envelope.Resource.Type != "workload_mount_binding" || envelope.Resource.ID != "wmb_123" {
					t.Fatalf("operation envelope = %#v, want queued workload mount binding callback", envelope)
				}
				if env.operationStore.calls != i+1 || env.operationStore.spec.Scope.OperationType != tt.wantType || env.operationStore.spec.Phase != tt.wantPhase {
					t.Fatalf("operation intake = calls %d spec %#v, want %s/%s", env.operationStore.calls, env.operationStore.spec, tt.wantType, tt.wantPhase)
				}
				assertProductCallerResponseDoesNotLeakStorage(t, rec.Body.String())
			})
		}

		rec := env.serve(callerRuntimeBoundaryE2ERequest(http.MethodPost, "/internal/v1/workload-mount-bindings/wmb_123:revoke", "", "runtime-orchestrator", "ns_123", "idem_orchestrator_revoke_e2e"))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d body = %s, want 403", rec.Code, rec.Body.String())
		}
		if envelope := decodeErrorEnvelope(t, rec.Body.Bytes()); envelope.Error.Code != CodeRoleNotAllowed {
			t.Fatalf("error = %#v, want ROLE_NOT_ALLOWED", envelope.Error)
		}
		if env.operationStore.calls != len(tests) {
			t.Fatalf("operation intake calls = %d, want orchestrator revoke denial before intake", env.operationStore.calls)
		}
		assertProductCallerResponseDoesNotLeakStorage(t, rec.Body.String())
	})
}

func TestProductCallerRuntimeBoundaryWorkloadMountRejectsCallerSuppliedStorageFields(t *testing.T) {
	env := newCallerRuntimeBoundaryE2E(t, nil)
	body := `{"mount_path":"/mnt/repo","read_only":true,"lease_seconds":120,"metadata_url":"redis://metadata-url-secret","volume_id":"vol_attacker"}`

	rec := env.serve(callerRuntimeBoundaryE2ERequest(http.MethodPost, "/internal/v1/repos/repo_123/workload-mount-bindings", body, "product-caller", "ns_123", "idem_bad_mount_e2e"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	if env.operationStore.calls != 0 {
		t.Fatalf("operation intake calls = %d, want raw storage field rejection before intake", env.operationStore.calls)
	}
	for _, forbidden := range []string{"redis://metadata-url-secret", "vol_attacker", "metadata_url", "volume_id"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), strings.ToLower(forbidden)) {
			t.Fatalf("raw storage field rejection leaked %q: %s", forbidden, rec.Body.String())
		}
	}
}

func TestProductCallerRuntimeBoundaryLifecycleDeletePurgeRedactProductSecrets(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		body     string
		idem     string
		wantType operations.OperationType
	}{
		{
			name:     "delete",
			path:     "/internal/v1/repos/repo_delete:delete",
			body:     `{"reason":"delete-reason-secret-canary"}`,
			idem:     "idem_lifecycle_delete_e2e",
			wantType: operations.OperationRepoDelete,
		},
		{
			name:     "purge",
			path:     "/internal/v1/repos/repo_purge:purge",
			body:     `{"reason":"purge-reason-secret-canary","product_confirmation_ref":"product-confirmation-secret-canary"}`,
			idem:     "idem_lifecycle_purge_e2e",
			wantType: operations.OperationRepoPurge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newCallerRuntimeBoundaryE2E(t, nil)
			rec := env.serve(callerRuntimeBoundaryE2ERequest(http.MethodPost, tt.path, tt.body, "product-caller", "ns_123", tt.idem))

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
			}
			envelope := decodeOperationEnvelope(t, rec.Body.Bytes())
			if envelope.OperationState != OperationStateQueued || envelope.Resource.Type != "repo" {
				t.Fatalf("operation envelope = %#v, want queued repo lifecycle operation", envelope)
			}
			if env.operationStore.spec.Scope.OperationType != tt.wantType || env.operationStore.spec.Phase != operations.OperationPhaseRepoLifecycleValidate {
				t.Fatalf("operation spec = %#v, want type %s lifecycle validate phase", env.operationStore.spec, tt.wantType)
			}
			summary := strings.ToLower(mustMarshalString(t, env.operationStore.spec.InputSummary))
			response := strings.ToLower(rec.Body.String())
			for _, forbidden := range []string{"delete-reason-secret-canary", "purge-reason-secret-canary", "product-confirmation-secret-canary"} {
				if strings.Contains(summary, forbidden) {
					t.Fatalf("lifecycle summary leaked %q: %s", forbidden, summary)
				}
				if strings.Contains(response, forbidden) {
					t.Fatalf("lifecycle response leaked %q: %s", forbidden, rec.Body.String())
				}
			}
			if !strings.Contains(summary, "reason_present") {
				t.Fatalf("lifecycle summary missing reason presence redaction: %s", summary)
			}
			if tt.wantType == operations.OperationRepoPurge && !strings.Contains(summary, "product_confirmation_present") {
				t.Fatalf("purge summary missing confirmation presence redaction: %s", summary)
			}
			assertProductCallerResponseDoesNotLeakStorage(t, rec.Body.String())
		})
	}
}

type callerRuntimeBoundaryE2E struct {
	handler        http.Handler
	operationStore *fakeOperationIntakeStore
}

type callerRuntimeBoundaryE2EConfig struct {
	planCalls *fakeWorkloadMountPlanReaderCalls
}

func newCallerRuntimeBoundaryE2E(t *testing.T, edit func(*callerRuntimeBoundaryE2EConfig)) callerRuntimeBoundaryE2E {
	t.Helper()
	config := callerRuntimeBoundaryE2EConfig{}
	if edit != nil {
		edit(&config)
	}

	now := fixedNamespaceNow()
	volume := resources.Volume{
		ID:             "vol_123",
		Backend:        resources.VolumeBackendJuiceFS,
		IsolationClass: resources.VolumeIsolationShared,
		Status:         resources.VolumeStatusActive,
		Capabilities:   map[string]any{"webdav_export": true, "workload_mount": true, "jvs_external_control_root": true, "directory_quota": false},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	repoActive := repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive)
	repoDelete := repoResourceFixture("ns_123", "repo_delete", resources.RepoStatusActive)
	repoPurge := repoResourceFixture("ns_123", "repo_purge", resources.RepoStatusTombstoned)
	repoPurge.Lifecycle = resources.RepoLifecycle{
		Status:                   resources.RepoStatusTombstoned,
		RetentionExpiresAt:       ptrTime(now.Add(-time.Hour)),
		LastLifecycleOperationID: "op_repo_delete",
		PreDeleteStatus:          resources.RepoStatusActive,
	}
	binding := namespacePolicyBindingFixture(
		"ns_123",
		resources.AllowedCaller{
			CallerService: "product-caller",
			Roles: []resources.CallerRole{
				resources.CallerRoleRepoAdmin,
				resources.CallerRoleRepoLifecycleAdmin,
				resources.CallerRoleExportAdmin,
				resources.CallerRoleMountAdmin,
			},
		},
		resources.AllowedCaller{CallerService: "runtime-orchestrator", Roles: []resources.CallerRole{resources.CallerRoleOrchestratorMount}},
	)
	mount := workloadmount.Binding{
		ID:             "wmb_123",
		NamespaceID:    "ns_123",
		RepoID:         "repo_123",
		VolumeID:       "vol_123",
		MountPath:      "/mnt/repo",
		ReadOnly:       true,
		Status:         sessionstate.MountStatusActive,
		LeaseSeconds:   120,
		LeaseExpiresAt: now.Add(time.Hour),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	plan := workloadmount.Plan{
		MountBindingID:      "wmb_123",
		VolumeID:            "vol_123",
		PayloadVolumeSubdir: "afscp/namespaces/ns_123/repos/repo_123/payload",
		MountPath:           "/mnt/repo",
		ReadOnly:            true,
		SecretRef:           workloadmount.SecretRef{Namespace: "runtime-secret-namespace", Name: "runtime-secret-volume"},
		SecurityPolicy:      workloadmount.SecurityPolicy{RunAsNonRoot: true, AllowPrivileged: false, JVSControlOutsidePayload: true},
	}
	operationStore := &fakeOperationIntakeStore{}
	operationSeq := 0
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:          callerRuntimeBoundaryE2EPrincipalResolver{},
		NamespaceBindingReader:     &fakeNamespaceVolumeBindingReader{binding: binding},
		NamespaceReader:            &fakeNamespaceReader{namespace: activeNamespaceFixture("ns_123")},
		RepoReader:                 &fakeRepoReader{repos: []resources.Repo{repoActive, repoDelete, repoPurge}},
		VolumeReader:               fakeWorkloadMountVolumeReader{volume: volume},
		WorkloadMountBindingReader: fakeWorkloadMountReader{binding: mount},
		WorkloadMountPlanReader:    fakeWorkloadMountPlanReader{plan: plan, calls: config.planCalls},
		ExportStore:                &fakeExportStore{},
		RepoFenceReader:            &fakeRepoFenceReader{},
		OperationIntakeStore:       operationStore,
		RepoCreateIntakeStore:      operationStore,
		GenerateOperationID: func() string {
			operationSeq++
			return fmt.Sprintf("op_e2e_%02d", operationSeq)
		},
		Now: fixedNamespaceNow,
	})
	return callerRuntimeBoundaryE2E{handler: handler, operationStore: operationStore}
}

func (env callerRuntimeBoundaryE2E) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	return rec
}

type callerRuntimeBoundaryE2EPrincipalResolver struct{}

func (callerRuntimeBoundaryE2EPrincipalResolver) ResolvePrincipal(r *http.Request) (auth.AuthenticatedPrincipal, error) {
	caller := strings.TrimSpace(r.Header.Get(auth.HeaderCallerService))
	return auth.AuthenticatedPrincipal{Subject: "svc:" + caller, CanonicalCallerService: caller}, nil
}

func callerRuntimeBoundaryE2ERequest(method, path, body, caller, namespaceID, idempotencyKey string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(auth.HeaderCallerService, caller)
	req.Header.Set(HeaderCorrelationID, "corr_e2e")
	if namespaceID != "" {
		req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	}
	if idempotencyKey != "" {
		req.Header.Set(auth.HeaderIdempotencyKey, idempotencyKey)
		req.Header.Set(auth.HeaderActorType, "user")
		req.Header.Set(auth.HeaderActorID, "user_123")
	}
	return req
}

func assertProductCallerResponseDoesNotLeakStorage(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{
		"metadata_url",
		"redis://",
		"juicefs mount",
		"bucket_access_key",
		"bucket_secret_key",
		"raw_mount_command",
		"aws_access_key_id",
		"aws_secret_access_key",
		"secret_ref",
		"persistentvolume",
		"persistent_volume",
		"persistentvolumeclaim",
		"persistent_volume_claim",
		"payload_volume_subdir",
		"control_volume_subdir",
		"control_root",
		"afscp/namespaces/ns_123/repos/repo_123/payload",
		"afscp/namespaces/ns_123/repos/repo_123/control",
		"runtime-secret-namespace",
		"runtime-secret-volume",
	} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(forbidden)) {
			t.Fatalf("product response leaked %q: %s", forbidden, body)
		}
	}
}

func assertRuntimeOrchestratorPlanDoesNotExposeControlRoot(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{
		"control_volume_subdir",
		"control_root",
		"control-volume",
		"afscp/namespaces/ns_123/repos/repo_123/control",
		"metadata_url",
		"juicefs mount",
		"bucket_access_key",
		"bucket_secret_key",
		"persistentvolumeclaim",
		"persistent_volume_claim",
	} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(forbidden)) {
			t.Fatalf("orchestrator plan leaked %q: %s", forbidden, body)
		}
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
