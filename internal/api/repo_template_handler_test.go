package api

import (
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
	binding := namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "agentsmith-api", Roles: []resources.CallerRole{resources.CallerRoleTemplateAdmin}})
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
		AuditSink:         auditSink,
	})
}

func repoTemplateRequest(method, path, namespaceID, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_template")
	req.Header.Set(auth.HeaderCallerService, "agentsmith-api")
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
	return operations.OperationRecord{ID: operationID, Type: typ, State: operations.OperationStateQueued, Phase: phase, IdempotencyScope: operations.NewIdempotencyScope("agentsmith-api", "ns_123", typ, "idem_template").String(), IdempotencyKey: "idem_template", RequestHash: hash, CorrelationID: "corr_template", CallerService: "agentsmith-api", AuthorizedActor: operations.Actor{Type: "user", ID: "user_123"}, Resource: resource, NamespaceID: "ns_123", RepoID: repoID, TemplateID: "tmpl_base01", ExternalResourceIDs: map[string]string{}, InputSummary: map[string]any{}, CreatedAt: now}
}

func assertRepoTemplateResponseDoesNotLeak(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"password=secret", "raw secret reason", "/srv", "control_root", "payload_root", "raw_path", "token=", "bearer "} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("repo template response leaked %q: %s", forbidden, body)
		}
	}
}
