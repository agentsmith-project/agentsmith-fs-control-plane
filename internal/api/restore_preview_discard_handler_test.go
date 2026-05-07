package api

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
)

func TestRestorePreviewDiscardHandlerCreatesQueuedDiscardForPendingPlan(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestorePreviewDiscardStore(now)
	handler := restorePreviewDiscardHandlerForTest(store, func() string { return "op_discard" }, func() time.Time { return now })
	req := restorePreviewDiscardRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_discard" || env.OperationState != OperationStateQueued || env.Resource.Type != "repo" || env.Resource.ID != "repo_alpha01" {
		t.Fatalf("envelope = %#v, want queued op_discard repo resource", env)
	}
	spec := store.spec
	if spec.OperationID != "op_discard" || spec.Scope.OperationType != operations.OperationRestorePreviewDiscard || spec.Phase != operations.OperationPhaseRestorePreviewDiscardValidate {
		t.Fatalf("spec operation/scope/phase = %q/%#v/%q", spec.OperationID, spec.Scope, spec.Phase)
	}
	if spec.NamespaceID != "ns_alpha01" || spec.RepoID != "repo_alpha01" || spec.InputSummary["preview_operation_id"] != "op_preview01" {
		t.Fatalf("spec namespace/repo/input = %q/%q/%#v", spec.NamespaceID, spec.RepoID, spec.InputSummary)
	}
	if store.discardIntakeCalls != 1 || store.genericCreateCalls != 0 {
		t.Fatalf("intake calls typed/generic = %d/%d, want typed discard only", store.discardIntakeCalls, store.genericCreateCalls)
	}
}

func TestRestorePreviewDiscardHandlerFailsClosedForMismatchedOrNonPendingPlan(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	tests := []struct {
		name     string
		edit     func(*fakeRestorePreviewDiscardStore)
		wantCode ErrorCode
	}{
		{name: "preview wrong repo", edit: func(store *fakeRestorePreviewDiscardStore) { store.previewOperation.RepoID = "repo_other" }, wantCode: CodeOperationNotFound},
		{name: "missing plan", edit: func(store *fakeRestorePreviewDiscardStore) { store.planErr = sql.ErrNoRows }, wantCode: CodeOperationRecoveryRequired},
		{name: "discarding plan", edit: func(store *fakeRestorePreviewDiscardStore) { store.plan.Status = restoreplan.StatusDiscarding }, wantCode: CodeOperationRecoveryRequired},
		{name: "discarded plan", edit: func(store *fakeRestorePreviewDiscardStore) { store.plan.Status = restoreplan.StatusDiscarded }, wantCode: CodeOperationRecoveryRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeRestorePreviewDiscardStore(now)
			tt.edit(store)
			handler := restorePreviewDiscardHandlerForTest(store, func() string { return "op_discard" }, func() time.Time { return now })
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, restorePreviewDiscardRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01"))

			if rec.Code != http.StatusNotFound && rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 404/409", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode {
				t.Fatalf("error code = %s, want %s", env.Error.Code, tt.wantCode)
			}
			if store.createCalls != 0 {
				t.Fatalf("create calls = %d, want no discard operation creation", store.createCalls)
			}
		})
	}
}

