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

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

func TestCreateExportReturnsOneTimePasswordAndPersistsOnlyVerifier(t *testing.T) {
	store := &fakeExportStore{}
	handler := exportHandlerForTest(store, exportMetaFixture(), namespaceBindingAllowedPolicy(auth.RoleExportAdmin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_write","ttl_seconds":120}`, "ns_123"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationState != OperationStateSucceeded || env.OperationID != "op_export" || env.Resource.Type != "export" || env.Resource.ID != "export_123" {
		t.Fatalf("operation envelope = %#v", env)
	}
	access, ok := env.Result["access"].(map[string]any)
	if !ok {
		t.Fatalf("result.access = %#v, want object", env.Result["access"])
	}
	authBody, ok := access["auth"].(map[string]any)
	if !ok || authBody["username"] != "export_123" || authBody["password"] != "export-password-once" {
		t.Fatalf("access auth = %#v, want one-time password", access["auth"])
	}
	if store.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", store.createCalls)
	}
	if store.create.Verifier.Verify("export-password-once") == false {
		t.Fatalf("stored verifier did not verify generated password: %#v", store.create.Verifier)
	}
	renderedStoreInput := strings.ToLower(mustMarshalString(t, store.create))
	for _, forbidden := range []string{"export-password-once", "metadata_url", "secret_ref", "raw_path"} {
		if strings.Contains(renderedStoreInput, forbidden) {
			t.Fatalf("store request leaked %q: %s", forbidden, renderedStoreInput)
		}
	}
	renderedResponse := rec.Body.String()
	if strings.Contains(renderedResponse, "credential_hash") || strings.Contains(renderedResponse, "credential_salt") || strings.Contains(renderedResponse, "payload_volume_subdir") {
		t.Fatalf("create response leaked verifier/storage internals: %s", renderedResponse)
	}
}

func TestCreateExportIdempotentReplayReturnsRedactedSessionWithoutPassword(t *testing.T) {
	store := &fakeExportStore{reused: true}
	handler := exportHandlerForTest(store, exportMetaFixture(), namespaceBindingAllowedPolicy(auth.RoleExportAdmin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only","ttl_seconds":120}`, "ns_123"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if _, ok := env.Result["access"]; ok {
		t.Fatalf("idempotent replay returned access secret: %#v", env.Result)
	}
	body := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"password", "export-password-once", "credential_hash", "credential_salt"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("replay response leaked %q: %s", forbidden, rec.Body.String())
		}
	}
}

func TestCreateExportDefaultsTTLAndClampsDefaultToPolicyMax(t *testing.T) {
	tests := []struct {
		name    string
		maxTTL  any
		wantTTL int
	}{
		{name: "default ttl", maxTTL: float64(7200), wantTTL: exportaccess.DefaultTTLSeconds},
		{name: "policy max below default", maxTTL: float64(120), wantTTL: 120},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := exportMetaFixture()
			meta.binding.ExportPolicy["max_session_seconds"] = tt.maxTTL
			store := &fakeExportStore{}
			handler := exportHandlerForTest(store, meta, namespaceBindingAllowedPolicy(auth.RoleExportAdmin))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only"}`, "ns_123"))

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
			}
			wantExpires := fixedNamespaceNow().Add(time.Duration(tt.wantTTL) * time.Second)
			if !store.create.Session.ExpiresAt.Equal(wantExpires) {
				t.Fatalf("expires_at = %s, want %s", store.create.Session.ExpiresAt, wantExpires)
			}
			if got := store.create.Operation.InputSummary["ttl_seconds"]; got != tt.wantTTL {
				t.Fatalf("ttl summary = %#v, want %d", got, tt.wantTTL)
			}
		})
	}
}

