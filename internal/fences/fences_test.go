package fences

import (
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/api"
)

func TestFenceWireValuesAreStable(t *testing.T) {
	kinds := map[Kind]string{
		KindWriterSession: "writer_session",
		KindLifecycle:     "lifecycle",
	}
	for kind, want := range kinds {
		if got := kind.String(); got != want {
			t.Fatalf("%#v String() = %q, want %q", kind, got, want)
		}
	}

	statuses := map[Status]string{
		StatusActive:           "active",
		StatusExpired:          "expired",
		StatusRecoveryRequired: "recovery_required",
		StatusReleased:         "released",
		StatusRecovered:        "recovered",
	}
	for status, want := range statuses {
		if got := status.String(); got != want {
			t.Fatalf("%#v String() = %q, want %q", status, got, want)
		}
	}
}

func TestErrorFamiliesMapToStableAPICodes(t *testing.T) {
	families := map[ErrorFamily]api.ErrorCode{
		ErrorFamilyInvalidID:                 api.CodeInvalidID,
		ErrorFamilyWriterSessionFenceHeld:    api.CodeWriterSessionFenceHeld,
		ErrorFamilyRepoLifecycleFenceHeld:    api.CodeRepoLifecycleFenceHeld,
		ErrorFamilyOperationRecoveryRequired: api.CodeOperationRecoveryRequired,
	}
	for family, code := range families {
		if string(family) != string(code) {
			t.Fatalf("family %q does not map to API code %q", family, code)
		}
	}
}

