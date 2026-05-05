package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func TestCreateRepoHandlerCreatesOperationIntake(t *testing.T) {
	now := fixedNamespaceNow()
	store := &fakeOperationIntakeStore{}
	handler := createRepoHandlerForTest(store, func() string { return "op_repo" }, func() time.Time { return now }, namespaceBindingAllowedPolicy(auth.RoleRepoAdmin), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, createRepoRequest("ns_123", createRepoRequestBody("ns_123", "repo_123")))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("intake calls = %d, want 1", store.calls)
	}
	spec := store.spec
	wantScope := operations.NewIdempotencyScope("agentsmith-api", "ns_123", operations.OperationRepoCreate, "idem_repo")
	if spec.OperationID != "op_repo" || spec.Scope != wantScope {
		t.Fatalf("spec op/scope = %q/%#v, want op_repo/%#v", spec.OperationID, spec.Scope, wantScope)
	}
	if spec.Phase != operations.OperationPhaseRepoCreateValidate {
		t.Fatalf("phase = %q, want %s", spec.Phase, operations.OperationPhaseRepoCreateValidate)
	}
	if spec.NamespaceID != "ns_123" || spec.RepoID != "repo_123" || spec.Resource.Type != "repo" || spec.Resource.ID != "repo_123" {
		t.Fatalf("namespace/repo/resource = %q/%q/%#v", spec.NamespaceID, spec.RepoID, spec.Resource)
	}
	if len(spec.InputSummary) != 2 || spec.InputSummary["namespace_id"] != "ns_123" || spec.InputSummary["target_repo_id"] != "repo_123" {
		t.Fatalf("input summary = %#v, want safe ids only", spec.InputSummary)
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_repo" || env.OperationState != OperationStateQueued || env.Resource.Type != "repo" || env.Resource.ID != "repo_123" {
		t.Fatalf("envelope = %#v, want queued repo op", env)
	}
	assertOperationEnvelopeDoesNotLeakInternalFields(t, env)
}

func TestCreateRepoHandlerReusesExistingOperation(t *testing.T) {
	store := &fakeOperationIntakeStore{existingOperationID: "op_existing_repo", reused: true}
	handler := createRepoHandlerForTest(store, func() string { return "op_new_repo" }, fixedNamespaceNow, namespaceBindingAllowedPolicy(auth.RoleRepoAdmin), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, createRepoRequest("ns_123", createRepoRequestBody("ns_123", "repo_123")))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_existing_repo" || env.OperationState != OperationStateQueued {
		t.Fatalf("envelope = %#v, want reused existing queued operation", env)
	}
}

func TestCreateRepoHandlerReusesExistingOperationEvenWhenRepoMetadataExists(t *testing.T) {
	store := &fakeOperationIntakeStore{existingOperationID: "op_existing_repo", reused: true, repoAlreadyExists: true}
	handler := createRepoHandlerForTest(store, func() string { return "op_new_repo" }, fixedNamespaceNow, namespaceBindingAllowedPolicy(auth.RoleRepoAdmin), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, createRepoRequest("ns_123", createRepoRequestBody("ns_123", "repo_123")))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200 reuse", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_existing_repo" || env.OperationState != OperationStateQueued {
		t.Fatalf("envelope = %#v, want reused existing queued operation", env)
	}
}

