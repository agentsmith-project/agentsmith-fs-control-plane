package repoaccess

import (
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestAdmitAllowsActiveRepoNamespaceAndBinding(t *testing.T) {
	decision := Admit(Request{
		Repo:      repoFixture(resources.RepoStatusActive),
		Namespace: namespaceFixture(resources.NamespaceStatusActive),
		Binding:   bindingFixture(resources.NamespaceStatusActive),
		Intent:    IntentStorageSession,
		Mode:      ModeReadOnly,
	})

	if !decision.Allowed || decision.Action != ActionAllow || decision.ErrorFamily != "" {
		t.Fatalf("decision = %#v, want allowed", decision)
	}
}

func TestAdmitAllowsExistingRepoWhenBindingDefaultVolumeChanged(t *testing.T) {
	req := activeRequest(IntentStorageSession, ModeReadOnly, resources.RepoStatusActive, nil)
	req.Binding.DefaultVolumeID = "vol_456"

	decision := Admit(req)

	if !decision.Allowed {
		t.Fatalf("decision = %#v, want allow for existing repo recorded on non-default volume", decision)
	}
}

func TestAdmitDeniesInactiveNamespaceAndBinding(t *testing.T) {
	tests := []struct {
		name      string
		namespace resources.Namespace
		binding   resources.NamespaceVolumeBinding
	}{
		{name: "disabled namespace", namespace: namespaceFixture(resources.NamespaceStatusDisabled), binding: bindingFixture(resources.NamespaceStatusActive)},
		{name: "disabled binding", namespace: namespaceFixture(resources.NamespaceStatusActive), binding: bindingFixture(resources.NamespaceStatusDisabled)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := Admit(Request{
				Repo:      repoFixture(resources.RepoStatusActive),
				Namespace: tt.namespace,
				Binding:   tt.binding,
				Intent:    IntentStorageSession,
				Mode:      ModeReadOnly,
			})

			assertDenied(t, decision, ErrorFamilyNamespaceDisabled)
		})
	}
}

func TestAdmitRestorePreviewDiscardCleanupIntentMatrix(t *testing.T) {
	disabledNamespace := namespaceFixture(resources.NamespaceStatusDisabled)
	activeBinding := bindingFixture(resources.NamespaceStatusActive)
	disabledBinding := bindingFixture(resources.NamespaceStatusDisabled)

	tests := []struct {
		name       string
		request    Request
		wantAllow  bool
		wantFamily ErrorFamily
	}{
		{
			name: "disabled namespace active binding allows discard cleanup",
			request: Request{
				Repo:      repoFixture(resources.RepoStatusActive),
				Namespace: disabledNamespace,
				Binding:   activeBinding,
				Intent:    IntentRestorePreviewDiscard,
				Mode:      ModeReadOnly,
			},
			wantAllow: true,
		},
		{
			name: "disabled namespace active binding does not allow ordinary restore-run",
			request: Request{
				Repo:      repoFixture(resources.RepoStatusActive),
				Namespace: disabledNamespace,
				Binding:   activeBinding,
				Intent:    IntentRestoreRun,
				Mode:      ModeReadWrite,
			},
			wantFamily: ErrorFamilyNamespaceDisabled,
		},
		{
			name: "disabled binding denies discard cleanup",
			request: Request{
				Repo:      repoFixture(resources.RepoStatusActive),
				Namespace: namespaceFixture(resources.NamespaceStatusActive),
				Binding:   disabledBinding,
				Intent:    IntentRestorePreviewDiscard,
				Mode:      ModeReadOnly,
			},
			wantFamily: ErrorFamilyNamespaceDisabled,
		},
		{
			name:       "inactive repo denies discard cleanup",
			request:    activeRequest(IntentRestorePreviewDiscard, ModeReadOnly, resources.RepoStatusArchiving, nil),
			wantFamily: ErrorFamilyOperationRecoveryRequired,
		},
		{
			name:       "archived repo denies discard cleanup",
			request:    activeRequest(IntentRestorePreviewDiscard, ModeReadOnly, resources.RepoStatusArchived, nil),
			wantFamily: ErrorFamilyRepoArchived,
		},
		{
			name:       "lifecycle fence denies discard cleanup",
			request:    activeRequest(IntentRestorePreviewDiscard, ModeReadOnly, resources.RepoStatusActive, []Fence{fenceFixture(FenceKindLifecycle, FenceStatusActive)}),
			wantFamily: ErrorFamilyRepoLifecycleFenceHeld,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := Admit(tt.request)

			if tt.wantAllow {
				if !decision.Allowed {
					t.Fatalf("decision = %#v, want allowed", decision)
				}
				return
			}
			assertDenied(t, decision, tt.wantFamily)
		})
	}
}

