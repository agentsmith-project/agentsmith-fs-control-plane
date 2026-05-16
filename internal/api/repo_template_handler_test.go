package api

import (
	"context"
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

func TestCreateRepoTemplateHandlerQueuesOperationAfterPolicyAndMutationGates(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	meta := repoTemplateMetaFixture()
	handler := repoTemplateHandlerForTest(store, meta)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoTemplateRequest(http.MethodPost, "/internal/v1/repo-templates", "ns_123", `{"namespace_id":"ns_123","source_repo_id":"repo_123","target_template_id":"tmpl_base01","clone_history_mode":"main"}`))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	if store.calls != 1 || store.jvsMutationCalls != 1 {
		t.Fatalf("store calls intake/mutation = %d/%d, want 1/1", store.calls, store.jvsMutationCalls)
	}
	spec := store.spec
	if spec.Scope.OperationType != operations.OperationTemplateCreate || spec.RepoID != "repo_123" || spec.TemplateID != "tmpl_base01" || spec.Resource.Type != "repo_template" {
		t.Fatalf("spec = %#v, want source repo scoped template_create", spec)
	}
	if spec.InputSummary["clone_history_mode"] != "main" {
		t.Fatalf("input summary = %#v, want GA clone_history_mode main", spec.InputSummary)
	}
}

func TestCreateRepoTemplateMutationGateUsesProductSafeBlockingError(t *testing.T) {
	store := &fakeOperationIntakeStore{jvsMutation: true}
	meta := repoTemplateMetaFixture()
	handler := repoTemplateHandlerForTest(store, meta)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoTemplateRequest(http.MethodPost, "/internal/v1/repo-templates", "ns_123", `{"namespace_id":"ns_123","source_repo_id":"repo_123","target_template_id":"tmpl_base01","clone_history_mode":"main"}`))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeFileLibraryOperationPending || !env.Error.Retryable {
		t.Fatalf("error = %#v, want retryable FILE_LIBRARY_OPERATION_PENDING", env.Error)
	}
	assertBlockingOperationErrorProductSafe(t, rec.Body.String(), env, false)
	if store.calls != 0 || store.jvsMutationCalls != 1 {
		t.Fatalf("intake/mutation calls = %d/%d, want mutation gate before operation create", store.calls, store.jvsMutationCalls)
	}
}

func TestRepoTemplateCreateRejectsDisabledNamespaceBeforeIntakeAndAudits(t *testing.T) {
	tests := []struct {
		name string
		edit func(*repoTemplateMeta)
	}{
		{name: "namespace disabled", edit: func(meta *repoTemplateMeta) {
			meta.namespace = disabledNamespaceFixture("ns_123", "raw secret reason password=secret")
		}},
		{name: "namespace disabled beats template policy disabled", edit: func(meta *repoTemplateMeta) {
			meta.namespace = disabledNamespaceFixture("ns_123", "raw secret reason password=secret")
			meta.binding.TemplatePolicy["namespace_templates_enabled"] = false
		}},
		{name: "namespace disabled beats volume mismatch", edit: func(meta *repoTemplateMeta) {
			meta.namespace = disabledNamespaceFixture("ns_123", "raw secret reason password=secret")
			meta.repoReader.repos[0].VolumeID = "vol_other01"
		}},
		{name: "binding disabled beats template policy disabled", edit: func(meta *repoTemplateMeta) {
			meta.binding.Status = resources.NamespaceStatusDisabled
			meta.binding.TemplatePolicy["namespace_templates_enabled"] = false
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			meta := repoTemplateMetaFixture()
			tt.edit(&meta)
			meta.bindingReader = &fakeNamespaceVolumeBindingReader{binding: meta.binding}
			sink := &fakeAuditSink{}
			handler := repoTemplateHandlerForTestWithAudit(store, meta, sink)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoTemplateRequest(http.MethodPost, "/internal/v1/repo-templates", "ns_123", `{"namespace_id":"ns_123","source_repo_id":"repo_123","target_template_id":"tmpl_base01","clone_history_mode":"main"}`))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != CodeNamespaceDisabled {
				t.Fatalf("error code = %s, want %s", env.Error.Code, CodeNamespaceDisabled)
			}
			if store.calls != 0 {
				t.Fatalf("intake calls = %d, want rejected before operation create", store.calls)
			}
			if store.jvsMutationCalls != 0 {
				t.Fatalf("mutation gate calls = %d, want disabled namespace rejected before JVS gate", store.jvsMutationCalls)
			}
			assertRepoTemplateResponseDoesNotLeak(t, rec.Body.String())
			assertDisabledNamespaceDenialAuditDoesNotLeak(t, sink)
		})
	}
}