func TestHeldFollowsUnreleasedAndUnrecoveredDurableSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		fence Fence
		want  bool
	}{
		{name: "active", fence: heldFence(KindWriterSession, StatusActive, "repo_alpha", "op_restore"), want: true},
		{name: "expired still held", fence: heldFence(KindWriterSession, StatusExpired, "repo_alpha", "op_restore"), want: true},
		{name: "recovery required still held", fence: heldFence(KindLifecycle, StatusRecoveryRequired, "repo_alpha", "op_delete"), want: true},
		{name: "released unblocks", fence: releasedFence(KindWriterSession, "repo_alpha", "op_restore"), want: false},
		{name: "recovered unblocks", fence: recoveredFence(KindLifecycle, "repo_alpha", "op_delete"), want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.fence.Held(); got != tt.want {
				t.Fatalf("Held() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLifecycleFenceBlocksWriterSessionAndLifecycleAcquisition(t *testing.T) {
	t.Parallel()

	existing := []Fence{heldFence(KindLifecycle, StatusActive, "repo_alpha", "op_lifecycle")}
	for _, kind := range []Kind{KindWriterSession, KindLifecycle} {
		kind := kind
		t.Run(kind.String(), func(t *testing.T) {
			t.Parallel()

			decision := CanAcquire(AcquisitionRequest{
				RepoID:            "repo_alpha",
				Kind:              kind,
				HolderOperationID: "op_next",
			}, existing)
			assertDeniedWithFamily(t, decision, ErrorFamilyRepoLifecycleFenceHeld)
		})
	}
}

func TestHeldWriterSessionFenceBlocksWriterAndFailsClosedForLifecycle(t *testing.T) {
	t.Parallel()

	existing := []Fence{heldFence(KindWriterSession, StatusActive, "repo_alpha", "op_restore")}

	writerDecision := CanAcquire(AcquisitionRequest{
		RepoID:            "repo_alpha",
		Kind:              KindWriterSession,
		HolderOperationID: "op_export",
	}, existing)
	assertDeniedWithFamily(t, writerDecision, ErrorFamilyWriterSessionFenceHeld)

	lifecycleDecision := CanAcquire(AcquisitionRequest{
		RepoID:            "repo_alpha",
		Kind:              KindLifecycle,
		HolderOperationID: "op_delete",
	}, existing)
	assertDeniedWithFamily(t, lifecycleDecision, ErrorFamilyWriterSessionFenceHeld)
}

func TestLifecycleCanExplicitlyWaitOrRecoverHeldWriterSession(t *testing.T) {
	t.Parallel()

	existing := []Fence{heldFence(KindWriterSession, StatusActive, "repo_alpha", "op_restore")}
	decision := CanAcquire(AcquisitionRequest{
		RepoID:            "repo_alpha",
		Kind:              KindLifecycle,
		HolderOperationID: "op_delete",
		ConflictMode:      ConflictModeWaitOrRecover,
	}, existing)

	if decision.Allowed {
		t.Fatalf("Allowed = true, want wait/recover without acquisition")
	}
	if decision.Action != ActionWaitOrRecover {
		t.Fatalf("Action = %q, want %q", decision.Action, ActionWaitOrRecover)
	}
	if decision.Error != nil {
		t.Fatalf("Error = %v, want nil for explicit wait/recover decision", decision.Error)
	}
	if decision.BlockingFence == nil || decision.BlockingFence.Kind != KindWriterSession {
		t.Fatalf("BlockingFence = %#v, want writer-session fence", decision.BlockingFence)
	}
}

func TestExpiredOrRecoveryRequiredFenceFailsClosedUntilReleaseOrRecovery(t *testing.T) {
	t.Parallel()

	for _, status := range []Status{StatusExpired, StatusRecoveryRequired} {
		status := status
		t.Run(status.String(), func(t *testing.T) {
			t.Parallel()

			decision := CanAcquire(AcquisitionRequest{
				RepoID:            "repo_alpha",
				Kind:              KindWriterSession,
				HolderOperationID: "op_next",
			}, []Fence{heldFence(KindWriterSession, status, "repo_alpha", "op_restore")})
			assertDeniedWithFamily(t, decision, ErrorFamilyOperationRecoveryRequired)
		})
	}
}

func TestReleasedAndRecoveredFencesDoNotBlock(t *testing.T) {
	t.Parallel()

	existing := []Fence{
		releasedFence(KindLifecycle, "repo_alpha", "op_archive"),
		recoveredFence(KindWriterSession, "repo_alpha", "op_restore"),
	}
	decision := CanAcquire(AcquisitionRequest{
		RepoID:            "repo_alpha",
		Kind:              KindLifecycle,
		HolderOperationID: "op_delete",
	}, existing)

	if !decision.Allowed {
		t.Fatalf("Allowed = false, decision = %#v", decision)
	}
	if decision.Action != ActionAcquire {
		t.Fatalf("Action = %q, want %q", decision.Action, ActionAcquire)
	}
}

func TestDifferentReposDoNotBlockEachOther(t *testing.T) {
	t.Parallel()

	decision := CanAcquire(AcquisitionRequest{
		RepoID:            "repo_beta",
		Kind:              KindWriterSession,
		HolderOperationID: "op_next",
	}, []Fence{heldFence(KindLifecycle, StatusActive, "repo_alpha", "op_lifecycle")})

	if !decision.Allowed {
		t.Fatalf("Allowed = false, decision = %#v", decision)
	}
}

func TestInvalidNonRepoFieldsOnDifferentRepoDoNotBlock(t *testing.T) {
	t.Parallel()

	decision := CanAcquire(AcquisitionRequest{
		RepoID:            "repo_alpha",
		Kind:              KindWriterSession,
		HolderOperationID: "op_next",
	}, []Fence{
		{ID: "fence_1", RepoID: "repo_beta", Kind: Kind("wedged"), Status: Status("wedged")},
	})

	if !decision.Allowed {
		t.Fatalf("Allowed = false, decision = %#v", decision)
	}
}

func TestAcquisitionRejectsInvalidRequestInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request AcquisitionRequest
	}{
		{name: "missing repo", request: AcquisitionRequest{Kind: KindWriterSession, HolderOperationID: "op_restore"}},
		{name: "invalid repo", request: AcquisitionRequest{RepoID: "repo_bad/slash", Kind: KindWriterSession, HolderOperationID: "op_restore"}},
		{name: "missing kind", request: AcquisitionRequest{RepoID: "repo_alpha", HolderOperationID: "op_restore"}},
		{name: "invalid kind", request: AcquisitionRequest{RepoID: "repo_alpha", Kind: Kind("exclusive"), HolderOperationID: "op_restore"}},
		{name: "missing holder operation", request: AcquisitionRequest{RepoID: "repo_alpha", Kind: KindLifecycle}},
		{name: "invalid holder operation", request: AcquisitionRequest{RepoID: "repo_alpha", Kind: KindLifecycle, HolderOperationID: "restore"}},
		{name: "invalid conflict mode", request: AcquisitionRequest{RepoID: "repo_alpha", Kind: KindLifecycle, HolderOperationID: "op_restore", ConflictMode: ConflictMode("sleep")}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			decision := CanAcquire(tt.request, nil)
			assertDeniedWithFamily(t, decision, ErrorFamilyInvalidID)
		})
	}
}

