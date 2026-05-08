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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestRepoLifecycleHandlerCreatesArchiveOperationIntake(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	meta := repoLifecycleMetaFixture(resources.RepoStatusActive)
	handler := repoLifecycleHandlerForTest(store, meta)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:archive", "ns_123", `{"reason":"raw secret reason"}`))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("intake calls = %d, want 1", store.calls)
	}
	spec := store.spec
	if spec.Scope.OperationType != operations.OperationRepoArchive || spec.Phase != operations.OperationPhaseRepoLifecycleValidate {
		t.Fatalf("spec type/phase = %s/%s", spec.Scope.OperationType, spec.Phase)
	}
	if spec.NamespaceID != "ns_123" || spec.RepoID != "repo_123" || spec.Resource.Type != "repo" || spec.Resource.ID != "repo_123" {
		t.Fatalf("spec ns/repo/resource = %q/%q/%#v", spec.NamespaceID, spec.RepoID, spec.Resource)
	}
	if spec.InputSummary["reason_present"] != true || strings.Contains(renderLifecycleArgs(t, spec.InputSummary), "raw secret reason") {
		t.Fatalf("input summary = %#v, want safe reason presence only", spec.InputSummary)
	}
	wantHash, err := operations.HashRequest(repoLifecycleCanonicalRequest{RepoID: "repo_123", Body: lifecycleRequestDTO{Reason: "raw secret reason"}})
	if err != nil {
		t.Fatalf("hash lifecycle canonical request: %v", err)
	}
	if spec.RequestHash != wantHash {
		t.Fatalf("request hash = %q, want full canonical DTO hash %q", spec.RequestHash, wantHash)
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_lifecycle" || env.OperationState != OperationStateQueued {
		t.Fatalf("envelope = %#v, want queued lifecycle operation", env)
	}
}

func TestRepoLifecycleHandlerCreatesRetainedLifecycleOperationIntake(t *testing.T) {
	tests := []struct {
		name               string
		path               string
		status             resources.RepoStatus
		body               string
		wantType           operations.OperationType
		wantReasonPresent  bool
		wantPolicySnapshot bool
	}{
		{name: "archive", path: "/internal/v1/repos/repo_123:archive", status: resources.RepoStatusActive, body: `{"reason":"archive reason"}`, wantType: operations.OperationRepoArchive, wantReasonPresent: true},
		{name: "restore archived", path: "/internal/v1/repos/repo_123:restore-archived", status: resources.RepoStatusArchived, body: `{}`, wantType: operations.OperationRepoRestoreArchived},
		{name: "delete", path: "/internal/v1/repos/repo_123:delete", status: resources.RepoStatusActive, body: `{}`, wantType: operations.OperationRepoDelete, wantPolicySnapshot: true},
		{name: "restore tombstoned", path: "/internal/v1/repos/repo_123:restore-tombstoned", status: resources.RepoStatusTombstoned, body: `{}`, wantType: operations.OperationRepoRestoreTombstoned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			meta := repoLifecycleMetaFixture(tt.status)
			if tt.status == resources.RepoStatusTombstoned {
				retention := fixedNamespaceNow().Add(time.Hour)
				meta.repo.Lifecycle.RetentionExpiresAt = &retention
				meta.repoReader.repos = []resources.Repo{meta.repo}
			}
			handler := repoLifecycleHandlerForTest(store, meta)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoLifecycleRequest(tt.path, "ns_123", tt.body))

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
			}
			if store.calls != 1 {
				t.Fatalf("intake calls = %d, want 1", store.calls)
			}
			spec := store.spec
			if spec.Scope.OperationType != tt.wantType || spec.Phase != operations.OperationPhaseRepoLifecycleValidate {
				t.Fatalf("spec type/phase = %s/%s, want %s/%s", spec.Scope.OperationType, spec.Phase, tt.wantType, operations.OperationPhaseRepoLifecycleValidate)
			}
			if spec.NamespaceID != "ns_123" || spec.RepoID != "repo_123" || spec.Resource.Type != "repo" || spec.Resource.ID != "repo_123" {
				t.Fatalf("spec ns/repo/resource = %q/%q/%#v", spec.NamespaceID, spec.RepoID, spec.Resource)
			}
			if spec.InputSummary["reason_present"] != tt.wantReasonPresent {
				t.Fatalf("reason_present = %#v, want %v in %#v", spec.InputSummary["reason_present"], tt.wantReasonPresent, spec.InputSummary)
			}
			snapshot, hasSnapshot := spec.InputSummary["lifecycle_policy_snapshot"].(map[string]any)
			if hasSnapshot != tt.wantPolicySnapshot {
				t.Fatalf("lifecycle policy snapshot present = %v, want %v in %#v", hasSnapshot, tt.wantPolicySnapshot, spec.InputSummary)
			}
			if tt.wantPolicySnapshot {
				if snapshot["tombstone_retention_seconds"] != float64(604800) || snapshot["retention_met"] != false || snapshot["retention_override_requested"] != false {
					t.Fatalf("lifecycle policy snapshot = %#v, want retained delete policy window", snapshot)
				}
			}
		})
	}
}

func renderLifecycleArgs(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	return string(encoded)
}