func TestCreateRepoTemplateReusesIdempotentOperationBeforeMetadataReads(t *testing.T) {
	canonical := createRepoTemplateCanonicalRequest{NamespaceID: "ns_123", SourceRepoID: "repo_123", TargetTemplateID: "tmpl_base01", CloneHistoryMode: "main"}
	hash, err := operations.HashRequest(canonical)
	if err != nil {
		t.Fatalf("hash canonical: %v", err)
	}
	existing := templateOperationRecord("op_existing_template", operations.OperationTemplateCreate, hash)
	store := &fakeOperationIntakeStore{lookupRecord: &existing}
	meta := repoTemplateMetaFixture()
	handler := repoTemplateHandlerForTest(store, meta)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoTemplateRequest(http.MethodPost, "/internal/v1/repo-templates", "ns_123", `{"namespace_id":"ns_123","source_repo_id":"repo_123","target_template_id":"tmpl_base01","clone_history_mode":"main"}`))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	if store.lookupCalls != 1 || store.calls != 0 || meta.repoReader.getInNamespaceCalls != 0 || meta.bindingReader.calls != 0 {
		t.Fatalf("calls lookup/intake/repo/binding = %d/%d/%d/%d, want idempotency first only", store.lookupCalls, store.calls, meta.repoReader.getInNamespaceCalls, meta.bindingReader.calls)
	}
}