func TestAcquisitionRejectsInvalidExistingFenceInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		fence Fence
	}{
		{name: "missing repo", fence: Fence{ID: "fence_1", Kind: KindWriterSession, HolderOperationID: "op_restore", Status: StatusActive}},
		{name: "missing kind", fence: Fence{ID: "fence_1", RepoID: "repo_alpha", HolderOperationID: "op_restore", Status: StatusActive}},
		{name: "missing status", fence: Fence{ID: "fence_1", RepoID: "repo_alpha", Kind: KindWriterSession, HolderOperationID: "op_restore"}},
		{name: "invalid status", fence: Fence{ID: "fence_1", RepoID: "repo_alpha", Kind: KindWriterSession, HolderOperationID: "op_restore", Status: Status("wedged")}},
		{name: "missing holder operation", fence: Fence{ID: "fence_1", RepoID: "repo_alpha", Kind: KindWriterSession, Status: StatusActive}},
		{name: "active with released timestamp", fence: invalidActiveReleasedFence()},
		{name: "recovered without released timestamp", fence: invalidRecoveredWithoutReleaseFence()},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			decision := CanAcquire(AcquisitionRequest{
				RepoID:            "repo_alpha",
				Kind:              KindWriterSession,
				HolderOperationID: "op_next",
			}, []Fence{tt.fence})
			assertDeniedWithFamily(t, decision, ErrorFamilyOperationRecoveryRequired)
		})
	}
}

func TestValidateFenceReportsStructuredInvalidInput(t *testing.T) {
	t.Parallel()

	err := ValidateFence(Fence{
		ID:                "fence_1",
		RepoID:            "repo_alpha",
		Kind:              KindWriterSession,
		HolderOperationID: "op_restore",
		Status:            Status("wedged"),
	})
	if err == nil {
		t.Fatalf("ValidateFence succeeded, want structured error")
	}
	if err.Family != ErrorFamilyInvalidID {
		t.Fatalf("Family = %q, want %q", err.Family, ErrorFamilyInvalidID)
	}
	if err.Field != "status" {
		t.Fatalf("Field = %q, want status", err.Field)
	}
}

func assertDeniedWithFamily(t *testing.T, decision AcquisitionDecision, family ErrorFamily) {
	t.Helper()

	if decision.Allowed {
		t.Fatalf("Allowed = true, want denied")
	}
	if decision.Action != ActionDeny {
		t.Fatalf("Action = %q, want %q", decision.Action, ActionDeny)
	}
	if decision.Error == nil {
		t.Fatalf("Error = nil, want family %q", family)
	}
	if decision.Error.Family != family {
		t.Fatalf("Family = %q, want %q; error = %v", decision.Error.Family, family, decision.Error)
	}
}

func heldFence(kind Kind, status Status, repoID, operationID string) Fence {
	return Fence{
		ID:                "fence_" + repoID + "_" + kind.String(),
		RepoID:            repoID,
		Kind:              kind,
		HolderOperationID: operationID,
		Status:            status,
	}
}

func releasedFence(kind Kind, repoID, operationID string) Fence {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	return Fence{
		ID:                "fence_released_" + repoID + "_" + kind.String(),
		RepoID:            repoID,
		Kind:              kind,
		HolderOperationID: operationID,
		Status:            StatusReleased,
		ReleasedAt:        &now,
	}
}

func recoveredFence(kind Kind, repoID, operationID string) Fence {
	releasedAt := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	recoveredAt := releasedAt.Add(time.Minute)
	recoveryStartedAt := releasedAt.Add(-time.Minute)
	return Fence{
		ID:                "fence_recovered_" + repoID + "_" + kind.String(),
		RepoID:            repoID,
		Kind:              kind,
		HolderOperationID: operationID,
		Status:            StatusRecovered,
		ReleasedAt:        &releasedAt,
		RecoveryStartedAt: &recoveryStartedAt,
		RecoveredAt:       &recoveredAt,
	}
}

func invalidActiveReleasedFence() Fence {
	fence := heldFence(KindWriterSession, StatusActive, "repo_alpha", "op_restore")
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	fence.ReleasedAt = &now
	return fence
}

func invalidRecoveredWithoutReleaseFence() Fence {
	fence := heldFence(KindLifecycle, StatusRecovered, "repo_alpha", "op_delete")
	now := time.Date(2026, 5, 5, 12, 1, 0, 0, time.UTC)
	fence.RecoveredAt = &now
	return fence
}
