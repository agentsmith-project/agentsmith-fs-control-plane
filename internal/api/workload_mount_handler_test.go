package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"
)

func TestCreateWorkloadMountBindingQueuesOperationAndDoesNotLeakPlan(t *testing.T) {
	intake := &fakeOperationIntakeStore{}
	handler := workloadMountHandlerForTest(intake, namespaceBindingAllowedPolicy(auth.RoleMountAdmin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, workloadMountRequest(http.MethodPost, "/internal/v1/repos/repo_123/workload-mount-bindings", `{"mount_path":"/mnt/repo","read_only":false,"lease_seconds":120}`, "ns_123"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	if intake.calls != 1 || intake.spec.Scope.OperationType != operations.OperationMountBindingCreate || intake.spec.Phase != operations.OperationPhaseMountBindingCreateValidate {
		t.Fatalf("intake calls/spec = %d/%#v", intake.calls, intake.spec)
	}
	if intake.spec.MountBindingID != "wmb_123" || intake.spec.Resource.Type != "workload_mount_binding" || intake.spec.Resource.ID != "wmb_123" {
		t.Fatalf("mount resource = %q/%#v", intake.spec.MountBindingID, intake.spec.Resource)
	}
	if intake.spec.InputSummary["mount_path"] != "/mnt/repo" || intake.spec.InputSummary["volume_id"] != "vol_123" {
		t.Fatalf("summary = %#v", intake.spec.InputSummary)
	}
	assertWorkloadMountNoPlanLeak(t, rec.Body.String())
}

func TestCreateWorkloadMountBindingRejectsFilteredMountWithoutExternalControlRoot(t *testing.T) {
	for _, tt := range []struct {
		name     string
		readOnly bool
	}{
		{name: "read only", readOnly: true},
		{name: "read write", readOnly: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			meta := workloadMountMetaFixture()
			meta.volume.Capabilities = map[string]any{
				"webdav_export":             true,
				"workload_mount":            true,
				"filtered_mount":            true,
				"jvs_external_control_root": false,
				"directory_quota":           false,
			}
			intake := &fakeOperationIntakeStore{}
			handler := workloadMountHandlerWithMeta(intake, meta, namespaceBindingAllowedPolicy(auth.RoleMountAdmin))
			rec := httptest.NewRecorder()
			body := `{"mount_path":"/mnt/repo","read_only":false,"lease_seconds":120}`
			if tt.readOnly {
				body = `{"mount_path":"/mnt/repo","read_only":true,"lease_seconds":120}`
			}

			handler.ServeHTTP(rec, workloadMountRequest(http.MethodPost, "/internal/v1/repos/repo_123/workload-mount-bindings", body, "ns_123"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
			}
			if env := decodeErrorEnvelope(t, rec.Body.Bytes()); env.Error.Code != CodeRepoLifecycleInvalidState {
				t.Fatalf("error = %#v, want repo lifecycle invalid state", env.Error)
			}
			if intake.calls != 0 {
				t.Fatalf("intake calls = %d, want rejected before intake", intake.calls)
			}
			assertWorkloadMountNoPlanLeak(t, rec.Body.String())
		})
	}
}

func TestCreateWorkloadMountBindingRejectsDisabledNamespaceButReleaseStatusStayAvailable(t *testing.T) {
	now := fixedNamespaceNow()
	disabledAt := now
	meta := workloadMountMetaFixture()
	meta.namespace = resources.Namespace{ID: "ns_123", Status: resources.NamespaceStatusDisabled, DisabledReason: "security hold", DisabledAt: &disabledAt, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
	meta.mount.Status = sessionstate.MountStatusReleasing
	intake := &fakeOperationIntakeStore{}
	handler := workloadMountHandlerWithMeta(intake, meta, namespaceBindingAllowedPolicy(auth.RoleMountAdmin))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, workloadMountRequest(http.MethodPost, "/internal/v1/repos/repo_123/workload-mount-bindings", `{"mount_path":"/mnt/repo","read_only":false,"lease_seconds":120}`, "ns_123"))
	if rec.Code < 400 {
		t.Fatalf("create status = %d body = %s, want namespace disabled denial", rec.Code, rec.Body.String())
	}
	if env := decodeErrorEnvelope(t, rec.Body.Bytes()); env.Error.Code != CodeNamespaceDisabled {
		t.Fatalf("create error = %#v, want namespace disabled", env.Error)
	}
	if intake.calls != 0 {
		t.Fatalf("intake calls = %d, want create rejected before intake", intake.calls)
	}

	orchestratorConfig := workloadMountHandlerConfig(intake, fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "sandbox-orchestrator", Kind: auth.CallerKindOrchestrator, Roles: []auth.Role{auth.RoleOrchestratorMount}}}}, func(config *WorkloadMountHandlerConfig) {
		config.RepoReader = &fakeRepoReader{repos: []resources.Repo{meta.repo}}
		config.NamespaceReader = &fakeNamespaceReader{namespace: meta.namespace}
		config.BindingReader = &fakeNamespaceVolumeBindingReader{binding: meta.binding}
		config.VolumeReader = fakeWorkloadMountVolumeReader{volume: meta.volume}
		config.FenceReader = &fakeRepoFenceReader{fences: meta.fences}
		config.MountReader = fakeWorkloadMountReader{binding: meta.mount}
		config.PlanReader = fakeWorkloadMountPlanReader{plan: meta.plan}
	})
	orchestratorConfig.PrincipalResolver = fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{Subject: "svc:sandbox-orchestrator", CanonicalCallerService: "sandbox-orchestrator"}}
	orchestrator := WorkloadMountHandler(orchestratorConfig)

	rec = httptest.NewRecorder()
	orchestrator.ServeHTTP(rec, workloadMountRequestForCaller(http.MethodPost, "/internal/v1/workload-mount-bindings/wmb_123:release", "", "ns_123", "sandbox-orchestrator"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("release status = %d body = %s, want release intake preserved", rec.Code, rec.Body.String())
	}
	if intake.calls != 1 || intake.spec.Phase != operations.OperationPhaseMountBindingReleaseValidate || intake.spec.MountBindingID != "wmb_123" {
		t.Fatalf("release intake = calls %d spec %#v", intake.calls, intake.spec)
	}

	rec = httptest.NewRecorder()
	orchestrator.ServeHTTP(rec, workloadMountRequestForCaller(http.MethodPatch, "/internal/v1/workload-mount-bindings/wmb_123/status", `{"status":"released","observed_at":"2026-05-05T12:00:00Z","reason":"unmounted"}`, "ns_123", "sandbox-orchestrator"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status update = %d body = %s, want status intake preserved", rec.Code, rec.Body.String())
	}
	if intake.calls != 2 || intake.spec.Phase != operations.OperationPhaseMountBindingStatusValidate {
		t.Fatalf("status intake = calls %d spec %#v", intake.calls, intake.spec)
	}
}

func TestCreateWorkloadMountBindingRejectsCallerSuppliedVolumeID(t *testing.T) {
	intake := &fakeOperationIntakeStore{}
	handler := workloadMountHandlerForTest(intake, namespaceBindingAllowedPolicy(auth.RoleMountAdmin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, workloadMountRequest(http.MethodPost, "/internal/v1/repos/repo_123/workload-mount-bindings", `{"volume_id":"vol_other","mount_path":"/mnt/repo","read_only":true}`, "ns_123"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	if intake.calls != 0 {
		t.Fatalf("intake calls = %d, want rejected before intake", intake.calls)
	}
}

func TestCreateWorkloadMountBindingRequiresLeaseSecondsAtLeastMinimum(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "omitted", body: `{"mount_path":"/mnt/repo","read_only":true}`, want: http.StatusBadRequest},
		{name: "below minimum", body: `{"mount_path":"/mnt/repo","read_only":true,"lease_seconds":59}`, want: http.StatusBadRequest},
		{name: "minimum", body: `{"mount_path":"/mnt/repo","read_only":true,"lease_seconds":60}`, want: http.StatusAccepted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intake := &fakeOperationIntakeStore{}
			handler := workloadMountHandlerForTest(intake, namespaceBindingAllowedPolicy(auth.RoleMountAdmin))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, workloadMountRequest(http.MethodPost, "/internal/v1/repos/repo_123/workload-mount-bindings", tt.body, "ns_123"))

			if rec.Code != tt.want {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.want)
			}
		})
	}
}