func TestRepoTemplateIdempotencyLookupOutageAuditsDeniedBeforeMetadata(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "create", path: "/internal/v1/repo-templates", body: `{"namespace_id":"ns_123","source_repo_id":"repo_123","target_template_id":"tmpl_base01","clone_history_mode":"main"}`},
		{name: "clone", path: "/internal/v1/repo-templates/tmpl_base01:clone", body: `{"namespace_id":"ns_123","template_id":"tmpl_base01","target_repo_id":"repo_clone01"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{lookupErr: errors.New("postgres password=secret failed")}
			meta := repoTemplateMetaFixture()
			sink := &fakeAuditSink{}
			handler := repoTemplateHandlerForTestWithAudit(store, meta, sink)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoTemplateRequest(http.MethodPost, tt.path, "ns_123", tt.body))

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d body = %s, want 503", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != CodeStorageUnavailable || !env.Error.Retryable {
				t.Fatalf("error = %#v, want STORAGE_UNAVAILABLE retryable", env.Error)
			}
			if !auditValidationErrorsContain(env.Error.Details["validation_errors"], "idempotency_lookup_unavailable") {
				t.Fatalf("response validation_errors = %#v, want idempotency_lookup_unavailable", env.Error.Details["validation_errors"])
			}
			if store.lookupCalls != 1 || store.calls != 0 || store.jvsMutationCalls != 0 || meta.repoReader.getInNamespaceCalls != 0 || meta.bindingReader.calls != 0 || meta.fenceReader.calls != 0 {
				t.Fatalf("calls lookup/intake/mutation/repo/binding/fence = %d/%d/%d/%d/%d/%d, want lookup denial before metadata", store.lookupCalls, store.calls, store.jvsMutationCalls, meta.repoReader.getInNamespaceCalls, meta.bindingReader.calls, meta.fenceReader.calls)
			}
			if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied {
				t.Fatalf("audit events = %#v, want denied audit", sink.events)
			}
			if sink.events[0].Details["error_code"] != string(CodeStorageUnavailable) {
				t.Fatalf("audit error_code = %#v, want %s", sink.events[0].Details["error_code"], CodeStorageUnavailable)
			}
			if !auditValidationErrorsContain(sink.events[0].Details["validation_errors"], "idempotency_lookup_unavailable") {
				t.Fatalf("audit validation_errors = %#v, want idempotency_lookup_unavailable", sink.events[0].Details["validation_errors"])
			}
			renderedAudit := auditEventString(t, sink.events[0])
			for _, leaked := range []string{"postgres", "password=secret"} {
				if strings.Contains(rec.Body.String(), leaked) || strings.Contains(renderedAudit, leaked) {
					t.Fatalf("lookup outage leaked %q response=%s audit=%s", leaked, rec.Body.String(), renderedAudit)
				}
			}
		})
	}
}

func TestRepoTemplateAdmissionDisabledReplaysExistingOperationsBeforeMetadata(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		body          string
		canonical     any
		operationType operations.OperationType
	}{
		{
			name:          "create",
			path:          "/internal/v1/repo-templates",
			body:          `{"namespace_id":"ns_123","source_repo_id":"repo_123","target_template_id":"tmpl_base01","clone_history_mode":"main"}`,
			canonical:     createRepoTemplateCanonicalRequest{NamespaceID: "ns_123", SourceRepoID: "repo_123", TargetTemplateID: "tmpl_base01", CloneHistoryMode: "main"},
			operationType: operations.OperationTemplateCreate,
		},
		{
			name:          "clone",
			path:          "/internal/v1/repo-templates/tmpl_base01:clone",
			body:          `{"namespace_id":"ns_123","template_id":"tmpl_base01","target_repo_id":"repo_clone01"}`,
			canonical:     cloneRepoTemplateCanonicalRequest{NamespaceID: "ns_123", SourceNamespaceID: "ns_123", TemplateID: "tmpl_base01", TargetRepoID: "repo_clone01"},
			operationType: operations.OperationTemplateClone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, err := operations.HashRequest(tt.canonical)
			if err != nil {
				t.Fatalf("hash canonical: %v", err)
			}
			existing := templateOperationRecord("op_existing_template", tt.operationType, hash)
			store := &fakeOperationIntakeStore{lookupRecord: &existing}
			meta := repoTemplateMetaFixture()
			handler := repoTemplateHandlerForTestWithOptions(store, meta, nil, true)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoTemplateRequest(http.MethodPost, tt.path, "ns_123", tt.body))

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
			}
			if store.lookupCalls != 1 || store.calls != 0 || store.jvsMutationCalls != 0 || meta.repoReader.getInNamespaceCalls != 0 || meta.bindingReader.calls != 0 || meta.fenceReader.calls != 0 {
				t.Fatalf("calls lookup/intake/mutation/repo/binding/fence = %d/%d/%d/%d/%d/%d, want replay only", store.lookupCalls, store.calls, store.jvsMutationCalls, meta.repoReader.getInNamespaceCalls, meta.bindingReader.calls, meta.fenceReader.calls)
			}
		})
	}
}

func TestRepoTemplateAdmissionDisabledRejectsNewOperationsBeforeMetadataAndAudits(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "create", path: "/internal/v1/repo-templates", body: `{"namespace_id":"ns_123","source_repo_id":"repo_123","target_template_id":"tmpl_base01","clone_history_mode":"main"}`},
		{name: "clone", path: "/internal/v1/repo-templates/tmpl_base01:clone", body: `{"namespace_id":"ns_123","template_id":"tmpl_base01","target_repo_id":"repo_clone01"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			meta := repoTemplateMetaFixture()
			sink := &fakeAuditSink{}
			handler := repoTemplateHandlerForTestWithOptions(store, meta, sink, true)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoTemplateRequest(http.MethodPost, tt.path, "ns_123", tt.body))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d body = %s, want 403", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != CodeCapabilityDenied {
				t.Fatalf("error code = %s, want %s", env.Error.Code, CodeCapabilityDenied)
			}
			if store.lookupCalls != 1 || store.calls != 0 || store.jvsMutationCalls != 0 || meta.repoReader.getInNamespaceCalls != 0 || meta.bindingReader.calls != 0 || meta.fenceReader.calls != 0 {
				t.Fatalf("calls lookup/intake/mutation/repo/binding/fence = %d/%d/%d/%d/%d/%d, want lookup then deny only", store.lookupCalls, store.calls, store.jvsMutationCalls, meta.repoReader.getInNamespaceCalls, meta.bindingReader.calls, meta.fenceReader.calls)
			}
			if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied {
				t.Fatalf("audit events = %#v, want one denied audit", sink.events)
			}
			assertRepoTemplateAdmissionDisabledAudit(t, sink.events[0], "repo_template_admission_disabled")
		})
	}
}