func TestAdmitDeniesTerminalRepoStatusFamiliesForOrdinaryAccess(t *testing.T) {
	tests := []struct {
		status resources.RepoStatus
		family ErrorFamily
	}{
		{status: resources.RepoStatusArchived, family: ErrorFamilyRepoArchived},
		{status: resources.RepoStatusTombstoned, family: ErrorFamilyRepoTombstoned},
		{status: resources.RepoStatusPurged, family: ErrorFamilyRepoPurged},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			decision := Admit(activeRequest(IntentStorageSession, ModeReadOnly, tt.status, nil))

			assertDenied(t, decision, tt.family)
		})
	}
}

func TestAdmitDeniesTransitionalLifecycleWithFencePriority(t *testing.T) {
	withoutFence := Admit(activeRequest(IntentStorageSession, ModeReadOnly, resources.RepoStatusArchiving, nil))
	assertDenied(t, withoutFence, ErrorFamilyOperationRecoveryRequired)

	withFence := Admit(activeRequest(IntentStorageSession, ModeReadOnly, resources.RepoStatusArchiving, []Fence{
		fenceFixture(FenceKindLifecycle, FenceStatusActive),
	}))
	assertDenied(t, withFence, ErrorFamilyRepoLifecycleFenceHeld)
	if withFence.BlockingFenceKind != FenceKindLifecycle.String() {
		t.Fatalf("blocking fence kind = %q, want lifecycle", withFence.BlockingFenceKind)
	}
}

func TestAdmitHeldFenceRecoveryStatusesRequireOperationRecovery(t *testing.T) {
	tests := []struct {
		name string
		kind FenceKind
	}{
		{name: "lifecycle", kind: FenceKindLifecycle},
		{name: "writer", kind: FenceKindWriterSession},
	}
	statuses := []FenceStatus{FenceStatusExpired, FenceStatusRecoveryRequired}
	for _, tt := range tests {
		for _, status := range statuses {
			t.Run(tt.name+" "+string(status), func(t *testing.T) {
				intent := IntentStorageSession
				mode := ModeReadWrite
				if tt.kind == FenceKindLifecycle {
					intent = IntentExportCreate
					mode = ModeReadOnly
				}

				decision := Admit(activeRequest(intent, mode, resources.RepoStatusActive, []Fence{
					fenceFixture(tt.kind, status),
				}))

				assertDenied(t, decision, ErrorFamilyOperationRecoveryRequired)
				if decision.BlockingFenceKind != tt.kind.String() {
					t.Fatalf("blocking fence kind = %q, want %s", decision.BlockingFenceKind, tt.kind)
				}
			})
		}
	}
}

func TestAdmitLifecycleFenceBlocksRepoAccessIntents(t *testing.T) {
	for _, intent := range []Intent{
		IntentExportCreate,
		IntentWorkloadMount,
		IntentSavePointCreate,
		IntentRestoreRun,
		IntentTemplateCreateFromRepo,
		IntentTemplateCloneIntoRepo,
		IntentStorageMutation,
		IntentStorageSession,
	} {
		t.Run(string(intent), func(t *testing.T) {
			decision := Admit(activeRequest(intent, ModeReadWrite, resources.RepoStatusActive, []Fence{
				fenceFixture(FenceKindLifecycle, FenceStatusActive),
			}))

			assertDenied(t, decision, ErrorFamilyRepoLifecycleFenceHeld)
		})
	}
}

func TestAdmitWriterFenceBlocksReadWriteExportsAndMountsOnly(t *testing.T) {
	for _, intent := range []Intent{IntentExportCreate, IntentWorkloadMount} {
		t.Run(string(intent), func(t *testing.T) {
			heldWriter := []Fence{fenceFixture(FenceKindWriterSession, FenceStatusActive)}

			readWrite := Admit(activeRequest(intent, ModeReadWrite, resources.RepoStatusActive, heldWriter))
			assertDenied(t, readWrite, ErrorFamilyWriterSessionFenceHeld)
			if readWrite.BlockingFenceKind != FenceKindWriterSession.String() {
				t.Fatalf("blocking fence kind = %q, want writer_session", readWrite.BlockingFenceKind)
			}

			readOnly := Admit(activeRequest(intent, ModeReadOnly, resources.RepoStatusActive, heldWriter))
			if !readOnly.Allowed {
				t.Fatalf("read-only decision = %#v, want allowed", readOnly)
			}
		})
	}
}