func TestGetExportReturnsRedactedSessionOnly(t *testing.T) {
	store := &fakeExportStore{}
	handler := exportHandlerForTest(store, exportMetaFixture(), namespaceBindingAllowedPolicy(auth.RoleExportAdmin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodGet, "/internal/v1/exports/export_123", "", "ns_123"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	var body exportaccess.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v: %s", err, rec.Body.String())
	}
	if body.ID != "export_123" {
		t.Fatalf("get body = %#v, want direct export session", body)
	}
	if strings.Contains(rec.Body.String(), `"export":`) || strings.Contains(rec.Body.String(), `"access":`) {
		t.Fatalf("get response must be direct redacted session, got %s", rec.Body.String())
	}
	rendered := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"password", "credential", "secret", "raw_path", "payload_volume_subdir"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("get response leaked %q: %s", forbidden, rec.Body.String())
		}
	}
}

func TestGetExportRejectsNamespaceMismatch(t *testing.T) {
	store := &fakeExportStore{session: func() exportaccess.Session {
		session := exportSessionFixture(sessionstate.ExportStatusActive)
		session.NamespaceID = "ns_other"
		return session
	}()}
	handler := exportHandlerForTest(store, exportMetaFixture(), namespaceBindingAllowedPolicy(auth.RoleExportAdmin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodGet, "/internal/v1/exports/export_123", "", "ns_123"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeResourceNamespaceMismatch {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeResourceNamespaceMismatch)
	}
}

func TestRevokeExportIsIdempotentAndLeavesSessionRevoking(t *testing.T) {
	store := &fakeExportStore{}
	handler := exportHandlerForTest(store, exportMetaFixture(), namespaceBindingAllowedPolicy(auth.RoleExportAdmin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodDelete, "/internal/v1/exports/export_123", "", "ns_123"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	exportBody := env.Result["export"].(map[string]any)
	if exportBody["status"] != string(sessionstate.ExportStatusRevoking) {
		t.Fatalf("export status = %#v, want revoking", exportBody["status"])
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, exportRequest(http.MethodDelete, "/internal/v1/exports/export_123", "", "ns_123"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("second revoke status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	if store.revokeCalls != 2 {
		t.Fatalf("revoke calls = %d, want idempotent durable call per request", store.revokeCalls)
	}
}

func TestRevokeExportRemainsAvailableForRevokingSessionAfterNamespaceDisable(t *testing.T) {
	now := fixedNamespaceNow()
	disabledAt := now
	meta := exportMetaFixture()
	meta.namespace = resources.Namespace{ID: "ns_123", Status: resources.NamespaceStatusDisabled, DisabledReason: "security hold", DisabledAt: &disabledAt, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
	meta.namespaceReader = &fakeNamespaceReader{namespace: meta.namespace}
	store := &fakeExportStore{session: exportSessionFixture(sessionstate.ExportStatusRevoking)}
	handler := exportHandlerForTest(store, meta, namespaceBindingAllowedPolicy(auth.RoleExportAdmin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodDelete, "/internal/v1/exports/export_123", "", "ns_123"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want revoke preserved", rec.Code, rec.Body.String())
	}
	if store.revokeCalls != 1 || store.revoke.NamespaceID != "ns_123" || store.revoke.ExportID != "export_123" {
		t.Fatalf("revoke request = %#v, want namespace-scoped close path", store.revoke)
	}
}

func TestCreateExportAdmissionFailures(t *testing.T) {
	tests := []struct {
		name string
		meta exportMeta
		body string
		ns   string
		code ErrorCode
	}{
		{name: "namespace mismatch", meta: exportMetaFixture(), body: `{"mode":"read_only","ttl_seconds":120}`, ns: "ns_other", code: CodeRepoNotFound},
		{name: "namespace disabled", meta: func() exportMeta {
			now := fixedNamespaceNow()
			disabledAt := now
			meta := exportMetaFixture()
			meta.namespace = resources.Namespace{ID: "ns_123", Status: resources.NamespaceStatusDisabled, DisabledReason: "security hold", DisabledAt: &disabledAt, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
			meta.namespaceReader.namespace = meta.namespace
			return meta
		}(), body: `{"mode":"read_only","ttl_seconds":120}`, ns: "ns_123", code: CodeNamespaceDisabled},
		{name: "repo not found", meta: func() exportMeta {
			meta := exportMetaFixture()
			meta.repoReader = &fakeRepoReader{}
			return meta
		}(), body: `{"mode":"read_only","ttl_seconds":120}`, ns: "ns_123", code: CodeRepoNotFound},
		{name: "export policy disabled", meta: func() exportMeta {
			meta := exportMetaFixture()
			meta.binding.ExportPolicy["webdav_enabled"] = false
			return meta
		}(), body: `{"mode":"read_only","ttl_seconds":120}`, ns: "ns_123", code: CodeRepoLifecycleInvalidState},
		{name: "volume capability disabled", meta: func() exportMeta {
			meta := exportMetaFixture()
			meta.volume.Capabilities["webdav_export"] = false
			return meta
		}(), body: `{"mode":"read_only","ttl_seconds":120}`, ns: "ns_123", code: CodeRepoLifecycleInvalidState},
		{name: "volume disabled", meta: func() exportMeta {
			meta := exportMetaFixture()
			meta.volume.Status = resources.VolumeStatusDisabled
			meta.volumeReader.volume.Status = resources.VolumeStatusDisabled
			return meta
		}(), body: `{"mode":"read_only","ttl_seconds":120}`, ns: "ns_123", code: CodeRepoLifecycleInvalidState},
		{name: "writer fence blocks read-write", meta: func() exportMeta {
			meta := exportMetaFixture()
			meta.fences = []fences.Fence{repoLifecycleFenceFixture(fences.KindWriterSession, fences.StatusActive)}
			return meta
		}(), body: `{"mode":"read_write","ttl_seconds":120}`, ns: "ns_123", code: CodeWriterSessionFenceHeld},
		{name: "lifecycle fence blocks read-only", meta: func() exportMeta {
			meta := exportMetaFixture()
			meta.fences = []fences.Fence{repoLifecycleFenceFixture(fences.KindLifecycle, fences.StatusActive)}
			return meta
		}(), body: `{"mode":"read_only","ttl_seconds":120}`, ns: "ns_123", code: CodeRepoLifecycleFenceHeld},
		{name: "ttl below minimum", meta: exportMetaFixture(), body: `{"mode":"read_only","ttl_seconds":59}`, ns: "ns_123", code: CodeInvalidID},
		{name: "ttl above max", meta: exportMetaFixture(), body: `{"mode":"read_only","ttl_seconds":3601}`, ns: "ns_123", code: CodeInvalidID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeExportStore{}
			handler := exportHandlerForTest(store, tt.meta, namespaceBindingAllowedPolicy(auth.RoleExportAdmin))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", tt.body, tt.ns))

			if rec.Code < 400 {
				t.Fatalf("status = %d body = %s, want error", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.code {
				t.Fatalf("error code = %s body = %s, want %s", env.Error.Code, rec.Body.String(), tt.code)
			}
			if store.createCalls != 0 {
				t.Fatalf("create calls = %d, want rejected before durable create", store.createCalls)
			}
		})
	}
}

func TestExportHandlerAuthAndStoreErrors(t *testing.T) {
	t.Run("missing auth rejected before store", func(t *testing.T) {
		store := &fakeExportStore{}
		handler := exportHandlerForTest(store, exportMetaFixture(), namespaceBindingAllowedPolicy(auth.RoleExportAdmin))
		req := exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only","ttl_seconds":120}`, "ns_123")
		req.Header.Del(auth.HeaderAuthorization)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d body = %s, want 401", rec.Code, rec.Body.String())
		}
		env := decodeErrorEnvelope(t, rec.Body.Bytes())
		if env.Error.Code != CodeAuthenticationFailed {
			t.Fatalf("error code = %s, want %s", env.Error.Code, CodeAuthenticationFailed)
		}
		if store.createCalls != 0 {
			t.Fatalf("create calls = %d, want auth failure before store", store.createCalls)
		}
	})

	t.Run("role denied before store", func(t *testing.T) {
		store := &fakeExportStore{}
		handler := exportHandlerForTest(store, exportMetaFixture(), namespaceBindingAllowedPolicy(auth.RoleRepoAdmin))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only","ttl_seconds":120}`, "ns_123"))

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d body = %s, want 403", rec.Code, rec.Body.String())
		}
		env := decodeErrorEnvelope(t, rec.Body.Bytes())
		if env.Error.Code != CodeRoleNotAllowed {
			t.Fatalf("error code = %s, want %s", env.Error.Code, CodeRoleNotAllowed)
		}
		if store.createCalls != 0 {
			t.Fatalf("create calls = %d, want role failure before store", store.createCalls)
		}
	})

	t.Run("create idempotency conflict", func(t *testing.T) {
		store := &fakeExportStore{err: operations.ErrIdempotencyConflict}
		handler := exportHandlerForTest(store, exportMetaFixture(), namespaceBindingAllowedPolicy(auth.RoleExportAdmin))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only","ttl_seconds":120}`, "ns_123"))

		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
		}
		env := decodeErrorEnvelope(t, rec.Body.Bytes())
		if env.Error.Code != CodeIdempotencyConflict {
			t.Fatalf("error code = %s, want %s", env.Error.Code, CodeIdempotencyConflict)
		}
	})

	for _, tt := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "create outage", method: http.MethodPost, path: "/internal/v1/repos/repo_123/exports", body: `{"mode":"read_only","ttl_seconds":120}`},
		{name: "get outage", method: http.MethodGet, path: "/internal/v1/exports/export_123"},
		{name: "revoke outage", method: http.MethodDelete, path: "/internal/v1/exports/export_123"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeExportStore{err: errors.New("postgres password=export-secret metadata_url=raw failed")}
			handler := exportHandlerForTest(store, exportMetaFixture(), namespaceBindingAllowedPolicy(auth.RoleExportAdmin))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, exportRequest(tt.method, tt.path, tt.body, "ns_123"))

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d body = %s, want 503", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != CodeStorageUnavailable || !env.Error.Retryable {
				t.Fatalf("error = %#v, want retryable storage unavailable", env.Error)
			}
			for _, leaked := range []string{"export-secret", "metadata_url", "raw"} {
				if strings.Contains(strings.ToLower(rec.Body.String()), leaked) {
					t.Fatalf("store error leaked %q: %s", leaked, rec.Body.String())
				}
			}
		})
	}
}

func exportHandlerForTest(store *fakeExportStore, meta exportMeta, policy AllowedCallerPolicy) http.Handler {
	return ExportHandler(ExportHandlerConfig{
		RepoReader:        meta.repoReader,
		NamespaceReader:   meta.namespaceReader,
		BindingReader:     meta.bindingReader,
		VolumeReader:      meta.volumeReader,
		FenceReader:       &fakeRepoFenceReader{fences: meta.fences},
		Store:             store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    policy,
		OperationID:       func() string { return "op_export" },
		ExportID:          func() string { return "export_123" },
		Password:          func() string { return "export-password-once" },
		Now:               fixedNamespaceNow,
		PublicBaseURL:     "https://files.example.com",
	})
}

type exportMeta struct {
	repoReader      *fakeRepoReader
	namespaceReader *fakeNamespaceReader
	bindingReader   *fakeNamespaceVolumeBindingReader
	volumeReader    *fakeExportVolumeReader
	fenceReader     *fakeRepoFenceReader
	repo            resources.Repo
	namespace       resources.Namespace
	binding         resources.NamespaceVolumeBinding
	volume          resources.Volume
	fences          []fences.Fence
}

func exportMetaFixture() exportMeta {
	now := fixedNamespaceNow()
	repo := repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive)
	namespace := activeNamespaceFixture("ns_123")
	binding := namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleExportAdmin}})
	volume := resources.Volume{
		ID:             "vol_123",
		Backend:        resources.VolumeBackendJuiceFS,
		IsolationClass: resources.VolumeIsolationShared,
		Status:         resources.VolumeStatusActive,
		Capabilities:   map[string]any{"webdav_export": true, "workload_mount": true, "jvs_external_control_root": true, "directory_quota": false},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	meta := exportMeta{repo: repo, namespace: namespace, binding: binding, volume: volume}
	meta.repoReader = &fakeRepoReader{repos: []resources.Repo{repo}}
	meta.namespaceReader = &fakeNamespaceReader{namespace: namespace}
	meta.bindingReader = &fakeNamespaceVolumeBindingReader{binding: binding}
	meta.volumeReader = &fakeExportVolumeReader{volume: volume}
	meta.fenceReader = &fakeRepoFenceReader{}
	return meta
}