func TestRestorePreviewDiscardHandlerMapsAtomicPlanGateFailure(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestorePreviewDiscardStore(now)
	store.createErr = operations.ErrRestorePlanNotPending
	handler := restorePreviewDiscardHandlerForTest(store, func() string { return "op_discard" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restorePreviewDiscardRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeOperationRecoveryRequired || !env.Error.Retryable {
		t.Fatalf("error = %#v, want retryable OPERATION_RECOVERY_REQUIRED", env.Error)
	}
	if store.discardIntakeCalls != 1 || store.genericCreateCalls != 0 {
		t.Fatalf("intake calls typed/generic = %d/%d, want typed discard only", store.discardIntakeCalls, store.genericCreateCalls)
	}
}

func TestRestorePreviewDiscardHandlerReusesExistingIdempotentOperationBeforePlanState(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	existing := apiRestorePreviewDiscardQueuedOperation(now)
	store := newFakeRestorePreviewDiscardStore(now)
	store.existing = existing
	store.plan.Status = restoreplan.StatusDiscarded
	handler := restorePreviewDiscardHandlerForTest(store, func() string { return "op_new" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restorePreviewDiscardRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != existing.ID || store.createCalls != 0 {
		t.Fatalf("envelope/create = %#v/%d, want existing operation reused before plan state check", env, store.createCalls)
	}
	if store.discardIntakeCalls != 0 || store.genericCreateCalls != 0 {
		t.Fatalf("intake calls typed/generic = %d/%d, want no create after idempotency reuse", store.discardIntakeCalls, store.genericCreateCalls)
	}
}

func TestRestorePreviewDiscardHandlerAllowsDisabledNamespaceCleanupForPendingPlan(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestorePreviewDiscardStore(now)
	store.namespace.Status = resources.NamespaceStatusDisabled
	disabledAt := now.Add(-time.Minute)
	store.namespace.DisabledAt = &disabledAt
	store.namespace.DisabledReason = "maintenance"
	handler := restorePreviewDiscardHandlerForTest(store, func() string { return "op_discard" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restorePreviewDiscardRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_discard" || store.discardIntakeCalls != 1 {
		t.Fatalf("envelope/intake = %#v/%d, want queued cleanup despite disabled namespace", env, store.discardIntakeCalls)
	}
}

func TestRestorePreviewDiscardHandlerRejectsCleanupAdmissionRisksBeforePreviewPlanAndIntake(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	tests := []struct {
		name     string
		edit     func(*fakeRestorePreviewDiscardStore)
		wantCode ErrorCode
	}{
		{name: "disabled binding", edit: func(store *fakeRestorePreviewDiscardStore) {
			store.binding.Status = resources.NamespaceStatusDisabled
			store.operationErr = sql.ErrNoRows
		}, wantCode: CodeNamespaceDisabled},
		{name: "archived repo", edit: func(store *fakeRestorePreviewDiscardStore) {
			store.repo.Status = resources.RepoStatusArchived
			store.repo.Lifecycle.Status = resources.RepoStatusArchived
			store.previewOperation.RepoID = "repo_other"
		}, wantCode: CodeRepoArchived},
		{name: "lifecycle fence", edit: func(store *fakeRestorePreviewDiscardStore) {
			store.fences = []fences.Fence{restoreHTTPLifecycleFence(now)}
			store.planErr = sql.ErrNoRows
		}, wantCode: CodeRepoLifecycleFenceHeld},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeRestorePreviewDiscardStore(now)
			tt.edit(store)
			handler := restorePreviewDiscardHandlerForTest(store, func() string { return "op_discard" }, func() time.Time { return now })
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, restorePreviewDiscardRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode {
				t.Fatalf("error code = %s, want %s", env.Error.Code, tt.wantCode)
			}
			if store.createCalls != 0 {
				t.Fatalf("create calls = %d, want cleanup admission denied before intake", store.createCalls)
			}
			if store.operationCalls != 0 || store.planCalls != 0 {
				t.Fatalf("preview/plan calls = %d/%d, want cleanup admission denied before preview or plan validation", store.operationCalls, store.planCalls)
			}
		})
	}
}

func TestInternalAPIShellServesRestorePreviewDiscard(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestorePreviewDiscardStore(now)
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceReader:        store,
		NamespaceBindingReader: store,
		RepoReader:             store,
		RepoFenceReader:        store,
		OperationIntakeStore:   store,
		GenerateOperationID:    func() string { return "op_discard" },
		Now:                    func() time.Time { return now },
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, restorePreviewDiscardRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("discard status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
}

func restorePreviewDiscardHandlerForTest(store *fakeRestorePreviewDiscardStore, generate OperationIDGenerator, now func() time.Time) http.Handler {
	return RestorePreviewDiscardHandler(RestorePreviewDiscardHandlerConfig{
		RepoReader:        store,
		NamespaceReader:   store,
		BindingReader:     store,
		FenceReader:       store,
		MetadataReader:    store,
		IntakeStore:       store,
		IntakeLookupStore: store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    namespaceBindingAllowedPolicy(auth.RoleRestoreAdmin),
		OperationID:       generate,
		Now:               now,
	})
}

func restorePreviewDiscardRequest(body, repoID, namespaceID string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/repos/"+repoID+"/restore-preview:discard", bytes.NewBufferString(body))
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_restore_discard")
	req.Header.Set(auth.HeaderCallerService, "product-caller")
	req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	req.Header.Set(auth.HeaderIdempotencyKey, "idem_discard")
	req.Header.Set(auth.HeaderActorType, "system")
	req.Header.Set(auth.HeaderActorID, "svc-alpha")
	return req
}

type fakeRestorePreviewDiscardStore struct {
	repo               resources.Repo
	namespace          resources.Namespace
	binding            resources.NamespaceVolumeBinding
	fences             []fences.Fence
	previewOperation   operations.OperationRecord
	plan               restoreplan.Plan
	existing           operations.OperationRecord
	spec               operations.QueuedOperationSpec
	operationErr       error
	planErr            error
	lookupErr          error
	createErr          error
	operationCalls     int
	planCalls          int
	createCalls        int
	discardIntakeCalls int
	genericCreateCalls int
}

func newFakeRestorePreviewDiscardStore(now time.Time) *fakeRestorePreviewDiscardStore {
	return &fakeRestorePreviewDiscardStore{
		repo:             repoResourceFixture("ns_alpha01", "repo_alpha01", resources.RepoStatusActive),
		namespace:        activeNamespaceFixture("ns_alpha01"),
		binding:          namespacePolicyBindingFixture("ns_alpha01", resourcesAllowedCallerForRestoreAdmin()),
		previewOperation: apiRestorePreviewOperationRecord(now),
		plan:             apiRestorePreviewPendingPlan(now),
	}
}

func (store *fakeRestorePreviewDiscardStore) GetRepoInNamespace(_ context.Context, namespaceID, repoID string) (resources.Repo, error) {
	if store.repo.ID == repoID && store.repo.NamespaceID == namespaceID {
		return store.repo, nil
	}
	return resources.Repo{}, sql.ErrNoRows
}

func (store *fakeRestorePreviewDiscardStore) GetRepo(_ context.Context, repoID string) (resources.Repo, error) {
	if store.repo.ID == repoID {
		return store.repo, nil
	}
	return resources.Repo{}, sql.ErrNoRows
}

func (store *fakeRestorePreviewDiscardStore) ListReposByNamespace(_ context.Context, namespaceID string) ([]resources.Repo, error) {
	if store.repo.NamespaceID == namespaceID {
		return []resources.Repo{store.repo}, nil
	}
	return nil, nil
}

func (store *fakeRestorePreviewDiscardStore) GetNamespace(context.Context, string) (resources.Namespace, error) {
	return store.namespace, nil
}

func (store *fakeRestorePreviewDiscardStore) GetNamespaceVolumeBinding(context.Context, string) (resources.NamespaceVolumeBinding, error) {
	return store.binding, nil
}

func (store *fakeRestorePreviewDiscardStore) ListHeldRepoFences(context.Context, string) ([]fences.Fence, error) {
	return append([]fences.Fence(nil), store.fences...), nil
}

func (store *fakeRestorePreviewDiscardStore) GetOperation(context.Context, string) (operations.OperationRecord, error) {
	store.operationCalls++
	if store.operationErr != nil {
		return operations.OperationRecord{}, store.operationErr
	}
	return store.previewOperation, nil
}

func (store *fakeRestorePreviewDiscardStore) GetRestorePlanByPreviewOperation(context.Context, string) (restoreplan.Plan, error) {
	store.planCalls++
	if store.planErr != nil {
		return restoreplan.Plan{}, store.planErr
	}
	return store.plan, nil
}

func (store *fakeRestorePreviewDiscardStore) GetOperationByIdempotencyScope(context.Context, operations.IdempotencyScope) (operations.OperationRecord, error) {
	if store.lookupErr != nil {
		return operations.OperationRecord{}, store.lookupErr
	}
	if store.existing.ID == "" {
		return operations.OperationRecord{}, sql.ErrNoRows
	}
	return store.existing, nil
}

func (store *fakeRestorePreviewDiscardStore) CreateOrReuseOperation(_ context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.genericCreateCalls++
	return store.createOrReuseDiscard(spec)
}

func (store *fakeRestorePreviewDiscardStore) CreateOrReuseRestorePreviewDiscardOperation(_ context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.discardIntakeCalls++
	return store.createOrReuseDiscard(spec)
}

func (store *fakeRestorePreviewDiscardStore) createOrReuseDiscard(spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.createCalls++
	store.spec = spec
	if store.createErr != nil {
		return operations.IdempotencyResolution{}, store.createErr
	}
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	return operations.IdempotencyResolution{Operation: record.Sanitized()}, nil
}

func apiRestorePreviewOperationRecord(now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:                  "op_preview01",
		Type:                operations.OperationRestorePreview,
		State:               operations.OperationStateSucceeded,
		Phase:               operations.OperationPhaseRestorePreviewCommitted,
		IdempotencyScope:    operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationRestorePreview, "idem_preview").String(),
		IdempotencyKey:      "idem_preview",
		RequestHash:         operations.RequestHash("sha256:restore-preview"),
		CorrelationID:       "corr_restore_preview",
		CallerService:       "product-caller",
		AuthorizedActor:     operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:            operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:         "ns_alpha01",
		RepoID:              "repo_alpha01",
		InputSummary:        map[string]any{"save_point_id": "sp_001"},
		ExternalResourceIDs: map[string]string{"restore_plan_id": "plan_001", "source_save_point_id": "sp_001"},
		JVSJSONOutput:       map[string]any{"restore_plan_id": "plan_001", "source_save_point_id": "sp_001"},
		VerificationResult:  map[string]any{"restore_plan_id": "plan_001", "source_save_point_id": "sp_001"},
		CreatedAt:           now.Add(-time.Hour),
		FinishedAt:          &now,
	}
}

func apiRestorePreviewPendingPlan(now time.Time) restoreplan.Plan {
	return restoreplan.Plan{ID: "plan_001", NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", PreviewOperationID: "op_preview01", SourceSavePointID: "sp_001", Status: restoreplan.StatusPending, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute)}
}

func apiRestorePreviewDiscardQueuedOperation(now time.Time) operations.OperationRecord {
	hash, err := operations.HashRequest(restorePreviewDiscardCanonicalRequest{RepoID: "repo_alpha01", PreviewOperationID: "op_preview01"})
	if err != nil {
		panic(err)
	}
	return operations.OperationRecord{
		ID:                  "op_discard_existing",
		Type:                operations.OperationRestorePreviewDiscard,
		State:               operations.OperationStateQueued,
		Phase:               operations.OperationPhaseRestorePreviewDiscardValidate,
		IdempotencyScope:    operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationRestorePreviewDiscard, "idem_discard").String(),
		IdempotencyKey:      "idem_discard",
		RequestHash:         hash,
		CorrelationID:       "corr_restore_discard",
		CallerService:       "product-caller",
		AuthorizedActor:     operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:            operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:         "ns_alpha01",
		RepoID:              "repo_alpha01",
		InputSummary:        map[string]any{"preview_operation_id": "op_preview01"},
		ExternalResourceIDs: map[string]string{},
		CreatedAt:           now,
	}
}

func resourcesAllowedCallerForRestoreAdmin() resources.AllowedCaller {
	return resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleRestoreAdmin}}
}