func TestRepoTemplateAdmissionDisabledReturnsIdempotencyConflictBeforeCapabilityDenied(t *testing.T) {
	existing := templateOperationRecord("op_existing_template", operations.OperationTemplateCreate, operations.RequestHash("sha256:different"))
	store := &fakeOperationIntakeStore{lookupRecord: &existing}
	meta := repoTemplateMetaFixture()
	sink := &fakeAuditSink{}
	handler := repoTemplateHandlerForTestWithOptions(store, meta, sink, true)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoTemplateRequest(http.MethodPost, "/internal/v1/repo-templates", "ns_123", `{"namespace_id":"ns_123","source_repo_id":"repo_123","target_template_id":"tmpl_base01","clone_history_mode":"main"}`))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeIdempotencyConflict {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeIdempotencyConflict)
	}
	if store.calls != 0 || meta.repoReader.getInNamespaceCalls != 0 {
		t.Fatalf("intake/repo calls = %d/%d, want conflict before metadata", store.calls, meta.repoReader.getInNamespaceCalls)
	}
	if len(sink.events) != 0 {
		t.Fatalf("audit events = %#v, want no capability denied audit on idempotency conflict", sink.events)
	}
}

func TestRepoTemplateAdmissionDisabledWithoutLookupStoreFailsClosedBeforeMetadata(t *testing.T) {
	store := &fakeTemplateIntakeStoreWithoutLookup{}
	meta := repoTemplateMetaFixture()
	sink := &fakeAuditSink{}
	handler := RepoTemplateHandler(RepoTemplateHandlerConfig{
		RepoReader:        meta.repoReader,
		NamespaceReader:   &fakeNamespaceReader{namespace: meta.namespace},
		BindingReader:     meta.bindingReader,
		FenceReader:       meta.fenceReader,
		MutationGate:      &fakeOperationIntakeStore{},
		IntakeStore:       store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    namespaceBindingAllowedPolicy(auth.RoleTemplateAdmin),
		AdmissionDisabled: true,
		AuditSink:         sink,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoTemplateRequest(http.MethodPost, "/internal/v1/repo-templates", "ns_123", `{"namespace_id":"ns_123","source_repo_id":"repo_123","target_template_id":"tmpl_base01","clone_history_mode":"main"}`))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeCapabilityDenied {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeCapabilityDenied)
	}
	if store.calls != 0 || meta.repoReader.getInNamespaceCalls != 0 || meta.bindingReader.calls != 0 || meta.fenceReader.calls != 0 {
		t.Fatalf("calls intake/repo/binding/fence = %d/%d/%d/%d, want fail-closed before metadata", store.calls, meta.repoReader.getInNamespaceCalls, meta.bindingReader.calls, meta.fenceReader.calls)
	}
	if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("audit events = %#v, want denied audit", sink.events)
	}
}