func TestRepoLifecycleHandlerCreatesDeleteAndPurgeOperations(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		status    resources.RepoStatus
		body      string
		wantType  operations.OperationType
		wantSafe  []string
		forbidden []string
	}{
		{name: "delete active", path: "/internal/v1/repos/repo_123:delete", status: resources.RepoStatusActive, body: `{}`, wantType: operations.OperationRepoDelete},
		{name: "purge tombstoned", path: "/internal/v1/repos/repo_123:purge", status: resources.RepoStatusTombstoned, body: `{"reason":"delete secret","product_confirmation_ref":"confirm-secret"}`, wantType: operations.OperationRepoPurge, wantSafe: []string{"reason_present", "product_confirmation_present"}, forbidden: []string{"delete secret", "confirm-secret"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			meta := repoLifecycleMetaFixture(tt.status)
			if tt.status == resources.RepoStatusTombstoned {
				retention := fixedNamespaceNow().Add(-time.Hour)
				meta.repo.Lifecycle.RetentionExpiresAt = &retention
				meta.repoReader.repos = []resources.Repo{meta.repo}
			}
			handler := repoLifecycleHandlerForTest(store, meta)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoLifecycleRequest(tt.path, "ns_123", tt.body))

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
			}
			if store.spec.Scope.OperationType != tt.wantType {
				t.Fatalf("operation type = %s, want %s", store.spec.Scope.OperationType, tt.wantType)
			}
			rendered := renderLifecycleArgs(t, store.spec.InputSummary)
			if tt.wantType == operations.OperationRepoDelete || tt.wantType == operations.OperationRepoPurge {
				if !strings.Contains(rendered, "lifecycle_policy_snapshot") {
					t.Fatalf("summary missing lifecycle policy snapshot: %s", rendered)
				}
			}
			for _, want := range tt.wantSafe {
				if !strings.Contains(rendered, want) {
					t.Fatalf("summary %s missing %s", rendered, want)
				}
			}
			for _, forbidden := range tt.forbidden {
				if strings.Contains(rendered, forbidden) {
					t.Fatalf("summary leaked %q: %s", forbidden, rendered)
				}
			}
			if tt.wantType == operations.OperationRepoPurge {
				wantHash, err := operations.HashRequest(repoLifecycleCanonicalRequest{RepoID: "repo_123", Body: purgeRepoRequestDTO{Reason: "delete secret", ProductConfirmationRef: "confirm-secret"}})
				if err != nil {
					t.Fatalf("hash purge canonical request: %v", err)
				}
				if store.spec.RequestHash != wantHash {
					t.Fatalf("request hash = %q, want full purge DTO hash %q", store.spec.RequestHash, wantHash)
				}
				changedHash, err := operations.HashRequest(repoLifecycleCanonicalRequest{RepoID: "repo_123", Body: purgeRepoRequestDTO{Reason: "other reason", ProductConfirmationRef: "other-confirm"}})
				if err != nil {
					t.Fatalf("hash changed purge canonical request: %v", err)
				}
				if changedHash == store.spec.RequestHash {
					t.Fatalf("changed raw purge fields produced same hash %q", changedHash)
				}
			}
		})
	}
}

func TestRepoLifecycleHandlerPurgeApprovalEvidenceFingerprints(t *testing.T) {
	first := purgeOperationSummaryForTest(t, `{"reason":"raw secret reason","product_confirmation_ref":"confirm-secret-a","retention_override_requested":true,"operator_approval_ref":"approval-secret-a"}`)
	firstAgain := purgeOperationSummaryForTest(t, `{"reason":"different reason","product_confirmation_ref":"confirm-secret-a","retention_override_requested":true,"operator_approval_ref":"approval-secret-a"}`)
	second := purgeOperationSummaryForTest(t, `{"reason":"raw secret reason","product_confirmation_ref":"confirm-secret-b","retention_override_requested":true,"operator_approval_ref":"approval-secret-b"}`)

	firstConfirmation := requireSummaryString(t, first, "product_confirmation_ref_fingerprint")
	firstApproval := requireSummaryString(t, first, "operator_approval_ref_fingerprint")
	firstAgainConfirmation := requireSummaryString(t, firstAgain, "product_confirmation_ref_fingerprint")
	firstAgainApproval := requireSummaryString(t, firstAgain, "operator_approval_ref_fingerprint")
	secondConfirmation := requireSummaryString(t, second, "product_confirmation_ref_fingerprint")
	secondApproval := requireSummaryString(t, second, "operator_approval_ref_fingerprint")
	for _, fingerprint := range []string{firstConfirmation, firstApproval, secondConfirmation, secondApproval} {
		if !strings.HasPrefix(fingerprint, "sha256:") || len(fingerprint) != len("sha256:")+64 {
			t.Fatalf("fingerprint %q, want sha256 digest", fingerprint)
		}
	}
	if firstConfirmation != firstAgainConfirmation || firstApproval != firstAgainApproval {
		t.Fatalf("fingerprints changed for the same refs: %q/%q vs %q/%q", firstConfirmation, firstApproval, firstAgainConfirmation, firstAgainApproval)
	}
	if firstConfirmation == secondConfirmation {
		t.Fatalf("confirmation fingerprints did not distinguish different refs: %q", firstConfirmation)
	}
	if firstApproval == secondApproval {
		t.Fatalf("approval fingerprints did not distinguish different refs: %q", firstApproval)
	}

	rendered := renderLifecycleArgs(t, first)
	for _, forbidden := range []string{"raw secret reason", "confirm-secret-a", "approval-secret-a"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("purge summary leaked %q: %s", forbidden, rendered)
		}
	}
}

func TestRepoLifecycleHandlerReusesExistingOperationBeforeMetadata(t *testing.T) {
	canonical := repoLifecycleCanonicalRequest{RepoID: "repo_123", Body: lifecycleRequestDTO{}}
	requestHash, err := operations.HashRequest(canonical)
	if err != nil {
		t.Fatalf("hash lifecycle request: %v", err)
	}
	store := &fakeOperationIntakeStore{lookupRecord: existingLifecycleOperationRecord("op_existing_lifecycle", operations.OperationRepoArchive, requestHash)}
	meta := repoLifecycleMetaFixture(resources.RepoStatusActive)
	meta.repoReader.getErr = errors.New("postgres outage after original intake")
	meta.fenceReader.err = errors.New("fence outage after original intake")
	handler := repoLifecycleHandlerForTest(store, meta)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:archive", "ns_123", `{}`))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_existing_lifecycle" {
		t.Fatalf("operation id = %q, want existing", env.OperationID)
	}
	if store.calls != 0 || meta.repoReader.getInNamespaceCalls != 0 || meta.fenceReader.calls != 0 {
		t.Fatalf("calls intake/repo/fence = %d/%d/%d, want idempotency reuse before metadata", store.calls, meta.repoReader.getInNamespaceCalls, meta.fenceReader.calls)
	}
}