func TestCreateWorkloadMountBindingRejectsWhitespacePaddedMountPath(t *testing.T) {
	intake := &fakeOperationIntakeStore{}
	handler := workloadMountHandlerForTest(intake, namespaceBindingAllowedPolicy(auth.RoleMountAdmin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, workloadMountRequest(http.MethodPost, "/internal/v1/repos/repo_123/workload-mount-bindings", `{"mount_path":" /mnt/repo ","read_only":true,"lease_seconds":120}`, "ns_123"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	if intake.calls != 0 {
		t.Fatalf("intake calls = %d, want rejected before intake", intake.calls)
	}
}

func TestCreateWorkloadMountBindingRejectsUnsafeMountPathBeforeIntakeAndAudits(t *testing.T) {
	intake := &fakeOperationIntakeStore{}
	sink := &fakeAuditSink{}
	config := workloadMountHandlerConfig(intake, namespaceBindingAllowedPolicy(auth.RoleMountAdmin), func(config *WorkloadMountHandlerConfig) {
		config.AuditSink = sink
	})
	rec := httptest.NewRecorder()

	WorkloadMountHandler(config).ServeHTTP(rec, workloadMountRequest(http.MethodPost, "/internal/v1/repos/repo_123/workload-mount-bindings", `{"mount_path":"/proc","read_only":true,"lease_seconds":120}`, "ns_123"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	if intake.calls != 0 {
		t.Fatalf("intake calls = %d, want rejected before intake", intake.calls)
	}
	if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("audit events = %#v, want one denied validation audit", sink.events)
	}
	assertWorkloadMountNoPlanLeak(t, rec.Body.String())
}

func TestWorkloadMountGetAndPlanRedactionBoundary(t *testing.T) {
	handler := workloadMountHandlerForTest(&fakeOperationIntakeStore{}, namespaceBindingAllowedPolicy(auth.RoleMountAdmin))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, workloadMountRequest(http.MethodGet, "/internal/v1/workload-mount-bindings/wmb_123", "", "ns_123"))
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"mount_path":"/mnt/repo"`) || strings.Contains(rec.Body.String(), "secret_ref") || strings.Contains(rec.Body.String(), "payload_volume_subdir") {
		t.Fatalf("get response crossed redaction boundary: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, workloadMountRequest(http.MethodGet, "/internal/v1/workload-mount-bindings/wmb_123/orchestrator-plan", "", "ns_123"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("plan as product status = %d body = %s, want role denial", rec.Code, rec.Body.String())
	}

	planCalls := &fakeWorkloadMountPlanReaderCalls{}
	orchestratorConfig := workloadMountHandlerConfig(&fakeOperationIntakeStore{}, fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "sandbox-orchestrator", Kind: auth.CallerKindOrchestrator, Roles: []auth.Role{auth.RoleOrchestratorMount}}}}, func(config *WorkloadMountHandlerConfig) {
		config.PlanReader = fakeWorkloadMountPlanReader{plan: workloadMountMetaFixture().plan, calls: planCalls}
	})
	orchestratorConfig.PrincipalResolver = fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{Subject: "svc:sandbox-orchestrator", CanonicalCallerService: "sandbox-orchestrator"}}
	orchestrator := WorkloadMountHandler(orchestratorConfig)
	rec = httptest.NewRecorder()
	orchestrator.ServeHTTP(rec, workloadMountRequestForCaller(http.MethodGet, "/internal/v1/workload-mount-bindings/wmb_123/orchestrator-plan", "", "ns_123", "sandbox-orchestrator"))
	if rec.Code != http.StatusOK {
		t.Fatalf("plan status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	var plan struct {
		MountBindingID      string `json:"mount_binding_id"`
		VolumeID            string `json:"volume_id"`
		PayloadVolumeSubdir string `json:"payload_volume_subdir"`
		MountPath           string `json:"mount_path"`
		ReadOnly            bool   `json:"read_only"`
		SecretRef           struct {
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
		} `json:"secret_ref"`
		SecurityPolicy struct {
			RunAsNonRoot             bool `json:"run_as_non_root"`
			AllowPrivileged          bool `json:"allow_privileged"`
			JVSControlOutsidePayload bool `json:"jvs_control_outside_payload"`
		} `json:"security_policy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan: %v: %s", err, rec.Body.String())
	}
	if plan.SecretRef.Namespace == "" || plan.SecretRef.Name == "" || !plan.SecurityPolicy.RunAsNonRoot || plan.SecurityPolicy.AllowPrivileged || !plan.SecurityPolicy.JVSControlOutsidePayload {
		t.Fatalf("plan schema mismatch: %#v body=%s", plan, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "secret_ref") || !strings.Contains(rec.Body.String(), "payload_volume_subdir") || strings.Contains(rec.Body.String(), "control_volume_subdir") || strings.Contains(rec.Body.String(), "control_root") {
		t.Fatalf("plan response shape/redaction wrong: %s", rec.Body.String())
	}
	if planCalls.namespaceID != "ns_123" || planCalls.mountBindingID != "wmb_123" {
		t.Fatalf("plan reader scope = %q/%q, want ns_123/wmb_123", planCalls.namespaceID, planCalls.mountBindingID)
	}
}

func TestWorkloadMountGetAndPlanDenyNamespaceMismatchWithoutPlanLeak(t *testing.T) {
	meta := workloadMountMetaFixture()
	meta.mount.NamespaceID = "ns_other"
	meta.plan.SecretRef = workloadmount.SecretRef{Namespace: "kube-secret-ns", Name: "secret-volume"}
	handler := workloadMountHandlerWithMeta(&fakeOperationIntakeStore{}, meta, namespaceBindingAllowedPolicy(auth.RoleMountAdmin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, workloadMountRequest(http.MethodGet, "/internal/v1/workload-mount-bindings/wmb_123", "", "ns_123"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("get status = %d body = %s, want namespace mismatch", rec.Code, rec.Body.String())
	}

	orchestratorConfig := workloadMountHandlerConfig(&fakeOperationIntakeStore{}, fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "sandbox-orchestrator", Kind: auth.CallerKindOrchestrator, Roles: []auth.Role{auth.RoleOrchestratorMount}}}}, func(config *WorkloadMountHandlerConfig) {
		config.MountReader = fakeWorkloadMountReader{binding: meta.mount}
		config.PlanReader = fakeWorkloadMountPlanReader{plan: meta.plan}
	})
	orchestratorConfig.PrincipalResolver = fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{Subject: "svc:sandbox-orchestrator", CanonicalCallerService: "sandbox-orchestrator"}}
	rec = httptest.NewRecorder()
	WorkloadMountHandler(orchestratorConfig).ServeHTTP(rec, workloadMountRequestForCaller(http.MethodGet, "/internal/v1/workload-mount-bindings/wmb_123/orchestrator-plan", "", "ns_123", "sandbox-orchestrator"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("plan status = %d body = %s, want namespace mismatch", rec.Code, rec.Body.String())
	}
	assertWorkloadMountNoPlanLeak(t, rec.Body.String())
}

func TestWorkloadMountPlanAuditUsesMinimalDetails(t *testing.T) {
	sink := &fakeAuditSink{}
	config := workloadMountHandlerConfig(&fakeOperationIntakeStore{}, fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "sandbox-orchestrator", Kind: auth.CallerKindOrchestrator, Roles: []auth.Role{auth.RoleOrchestratorMount}}}}, func(config *WorkloadMountHandlerConfig) {
		config.AuditSink = sink
		config.EventID = func() string { return "evt_mount_plan" }
	})
	config.PrincipalResolver = fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{Subject: "svc:sandbox-orchestrator", CanonicalCallerService: "sandbox-orchestrator"}}
	rec := httptest.NewRecorder()

	WorkloadMountHandler(config).ServeHTTP(rec, workloadMountRequestForCaller(http.MethodGet, "/internal/v1/workload-mount-bindings/wmb_123/orchestrator-plan", "", "ns_123", "sandbox-orchestrator"))

	if rec.Code != http.StatusOK {
		t.Fatalf("plan status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if len(sink.events) != 1 {
		t.Fatalf("audit events = %#v, want one mount plan audit", sink.events)
	}
	event := sink.events[0]
	if event.Type != audit.EventTypeMountPlanIssued || event.Resource.Type != "workload_mount_binding" || event.Resource.ID != "wmb_123" || event.Resource.NamespaceID != "ns_123" {
		t.Fatalf("audit event identity = %#v", event)
	}
	details := event.Details
	for key, want := range map[string]any{
		"mount_binding_id": "wmb_123",
		"namespace_id":     "ns_123",
		"repo_id":          "repo_123",
		"read_only":        true,
	} {
		if got := details[key]; got != want {
			t.Fatalf("audit detail %s = %#v, want %#v; details=%#v", key, got, want, details)
		}
	}
	for _, forbidden := range []string{"secret_ref", "secret_ref_namespace", "secret_ref_name", "secret_ref_name_present", "payload_volume_subdir", "mount_path", "volume_id", "control", "root_path"} {
		for key := range details {
			if strings.Contains(key, forbidden) {
				t.Fatalf("audit details leaked key %q matching forbidden %q: %#v", key, forbidden, details)
			}
		}
		rendered := strings.ToLower(auditEventString(t, event))
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("audit event leaked forbidden token %q: %s", forbidden, rendered)
		}
	}
}

