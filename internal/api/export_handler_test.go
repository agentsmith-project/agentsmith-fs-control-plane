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

func TestCreateExportNotReadyReturnsRetryableConflictWithoutAccessSecret(t *testing.T) {
	store := &fakeExportStore{err: exportaccess.ErrExportNotReady}
	handler := exportHandlerForTest(store, exportMetaFixture(), namespaceBindingAllowedPolicy(auth.RoleExportAdmin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only","ttl_seconds":120}`, "ns_123"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeExportNotReady || !env.Error.Retryable {
		t.Fatalf("error = %#v, want retryable EXPORT_NOT_READY", env.Error)
	}
	if store.createCalls != 1 {
		t.Fatalf("create calls = %d, want one durable admission attempt", store.createCalls)
	}
	rendered := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"access", "password", "export-password-once", "credential_hash", "credential_salt", "verifier"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("not-ready response leaked %q: %s", forbidden, rec.Body.String())
		}
	}
}

func TestCreateExportIdempotentReplayReturnsRedactedSessionWithoutPassword(t *testing.T) {
	store := &fakeExportStore{reused: true}
	handler := exportHandlerForTest(store, exportMetaFixture(), namespaceBindingAllowedPolicy(auth.RoleExportAdmin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only"}`, "ns_123"))

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

func TestCreateExportAdmissionDisabledReplaysExistingOperationBeforeMetadata(t *testing.T) {
	hash, err := operations.HashRequest(exportCanonicalRequest{RepoID: "repo_123", Mode: string(sessionstate.AccessModeReadOnly), TTLSeconds: 120})
	if err != nil {
		t.Fatalf("hash export request: %v", err)
	}
	meta := exportMetaFixture()
	sink := &fakeAuditSink{}
	passwordCalls := 0
	store := &fakeExportStore{lookupRecord: existingExportOperationRecord("op_existing_export", hash, map[string]any{"ttl_seconds": 120}), session: func() exportaccess.Session {
		session := exportSessionFixture(sessionstate.ExportStatusActive)
		session.Mode = sessionstate.AccessModeReadOnly
		return session
	}()}
	fenceReader := &fakeRepoFenceReader{fences: meta.fences}
	handler := ExportHandler(ExportHandlerConfig{
		RepoReader:        meta.repoReader,
		NamespaceReader:   meta.namespaceReader,
		BindingReader:     meta.bindingReader,
		VolumeReader:      meta.volumeReader,
		FenceReader:       fenceReader,
		Store:             store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "product-caller", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleExportAdmin}}}},
		OperationID:       func() string { return "op_export" },
		ExportID:          func() string { return "export_new" },
		Password: func() string {
			passwordCalls++
			return "export-password-once"
		},
		Now:               fixedNamespaceNow,
		PublicBaseURL:     "https://files.example.com",
		AdmissionDisabled: true,
		AuditSink:         sink,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only"}`, "ns_123"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202 existing operation", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_existing_export" || env.OperationState != OperationStateSucceeded {
		t.Fatalf("operation = %q/%q, want existing succeeded operation", env.OperationID, env.OperationState)
	}
	exportBody, ok := env.Result["export"].(map[string]any)
	if !ok || exportBody["export_id"] != "export_123" {
		t.Fatalf("result.export = %#v, want redacted export session", env.Result["export"])
	}
	if _, ok := env.Result["access"]; ok {
		t.Fatalf("replay returned access secret: %#v", env.Result)
	}
	rendered := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"password", "export-password-once", "credential_hash", "credential_salt", "verifier"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("replay response leaked %q: %s", forbidden, rec.Body.String())
		}
	}
	if store.lookupCalls != 1 || store.createCalls != 0 || store.getCalls != 1 || passwordCalls != 0 {
		t.Fatalf("lookup/create/get/password calls = %d/%d/%d/%d, want 1/0/1/0", store.lookupCalls, store.createCalls, store.getCalls, passwordCalls)
	}
	if meta.repoReader.getInNamespaceCalls != 0 || meta.namespaceReader.calls != 0 || meta.bindingReader.calls != 0 || meta.volumeReader.calls != 0 || fenceReader.calls != 0 {
		t.Fatalf("metadata calls repo/ns/binding/volume/fence = %d/%d/%d/%d/%d, want none", meta.repoReader.getInNamespaceCalls, meta.namespaceReader.calls, meta.bindingReader.calls, meta.volumeReader.calls, fenceReader.calls)
	}
	if len(sink.events) != 0 {
		t.Fatalf("audit events = %#v, want no denial audit for replay", sink.events)
	}
}

