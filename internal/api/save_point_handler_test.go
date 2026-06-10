package api

import (
	"context"
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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

func TestSavePointCreateValidatesMessageAndCreatesQueuedOperation(t *testing.T) {
	now := fixedNamespaceNow()
	intake := &fakeOperationIntakeStore{}
	handler := savePointTestHandler(intake, nil, resources.RepoStatusActive, nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, savePointRequest(http.MethodPost, "/internal/v1/repos/repo_123/save-points", `{"message":"  checkpoint  "}`, "ns_123"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	if intake.calls != 1 || intake.spec.Phase != operations.OperationPhaseSavePointCreateValidate || intake.spec.RepoID != "repo_123" {
		t.Fatalf("intake calls/spec = %d/%#v", intake.calls, intake.spec)
	}
	if got := intake.spec.InputSummary["message"]; got != "checkpoint" {
		t.Fatalf("message summary = %#v, want trimmed checkpoint", got)
	}
	_ = now
	assertSavePointResponseDoesNotLeak(t, rec.Body.String())
}

func TestSavePointCreateRequiresExecutionCapabilityBeforeIntake(t *testing.T) {
	intake := &fakeOperationIntakeStore{}
	handler := SavePointHandler(SavePointHandlerConfig{
		RepoReader:         &fakeRepoReader{repos: []resources.Repo{repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive)}},
		NamespaceReader:    &fakeNamespaceReader{namespace: activeNamespaceFixture("ns_123")},
		BindingReader:      &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin}})},
		FenceReader:        &fakeRepoFenceReader{},
		MutationGate:       &fakeRepoJVSMutationGateReader{},
		SessionStateReader: &fakeSavePointSessionStateReader{},
		IntakeStore:        intake,
		PrincipalResolver:  namespaceBindingPrincipalResolver(),
		AllowedCallers:     namespaceBindingAllowedPolicy(auth.RoleRepoAdmin),
		OperationID:        func() string { return "op_savepoint" },
		Now:                func() time.Time { return fixedNamespaceNow() },
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, savePointRequest(http.MethodPost, "/internal/v1/repos/repo_123/save-points", `{"message":"checkpoint"}`, "ns_123"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeFileLibraryOperationPending || !env.Error.Retryable {
		t.Fatalf("error = %#v, want retryable %s", env.Error, CodeFileLibraryOperationPending)
	}
	if got, ok := env.Error.Details["execution_status"].(string); !ok || got != "pending" {
		t.Fatalf("execution_status = %#v, want pending", env.Error.Details["execution_status"])
	}
	if intake.calls != 0 {
		t.Fatalf("intake calls = %d, want denied before operation intake", intake.calls)
	}
	assertSavePointResponseDoesNotLeak(t, rec.Body.String())
}

func TestSavePointCreateRequiresLiveWorkerRecoveryCapabilityBeforeIntake(t *testing.T) {
	intake := &fakeOperationIntakeStore{}
	reader := &fakeSavePointCreateReadyReader{ready: false}
	handler := SavePointHandler(SavePointHandlerConfig{
		RepoReader:         &fakeRepoReader{repos: []resources.Repo{repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive)}},
		NamespaceReader:    &fakeNamespaceReader{namespace: activeNamespaceFixture("ns_123")},
		BindingReader:      &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin}})},
		FenceReader:        &fakeRepoFenceReader{},
		MutationGate:       &fakeRepoJVSMutationGateReader{},
		SessionStateReader: &fakeSavePointSessionStateReader{},
		CreateReady:        true,
		CreateReadyReader:  reader,
		IntakeStore:        intake,
		PrincipalResolver:  namespaceBindingPrincipalResolver(),
		AllowedCallers:     namespaceBindingAllowedPolicy(auth.RoleRepoAdmin),
		OperationID:        func() string { return "op_savepoint" },
		Now:                func() time.Time { return fixedNamespaceNow() },
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, savePointRequest(http.MethodPost, "/internal/v1/repos/repo_123/save-points", `{"message":"checkpoint"}`, "ns_123"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeFileLibraryOperationPending || !env.Error.Retryable {
		t.Fatalf("error = %#v, want retryable %s", env.Error, CodeFileLibraryOperationPending)
	}
	if got, ok := env.Error.Details["execution_reason"].(string); !ok || got != "worker_recovery_not_ready" {
		t.Fatalf("execution_reason = %#v, want worker_recovery_not_ready", env.Error.Details["execution_reason"])
	}
	if reader.calls != 1 || intake.calls != 0 {
		t.Fatalf("ready/intake calls = %d/%d, want worker capability check before operation intake", reader.calls, intake.calls)
	}
	assertSavePointResponseDoesNotLeak(t, rec.Body.String())
}

