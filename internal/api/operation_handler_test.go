package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operationinspect"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestOperationInspectionHandlerReturnsRedactedRecordWithoutNamespaceHeader(t *testing.T) {
	reader := &fakeInspectionOperationReader{records: map[string]operations.OperationRecord{
		"op_secret": operationInspectionRecord("op_secret", "ns_123"),
	}}
	authorizer := &fakeStoredInspectionAuthorizer{allowed: map[string]bool{"ns_123": true}}
	handler := operationInspectionHandlerForTest(reader, authorizer, operationInspectionPolicy(auth.AllowedCaller{CallerService: "agentsmith-api", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleOperationInspector}}), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operationInspectionRequest("op_secret", "", "agentsmith-api"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if reader.calls != 1 || reader.operationID != "op_secret" {
		t.Fatalf("reader calls/op = %d/%q, want op_secret", reader.calls, reader.operationID)
	}
	if authorizer.calls != 1 || authorizer.namespaceID != "ns_123" {
		t.Fatalf("authorizer calls/ns = %d/%q, want stored namespace ns_123", authorizer.calls, authorizer.namespaceID)
	}
	body := rec.Body.String()
	for _, want := range []string{`"operation_id":"op_secret"`, `"namespace_id":"ns_123"`, `"repo_id":"repo_123"`, `"lease_owner":null`, `"lease_expires_at":null`, `"external_resource_ids":{`, `"input_summary":{`, `"jvs_json_output":`, `"verification_result":`, `"started_at":null`, `"finished_at":null`} {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %s: %s", want, body)
		}
	}
	assertOperationInspectionResponseDoesNotLeak(t, body)
	var got operations.OperationRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("operation record JSON did not unmarshal: %v", err)
	}
}

func TestOperationInspectionHandlerHidesProductNamespaceMismatchAsNotFoundAndAudits(t *testing.T) {
	reader := &fakeInspectionOperationReader{records: map[string]operations.OperationRecord{
		"op_secret": operationInspectionRecord("op_secret", "ns_123"),
	}}
	authorizer := &fakeStoredInspectionAuthorizer{allowed: map[string]bool{"ns_123": true}}
	sink := &fakeAuditSink{}
	handler := operationInspectionHandlerForTest(reader, authorizer, operationInspectionPolicy(auth.AllowedCaller{CallerService: "agentsmith-api", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleOperationInspector}}), sink)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operationInspectionRequest("op_secret", "ns_other", "agentsmith-api"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", rec.Code, rec.Body.String())
	}
	if authorizer.calls != 0 {
		t.Fatalf("authorizer calls = %d, want namespace mismatch denied before binding auth", authorizer.calls)
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeOperationNotFound {
		t.Fatalf("error code = %s, want OPERATION_NOT_FOUND", env.Error.Code)
	}
	if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("audit events = %#v, want denied audit", sink.events)
	}
	assertOperationInspectionResponseDoesNotLeak(t, rec.Body.String())
}

func TestOperationInspectionHandlerHidesProductGlobalDeniedAsNotFoundAndAudits(t *testing.T) {
	reader := &fakeInspectionOperationReader{records: map[string]operations.OperationRecord{
		"op_global": operationInspectionRecord("op_global", ""),
	}}
	authorizer := &fakeStoredInspectionAuthorizer{allowed: map[string]bool{"ns_123": true}}
	sink := &fakeAuditSink{}
	handler := operationInspectionHandlerForTest(reader, authorizer, operationInspectionPolicy(auth.AllowedCaller{CallerService: "agentsmith-api", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleOperationInspector}}), sink)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operationInspectionRequest("op_global", "ns_123", "agentsmith-api"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeOperationNotFound {
		t.Fatalf("error code = %s, want OPERATION_NOT_FOUND", env.Error.Code)
	}
	if authorizer.calls != 0 {
		t.Fatalf("authorizer calls = %d, want no binding auth for global product denial", authorizer.calls)
	}
	if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("audit events = %#v, want denied audit", sink.events)
	}
	assertOperationInspectionResponseDoesNotLeak(t, rec.Body.String())
}

func TestOperationInspectionHandlerRequiresStoredBindingForProductCaller(t *testing.T) {
	reader := &fakeInspectionOperationReader{records: map[string]operations.OperationRecord{
		"op_secret": operationInspectionRecord("op_secret", "ns_123"),
	}}
	authorizer := &fakeStoredInspectionAuthorizer{allowed: map[string]bool{"ns_123": false}}
	sink := &fakeAuditSink{}
	handler := operationInspectionHandlerForTest(reader, authorizer, operationInspectionPolicy(auth.AllowedCaller{CallerService: "agentsmith-api", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleOperationInspector}}), sink)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operationInspectionRequest("op_secret", "", "agentsmith-api"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", rec.Code, rec.Body.String())
	}
	if authorizer.calls != 1 {
		t.Fatalf("authorizer calls = %d, want stored binding authorization checked", authorizer.calls)
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeOperationNotFound {
		t.Fatalf("error code = %s, want OPERATION_NOT_FOUND", env.Error.Code)
	}
	if len(sink.events) != 1 {
		t.Fatalf("audit events = %#v, want denied audit", sink.events)
	}
	assertOperationInspectionResponseDoesNotLeak(t, rec.Body.String())
}