func TestCloneRepoTemplateRejectsCrossNamespaceEvenWhenPolicyFlagExists(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	meta := repoTemplateMetaFixture()
	meta.binding.TemplatePolicy["cross_namespace_clone_enabled"] = true
	handler := repoTemplateHandlerForTest(store, meta)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoTemplateRequest(http.MethodPost, "/internal/v1/repo-templates/tmpl_base01:clone", "ns_123", `{"namespace_id":"ns_123","source_namespace_id":"ns_other","template_id":"tmpl_base01","target_repo_id":"repo_clone01"}`))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", rec.Code, rec.Body.String())
	}
	if store.calls != 0 || meta.repoReader.getInNamespaceCalls != 0 {
		t.Fatalf("calls intake/repo = %d/%d, want reject before store metadata", store.calls, meta.repoReader.getInNamespaceCalls)
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeResourceNamespaceMismatch {
		t.Fatalf("error code = %s, want RESOURCE_NAMESPACE_MISMATCH", env.Error.Code)
	}
}

func TestCloneRepoTemplateRejectsCrossVolumeTemplate(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	meta := repoTemplateMetaFixture()
	meta.template.VolumeID = "vol_other01"
	meta.repoReader.repos[1] = meta.template
	handler := repoTemplateHandlerForTest(store, meta)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoTemplateRequest(http.MethodPost, "/internal/v1/repo-templates/tmpl_base01:clone", "ns_123", `{"namespace_id":"ns_123","template_id":"tmpl_base01","target_repo_id":"repo_clone01"}`))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	if store.calls != 0 {
		t.Fatalf("intake calls = %d, want reject before operation create", store.calls)
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeVolumeMismatchRequiresImport {
		t.Fatalf("error code = %s, want VOLUME_MISMATCH_REQUIRES_IMPORT", env.Error.Code)
	}
}

func TestRepoTemplateCloneRejectsDisabledNamespaceBeforeIntakeAndAudits(t *testing.T) {
	tests := []struct {
		name string
		edit func(*repoTemplateMeta)
	}{
		{name: "namespace disabled", edit: func(meta *repoTemplateMeta) {
			meta.namespace = disabledNamespaceFixture("ns_123", "raw secret reason password=secret")
		}},
		{name: "namespace disabled beats template policy disabled", edit: func(meta *repoTemplateMeta) {
			meta.namespace = disabledNamespaceFixture("ns_123", "raw secret reason password=secret")
			meta.binding.TemplatePolicy["namespace_templates_enabled"] = false
		}},
		{name: "namespace disabled beats volume mismatch", edit: func(meta *repoTemplateMeta) {
			meta.namespace = disabledNamespaceFixture("ns_123", "raw secret reason password=secret")
			meta.template.VolumeID = "vol_other01"
			meta.repoReader.repos[1] = meta.template
		}},
		{name: "binding disabled beats template policy disabled", edit: func(meta *repoTemplateMeta) {
			meta.binding.Status = resources.NamespaceStatusDisabled
			meta.binding.TemplatePolicy["namespace_templates_enabled"] = false
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			meta := repoTemplateMetaFixture()
			tt.edit(&meta)
			meta.bindingReader = &fakeNamespaceVolumeBindingReader{binding: meta.binding}
			sink := &fakeAuditSink{}
			handler := repoTemplateHandlerForTestWithAudit(store, meta, sink)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoTemplateRequest(http.MethodPost, "/internal/v1/repo-templates/tmpl_base01:clone", "ns_123", `{"namespace_id":"ns_123","template_id":"tmpl_base01","target_repo_id":"repo_clone01"}`))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != CodeNamespaceDisabled {
				t.Fatalf("error code = %s, want %s", env.Error.Code, CodeNamespaceDisabled)
			}
			if store.calls != 0 {
				t.Fatalf("intake calls = %d, want rejected before operation create", store.calls)
			}
			assertRepoTemplateResponseDoesNotLeak(t, rec.Body.String())
			assertDisabledNamespaceDenialAuditDoesNotLeak(t, sink)
		})
	}
}