func TestSavePointCreateLiveWorkerRecoveryCapabilityErrorFailsClosed(t *testing.T) {
	intake := &fakeOperationIntakeStore{}
	reader := &fakeSavePointCreateReadyReader{err: errors.New("metadata unavailable")}
	handler := SavePointHandler(SavePointHandlerConfig{
		RepoReader:         &fakeRepoReader{repos: []resources.Repo{repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive)}},
		NamespaceReader:    &fakeNamespaceReader{namespace: activeNamespaceFixture("ns_123")},
		BindingReader:      &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin}})},
		FenceReader:        &fakeRepoFenceReader{},
		MutationGate:       &fakeRepoJVSMutationGateReader{},
		SessionStateReader: &fakeSavePointSessionStateReader{},
		CreateReady:        true,
		CreateReadyReader:  reader,
		IntakeStore:        intake,
		PrincipalResolver:  namespaceBindingPrincipalResolver(),
		AllowedCallers:     namespaceBindingAllowedPolicy(auth.RoleRepoAdmin),
		OperationID:        func() string { return "op_savepoint" },
		Now:                func() time.Time { return fixedNamespaceNow() },
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, savePointRequest(http.MethodPost, "/internal/v1/repos/repo_123/save-points", `{"message":"checkpoint"}`, "ns_123"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s, want 503", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeStorageUnavailable || !env.Error.Retryable {
		t.Fatalf("error = %#v, want retryable storage unavailable", env.Error)
	}
	if intake.calls != 0 {
		t.Fatalf("intake calls = %d, want denied before operation intake", intake.calls)
	}
	assertSavePointResponseDoesNotLeak(t, rec.Body.String())
}

func TestSavePointCreatePreservesNaturalLanguageSensitiveWords(t *testing.T) {
	for _, message := range []string{"fix secret handling", "rotate token docs", "update password policy"} {
		t.Run(message, func(t *testing.T) {
			intake := &fakeOperationIntakeStore{}
			handler := savePointTestHandler(intake, nil, resources.RepoStatusActive, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, savePointRequest(http.MethodPost, "/internal/v1/repos/repo_123/save-points", `{"message":"`+message+`"}`, "ns_123"))

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
			}
			if got := intake.spec.InputSummary["message"]; got != message {
				t.Fatalf("message summary = %#v, want %q", got, message)
			}
		})
	}
}

func TestSavePointCreateRejectsSecretShapedMessage(t *testing.T) {
	tests := []struct {
		name    string
		message string
		leak    string
	}{
		{name: "assignment", message: "token=abc123", leak: "abc123"},
		{name: "cli flag", message: "--password abc123", leak: "abc123"},
		{name: "bearer", message: "Authorization: Bearer abc.def.ghi", leak: "abc.def.ghi"},
		{name: "metadata url", message: "metadata at postgres://user:secret@metadata.internal:5432/jfs", leak: "postgres://"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intake := &fakeOperationIntakeStore{}
			handler := savePointTestHandler(intake, nil, resources.RepoStatusActive, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, savePointRequest(http.MethodPost, "/internal/v1/repos/repo_123/save-points", `{"message":"`+tt.message+`"}`, "ns_123"))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
			}
			if intake.calls != 0 {
				t.Fatalf("intake calls = %d, want rejected before durable operation", intake.calls)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != CodeInvalidID {
				t.Fatalf("code = %s, want %s", env.Error.Code, CodeInvalidID)
			}
			if strings.Contains(rec.Body.String(), tt.leak) {
				t.Fatalf("validation response leaked %q: %s", tt.leak, rec.Body.String())
			}
		})
	}
}

func TestSavePointCreateMessageLengthUsesUnicodeCharacters(t *testing.T) {
	valid := strings.Repeat("密", operations.MaxSavePointMessageRunes)
	intake := &fakeOperationIntakeStore{}
	handler := savePointTestHandler(intake, nil, resources.RepoStatusActive, nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, savePointRequest(http.MethodPost, "/internal/v1/repos/repo_123/save-points", `{"message":"`+valid+`"}`, "ns_123"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202 for 512 Unicode characters", rec.Code, rec.Body.String())
	}
	if got := intake.spec.InputSummary["message"]; got != valid {
		t.Fatalf("message summary length/value mismatch")
	}

	tooLong := valid + "密"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, savePointRequest(http.MethodPost, "/internal/v1/repos/repo_123/save-points", `{"message":"`+tooLong+`"}`, "ns_123"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400 for 513 Unicode characters", rec.Code, rec.Body.String())
	}
}

func TestSavePointCreateIdempotentReuseBeforeRepoStateChecks(t *testing.T) {
	canonical := savePointCreateCanonicalRequest{RepoID: "repo_123", Message: "checkpoint"}
	hash, err := operations.HashRequest(canonical)
	if err != nil {
		t.Fatal(err)
	}
	existing := savePointOperationRecord("op_existing", hash)
	intake := &fakeOperationIntakeStore{lookupRecord: &existing}
	repoReader := &fakeRepoReader{repos: []resources.Repo{repoResourceFixture("ns_123", "repo_123", resources.RepoStatusArchived)}}
	handler := SavePointHandler(SavePointHandlerConfig{
		RepoReader:        repoReader,
		NamespaceReader:   &fakeNamespaceReader{namespace: activeNamespaceFixture("ns_123")},
		BindingReader:     &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin}})},
		FenceReader:       &fakeRepoFenceReader{},
		IntakeStore:       intake,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    namespaceBindingAllowedPolicy(auth.RoleRepoAdmin),
		OperationID:       func() string { return "op_new" },
		Now:               func() time.Time { return fixedNamespaceNow() },
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, savePointRequest(http.MethodPost, "/internal/v1/repos/repo_123/save-points", `{"message":"checkpoint"}`, "ns_123"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	if intake.calls != 0 || repoReader.getInNamespaceCalls != 0 {
		t.Fatalf("intake/repo calls = %d/%d, want reused before metadata", intake.calls, repoReader.getInNamespaceCalls)
	}
	if !strings.Contains(rec.Body.String(), `"operation_id":"op_existing"`) {
		t.Fatalf("body = %s, want existing operation", rec.Body.String())
	}
}