func TestRepoLifecycleHandlerIdempotencyConflictBeforeMetadata(t *testing.T) {
	store := &fakeOperationIntakeStore{lookupRecord: existingLifecycleOperationRecord("op_existing_lifecycle", operations.OperationRepoArchive, operations.RequestHash("sha256:different"))}
	meta := repoLifecycleMetaFixture(resources.RepoStatusActive)
	meta.repoReader.getErr = errors.New("postgres outage should not be reached")
	handler := repoLifecycleHandlerForTest(store, meta)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:archive", "ns_123", `{}`))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeIdempotencyConflict {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeIdempotencyConflict)
	}
	if store.calls != 0 || meta.repoReader.getInNamespaceCalls != 0 {
		t.Fatalf("calls intake/repo = %d/%d, want conflict before metadata", store.calls, meta.repoReader.getInNamespaceCalls)
	}
}

func TestRepoLifecyclePurgeAdmissionDisabledRejectsNewBeforeMetadataAndAudits(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	meta := repoLifecycleMetaFixture(resources.RepoStatusTombstoned)
	sink := &fakeAuditSink{}
	handler := repoLifecycleHandlerForTestWithOptions(store, meta, namespaceBindingAllowedPolicy(auth.RoleRepoLifecycleAdmin), nil, sink, true)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:purge", "ns_123", `{"reason":"raw reason secret","product_confirmation_ref":"confirm-secret"}`))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeCapabilityDenied {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeCapabilityDenied)
	}
	if store.lookupCalls != 1 || store.calls != 0 || meta.repoReader.getInNamespaceCalls != 0 || meta.namespaceRead.calls != 0 || meta.bindingReader.calls != 0 || meta.fenceReader.calls != 0 {
		t.Fatalf("calls lookup/intake/repo/ns/binding/fence = %d/%d/%d/%d/%d/%d, want lookup then deny only", store.lookupCalls, store.calls, meta.repoReader.getInNamespaceCalls, meta.namespaceRead.calls, meta.bindingReader.calls, meta.fenceReader.calls)
	}
	if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("audit events = %#v, want one denied audit", sink.events)
	}
	assertRepoPurgeAdmissionDisabledAudit(t, sink.events[0])
	if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "confirm-secret") {
		t.Fatalf("response leaked purge input: %s", rec.Body.String())
	}
}

func TestRepoLifecyclePurgeAdmissionDisabledReturnsConflictBeforeCapabilityDenied(t *testing.T) {
	store := &fakeOperationIntakeStore{lookupRecord: existingLifecycleOperationRecord("op_existing_lifecycle", operations.OperationRepoPurge, operations.RequestHash("sha256:different"))}
	meta := repoLifecycleMetaFixture(resources.RepoStatusTombstoned)
	sink := &fakeAuditSink{}
	handler := repoLifecycleHandlerForTestWithOptions(store, meta, namespaceBindingAllowedPolicy(auth.RoleRepoLifecycleAdmin), nil, sink, true)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:purge", "ns_123", `{"reason":"raw reason secret","product_confirmation_ref":"confirm-secret"}`))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeIdempotencyConflict {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeIdempotencyConflict)
	}
	if store.calls != 0 || meta.repoReader.getInNamespaceCalls != 0 {
		t.Fatalf("intake/repo calls = %d/%d, want conflict before metadata", store.calls, meta.repoReader.getInNamespaceCalls)
	}
	if len(sink.events) != 0 {
		t.Fatalf("audit events = %#v, want no capability denied audit on idempotency conflict", sink.events)
	}
}

func TestRepoLifecyclePurgeAdmissionDisabledWithoutLookupStoreFailsClosedBeforeMetadata(t *testing.T) {
	store := &fakeNoLookupOperationIntakeStore{}
	meta := repoLifecycleMetaFixture(resources.RepoStatusTombstoned)
	sink := &fakeAuditSink{}
	handler := repoLifecycleHandlerForTestWithOptions(store, meta, namespaceBindingAllowedPolicy(auth.RoleRepoLifecycleAdmin), nil, sink, true)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:purge", "ns_123", `{"reason":"raw reason secret","product_confirmation_ref":"confirm-secret"}`))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeCapabilityDenied {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeCapabilityDenied)
	}
	if store.calls != 0 || meta.repoReader.getInNamespaceCalls != 0 || meta.namespaceRead.calls != 0 || meta.bindingReader.calls != 0 || meta.fenceReader.calls != 0 {
		t.Fatalf("calls intake/repo/ns/binding/fence = %d/%d/%d/%d/%d, want fail-closed before metadata", store.calls, meta.repoReader.getInNamespaceCalls, meta.namespaceRead.calls, meta.bindingReader.calls, meta.fenceReader.calls)
	}
	if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("audit events = %#v, want denied audit", sink.events)
	}
}

