package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func TestUpsertNamespaceHandlerCreatesOperationIntake(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	store := &fakeOperationIntakeStore{}
	handler := upsertNamespaceHandlerForTest(store, func() string { return "op_ns_123" }, func() time.Time { return now }, namespaceBindingAllowedPolicy(auth.RoleNamespaceAdmin))
	req := upsertNamespaceRequest("/internal/v1/namespaces/ns_123", "ns_123", `{"namespace_id":"ns_123"}`)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("intake calls = %d, want 1", store.calls)
	}
	spec := store.spec
	wantScope := operations.NewIdempotencyScope("agentsmith-api", "ns_123", operations.OperationNamespaceUpsert, "idem_namespace")
	if spec.OperationID != "op_ns_123" || spec.Scope != wantScope {
		t.Fatalf("spec op/scope = %q/%#v, want op_ns_123/%#v", spec.OperationID, spec.Scope, wantScope)
	}
	if spec.RequestHash == "" {
		t.Fatal("request hash is empty")
	}
	wantHash, err := operations.HashRequest(upsertNamespaceRequestBody{NamespaceID: "ns_123"})
	if err != nil {
		t.Fatalf("hash canonical request: %v", err)
	}
	if spec.RequestHash != wantHash {
		t.Fatalf("request hash = %q, want %q", spec.RequestHash, wantHash)
	}
	if spec.Phase != operations.OperationPhaseNamespaceUpsertValidate {
		t.Fatalf("phase = %q, want %s", spec.Phase, operations.OperationPhaseNamespaceUpsertValidate)
	}
	if spec.NamespaceID != "ns_123" || spec.Resource.Type != "namespace" || spec.Resource.ID != "ns_123" {
		t.Fatalf("namespace/resource = %q/%#v", spec.NamespaceID, spec.Resource)
	}
	if spec.CorrelationID != "corr_namespace" || spec.CallerService != "agentsmith-api" {
		t.Fatalf("correlation/caller = %q/%q", spec.CorrelationID, spec.CallerService)
	}
	if spec.AuthorizedActor.Type != "user" || spec.AuthorizedActor.ID != "user_123" {
		t.Fatalf("actor = %#v", spec.AuthorizedActor)
	}
	if spec.InputSummary["namespace_id"] != "ns_123" || len(spec.ExternalResourceIDs) != 0 {
		t.Fatalf("summary/external = %#v/%#v", spec.InputSummary, spec.ExternalResourceIDs)
	}

	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_ns_123" || env.OperationState != OperationStateQueued {
		t.Fatalf("envelope id/state = %q/%q, want op_ns_123/queued", env.OperationID, env.OperationState)
	}
	if env.Resource.Type != "namespace" || env.Resource.ID != "ns_123" {
		t.Fatalf("envelope resource = %#v", env.Resource)
	}
	assertOperationEnvelopeDoesNotLeakInternalFields(t, env)
}

func TestDisableNamespaceHandlerCreatesOperationIntake(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	store := &fakeOperationIntakeStore{}
	handler := DisableNamespaceHandler(DisableNamespaceHandlerConfig{
		IntakeStore:       store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		OperationID:       func() string { return "op_ns_disable" },
		Now:               func() time.Time { return now },
		DeploymentPolicy:  namespaceBindingAllowedPolicy(auth.RoleNamespaceAdmin),
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, disableNamespaceRequest("/internal/v1/namespaces/ns_123:disable", "ns_123", `{"reason":"security hold"}`))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("intake calls = %d, want 1", store.calls)
	}
	spec := store.spec
	wantScope := operations.NewIdempotencyScope("agentsmith-api", "ns_123", operations.OperationNamespaceDisable, "idem_namespace")
	if spec.OperationID != "op_ns_disable" || spec.Scope != wantScope {
		t.Fatalf("spec op/scope = %q/%#v, want op_ns_disable/%#v", spec.OperationID, spec.Scope, wantScope)
	}
	if spec.Phase != operations.OperationPhaseNamespaceDisableValidate {
		t.Fatalf("phase = %q, want %s", spec.Phase, operations.OperationPhaseNamespaceDisableValidate)
	}
	if spec.NamespaceID != "ns_123" || spec.Resource.Type != "namespace" || spec.Resource.ID != "ns_123" {
		t.Fatalf("namespace/resource = %q/%#v", spec.NamespaceID, spec.Resource)
	}
	if spec.InputSummary["reason"] != "security hold" {
		t.Fatalf("input summary = %#v, want disable reason", spec.InputSummary)
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_ns_disable" || env.OperationState != OperationStateQueued {
		t.Fatalf("envelope = %#v, want queued namespace disable op", env)
	}
}

