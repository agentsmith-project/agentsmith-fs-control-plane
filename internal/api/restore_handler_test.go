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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

func TestRestoreHandlerRejectsLegacyDiscardConfirmationField(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestoreHTTPStore(now)
	handler := restoreHandlerForTest(store, func() string { return "op_restore" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restoreRequest(`{"save_point_id":"sp_001","discard_unsaved_changes_confirmed":false}`, "repo_alpha01", "ns_alpha01", "idem_restore"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeInvalidID {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeInvalidID)
	}
	if store.createCalls != 0 || store.restoreIntakeCalls != 0 || store.jvsMutationCalls != 0 {
		t.Fatalf("create/restore/gate calls = %d/%d/%d, want rejected before mutable gates", store.createCalls, store.restoreIntakeCalls, store.jvsMutationCalls)
	}
}

func TestRestoreHandlerCreatesQueuedOperationForDirectRestore(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestoreHTTPStore(now)
	handler := restoreHandlerForTest(store, func() string { return "op_restore" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restoreRequest(`{"save_point_id":"sp_001"}`, "repo_alpha01", "ns_alpha01", "idem_restore"))

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
	if spec.NamespaceID != "ns_alpha01" || spec.RepoID != "repo_alpha01" || len(spec.InputSummary) != 1 || spec.InputSummary["save_point_id"] != "sp_001" {
		t.Fatalf("spec namespace/repo/input = %q/%q/%#v, want save point only", spec.NamespaceID, spec.RepoID, spec.InputSummary)
	}
	assertRestoreHTTPNoRawCommand(t, spec.InputSummary)
}

func TestRestoreHandlerReusesExistingIdempotentOperationBeforeMutationGate(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	existing := apiRestoreQueuedOperation(now)
	store := newFakeRestoreHTTPStore(now)
	store.existing = existing
	store.jvsMutation = true
	handler := restoreHandlerForTest(store, func() string { return "op_new" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restoreRequest(`{"save_point_id":"sp_001"}`, "repo_alpha01", "ns_alpha01", "idem_restore"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != existing.ID || store.createCalls != 0 || store.restoreIntakeCalls != 0 || store.jvsMutationCalls != 0 {
		t.Fatalf("envelope/create/gates = %#v/%d/%d/%d, want existing operation reused before mutable gates", env, store.createCalls, store.restoreIntakeCalls, store.jvsMutationCalls)
	}
}

func TestRestoreHandlerMapsAtomicIntakeGateFailures(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	tests := []struct {
		name     string
		err      error
		wantCode ErrorCode
	}{
		{name: "same repo jvs mutation", err: operations.ErrRepoJVSMutationInProgress, wantCode: CodeFileLibraryOperationPending},
		{name: "different idempotency body", err: operations.ErrIdempotencyConflict, wantCode: CodeIdempotencyConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeRestoreHTTPStore(now)
			store.createErr = tt.err
			handler := restoreHandlerForTest(store, func() string { return "op_restore" }, func() time.Time { return now })
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, restoreRequest(`{"save_point_id":"sp_001"}`, "repo_alpha01", "ns_alpha01", "idem_restore"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode {
				t.Fatalf("error = %#v, want %s", env.Error, tt.wantCode)
			}
			if tt.err == operations.ErrRepoJVSMutationInProgress {
				assertBlockingOperationErrorProductSafe(t, rec.Body.String(), env, false)
			}
			if store.restoreIntakeCalls != 1 || store.genericCreateCalls != 0 {
				t.Fatalf("intake calls restore/generic = %d/%d, want restore-only intake", store.restoreIntakeCalls, store.genericCreateCalls)
			}
		})
	}
}

func TestRestoreHandlerMapsOperatorInterventionGateToRecoveryRequired(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestoreHTTPStore(now)
	store.jvsMutationStatus = &RepoJVSMutationGateStatus{
		InProgress:       true,
		OperationID:      "op_manual",
		OperationType:    operations.OperationRestore,
		OperationState:   operations.OperationStateOperatorInterventionRequired,
		RecoveryRequired: true,
	}
	handler := restoreHandlerForTest(store, func() string { return "op_restore" }, func() time.Time { return now })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restoreRequest(`{"save_point_id":"sp_001"}`, "repo_alpha01", "ns_alpha01", "idem_restore"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeFileLibraryOperationRequiresRecovery || env.Error.Retryable {
		t.Fatalf("error = %#v, want non-retryable FILE_LIBRARY_OPERATION_REQUIRES_RECOVERY", env.Error)
	}
	if env.Error.OperationID == nil || *env.Error.OperationID != "op_manual" {
		t.Fatalf("error operation id = %#v, want blocking operation id", env.Error.OperationID)
	}
	assertBlockingOperationErrorProductSafe(t, rec.Body.String(), env, true)
	if store.restoreIntakeCalls != 0 || store.createCalls != 0 {
		t.Fatalf("intake calls restore/create = %d/%d, want typed gate failure before operation create", store.restoreIntakeCalls, store.createCalls)
	}
}

func TestRestoreHandlerBlocksUndrainedWritersBeforeIntake(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	tests := []struct {
		name    string
		exports []sessionstate.ExportSession
		mounts  []sessionstate.WorkloadMountBinding
	}{
		{
			name:    "active read write export",
			exports: []sessionstate.ExportSession{restoreHTTPExportFixture(now, "export_rw_active", sessionstate.AccessModeReadWrite, sessionstate.ExportStatusActive, now.Add(time.Hour))},
		},
		{
			name:   "releasing read write mount",
			mounts: []sessionstate.WorkloadMountBinding{restoreHTTPMountFixture(now, "wmb_releasing", false, sessionstate.MountStatusReleasing, now.Add(time.Hour), nil, nil)},
		},
		{
			name:   "stale read write mount",
			mounts: []sessionstate.WorkloadMountBinding{restoreHTTPMountFixture(now, "wmb_stale", false, sessionstate.MountStatusActive, now.Add(-time.Minute), nil, nil)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeRestoreHTTPStore(now)
			store.exports = tt.exports
			store.mounts = tt.mounts
			handler := restoreHandlerForTest(store, func() string { return "op_restore" }, func() time.Time { return now })
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, restoreRequest(`{"save_point_id":"sp_001"}`, "repo_alpha01", "ns_alpha01", "idem_restore"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != CodeFileLibraryOperationPending || !env.Error.Retryable {
				t.Fatalf("error = %#v, want retryable FILE_LIBRARY_OPERATION_PENDING", env.Error)
			}
			if store.restoreIntakeCalls != 0 || store.createCalls != 0 {
				t.Fatalf("intake calls restore/create = %d/%d, want writer gate before operation intake", store.restoreIntakeCalls, store.createCalls)
			}
			if store.exportSessionCalls != 1 || store.mountSessionCalls != 1 {
				t.Fatalf("session calls exports/mounts = %d/%d, want both session readers", store.exportSessionCalls, store.mountSessionCalls)
			}
			assertBlockingOperationErrorProductSafe(t, rec.Body.String(), env, false)
		})
	}
}

func TestRestoreHandlerAllowsReadOnlyAndDrainedTerminalSessions(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	confirmed := now.Add(-time.Minute)
	tests := []struct {
		name    string
		exports []sessionstate.ExportSession
		mounts  []sessionstate.WorkloadMountBinding
	}{
		{
			name: "read only sessions",
			exports: []sessionstate.ExportSession{
				restoreHTTPExportFixture(now, "export_ro_active", sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive, now.Add(time.Hour)),
			},
			mounts: []sessionstate.WorkloadMountBinding{
				restoreHTTPMountFixture(now, "wmb_ro_active", true, sessionstate.MountStatusActive, now.Add(time.Hour), nil, nil),
			},
		},
		{
			name: "released mount confirmed unmounted",
			mounts: []sessionstate.WorkloadMountBinding{
				restoreHTTPMountFixture(now, "wmb_released", false, sessionstate.MountStatusReleased, now.Add(-time.Hour), &confirmed, nil),
			},
		},
		{
			name: "revoked mount unable to write",
			mounts: []sessionstate.WorkloadMountBinding{
				restoreHTTPMountFixture(now, "wmb_revoked", false, sessionstate.MountStatusRevoked, now.Add(-time.Hour), nil, &confirmed),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeRestoreHTTPStore(now)
			store.exports = tt.exports
			store.mounts = tt.mounts
			handler := restoreHandlerForTest(store, func() string { return "op_restore" }, func() time.Time { return now })
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, restoreRequest(`{"save_point_id":"sp_001"}`, "repo_alpha01", "ns_alpha01", "idem_restore"))

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
			}
			if store.restoreIntakeCalls != 1 || store.createCalls != 1 {
				t.Fatalf("intake calls restore/create = %d/%d, want accepted restore", store.restoreIntakeCalls, store.createCalls)
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

	handler.ServeHTTP(rec, restoreRequest(`{"save_point_id":"sp_001"}`, "repo_alpha01", "ns_alpha01", "idem_restore"))

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
		"RestoreAdmitHandler(",
		"restorePreview",
		"restoreRun",
		"restoreAdmit",
		"RestoreAdmitResponse",
	})
}

func restoreHTTPExportFixture(now time.Time, id string, mode sessionstate.AccessMode, status sessionstate.ExportStatus, expiresAt time.Time) sessionstate.ExportSession {
	heartbeatExpiresAt := now.Add(time.Minute)
	return sessionstate.ExportSession{
		ID:                        id,
		NamespaceID:               "ns_alpha01",
		RepoID:                    "repo_alpha01",
		Mode:                      mode,
		Status:                    status,
		ExpiresAt:                 expiresAt,
		ActiveRequestCount:        1,
		ActiveWriteCount:          restoreHTTPActiveWriteCount(mode),
		LastObservedAt:            &now,
		LastGatewayHeartbeatAt:    &now,
		GatewayHeartbeatExpiresAt: &heartbeatExpiresAt,
		CreatedAt:                 now.Add(-time.Hour),
		UpdatedAt:                 now,
	}
}

func restoreHTTPActiveWriteCount(mode sessionstate.AccessMode) int {
	if mode == sessionstate.AccessModeReadWrite {
		return 1
	}
	return 0
}

func restoreHTTPMountFixture(now time.Time, id string, readOnly bool, status sessionstate.MountStatus, leaseExpiresAt time.Time, confirmedUnmountedAt, unableToWriteAt *time.Time) sessionstate.WorkloadMountBinding {
	return sessionstate.WorkloadMountBinding{
		ID:                   id,
		NamespaceID:          "ns_alpha01",
		RepoID:               "repo_alpha01",
		ReadOnly:             readOnly,
		Status:               status,
		LeaseExpiresAt:       leaseExpiresAt,
		ConfirmedUnmountedAt: confirmedUnmountedAt,
		UnableToWriteAt:      unableToWriteAt,
		CreatedAt:            now.Add(-time.Hour),
		UpdatedAt:            now,
	}
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
	hash, err := operations.HashRequest(restoreCanonicalRequest{RepoID: "repo_alpha01", SavePointID: "sp_001"})
	if err != nil {
		panic(err)
	}
	return operations.OperationRecord{ID: "op_restore_existing", Type: operations.OperationRestore, State: operations.OperationStateQueued, Phase: operations.OperationPhaseRestoreValidate, IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationRestore, "idem_restore").String(), IdempotencyKey: "idem_restore", RequestHash: hash, CorrelationID: "corr_restore", CallerService: "product-caller", AuthorizedActor: operations.Actor{Type: "system", ID: "svc-alpha"}, Resource: operations.ResourceRef{Type: "repo", ID: "repo_alpha01"}, NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", InputSummary: map[string]any{"save_point_id": "sp_001"}, ExternalResourceIDs: map[string]string{}, CreatedAt: now}
}