func TestRepoLifecyclePurgeAdmissionDisabledDoesNotGateOtherLifecycleMutations(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		status resources.RepoStatus
		body   string
	}{
		{name: "archive", path: "/internal/v1/repos/repo_123:archive", status: resources.RepoStatusActive, body: `{}`},
		{name: "delete", path: "/internal/v1/repos/repo_123:delete", status: resources.RepoStatusActive, body: `{}`},
		{name: "restore archived", path: "/internal/v1/repos/repo_123:restore-archived", status: resources.RepoStatusArchived, body: `{}`},
		{name: "restore tombstoned", path: "/internal/v1/repos/repo_123:restore-tombstoned", status: resources.RepoStatusTombstoned, body: `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			meta := repoLifecycleMetaFixture(tt.status)
			if tt.status == resources.RepoStatusTombstoned {
				retention := fixedNamespaceNow().Add(time.Hour)
				meta.repo.Lifecycle.RetentionExpiresAt = &retention
				meta.repoReader.repos = []resources.Repo{meta.repo}
			}
			handler := repoLifecycleHandlerForTestWithOptions(store, meta, namespaceBindingAllowedPolicy(auth.RoleRepoLifecycleAdmin), nil, nil, true)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoLifecycleRequest(tt.path, "ns_123", tt.body))

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
			}
			if store.calls != 1 {
				t.Fatalf("intake calls = %d, want other lifecycle operation to enter handler", store.calls)
			}
		})
	}
}

func TestRepoLifecycleHandlerCanonicalRequestIncludesPathRepoID(t *testing.T) {
	body := lifecycleRequestDTO{}
	repoHash, err := operations.HashRequest(repoLifecycleCanonicalRequest{RepoID: "repo_123", Body: body})
	if err != nil {
		t.Fatalf("hash repo_123 canonical request: %v", err)
	}
	otherHash, err := operations.HashRequest(repoLifecycleCanonicalRequest{RepoID: "repo_other", Body: body})
	if err != nil {
		t.Fatalf("hash repo_other canonical request: %v", err)
	}
	if repoHash == otherHash {
		t.Fatalf("same body with different repo id produced same hash %q", repoHash)
	}

	store := &fakeOperationIntakeStore{lookupRecord: existingLifecycleOperationRecord("op_existing_lifecycle", operations.OperationRepoArchive, repoHash)}
	meta := repoLifecycleMetaFixture(resources.RepoStatusActive)
	handler := repoLifecycleHandlerForTest(store, meta)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_other:archive", "ns_123", `{}`))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeIdempotencyConflict {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeIdempotencyConflict)
	}
	if store.calls != 0 || meta.repoReader.getInNamespaceCalls != 0 {
		t.Fatalf("calls intake/repo = %d/%d, want conflict before metadata", store.calls, meta.repoReader.getInNamespaceCalls)
	}
}

func TestRepoLifecycleHandlerValidationDeniesBeforeMetadata(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		ns       string
		body     string
		wantCode ErrorCode
	}{
		{name: "invalid repo id", path: "/internal/v1/repos/bad:archive", ns: "ns_123", body: `{}`, wantCode: CodeInvalidID},
		{name: "missing namespace", path: "/internal/v1/repos/repo_123:archive", body: `{}`, wantCode: CodeResourceNamespaceMismatch},
		{name: "unknown lifecycle field", path: "/internal/v1/repos/repo_123:archive", ns: "ns_123", body: `{"raw_path":"/srv/secret"}`, wantCode: CodeInvalidID},
		{name: "purge unknown field", path: "/internal/v1/repos/repo_123:purge", ns: "ns_123", body: `{"reason":"secret","product_confirmation_ref":"confirm","raw_path":"/srv"}`, wantCode: CodeInvalidID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			meta := repoLifecycleMetaFixture(resources.RepoStatusActive)
			sink := &fakeAuditSink{}
			handler := repoLifecycleHandlerForTestWithAudit(store, meta, sink)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoLifecycleRequest(tt.path, tt.ns, tt.body))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
			}
			if store.calls != 0 || meta.repoReader.getInNamespaceCalls != 0 {
				t.Fatalf("store/intake calls = %d/%d, want none", meta.repoReader.getInNamespaceCalls, store.calls)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode {
				t.Fatalf("error code = %s, want %s", env.Error.Code, tt.wantCode)
			}
			if len(sink.events) != 1 {
				t.Fatalf("audit events = %#v, want validation audit", sink.events)
			}
			if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "/srv") || strings.Contains(rec.Body.String(), "raw_path") {
				t.Fatalf("response leaked validation detail: %s", rec.Body.String())
			}
		})
	}
}

func TestRepoLifecycleHandlerMapsRepoAccessAdmissionFamilies(t *testing.T) {
	tests := []struct {
		name     string
		status   resources.RepoStatus
		fences   []fences.Fence
		wantCode ErrorCode
	}{
		{name: "archived delete invalid", status: resources.RepoStatusTombstoned, wantCode: CodeRepoLifecycleInvalidState},
		{name: "lifecycle fence held", status: resources.RepoStatusActive, fences: []fences.Fence{repoLifecycleFenceFixture(fences.KindLifecycle, fences.StatusActive)}, wantCode: CodeRepoLifecycleFenceHeld},
		{name: "writer fence held", status: resources.RepoStatusActive, fences: []fences.Fence{repoLifecycleFenceFixture(fences.KindWriterSession, fences.StatusActive)}, wantCode: CodeWriterSessionFenceHeld},
		{name: "expired fence recovery", status: resources.RepoStatusActive, fences: []fences.Fence{repoLifecycleFenceFixture(fences.KindLifecycle, fences.StatusExpired)}, wantCode: CodeOperationRecoveryRequired},
		{name: "namespace disabled", status: resources.RepoStatusActive, wantCode: CodeNamespaceDisabled},
		{name: "internal invalid stored state", status: resources.RepoStatusActive, wantCode: CodeInternalError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			meta := repoLifecycleMetaFixture(tt.status)
			meta.fenceReader.fences = tt.fences
			path := "/internal/v1/repos/repo_123:delete"
			if tt.wantCode == CodeRepoLifecycleInvalidState && tt.status == resources.RepoStatusTombstoned {
				path = "/internal/v1/repos/repo_123:delete"
			}
			if tt.wantCode == CodeNamespaceDisabled {
				meta.namespace.Status = resources.NamespaceStatusDisabled
				disabledAt := fixedNamespaceNow()
				meta.namespace.DisabledAt = &disabledAt
				meta.namespace.DisabledReason = "policy"
				meta.namespaceRead.namespace = meta.namespace
			}
			if tt.wantCode == CodeInternalError {
				meta.repo.JVSRepoID = "/srv/secret"
				meta.repoReader.repos = []resources.Repo{meta.repo}
			}
			handler := repoLifecycleHandlerForTest(store, meta)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoLifecycleRequest(path, "ns_123", `{}`))

			wantStatus := http.StatusConflict
			if tt.wantCode == CodeInternalError {
				wantStatus = http.StatusInternalServerError
			}
			if rec.Code != wantStatus {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), wantStatus)
			}
			if store.calls != 0 {
				t.Fatalf("intake calls = %d, want none", store.calls)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode {
				t.Fatalf("error code = %s, want %s", env.Error.Code, tt.wantCode)
			}
			if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "/srv") || strings.Contains(rec.Body.String(), "fence_") {
				t.Fatalf("admission response leaked detail: %s", rec.Body.String())
			}
		})
	}
}

func TestRepoLifecycleHandlerConvertsDurableFencesForRepoAccess(t *testing.T) {
	now := fixedNamespaceNow()
	storeFence := fences.Fence{
		ID:                "fence_123",
		RepoID:            "repo_123",
		Kind:              fences.KindWriterSession,
		HolderOperationID: "op_fence",
		Status:            fences.StatusExpired,
		ExpiresAt:         now.Add(time.Hour),
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	got := repoAccessFencesFromStore([]fences.Fence{storeFence})

	if len(got) != 1 || got[0].Kind.String() != fences.KindWriterSession.String() || string(got[0].Status) != fences.StatusExpired.String() {
		t.Fatalf("converted fences = %#v, want writer_session/expired", got)
	}
}

func TestRepoLifecycleHandlerAdmissionDenialAudits(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	meta := repoLifecycleMetaFixture(resources.RepoStatusActive)
	meta.fenceReader.fences = []fences.Fence{repoLifecycleFenceFixture(fences.KindLifecycle, fences.StatusActive)}
	sink := &fakeAuditSink{}
	handler := repoLifecycleHandlerForTestWithAudit(store, meta, sink)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:delete", "ns_123", `{}`))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("audit events = %#v, want denied audit", sink.events)
	}
	if strings.Contains(rec.Body.String(), "fence_") || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("denial leaked detail: %s", rec.Body.String())
	}
}

func TestRepoLifecycleHandlerMapsNotFoundAndStoreOutage(t *testing.T) {
	tests := []struct {
		name     string
		edit     func(repoLifecycleMeta)
		wantHTTP int
		wantCode ErrorCode
		retry    bool
		audit    bool
	}{
		{name: "repo not found", edit: func(meta repoLifecycleMeta) { meta.repoReader.getErr = sql.ErrNoRows }, wantHTTP: http.StatusNotFound, wantCode: CodeRepoNotFound, audit: true},
		{name: "repo namespace mismatch", edit: func(meta repoLifecycleMeta) {
			meta.repoReader.repoInNamespaceOverride = repoResourceFixture("ns_other", "repo_123", resources.RepoStatusActive)
		}, wantHTTP: http.StatusNotFound, wantCode: CodeRepoNotFound, audit: true},
		{name: "repo store outage", edit: func(meta repoLifecycleMeta) { meta.repoReader.getErr = errors.New("postgres password=secret failed") }, wantHTTP: http.StatusServiceUnavailable, wantCode: CodeStorageUnavailable, retry: true, audit: true},
		{name: "fence store outage", edit: func(meta repoLifecycleMeta) { meta.fenceReader.err = errors.New("postgres raw_path=/srv failed") }, wantHTTP: http.StatusServiceUnavailable, wantCode: CodeStorageUnavailable, retry: true, audit: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			meta := repoLifecycleMetaFixture(resources.RepoStatusActive)
			tt.edit(meta)
			sink := &fakeAuditSink{}
			handler := repoLifecycleHandlerForTestWithAudit(store, meta, sink)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:archive", "ns_123", `{}`))

			if rec.Code != tt.wantHTTP {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.wantHTTP)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode || env.Error.Retryable != tt.retry {
				t.Fatalf("error = %#v, want %s retry=%v", env.Error, tt.wantCode, tt.retry)
			}
			if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "/srv") || strings.Contains(rec.Body.String(), "postgres") {
				t.Fatalf("error leaked raw detail: %s", rec.Body.String())
			}
			if tt.audit {
				if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied {
					t.Fatalf("audit events = %#v, want denied audit", sink.events)
				}
				if sink.events[0].OperationID != "" || sink.events[0].Resource.NamespaceID != "ns_123" || sink.events[0].Details["error_code"] != string(tt.wantCode) {
					t.Fatalf("audit event = %#v, want internal denial audit without mutation operation", sink.events[0])
				}
				renderedAudit := auditEventString(t, sink.events[0])
				if strings.Contains(renderedAudit, "postgres") || strings.Contains(renderedAudit, "password=secret") || strings.Contains(renderedAudit, "/srv") {
					t.Fatalf("audit leaked raw storage detail: %s", renderedAudit)
				}
			}
			if store.calls != 0 {
				t.Fatalf("intake calls = %d, want none", store.calls)
			}
		})
	}
}

func TestRepoLifecycleHandlerIdempotencyLookupOutageAuditsDenied(t *testing.T) {
	store := &fakeOperationIntakeStore{lookupErr: errors.New("postgres password=secret failed")}
	meta := repoLifecycleMetaFixture(resources.RepoStatusActive)
	sink := &fakeAuditSink{}
	handler := repoLifecycleHandlerForTestWithAudit(store, meta, sink)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:archive", "ns_123", `{}`))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s, want 503", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeStorageUnavailable || !env.Error.Retryable {
		t.Fatalf("error = %#v, want STORAGE_UNAVAILABLE retryable", env.Error)
	}
	if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("audit events = %#v, want denied audit", sink.events)
	}
	if store.calls != 0 || meta.repoReader.getInNamespaceCalls != 0 {
		t.Fatalf("intake/repo calls = %d/%d, want none", store.calls, meta.repoReader.getInNamespaceCalls)
	}
	if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "postgres") {
		t.Fatalf("response leaked raw lookup error: %s", rec.Body.String())
	}
}

func TestRepoLifecycleHandlerPurgePolicy(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		edit     func(*repoLifecycleMeta)
		wantCode ErrorCode
		wantHTTP int
	}{
		{name: "missing reason", body: `{"product_confirmation_ref":"confirm"}`, edit: func(meta *repoLifecycleMeta) {
			retention := fixedNamespaceNow().Add(-time.Hour)
			meta.repo.Lifecycle.RetentionExpiresAt = &retention
			meta.repoReader.repos = []resources.Repo{meta.repo}
		}, wantCode: CodePurgeConfirmationRequired, wantHTTP: http.StatusConflict},
		{name: "missing confirmation", body: `{"reason":"secret"}`, edit: func(meta *repoLifecycleMeta) {
			retention := fixedNamespaceNow().Add(-time.Hour)
			meta.repo.Lifecycle.RetentionExpiresAt = &retention
			meta.repoReader.repos = []resources.Repo{meta.repo}
		}, wantCode: CodePurgeConfirmationRequired, wantHTTP: http.StatusConflict},
		{name: "retention not met", body: `{"reason":"secret","product_confirmation_ref":"confirm"}`, wantCode: CodePurgeRetentionNotMet, wantHTTP: http.StatusConflict},
		{name: "override without approval", body: `{"reason":"secret","product_confirmation_ref":"confirm","retention_override_requested":true}`, wantCode: CodePurgeRequiresOperatorApproval, wantHTTP: http.StatusConflict},
		{name: "override disabled", body: `{"reason":"secret","product_confirmation_ref":"confirm","retention_override_requested":true,"operator_approval_ref":"approval-secret"}`, edit: func(meta *repoLifecycleMeta) {
			meta.binding.LifecyclePolicy["break_glass_purge_enabled"] = false
		}, wantCode: CodePurgeRequiresOperatorApproval, wantHTTP: http.StatusConflict},
		{name: "override lacks break glass admin", body: `{"reason":"secret","product_confirmation_ref":"confirm","retention_override_requested":true,"operator_approval_ref":"approval-secret"}`, wantCode: CodePurgeRequiresOperatorApproval, wantHTTP: http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			meta := repoLifecycleMetaFixture(resources.RepoStatusTombstoned)
			retention := fixedNamespaceNow().Add(time.Hour)
			meta.repo.Lifecycle.RetentionExpiresAt = &retention
			meta.binding.LifecyclePolicy["break_glass_purge_enabled"] = true
			if tt.edit != nil {
				tt.edit(&meta)
			}
			sink := &fakeAuditSink{}
			handler := repoLifecycleHandlerForTestWithAudit(store, meta, sink)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:purge", "ns_123", tt.body))

			if rec.Code != tt.wantHTTP {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.wantHTTP)
			}
			if store.calls != 0 {
				t.Fatalf("intake calls = %d, want none", store.calls)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode {
				t.Fatalf("error code = %s, want %s", env.Error.Code, tt.wantCode)
			}
			if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "confirm-secret") || strings.Contains(rec.Body.String(), "approval-secret") {
				t.Fatalf("purge error leaked raw input: %s", rec.Body.String())
			}
			if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied {
				t.Fatalf("audit events = %#v, want denied audit", sink.events)
			}
		})
	}
}

func TestRepoLifecycleHandlerPurgeOverrideSkipsBreakGlassPolicyWhenLocalEvidenceMissing(t *testing.T) {
	tests := []struct {
		name string
		edit func(*repoLifecycleMeta)
		body string
	}{
		{name: "policy disabled", body: `{"reason":"secret","product_confirmation_ref":"confirm","retention_override_requested":true,"operator_approval_ref":"approval-secret"}`, edit: func(meta *repoLifecycleMeta) {
			meta.binding.LifecyclePolicy["break_glass_purge_enabled"] = false
		}},
		{name: "missing approval", body: `{"reason":"secret","product_confirmation_ref":"confirm","retention_override_requested":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			meta := repoLifecycleMetaFixture(resources.RepoStatusTombstoned)
			retention := fixedNamespaceNow().Add(time.Hour)
			meta.repo.Lifecycle.RetentionExpiresAt = &retention
			meta.binding.LifecyclePolicy["break_glass_purge_enabled"] = true
			if tt.edit != nil {
				tt.edit(&meta)
			}
			breakGlass := &recordingAllowedCallerPolicy{callers: repoLifecycleBreakGlassAllowedCallers()}
			handler := repoLifecycleHandlerForTestWithPolicies(store, meta, namespaceBindingAllowedPolicy(auth.RoleRepoLifecycleAdmin), breakGlass, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:purge", "ns_123", tt.body))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != CodePurgeRequiresOperatorApproval {
				t.Fatalf("error code = %s, want %s", env.Error.Code, CodePurgeRequiresOperatorApproval)
			}
			if breakGlass.calls != 0 {
				t.Fatalf("break-glass policy calls = %d, want 0", breakGlass.calls)
			}
			if store.calls != 0 {
				t.Fatalf("intake calls = %d, want none", store.calls)
			}
		})
	}
}