func TestCreateExportAdmissionDisabledRejectsNewBeforeMetadataAndAudits(t *testing.T) {
	meta := exportMetaFixture()
	sink := &fakeAuditSink{}
	store := &fakeExportStore{}
	fenceReader := &fakeRepoFenceReader{fences: meta.fences}
	handler := ExportHandler(ExportHandlerConfig{
		RepoReader:        meta.repoReader,
		NamespaceReader:   meta.namespaceReader,
		BindingReader:     meta.bindingReader,
		VolumeReader:      meta.volumeReader,
		FenceReader:       fenceReader,
		Store:             store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "product-caller", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleExportAdmin}}}},
		OperationID:       func() string { return "op_export" },
		ExportID:          func() string { return "export_new" },
		Password:          func() string { t.Fatal("password generator must not be called for disabled new create"); return "" },
		Now:               fixedNamespaceNow,
		AdmissionDisabled: true,
		AuditSink:         sink,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only"}`, "ns_123"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", rec.Code, rec.Body.String())
	}
	if env := decodeErrorEnvelope(t, rec.Body.Bytes()); env.Error.Code != CodeCapabilityDenied {
		t.Fatalf("error = %#v, want capability denied", env.Error)
	}
	if store.lookupCalls != 1 || store.createCalls != 0 || store.getCalls != 0 {
		t.Fatalf("lookup/create/get calls = %d/%d/%d, want 1/0/0", store.lookupCalls, store.createCalls, store.getCalls)
	}
	if meta.repoReader.getInNamespaceCalls != 0 || meta.namespaceReader.calls != 0 || meta.bindingReader.calls != 0 || meta.volumeReader.calls != 0 || fenceReader.calls != 0 {
		t.Fatalf("metadata calls repo/ns/binding/volume/fence = %d/%d/%d/%d/%d, want none", meta.repoReader.getInNamespaceCalls, meta.namespaceReader.calls, meta.bindingReader.calls, meta.volumeReader.calls, fenceReader.calls)
	}
	if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied || sink.events[0].Reason != "webdav export create admission is disabled" {
		t.Fatalf("audit events = %#v, want one denied admission audit", sink.events)
	}
	assertWebDAVExportAdmissionDisabledAudit(t, sink.events[0])
}

func TestRestoreReconciliationModeDeniesExportCreateBeforePassword(t *testing.T) {
	meta := exportMetaFixture()
	sink := &fakeAuditSink{}
	passwordCalls := 0
	store := &fakeExportStore{restoreReconciliationBlocked: true}
	fenceReader := &fakeRepoFenceReader{fences: meta.fences}
	handler := ExportHandler(ExportHandlerConfig{
		RepoReader:        meta.repoReader,
		NamespaceReader:   meta.namespaceReader,
		BindingReader:     meta.bindingReader,
		VolumeReader:      meta.volumeReader,
		FenceReader:       fenceReader,
		Store:             store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "product-caller", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleExportAdmin}}}},
		OperationID:       func() string { return "op_export" },
		ExportID:          func() string { return "export_new" },
		Password: func() string {
			passwordCalls++
			return "export-password-once"
		},
		Now:       fixedNamespaceNow,
		AuditSink: sink,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only"}`, "ns_123"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	if env := decodeErrorEnvelope(t, rec.Body.Bytes()); env.Error.Code != CodeRestoreReconciliationActive || !env.Error.Retryable {
		t.Fatalf("error = %#v, want restore reconciliation active retryable", env.Error)
	}
	if store.lookupCalls != 1 || store.createCalls != 0 || store.restoreReconciliationCalls != 1 || passwordCalls != 0 {
		t.Fatalf("lookup/create/gate/password = %d/%d/%d/%d, want 1/0/1/0", store.lookupCalls, store.createCalls, store.restoreReconciliationCalls, passwordCalls)
	}
	if meta.repoReader.getInNamespaceCalls != 0 || meta.namespaceReader.calls != 0 || meta.bindingReader.calls != 0 || meta.volumeReader.calls != 0 || fenceReader.calls != 0 {
		t.Fatalf("metadata calls repo/ns/binding/volume/fence = %d/%d/%d/%d/%d, want none", meta.repoReader.getInNamespaceCalls, meta.namespaceReader.calls, meta.bindingReader.calls, meta.volumeReader.calls, fenceReader.calls)
	}
	if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied || sink.events[0].Reason != "restore reconciliation is active" {
		t.Fatalf("audit events = %#v, want restore reconciliation denial audit", sink.events)
	}
}

