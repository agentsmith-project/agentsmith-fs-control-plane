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
	handler := operationInspectionHandlerForTest(reader, authorizer, operationInspectionPolicy(auth.AllowedCaller{CallerService: "product-caller", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleOperationInspector}}), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operationInspectionRequest("op_secret", "", "product-caller"))

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
	handler := operationInspectionHandlerForTest(reader, authorizer, operationInspectionPolicy(auth.AllowedCaller{CallerService: "product-caller", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleOperationInspector}}), sink)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operationInspectionRequest("op_secret", "ns_other", "product-caller"))

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
	handler := operationInspectionHandlerForTest(reader, authorizer, operationInspectionPolicy(auth.AllowedCaller{CallerService: "product-caller", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleOperationInspector}}), sink)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operationInspectionRequest("op_global", "ns_123", "product-caller"))

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
	handler := operationInspectionHandlerForTest(reader, authorizer, operationInspectionPolicy(auth.AllowedCaller{CallerService: "product-caller", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleOperationInspector}}), sink)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operationInspectionRequest("op_secret", "", "product-caller"))

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

func TestOperationInspectionHandlerHidesRepoAdminOnlyBindingAsNotFound(t *testing.T) {
	reader := &fakeInspectionOperationReader{records: map[string]operations.OperationRecord{
		"op_repo_create": operationInspectionRecord("op_repo_create", "ns_123"),
	}}
	bindingReader := &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{
		CallerService: "product-caller",
		Roles:         []resources.CallerRole{resources.CallerRoleRepoAdmin},
	})}
	sink := &fakeAuditSink{}
	handler := OperationInspectionHandler(OperationInspectionHandlerConfig{
		Reader:                    reader,
		StoredNamespaceAuthorizer: operationInspectionNamespaceBindingAuthorizer{Reader: bindingReader},
		AllowedCallers: operationInspectionPolicy(auth.AllowedCaller{
			CallerService: "product-caller",
			Kind:          auth.CallerKindProduct,
			Roles:         []auth.Role{auth.RoleOperationInspector},
		}),
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AuditSink:         sink,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operationInspectionRequest("op_repo_create", "", "product-caller"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", rec.Code, rec.Body.String())
	}
	if bindingReader.calls != 1 || bindingReader.namespaceID != "ns_123" {
		t.Fatalf("binding reads = %d namespace = %q, want stored namespace authorization", bindingReader.calls, bindingReader.namespaceID)
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

func TestDiscoverySurfacesCallerOperationInspectionRedactsCallerUnsafeFields(t *testing.T) {
	reader := &fakeInspectionOperationReader{records: map[string]operations.OperationRecord{
		"op_secret": operationInspectionRecord("op_secret", "ns_123"),
	}}
	authorizer := &fakeStoredInspectionAuthorizer{allowed: map[string]bool{"ns_123": true}}
	handler := operationInspectionHandlerForTest(reader, authorizer, operationInspectionPolicy(auth.AllowedCaller{CallerService: "product-caller", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleOperationInspector}}), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operationInspectionRequest("op_secret", "", "product-caller"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "repair_outcome") || strings.Contains(body, "operator_repair") {
		t.Fatalf("read-only operation inspection leaked repair/write surface fields: %s", body)
	}
	assertOperationInspectionResponseDoesNotLeak(t, body)
}

func TestSecretPathRedactionCallerRepoAndOperationResponsesDoNotLeakStorageMaterial(t *testing.T) {
	repoReader := &fakeRepoReader{repos: []resources.Repo{repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive)}}
	repoHandler := repoReadHandlerForTest(repoReader, namespaceBindingAllowedPolicy(auth.RoleRepoAdmin), nil)

	for _, req := range []*http.Request{
		repoReadRequest(http.MethodGet, "/internal/v1/repos/repo_123", "ns_123"),
		repoReadRequest(http.MethodGet, "/internal/v1/repos?namespace_id=ns_123", "ns_123"),
	} {
		rec := httptest.NewRecorder()
		repoHandler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d body = %s, want 200", req.Method, req.URL.String(), rec.Code, rec.Body.String())
		}
		assertSecretPathRedactionCorpusNotLeaked(t, rec.Body.String())
	}

	opReader := &fakeInspectionOperationReader{records: map[string]operations.OperationRecord{
		"op_secret": operationInspectionRecord("op_secret", "ns_123"),
	}}
	authorizer := &fakeStoredInspectionAuthorizer{allowed: map[string]bool{"ns_123": true}}
	opHandler := operationInspectionHandlerForTest(opReader, authorizer, operationInspectionPolicy(auth.AllowedCaller{CallerService: "product-caller", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleOperationInspector}}), nil)
	rec := httptest.NewRecorder()

	opHandler.ServeHTTP(rec, operationInspectionRequest("op_secret", "", "product-caller"))

	if rec.Code != http.StatusOK {
		t.Fatalf("operation inspection status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	assertSecretPathRedactionCorpusNotLeaked(t, rec.Body.String())
	assertOperationInspectionResponseDoesNotLeak(t, rec.Body.String())
}

func TestSecretPathRedactionOperatorInspectionResponseDoesNotLeakStorageMaterial(t *testing.T) {
	reader := &fakeInspectionOperationReader{records: map[string]operations.OperationRecord{
		"op_global": operationInspectionRecord("op_global", ""),
	}}
	authorizer := &fakeStoredInspectionAuthorizer{allowed: map[string]bool{"ns_123": true}}
	handler := OperationInspectionHandler(OperationInspectionHandlerConfig{
		Reader:                    reader,
		StoredNamespaceAuthorizer: authorizer,
		AllowedCallers:            operationInspectionPolicy(auth.AllowedCaller{CallerService: "ops-service", Kind: auth.CallerKindOperator, Roles: []auth.Role{auth.RoleOperatorAdmin}}),
		PrincipalResolver:         fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{Subject: "svc:ops-service", CanonicalCallerService: "ops-service"}},
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operationInspectionRequest("op_global", "ns_ignored", "ops-service"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if authorizer.calls != 0 {
		t.Fatalf("authorizer calls = %d, want operator inspection independent of namespace binding auth", authorizer.calls)
	}
	body := rec.Body.String()
	for _, forbidden := range []string{
		"operator_repair",
		"repair_outcome",
		"terminalize_unsupported_intervention_as_failed",
		"affected_ids",
		"evidence_ref",
		`"before":`,
		`"after":`,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("operator inspection response leaked repair/write surface field %q: %s", forbidden, body)
		}
	}
	assertSecretPathRedactionCorpusNotLeaked(t, body)
	assertOperationInspectionResponseDoesNotLeak(t, body)
}

func TestDiscoverySurfacesOperatorInspectionGlobalRecordIsReadOnlyRedactedAndDistinctFromRepair(t *testing.T) {
	reader := &fakeInspectionOperationReader{records: map[string]operations.OperationRecord{
		"op_global": operationInspectionRecord("op_global", ""),
	}}
	authorizer := &fakeStoredInspectionAuthorizer{allowed: map[string]bool{"ns_123": true}}
	handler := OperationInspectionHandler(OperationInspectionHandlerConfig{
		Reader:                    reader,
		StoredNamespaceAuthorizer: authorizer,
		AllowedCallers:            operationInspectionPolicy(auth.AllowedCaller{CallerService: "ops-service", Kind: auth.CallerKindOperator, Roles: []auth.Role{auth.RoleOperatorAdmin}}),
		PrincipalResolver:         fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{Subject: "svc:ops-service", CanonicalCallerService: "ops-service"}},
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operationInspectionRequest("op_global", "ns_ignored", "ops-service"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if reader.calls != 1 || reader.operationID != "op_global" {
		t.Fatalf("reader calls/op = %d/%q, want op_global", reader.calls, reader.operationID)
	}
	if authorizer.calls != 0 {
		t.Fatalf("authorizer calls = %d, want operator inspection independent of namespace binding auth", authorizer.calls)
	}
	body := rec.Body.String()
	for _, want := range []string{`"operation_id":"op_global"`, `"namespace_id":null`, `"lease_owner":null`, `"external_resource_ids":{`, `"input_summary":{`, `"jvs_json_output":`, `"verification_result":`} {
		if !strings.Contains(body, want) {
			t.Fatalf("operator inspection response missing %s: %s", want, body)
		}
	}
	for _, forbidden := range []string{
		"operator_repair",
		"repair_outcome",
		"terminalize_unsupported_intervention_as_failed",
		"affected_ids",
		"evidence_ref",
		`"before":`,
		`"after":`,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("operator inspection response leaked repair/write surface field %q: %s", forbidden, body)
		}
	}
	assertOperationInspectionResponseDoesNotLeak(t, body)
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
				CallerService: "product-caller",
				Roles:         []resources.CallerRole{resources.CallerRoleOperationInspector},
			})},
			wantHTTP: http.StatusInternalServerError,
			wantCode: CodeInternalError,
		},
		{
			name: "invalid stored caller invariant",
			reader: &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{
				CallerService: "product-caller",
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
			handler := operationInspectionHandlerForTest(opReader, operationInspectionNamespaceBindingAuthorizer{Reader: tt.reader}, operationInspectionPolicy(auth.AllowedCaller{CallerService: "product-caller", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleOperationInspector}}), nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, operationInspectionRequest("op_secret", "", "product-caller"))

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
	handler := operationInspectionHandlerForTest(reader, authorizer, operationInspectionPolicy(auth.AllowedCaller{CallerService: "product-caller", Kind: auth.CallerKindOperator, Roles: []auth.Role{auth.RoleOperatorAdmin}}), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operationInspectionRequest("op_global", "", "product-caller"))

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
			handler := operationInspectionHandlerForTest(reader, &fakeStoredInspectionAuthorizer{}, operationInspectionPolicy(auth.AllowedCaller{CallerService: "product-caller", Kind: auth.CallerKindOperator, Roles: []auth.Role{auth.RoleOperatorAdmin}}), nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, operationInspectionRequest("op_missing", "", "product-caller"))

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
		{name: "invalid operation id", path: "/internal/v1/operations/bad", reader: &fakeInspectionOperationReader{}, policy: operationInspectionPolicy(auth.AllowedCaller{CallerService: "product-caller", Kind: auth.CallerKindOperator, Roles: []auth.Role{auth.RoleOperatorAdmin}}), wantHTTP: http.StatusBadRequest, wantCode: CodeInvalidID},
		{name: "missing reader", path: "/internal/v1/operations/op_123", policy: operationInspectionPolicy(auth.AllowedCaller{CallerService: "product-caller", Kind: auth.CallerKindOperator, Roles: []auth.Role{auth.RoleOperatorAdmin}}), wantHTTP: http.StatusInternalServerError, wantCode: CodeInternalError},
		{name: "missing policy", path: "/internal/v1/operations/op_123", reader: &fakeInspectionOperationReader{}, wantHTTP: http.StatusInternalServerError, wantCode: CodeInternalError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := operationInspectionHandlerForTest(tt.reader, &fakeStoredInspectionAuthorizer{}, tt.policy, nil)
			req := operationInspectionRequest("op_123", "", "product-caller")
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
		IdempotencyScope: "product-caller::repo_create:idem-visible",
		IdempotencyKey:   "idem-visible",
		RequestHash:      "sha256:visible",
		CorrelationID:    "corr_operation",
		CallerService:    "product-caller",
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
				"checksum":                 "sha256:internal-checksum",
				"digest":                   "sha256:internal-digest",
				"capacity_bytes":           123456,
				"tree_scan":                "internal-tree-scan",
				"file_count":               42,
				"payload_tree":             map[string]any{"root": "payload-tree-root"},
				"sync_state":               "internal-sync-state",
				"proof":                    "internal-proof",
				"internal_path":            "/srv/afscp/internal/path",
				"target_control_root":      "afscp/namespaces/ns_123/repos/repo_456/control",
				"control_volume_subdir":    "afscp/namespaces/ns_123/repos/repo_123/control",
				"payload_volume_subdir":    "afscp/namespaces/ns_123/repos/repo_123/payload",
				"run_command":              "jvs restore --run plan-secret",
				"raw_command":              "jvs afscp --control-root raw-control --home raw-home restore",
				"recommended_next_command": "jvs restore --run recommended-secret",
				"restore_command":          "jvs restore --run restore-secret",
				"mount_command":            "juicefs mount repo_main /mnt/workspace",
				"raw_mount_command":        "juicefs mount repo_raw /mnt/raw",
				"direct_mount_command":     "juicefs mount repo_direct /mnt/direct",
				"command":                  "jvs init /srv/afscp/namespaces/ns_123/repos/repo_123/payload",
			},
			"array_storage": []any{
				map[string]any{
					"control_root":          "/srv/afscp/namespaces/ns_123/repos/repo_123/control",
					"control_volume_subdir": "afscp/namespaces/ns_123/repos/repo_123/control",
					"run_command":           "jvs restore --run array-secret",
					"mount_command":         "juicefs mount repo_array /mnt/array",
				},
			},
			"string_storage": map[string]string{
				"payload_root":          "/srv/afscp/namespaces/ns_123/repos/repo_123/payload",
				"payload_volume_subdir": "afscp/namespaces/ns_123/repos/repo_123/payload",
				"command":               "jvs doctor /srv/afscp/namespaces/ns_123/repos/repo_123/control",
				"raw_mount_command":     "juicefs mount repo_string /mnt/string",
			},
		},
		JVSJSONOutput: map[string]any{
			"stdout":               "token secret",
			"repo_id":              "jvs_repo_alpha",
			"repo_root":            "/srv/afscp/namespaces/ns_123/repos/repo_123",
			"hash":                 "internal-output-hash",
			"proof":                "internal-output-proof",
			"payload_tree":         "internal-output-payload-tree",
			"command":              "jvs restore --run output-secret",
			"mount_command":        "juicefs mount repo_output /mnt/output",
			"raw_mount_command":    "juicefs mount repo_output_raw /mnt/output-raw",
			"direct_mount_command": "juicefs mount repo_output_direct /mnt/output-direct",
			"output_map":           map[string]string{"control_root": "/srv/afscp/namespaces/ns_123/repos/repo_123/control"},
		},
		VerificationResult: map[string]any{
			"healthy":               true,
			"stderr":                "password secret",
			"checksum":              "sha256:internal-verification-checksum",
			"digest":                "sha256:internal-verification-digest",
			"tree_scan_result":      "internal-verification-tree-scan",
			"file_count":            7,
			"raw command":           "jvs afscp --control-root verify-control --home verify-home status",
			"control_volume_subdir": "afscp/namespaces/ns_123/repos/repo_123/control",
			"payload_volume_subdir": "afscp/namespaces/ns_123/repos/repo_123/payload",
			"mount_command":         "juicefs mount repo_verify /mnt/verify",
			"raw_mount_command":     "juicefs mount repo_verify_raw /mnt/verify-raw",
			"direct_mount_command":  "juicefs mount repo_verify_direct /mnt/verify-direct",
		},
		Error: &operations.OperationError{Code: "FAILED", Message: "failed with password secret", CorrelationID: "corr-secret", OperationID: operationID, Details: map[string]any{
			"token":                "secret-token",
			"restore_command":      "jvs restore --run error-secret",
			"mount_command":        "juicefs mount repo_error /mnt/error",
			"raw_mount_command":    "juicefs mount repo_error_raw /mnt/error-raw",
			"direct_mount_command": "juicefs mount repo_error_direct /mnt/error-direct",
			"payload_file_count":   99,
			"internal_path":        "/srv/afscp/error/internal",
			"raw_command":          "jvs afscp --control-root error-control --home error-home doctor",
			"target_root_paths":    []any{map[string]any{"target_control_root": "/srv/afscp/namespaces/ns_123/repos/repo_456/control"}},
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
		"juicefs mount",
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
		"checksum",
		"digest",
		"capacity_bytes",
		"tree_scan",
		"tree_scan_result",
		"file_count",
		"payload_tree",
		"payload_file_count",
		"sync_state",
		"proof",
		"internal_path",
		"internal-output",
		"internal-verification",
		"target_control_root",
		"control_volume_subdir",
		"payload_volume_subdir",
		"run_command",
		"raw_command",
		"raw command",
		"recommended_next_command",
		"restore_command",
		"mount_command",
		"raw_mount_command",
		"direct_mount_command",
		"command",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("operation inspection response leaked %q: %s", forbidden, body)
		}
	}
}