func TestRepoLifecycleHandlerPurgeOverrideRequiresBreakGlassAdmin(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	meta := repoLifecycleMetaFixture(resources.RepoStatusTombstoned)
	retention := fixedNamespaceNow().Add(time.Hour)
	meta.repo.Lifecycle.RetentionExpiresAt = &retention
	meta.repoReader.repos = []resources.Repo{meta.repo}
	meta.binding.LifecyclePolicy["break_glass_purge_enabled"] = true
	handler := repoLifecycleHandlerForTestWithPolicy(store, meta, repoLifecycleBreakGlassAllowedPolicy(), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:purge", "ns_123", `{"reason":"raw secret reason","product_confirmation_ref":"confirm-secret","retention_override_requested":true,"operator_approval_ref":"approval-secret"}`))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	rendered := renderLifecycleArgs(t, store.spec.InputSummary)
	for _, want := range []string{"break_glass_authorized", "operator_approval_present", "retention_override_requested"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("summary missing %s: %s", want, rendered)
		}
	}
	for _, forbidden := range []string{"raw secret reason", "confirm-secret", "approval-secret"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("summary leaked %q: %s", forbidden, rendered)
		}
	}
}

func TestRepoLifecycleHandlerRestoreTombstonedRetentionWindow(t *testing.T) {
	tests := []struct {
		name     string
		expires  time.Time
		wantHTTP int
		wantCode ErrorCode
	}{
		{name: "retention future allows intake", expires: fixedNamespaceNow().Add(time.Hour), wantHTTP: http.StatusAccepted},
		{name: "retention expired rejects", expires: fixedNamespaceNow().Add(-time.Hour), wantHTTP: http.StatusConflict, wantCode: CodeRepoLifecycleInvalidState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			meta := repoLifecycleMetaFixture(resources.RepoStatusTombstoned)
			meta.repo.Lifecycle.RetentionExpiresAt = &tt.expires
			meta.repoReader.repos = []resources.Repo{meta.repo}
			handler := repoLifecycleHandlerForTest(store, meta)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:restore-tombstoned", "ns_123", `{}`))

			if rec.Code != tt.wantHTTP {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.wantHTTP)
			}
			if tt.wantHTTP != http.StatusAccepted {
				env := decodeErrorEnvelope(t, rec.Body.Bytes())
				if env.Error.Code != tt.wantCode {
					t.Fatalf("error code = %s, want %s", env.Error.Code, tt.wantCode)
				}
			}
		})
	}
}