func TestOperationInspectionHandlerMapsStoredBindingAuthorizationErrors(t *testing.T) {
	tests := []struct {
		name     string
		reader   *fakeNamespaceVolumeBindingReader
		wantHTTP int
		wantCode ErrorCode
		retry    bool
	}{
		{
			name:     "binding store outage",
			reader:   &fakeNamespaceVolumeBindingReader{err: errors.New("postgres password=secret failed")},
			wantHTTP: http.StatusServiceUnavailable,
			wantCode: CodeStorageUnavailable,
			retry:    true,
		},
		{
			name: "invalid stored binding invariant",
			reader: &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_other", resources.AllowedCaller{
				CallerService: "agentsmith-api",
				Roles:         []resources.CallerRole{resources.CallerRoleOperationInspector},
			})},
			wantHTTP: http.StatusInternalServerError,
			wantCode: CodeInternalError,
		},
		{
			name: "invalid stored caller invariant",
			reader: &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{
				CallerService: "agentsmith-api",
				Roles:         []resources.CallerRole{resources.CallerRoleVolumeAdmin},
			})},
			wantHTTP: http.StatusInternalServerError,
			wantCode: CodeInternalError,
		},
		{
			name:     "missing binding denies as not found for product",
			reader:   &fakeNamespaceVolumeBindingReader{err: sql.ErrNoRows},
			wantHTTP: http.StatusNotFound,
			wantCode: CodeOperationNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opReader := &fakeInspectionOperationReader{records: map[string]operations.OperationRecord{
				"op_secret": operationInspectionRecord("op_secret", "ns_123"),
			}}
			handler := operationInspectionHandlerForTest(opReader, operationInspectionNamespaceBindingAuthorizer{Reader: tt.reader}, operationInspectionPolicy(auth.AllowedCaller{CallerService: "agentsmith-api", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleOperationInspector}}), nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, operationInspectionRequest("op_secret", "", "agentsmith-api"))

			if rec.Code != tt.wantHTTP {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.wantHTTP)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode || env.Error.Retryable != tt.retry {
				t.Fatalf("error = %#v, want %s retry=%v", env.Error, tt.wantCode, tt.retry)
			}
			assertOperationInspectionResponseDoesNotLeak(t, rec.Body.String())
		})
	}
}

func TestOperationInspectionHandlerAllowsOperatorAdminForGlobalRecord(t *testing.T) {
	reader := &fakeInspectionOperationReader{records: map[string]operations.OperationRecord{
		"op_global": operationInspectionRecord("op_global", ""),
	}}
	authorizer := &fakeStoredInspectionAuthorizer{}
	handler := operationInspectionHandlerForTest(reader, authorizer, operationInspectionPolicy(auth.AllowedCaller{CallerService: "agentsmith-api", Kind: auth.CallerKindOperator, Roles: []auth.Role{auth.RoleOperatorAdmin}}), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operationInspectionRequest("op_global", "", "agentsmith-api"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if authorizer.calls != 0 {
		t.Fatalf("authorizer calls = %d, want no namespace auth for operator global record", authorizer.calls)
	}
	if !strings.Contains(rec.Body.String(), `"namespace_id":null`) {
		t.Fatalf("global operation response missing namespace null: %s", rec.Body.String())
	}
}

func TestOperationInspectionHandlerMapsMissingAndStorageErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantHTTP int
		wantCode ErrorCode
		retry    bool
	}{
		{name: "not found", err: sql.ErrNoRows, wantHTTP: http.StatusNotFound, wantCode: CodeOperationNotFound},
		{name: "storage unavailable", err: errors.New("postgres password=secret failed"), wantHTTP: http.StatusServiceUnavailable, wantCode: CodeStorageUnavailable, retry: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeInspectionOperationReader{err: tt.err}
			handler := operationInspectionHandlerForTest(reader, &fakeStoredInspectionAuthorizer{}, operationInspectionPolicy(auth.AllowedCaller{CallerService: "agentsmith-api", Kind: auth.CallerKindOperator, Roles: []auth.Role{auth.RoleOperatorAdmin}}), nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, operationInspectionRequest("op_missing", "", "agentsmith-api"))

			if rec.Code != tt.wantHTTP {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.wantHTTP)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode || env.Error.Retryable != tt.retry {
				t.Fatalf("error = %#v, want %s retry=%v", env.Error, tt.wantCode, tt.retry)
			}
			assertOperationInspectionResponseDoesNotLeak(t, rec.Body.String())
		})
	}
}