func TestAdmitWriterFenceBlocksLifecycleOperationAndRestoreRun(t *testing.T) {
	tests := []struct {
		name   string
		intent Intent
		mode   Mode
	}{
		{name: "lifecycle delete", intent: IntentLifecycleDelete, mode: ModeReadWrite},
		{name: "restore run", intent: IntentRestoreRun, mode: ModeReadWrite},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := Admit(activeRequest(tt.intent, tt.mode, resources.RepoStatusActive, []Fence{
				fenceFixture(FenceKindWriterSession, FenceStatusActive),
			}))

			assertDenied(t, decision, ErrorFamilyWriterSessionFenceHeld)
			if decision.BlockingFenceKind != FenceKindWriterSession.String() {
				t.Fatalf("blocking fence kind = %q, want writer_session", decision.BlockingFenceKind)
			}
		})
	}
}

func TestAdmitLifecycleOperationSourceStatusRules(t *testing.T) {
	tests := []struct {
		name       string
		intent     Intent
		status     resources.RepoStatus
		wantAllow  bool
		wantFamily ErrorFamily
	}{
		{name: "archive active", intent: IntentLifecycleArchive, status: resources.RepoStatusActive, wantAllow: true},
		{name: "archive archived", intent: IntentLifecycleArchive, status: resources.RepoStatusArchived, wantFamily: ErrorFamilyRepoLifecycleInvalidState},
		{name: "restore archived", intent: IntentLifecycleRestoreArchived, status: resources.RepoStatusArchived, wantAllow: true},
		{name: "restore archived from active", intent: IntentLifecycleRestoreArchived, status: resources.RepoStatusActive, wantFamily: ErrorFamilyRepoLifecycleInvalidState},
		{name: "delete active", intent: IntentLifecycleDelete, status: resources.RepoStatusActive, wantAllow: true},
		{name: "delete archived", intent: IntentLifecycleDelete, status: resources.RepoStatusArchived, wantAllow: true},
		{name: "delete tombstoned", intent: IntentLifecycleDelete, status: resources.RepoStatusTombstoned, wantFamily: ErrorFamilyRepoLifecycleInvalidState},
		{name: "restore tombstoned", intent: IntentLifecycleRestoreTombstoned, status: resources.RepoStatusTombstoned, wantAllow: true},
		{name: "purge tombstoned", intent: IntentLifecyclePurge, status: resources.RepoStatusTombstoned, wantAllow: true},
		{name: "purge archived", intent: IntentLifecyclePurge, status: resources.RepoStatusArchived, wantFamily: ErrorFamilyRepoLifecycleInvalidState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := Admit(activeRequest(tt.intent, ModeReadWrite, tt.status, nil))

			if tt.wantAllow {
				if !decision.Allowed {
					t.Fatalf("decision = %#v, want allowed", decision)
				}
				return
			}
			assertDenied(t, decision, tt.wantFamily)
		})
	}
}

func TestAdmitInvalidStoredStateFailsClosedWithoutLeakingSecrets(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Request)
	}{
		{name: "invalid repo", edit: func(req *Request) { req.Repo.JVSRepoID = "/srv/secret" }},
		{name: "invalid binding", edit: func(req *Request) { req.Binding.AllowedCallers = nil }},
		{name: "invalid fence", edit: func(req *Request) {
			req.HeldRepoFences = []Fence{fenceFixture(FenceKindLifecycle, FenceStatus("bad/secret"))}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := activeRequest(IntentStorageSession, ModeReadOnly, resources.RepoStatusActive, nil)
			tt.edit(&req)

			decision := Admit(req)

			assertDenied(t, decision, ErrorFamilyInternalError)
			rendered := strings.ToLower(decision.ErrorFamily.String() + " " + decision.Reason + " " + decision.BlockingFenceKind)
			for _, leaked := range []string{"/srv", "secret", "bad/secret"} {
				if strings.Contains(rendered, leaked) {
					t.Fatalf("decision leaked %q: %#v", leaked, decision)
				}
			}
		})
	}
}