type repoLifecycleMeta struct {
	repo          resources.Repo
	namespace     resources.Namespace
	binding       resources.NamespaceVolumeBinding
	repoReader    *fakeRepoReader
	namespaceRead *fakeNamespaceReader
	bindingReader *fakeNamespaceVolumeBindingReader
	fenceReader   *fakeRepoFenceReader
}

func repoLifecycleMetaFixture(status resources.RepoStatus) repoLifecycleMeta {
	repo := repoResourceFixture("ns_123", "repo_123", status)
	repo.Lifecycle = lifecycleFixture(status)
	namespace := resources.Namespace{ID: "ns_123", Status: resources.NamespaceStatusActive, CreatedAt: fixedNamespaceNow(), UpdatedAt: fixedNamespaceNow()}
	binding := namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleRepoLifecycleAdmin}})
	meta := repoLifecycleMeta{repo: repo, namespace: namespace, binding: binding}
	meta.repoReader = &fakeRepoReader{repos: []resources.Repo{repo}}
	meta.namespaceRead = &fakeNamespaceReader{namespace: namespace}
	meta.bindingReader = &fakeNamespaceVolumeBindingReader{binding: binding}
	meta.fenceReader = &fakeRepoFenceReader{}
	return meta
}

func lifecycleFixture(status resources.RepoStatus) resources.RepoLifecycle {
	lifecycle := resources.RepoLifecycle{Status: status, LastLifecycleOperationID: "op_repo_create"}
	now := fixedNamespaceNow()
	switch status {
	case resources.RepoStatusTombstoned:
		retention := now.Add(time.Hour)
		lifecycle.RetentionExpiresAt = &retention
		lifecycle.PreDeleteStatus = resources.RepoStatusActive
	case resources.RepoStatusPurged:
		lifecycle.PreDeleteStatus = resources.RepoStatusArchived
	case resources.RepoStatusDeleting, resources.RepoStatusRestoringTombstoned, resources.RepoStatusPurging:
		lifecycle.PreDeleteStatus = resources.RepoStatusActive
		if status != resources.RepoStatusDeleting {
			retention := now.Add(time.Hour)
			lifecycle.RetentionExpiresAt = &retention
		}
	}
	return lifecycle
}