func TestCreateRepoTemplateRequiresCloneHistoryModeMain(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing", body: `{"namespace_id":"ns_123","source_repo_id":"repo_123","target_template_id":"tmpl_base01"}`},
		{name: "unsupported", body: `{"namespace_id":"ns_123","source_repo_id":"repo_123","target_template_id":"tmpl_base01","clone_history_mode":"all"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			meta := repoTemplateMetaFixture()
			handler := repoTemplateHandlerForTest(store, meta)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoTemplateRequest(http.MethodPost, "/internal/v1/repo-templates", "ns_123", tt.body))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
			}
			if store.calls != 0 || meta.repoReader.getInNamespaceCalls != 0 {
				t.Fatalf("intake/repo calls = %d/%d, want validation before metadata", store.calls, meta.repoReader.getInNamespaceCalls)
			}
		})
	}
}

func TestCloneRepoTemplateRequiresBodyTemplateIDAndPathMatch(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing", body: `{"namespace_id":"ns_123","target_repo_id":"repo_clone01"}`},
		{name: "path body mismatch", body: `{"namespace_id":"ns_123","template_id":"tmpl_other01","target_repo_id":"repo_clone01"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			meta := repoTemplateMetaFixture()
			handler := repoTemplateHandlerForTest(store, meta)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoTemplateRequest(http.MethodPost, "/internal/v1/repo-templates/tmpl_base01:clone", "ns_123", tt.body))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
			}
			if store.calls != 0 || meta.repoReader.getInNamespaceCalls != 0 {
				t.Fatalf("intake/repo calls = %d/%d, want validation before metadata", store.calls, meta.repoReader.getInNamespaceCalls)
			}
		})
	}
}

type repoTemplateMeta struct {
	repoReader    *fakeRepoReader
	namespace     resources.Namespace
	binding       resources.NamespaceVolumeBinding
	bindingReader *fakeNamespaceVolumeBindingReader
	fenceReader   *fakeRepoFenceReader
	template      resources.Repo
}

func repoTemplateMetaFixture() repoTemplateMeta {
	now := fixedNamespaceNow()
	binding := namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleTemplateAdmin}})
	template := resources.Repo{
		ID:                  "tmpl_base01",
		NamespaceID:         "ns_123",
		VolumeID:            "vol_123",
		JVSRepoID:           "jvs_template_alpha",
		Kind:                resources.RepoKindTemplate,
		Status:              resources.RepoStatusActive,
		ControlVolumeSubdir: "afscp/namespaces/ns_123/templates/tmpl_base01/control",
		PayloadVolumeSubdir: "afscp/namespaces/ns_123/templates/tmpl_base01/payload",
		Lifecycle:           resources.RepoLifecycle{Status: resources.RepoStatusActive},
		CreatedAt:           now,
		UpdatedAt:           now.Add(time.Minute),
	}
	repoReader := &fakeRepoReader{repos: []resources.Repo{repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive), template}}
	return repoTemplateMeta{repoReader: repoReader, namespace: activeNamespaceFixture("ns_123"), binding: binding, bindingReader: &fakeNamespaceVolumeBindingReader{binding: binding}, fenceReader: &fakeRepoFenceReader{}, template: template}
}

func repoTemplateHandlerForTest(store *fakeOperationIntakeStore, meta repoTemplateMeta) http.Handler {
	return repoTemplateHandlerForTestWithAudit(store, meta, nil)
}

func repoTemplateHandlerForTestWithAudit(store *fakeOperationIntakeStore, meta repoTemplateMeta, sink *fakeAuditSink) http.Handler {
	return repoTemplateHandlerForTestWithOptions(store, meta, sink, false)
}