type fakeExportVolumeReader struct {
	volume resources.Volume
	err    error
}

func (reader *fakeExportVolumeReader) GetVolume(context.Context, string) (resources.Volume, error) {
	if reader.err != nil {
		return resources.Volume{}, reader.err
	}
	return reader.volume, nil
}

type fakeExportStore struct {
	createCalls int
	revokeCalls int
	getCalls    int
	create      exportaccess.CreateRequest
	revoke      exportaccess.RevokeRequest
	reused      bool
	err         error
	session     exportaccess.Session
}

func (store *fakeExportStore) CreateOrReuseExport(_ context.Context, request exportaccess.CreateRequest) (exportaccess.CreateResult, error) {
	store.createCalls++
	store.create = request
	if store.err != nil {
		return exportaccess.CreateResult{}, store.err
	}
	result := exportaccess.CreateResult{Operation: request.Operation, Session: request.Session, Reused: store.reused}
	if store.reused {
		result.Session = exportSessionFixture(sessionstate.ExportStatusActive)
		result.Operation.ID = "op_existing_export"
	}
	return result, nil
}

func (store *fakeExportStore) GetExportSession(_ context.Context, exportID string) (exportaccess.Session, error) {
	store.getCalls++
	if store.err != nil {
		return exportaccess.Session{}, store.err
	}
	if exportID != "export_123" {
		return exportaccess.Session{}, sql.ErrNoRows
	}
	if store.session.ID != "" {
		return store.session, nil
	}
	return exportSessionFixture(sessionstate.ExportStatusActive), nil
}