func TestSavePointCreateDeniesArchivedAndLifecycleFence(t *testing.T) {
	tests := []struct {
		name   string
		status resources.RepoStatus
		fences []fences.Fence
		code   ErrorCode
	}{
		{name: "archived", status: resources.RepoStatusArchived, code: CodeRepoArchived},
		{name: "lifecycle fence", status: resources.RepoStatusActive, fences: []fences.Fence{savePointLifecycleFence("op_lifecycle")}, code: CodeRepoLifecycleFenceHeld},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := savePointTestHandler(&fakeOperationIntakeStore{}, nil, tt.status, tt.fences)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, savePointRequest(http.MethodPost, "/internal/v1/repos/repo_123/save-points", `{"message":"checkpoint"}`, "ns_123"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want conflict", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.code {
				t.Fatalf("code = %s, want %s", env.Error.Code, tt.code)
			}
			assertSavePointResponseDoesNotLeak(t, rec.Body.String())
		})
	}
}

func TestSavePointCreateAcceptsUndrainedWritersForExecutorDrain(t *testing.T) {
	now := fixedNamespaceNow()
	for _, tt := range []struct {
		name    string
		exports []sessionstate.ExportSession
		mounts  []sessionstate.WorkloadMountBinding
	}{
		{name: "active read-write export", exports: []sessionstate.ExportSession{savePointExportFixture(now, sessionstate.AccessModeReadWrite, sessionstate.ExportStatusActive, now.Add(time.Hour))}},
		{name: "stale read-write export", exports: []sessionstate.ExportSession{savePointExportFixture(now, sessionstate.AccessModeReadWrite, sessionstate.ExportStatusActive, now.Add(-time.Hour))}},
		{name: "active read-write mount", mounts: []sessionstate.WorkloadMountBinding{savePointMountFixture(now, false, sessionstate.MountStatusActive, now.Add(time.Hour), nil, nil)}},
		{name: "stale read-write mount", mounts: []sessionstate.WorkloadMountBinding{savePointMountFixture(now, false, sessionstate.MountStatusActive, now.Add(-time.Hour), nil, nil)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			intake := &fakeOperationIntakeStore{}
			sessionReader := &fakeSavePointSessionStateReader{
				exports: tt.exports,
				mounts:  tt.mounts,
			}
			handler := savePointTestHandlerWithSessions(intake, nil, &fakeRepoJVSMutationGateReader{}, sessionReader, resources.RepoStatusActive, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, savePointRequest(http.MethodPost, "/internal/v1/repos/repo_123/save-points", `{"message":"checkpoint"}`, "ns_123"))

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
			}
			if intake.calls != 1 {
				t.Fatalf("intake calls = %d, want create operation", intake.calls)
			}
			if intake.spec.Phase != operations.OperationPhaseSavePointCreateValidate || intake.spec.RepoID != "repo_123" {
				t.Fatalf("intake spec = %#v, want save point validate operation for repo_123", intake.spec)
			}
			if sessionReader.exportCalls != 0 || sessionReader.mountCalls != 0 {
				t.Fatalf("session calls export/mount = %d/%d, want executor-only writer drain", sessionReader.exportCalls, sessionReader.mountCalls)
			}
			assertSavePointResponseDoesNotLeak(t, rec.Body.String())
		})
	}
}