func TestCreateRepoHandlerValidationDeniesBeforeIntake(t *testing.T) {
	tests := []struct {
		name     string
		headerNS string
		body     string
		wantCode ErrorCode
	}{
		{name: "missing namespace header", body: createRepoRequestBody("ns_123", "repo_123"), wantCode: CodeResourceNamespaceMismatch},
		{name: "namespace mismatch", headerNS: "ns_456", body: createRepoRequestBody("ns_123", "repo_123"), wantCode: CodeResourceNamespaceMismatch},
		{name: "missing body namespace", headerNS: "ns_123", body: `{"target_repo_id":"repo_123"}`, wantCode: CodeInvalidID},
		{name: "invalid body namespace", headerNS: "ns_123", body: createRepoRequestBody("bad_ns", "repo_123"), wantCode: CodeResourceNamespaceMismatch},
		{name: "invalid target repo", headerNS: "ns_123", body: createRepoRequestBody("ns_123", "bad_repo"), wantCode: CodeInvalidID},
		{name: "unknown field", headerNS: "ns_123", body: `{"namespace_id":"ns_123","target_repo_id":"repo_123","raw_path":"/secret"}`, wantCode: CodeInvalidID},
		{name: "malformed json", headerNS: "ns_123", body: `{"namespace_id":`, wantCode: CodeInvalidID},
		{name: "trailing json", headerNS: "ns_123", body: createRepoRequestBody("ns_123", "repo_123") + ` {}`, wantCode: CodeInvalidID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			sink := &fakeAuditSink{}
			handler := createRepoHandlerForTest(store, func() string { return "op_repo" }, fixedNamespaceNow, namespaceBindingAllowedPolicy(auth.RoleRepoAdmin), sink)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, createRepoRequest(tt.headerNS, tt.body))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
			}
			if store.calls != 0 {
				t.Fatalf("intake calls = %d, want 0", store.calls)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode {
				t.Fatalf("error code = %s, want %s", env.Error.Code, tt.wantCode)
			}
			if len(sink.events) != 1 {
				t.Fatalf("audit events = %#v, want validation denial", sink.events)
			}
			if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "raw_path") {
				t.Fatalf("validation leaked sensitive detail: %s", rec.Body.String())
			}
		})
	}
}

func TestCreateRepoHandlerReturnsRepoAlreadyExistsFromDedicatedIntake(t *testing.T) {
	store := &fakeOperationIntakeStore{repoAlreadyExists: true}
	sink := &fakeAuditSink{}
	handler := createRepoHandlerForTest(store, func() string { return "op_repo" }, fixedNamespaceNow, namespaceBindingAllowedPolicy(auth.RoleRepoAdmin), sink)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, createRepoRequest("ns_123", createRepoRequestBody("ns_123", "repo_123")))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("intake calls = %d, want durable intake boundary called", store.calls)
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeRepoAlreadyExists || env.Error.Retryable {
		t.Fatalf("error = %#v, want REPO_ALREADY_EXISTS non-retryable", env.Error)
	}
	if len(sink.events) != 1 {
		t.Fatalf("audit events = %#v, want denied audit", sink.events)
	}
}

func TestCreateRepoHandlerMapsIntakeErrorsWithoutLeakingDetails(t *testing.T) {
	tests := []struct {
		name     string
		store    *fakeOperationIntakeStore
		wantCode ErrorCode
		status   int
		retry    bool
	}{
		{name: "idempotency conflict", store: &fakeOperationIntakeStore{err: operations.ErrIdempotencyConflict}, wantCode: CodeIdempotencyConflict, status: http.StatusConflict},
		{name: "intake outage", store: &fakeOperationIntakeStore{err: errors.New("postgres password=secret failed")}, wantCode: CodeStorageUnavailable, status: http.StatusServiceUnavailable, retry: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := createRepoHandlerForTest(tt.store, func() string { return "op_repo" }, fixedNamespaceNow, namespaceBindingAllowedPolicy(auth.RoleRepoAdmin), nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, createRepoRequest("ns_123", createRepoRequestBody("ns_123", "repo_123")))

			if rec.Code != tt.status {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.status)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode || env.Error.Retryable != tt.retry {
				t.Fatalf("error = %#v, want %s retry=%v", env.Error, tt.wantCode, tt.retry)
			}
			if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "postgres") {
				t.Fatalf("error leaked raw detail: %s", rec.Body.String())
			}
		})
	}
}

func createRepoRequest(namespaceID string, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/repos", strings.NewReader(body))
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_repo")
	req.Header.Set(auth.HeaderCallerService, "agentsmith-api")
	req.Header.Set(auth.HeaderIdempotencyKey, "idem_repo")
	req.Header.Set(auth.HeaderActorType, "user")
	req.Header.Set(auth.HeaderActorID, "user_123")
	if namespaceID != "" {
		req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	}
	return req
}

func createRepoRequestBody(namespaceID, repoID string) string {
	return `{"namespace_id":"` + namespaceID + `","target_repo_id":"` + repoID + `"}`
}

func createRepoHandlerForTest(store RepoCreateOperationIntakeStore, generate OperationIDGenerator, now func() time.Time, policy AllowedCallerPolicy, sink *fakeAuditSink) http.Handler {
	var auditSink audit.Sink
	if sink != nil {
		auditSink = sink
	}
	return CreateRepoHandler(CreateRepoHandlerConfig{
		IntakeStore:       store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    policy,
		OperationID:       generate,
		Now:               now,
		AuditSink:         auditSink,
	})
}