func TestRestoreReconciliationModeExportReplayDoesNotReturnAccess(t *testing.T) {
	hash, err := operations.HashRequest(exportCanonicalRequest{RepoID: "repo_123", Mode: string(sessionstate.AccessModeReadOnly), TTLSeconds: 120})
	if err != nil {
		t.Fatalf("hash export request: %v", err)
	}
	session := exportSessionFixture(sessionstate.ExportStatusActive)
	session.Mode = sessionstate.AccessModeReadOnly
	store := &fakeExportStore{restoreReconciliationBlocked: true, lookupRecord: existingExportOperationRecord("op_existing_export", hash, map[string]any{"ttl_seconds": 120}), session: session}
	handler := exportHandlerForTest(store, exportMetaFixture(), namespaceBindingAllowedPolicy(auth.RoleExportAdmin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only"}`, "ns_123"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want existing replay", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if _, ok := env.Result["access"]; ok {
		t.Fatalf("replay during reconciliation returned access credential: %#v", env.Result)
	}
	if store.restoreReconciliationCalls != 0 || store.createCalls != 0 {
		t.Fatalf("gate/create calls = %d/%d, want replay before reconciliation denial and no new credential", store.restoreReconciliationCalls, store.createCalls)
	}
}

func TestCreateExportAdmissionDisabledReportsHashConflictBeforeCapabilityDenied(t *testing.T) {
	meta := exportMetaFixture()
	store := &fakeExportStore{lookupRecord: existingExportOperationRecord("op_existing_export", operations.RequestHash("sha256:different"), map[string]any{"ttl_seconds": 120})}
	fenceReader := &fakeRepoFenceReader{fences: meta.fences}
	handler := ExportHandler(ExportHandlerConfig{
		RepoReader:        meta.repoReader,
		NamespaceReader:   meta.namespaceReader,
		BindingReader:     meta.bindingReader,
		VolumeReader:      meta.volumeReader,
		FenceReader:       fenceReader,
		Store:             store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "product-caller", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleExportAdmin}}}},
		OperationID:       func() string { return "op_export" },
		ExportID:          func() string { return "export_new" },
		Password:          func() string { t.Fatal("password generator must not be called for conflict"); return "" },
		Now:               fixedNamespaceNow,
		AdmissionDisabled: true,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only","ttl_seconds":120}`, "ns_123"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	if env := decodeErrorEnvelope(t, rec.Body.Bytes()); env.Error.Code != CodeIdempotencyConflict {
		t.Fatalf("error = %#v, want idempotency conflict", env.Error)
	}
	if store.lookupCalls != 1 || store.createCalls != 0 || store.getCalls != 0 {
		t.Fatalf("lookup/create/get calls = %d/%d/%d, want 1/0/0", store.lookupCalls, store.createCalls, store.getCalls)
	}
	if meta.repoReader.getInNamespaceCalls != 0 || meta.namespaceReader.calls != 0 || meta.bindingReader.calls != 0 || meta.volumeReader.calls != 0 || fenceReader.calls != 0 {
		t.Fatalf("metadata calls repo/ns/binding/volume/fence = %d/%d/%d/%d/%d, want none", meta.repoReader.getInNamespaceCalls, meta.namespaceReader.calls, meta.bindingReader.calls, meta.volumeReader.calls, fenceReader.calls)
	}
}

