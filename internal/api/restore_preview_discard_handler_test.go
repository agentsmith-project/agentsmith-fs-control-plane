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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
)

func TestRestorePreviewDiscardHandlerCreatesQueuedDiscardForPendingPlan(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := &fakeRestorePreviewDiscardStore{
		previewOperation: apiRestorePreviewOperationRecord(now),
		plan:             apiRestorePreviewPendingPlan(now),
	}
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
			store := &fakeRestorePreviewDiscardStore{
				previewOperation: apiRestorePreviewOperationRecord(now),
				plan:             apiRestorePreviewPendingPlan(now),
			}
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

func TestRestorePreviewDiscardHandlerReusesExistingIdempotentOperationBeforePlanState(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	existing := apiRestorePreviewDiscardQueuedOperation(now)
	store := &fakeRestorePreviewDiscardStore{
		previewOperation: apiRestorePreviewOperationRecord(now),
		plan:             apiRestorePreviewPendingPlan(now),
		existing:         existing,
	}
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
}

func TestInternalAPIShellServesRestorePreviewDiscardButKeepsPreviewAndRunFailClosed(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := &fakeRestorePreviewDiscardStore{
		previewOperation: apiRestorePreviewOperationRecord(now),
		plan:             apiRestorePreviewPendingPlan(now),
	}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceBindingReader: &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_alpha01", resourcesAllowedCallerForRestoreAdmin())},
		OperationIntakeStore:   store,
		GenerateOperationID:    func() string { return "op_discard" },
		Now:                    func() time.Time { return now },
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, restorePreviewDiscardRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("discard status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}

	for _, path := range []string{"/internal/v1/repos/repo_alpha01/restore-preview", "/internal/v1/repos/repo_alpha01/restore-run"} {
		rec := httptest.NewRecorder()
		req := restorePreviewDiscardRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01")
		req.URL.Path = path
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d body = %s, want 403", path, rec.Code, rec.Body.String())
		}
		env := decodeErrorEnvelope(t, rec.Body.Bytes())
		if env.Error.Code != CodeCapabilityDenied {
			t.Fatalf("%s error code = %s, want CAPABILITY_DENIED", path, env.Error.Code)
		}
	}
}

func restorePreviewDiscardHandlerForTest(store *fakeRestorePreviewDiscardStore, generate OperationIDGenerator, now func() time.Time) http.Handler {
	return RestorePreviewDiscardHandler(RestorePreviewDiscardHandlerConfig{
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
	req.Header.Set(auth.HeaderCallerService, "agentsmith-api")
	req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	req.Header.Set(auth.HeaderIdempotencyKey, "idem_discard")
	req.Header.Set(auth.HeaderActorType, "system")
	req.Header.Set(auth.HeaderActorID, "svc-alpha")
	return req
}

type fakeRestorePreviewDiscardStore struct {
	previewOperation operations.OperationRecord
	plan             restoreplan.Plan
	existing         operations.OperationRecord
	spec             operations.QueuedOperationSpec
	operationErr     error
	planErr          error
	lookupErr        error
	createCalls      int
}

func (store *fakeRestorePreviewDiscardStore) GetOperation(context.Context, string) (operations.OperationRecord, error) {
	if store.operationErr != nil {
		return operations.OperationRecord{}, store.operationErr
	}
	return store.previewOperation, nil
}

func (store *fakeRestorePreviewDiscardStore) GetRestorePlanByPreviewOperation(context.Context, string) (restoreplan.Plan, error) {
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
	store.createCalls++
	store.spec = spec
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
		IdempotencyScope:    operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationRestorePreview, "idem_preview").String(),
		IdempotencyKey:      "idem_preview",
		RequestHash:         operations.RequestHash("sha256:restore-preview"),
		CorrelationID:       "corr_restore_preview",
		CallerService:       "agentsmith-api",
		AuthorizedActor:     operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:            operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:         "ns_alpha01",
		RepoID:              "repo_alpha01",
		InputSummary:        map[string]any{"save_point_id": "sp_001"},
		ExternalResourceIDs: map[string]string{"restore_plan_id": "plan_001"},
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
		IdempotencyScope:    operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationRestorePreviewDiscard, "idem_discard").String(),
		IdempotencyKey:      "idem_discard",
		RequestHash:         hash,
		CorrelationID:       "corr_restore_discard",
		CallerService:       "agentsmith-api",
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
	return resources.AllowedCaller{CallerService: "agentsmith-api", Roles: []resources.CallerRole{resources.CallerRoleRestoreAdmin}}
}