func TestSavePointCreateAllowsReadOnlyAndWriterDrainedMounts(t *testing.T) {
	now := fixedNamespaceNow()
	for _, tt := range []struct {
		name    string
		exports []sessionstate.ExportSession
		mounts  []sessionstate.WorkloadMountBinding
	}{
		{
			name:    "read only active export",
			exports: []sessionstate.ExportSession{savePointExportFixture(now, sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive, now.Add(time.Hour))},
		},
		{
			name:   "read only active mount",
			mounts: []sessionstate.WorkloadMountBinding{savePointMountFixture(now, true, sessionstate.MountStatusActive, now.Add(time.Hour), nil, nil)},
		},
		{
			name:   "released confirmed unmounted",
			mounts: []sessionstate.WorkloadMountBinding{savePointMountFixture(now, false, sessionstate.MountStatusReleased, now.Add(time.Hour), &now, nil)},
		},
		{
			name:   "revoked unable to write",
			mounts: []sessionstate.WorkloadMountBinding{savePointMountFixture(now, false, sessionstate.MountStatusRevoked, now.Add(time.Hour), nil, &now)},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			intake := &fakeOperationIntakeStore{}
			handler := savePointTestHandlerWithSessions(
				intake,
				nil,
				&fakeRepoJVSMutationGateReader{},
				&fakeSavePointSessionStateReader{exports: tt.exports, mounts: tt.mounts},
				resources.RepoStatusActive,
				nil,
			)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, savePointRequest(http.MethodPost, "/internal/v1/repos/repo_123/save-points", `{"message":"checkpoint"}`, "ns_123"))

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
			}
			if intake.calls != 1 {
				t.Fatalf("intake calls = %d, want create operation", intake.calls)
			}
			assertSavePointResponseDoesNotLeak(t, rec.Body.String())
		})
	}
}

func TestSavePointCreateRejectsDisabledNamespaceBeforeIntakeAndAudits(t *testing.T) {
	intake := &fakeOperationIntakeStore{}
	sink := &fakeAuditSink{}
	handler := SavePointHandler(SavePointHandlerConfig{
		RepoReader:        &fakeRepoReader{repos: []resources.Repo{repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive)}},
		NamespaceReader:   &fakeNamespaceReader{namespace: disabledNamespaceFixture("ns_123", "raw secret reason password=secret")},
		BindingReader:     &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin}})},
		FenceReader:       &fakeRepoFenceReader{},
		MutationGate:      &fakeRepoJVSMutationGateReader{},
		CreateReady:       true,
		CreateReadyReader: &fakeSavePointCreateReadyReader{ready: true},
		IntakeStore:       intake,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    namespaceBindingAllowedPolicy(auth.RoleRepoAdmin),
		OperationID:       func() string { return "op_savepoint" },
		Now:               func() time.Time { return fixedNamespaceNow() },
		AuditSink:         sink,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, savePointRequest(http.MethodPost, "/internal/v1/repos/repo_123/save-points", `{"message":"checkpoint"}`, "ns_123"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeNamespaceDisabled {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeNamespaceDisabled)
	}
	if intake.calls != 0 {
		t.Fatalf("intake calls = %d, want rejected before durable operation", intake.calls)
	}
	assertSavePointResponseDoesNotLeak(t, rec.Body.String())
	assertDisabledNamespaceDenialAuditDoesNotLeak(t, sink)
}

func TestSavePointListReturnsHistoryAndFailsClosed(t *testing.T) {
	reader := &fakeSavePointHistoryReader{history: SavePointHistory{SavePoints: []SavePointResponse{{SavePointID: "sp_001", Message: "first", CreatedAt: "2026-05-05T12:00:00Z", RepoID: "repo_123"}}}}
	handler := savePointTestHandler(&fakeOperationIntakeStore{}, reader, resources.RepoStatusActive, nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, savePointRequest(http.MethodGet, "/internal/v1/repos/repo_123/save-points", "", "ns_123"))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"save_point_id":"sp_001"`) || !strings.Contains(rec.Body.String(), `"message":"first"`) {
		t.Fatalf("status/body = %d/%s, want history", rec.Code, rec.Body.String())
	}
	assertSavePointResponseDoesNotLeak(t, rec.Body.String())

	reader.err = errors.New("malformed /srv/secret/.jvs")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, savePointRequest(http.MethodGet, "/internal/v1/repos/repo_123/save-points", "", "ns_123"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s, want fail closed", rec.Code, rec.Body.String())
	}
	assertSavePointResponseDoesNotLeak(t, rec.Body.String())
}

func TestSavePointListMissingHistoryReaderReturnsTypedCapabilityError(t *testing.T) {
	handler := savePointTestHandler(&fakeOperationIntakeStore{}, nil, resources.RepoStatusActive, nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, savePointRequest(http.MethodGet, "/internal/v1/repos/repo_123/save-points", "", "ns_123"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s, want typed 503", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeCapabilityDenied {
		t.Fatalf("error code = %s, want %s; body=%s", env.Error.Code, CodeCapabilityDenied, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), string(CodeInternalError)) {
		t.Fatalf("missing history reader returned internal error: %s", rec.Body.String())
	}
}

func TestSavePointListReturnsNaturalLanguageSensitiveWords(t *testing.T) {
	reader := &fakeSavePointHistoryReader{history: SavePointHistory{SavePoints: []SavePointResponse{{SavePointID: "sp_001", Message: "fix secret handling", CreatedAt: "2026-05-05T12:00:00Z", RepoID: "repo_123"}}}}
	handler := savePointTestHandler(&fakeOperationIntakeStore{}, reader, resources.RepoStatusActive, nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, savePointRequest(http.MethodGet, "/internal/v1/repos/repo_123/save-points", "", "ns_123"))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"message":"fix secret handling"`) {
		t.Fatalf("status/body = %d/%s, want natural-language message in history", rec.Code, rec.Body.String())
	}
	assertSavePointResponseDoesNotLeak(t, rec.Body.String())
}