func TestCreateExportAdmissionDisabledRejectsMismatchedReplaySessionWithoutLeaking(t *testing.T) {
	hash, err := operations.HashRequest(exportCanonicalRequest{RepoID: "repo_123", Mode: string(sessionstate.AccessModeReadOnly), TTLSeconds: 120})
	if err != nil {
		t.Fatalf("hash export request: %v", err)
	}
	tests := []struct {
		name string
		edit func(*exportaccess.Session)
		leak string
	}{
		{name: "namespace mismatch", edit: func(session *exportaccess.Session) { session.NamespaceID = "ns_other" }, leak: "ns_other"},
		{name: "repo mismatch", edit: func(session *exportaccess.Session) { session.RepoID = "repo_other" }, leak: "repo_other"},
		{name: "mode mismatch", edit: func(session *exportaccess.Session) { session.Mode = sessionstate.AccessModeReadWrite }, leak: string(sessionstate.AccessModeReadWrite)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := exportMetaFixture()
			session := exportSessionFixture(sessionstate.ExportStatusActive)
			session.Mode = sessionstate.AccessModeReadOnly
			tt.edit(&session)
			store := &fakeExportStore{lookupRecord: existingExportOperationRecord("op_existing_export", hash, map[string]any{"ttl_seconds": 120}), session: session}
			fenceReader := &fakeRepoFenceReader{fences: meta.fences}
			handler := ExportHandler(ExportHandlerConfig{
				RepoReader:        meta.repoReader,
				NamespaceReader:   meta.namespaceReader,
				BindingReader:     meta.bindingReader,
				VolumeReader:      meta.volumeReader,
				FenceReader:       fenceReader,
				Store:             store,
				PrincipalResolver: namespaceBindingPrincipalResolver(),
				AllowedCallers:    fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "product-caller", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleExportAdmin}}}},
				OperationID:       func() string { return "op_export" },
				Password: func() string {
					t.Fatal("password generator must not be called for mismatched replay session")
					return ""
				},
				Now:               fixedNamespaceNow,
				AdmissionDisabled: true,
			})
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only"}`, "ns_123"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
			}
			if env := decodeErrorEnvelope(t, rec.Body.Bytes()); env.Error.Code != CodeIdempotencyConflict {
				t.Fatalf("error = %#v, want idempotency conflict", env.Error)
			}
			if strings.Contains(rec.Body.String(), tt.leak) || strings.Contains(rec.Body.String(), `"export":`) {
				t.Fatalf("mismatched session leaked in response: %s", rec.Body.String())
			}
			if store.lookupCalls != 1 || store.getCalls != 1 || store.createCalls != 0 {
				t.Fatalf("lookup/get/create calls = %d/%d/%d, want 1/1/0", store.lookupCalls, store.getCalls, store.createCalls)
			}
			if meta.repoReader.getInNamespaceCalls != 0 || meta.namespaceReader.calls != 0 || meta.bindingReader.calls != 0 || meta.volumeReader.calls != 0 || fenceReader.calls != 0 {
				t.Fatalf("metadata calls repo/ns/binding/volume/fence = %d/%d/%d/%d/%d, want none", meta.repoReader.getInNamespaceCalls, meta.namespaceReader.calls, meta.bindingReader.calls, meta.volumeReader.calls, fenceReader.calls)
			}
		})
	}
}