func repoTemplateHandlerForTestWithOptions(store *fakeOperationIntakeStore, meta repoTemplateMeta, sink *fakeAuditSink, admissionDisabled bool) http.Handler {
	if meta.bindingReader == nil {
		meta.bindingReader = &fakeNamespaceVolumeBindingReader{binding: meta.binding}
	}
	var auditSink audit.Sink
	if sink != nil {
		auditSink = sink
	}
	return RepoTemplateHandler(RepoTemplateHandlerConfig{
		RepoReader:        meta.repoReader,
		NamespaceReader:   &fakeNamespaceReader{namespace: meta.namespace},
		BindingReader:     meta.bindingReader,
		FenceReader:       meta.fenceReader,
		MutationGate:      store,
		IntakeStore:       store,
		IntakeLookupStore: store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    namespaceBindingAllowedPolicy(auth.RoleTemplateAdmin),
		OperationID:       func() string { return "op_template" },
		Now:               fixedNamespaceNow,
		AdmissionDisabled: admissionDisabled,
		AuditSink:         auditSink,
	})
}

func repoTemplateRequest(method, path, namespaceID, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_template")
	req.Header.Set(auth.HeaderCallerService, "product-caller")
	req.Header.Set(auth.HeaderIdempotencyKey, "idem_template")
	req.Header.Set(auth.HeaderActorType, "user")
	req.Header.Set(auth.HeaderActorID, "user_123")
	if namespaceID != "" {
		req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	}
	return req
}

func templateOperationRecord(operationID string, typ operations.OperationType, hash operations.RequestHash) operations.OperationRecord {
	now := fixedNamespaceNow()
	phase := operations.OperationPhaseTemplateCreateValidate
	resource := operations.ResourceRef{Type: "repo_template", ID: "tmpl_base01"}
	repoID := "repo_123"
	if typ == operations.OperationTemplateClone {
		phase = operations.OperationPhaseTemplateCloneValidate
		resource = operations.ResourceRef{Type: "repo", ID: "repo_clone01"}
		repoID = "repo_clone01"
	}
	return operations.OperationRecord{ID: operationID, Type: typ, State: operations.OperationStateQueued, Phase: phase, IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_123", typ, "idem_template").String(), IdempotencyKey: "idem_template", RequestHash: hash, CorrelationID: "corr_template", CallerService: "product-caller", AuthorizedActor: operations.Actor{Type: "user", ID: "user_123"}, Resource: resource, NamespaceID: "ns_123", RepoID: repoID, TemplateID: "tmpl_base01", ExternalResourceIDs: map[string]string{}, InputSummary: map[string]any{}, CreatedAt: now}
}

type fakeTemplateIntakeStoreWithoutLookup struct {
	calls int
}

func (store *fakeTemplateIntakeStoreWithoutLookup) CreateOrReuseTemplateCreateOperation(_ context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.calls++
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	return operations.IdempotencyResolution{Operation: record.Sanitized()}, nil
}

func (store *fakeTemplateIntakeStoreWithoutLookup) CreateOrReuseTemplateCloneOperation(_ context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.calls++
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	return operations.IdempotencyResolution{Operation: record.Sanitized()}, nil
}

func assertRepoTemplateResponseDoesNotLeak(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"password=secret", "raw secret reason", "/srv", "control_root", "payload_root", "raw_path", "token=", "bearer "} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("repo template response leaked %q: %s", forbidden, body)
		}
	}
}

func assertRepoTemplateAdmissionDisabledAudit(t *testing.T, event audit.Event, wantValidation string) {
	t.Helper()
	if event.Type != audit.EventTypeCapabilityDenied {
		t.Fatalf("audit event Type = %q, want %q", event.Type, audit.EventTypeCapabilityDenied)
	}
	if !auditValidationErrorsContain(event.Details["validation_errors"], wantValidation) {
		t.Fatalf("audit validation_errors = %#v, want %s; details=%#v", event.Details["validation_errors"], wantValidation, event.Details)
	}
}