func (store *fakeExportStore) RevokeExport(_ context.Context, request exportaccess.RevokeRequest) (exportaccess.RevokeResult, error) {
	store.revokeCalls++
	store.revoke = request
	if store.err != nil {
		return exportaccess.RevokeResult{}, store.err
	}
	session := exportSessionFixture(sessionstate.ExportStatusRevoking)
	return exportaccess.RevokeResult{Operation: request.Operation, Session: session, Reused: store.revokeCalls > 1}, nil
}

func exportSessionFixture(status sessionstate.ExportStatus) exportaccess.Session {
	now := fixedNamespaceNow()
	var revokedAt *time.Time
	if status == sessionstate.ExportStatusRevoking || status == sessionstate.ExportStatusRevoked {
		revokedAt = &now
	}
	return exportaccess.Session{
		ID:                     "export_123",
		NamespaceID:            "ns_123",
		RepoID:                 "repo_123",
		Protocol:               exportaccess.ProtocolWebDAV,
		Mode:                   sessionstate.AccessModeReadWrite,
		Status:                 status,
		ExpiresAt:              now.Add(120 * time.Second),
		CreatedByCallerService: "product-caller",
		CreatedByActor:         exportaccess.Actor{Type: "user", ID: "user_123"},
		RevokedAt:              revokedAt,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
}

func exportRequest(method, path, body, namespaceID string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(auth.HeaderAuthorization, "Bearer token")
	req.Header.Set(auth.HeaderCallerService, "product-caller")
	req.Header.Set(auth.HeaderIdempotencyKey, "idem_export")
	req.Header.Set(HeaderCorrelationID, "corr_export")
	req.Header.Set(auth.HeaderActorType, "user")
	req.Header.Set(auth.HeaderActorID, "user_123")
	if namespaceID != "" {
		req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	}
	return req
}

func mustMarshalString(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(data)
}