func TestCreateExportAdmissionDisabledRejectsMismatchedReplayRecordBeforeSessionFetch(t *testing.T) {
	hash, err := operations.HashRequest(exportCanonicalRequest{RepoID: "repo_123", Mode: string(sessionstate.AccessModeReadOnly), TTLSeconds: 120})
	if err != nil {
		t.Fatalf("hash export request: %v", err)
	}
	record := existingExportOperationRecord("op_existing_export", hash, map[string]any{"ttl_seconds": 120})
	record.Resource.ID = "export_other"
	meta := exportMetaFixture()
	store := &fakeExportStore{lookupRecord: record}
	fenceReader := &fakeRepoFenceReader{fences: meta.fences}
	handler := ExportHandler(ExportHandlerConfig{
		RepoReader:        meta.repoReader,
		NamespaceReader:   meta.namespaceReader,
		BindingReader:     meta.bindingReader,
		VolumeReader:      meta.volumeReader,
		FenceReader:       fenceReader,
		Store:             store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "product-caller", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleExportAdmin}}}},
		OperationID:       func() string { return "op_export" },
		Password: func() string {
			t.Fatal("password generator must not be called for mismatched replay record")
			return ""
		},
		Now:               fixedNamespaceNow,
		AdmissionDisabled: true,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only"}`, "ns_123"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	if env := decodeErrorEnvelope(t, rec.Body.Bytes()); env.Error.Code != CodeIdempotencyConflict {
		t.Fatalf("error = %#v, want idempotency conflict", env.Error)
	}
	if store.lookupCalls != 1 || store.getCalls != 0 || store.createCalls != 0 {
		t.Fatalf("lookup/get/create calls = %d/%d/%d, want 1/0/0", store.lookupCalls, store.getCalls, store.createCalls)
	}
	if strings.Contains(rec.Body.String(), "export_other") || strings.Contains(rec.Body.String(), `"export":`) {
		t.Fatalf("mismatched record leaked in response: %s", rec.Body.String())
	}
}

func TestCreateExportAdmissionDisabledExplicitZeroTTLRejectedBeforeReplayLookup(t *testing.T) {
	meta := exportMetaFixture()
	store := &fakeExportStore{}
	fenceReader := &fakeRepoFenceReader{fences: meta.fences}
	handler := ExportHandler(ExportHandlerConfig{
		RepoReader:        meta.repoReader,
		NamespaceReader:   meta.namespaceReader,
		BindingReader:     meta.bindingReader,
		VolumeReader:      meta.volumeReader,
		FenceReader:       fenceReader,
		Store:             store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "product-caller", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleExportAdmin}}}},
		OperationID:       func() string { return "op_export" },
		Password:          func() string { t.Fatal("password generator must not be called for invalid ttl"); return "" },
		Now:               fixedNamespaceNow,
		AdmissionDisabled: true,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only","ttl_seconds":0}`, "ns_123"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	if env := decodeErrorEnvelope(t, rec.Body.Bytes()); env.Error.Code != CodeInvalidID {
		t.Fatalf("error = %#v, want invalid id", env.Error)
	}
	if store.lookupCalls != 0 || store.getCalls != 0 || store.createCalls != 0 {
		t.Fatalf("lookup/get/create calls = %d/%d/%d, want 0/0/0", store.lookupCalls, store.getCalls, store.createCalls)
	}
	if meta.repoReader.getInNamespaceCalls != 0 || meta.namespaceReader.calls != 0 || meta.bindingReader.calls != 0 || meta.volumeReader.calls != 0 || fenceReader.calls != 0 {
		t.Fatalf("metadata calls repo/ns/binding/volume/fence = %d/%d/%d/%d/%d, want none", meta.repoReader.getInNamespaceCalls, meta.namespaceReader.calls, meta.bindingReader.calls, meta.volumeReader.calls, fenceReader.calls)
	}
}

