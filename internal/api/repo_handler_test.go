package api

import (
	"context"
	"database/sql"
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

func TestGetRepoHandlerReturnsNamespaceBoundProjection(t *testing.T) {
	reader := &fakeRepoReader{repos: []resources.Repo{repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive)}}
	handler := repoReadHandlerForTest(reader, namespaceBindingAllowedPolicy(auth.RoleRepoAdmin), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoReadRequest(http.MethodGet, "/internal/v1/repos/repo_123", "ns_123"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if reader.getInNamespaceCalls != 1 || reader.getCalls != 0 || reader.lastNamespaceID != "ns_123" || reader.lastRepoID != "repo_123" {
		t.Fatalf("scoped/get calls ns/repo = %d/%d %q/%q, want scoped ns_123/repo_123 only", reader.getInNamespaceCalls, reader.getCalls, reader.lastNamespaceID, reader.lastRepoID)
	}
	body := rec.Body.String()
	for _, want := range []string{`"repo_id":"repo_123"`, `"namespace_id":"ns_123"`, `"volume_id":"vol_123"`, `"jvs_repo_id":"jvs_repo_alpha"`, `"status":"active"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("response %s missing %s", body, want)
		}
	}
	assertRepoReadResponseDoesNotLeak(t, body)
}

func TestGetRepoHandlerUsesNamespaceScopedReadBoundary(t *testing.T) {
	reader := &fakeRepoReader{
		repos:             []resources.Repo{repoResourceFixture("ns_other", "repo_123", resources.RepoStatusActive)},
		panicOnGlobalRead: true,
	}
	handler := repoReadHandlerForTest(reader, namespaceBindingAllowedPolicy(auth.RoleRepoAdmin), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoReadRequest(http.MethodGet, "/internal/v1/repos/repo_123", "ns_123"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", rec.Code, rec.Body.String())
	}
	if reader.getInNamespaceCalls != 1 || reader.getCalls != 0 {
		t.Fatalf("get calls scoped/global = %d/%d, want scoped only", reader.getInNamespaceCalls, reader.getCalls)
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeRepoNotFound {
		t.Fatalf("error code = %s, want REPO_NOT_FOUND", env.Error.Code)
	}
	if strings.Contains(rec.Body.String(), "ns_other") {
		t.Fatalf("foreign namespace leaked: %s", rec.Body.String())
	}
}

func TestListReposHandlerReturnsNamespaceBoundProjectionAndLifecycleFilter(t *testing.T) {
	reader := &fakeRepoReader{repos: []resources.Repo{
		repoResourceFixture("ns_123", "repo_active", resources.RepoStatusActive),
		repoResourceFixture("ns_123", "repo_archived", resources.RepoStatusArchived),
		repoResourceFixture("ns_other", "repo_other", resources.RepoStatusActive),
	}}
	handler := repoReadHandlerForTest(reader, namespaceBindingAllowedPolicy(auth.RoleRepoAdmin), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoReadRequest(http.MethodGet, "/internal/v1/repos?namespace_id=ns_123&lifecycle_status=archived", "ns_123"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if reader.listCalls != 1 || reader.lastNamespaceID != "ns_123" {
		t.Fatalf("list calls/ns = %d/%q, want ns_123", reader.listCalls, reader.lastNamespaceID)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"repo_id":"repo_archived"`) || strings.Contains(body, `"repo_id":"repo_active"`) || strings.Contains(body, `"repo_id":"repo_other"`) {
		t.Fatalf("filtered list response = %s", body)
	}
	assertRepoReadResponseDoesNotLeak(t, body)
}

func TestRepoReadHandlerRejectsUnsafeStoredJVSRepoIDWithoutLeaking(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{name: "raw path", id: "/srv/secret"},
		{name: "backslash", id: `bad\id`},
		{name: "equals", id: "bad=id"},
		{name: "whitespace", id: "bad id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive)
			repo.JVSRepoID = tt.id
			reader := &fakeRepoReader{repos: []resources.Repo{repo}}
			handler := repoReadHandlerForTest(reader, namespaceBindingAllowedPolicy(auth.RoleRepoAdmin), nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoReadRequest(http.MethodGet, "/internal/v1/repos/repo_123", "ns_123"))

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d body = %s, want 500", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != CodeInternalError || env.Error.Retryable {
				t.Fatalf("error = %#v, want non-retryable INTERNAL_ERROR", env.Error)
			}
			if strings.Contains(rec.Body.String(), tt.id) || strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "/srv") {
				t.Fatalf("unsafe jvs_repo_id leaked: %s", rec.Body.String())
			}
		})
	}
}

