package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

func TestWorkloadMountTerminalStatusRequiresExportVisiblePayloadRoot(t *testing.T) {
	for _, status := range []sessionstate.MountStatus{sessionstate.MountStatusReleased, sessionstate.MountStatusRevoked} {
		t.Run(string(status), func(t *testing.T) {
			meta := workloadMountMetaFixture()
			meta.mount.Status = sessionstate.MountStatusReleasing
			roots, volumeRoot, _ := missingPayloadVolumeRootsForTest(t, meta.mount.VolumeID, meta.mount.NamespaceID, meta.mount.RepoID)
			store := &fakeOperationIntakeStore{}
			config := workloadMountOrchestratorConfigForExportVisibleTest(store, meta, roots)
			rec := httptest.NewRecorder()

			WorkloadMountHandler(config).ServeHTTP(rec, workloadMountRequestForCaller(http.MethodPatch, "/internal/v1/workload-mount-bindings/wmb_123/status", terminalStatusBody(status), "ns_123", "runtime-orchestrator"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != CodeExportNotReady || !env.Error.Retryable {
				t.Fatalf("error = %#v, want retryable EXPORT_NOT_READY", env.Error)
			}
			if store.calls != 0 {
				t.Fatalf("intake calls = %d, want terminal status denied before operation create", store.calls)
			}
			assertDoesNotLeakPayloadRoot(t, rec.Body.String(), volumeRoot)
		})
	}
}

func TestRepoPayloadExportVisibleFailsClosedForNilAndEmptyVolumeRoots(t *testing.T) {
	for _, tt := range []struct {
		name  string
		roots map[string]string
	}{
		{name: "nil roots", roots: nil},
		{name: "empty roots", roots: map[string]string{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := repoPayloadExportVisible(tt.roots, "vol_123", "ns_123", "repo_123"); err == nil {
				t.Fatal("repoPayloadExportVisible returned nil, want not export-visible")
			}
		})
	}
}

func TestWorkloadMountTerminalStatusFailsClosedWithoutConfiguredVolumeRoots(t *testing.T) {
	for _, tt := range []struct {
		name  string
		roots map[string]string
	}{
		{name: "nil roots", roots: nil},
		{name: "empty roots", roots: map[string]string{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			meta := workloadMountMetaFixture()
			meta.mount.Status = sessionstate.MountStatusReleasing
			store := &fakeOperationIntakeStore{}
			config := workloadMountOrchestratorConfigForExportVisibleTest(store, meta, tt.roots)
			rec := httptest.NewRecorder()

			WorkloadMountHandler(config).ServeHTTP(rec, workloadMountRequestForCaller(http.MethodPatch, "/internal/v1/workload-mount-bindings/wmb_123/status", terminalStatusBody(sessionstate.MountStatusReleased), "ns_123", "runtime-orchestrator"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != CodeExportNotReady || !env.Error.Retryable {
				t.Fatalf("error = %#v, want retryable EXPORT_NOT_READY", env.Error)
			}
			if store.calls != 0 {
				t.Fatalf("intake calls = %d, want terminal status denied before operation create", store.calls)
			}
		})
	}
}

func TestWorkloadMountTerminalStatusRejectsSymlinkPayloadRoot(t *testing.T) {
	meta := workloadMountMetaFixture()
	meta.mount.Status = sessionstate.MountStatusReleasing
	roots, volumeRoot, payloadRoot := missingPayloadVolumeRootsForTest(t, meta.mount.VolumeID, meta.mount.NamespaceID, meta.mount.RepoID)
	if err := os.MkdirAll(filepath.Dir(payloadRoot), 0o755); err != nil {
		t.Fatalf("mkdir payload parent: %v", err)
	}
	target := filepath.Join(t.TempDir(), "payload-target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir symlink target: %v", err)
	}
	if err := os.Symlink(target, payloadRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store := &fakeOperationIntakeStore{}
	config := workloadMountOrchestratorConfigForExportVisibleTest(store, meta, roots)
	rec := httptest.NewRecorder()

	WorkloadMountHandler(config).ServeHTTP(rec, workloadMountRequestForCaller(http.MethodPatch, "/internal/v1/workload-mount-bindings/wmb_123/status", terminalStatusBody(sessionstate.MountStatusReleased), "ns_123", "runtime-orchestrator"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeExportNotReady || !env.Error.Retryable {
		t.Fatalf("error = %#v, want retryable EXPORT_NOT_READY", env.Error)
	}
	if store.calls != 0 {
		t.Fatalf("intake calls = %d, want symlink root denied before operation create", store.calls)
	}
	assertDoesNotLeakPayloadRoot(t, rec.Body.String(), volumeRoot)
	assertDoesNotLeakPayloadRoot(t, rec.Body.String(), target)
}

func TestRepoPayloadExportVisibleRejectsNonDirectoryPayloadRoot(t *testing.T) {
	meta := workloadMountMetaFixture()
	roots, _, payloadRoot := missingPayloadVolumeRootsForTest(t, meta.mount.VolumeID, meta.mount.NamespaceID, meta.mount.RepoID)
	if err := os.MkdirAll(filepath.Dir(payloadRoot), 0o755); err != nil {
		t.Fatalf("mkdir payload parent: %v", err)
	}
	if err := os.WriteFile(payloadRoot, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write payload file: %v", err)
	}

	if err := repoPayloadExportVisible(roots, meta.mount.VolumeID, meta.mount.NamespaceID, meta.mount.RepoID); err == nil {
		t.Fatal("repoPayloadExportVisible(file payload root) = nil, want not export-visible")
	}
}

func TestWorkloadMountTerminalStatusAllowsWhenPayloadRootExportVisible(t *testing.T) {
	for _, status := range []sessionstate.MountStatus{sessionstate.MountStatusReleased, sessionstate.MountStatusRevoked} {
		t.Run(string(status), func(t *testing.T) {
			meta := workloadMountMetaFixture()
			meta.mount.Status = sessionstate.MountStatusReleasing
			roots, _ := visiblePayloadVolumeRootsForTest(t, meta.mount.VolumeID, meta.mount.NamespaceID, meta.mount.RepoID)
			store := &fakeOperationIntakeStore{}
			config := workloadMountOrchestratorConfigForExportVisibleTest(store, meta, roots)
			rec := httptest.NewRecorder()

			WorkloadMountHandler(config).ServeHTTP(rec, workloadMountRequestForCaller(http.MethodPatch, "/internal/v1/workload-mount-bindings/wmb_123/status", terminalStatusBody(status), "ns_123", "runtime-orchestrator"))

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
			}
			if store.calls != 1 || store.spec.Phase != operations.OperationPhaseMountBindingStatusValidate {
				t.Fatalf("intake calls/spec = %d/%#v, want status update operation", store.calls, store.spec)
			}
		})
	}
}

func TestWorkloadMountNonTerminalStatusDoesNotRequireExportVisiblePayloadRoot(t *testing.T) {
	for _, status := range []sessionstate.MountStatus{sessionstate.MountStatusPending, sessionstate.MountStatusActive} {
		t.Run(string(status), func(t *testing.T) {
			meta := workloadMountMetaFixture()
			roots, _, _ := missingPayloadVolumeRootsForTest(t, meta.mount.VolumeID, meta.mount.NamespaceID, meta.mount.RepoID)
			store := &fakeOperationIntakeStore{}
			config := workloadMountOrchestratorConfigForExportVisibleTest(store, meta, roots)
			rec := httptest.NewRecorder()

			WorkloadMountHandler(config).ServeHTTP(rec, workloadMountRequestForCaller(http.MethodPatch, "/internal/v1/workload-mount-bindings/wmb_123/status", terminalStatusBody(status), "ns_123", "runtime-orchestrator"))

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
			}
			if store.calls != 1 || store.spec.Phase != operations.OperationPhaseMountBindingStatusValidate {
				t.Fatalf("intake calls/spec = %d/%#v, want status update operation", store.calls, store.spec)
			}
		})
	}
}

func TestCreateExportRequiresExportVisiblePayloadRootBeforePassword(t *testing.T) {
	meta := exportMetaFixture()
	roots, volumeRoot, _ := missingPayloadVolumeRootsForTest(t, meta.repo.VolumeID, meta.repo.NamespaceID, meta.repo.ID)
	store := &fakeExportStore{}
	passwordCalls := 0
	handler := exportHandlerWithVolumeRootsForTest(store, meta, roots, func() string {
		passwordCalls++
		return "export-password-once"
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only","ttl_seconds":120}`, "ns_123"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeExportNotReady || !env.Error.Retryable {
		t.Fatalf("error = %#v, want retryable EXPORT_NOT_READY", env.Error)
	}
	if store.createCalls != 0 || passwordCalls != 0 {
		t.Fatalf("create/password calls = %d/%d, want denied before credential creation", store.createCalls, passwordCalls)
	}
	rendered := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"access", "password", "export-password-once", "credential_hash", "credential_salt", "verifier"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("not-ready response leaked %q: %s", forbidden, rec.Body.String())
		}
	}
	assertDoesNotLeakPayloadRoot(t, rec.Body.String(), volumeRoot)
}

func TestCreateExportFailsClosedWithoutConfiguredVolumeRootsBeforePassword(t *testing.T) {
	for _, tt := range []struct {
		name  string
		roots map[string]string
	}{
		{name: "nil roots", roots: nil},
		{name: "empty roots", roots: map[string]string{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			meta := exportMetaFixture()
			store := &fakeExportStore{}
			passwordCalls := 0
			handler := exportHandlerWithVolumeRootsForTest(store, meta, tt.roots, func() string {
				passwordCalls++
				return "export-password-once"
			})
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only","ttl_seconds":120}`, "ns_123"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != CodeExportNotReady || !env.Error.Retryable {
				t.Fatalf("error = %#v, want retryable EXPORT_NOT_READY", env.Error)
			}
			if store.createCalls != 0 || passwordCalls != 0 {
				t.Fatalf("create/password calls = %d/%d, want denied before credential creation", store.createCalls, passwordCalls)
			}
		})
	}
}

func TestCreateExportAllowsWhenPayloadRootExportVisible(t *testing.T) {
	meta := exportMetaFixture()
	roots, _ := visiblePayloadVolumeRootsForTest(t, meta.repo.VolumeID, meta.repo.NamespaceID, meta.repo.ID)
	store := &fakeExportStore{}
	handler := exportHandlerWithVolumeRootsForTest(store, meta, roots, func() string { return "export-password-once" })
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only","ttl_seconds":120}`, "ns_123"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	if store.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", store.createCalls)
	}
}