func TestCreateExportAdmissionDisabledFailsClosedWhenExistingTTLIsUnavailable(t *testing.T) {
	hash, err := operations.HashRequest(exportCanonicalRequest{RepoID: "repo_123", Mode: string(sessionstate.AccessModeReadOnly), TTLSeconds: 120})
	if err != nil {
		t.Fatalf("hash export request: %v", err)
	}
	record := existingExportOperationRecord("op_existing_export", hash, map[string]any{"ttl_seconds": 120})
	delete(record.InputSummary, "ttl_seconds")
	meta := exportMetaFixture()
	store := &fakeExportStore{lookupRecord: record}
	fenceReader := &fakeRepoFenceReader{fences: meta.fences}
	handler := ExportHandler(ExportHandlerConfig{
		RepoReader:        meta.repoReader,
		NamespaceReader:   meta.namespaceReader,
		BindingReader:     meta.bindingReader,
		VolumeReader:      meta.volumeReader,
		FenceReader:       fenceReader,
		Store:             store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "product-caller", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleExportAdmin}}}},
		OperationID:       func() string { return "op_export" },
		ExportID:          func() string { return "export_new" },
		Password: func() string {
			t.Fatal("password generator must not be called when replay ttl is unavailable")
			return ""
		},
		Now:               fixedNamespaceNow,
		AdmissionDisabled: true,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only"}`, "ns_123"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want fail-closed 409", rec.Code, rec.Body.String())
	}
	if env := decodeErrorEnvelope(t, rec.Body.Bytes()); env.Error.Code != CodeIdempotencyConflict {
		t.Fatalf("error = %#v, want idempotency conflict", env.Error)
	}
	if store.lookupCalls != 1 || store.createCalls != 0 || store.getCalls != 0 {
		t.Fatalf("lookup/create/get calls = %d/%d/%d, want 1/0/0", store.lookupCalls, store.createCalls, store.getCalls)
	}
	if meta.repoReader.getInNamespaceCalls != 0 || meta.namespaceReader.calls != 0 || meta.bindingReader.calls != 0 || meta.volumeReader.calls != 0 || fenceReader.calls != 0 {
		t.Fatalf("metadata calls repo/ns/binding/volume/fence = %d/%d/%d/%d/%d, want none", meta.repoReader.getInNamespaceCalls, meta.namespaceReader.calls, meta.bindingReader.calls, meta.volumeReader.calls, fenceReader.calls)
	}
}

func TestCreateExportAdmissionDisabledOmittedTTLRequiresExistingResolvedTTL(t *testing.T) {
	hash, err := operations.HashRequest(exportCanonicalRequest{RepoID: "repo_123", Mode: string(sessionstate.AccessModeReadOnly), TTLSeconds: 120})
	if err != nil {
		t.Fatalf("hash export request: %v", err)
	}
	record := existingExportOperationRecord("op_existing_export", hash, map[string]any{"ttl_seconds": 120})
	delete(record.InputSummary, "ttl_seconds")
	meta := exportMetaFixture()
	store := &fakeExportStore{lookupRecord: record}
	fenceReader := &fakeRepoFenceReader{fences: meta.fences}
	handler := ExportHandler(ExportHandlerConfig{
		RepoReader:        meta.repoReader,
		NamespaceReader:   meta.namespaceReader,
		BindingReader:     meta.bindingReader,
		VolumeReader:      meta.volumeReader,
		FenceReader:       fenceReader,
		Store:             store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "product-caller", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleExportAdmin}}}},
		OperationID:       func() string { return "op_export" },
		Password: func() string {
			t.Fatal("password generator must not be called when explicit zero ttl cannot resolve")
			return ""
		},
		Now:               fixedNamespaceNow,
		AdmissionDisabled: true,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only"}`, "ns_123"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want fail-closed 409", rec.Code, rec.Body.String())
	}
	if env := decodeErrorEnvelope(t, rec.Body.Bytes()); env.Error.Code != CodeIdempotencyConflict {
		t.Fatalf("error = %#v, want idempotency conflict", env.Error)
	}
	if store.lookupCalls != 1 || store.getCalls != 0 || store.createCalls != 0 {
		t.Fatalf("lookup/get/create calls = %d/%d/%d, want 1/0/0", store.lookupCalls, store.getCalls, store.createCalls)
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
	calls  int
}

func (reader *fakeExportVolumeReader) GetVolume(context.Context, string) (resources.Volume, error) {
	reader.calls++
	if reader.err != nil {
		return resources.Volume{}, reader.err
	}
	return reader.volume, nil
}

type fakeExportStore struct {
	createCalls                  int
	revokeCalls                  int
	getCalls                     int
	create                       exportaccess.CreateRequest
	revoke                       exportaccess.RevokeRequest
	reused                       bool
	err                          error
	session                      exportaccess.Session
	lookupCalls                  int
	lookupErr                    error
	lookupRecord                 *operations.OperationRecord
	lookupScope                  operations.IdempotencyScope
	restoreReconciliationBlocked bool
	restoreReconciliationCalls   int
}