func TestOperationInspectionHandlerValidationAndConfigErrors(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		reader   OperationInspectionReader
		policy   AllowedCallerPolicy
		wantHTTP int
		wantCode ErrorCode
	}{
		{name: "invalid operation id", path: "/internal/v1/operations/bad", reader: &fakeInspectionOperationReader{}, policy: operationInspectionPolicy(auth.AllowedCaller{CallerService: "agentsmith-api", Kind: auth.CallerKindOperator, Roles: []auth.Role{auth.RoleOperatorAdmin}}), wantHTTP: http.StatusBadRequest, wantCode: CodeInvalidID},
		{name: "missing reader", path: "/internal/v1/operations/op_123", policy: operationInspectionPolicy(auth.AllowedCaller{CallerService: "agentsmith-api", Kind: auth.CallerKindOperator, Roles: []auth.Role{auth.RoleOperatorAdmin}}), wantHTTP: http.StatusInternalServerError, wantCode: CodeInternalError},
		{name: "missing policy", path: "/internal/v1/operations/op_123", reader: &fakeInspectionOperationReader{}, wantHTTP: http.StatusInternalServerError, wantCode: CodeInternalError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := operationInspectionHandlerForTest(tt.reader, &fakeStoredInspectionAuthorizer{}, tt.policy, nil)
			req := operationInspectionRequest("op_123", "", "agentsmith-api")
			req.URL.Path = tt.path
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantHTTP {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.wantHTTP)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode {
				t.Fatalf("error code = %s, want %s", env.Error.Code, tt.wantCode)
			}
		})
	}
}

func operationInspectionHandlerForTest(reader OperationInspectionReader, authorizer inspectionAuthorizerForOperationTest, policy AllowedCallerPolicy, sink *fakeAuditSink) http.Handler {
	var auditSink audit.Sink
	if sink != nil {
		auditSink = sink
	}
	return OperationInspectionHandler(OperationInspectionHandlerConfig{
		Reader:                    reader,
		StoredNamespaceAuthorizer: authorizer,
		AllowedCallers:            policy,
		PrincipalResolver:         namespaceBindingPrincipalResolver(),
		AuditSink:                 auditSink,
	})
}

type inspectionAuthorizerForOperationTest interface {
	operationinspect.StoredNamespaceAuthorizer
}

func operationInspectionPolicy(callers ...auth.AllowedCaller) AllowedCallerPolicy {
	return fakeAllowedCallerPolicy{callers: callers}
}

func operationInspectionRequest(operationID, namespaceID, callerService string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/operations/"+operationID, nil)
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_operation")
	req.Header.Set(auth.HeaderCallerService, callerService)
	if namespaceID != "" {
		req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	}
	return req
}

type fakeInspectionOperationReader struct {
	calls       int
	operationID string
	records     map[string]operations.OperationRecord
	err         error
}

type fakeOperationInspectionStoreReader struct {
	reader *fakeInspectionOperationReader
}

func (reader fakeOperationInspectionStoreReader) GetOperation(ctx context.Context, operationID string) (operations.OperationRecord, error) {
	return reader.reader.ReadOperation(ctx, operationID)
}

func (reader *fakeInspectionOperationReader) ReadOperation(_ context.Context, operationID string) (operations.OperationRecord, error) {
	reader.calls++
	reader.operationID = operationID
	if reader.err != nil {
		return operations.OperationRecord{}, reader.err
	}
	record, ok := reader.records[operationID]
	if !ok {
		return operations.OperationRecord{}, sql.ErrNoRows
	}
	return record, nil
}

type fakeStoredInspectionAuthorizer struct {
	calls       int
	namespaceID string
	caller      auth.AllowedCaller
	allowed     map[string]bool
	err         error
}

func (authorizer *fakeStoredInspectionAuthorizer) AllowsOperationInspection(_ context.Context, namespaceID string, caller auth.AllowedCaller) bool {
	allowed, err := authorizer.AllowsOperationInspectionWithError(context.Background(), namespaceID, caller)
	return err == nil && allowed
}

func (authorizer *fakeStoredInspectionAuthorizer) AllowsOperationInspectionWithError(_ context.Context, namespaceID string, caller auth.AllowedCaller) (bool, error) {
	authorizer.calls++
	authorizer.namespaceID = namespaceID
	authorizer.caller = caller
	if authorizer.err != nil {
		return false, authorizer.err
	}
	return authorizer.allowed[namespaceID], nil
}

