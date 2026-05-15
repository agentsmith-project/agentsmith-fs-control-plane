package api

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
)

func TestRestorePreviewHandlerCreatesQueuedPreviewForSavePoint(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestoreHTTPStore(now)
	handler := restorePreviewHandlerForTest(store, func() string { return "op_preview" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restorePreviewRequest(`{"save_point_id":"sp_001"}`, "repo_alpha01", "ns_alpha01", "idem_preview"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_preview" || env.OperationState != OperationStateQueued || env.Resource.Type != "repo" || env.Resource.ID != "repo_alpha01" {
		t.Fatalf("envelope = %#v, want queued op_preview repo resource", env)
	}
	spec := store.spec
	if spec.OperationID != "op_preview" || spec.Scope.OperationType != operations.OperationRestorePreview || spec.Phase != operations.OperationPhaseRestorePreviewValidate {
		t.Fatalf("spec operation/scope/phase = %q/%#v/%q", spec.OperationID, spec.Scope, spec.Phase)
	}
	if store.previewIntakeCalls != 1 || store.genericCreateCalls != 0 {
		t.Fatalf("typed/generic intake calls = %d/%d, want typed restore preview intake only", store.previewIntakeCalls, store.genericCreateCalls)
	}
	if spec.NamespaceID != "ns_alpha01" || spec.RepoID != "repo_alpha01" || len(spec.InputSummary) != 1 || spec.InputSummary["save_point_id"] != "sp_001" {
		t.Fatalf("spec namespace/repo/input = %q/%q/%#v, want only save_point_id", spec.NamespaceID, spec.RepoID, spec.InputSummary)
	}
	assertRestoreHTTPNoRawCommand(t, spec.InputSummary)
}

func TestRestorePreviewHandlerFailsClosedForActivePlanOrJVSMutation(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	tests := []struct {
		name     string
		edit     func(*fakeRestoreHTTPStore)
		wantCode ErrorCode
	}{
		{name: "active restore plan", edit: func(store *fakeRestoreHTTPStore) {
			store.activePlanErr = nil
			store.activePlan = apiRestorePreviewPendingPlan(now)
		}, wantCode: CodeOperationRecoveryRequired},
		{name: "same repo jvs mutation", edit: func(store *fakeRestoreHTTPStore) { store.jvsMutation = true }, wantCode: CodeRepoJVSMutationInProgress},
		{name: "archived repo", edit: func(store *fakeRestoreHTTPStore) {
			store.repo.Status = resources.RepoStatusArchived
			store.repo.Lifecycle.Status = resources.RepoStatusArchived
		}, wantCode: CodeRepoArchived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeRestoreHTTPStore(now)
			tt.edit(store)
			handler := restorePreviewHandlerForTest(store, func() string { return "op_preview" }, func() time.Time { return now })
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, restorePreviewRequest(`{"save_point_id":"sp_001"}`, "repo_alpha01", "ns_alpha01", "idem_preview"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode {
				t.Fatalf("error code = %s, want %s", env.Error.Code, tt.wantCode)
			}
			if store.createCalls != 0 {
				t.Fatalf("create calls = %d, want no preview operation creation", store.createCalls)
			}
		})
	}
}

func TestRestorePreviewHandlerMapsAtomicIntakeGateFailures(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	tests := []struct {
		name       string
		createErr  error
		wantCode   ErrorCode
		wantStatus int
	}{
		{name: "active restore plan at insert boundary", createErr: operations.ErrActiveRestorePlan, wantCode: CodeOperationRecoveryRequired, wantStatus: http.StatusConflict},
		{name: "same repo jvs mutation at insert boundary", createErr: operations.ErrRepoJVSMutationInProgress, wantCode: CodeRepoJVSMutationInProgress, wantStatus: http.StatusConflict},
		{name: "different idempotency body", createErr: operations.ErrIdempotencyConflict, wantCode: CodeIdempotencyConflict, wantStatus: http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeRestoreHTTPStore(now)
			store.createErr = tt.createErr
			handler := restorePreviewHandlerForTest(store, func() string { return "op_preview" }, func() time.Time { return now })
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, restorePreviewRequest(`{"save_point_id":"sp_001"}`, "repo_alpha01", "ns_alpha01", "idem_preview"))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.wantStatus)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode || !env.Error.Retryable && tt.wantCode != CodeIdempotencyConflict {
				t.Fatalf("error = %#v, want %s", env.Error, tt.wantCode)
			}
			if store.previewIntakeCalls != 1 {
				t.Fatalf("typed preview intake calls = %d, want 1", store.previewIntakeCalls)
			}
		})
	}
}