func TestUpsertNamespaceHandlerMapsIntakeErrors(t *testing.T) {
	tests := []struct {
		name     string
		store    *fakeOperationIntakeStore
		generate OperationIDGenerator
		wantCode ErrorCode
		status   int
		retry    bool
	}{
		{name: "conflict", store: &fakeOperationIntakeStore{err: operations.ErrIdempotencyConflict}, generate: func() string { return "op_ns_123" }, wantCode: CodeIdempotencyConflict, status: http.StatusConflict},
		{name: "store outage", store: &fakeOperationIntakeStore{err: errors.New("postgres dsn password=secret failed")}, generate: func() string { return "op_ns_123" }, wantCode: CodeStorageUnavailable, status: http.StatusServiceUnavailable, retry: true},
		{name: "nil store", generate: func() string { return "op_ns_123" }, wantCode: CodeInternalError, status: http.StatusInternalServerError},
		{name: "empty operation id", store: &fakeOperationIntakeStore{}, generate: func() string { return "" }, wantCode: CodeInternalError, status: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := upsertNamespaceHandlerForTest(tt.store, tt.generate, fixedNamespaceNow, namespaceBindingAllowedPolicy(auth.RoleNamespaceAdmin))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, upsertNamespaceRequest("/internal/v1/namespaces/ns_123", "ns_123", `{"namespace_id":"ns_123"}`))

			if rec.Code != tt.status {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.status)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode || env.Error.Retryable != tt.retry {
				t.Fatalf("error = %#v, want code/retry %s/%v", env.Error, tt.wantCode, tt.retry)
			}
			if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "postgres") {
				t.Fatalf("error leaked raw detail: %s", rec.Body.String())
			}
		})
	}
}