func workloadMountOrchestratorConfigForExportVisibleTest(store *fakeOperationIntakeStore, meta workloadMountMeta, roots map[string]string) WorkloadMountHandlerConfig {
	config := workloadMountHandlerConfig(store, fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{CallerService: "runtime-orchestrator", Kind: auth.CallerKindOrchestrator, Roles: []auth.Role{auth.RoleOrchestratorMount}}}}, func(config *WorkloadMountHandlerConfig) {
		config.MountReader = fakeWorkloadMountReader{binding: meta.mount}
		config.VolumeRoots = roots
	})
	config.PrincipalResolver = fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{Subject: "svc:runtime-orchestrator", CanonicalCallerService: "runtime-orchestrator"}}
	return config
}

func exportHandlerWithVolumeRootsForTest(store *fakeExportStore, meta exportMeta, roots map[string]string, password func() string) http.Handler {
	return ExportHandler(ExportHandlerConfig{
		RepoReader:        meta.repoReader,
		NamespaceReader:   meta.namespaceReader,
		BindingReader:     meta.bindingReader,
		VolumeReader:      meta.volumeReader,
		FenceReader:       &fakeRepoFenceReader{fences: meta.fences},
		VolumeRoots:       roots,
		Store:             store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    namespaceBindingAllowedPolicy(auth.RoleExportAdmin),
		OperationID:       func() string { return "op_export" },
		ExportID:          func() string { return "export_123" },
		Password:          password,
		Now:               fixedNamespaceNow,
		PublicBaseURL:     "https://files.example.com",
	})
}