func TestRestorePreviewHandlerRejectsDisabledNamespacePolicy(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestoreHTTPStore(now)
	store.namespace.Status = resources.NamespaceStatusDisabled
	disabledAt := now.Add(-time.Minute)
	store.namespace.DisabledAt = &disabledAt
	store.namespace.DisabledReason = "maintenance"
	handler := restorePreviewHandlerForTest(store, func() string { return "op_preview" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restorePreviewRequest(`{"save_point_id":"sp_001"}`, "repo_alpha01", "ns_alpha01", "idem_preview"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeNamespaceDisabled {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeNamespaceDisabled)
	}
	if store.createCalls != 0 {
		t.Fatalf("create calls = %d, want no restore preview intake", store.createCalls)
	}
}

func TestRestorePreviewHandlerReusesExistingIdempotentOperationBeforePlanState(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	existing := apiRestorePreviewQueuedOperation(now)
	store := newFakeRestoreHTTPStore(now)
	store.existing = existing
	store.activePlanErr = nil
	store.activePlan = apiRestorePreviewPendingPlan(now)
	handler := restorePreviewHandlerForTest(store, func() string { return "op_new" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restorePreviewRequest(`{"save_point_id":"sp_001"}`, "repo_alpha01", "ns_alpha01", "idem_preview"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != existing.ID || store.createCalls != 0 || store.jvsMutationCalls != 0 || store.activePlanCalls != 0 {
		t.Fatalf("envelope/create/gates = %#v/%d/%d/%d, want existing operation reused before mutable gates", env, store.createCalls, store.jvsMutationCalls, store.activePlanCalls)
	}
}

func restorePreviewHandlerForTest(store *fakeRestoreHTTPStore, generate OperationIDGenerator, now func() time.Time) http.Handler {
	return RestorePreviewHandler(RestorePreviewHandlerConfig{
		RepoReader:        store,
		NamespaceReader:   store,
		BindingReader:     store,
		FenceReader:       store,
		MutationGate:      store,
		RestorePlanReader: store,
		IntakeStore:       store,
		IntakeLookupStore: store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    namespaceBindingAllowedPolicy(auth.RoleRestoreAdmin),
		OperationID:       generate,
		Now:               now,
	})
}

func restorePreviewRequest(body, repoID, namespaceID, idempotencyKey string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/repos/"+repoID+"/restore-preview", bytes.NewBufferString(body))
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_restore_preview")
	req.Header.Set(auth.HeaderCallerService, "product-caller")
	req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	req.Header.Set(auth.HeaderIdempotencyKey, idempotencyKey)
	req.Header.Set(auth.HeaderActorType, "system")
	req.Header.Set(auth.HeaderActorID, "svc-alpha")
	return req
}

type fakeRestoreHTTPStore struct {
	repo               resources.Repo
	namespace          resources.Namespace
	binding            resources.NamespaceVolumeBinding
	fences             []fences.Fence
	previewOperation   operations.OperationRecord
	plan               restoreplan.Plan
	activePlan         restoreplan.Plan
	existing           operations.OperationRecord
	spec               operations.QueuedOperationSpec
	repoErr            error
	namespaceErr       error
	bindingErr         error
	fenceErr           error
	jvsMutation        bool
	jvsMutationErr     error
	activePlanErr      error
	operationErr       error
	planErr            error
	lookupErr          error
	runExists          bool
	runExistsErr       error
	createErr          error
	createCalls        int
	genericCreateCalls int
	restoreIntakeCalls int
	previewIntakeCalls int
	discardIntakeCalls int
	runIntakeCalls     int
	jvsMutationCalls   int
	activePlanCalls    int
	runExistsCalls     int
}

func newFakeRestoreHTTPStore(now time.Time) *fakeRestoreHTTPStore {
	return &fakeRestoreHTTPStore{
		repo:             repoResourceFixture("ns_alpha01", "repo_alpha01", resources.RepoStatusActive),
		namespace:        activeNamespaceFixture("ns_alpha01"),
		binding:          namespacePolicyBindingFixture("ns_alpha01", resourcesAllowedCallerForRestoreAdmin()),
		previewOperation: apiRestorePreviewOperationRecord(now),
		plan:             apiRestorePreviewPendingPlan(now),
		activePlanErr:    sql.ErrNoRows,
	}
}

func (store *fakeRestoreHTTPStore) GetRepoInNamespace(_ context.Context, namespaceID, repoID string) (resources.Repo, error) {
	if store.repoErr != nil {
		return resources.Repo{}, store.repoErr
	}
	if store.repo.ID == repoID && store.repo.NamespaceID == namespaceID {
		return store.repo, nil
	}
	return resources.Repo{}, sql.ErrNoRows
}

func (store *fakeRestoreHTTPStore) GetRepo(_ context.Context, repoID string) (resources.Repo, error) {
	if store.repoErr != nil {
		return resources.Repo{}, store.repoErr
	}
	if store.repo.ID == repoID {
		return store.repo, nil
	}
	return resources.Repo{}, sql.ErrNoRows
}

func (store *fakeRestoreHTTPStore) ListReposByNamespace(_ context.Context, namespaceID string) ([]resources.Repo, error) {
	if store.repoErr != nil {
		return nil, store.repoErr
	}
	if store.repo.NamespaceID == namespaceID {
		return []resources.Repo{store.repo}, nil
	}
	return nil, nil
}

func (store *fakeRestoreHTTPStore) GetNamespace(context.Context, string) (resources.Namespace, error) {
	if store.namespaceErr != nil {
		return resources.Namespace{}, store.namespaceErr
	}
	return store.namespace, nil
}

func (store *fakeRestoreHTTPStore) GetNamespaceVolumeBinding(context.Context, string) (resources.NamespaceVolumeBinding, error) {
	if store.bindingErr != nil {
		return resources.NamespaceVolumeBinding{}, store.bindingErr
	}
	return store.binding, nil
}

func (store *fakeRestoreHTTPStore) ListHeldRepoFences(context.Context, string) ([]fences.Fence, error) {
	if store.fenceErr != nil {
		return nil, store.fenceErr
	}
	return append([]fences.Fence(nil), store.fences...), nil
}

func (store *fakeRestoreHTTPStore) RepoHasNonTerminalJVSMutation(context.Context, string) (bool, error) {
	store.jvsMutationCalls++
	if store.jvsMutationErr != nil {
		return false, store.jvsMutationErr
	}
	return store.jvsMutation, nil
}

func (store *fakeRestoreHTTPStore) GetActiveRestorePlanByRepo(context.Context, string) (restoreplan.Plan, error) {
	store.activePlanCalls++
	if store.activePlanErr != nil {
		return restoreplan.Plan{}, store.activePlanErr
	}
	return store.activePlan, nil
}

func (store *fakeRestoreHTTPStore) GetOperation(_ context.Context, operationID string) (operations.OperationRecord, error) {
	if store.operationErr != nil {
		return operations.OperationRecord{}, store.operationErr
	}
	if store.previewOperation.ID == operationID {
		return store.previewOperation, nil
	}
	return operations.OperationRecord{}, sql.ErrNoRows
}

func (store *fakeRestoreHTTPStore) GetRestorePlanByPreviewOperation(_ context.Context, previewOperationID string) (restoreplan.Plan, error) {
	if store.planErr != nil {
		return restoreplan.Plan{}, store.planErr
	}
	if store.plan.PreviewOperationID == previewOperationID {
		return store.plan, nil
	}
	return restoreplan.Plan{}, sql.ErrNoRows
}

func (store *fakeRestoreHTTPStore) RestoreRunExistsForPreviewOperation(context.Context, string, string, string) (bool, error) {
	store.runExistsCalls++
	if store.runExistsErr != nil {
		return false, store.runExistsErr
	}
	return store.runExists, nil
}

func (store *fakeRestoreHTTPStore) GetOperationByIdempotencyScope(context.Context, operations.IdempotencyScope) (operations.OperationRecord, error) {
	if store.lookupErr != nil {
		return operations.OperationRecord{}, store.lookupErr
	}
	if store.existing.ID == "" {
		return operations.OperationRecord{}, sql.ErrNoRows
	}
	return store.existing, nil
}

func (store *fakeRestoreHTTPStore) CreateOrReuseOperation(_ context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.genericCreateCalls++
	return store.createOrReuseOperation(spec)
}

func (store *fakeRestoreHTTPStore) CreateOrReuseRestorePreviewOperation(_ context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.previewIntakeCalls++
	return store.createOrReuseOperation(spec)
}

func (store *fakeRestoreHTTPStore) CreateOrReuseRestoreOperation(_ context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.restoreIntakeCalls++
	return store.createOrReuseOperation(spec)
}

func (store *fakeRestoreHTTPStore) CreateOrReuseRestorePreviewDiscardOperation(_ context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.discardIntakeCalls++
	return store.createOrReuseOperation(spec)
}

func (store *fakeRestoreHTTPStore) CreateOrReuseRestoreRunOperation(_ context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.runIntakeCalls++
	return store.createOrReuseOperation(spec)
}

func (store *fakeRestoreHTTPStore) createOrReuseOperation(spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
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

func apiRestorePreviewQueuedOperation(now time.Time) operations.OperationRecord {
	hash, err := operations.HashRequest(restorePreviewCanonicalRequest{RepoID: "repo_alpha01", SavePointID: "sp_001"})
	if err != nil {
		panic(err)
	}
	return operations.OperationRecord{ID: "op_preview_existing", Type: operations.OperationRestorePreview, State: operations.OperationStateQueued, Phase: operations.OperationPhaseRestorePreviewValidate, IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationRestorePreview, "idem_preview").String(), IdempotencyKey: "idem_preview", RequestHash: hash, CorrelationID: "corr_restore_preview", CallerService: "product-caller", AuthorizedActor: operations.Actor{Type: "system", ID: "svc-alpha"}, Resource: operations.ResourceRef{Type: "repo", ID: "repo_alpha01"}, NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", InputSummary: map[string]any{"save_point_id": "sp_001"}, ExternalResourceIDs: map[string]string{}, CreatedAt: now}
}

func assertRestoreHTTPNoRawCommand(t *testing.T, value any) {
	t.Helper()
	rendered := strings.ToLower(fmt.Sprint(value))
	for _, forbidden := range []string{"run_command", "recommended_next_command", "restore_command", "command"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("restore HTTP intake leaked raw command marker %q in %#v", forbidden, value)
		}
	}
}