func TestWorkloadMountAdmissionBlocksWriterFenceOnlyForReadWrite(t *testing.T) {
	intake := &fakeOperationIntakeStore{}
	meta := workloadMountMetaFixture()
	meta.fences = []fences.Fence{workloadMountWriterFence("op_writer")}
	handler := workloadMountHandlerWithMeta(intake, meta, namespaceBindingAllowedPolicy(auth.RoleMountAdmin))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, workloadMountRequest(http.MethodPost, "/internal/v1/repos/repo_123/workload-mount-bindings", `{"mount_path":"/mnt/rw","read_only":false,"lease_seconds":120}`, "ns_123"))
	if rec.Code != http.StatusConflict || intake.calls != 0 {
		t.Fatalf("rw status/calls = %d/%d, want conflict before intake", rec.Code, intake.calls)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, workloadMountRequest(http.MethodPost, "/internal/v1/repos/repo_123/workload-mount-bindings", `{"mount_path":"/mnt/ro","read_only":true,"lease_seconds":120}`, "ns_123"))
	if rec.Code != http.StatusAccepted || intake.calls != 1 {
		t.Fatalf("ro status/calls = %d/%d, want accepted", rec.Code, intake.calls)
	}
}

func TestWorkloadMountStatusRequiresObservedAtAndQueuesIt(t *testing.T) {
	intake := &fakeOperationIntakeStore{}
	config := workloadMountHandlerConfig(intake, fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "sandbox-orchestrator", Kind: auth.CallerKindOrchestrator, Roles: []auth.Role{auth.RoleOrchestratorMount}}}})
	config.PrincipalResolver = fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{Subject: "svc:sandbox-orchestrator", CanonicalCallerService: "sandbox-orchestrator"}}
	handler := WorkloadMountHandler(config)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, workloadMountRequestForCaller(http.MethodPatch, "/internal/v1/workload-mount-bindings/wmb_123/status", `{"status":"active"}`, "ns_123", "sandbox-orchestrator"))
	if rec.Code != http.StatusBadRequest || intake.calls != 0 {
		t.Fatalf("missing observed_at status/calls = %d/%d, want 400 before intake", rec.Code, intake.calls)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, workloadMountRequestForCaller(http.MethodPatch, "/internal/v1/workload-mount-bindings/wmb_123/status", `{"status":"active","observed_at":"2026-05-05T12:34:56Z","lease_expires_at":"2026-05-05T13:34:56Z","reason":"mounted"}`, "ns_123", "sandbox-orchestrator"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("valid status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	if intake.spec.InputSummary["observed_at"] != "2026-05-05T12:34:56Z" || intake.spec.InputSummary["lease_expires_at"] != "2026-05-05T13:34:56Z" {
		t.Fatalf("summary = %#v, want observed/lease timestamps", intake.spec.InputSummary)
	}
}

func TestWorkloadMountStatusRejectsNonOrchestratorStatuses(t *testing.T) {
	for _, status := range []string{"issued", "releasing"} {
		t.Run(status, func(t *testing.T) {
			intake := &fakeOperationIntakeStore{}
			config := workloadMountHandlerConfig(intake, fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "sandbox-orchestrator", Kind: auth.CallerKindOrchestrator, Roles: []auth.Role{auth.RoleOrchestratorMount}}}})
			config.PrincipalResolver = fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{Subject: "svc:sandbox-orchestrator", CanonicalCallerService: "sandbox-orchestrator"}}
			rec := httptest.NewRecorder()

			WorkloadMountHandler(config).ServeHTTP(rec, workloadMountRequestForCaller(http.MethodPatch, "/internal/v1/workload-mount-bindings/wmb_123/status", `{"status":"`+status+`","observed_at":"2026-05-05T12:34:56Z"}`, "ns_123", "sandbox-orchestrator"))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
			}
			if intake.calls != 0 {
				t.Fatalf("intake calls = %d, want rejected before intake", intake.calls)
			}
		})
	}
}