func TestAdmitRejectsTemplateStorageIdentityForOrdinaryRepoAdmission(t *testing.T) {
	req := activeRequest(IntentStorageSession, ModeReadOnly, resources.RepoStatusActive, nil)
	req.Repo.ID = "tmpl_123"
	req.Repo.Kind = resources.RepoKindTemplate
	req.Repo.ControlVolumeSubdir = "afscp/namespaces/ns_123/templates/tmpl_123/control"
	req.Repo.PayloadVolumeSubdir = "afscp/namespaces/ns_123/templates/tmpl_123/payload"

	decision := Admit(req)

	assertDenied(t, decision, ErrorFamilyInternalError)
}

func activeRequest(intent Intent, mode Mode, status resources.RepoStatus, held []Fence) Request {
	return Request{
		Repo:           repoFixture(status),
		Namespace:      namespaceFixture(resources.NamespaceStatusActive),
		Binding:        bindingFixture(resources.NamespaceStatusActive),
		HeldRepoFences: held,
		Intent:         intent,
		Mode:           mode,
	}
}

func assertDenied(t *testing.T, decision Decision, family ErrorFamily) {
	t.Helper()
	if decision.Allowed || decision.Action != ActionDeny || decision.ErrorFamily != family {
		t.Fatalf("decision = %#v, want denied family %s", decision, family)
	}
}

func repoFixture(status resources.RepoStatus) resources.Repo {
	now := testNow()
	repo := resources.Repo{
		ID:                  "repo_123",
		NamespaceID:         "ns_123",
		VolumeID:            "vol_123",
		JVSRepoID:           "jvs_repo_alpha",
		Kind:                resources.RepoKindRepo,
		Status:              status,
		ControlVolumeSubdir: "afscp/namespaces/ns_123/repos/repo_123/control",
		PayloadVolumeSubdir: "afscp/namespaces/ns_123/repos/repo_123/payload",
		Lifecycle:           resources.RepoLifecycle{Status: status},
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	switch status {
	case resources.RepoStatusTombstoned:
		retention := now.Add(time.Hour)
		repo.Lifecycle.RetentionExpiresAt = &retention
		repo.Lifecycle.PreDeleteStatus = resources.RepoStatusActive
	case resources.RepoStatusPurged:
		repo.Lifecycle.PreDeleteStatus = resources.RepoStatusArchived
	case resources.RepoStatusDeleting, resources.RepoStatusRestoringTombstoned, resources.RepoStatusPurging:
		repo.Lifecycle.PreDeleteStatus = resources.RepoStatusActive
		if status == resources.RepoStatusRestoringTombstoned || status == resources.RepoStatusPurging {
			retention := now.Add(time.Hour)
			repo.Lifecycle.RetentionExpiresAt = &retention
		}
	}
	return repo
}

func namespaceFixture(status resources.NamespaceStatus) resources.Namespace {
	now := testNow()
	namespace := resources.Namespace{ID: "ns_123", Status: status, CreatedAt: now, UpdatedAt: now}
	if status == resources.NamespaceStatusDisabled {
		disabledAt := now.Add(time.Minute)
		namespace.DisabledAt = &disabledAt
		namespace.DisabledReason = "policy"
	}
	return namespace
}

func bindingFixture(status resources.NamespaceStatus) resources.NamespaceVolumeBinding {
	now := testNow()
	return resources.NamespaceVolumeBinding{
		NamespaceID:       "ns_123",
		DefaultVolumeID:   "vol_123",
		AllowedCallers:    []resources.AllowedCaller{{CallerService: "agentsmith-api", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin}}},
		QuotaBytesDefault: 4096,
		ExportPolicy:      map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600)},
		LifecyclePolicy:   map[string]any{"tombstone_retention_seconds": float64(604800), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
		MountPolicy:       map[string]any{"workload_mount_enabled": true, "workload_mount_requires_jvs_external_control_root": true, "allow_privileged_workload": false},
		TemplatePolicy:    map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
		Status:            status,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func fenceFixture(kind FenceKind, status FenceStatus) Fence {
	now := testNow()
	return Fence{
		ID:                "fence_123",
		RepoID:            "repo_123",
		Kind:              kind,
		HolderOperationID: "op_fence",
		Status:            status,
		ExpiresAt:         now.Add(time.Hour),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func testNow() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}