func repoLifecycleFenceFixture(kind fences.Kind, status fences.Status) fences.Fence {
	now := fixedNamespaceNow()
	return fences.Fence{ID: "fence_123", RepoID: "repo_123", Kind: kind, HolderOperationID: "op_fence", Status: status, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
}

func repoLifecycleHandlerForTest(store OperationIntakeStore, meta repoLifecycleMeta) http.Handler {
	return repoLifecycleHandlerForTestWithAudit(store, meta, nil)
}

func repoLifecycleHandlerForTestWithAudit(store OperationIntakeStore, meta repoLifecycleMeta, sink *fakeAuditSink) http.Handler {
	return repoLifecycleHandlerForTestWithPolicy(store, meta, namespaceBindingAllowedPolicy(auth.RoleRepoLifecycleAdmin), sink)
}

func repoLifecycleHandlerForTestWithPolicy(store OperationIntakeStore, meta repoLifecycleMeta, policy AllowedCallerPolicy, sink *fakeAuditSink) http.Handler {
	return repoLifecycleHandlerForTestWithPolicies(store, meta, policy, policy, sink)
}

func repoLifecycleHandlerForTestWithPolicies(store OperationIntakeStore, meta repoLifecycleMeta, policy AllowedCallerPolicy, breakGlass AllowedCallerPolicy, sink *fakeAuditSink) http.Handler {
	return repoLifecycleHandlerForTestWithOptions(store, meta, policy, breakGlass, sink, false)
}

func repoLifecycleHandlerForTestWithOptions(store OperationIntakeStore, meta repoLifecycleMeta, policy AllowedCallerPolicy, breakGlass AllowedCallerPolicy, sink *fakeAuditSink, purgeAdmissionDisabled bool) http.Handler {
	var auditSink audit.Sink
	if sink != nil {
		auditSink = sink
	}
	return RepoLifecycleHandler(RepoLifecycleHandlerConfig{
		RepoReader:             meta.repoReader,
		NamespaceReader:        meta.namespaceRead,
		BindingReader:          meta.bindingReader,
		FenceReader:            meta.fenceReader,
		IntakeStore:            store,
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		AllowedCallers:         policy,
		BreakGlassCallers:      breakGlass,
		OperationID:            func() string { return "op_lifecycle" },
		Now:                    fixedNamespaceNow,
		PurgeAdmissionDisabled: purgeAdmissionDisabled,
		AuditSink:              auditSink,
	})
}

func purgeOperationSummaryForTest(t *testing.T, body string) map[string]any {
	t.Helper()
	store := &fakeOperationIntakeStore{}
	meta := repoLifecycleMetaFixture(resources.RepoStatusTombstoned)
	retention := fixedNamespaceNow().Add(time.Hour)
	meta.repo.Lifecycle.RetentionExpiresAt = &retention
	meta.repoReader.repos = []resources.Repo{meta.repo}
	meta.binding.LifecyclePolicy["break_glass_purge_enabled"] = true
	handler := repoLifecycleHandlerForTestWithPolicy(store, meta, repoLifecycleBreakGlassAllowedPolicy(), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:purge", "ns_123", body))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	return store.spec.InputSummary
}

func requireSummaryString(t *testing.T, summary map[string]any, key string) string {
	t.Helper()
	value, ok := summary[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		t.Fatalf("summary[%s] = %#v, want non-empty string in %#v", key, summary[key], summary)
	}
	return value
}

func repoLifecycleBreakGlassAllowedCallers() []auth.AllowedCaller {
	return []auth.AllowedCaller{{
		CallerService: "product-caller",
		Kind:          auth.CallerKindOperator,
		Roles:         []auth.Role{auth.RoleRepoLifecycleAdmin, auth.RoleBreakGlassAdmin},
	}}
}

func repoLifecycleBreakGlassAllowedPolicy() AllowedCallerPolicy {
	return fakeAllowedCallerPolicy{callers: repoLifecycleBreakGlassAllowedCallers()}
}

func repoLifecycleRequest(path, namespaceID, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_lifecycle")
	req.Header.Set(auth.HeaderCallerService, "product-caller")
	req.Header.Set(auth.HeaderIdempotencyKey, "idem_lifecycle")
	req.Header.Set(auth.HeaderActorType, "user")
	req.Header.Set(auth.HeaderActorID, "user_123")
	if namespaceID != "" {
		req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	}
	return req
}

type fakeNamespaceReader struct {
	namespace resources.Namespace
	err       error
	calls     int
}

func (reader *fakeNamespaceReader) GetNamespace(context.Context, string) (resources.Namespace, error) {
	reader.calls++
	if reader.err != nil {
		return resources.Namespace{}, reader.err
	}
	return reader.namespace, nil
}

type fakeRepoFenceReader struct {
	fences []fences.Fence
	err    error
	calls  int
}

func (reader *fakeRepoFenceReader) ListHeldRepoFences(context.Context, string) ([]fences.Fence, error) {
	reader.calls++
	if reader.err != nil {
		return nil, reader.err
	}
	return append([]fences.Fence(nil), reader.fences...), nil
}

func existingLifecycleOperationRecord(operationID string, operationType operations.OperationType, requestHash operations.RequestHash) *operations.OperationRecord {
	now := fixedNamespaceNow()
	return &operations.OperationRecord{
		ID:               operationID,
		Type:             operationType,
		State:            operations.OperationStateQueued,
		Phase:            operations.OperationPhaseRepoLifecycleValidate,
		IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_123", operationType, "idem_lifecycle").String(),
		IdempotencyKey:   "idem_lifecycle",
		RequestHash:      requestHash,
		CorrelationID:    "corr_lifecycle",
		CallerService:    "product-caller",
		AuthorizedActor:  operations.Actor{Type: "user", ID: "user_123"},
		Resource:         operations.ResourceRef{Type: "repo", ID: "repo_123"},
		NamespaceID:      "ns_123",
		RepoID:           "repo_123",
		InputSummary:     map[string]any{},
		CreatedAt:        now,
	}
}

func assertRepoPurgeAdmissionDisabledAudit(t *testing.T, event audit.Event) {
	t.Helper()
	if event.Type != audit.EventTypeCapabilityDenied {
		t.Fatalf("audit event Type = %q, want %q", event.Type, audit.EventTypeCapabilityDenied)
	}
	if got := event.Details["route_operation_id"]; got != "purgeRepo" {
		t.Fatalf("audit route_operation_id = %#v, want purgeRepo; details=%#v", got, event.Details)
	}
	if !auditValidationErrorsContain(event.Details["validation_errors"], "repo_purge_admission_disabled") {
		t.Fatalf("audit validation_errors = %#v, want repo_purge_admission_disabled; details=%#v", event.Details["validation_errors"], event.Details)
	}
}
