package api

import (
	"bytes"
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

func TestRestoreRunHandlerCreatesQueuedRunForPendingPlan(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestoreHTTPStore(now)
	handler := restoreRunHandlerForTest(store, func() string { return "op_run" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restoreRunRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01", "idem_run"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_run" || env.OperationState != OperationStateQueued || env.Resource.Type != "repo" || env.Resource.ID != "repo_alpha01" {
		t.Fatalf("envelope = %#v, want queued op_run repo resource", env)
	}
	spec := store.spec
	if spec.OperationID != "op_run" || spec.Scope.OperationType != operations.OperationRestoreRun || spec.Phase != operations.OperationPhaseRestoreRunValidate {
		t.Fatalf("spec operation/scope/phase = %q/%#v/%q", spec.OperationID, spec.Scope, spec.Phase)
	}
	if store.runIntakeCalls != 1 || store.genericCreateCalls != 0 {
		t.Fatalf("typed/generic intake calls = %d/%d, want typed restore-run intake only", store.runIntakeCalls, store.genericCreateCalls)
	}
	if spec.NamespaceID != "ns_alpha01" || spec.RepoID != "repo_alpha01" || len(spec.InputSummary) != 1 || spec.InputSummary["preview_operation_id"] != "op_preview01" {
		t.Fatalf("spec namespace/repo/input = %q/%q/%#v, want only preview_operation_id", spec.NamespaceID, spec.RepoID, spec.InputSummary)
	}
	if store.runExistsCalls != 1 {
		t.Fatalf("run exists calls = %d, want duplicate run gate", store.runExistsCalls)
	}
	assertRestoreHTTPNoRawCommand(t, spec.InputSummary)
}

func TestRestoreRunHandlerRejectsMismatchedPreviewAsNotFound(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestoreHTTPStore(now)
	store.previewOperation.RepoID = "repo_other01"
	handler := restoreRunHandlerForTest(store, func() string { return "op_run" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restoreRunRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01", "idem_run"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeOperationNotFound {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeOperationNotFound)
	}
	if store.createCalls != 0 || store.runExistsCalls != 0 {
		t.Fatalf("create/run gate calls = %d/%d, want no run creation after cross-repo preview", store.createCalls, store.runExistsCalls)
	}
}

func TestRestoreRunHandlerRejectsNonPendingPlanStatuses(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	for _, status := range []restoreplan.Status{
		restoreplan.StatusConsuming,
		restoreplan.StatusConsumed,
		restoreplan.StatusDiscarding,
		restoreplan.StatusDiscarded,
		restoreplan.StatusOperatorInterventionRequired,
	} {
		t.Run(status.String(), func(t *testing.T) {
			store := newFakeRestoreHTTPStore(now)
			store.plan.Status = status
			handler := restoreRunHandlerForTest(store, func() string { return "op_run" }, func() time.Time { return now })
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, restoreRunRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01", "idem_run"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != CodeOperationRecoveryRequired {
				t.Fatalf("error code = %s, want %s", env.Error.Code, CodeOperationRecoveryRequired)
			}
			if store.createCalls != 0 {
				t.Fatalf("create calls = %d, want no restore run creation", store.createCalls)
			}
		})
	}
}

func TestRestoreRunHandlerRejectsPreviewMetadataMismatch(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	tests := []struct {
		name string
		edit func(*fakeRestoreHTTPStore)
	}{
		{name: "missing restore plan id", edit: func(store *fakeRestoreHTTPStore) {
			store.previewOperation.ExternalResourceIDs = nil
			store.previewOperation.VerificationResult = map[string]any{"source_save_point_id": "sp_001"}
			store.previewOperation.JVSJSONOutput = nil
		}},
		{name: "mismatched restore plan id", edit: func(store *fakeRestoreHTTPStore) {
			store.previewOperation.VerificationResult = map[string]any{"restore_plan_id": "plan_other", "source_save_point_id": "sp_001"}
			store.previewOperation.JVSJSONOutput = nil
		}},
		{name: "missing source save point id", edit: func(store *fakeRestoreHTTPStore) {
			store.previewOperation.ExternalResourceIDs = nil
			store.previewOperation.VerificationResult = map[string]any{"restore_plan_id": "plan_001"}
			store.previewOperation.JVSJSONOutput = nil
		}},
		{name: "mismatched source save point id", edit: func(store *fakeRestoreHTTPStore) {
			store.previewOperation.VerificationResult = map[string]any{"restore_plan_id": "plan_001", "source_save_point_id": "sp_other"}
			store.previewOperation.JVSJSONOutput = nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeRestoreHTTPStore(now)
			tt.edit(store)
			handler := restoreRunHandlerForTest(store, func() string { return "op_run" }, func() time.Time { return now })
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, restoreRunRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01", "idem_run"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != CodeOperationRecoveryRequired {
				t.Fatalf("error code = %s, want %s", env.Error.Code, CodeOperationRecoveryRequired)
			}
			if store.createCalls != 0 || store.runExistsCalls != 0 {
				t.Fatalf("create/run gate calls = %d/%d, want rejected before run intake", store.createCalls, store.runExistsCalls)
			}
		})
	}
}

func TestRestoreRunHandlerRejectsExistingRunForPreview(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestoreHTTPStore(now)
	store.runExists = true
	handler := restoreRunHandlerForTest(store, func() string { return "op_run" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restoreRunRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01", "idem_run_new"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeRepoJVSMutationInProgress {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeRepoJVSMutationInProgress)
	}
	if store.createCalls != 0 {
		t.Fatalf("create calls = %d, want no duplicate run operation", store.createCalls)
	}
}

func TestRestoreRunHandlerRejectsDisabledNamespaceBeforeIntake(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestoreHTTPStore(now)
	store.namespace.Status = resources.NamespaceStatusDisabled
	disabledAt := now.Add(-time.Minute)
	store.namespace.DisabledAt = &disabledAt
	store.namespace.DisabledReason = "maintenance"
	handler := restoreRunHandlerForTest(store, func() string { return "op_run" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restoreRunRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01", "idem_run"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeNamespaceDisabled {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeNamespaceDisabled)
	}
	if store.createCalls != 0 || store.runExistsCalls != 0 {
		t.Fatalf("create/run gate calls = %d/%d, want rejected before restore-run intake", store.createCalls, store.runExistsCalls)
	}
}

func TestRestoreRunHandlerRejectsLifecycleFenceBeforeIntake(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestoreHTTPStore(now)
	store.fences = []fences.Fence{restoreHTTPLifecycleFence(now)}
	handler := restoreRunHandlerForTest(store, func() string { return "op_run" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restoreRunRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01", "idem_run"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeRepoLifecycleFenceHeld {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeRepoLifecycleFenceHeld)
	}
	if store.createCalls != 0 || store.runExistsCalls != 0 {
		t.Fatalf("create/run gate calls = %d/%d, want rejected before restore-run intake", store.createCalls, store.runExistsCalls)
	}
}

func TestRestoreRunHandlerMapsAtomicIntakeFailures(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	tests := []struct {
		name     string
		err      error
		wantCode ErrorCode
	}{
		{name: "duplicate run", err: operations.ErrRestoreRunAlreadyExists, wantCode: CodeRepoJVSMutationInProgress},
		{name: "plan not pending", err: operations.ErrRestorePlanNotPending, wantCode: CodeOperationRecoveryRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeRestoreHTTPStore(now)
			store.createErr = tt.err
			handler := restoreRunHandlerForTest(store, func() string { return "op_run" }, func() time.Time { return now })
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, restoreRunRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01", "idem_run_new"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode || !env.Error.Retryable {
				t.Fatalf("error = %#v, want retryable %s", env.Error, tt.wantCode)
			}
			if store.runIntakeCalls != 1 {
				t.Fatalf("typed run intake calls = %d, want 1", store.runIntakeCalls)
			}
		})
	}
}

func TestRestoreRunHandlerReusesExistingIdempotentOperationBeforePlanStateAndRunGate(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	existing := apiRestoreRunQueuedOperation(now)
	store := newFakeRestoreHTTPStore(now)
	store.existing = existing
	store.plan.Status = restoreplan.StatusConsumed
	store.runExists = true
	handler := restoreRunHandlerForTest(store, func() string { return "op_new" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restoreRunRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01", "idem_run"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != existing.ID || store.createCalls != 0 || store.runExistsCalls != 0 {
		t.Fatalf("envelope/create/run gate = %#v/%d/%d, want existing operation reused before plan/run gate", env, store.createCalls, store.runExistsCalls)
	}
}

func TestInternalAPIShellServesRestorePreviewRunAndDiscard(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestoreHTTPStore(now)
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceReader:        store,
		NamespaceBindingReader: store,
		RepoReader:             store,
		RepoFenceReader:        store,
		OperationIntakeStore:   store,
		GenerateOperationID:    func() string { return "op_restore" },
		Now:                    func() time.Time { return now },
	})

	tests := []struct {
		name string
		req  *http.Request
		typ  operations.OperationType
	}{
		{name: "preview", req: restorePreviewRequest(`{"save_point_id":"sp_001"}`, "repo_alpha01", "ns_alpha01", "idem_preview"), typ: operations.OperationRestorePreview},
		{name: "run", req: restoreRunRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01", "idem_run"), typ: operations.OperationRestoreRun},
		{name: "discard", req: restorePreviewDiscardRequest(`{"preview_operation_id":"op_preview01"}`, "repo_alpha01", "ns_alpha01"), typ: operations.OperationRestorePreviewDiscard},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store.createCalls = 0
			store.spec = operations.QueuedOperationSpec{}
			store.activePlanErr = sql.ErrNoRows
			store.runExists = false
			store.plan.Status = restoreplan.StatusPending
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, tt.req)

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
			}
			if store.spec.Scope.OperationType != tt.typ {
				t.Fatalf("created operation type = %s, want %s", store.spec.Scope.OperationType, tt.typ)
			}
		})
	}
}