func visiblePayloadVolumeRootsForTest(t testing.TB, volumeID, namespaceID, repoID string) (map[string]string, string) {
	t.Helper()
	roots, volumeRoot, payloadRoot := missingPayloadVolumeRootsForTest(t, volumeID, namespaceID, repoID)
	if err := os.MkdirAll(payloadRoot, 0o755); err != nil {
		t.Fatalf("mkdir payload root: %v", err)
	}
	return roots, volumeRoot
}

func missingPayloadVolumeRootsForTest(t testing.TB, volumeID, namespaceID, repoID string) (map[string]string, string, string) {
	t.Helper()
	volumeRoot := t.TempDir()
	paths, err := pathresolver.ResolveRepoRootPaths(volumeRoot, namespaceID, repoID)
	if err != nil {
		t.Fatalf("resolve repo root paths: %v", err)
	}
	return map[string]string{volumeID: volumeRoot}, volumeRoot, paths.PayloadRootPath
}

func terminalStatusBody(status sessionstate.MountStatus) string {
	return `{"status":"` + string(status) + `","observed_at":"2026-05-05T12:00:00Z","reason":"unmounted"}`
}

func assertDoesNotLeakPayloadRoot(t *testing.T, body, root string) {
	t.Helper()
	if root != "" && strings.Contains(body, root) {
		t.Fatalf("response leaked local root %q: %s", root, body)
	}
}