func TestRepoReadHandlerValidationDeniesBeforeStore(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		path     string
		headerNS string
		wantCode ErrorCode
	}{
		{name: "get invalid repo id", method: http.MethodGet, path: "/internal/v1/repos/bad_repo", headerNS: "ns_123", wantCode: CodeInvalidID},
		{name: "get missing namespace header", method: http.MethodGet, path: "/internal/v1/repos/repo_123", wantCode: CodeResourceNamespaceMismatch},
		{name: "list missing query namespace", method: http.MethodGet, path: "/internal/v1/repos", headerNS: "ns_123", wantCode: CodeResourceNamespaceMismatch},
		{name: "list query namespace mismatch", method: http.MethodGet, path: "/internal/v1/repos?namespace_id=ns_456", headerNS: "ns_123", wantCode: CodeResourceNamespaceMismatch},
		{name: "list invalid query namespace", method: http.MethodGet, path: "/internal/v1/repos?namespace_id=bad_ns", headerNS: "bad_ns", wantCode: CodeInvalidID},
		{name: "list invalid lifecycle filter", method: http.MethodGet, path: "/internal/v1/repos?namespace_id=ns_123&lifecycle_status=missing", headerNS: "ns_123", wantCode: CodeInvalidID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeRepoReader{}
			sink := &fakeAuditSink{}
			handler := repoReadHandlerForTest(reader, namespaceBindingAllowedPolicy(auth.RoleRepoAdmin), sink)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoReadRequest(tt.method, tt.path, tt.headerNS))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
			}
			if reader.getCalls != 0 || reader.listCalls != 0 {
				t.Fatalf("store calls get/list = %d/%d, want none", reader.getCalls, reader.listCalls)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode {
				t.Fatalf("error code = %s, want %s", env.Error.Code, tt.wantCode)
			}
			if len(sink.events) != 1 {
				t.Fatalf("audit events = %#v, want validation denial", sink.events)
			}
		})
	}
}

func TestRepoReadHandlerNotFoundNamespaceMismatchAndStoreErrors(t *testing.T) {
	tests := []struct {
		name     string
		reader   *fakeRepoReader
		path     string
		wantHTTP int
		wantCode ErrorCode
		retry    bool
	}{
		{name: "get not found", reader: &fakeRepoReader{getErr: sql.ErrNoRows}, path: "/internal/v1/repos/repo_123", wantHTTP: http.StatusNotFound, wantCode: CodeRepoNotFound},
		{name: "get returned namespace mismatch", reader: &fakeRepoReader{repoInNamespaceOverride: repoResourceFixture("ns_other", "repo_123", resources.RepoStatusActive)}, path: "/internal/v1/repos/repo_123", wantHTTP: http.StatusNotFound, wantCode: CodeRepoNotFound},
		{name: "get storage unavailable", reader: &fakeRepoReader{getErr: errors.New("postgres password=secret failed")}, path: "/internal/v1/repos/repo_123", wantHTTP: http.StatusServiceUnavailable, wantCode: CodeStorageUnavailable, retry: true},
		{name: "list storage unavailable", reader: &fakeRepoReader{listErr: errors.New("sql raw_path=/srv/secret failed")}, path: "/internal/v1/repos?namespace_id=ns_123", wantHTTP: http.StatusServiceUnavailable, wantCode: CodeStorageUnavailable, retry: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := repoReadHandlerForTest(tt.reader, namespaceBindingAllowedPolicy(auth.RoleRepoAdmin), nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoReadRequest(http.MethodGet, tt.path, "ns_123"))

			if rec.Code != tt.wantHTTP {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.wantHTTP)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode || env.Error.Retryable != tt.retry {
				t.Fatalf("error = %#v, want %s retry=%v", env.Error, tt.wantCode, tt.retry)
			}
			if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "postgres") || strings.Contains(rec.Body.String(), "/srv") {
				t.Fatalf("error leaked raw detail: %s", rec.Body.String())
			}
		})
	}
}

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

