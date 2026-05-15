package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func TestRestoreHandlerRequiresDiscardConfirmation(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestoreHTTPStore(now)
	handler := restoreHandlerForTest(store, func() string { return "op_restore" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restoreRequest(`{"save_point_id":"sp_001","discard_unsaved_changes_confirmed":false}`, "repo_alpha01", "ns_alpha01", "idem_restore"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeRestoreConfirmationRequired {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeRestoreConfirmationRequired)
	}
	if store.createCalls != 0 || store.restoreIntakeCalls != 0 || store.jvsMutationCalls != 0 {
		t.Fatalf("create/restore/gate calls = %d/%d/%d, want rejected before mutable gates", store.createCalls, store.restoreIntakeCalls, store.jvsMutationCalls)
	}
}

func TestRestoreHandlerCreatesQueuedOperationForConfirmedDirectRestore(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestoreHTTPStore(now)
	handler := restoreHandlerForTest(store, func() string { return "op_restore" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restoreRequest(`{"save_point_id":"sp_001","discard_unsaved_changes_confirmed":true}`, "repo_alpha01", "ns_alpha01", "idem_restore"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_restore" || env.OperationState != OperationStateQueued || env.Resource.Type != "repo" || env.Resource.ID != "repo_alpha01" {
		t.Fatalf("envelope = %#v, want queued direct restore repo resource", env)
	}
	spec := store.spec
	if spec.OperationID != "op_restore" || spec.Scope.OperationType != operations.OperationRestore || spec.Phase != operations.OperationPhaseRestoreValidate {
		t.Fatalf("spec operation/scope/phase = %q/%#v/%q", spec.OperationID, spec.Scope, spec.Phase)
	}
	if store.restoreIntakeCalls != 1 || store.genericCreateCalls != 0 || store.previewIntakeCalls != 0 || store.runIntakeCalls != 0 {
		t.Fatalf("intake calls restore/generic/preview/run = %d/%d/%d/%d, want restore-only intake", store.restoreIntakeCalls, store.genericCreateCalls, store.previewIntakeCalls, store.runIntakeCalls)
	}
	if spec.NamespaceID != "ns_alpha01" || spec.RepoID != "repo_alpha01" || len(spec.InputSummary) != 2 || spec.InputSummary["save_point_id"] != "sp_001" || spec.InputSummary["discard_unsaved_changes_confirmed"] != true {
		t.Fatalf("spec namespace/repo/input = %q/%q/%#v, want save point and confirmation only", spec.NamespaceID, spec.RepoID, spec.InputSummary)
	}
	assertRestoreHTTPNoRawCommand(t, spec.InputSummary)
}

func TestRestoreHandlerReusesExistingIdempotentOperationBeforeActivePlanAndMutationGate(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	existing := apiRestoreQueuedOperation(now)
	store := newFakeRestoreHTTPStore(now)
	store.existing = existing
	store.jvsMutation = true
	store.activePlanErr = nil
	store.activePlan = apiRestorePreviewPendingPlan(now)
	handler := restoreHandlerForTest(store, func() string { return "op_new" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restoreRequest(`{"save_point_id":"sp_001","discard_unsaved_changes_confirmed":true}`, "repo_alpha01", "ns_alpha01", "idem_restore"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != existing.ID || store.createCalls != 0 || store.restoreIntakeCalls != 0 || store.jvsMutationCalls != 0 || store.activePlanCalls != 0 {
		t.Fatalf("envelope/create/gates = %#v/%d/%d/%d/%d, want existing operation reused before mutable gates", env, store.createCalls, store.restoreIntakeCalls, store.jvsMutationCalls, store.activePlanCalls)
	}
}

func TestRestoreHandlerMapsAtomicIntakeGateFailures(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	tests := []struct {
		name     string
		err      error
		wantCode ErrorCode
	}{
		{name: "same repo jvs mutation", err: operations.ErrRepoJVSMutationInProgress, wantCode: CodeRepoJVSMutationInProgress},
		{name: "active restore plan", err: operations.ErrActiveRestorePlan, wantCode: CodeOperationRecoveryRequired},
		{name: "different idempotency body", err: operations.ErrIdempotencyConflict, wantCode: CodeIdempotencyConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeRestoreHTTPStore(now)
			store.createErr = tt.err
			handler := restoreHandlerForTest(store, func() string { return "op_restore" }, func() time.Time { return now })
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, restoreRequest(`{"save_point_id":"sp_001","discard_unsaved_changes_confirmed":true}`, "repo_alpha01", "ns_alpha01", "idem_restore"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode {
				t.Fatalf("error = %#v, want %s", env.Error, tt.wantCode)
			}
			if store.restoreIntakeCalls != 1 || store.genericCreateCalls != 0 {
				t.Fatalf("intake calls restore/generic = %d/%d, want restore-only intake", store.restoreIntakeCalls, store.genericCreateCalls)
			}
		})
	}
}

func TestInternalAPIShellServesRestore(t *testing.T) {
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
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restoreRequest(`{"save_point_id":"sp_001","discard_unsaved_changes_confirmed":true}`, "repo_alpha01", "ns_alpha01", "idem_restore"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	if store.spec.Scope.OperationType != operations.OperationRestore {
		t.Fatalf("created operation type = %s, want %s", store.spec.Scope.OperationType, operations.OperationRestore)
	}
}

func TestRestoreHandlerSourceDoesNotCallPreviewOrRunHandlers(t *testing.T) {
	assertGoFileDoesNotContain(t, "restore_handler.go", []string{
		"RestorePreviewHandler(",
		"RestoreRunHandler(",
		"restorePreview",
		"restoreRun",
	})
}

func assertGoFileDoesNotContain(t *testing.T, path string, forbidden []string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	source := string(body)
	for _, token := range forbidden {
		if strings.Contains(source, token) {
			t.Fatalf("%s must not contain %q", path, token)
		}
	}
}

func restoreHandlerForTest(store *fakeRestoreHTTPStore, generate OperationIDGenerator, now func() time.Time) http.Handler {
	return RestoreHandler(RestoreHandlerConfig{
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

func restoreRequest(body, repoID, namespaceID, idempotencyKey string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/repos/"+repoID+"/restore", bytes.NewBufferString(body))
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_restore")
	req.Header.Set(auth.HeaderCallerService, "product-caller")
	req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	req.Header.Set(auth.HeaderIdempotencyKey, idempotencyKey)
	req.Header.Set(auth.HeaderActorType, "system")
	req.Header.Set(auth.HeaderActorID, "svc-alpha")
	return req
}

func apiRestoreQueuedOperation(now time.Time) operations.OperationRecord {
	hash, err := operations.HashRequest(restoreCanonicalRequest{RepoID: "repo_alpha01", SavePointID: "sp_001", DiscardUnsavedChangesConfirmed: true})
	if err != nil {
		panic(err)
	}
	return operations.OperationRecord{ID: "op_restore_existing", Type: operations.OperationRestore, State: operations.OperationStateQueued, Phase: operations.OperationPhaseRestoreValidate, IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationRestore, "idem_restore").String(), IdempotencyKey: "idem_restore", RequestHash: hash, CorrelationID: "corr_restore", CallerService: "product-caller", AuthorizedActor: operations.Actor{Type: "system", ID: "svc-alpha"}, Resource: operations.ResourceRef{Type: "repo", ID: "repo_alpha01"}, NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", InputSummary: map[string]any{"save_point_id": "sp_001", "discard_unsaved_changes_confirmed": true}, ExternalResourceIDs: map[string]string{}, CreatedAt: now}
}