func restoreRunHandlerForTest(store *fakeRestoreHTTPStore, generate OperationIDGenerator, now func() time.Time) http.Handler {
	return RestoreRunHandler(RestoreRunHandlerConfig{
		RepoReader:        store,
		NamespaceReader:   store,
		BindingReader:     store,
		FenceReader:       store,
		MetadataReader:    store,
		RunGate:           store,
		IntakeStore:       store,
		IntakeLookupStore: store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    namespaceBindingAllowedPolicy(auth.RoleRestoreAdmin),
		OperationID:       generate,
		Now:               now,
	})
}

func restoreRunRequest(body, repoID, namespaceID, idempotencyKey string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/repos/"+repoID+"/restore-run", bytes.NewBufferString(body))
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_restore_run")
	req.Header.Set(auth.HeaderCallerService, "agentsmith-api")
	req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	req.Header.Set(auth.HeaderIdempotencyKey, idempotencyKey)
	req.Header.Set(auth.HeaderActorType, "system")
	req.Header.Set(auth.HeaderActorID, "svc-alpha")
	return req
}

func apiRestoreRunQueuedOperation(now time.Time) operations.OperationRecord {
	hash, err := operations.HashRequest(restoreRunCanonicalRequest{RepoID: "repo_alpha01", PreviewOperationID: "op_preview01"})
	if err != nil {
		panic(err)
	}
	return operations.OperationRecord{ID: "op_run_existing", Type: operations.OperationRestoreRun, State: operations.OperationStateQueued, Phase: operations.OperationPhaseRestoreRunValidate, IdempotencyScope: operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationRestoreRun, "idem_run").String(), IdempotencyKey: "idem_run", RequestHash: hash, CorrelationID: "corr_restore_run", CallerService: "agentsmith-api", AuthorizedActor: operations.Actor{Type: "system", ID: "svc-alpha"}, Resource: operations.ResourceRef{Type: "repo", ID: "repo_alpha01"}, NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", InputSummary: map[string]any{"preview_operation_id": "op_preview01"}, ExternalResourceIDs: map[string]string{}, CreatedAt: now}
}

func restoreHTTPLifecycleFence(now time.Time) fences.Fence {
	return fences.Fence{ID: "fence_restore", RepoID: "repo_alpha01", Kind: fences.KindLifecycle, HolderOperationID: "op_lifecycle01", Status: fences.StatusActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
}