func TestWorkloadMountStatusRejectsReasonOverMaxLength(t *testing.T) {
	intake := &fakeOperationIntakeStore{}
	config := workloadMountHandlerConfig(intake, fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "sandbox-orchestrator", Kind: auth.CallerKindOrchestrator, Roles: []auth.Role{auth.RoleOrchestratorMount}}}})
	config.PrincipalResolver = fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{Subject: "svc:sandbox-orchestrator", CanonicalCallerService: "sandbox-orchestrator"}}
	rec := httptest.NewRecorder()
	body := `{"status":"active","observed_at":"2026-05-05T12:34:56Z","reason":"` + strings.Repeat("x", 1025) + `"}`

	WorkloadMountHandler(config).ServeHTTP(rec, workloadMountRequestForCaller(http.MethodPatch, "/internal/v1/workload-mount-bindings/wmb_123/status", body, "ns_123", "sandbox-orchestrator"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	if intake.calls != 0 {
		t.Fatalf("intake calls = %d, want rejected before intake", intake.calls)
	}
}

func workloadMountHandlerForTest(store *fakeOperationIntakeStore, policy AllowedCallerPolicy) http.Handler {
	return workloadMountHandlerWithMeta(store, workloadMountMetaFixture(), policy)
}

func workloadMountHandlerWithMeta(store *fakeOperationIntakeStore, meta workloadMountMeta, policy AllowedCallerPolicy) http.Handler {
	return WorkloadMountHandler(workloadMountHandlerConfig(store, policy, func(config *WorkloadMountHandlerConfig) {
		config.RepoReader = &fakeRepoReader{repos: []resources.Repo{meta.repo}}
		config.NamespaceReader = &fakeNamespaceReader{namespace: meta.namespace}
		config.BindingReader = &fakeNamespaceVolumeBindingReader{binding: meta.binding}
		config.VolumeReader = fakeWorkloadMountVolumeReader{volume: meta.volume}
		config.FenceReader = &fakeRepoFenceReader{fences: meta.fences}
		config.MountReader = fakeWorkloadMountReader{binding: meta.mount}
		config.PlanReader = fakeWorkloadMountPlanReader{plan: meta.plan}
	}))
}

func workloadMountHandlerConfig(store *fakeOperationIntakeStore, policy AllowedCallerPolicy, edits ...func(*WorkloadMountHandlerConfig)) WorkloadMountHandlerConfig {
	meta := workloadMountMetaFixture()
	config := WorkloadMountHandlerConfig{
		RepoReader:        &fakeRepoReader{repos: []resources.Repo{meta.repo}},
		NamespaceReader:   &fakeNamespaceReader{namespace: meta.namespace},
		BindingReader:     &fakeNamespaceVolumeBindingReader{binding: meta.binding},
		VolumeReader:      fakeWorkloadMountVolumeReader{volume: meta.volume},
		FenceReader:       &fakeRepoFenceReader{},
		MountReader:       fakeWorkloadMountReader{binding: meta.mount},
		PlanReader:        fakeWorkloadMountPlanReader{plan: meta.plan},
		IntakeStore:       store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    policy,
		OperationID:       func() string { return "op_mount" },
		MountBindingID:    func() string { return "wmb_123" },
		Now:               fixedNamespaceNow,
	}
	for _, edit := range edits {
		edit(&config)
	}
	return config
}

type workloadMountMeta struct {
	repo      resources.Repo
	namespace resources.Namespace
	binding   resources.NamespaceVolumeBinding
	volume    resources.Volume
	mount     workloadmount.Binding
	plan      workloadmount.Plan
	fences    []fences.Fence
}

func workloadMountMetaFixture() workloadMountMeta {
	now := fixedNamespaceNow()
	return workloadMountMeta{
		repo:      repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive),
		namespace: activeNamespaceFixture("ns_123"),
		binding:   namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "agentsmith-api", Roles: []resources.CallerRole{resources.CallerRoleMountAdmin}}),
		volume: resources.Volume{
			ID:             "vol_123",
			Backend:        resources.VolumeBackendJuiceFS,
			IsolationClass: resources.VolumeIsolationShared,
			Status:         resources.VolumeStatusActive,
			Capabilities:   map[string]any{"webdav_export": true, "workload_mount": true, "jvs_external_control_root": true, "directory_quota": false},
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		mount: workloadmount.Binding{ID: "wmb_123", NamespaceID: "ns_123", RepoID: "repo_123", VolumeID: "vol_123", MountPath: "/mnt/repo", ReadOnly: true, Status: sessionstate.MountStatusActive, LeaseSeconds: 120, LeaseExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now},
		plan:  workloadmount.Plan{MountBindingID: "wmb_123", VolumeID: "vol_123", PayloadVolumeSubdir: "afscp/namespaces/ns_123/repos/repo_123/payload", MountPath: "/mnt/repo", ReadOnly: true, SecretRef: workloadmount.SecretRef{Namespace: "kube-secret-ns", Name: "secret-volume"}, SecurityPolicy: workloadmount.SecurityPolicy{RunAsNonRoot: true, AllowPrivileged: false, JVSControlOutsidePayload: true}},
	}
}

