package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestRestoreAdmitDeniesWhenDirectRestoreCapabilityDisabledWithoutCreatingOperation(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestoreHTTPStore(now)
	store.binding = namespacePolicyBindingFixture("ns_alpha01", resources.AllowedCaller{
		CallerService: "product-caller",
		Roles:         []resources.CallerRole{resources.CallerRoleRepoAdmin, resources.CallerRoleRestoreAdmin},
	})
	history := &fakeSavePointHistoryReader{history: SavePointHistory{SavePoints: []SavePointResponse{
		{SavePointID: "sp_001", Message: "first", CreatedAt: "2026-05-05T12:00:00Z", RepoID: "repo_alpha01"},
	}}}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:              namespaceBindingPrincipalResolver(),
		NamespaceReader:                store,
		NamespaceBindingReader:         store,
		RepoReader:                     store,
		RepoFenceReader:                store,
		SavePointMutationGate:          store,
		SavePointHistoryReader:         history,
		OperationIntakeStore:           store,
		GenerateOperationID:            func() string { return "op_restore_should_not_create" },
		Now:                            func() time.Time { return now },
		DirectRestoreAdmissionDisabled: true,
	})

	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, savePointRequest(http.MethodGet, "/internal/v1/repos/repo_alpha01/save-points", "", "ns_alpha01"))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s, want 200", listRec.Code, listRec.Body.String())
	}

	admitRec := httptest.NewRecorder()
	handler.ServeHTTP(admitRec, restoreAdmitRequest(`{"save_point_id":"sp_001","discard_unsaved_changes_confirmed":true}`, "repo_alpha01", "ns_alpha01", "idem_restore_admit"))

	if admitRec.Code != http.StatusForbidden {
		t.Fatalf("admit status = %d body = %s, want 403", admitRec.Code, admitRec.Body.String())
	}
	env := decodeErrorEnvelope(t, admitRec.Body.Bytes())
	if env.Error.Code != CodeCapabilityDenied {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeCapabilityDenied)
	}
	if store.restoreIntakeCalls != 0 || store.createCalls != 0 {
		t.Fatalf("restore intake/create calls = %d/%d, want no durable operation", store.restoreIntakeCalls, store.createCalls)
	}
	if store.jvsMutationCalls == 0 || store.activePlanCalls == 0 || history.calls == 0 {
		t.Fatalf("gate/history calls = %d/%d/%d, want restore admission preflight gates before capability denial", store.jvsMutationCalls, store.activePlanCalls, history.calls)
	}
}

func TestRestoreAdmitReturnsAdmittedWithoutCreatingOperation(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := newFakeRestoreHTTPStore(now)
	history := &fakeSavePointHistoryReader{history: SavePointHistory{SavePoints: []SavePointResponse{
		{SavePointID: "sp_001", Message: "first", CreatedAt: "2026-05-05T12:00:00Z", RepoID: "repo_alpha01"},
	}}}
	handler := RestoreAdmitHandler(RestoreAdmitHandlerConfig{
		RepoReader:        store,
		NamespaceReader:   store,
		BindingReader:     store,
		FenceReader:       store,
		MutationGate:      store,
		RestorePlanReader: store,
		HistoryReader:     history,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    namespaceBindingAllowedPolicy(auth.RoleRestoreAdmin),
		AuditSink:         nil,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, restoreAdmitRequest(`{"save_point_id":"sp_001","discard_unsaved_changes_confirmed":true}`, "repo_alpha01", "ns_alpha01", "idem_restore_admit"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	var response RestoreAdmitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if !response.Admitted || response.RepoID != "repo_alpha01" || response.SavePointID != "sp_001" || response.OperationType != "restore" {
		t.Fatalf("response = %#v, want admitted direct restore preflight", response)
	}
	if store.restoreIntakeCalls != 0 || store.createCalls != 0 {
		t.Fatalf("restore intake/create calls = %d/%d, want no durable operation", store.restoreIntakeCalls, store.createCalls)
	}
}

func TestRestoreAdmitRouteMetadata(t *testing.T) {
	route, ok := RouteMetadataByOperationID("restoreAdmit")
	if !ok {
		t.Fatal("restoreAdmit route metadata missing")
	}
	if route.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", route.Method)
	}
	if route.Path != "/internal/v1/repos/{repoId}/restore:admit" {
		t.Fatalf("path = %q, want restore admit endpoint", route.Path)
	}
	if route.Class != auth.RouteClassNamespaceBound {
		t.Fatalf("class = %q, want namespace-bound", route.Class)
	}
	if route.Mutating {
		t.Fatal("mutating = true, want false because restore admit does not create a durable operation")
	}
	if route.RequiredRole != auth.RoleRestoreAdmin {
		t.Fatalf("required role = %q, want restore_admin", route.RequiredRole)
	}
}

func restoreAdmitRequest(body, repoID, namespaceID, idempotencyKey string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/repos/"+repoID+"/restore:admit", bytes.NewBufferString(body))
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_restore_admit")
	req.Header.Set(auth.HeaderCallerService, "product-caller")
	req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	req.Header.Set(auth.HeaderIdempotencyKey, idempotencyKey)
	req.Header.Set(auth.HeaderActorType, "system")
	req.Header.Set(auth.HeaderActorID, "svc-alpha")
	return req
}