func TestSavePointListGateConflictDoesNotReadHistory(t *testing.T) {
	reader := &fakeSavePointHistoryReader{history: SavePointHistory{SavePoints: []SavePointResponse{{SavePointID: "sp_001", CreatedAt: "2026-05-05T12:00:00Z", RepoID: "repo_123"}}}}
	gate := &fakeRepoJVSMutationGateReader{status: &RepoJVSMutationGateStatus{InProgress: true, OperationID: "op_active", OperationType: operations.OperationRestore, OperationState: operations.OperationStateRunning}}
	handler := savePointTestHandlerWithGate(&fakeOperationIntakeStore{}, reader, gate, resources.RepoStatusActive, nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, savePointRequest(http.MethodGet, "/internal/v1/repos/repo_123/save-points", "", "ns_123"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeFileLibraryOperationPending || !env.Error.Retryable {
		t.Fatalf("error = %#v, want retryable FILE_LIBRARY_OPERATION_PENDING", env.Error)
	}
	if env.Error.OperationID == nil || *env.Error.OperationID != "op_active" {
		t.Fatalf("error operation id = %#v, want blocking operation id", env.Error.OperationID)
	}
	assertBlockingOperationErrorProductSafe(t, rec.Body.String(), env, false)
	if reader.calls != 0 {
		t.Fatalf("history calls = %d, want none", reader.calls)
	}
	if gate.statusCalls != 1 {
		t.Fatalf("gate status calls = %d, want one pre-history check", gate.statusCalls)
	}
	assertSavePointResponseDoesNotLeak(t, rec.Body.String())
}

func TestSavePointListGateOperatorInterventionMapsRecoveryRequired(t *testing.T) {
	reader := &fakeSavePointHistoryReader{history: SavePointHistory{SavePoints: []SavePointResponse{{SavePointID: "sp_001", CreatedAt: "2026-05-05T12:00:00Z", RepoID: "repo_123"}}}}
	gate := &fakeRepoJVSMutationGateReader{status: &RepoJVSMutationGateStatus{InProgress: true, OperationID: "op_manual", OperationType: operations.OperationRestore, OperationState: operations.OperationStateOperatorInterventionRequired, RecoveryRequired: true}}
	handler := savePointTestHandlerWithGate(&fakeOperationIntakeStore{}, reader, gate, resources.RepoStatusActive, nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, savePointRequest(http.MethodGet, "/internal/v1/repos/repo_123/save-points", "", "ns_123"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeFileLibraryOperationRequiresRecovery || env.Error.Retryable {
		t.Fatalf("error = %#v, want non-retryable FILE_LIBRARY_OPERATION_REQUIRES_RECOVERY", env.Error)
	}
	if env.Error.OperationID == nil || *env.Error.OperationID != "op_manual" {
		t.Fatalf("error operation id = %#v, want manual operation id", env.Error.OperationID)
	}
	assertBlockingOperationErrorProductSafe(t, rec.Body.String(), env, true)
	if reader.calls != 0 {
		t.Fatalf("history calls = %d, want none for recovery required gate", reader.calls)
	}
}

func TestSavePointListGateErrorReturnsStorageUnavailable(t *testing.T) {
	reader := &fakeSavePointHistoryReader{history: SavePointHistory{SavePoints: []SavePointResponse{{SavePointID: "sp_001", CreatedAt: "2026-05-05T12:00:00Z", RepoID: "repo_123"}}}}
	gate := &fakeRepoJVSMutationGateReader{err: errors.New("postgres password=secret failed")}
	handler := savePointTestHandlerWithGate(&fakeOperationIntakeStore{}, reader, gate, resources.RepoStatusActive, nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, savePointRequest(http.MethodGet, "/internal/v1/repos/repo_123/save-points", "", "ns_123"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s, want 503", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeStorageUnavailable || !env.Error.Retryable {
		t.Fatalf("error = %#v, want retryable STORAGE_UNAVAILABLE", env.Error)
	}
	if reader.calls != 0 {
		t.Fatalf("history calls = %d, want none", reader.calls)
	}
	assertSavePointResponseDoesNotLeak(t, rec.Body.String())
}

func TestSavePointListChecksGateAgainAfterHistory(t *testing.T) {
	reader := &fakeSavePointHistoryReader{history: SavePointHistory{SavePoints: []SavePointResponse{{SavePointID: "sp_001", CreatedAt: "2026-05-05T12:00:00Z", RepoID: "repo_123"}}}}
	gate := &fakeRepoJVSMutationGateReader{responses: []bool{false, true}}
	handler := savePointTestHandlerWithGate(&fakeOperationIntakeStore{}, reader, gate, resources.RepoStatusActive, nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, savePointRequest(http.MethodGet, "/internal/v1/repos/repo_123/save-points", "", "ns_123"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	if reader.calls != 1 || gate.calls != 2 {
		t.Fatalf("history/gate calls = %d/%d, want 1/2", reader.calls, gate.calls)
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeFileLibraryOperationPending || !env.Error.Retryable {
		t.Fatalf("error = %#v, want retryable FILE_LIBRARY_OPERATION_PENDING", env.Error)
	}
	assertBlockingOperationErrorProductSafe(t, rec.Body.String(), env, false)
}

func assertBlockingOperationErrorProductSafe(t *testing.T, body string, env ErrorEnvelope, recoveryRequired bool) {
	t.Helper()
	for _, forbidden := range []string{
		"repo JVS mutation",
		"REPO_JVS_MUTATION",
		"blocking_operation_type",
		"blocking_operation_state",
		"save_point_create",
		"template_create",
		"template_clone",
		"operator_intervention_required",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("blocking operation response leaked internal term %q: %s", forbidden, body)
		}
	}
	if got, ok := env.Error.Details["recovery_required"].(bool); !ok || got != recoveryRequired {
		t.Fatalf("recovery_required detail = %#v, want %v", env.Error.Details["recovery_required"], recoveryRequired)
	}
	wantStatus := "in_progress"
	wantRetryable := true
	if recoveryRequired {
		wantStatus = "requires_recovery"
		wantRetryable = false
	}
	if got, ok := env.Error.Details["blocking_status"].(string); !ok || got != wantStatus {
		t.Fatalf("blocking_status detail = %#v, want %s", env.Error.Details["blocking_status"], wantStatus)
	}
	if got, ok := env.Error.Details["retryable"].(bool); !ok || got != wantRetryable {
		t.Fatalf("retryable detail = %#v, want %v", env.Error.Details["retryable"], wantRetryable)
	}
	if _, ok := env.Error.Details["blocking_operation_type"]; ok {
		t.Fatalf("details leaked blocking_operation_type: %#v", env.Error.Details)
	}
	if _, ok := env.Error.Details["blocking_operation_state"]; ok {
		t.Fatalf("details leaked blocking_operation_state: %#v", env.Error.Details)
	}
}

func TestInternalAPIShellCanInjectSavePointMutationGate(t *testing.T) {
	reader := &fakeSavePointHistoryReader{history: SavePointHistory{SavePoints: []SavePointResponse{{SavePointID: "sp_001", CreatedAt: "2026-05-05T12:00:00Z", RepoID: "repo_123"}}}}
	gate := &fakeRepoJVSMutationGateReader{inProgress: true}
	handler := savePointShellForGateTest(nil, reader, gate)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, savePointRequest(http.MethodGet, "/internal/v1/repos/repo_123/save-points", "", "ns_123"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	if gate.calls != 1 || reader.calls != 0 {
		t.Fatalf("gate/history calls = %d/%d, want 1/0", gate.calls, reader.calls)
	}
}

func TestInternalAPIShellAutoAssemblesSavePointMutationGateFromOperationStore(t *testing.T) {
	reader := &fakeSavePointHistoryReader{history: SavePointHistory{SavePoints: []SavePointResponse{{SavePointID: "sp_001", CreatedAt: "2026-05-05T12:00:00Z", RepoID: "repo_123"}}}}
	store := &fakeOperationIntakeStore{jvsMutation: true}
	handler := savePointShellForGateTest(store, reader, nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, savePointRequest(http.MethodGet, "/internal/v1/repos/repo_123/save-points", "", "ns_123"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	if store.jvsMutationCalls != 1 || reader.calls != 0 {
		t.Fatalf("store gate/history calls = %d/%d, want 1/0", store.jvsMutationCalls, reader.calls)
	}
}

func TestSavePointListDeniesArchivedAndLifecycleFenceBeforeHistory(t *testing.T) {
	tests := []struct {
		name   string
		status resources.RepoStatus
		fences []fences.Fence
		code   ErrorCode
	}{
		{name: "archived", status: resources.RepoStatusArchived, code: CodeRepoArchived},
		{name: "lifecycle fence", status: resources.RepoStatusActive, fences: []fences.Fence{savePointLifecycleFence("op_lifecycle")}, code: CodeRepoLifecycleFenceHeld},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeSavePointHistoryReader{history: SavePointHistory{SavePoints: []SavePointResponse{{SavePointID: "sp_001", CreatedAt: "2026-05-05T12:00:00Z", RepoID: "repo_123"}}}}
			handler := savePointTestHandler(&fakeOperationIntakeStore{}, reader, tt.status, tt.fences)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, savePointRequest(http.MethodGet, "/internal/v1/repos/repo_123/save-points", "", "ns_123"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want conflict", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.code {
				t.Fatalf("code = %s, want %s", env.Error.Code, tt.code)
			}
			if reader.calls != 0 {
				t.Fatalf("history calls = %d, want denied before history", reader.calls)
			}
			assertSavePointResponseDoesNotLeak(t, rec.Body.String())
		})
	}
}

func savePointTestHandler(intake *fakeOperationIntakeStore, history SavePointHistoryReader, repoStatus resources.RepoStatus, held []fences.Fence) http.Handler {
	return savePointTestHandlerWithGate(intake, history, &fakeRepoJVSMutationGateReader{}, repoStatus, held)
}

func savePointTestHandlerWithGate(intake *fakeOperationIntakeStore, history SavePointHistoryReader, gate RepoJVSMutationGateStatusReader, repoStatus resources.RepoStatus, held []fences.Fence) http.Handler {
	return savePointTestHandlerWithSessions(intake, history, gate, &fakeSavePointSessionStateReader{}, repoStatus, held)
}

func savePointTestHandlerWithSessions(intake *fakeOperationIntakeStore, history SavePointHistoryReader, gate RepoJVSMutationGateStatusReader, sessionReader SavePointSessionStateReader, repoStatus resources.RepoStatus, held []fences.Fence) http.Handler {
	return SavePointHandler(SavePointHandlerConfig{
		RepoReader:         &fakeRepoReader{repos: []resources.Repo{repoResourceFixture("ns_123", "repo_123", repoStatus)}},
		NamespaceReader:    &fakeNamespaceReader{namespace: activeNamespaceFixture("ns_123")},
		BindingReader:      &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin}})},
		FenceReader:        &fakeRepoFenceReader{fences: held},
		HistoryReader:      history,
		MutationGate:       gate,
		SessionStateReader: sessionReader,
		CreateReady:        true,
		CreateReadyReader:  &fakeSavePointCreateReadyReader{ready: true},
		IntakeStore:        intake,
		PrincipalResolver:  namespaceBindingPrincipalResolver(),
		AllowedCallers:     namespaceBindingAllowedPolicy(auth.RoleRepoAdmin),
		OperationID:        func() string { return "op_savepoint" },
		Now:                func() time.Time { return fixedNamespaceNow() },
	})
}

func savePointShellForGateTest(store OperationIntakeStore, history SavePointHistoryReader, gate RepoJVSMutationGateStatusReader) http.Handler {
	return NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceReader:        &fakeNamespaceReader{namespace: activeNamespaceFixture("ns_123")},
		NamespaceBindingReader: &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin}})},
		RepoReader:             &fakeRepoReader{repos: []resources.Repo{repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive)}},
		RepoFenceReader:        &fakeRepoFenceReader{},
		SavePointHistoryReader: history,
		SavePointMutationGate:  gate,
		OperationIntakeStore:   store,
	})
}