type fakeWorkloadMountVolumeReader struct{ volume resources.Volume }

func (reader fakeWorkloadMountVolumeReader) GetVolume(context.Context, string) (resources.Volume, error) {
	return reader.volume, nil
}

type fakeWorkloadMountReader struct{ binding workloadmount.Binding }

func (reader fakeWorkloadMountReader) GetWorkloadMountBinding(context.Context, string) (workloadmount.Binding, error) {
	return reader.binding, nil
}

type fakeWorkloadMountPlanReaderCalls struct {
	namespaceID    string
	mountBindingID string
}

type fakeWorkloadMountPlanReader struct {
	plan  workloadmount.Plan
	calls *fakeWorkloadMountPlanReaderCalls
}

func (reader fakeWorkloadMountPlanReader) GetOrchestratorMountPlan(_ context.Context, namespaceID, mountBindingID string) (workloadmount.Plan, error) {
	if reader.calls != nil {
		reader.calls.namespaceID = namespaceID
		reader.calls.mountBindingID = mountBindingID
	}
	return reader.plan, nil
}

func workloadMountRequest(method, path, body, namespaceID string) *http.Request {
	return workloadMountRequestForCaller(method, path, body, namespaceID, "agentsmith-api")
}

func workloadMountRequestForCaller(method, path, body, namespaceID, authCaller string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(auth.HeaderAuthorization, "Bearer token")
	req.Header.Set(auth.HeaderCallerService, authCaller)
	req.Header.Set(auth.HeaderIdempotencyKey, "idem_mount")
	req.Header.Set(HeaderCorrelationID, "corr_mount")
	req.Header.Set(auth.HeaderActorType, "user")
	req.Header.Set(auth.HeaderActorID, "user_123")
	if namespaceID != "" {
		req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	}
	return req
}

func workloadMountWriterFence(operationID string) fences.Fence {
	now := fixedNamespaceNow()
	return fences.Fence{ID: "fence_writer", RepoID: "repo_123", Kind: fences.KindWriterSession, HolderOperationID: operationID, Status: fences.StatusActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
}

func assertWorkloadMountNoPlanLeak(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"secret_ref", "payload_volume_subdir", "control", "metadata_url", "root_path"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("workload mount response leaked %q: %s", forbidden, body)
		}
	}
}