func repoReadHandlerForTest(reader RepoReader, policy AllowedCallerPolicy, sink *fakeAuditSink) http.Handler {
	var auditSink audit.Sink
	if sink != nil {
		auditSink = sink
	}
	return RepoReadHandler(RepoReadHandlerConfig{
		Reader:            reader,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    policy,
		AuditSink:         auditSink,
	})
}

func repoReadRequest(method, path, namespaceID string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_repo_read")
	req.Header.Set(auth.HeaderCallerService, "agentsmith-api")
	if namespaceID != "" {
		req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	}
	return req
}

type fakeRepoReader struct {
	getCalls                int
	getInNamespaceCalls     int
	listCalls               int
	lastRepoID              string
	lastNamespaceID         string
	repos                   []resources.Repo
	getErr                  error
	getInNamespaceErr       error
	listErr                 error
	panicOnGlobalRead       bool
	repoInNamespaceOverride resources.Repo
}

func (reader *fakeRepoReader) GetRepo(_ context.Context, repoID string) (resources.Repo, error) {
	reader.getCalls++
	reader.lastRepoID = repoID
	if reader.panicOnGlobalRead {
		panic("GetRepo must not be used by repo read handler")
	}
	if reader.getErr != nil {
		return resources.Repo{}, reader.getErr
	}
	for _, repo := range reader.repos {
		if repo.ID == repoID {
			return repo, nil
		}
	}
	return resources.Repo{}, sql.ErrNoRows
}

func (reader *fakeRepoReader) GetRepoInNamespace(_ context.Context, namespaceID, repoID string) (resources.Repo, error) {
	reader.getInNamespaceCalls++
	reader.lastNamespaceID = namespaceID
	reader.lastRepoID = repoID
	if reader.getInNamespaceErr != nil {
		return resources.Repo{}, reader.getInNamespaceErr
	}
	if reader.getErr != nil {
		return resources.Repo{}, reader.getErr
	}
	if reader.repoInNamespaceOverride.ID != "" {
		return reader.repoInNamespaceOverride, nil
	}
	for _, repo := range reader.repos {
		if repo.NamespaceID == namespaceID && repo.ID == repoID {
			return repo, nil
		}
	}
	return resources.Repo{}, sql.ErrNoRows
}

func (reader *fakeRepoReader) ListReposByNamespace(_ context.Context, namespaceID string) ([]resources.Repo, error) {
	reader.listCalls++
	reader.lastNamespaceID = namespaceID
	if reader.listErr != nil {
		return nil, reader.listErr
	}
	out := []resources.Repo{}
	for _, repo := range reader.repos {
		if repo.NamespaceID == namespaceID {
			out = append(out, repo)
		}
	}
	return out, nil
}

func repoResourceFixture(namespaceID, repoID string, status resources.RepoStatus) resources.Repo {
	now := fixedNamespaceNow()
	return resources.Repo{
		ID:                  repoID,
		NamespaceID:         namespaceID,
		VolumeID:            "vol_123",
		JVSRepoID:           "jvs_repo_alpha",
		Kind:                resources.RepoKindRepo,
		Status:              status,
		ControlVolumeSubdir: "afscp/namespaces/" + namespaceID + "/repos/" + repoID + "/control",
		PayloadVolumeSubdir: "afscp/namespaces/" + namespaceID + "/repos/" + repoID + "/payload",
		Lifecycle: resources.RepoLifecycle{
			Status:                   status,
			LastLifecycleOperationID: "op_repo_create",
		},
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
	}
}

func assertRepoReadResponseDoesNotLeak(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"control_volume_subdir", "payload_volume_subdir", "control_root", "payload_root", "raw_path", "/srv", "stdout", "stderr", "secret"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("repo response leaked %q: %s", forbidden, body)
		}
	}
}