func savePointRequest(method, path, body, namespaceID string) *http.Request {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_savepoint")
	req.Header.Set(auth.HeaderCallerService, "product-caller")
	req.Header.Set(auth.HeaderIdempotencyKey, "idem_savepoint")
	req.Header.Set(auth.HeaderActorType, "user")
	req.Header.Set(auth.HeaderActorID, "user_123")
	if namespaceID != "" {
		req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	}
	return req
}

func activeNamespaceFixture(namespaceID string) resources.Namespace {
	now := fixedNamespaceNow()
	return resources.Namespace{ID: namespaceID, Status: resources.NamespaceStatusActive, CreatedAt: now, UpdatedAt: now}
}

func disabledNamespaceFixture(namespaceID, reason string) resources.Namespace {
	now := fixedNamespaceNow()
	disabledAt := now.Add(-time.Minute)
	return resources.Namespace{ID: namespaceID, Status: resources.NamespaceStatusDisabled, DisabledReason: reason, DisabledAt: &disabledAt, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
}

func savePointOperationRecord(operationID string, hash operations.RequestHash) operations.OperationRecord {
	now := fixedNamespaceNow()
	return operations.OperationRecord{ID: operationID, Type: operations.OperationSavePointCreate, State: operations.OperationStateQueued, Phase: operations.OperationPhaseSavePointCreateValidate, IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_123", operations.OperationSavePointCreate, "idem_savepoint").String(), IdempotencyKey: "idem_savepoint", RequestHash: hash, CorrelationID: "corr_savepoint", CallerService: "product-caller", AuthorizedActor: operations.Actor{Type: "user", ID: "user_123"}, Resource: operations.ResourceRef{Type: "repo", ID: "repo_123"}, NamespaceID: "ns_123", RepoID: "repo_123", ExternalResourceIDs: map[string]string{}, InputSummary: map[string]any{"message": "checkpoint"}, CreatedAt: now}
}

func savePointLifecycleFence(operationID string) fences.Fence {
	now := fixedNamespaceNow()
	return fences.Fence{ID: "fence_savepoint", RepoID: "repo_123", Kind: fences.KindLifecycle, HolderOperationID: operationID, Status: fences.StatusActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
}

func savePointExportFixture(now time.Time, mode sessionstate.AccessMode, status sessionstate.ExportStatus, expiresAt time.Time) sessionstate.ExportSession {
	return sessionstate.ExportSession{
		ID:                        "export_savepoint",
		NamespaceID:               "ns_123",
		RepoID:                    "repo_123",
		Mode:                      mode,
		Status:                    status,
		ExpiresAt:                 expiresAt,
		ActiveRequestCount:        1,
		ActiveWriteCount:          0,
		LastObservedAt:            &now,
		GatewayHeartbeatExpiresAt: ptrTime(now.Add(time.Minute)),
		CreatedAt:                 now.Add(-time.Minute),
		UpdatedAt:                 now,
	}
}

func savePointMountFixture(now time.Time, readOnly bool, status sessionstate.MountStatus, leaseExpiresAt time.Time, confirmedUnmountedAt, unableToWriteAt *time.Time) sessionstate.WorkloadMountBinding {
	return sessionstate.WorkloadMountBinding{
		ID:                   "wmb_savepoint",
		NamespaceID:          "ns_123",
		RepoID:               "repo_123",
		ReadOnly:             readOnly,
		Status:               status,
		LeaseExpiresAt:       leaseExpiresAt,
		ConfirmedUnmountedAt: confirmedUnmountedAt,
		UnableToWriteAt:      unableToWriteAt,
		CreatedAt:            now.Add(-time.Minute),
		UpdatedAt:            now,
	}
}

type fakeSavePointHistoryReader struct {
	history SavePointHistory
	err     error
	calls   int
}

func (reader *fakeSavePointHistoryReader) ListSavePoints(context.Context, string, string) (SavePointHistory, error) {
	reader.calls++
	if reader.err != nil {
		return SavePointHistory{}, reader.err
	}
	return reader.history, nil
}

type fakeSavePointCreateReadyReader struct {
	ready bool
	err   error
	calls int
}

func (reader *fakeSavePointCreateReadyReader) SavePointCreateRecoveryCapabilityReady(context.Context, time.Time) (bool, error) {
	reader.calls++
	if reader.err != nil {
		return false, reader.err
	}
	return reader.ready, nil
}

type fakeSavePointSessionStateReader struct {
	exports     []sessionstate.ExportSession
	mounts      []sessionstate.WorkloadMountBinding
	err         error
	exportCalls int
	mountCalls  int
}

func (reader *fakeSavePointSessionStateReader) ListExportSessionsByRepo(context.Context, string) ([]sessionstate.ExportSession, error) {
	reader.exportCalls++
	if reader.err != nil {
		return nil, reader.err
	}
	return append([]sessionstate.ExportSession(nil), reader.exports...), nil
}

func (reader *fakeSavePointSessionStateReader) ListWorkloadMountBindingsByRepo(context.Context, string) ([]sessionstate.WorkloadMountBinding, error) {
	reader.mountCalls++
	if reader.err != nil {
		return nil, reader.err
	}
	return append([]sessionstate.WorkloadMountBinding(nil), reader.mounts...), nil
}

type fakeRepoJVSMutationGateReader struct {
	inProgress  bool
	responses   []bool
	err         error
	calls       int
	status      *RepoJVSMutationGateStatus
	statusErr   error
	statusCalls int
}

func (reader *fakeRepoJVSMutationGateReader) RepoHasNonTerminalJVSMutation(context.Context, string) (bool, error) {
	reader.calls++
	if reader.err != nil {
		return false, reader.err
	}
	if len(reader.responses) > 0 {
		idx := reader.calls - 1
		if idx >= len(reader.responses) {
			idx = len(reader.responses) - 1
		}
		return reader.responses[idx], nil
	}
	return reader.inProgress, nil
}

func (reader *fakeRepoJVSMutationGateReader) GetRepoJVSMutationGateStatus(context.Context, string) (RepoJVSMutationGateStatus, error) {
	reader.statusCalls++
	if reader.statusErr != nil {
		return RepoJVSMutationGateStatus{}, reader.statusErr
	}
	if reader.status != nil {
		return *reader.status, nil
	}
	inProgress, err := reader.RepoHasNonTerminalJVSMutation(context.Background(), "")
	if err != nil {
		return RepoJVSMutationGateStatus{}, err
	}
	if inProgress {
		return RepoJVSMutationGateStatus{
			InProgress:     true,
			OperationID:    "op_blocking",
			OperationType:  operations.OperationRestore,
			OperationState: operations.OperationStateRunning,
		}, nil
	}
	return RepoJVSMutationGateStatus{}, nil
}

func assertSavePointResponseDoesNotLeak(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"/srv", ".jvs", "control_root", "payload_root", "raw_path", "jvs save", "jvs history", "password=secret", "raw secret reason", "token=", "bearer "} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("save point response leaked %q: %s", forbidden, body)
		}
	}
}

func assertDisabledNamespaceDenialAuditDoesNotLeak(t *testing.T, sink *fakeAuditSink) {
	t.Helper()
	if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("audit events = %#v, want one denied audit", sink.events)
	}
	rendered := strings.ToLower(auditEventString(t, sink.events[0]))
	for _, forbidden := range []string{"password=secret", "raw secret reason"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("denied audit leaked %q: %s", forbidden, rendered)
		}
	}
}