func assertSecretPathRedactionCorpusNotLeaked(t *testing.T, body string) {
	t.Helper()
	rendered := strings.ToLower(body)
	for _, forbidden := range []string{
		"/srv/afscp",
		".jvs",
		"secretref",
		"secret_ref",
		"metadata_url",
		"redis://",
		"postgres://",
		"token secret",
		"secret-token",
		"password secret",
		"super-secret-password",
		"credential",
		"control_volume_subdir",
		"payload_volume_subdir",
		"control_root",
		"payload_root",
		"checksum",
		"digest",
		"capacity_bytes",
		"tree_scan",
		"file_count",
		"payload_tree",
		"payload_file_count",
		"sync_state",
		"proof",
		"internal_path",
		"afscp/namespaces/ns_123/repos/repo_123/control",
		"afscp/namespaces/ns_123/repos/repo_123/payload",
		"run_command",
		"raw_command",
		"raw command",
		"recommended_next_command",
		"restore_command",
		"mount_command",
		"raw_mount_command",
		"direct_mount_command",
		"jvs restore --run",
		"jvs init",
		"jvs doctor",
		"juicefs mount",
	} {
		if strings.Contains(rendered, strings.ToLower(forbidden)) {
			t.Fatalf("secret/path redaction corpus leaked %q: %s", forbidden, body)
		}
	}
}