type fakeNoLookupExportStore struct {
	createCalls int
	revokeCalls int
	getCalls    int
	create      exportaccess.CreateRequest
	revoke      exportaccess.RevokeRequest
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

func (store *fakeExportStore) GetOperationByIdempotencyScope(_ context.Context, scope operations.IdempotencyScope) (operations.OperationRecord, error) {
	store.lookupCalls++
	store.lookupScope = scope
	if store.lookupErr != nil {
		return operations.OperationRecord{}, store.lookupErr
	}
	if store.lookupRecord != nil {
		return store.lookupRecord.Sanitized(), nil
	}
	return operations.OperationRecord{}, sql.ErrNoRows
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

func (store *fakeExportStore) RestoreReconciliationWriteBlocked(_ context.Context, namespaceID, repoID string) (bool, error) {
	store.restoreReconciliationCalls++
	if strings.TrimSpace(namespaceID) == "" || strings.TrimSpace(repoID) == "" {
		return false, errors.New("unexpected restore reconciliation target")
	}
	return store.restoreReconciliationBlocked, nil
}

func existingExportOperationRecord(operationID string, requestHash operations.RequestHash, inputSummary map[string]any) *operations.OperationRecord {
	now := fixedNamespaceNow()
	summary := map[string]any{"export_id": "export_123", "namespace_id": "ns_123", "repo_id": "repo_123", "protocol": string(exportaccess.ProtocolWebDAV), "mode": string(sessionstate.AccessModeReadOnly), "ttl_seconds": 120, "expires_at": now.Add(120 * time.Second).Format(time.RFC3339)}
	for key, value := range inputSummary {
		summary[key] = value
	}
	return &operations.OperationRecord{
		ID:                  operationID,
		Type:                operations.OperationExportCreate,
		State:               operations.OperationStateSucceeded,
		Phase:               operations.OperationPhaseExportCreateCommitted,
		Attempt:             1,
		IdempotencyScope:    operations.NewIdempotencyScope("product-caller", "ns_123", operations.OperationExportCreate, "idem_export").String(),
		IdempotencyKey:      "idem_export",
		RequestHash:         requestHash,
		CorrelationID:       "corr_export",
		CallerService:       "product-caller",
		AuthorizedActor:     operations.Actor{Type: "user", ID: "user_123"},
		Resource:            operations.ResourceRef{Type: "export", ID: "export_123"},
		NamespaceID:         "ns_123",
		RepoID:              "repo_123",
		ExportID:            "export_123",
		ExternalResourceIDs: map[string]string{},
		InputSummary:        summary,
		CreatedAt:           now,
		StartedAt:           &now,
		FinishedAt:          &now,
	}
}

func assertWebDAVExportAdmissionDisabledAudit(t *testing.T, event audit.Event) {
	t.Helper()
	if event.Type != audit.EventTypeCapabilityDenied {
		t.Fatalf("audit event Type = %q, want %q", event.Type, audit.EventTypeCapabilityDenied)
	}
	if got := event.Details["route_operation_id"]; got != "createExport" {
		t.Fatalf("audit route_operation_id = %#v, want createExport; details=%#v", got, event.Details)
	}
	if !auditValidationErrorsContain(event.Details["validation_errors"], "webdav_export_admission_disabled") {
		t.Fatalf("audit validation_errors = %#v, want webdav_export_admission_disabled; details=%#v", event.Details["validation_errors"], event.Details)
	}
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

func (store *fakeNoLookupExportStore) CreateOrReuseExport(_ context.Context, request exportaccess.CreateRequest) (exportaccess.CreateResult, error) {
	store.createCalls++
	store.create = request
	if store.err != nil {
		return exportaccess.CreateResult{}, store.err
	}
	return exportaccess.CreateResult{Operation: request.Operation, Session: request.Session}, nil
}

func (store *fakeNoLookupExportStore) GetExportSession(_ context.Context, exportID string) (exportaccess.Session, error) {
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

func (store *fakeNoLookupExportStore) RevokeExport(_ context.Context, request exportaccess.RevokeRequest) (exportaccess.RevokeResult, error) {
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