func operationInspectionRecord(operationID, namespaceID string) operations.OperationRecord {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	return operations.OperationRecord{
		ID:               operationID,
		Type:             operations.OperationRepoCreate,
		State:            operations.OperationStateQueued,
		Phase:            operations.OperationPhaseRepoCreateValidate,
		Attempt:          0,
		IdempotencyScope: "agentsmith-api::repo_create:idem-visible",
		IdempotencyKey:   "idem-visible",
		RequestHash:      "sha256:visible",
		CorrelationID:    "corr_operation",
		CallerService:    "agentsmith-api",
		AuthorizedActor:  operations.Actor{Type: "user", ID: "user_123"},
		Resource:         operations.ResourceRef{Type: "repo", ID: "repo_123"},
		NamespaceID:      namespaceID,
		RepoID:           "repo_123",
		ExternalResourceIDs: map[string]string{
			"jvs_repo_id": "jvs-secret-id",
		},
		InputSummary: map[string]any{
			"safe":     "visible",
			"password": "super-secret-password",
			"raw_path": "/srv/afscp/secret",
			"nested_storage": map[string]any{
				"control_root":             "/srv/afscp/namespaces/ns_123/repos/repo_123/control",
				"payload_root":             "/srv/afscp/namespaces/ns_123/repos/repo_123/payload",
				"control_root_path":        "/srv/afscp/namespaces/ns_123/repos/repo_123/control/.jvs",
				"payload_root_path":        "/srv/afscp/namespaces/ns_123/repos/repo_123/payload",
				"repo_root":                "/srv/afscp/namespaces/ns_123/repos/repo_123",
				"target_control_root":      "afscp/namespaces/ns_123/repos/repo_456/control",
				"control_volume_subdir":    "afscp/namespaces/ns_123/repos/repo_123/control",
				"payload_volume_subdir":    "afscp/namespaces/ns_123/repos/repo_123/payload",
				"run_command":              "jvs restore --run plan-secret",
				"recommended_next_command": "jvs restore --run recommended-secret",
				"restore_command":          "jvs restore --run restore-secret",
				"command":                  "jvs init /srv/afscp/namespaces/ns_123/repos/repo_123/payload",
			},
			"array_storage": []any{
				map[string]any{
					"control_root":          "/srv/afscp/namespaces/ns_123/repos/repo_123/control",
					"control_volume_subdir": "afscp/namespaces/ns_123/repos/repo_123/control",
					"run_command":           "jvs restore --run array-secret",
				},
			},
			"string_storage": map[string]string{
				"payload_root":          "/srv/afscp/namespaces/ns_123/repos/repo_123/payload",
				"payload_volume_subdir": "afscp/namespaces/ns_123/repos/repo_123/payload",
				"command":               "jvs doctor /srv/afscp/namespaces/ns_123/repos/repo_123/control",
			},
		},
		JVSJSONOutput: map[string]any{
			"stdout":     "token secret",
			"repo_id":    "jvs_repo_alpha",
			"repo_root":  "/srv/afscp/namespaces/ns_123/repos/repo_123",
			"command":    "jvs restore --run output-secret",
			"output_map": map[string]string{"control_root": "/srv/afscp/namespaces/ns_123/repos/repo_123/control"},
		},
		VerificationResult: map[string]any{
			"healthy":               true,
			"stderr":                "password secret",
			"control_volume_subdir": "afscp/namespaces/ns_123/repos/repo_123/control",
			"payload_volume_subdir": "afscp/namespaces/ns_123/repos/repo_123/payload",
		},
		Error: &operations.OperationError{Code: "FAILED", Message: "failed with password secret", CorrelationID: "corr-secret", OperationID: operationID, Details: map[string]any{
			"token":             "secret-token",
			"restore_command":   "jvs restore --run error-secret",
			"target_root_paths": []any{map[string]any{"target_control_root": "/srv/afscp/namespaces/ns_123/repos/repo_456/control"}},
		}},
		CreatedAt: now,
	}
}

func assertOperationInspectionResponseDoesNotLeak(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{
		"super-secret-password",
		"/srv/afscp",
		"afscp/namespaces/ns_123/repos/repo_123/control",
		"afscp/namespaces/ns_123/repos/repo_123/payload",
		"jvs restore --run",
		"jvs init",
		"jvs doctor",
		".jvs",
		"token secret",
		"password secret",
		"secret-token",
		"postgres",
		"raw_path",
		"stdout",
		"stderr",
		"control_root",
		"payload_root",
		"control_root_path",
		"payload_root_path",
		"repo_root",
		"target_control_root",
		"control_volume_subdir",
		"payload_volume_subdir",
		"run_command",
		"recommended_next_command",
		"restore_command",
		"command",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("operation inspection response leaked %q: %s", forbidden, body)
		}
	}
}