func TestUpsertNamespaceHandlerReturnsReusedOperationEnvelope(t *testing.T) {
	store := &fakeOperationIntakeStore{existingOperationID: "op_existing_ns", reused: true}
	handler := upsertNamespaceHandlerForTest(store, func() string { return "op_new_ns" }, fixedNamespaceNow, namespaceBindingAllowedPolicy(auth.RoleNamespaceAdmin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, upsertNamespaceRequest("/internal/v1/namespaces/ns_123", "ns_123", `{"namespace_id":"ns_123"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_existing_ns" || env.OperationState != OperationStateQueued {
		t.Fatalf("envelope = %#v, want reused existing queued operation", env)
	}
	assertOperationEnvelopeDoesNotLeakInternalFields(t, env)
}

func TestUpsertNamespaceHandlerValidationDeniesBeforeIntake(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		headerNS string
		body     string
		wantCode ErrorCode
	}{
		{name: "invalid path namespace", path: "/internal/v1/namespaces/bad_ns", headerNS: "bad_ns", body: `{"namespace_id":"bad_ns"}`, wantCode: CodeInvalidID},
		{name: "missing namespace header", path: "/internal/v1/namespaces/ns_123", body: `{"namespace_id":"ns_123"}`, wantCode: CodeResourceNamespaceMismatch},
		{name: "mismatched namespace header", path: "/internal/v1/namespaces/ns_123", headerNS: "ns_456", body: `{"namespace_id":"ns_123"}`, wantCode: CodeResourceNamespaceMismatch},
		{name: "body namespace mismatch", path: "/internal/v1/namespaces/ns_123", headerNS: "ns_123", body: `{"namespace_id":"ns_456"}`, wantCode: CodeResourceNamespaceMismatch},
		{name: "malformed json", path: "/internal/v1/namespaces/ns_123", headerNS: "ns_123", body: `{"namespace_id":`, wantCode: CodeInvalidID},
		{name: "trailing garbage", path: "/internal/v1/namespaces/ns_123", headerNS: "ns_123", body: `{"namespace_id":"ns_123"} garbage`, wantCode: CodeInvalidID},
		{name: "unknown field", path: "/internal/v1/namespaces/ns_123", headerNS: "ns_123", body: `{"namespace_id":"ns_123","secret":"x"}`, wantCode: CodeInvalidID},
		{name: "missing body namespace", path: "/internal/v1/namespaces/ns_123", headerNS: "ns_123", body: `{}`, wantCode: CodeInvalidID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			handler := upsertNamespaceHandlerForTest(store, func() string { return "op_ns_123" }, fixedNamespaceNow, namespaceBindingAllowedPolicy(auth.RoleNamespaceAdmin))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, upsertNamespaceRequest(tt.path, tt.headerNS, tt.body))

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
		})
	}
}

func TestUpsertNamespaceHandlerUsesDeploymentPolicyAndNotBindingPolicy(t *testing.T) {
	t.Run("deployment policy grants", func(t *testing.T) {
		store := &fakeOperationIntakeStore{}
		binding := &fakeNamespaceVolumeBindingReader{}
		handler := NamespaceUpsertHandler(NamespaceUpsertHandlerConfig{
			IntakeStore:       store,
			PrincipalResolver: namespaceBindingPrincipalResolver(),
			OperationID:       func() string { return "op_ns_123" },
			Now:               fixedNamespaceNow,
			DeploymentPolicy: RouteAwareAllowedCallerPolicy{
				DeploymentNamespace: namespaceBindingAllowedPolicy(auth.RoleNamespaceAdmin),
				NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: binding},
			},
		})

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, upsertNamespaceRequest("/internal/v1/namespaces/ns_123", "ns_123", `{"namespace_id":"ns_123"}`))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
		}
		if store.calls != 1 {
			t.Fatalf("intake calls = %d, want 1", store.calls)
		}
		if binding.calls != 0 {
			t.Fatalf("binding policy reader calls = %d, want 0", binding.calls)
		}
	})

	t.Run("deployment policy denies before intake", func(t *testing.T) {
		store := &fakeOperationIntakeStore{}
		handler := upsertNamespaceHandlerForTest(store, func() string { return "op_ns_123" }, fixedNamespaceNow, namespaceBindingAllowedPolicy(auth.RoleRepoAdmin))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, upsertNamespaceRequest("/internal/v1/namespaces/ns_123", "ns_123", `{"namespace_id":"ns_123"}`))

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d body = %s, want 403", rec.Code, rec.Body.String())
		}
		if store.calls != 0 {
			t.Fatalf("intake calls = %d, want 0", store.calls)
		}
		env := decodeErrorEnvelope(t, rec.Body.Bytes())
		if env.Error.Code != CodeRoleNotAllowed {
			t.Fatalf("error code = %s, want ROLE_NOT_ALLOWED", env.Error.Code)
		}
	})
}

func TestUpsertNamespaceHandlerDoesNotImportMutationDependencies(t *testing.T) {
	assertNamespaceSourceDoesNotImport(t, "namespace_handler.go", []string{
		"internal/resources",
		"internal/store/postgres",
		"internal/jvs",
		"internal/worker",
	})
}

func assertNamespaceSourceDoesNotImport(t *testing.T, filename string, forbidden []string) {
	t.Helper()
	source, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read %s: %v", filename, err)
	}
	for _, item := range forbidden {
		if strings.Contains(string(source), item) {
			t.Fatalf("%s imported forbidden dependency %q", filename, item)
		}
	}
}

func upsertNamespaceHandlerForTest(store OperationIntakeStore, generate OperationIDGenerator, now func() time.Time, policy AllowedCallerPolicy) http.Handler {
	return NamespaceUpsertHandler(NamespaceUpsertHandlerConfig{
		IntakeStore:       store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		OperationID:       generate,
		Now:               now,
		DeploymentPolicy:  policy,
	})
}

func upsertNamespaceRequest(path string, namespaceID string, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_namespace")
	req.Header.Set(auth.HeaderCallerService, "agentsmith-api")
	req.Header.Set(auth.HeaderIdempotencyKey, "idem_namespace")
	req.Header.Set(auth.HeaderActorType, "user")
	req.Header.Set(auth.HeaderActorID, "user_123")
	if namespaceID != "" {
		req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	}
	return req
}

func disableNamespaceRequest(path string, namespaceID string, body string) *http.Request {
	req := upsertNamespaceRequest(path, namespaceID, body)
	req.Method = http.MethodPost
	return req
}

func fixedNamespaceNow() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}

func decodeOperationEnvelope(t *testing.T, body []byte) OperationEnvelope {
	t.Helper()
	var envelope OperationEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode operation envelope %s: %v", string(body), err)
	}
	return envelope
}

func TestInternalAPIShellRoutesUpsertNamespaceAndLogs(t *testing.T) {
	var logs bytes.Buffer
	store := &fakeOperationIntakeStore{}
	bindingReader := &fakeNamespaceVolumeBindingReader{}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		Logger:                     observability.NewJSONLogger(&logs, nil),
		PrincipalResolver:          namespaceBindingPrincipalResolver(),
		NamespaceBindingReader:     bindingReader,
		OperationIntakeStore:       store,
		GenerateOperationID:        func() string { return "op_shell_ns" },
		Now:                        fixedNamespaceNow,
		DeploymentNamespaceCallers: []auth.AllowedCaller{{CallerService: "agentsmith-api", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleNamespaceAdmin}}},
	})
	req := upsertNamespaceRequest("/internal/v1/namespaces/ns_123?token=query-secret", "ns_123", `{"namespace_id":"ns_123","body_secret":"x"}`)
	req.Header.Set(auth.HeaderAuthorization, "Bearer auth-secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("sanity status = %d body = %s, want validation 400 for unknown body field", rec.Code, rec.Body.String())
	}

	logs.Reset()
	req = upsertNamespaceRequest("/internal/v1/namespaces/ns_123?token=query-secret", "ns_123", `{"namespace_id":"ns_123"}`)
	req.Header.Set(auth.HeaderAuthorization, "Bearer auth-secret")
	rec = httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("intake calls = %d, want 1", store.calls)
	}
	if bindingReader.calls != 0 {
		t.Fatalf("binding reader calls = %d, want 0 for namespace bootstrap route", bindingReader.calls)
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_shell_ns" || env.Resource.Type != "namespace" {
		t.Fatalf("envelope = %#v, want namespace operation", env)
	}
	entry := decodeSingleStructuredLogEntry(t, logs.Bytes())
	if got, want := entry["event"], "afscp.request"; got != want {
		t.Fatalf("event = %#v, want %#v", got, want)
	}
	if got, want := entry["level"], slog.LevelInfo.String(); got != want {
		t.Fatalf("level = %#v, want %#v", got, want)
	}
	if got, want := entry["correlation_id"], "corr_namespace"; got != want {
		t.Fatalf("correlation_id = %#v, want %#v", got, want)
	}
	if got, want := entry["method"], http.MethodPut; got != want {
		t.Fatalf("method = %#v, want %#v", got, want)
	}
	if got, want := entry["path"], "/internal/v1/namespaces/ns_123"; got != want {
		t.Fatalf("path = %#v, want %#v", got, want)
	}
	if got, want := entry["route"], "/internal/v1/namespaces/{namespaceId}"; got != want {
		t.Fatalf("route = %#v, want %#v", got, want)
	}
	if got, want := entry["operation_id"], "upsertNamespace"; got != want {
		t.Fatalf("operation_id = %#v, want %#v", got, want)
	}
	if got, want := entry["status"], float64(http.StatusOK); got != want {
		t.Fatalf("status = %#v, want %#v", got, want)
	}
	rendered := logs.String()
	for _, leaked := range []string{"auth-secret", "query-secret", "body_secret"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("upsert log leaked %q: %s", leaked, rendered)
		}
	}
}
