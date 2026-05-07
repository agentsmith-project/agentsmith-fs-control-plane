package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

func TestOperationStoreContractComposesReaderAndWriter(t *testing.T) {
	fake := &fakeOperationStore{}

	var _ OperationReader = fake
	var _ OperationWriter = fake
	var _ OperationStore = fake

	record := operations.OperationRecord{
		ID:        "op_alpha",
		Type:      operations.OperationRepoCreate,
		State:     operations.OperationStateQueued,
		CreatedAt: time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC),
	}

	if err := fake.CreateOperation(context.Background(), record.SanitizedForPersistence()); err != nil {
		t.Fatalf("create operation: %v", err)
	}

	got, err := fake.GetOperation(context.Background(), "op_alpha")
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.ID != "op_alpha" || got.State != operations.OperationStateQueued {
		t.Fatalf("operation = %#v, want queued op_alpha", got)
	}
}

func TestOperationLeaseStoreContractCoversClaimReclaimRenewAndFencedUpdateByOperationID(t *testing.T) {
	fake := &fakeOperationStore{}
	var _ OperationLeaseStore = fake

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	fake.record = operations.OperationRecord{
		ID:        "op_alpha",
		Type:      operations.OperationRepoCreate,
		State:     operations.OperationStateQueued,
		CreatedAt: now.Add(-time.Minute),
	}

	claimed, err := fake.AcquireOperationLease(context.Background(), "op_alpha", operations.LeaseRequest{
		Owner:    "worker-a",
		Duration: 30 * time.Minute,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("claim operation lease: %v", err)
	}
	if claimed.State != operations.OperationStateRunning || claimed.LeaseOwner != "worker-a" || claimed.Attempt != 1 {
		t.Fatalf("claimed operation = %#v, want running worker-a attempt 1", claimed)
	}

	reclaimed, err := fake.AcquireOperationLease(context.Background(), "op_alpha", operations.LeaseRequest{
		Owner:    "worker-b",
		Duration: 45 * time.Minute,
		Now:      now.Add(31 * time.Minute),
	})
	if err != nil {
		t.Fatalf("reclaim operation lease: %v", err)
	}
	if reclaimed.LeaseOwner != "worker-b" || reclaimed.Attempt != 2 {
		t.Fatalf("reclaimed operation = %#v, want worker-b attempt 2", reclaimed)
	}

	renewed, err := fake.RenewOperationLease(context.Background(), "op_alpha", operations.LeaseRequest{
		Owner:    "worker-b",
		Duration: time.Hour,
		Now:      now.Add(32 * time.Minute),
	})
	if err != nil {
		t.Fatalf("renew operation lease: %v", err)
	}
	if renewed.LeaseOwner != "worker-b" || renewed.Attempt != 2 {
		t.Fatalf("renewed operation = %#v, want worker-b attempt unchanged", renewed)
	}
	if renewed.LeaseExpiresAt == nil || !renewed.LeaseExpiresAt.Equal(now.Add(92*time.Minute)) {
		t.Fatalf("renewed lease expiry = %v, want %v", renewed.LeaseExpiresAt, now.Add(92*time.Minute))
	}

	renewed.Phase = "verify_result"
	renewed.JVSJSONOutput = map[string]any{"token": "lease-fenced-secret"}
	updated, err := fake.UpdateOperationWithLease(context.Background(), renewed.SanitizedForPersistence(), "worker-b", now.Add(33*time.Minute))
	if err != nil {
		t.Fatalf("lease-fenced update operation: %v", err)
	}
	if updated.LeaseOwner != "worker-b" || updated.Attempt != 2 {
		t.Fatalf("fenced updated operation = %#v, want worker-b attempt unchanged", updated)
	}
	if rendered := updated.JVSJSONOutput.(map[string]any)["token"]; rendered == "lease-fenced-secret" {
		t.Fatalf("fenced update stored unsanitized output: %#v", updated.JVSJSONOutput)
	}
}

func TestOperationWorkerCommitStoreContractCommitsSanitizedOperationAndAuditTogether(t *testing.T) {
	fake := &fakeOperationStore{}
	var _ OperationWorkerCommitStore = fake

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	leaseExpiresAt := now.Add(30 * time.Minute)
	fake.record = operations.OperationRecord{
		ID:             "op_alpha",
		Type:           operations.OperationExportCreate,
		State:          operations.OperationStateRunning,
		LeaseOwner:     "worker-a",
		LeaseExpiresAt: &leaseExpiresAt,
		CreatedAt:      now.Add(-time.Hour),
	}
	update := fake.record
	update.State = operations.OperationStateSucceeded
	update.InputSummary = map[string]any{"command": "export --token contract-secret"}
	event := audit.NewEvent(audit.Event{
		EventID:       "audit-op-alpha",
		Type:          audit.EventTypeExportCreate,
		Time:          now,
		OperationID:   "op_alpha",
		Resource:      audit.Resource{Type: "export", ID: "export_alpha"},
		Outcome:       audit.OutcomeSucceeded,
		Reason:        "done token=audit-secret",
		CallerService: "afscp-api",
	})

	committed, err := fake.CommitOperationWithLease(context.Background(), update.SanitizedForPersistence(), "worker-a", now, event)
	if err != nil {
		t.Fatalf("commit operation with audit: %v", err)
	}
	if committed.State != operations.OperationStateSucceeded || committed.LeaseOwner != "" || len(fake.auditEvents) != 1 {
		t.Fatalf("commit state/audit = %#v/%#v", committed, fake.auditEvents)
	}
	if strings.Contains(toStoreContractString(fake.record.InputSummary), "contract-secret") ||
		strings.Contains(fake.auditEvents[0].Reason, "audit-secret") {
		t.Fatalf("commit stored unsanitized operation/audit: %#v %#v", fake.record, fake.auditEvents)
	}
}

func TestExportAccessStoreContractDoesNotRequireLegacyTerminalHelper(t *testing.T) {
	fake := &fakeExportAccessStore{}
	var _ ExportStore = fake
	var _ ExportAccessStore = fake

	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	request := exportaccess.ReconcileRequest{
		ExportID:           "export_alpha01",
		NamespaceID:        "ns_alpha01",
		TargetStatus:       sessionstate.ExportStatusRevoked,
		ObservedAt:         now,
		ActiveRequestCount: 0,
		ActiveWriteCount:   0,
	}
	result, err := fake.ReconcileExportSessionTerminal(context.Background(), request)
	if err != nil {
		t.Fatalf("reconcile terminal through contract fake: %v", err)
	}
	if result.Session.ID != request.ExportID || result.Session.Status != sessionstate.ExportStatusRevoked {
		t.Fatalf("reconciled session = %#v, want revoked export_alpha01", result.Session)
	}
}

func TestRestorePlanStoreContractOwnsPreviewRunDiscardLifecycle(t *testing.T) {
	fake := &fakeRestorePlanStore{}
	var _ RestorePlanReader = fake
	var _ RestorePlanWriter = fake
	var _ RestorePlanStore = fake

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	plan := restoreplan.Plan{
		ID:                 "b644aec4-bcb6-4480-b5fa-a283927dd3cd",
		NamespaceID:        "ns_alpha01",
		RepoID:             "repo_alpha01",
		PreviewOperationID: "op_preview01",
		SourceSavePointID:  "sp_001",
		Status:             restoreplan.StatusPending,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := fake.CreatePendingRestorePlan(context.Background(), plan); err != nil {
		t.Fatalf("create pending restore plan: %v", err)
	}
	active, err := fake.GetActiveRestorePlanByRepo(context.Background(), "repo_alpha01")
	if err != nil {
		t.Fatalf("get active restore plan: %v", err)
	}
	if active.ID != "b644aec4-bcb6-4480-b5fa-a283927dd3cd" || !active.Active() {
		t.Fatalf("active plan = %#v, want pending UUID-like JVS plan id", active)
	}

	discarding, err := fake.TransitionRestorePlanStatus(context.Background(), "b644aec4-bcb6-4480-b5fa-a283927dd3cd", restoreplan.StatusPending, restoreplan.StatusDiscarding, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("transition restore plan: %v", err)
	}
	if discarding.Status != restoreplan.StatusDiscarding || discarding.PreviewOperationID != "op_preview01" {
		t.Fatalf("transitioned plan = %#v, want discarding with preview linkage", discarding)
	}
}

func TestRestorePreviewOperationRecoveryStoreContractCommitsPlanOperationAndAuditTogether(t *testing.T) {
	fake := &fakeRestorePreviewOperationStore{}
	var _ RestorePreviewOperationCommitStore = fake
	var _ RestorePreviewOperationMetadataReader = fake
	var _ RestorePreviewOperationRecoveryStore = fake

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	leaseExpiresAt := now.Add(30 * time.Minute)
	fake.record = operations.OperationRecord{
		ID:               "op_preview01",
		Type:             operations.OperationRestorePreview,
		State:            operations.OperationStateRunning,
		Phase:            operations.OperationPhaseRestorePreviewValidate,
		LeaseOwner:       "worker-a",
		LeaseExpiresAt:   &leaseExpiresAt,
		IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationRestorePreview, "idem_preview").String(),
		IdempotencyKey:   "idem_preview",
		RequestHash:      operations.RequestHash("sha256:restore-preview"),
		CallerService:    "product-caller",
		CorrelationID:    "corr-preview",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:      "ns_alpha01",
		RepoID:           "repo_alpha01",
		InputSummary:     map[string]any{"save_point_id": "sp_001"},
		CreatedAt:        now.Add(-time.Hour),
	}

	preflight := fake.record
	preflight.Phase = operations.OperationPhaseRestorePreviewPreflightIdle
	preflight.VerificationResult = map[string]any{"preflight_recovery_status_captured": true, "preflight_restore_state": "idle", "preflight_blocking": false}
	updated, err := fake.UpdateRestorePreviewPreflightWithLease(context.Background(), preflight.SanitizedForPersistence(), "worker-a", now)
	if err != nil {
		t.Fatalf("preflight update: %v", err)
	}
	if updated.Phase != operations.OperationPhaseRestorePreviewPreflightIdle {
		t.Fatalf("preflight operation = %#v", updated)
	}

	terminal := updated
	terminal.State = operations.OperationStateSucceeded
	terminal.Phase = operations.OperationPhaseRestorePreviewCommitted
	terminal.ExternalResourceIDs = map[string]string{"restore_plan_id": "plan_001"}
	terminal.JVSJSONOutput = map[string]any{"restore_plan_id": "plan_001", "source_save_point_id": "sp_001", "run_command_present": true}
	terminal.VerificationResult = map[string]any{"preflight_recovery_status_captured": true, "preflight_restore_state": "idle", "preflight_blocking": false, "restore_plan_id": "plan_001", "source_save_point_id": "sp_001"}
	terminal.FinishedAt = &now
	plan := restoreplan.Plan{ID: "plan_001", NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", PreviewOperationID: "op_preview01", SourceSavePointID: "sp_001", Status: restoreplan.StatusPending, CreatedAt: now, UpdatedAt: now}
	event := audit.NewEvent(audit.Event{EventID: "audit-preview", Type: audit.EventTypeRestorePreview, Time: now, OperationID: "op_preview01", CallerService: "product-caller", CorrelationID: "corr-preview", AuthorizedActor: audit.Actor{Type: "system", ID: "svc-alpha"}, Resource: audit.Resource{Type: "repo", ID: "repo_alpha01", NamespaceID: "ns_alpha01"}, Outcome: audit.OutcomeSucceeded, Reason: "restore_preview_committed"})

	gotPlan, gotOperation, err := fake.CommitRestorePreviewSucceededWithLease(context.Background(), plan, terminal.SanitizedForPersistence(), "worker-a", now, event)
	if err != nil {
		t.Fatalf("success commit: %v", err)
	}
	if gotPlan.Status != restoreplan.StatusPending || gotOperation.State != operations.OperationStateSucceeded || len(fake.auditEvents) != 1 {
		t.Fatalf("commit plan/operation/audit = %#v/%#v/%#v", gotPlan, gotOperation, fake.auditEvents)
	}
	if _, err := fake.GetRestorePlanByPreviewOperation(context.Background(), "op_preview01"); err != nil {
		t.Fatalf("durable restore plan missing after commit: %v", err)
	}
	if strings.Contains(toStoreContractString(gotOperation.JVSJSONOutput), "run restore") || strings.Contains(toStoreContractString(gotOperation.VerificationResult), "recommended_next_command") {
		t.Fatalf("restore preview operation persisted raw command fields: %#v", gotOperation)
	}
}

func TestRestoreRunOperationRecoveryStoreContractOwnsFencePlanOperationAndAudit(t *testing.T) {
	fake := &fakeRestoreRunOperationStore{}
	var _ RestoreRunOperationCommitStore = fake
	var _ RestoreRunOperationMetadataReader = fake
	var _ RestoreRunOperationRecoveryStore = fake

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	fake.previewOperation = operations.OperationRecord{
		ID:          "op_preview01",
		Type:        operations.OperationRestorePreview,
		State:       operations.OperationStateSucceeded,
		Phase:       operations.OperationPhaseRestorePreviewCommitted,
		NamespaceID: "ns_alpha01",
		RepoID:      "repo_alpha01",
		Resource:    operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		CreatedAt:   now.Add(-time.Hour),
	}
	fake.plan = restoreplan.Plan{ID: "plan_001", NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", PreviewOperationID: "op_preview01", SourceSavePointID: "sp_001", Status: restoreplan.StatusPending, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}
	fake.record = operations.OperationRecord{
		ID:               "op_restore_run01",
		Type:             operations.OperationRestoreRun,
		State:            operations.OperationStateQueued,
		Phase:            operations.OperationPhaseRestoreRunValidate,
		IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationRestoreRun, "idem_run").String(),
		IdempotencyKey:   "idem_run",
		RequestHash:      operations.RequestHash("sha256:restore-run"),
		CallerService:    "product-caller",
		CorrelationID:    "corr-run",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:      "ns_alpha01",
		RepoID:           "repo_alpha01",
		InputSummary:     map[string]any{"preview_operation_id": "op_preview01"},
		CreatedAt:        now.Add(-30 * time.Minute),
	}

	claimed, err := fake.AcquireRestoreRunOperationLease(context.Background(), "op_restore_run01", operations.LeaseRequest{Owner: "worker-a", Duration: 30 * time.Minute, Now: now})
	if err != nil {
		t.Fatalf("acquire restore run: %v", err)
	}
	if claimed.Type != operations.OperationRestoreRun || claimed.Phase != operations.OperationPhaseRestoreRunValidate {
		t.Fatalf("claimed operation = %#v", claimed)
	}

	writerFenced := claimed
	writerFenced.Phase = operations.OperationPhaseRestoreRunWriterFenced
	writerFenced.SessionFenceID = "fence_restore_run01"
	fence := fences.Fence{ID: "fence_restore_run01", RepoID: "repo_alpha01", Kind: fences.KindWriterSession, HolderOperationID: "op_restore_run01", Status: fences.StatusActive, ExpiresAt: now.Add(20 * time.Minute), CreatedAt: now, UpdatedAt: now}
	gotFence, gotOperation, err := fake.MarkRestoreRunWriterFencedWithLease(context.Background(), fence, writerFenced.SanitizedForPersistence(), "worker-a", now)
	if err != nil {
		t.Fatalf("mark writer fenced: %v", err)
	}
	if gotFence.Kind != fences.KindWriterSession || gotOperation.SessionFenceID != "fence_restore_run01" || gotOperation.Phase != operations.OperationPhaseRestoreRunWriterFenced {
		t.Fatalf("writer fence/operation = %#v/%#v", gotFence, gotOperation)
	}

	consuming := gotOperation
	consuming.Phase = operations.OperationPhaseRestoreRunConsuming
	consuming.ExternalResourceIDs = map[string]string{"restore_plan_id": "plan_001"}
	consumedPlan, consumingOperation, err := fake.MarkRestoreRunConsumingWithLease(context.Background(), consuming.SanitizedForPersistence(), "worker-a", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("mark consuming: %v", err)
	}
	if consumedPlan.Status != restoreplan.StatusConsuming || consumingOperation.Phase != operations.OperationPhaseRestoreRunConsuming {
		t.Fatalf("consuming plan/operation = %#v/%#v", consumedPlan, consumingOperation)
	}

	success := consumingOperation
	success.State = operations.OperationStateSucceeded
	success.Phase = operations.OperationPhaseRestoreRunCommitted
	success.VerificationResult = map[string]any{"restore_plan_id": "plan_001", "restore_plan_status": "consumed"}
	success.FinishedAt = &now
	event := audit.NewEvent(audit.Event{EventID: "audit-run", Type: audit.EventTypeRestoreRun, Time: now, OperationID: "op_restore_run01", CallerService: "product-caller", CorrelationID: "corr-run", AuthorizedActor: audit.Actor{Type: "system", ID: "svc-alpha"}, Resource: audit.Resource{Type: "repo", ID: "repo_alpha01", NamespaceID: "ns_alpha01"}, Outcome: audit.OutcomeSucceeded, Reason: "restore_run_committed"})

	finalPlan, finalOperation, err := fake.CommitRestoreRunSucceededWithLease(context.Background(), success.SanitizedForPersistence(), "worker-a", now.Add(2*time.Minute), event)
	if err != nil {
		t.Fatalf("success commit: %v", err)
	}
	if finalPlan.Status != restoreplan.StatusConsumed || finalOperation.State != operations.OperationStateSucceeded || fake.fence.Status != fences.StatusReleased || len(fake.auditEvents) != 1 {
		t.Fatalf("final plan/operation/fence/audit = %#v/%#v/%#v/%#v", finalPlan, finalOperation, fake.fence, fake.auditEvents)
	}
}

func TestNamespaceUpsertOperationCommitStoreContractCommitsMetadataOperationAndAuditTogether(t *testing.T) {
	fake := &fakeOperationStore{}
	var _ NamespaceUpsertOperationCommitStore = fake

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	leaseExpiresAt := now.Add(30 * time.Minute)
	fake.record = operations.OperationRecord{
		ID:             "op_namespace",
		Type:           operations.OperationNamespaceUpsert,
		State:          operations.OperationStateRunning,
		Phase:          operations.OperationPhaseNamespaceUpsertValidate,
		LeaseOwner:     "worker-a",
		LeaseExpiresAt: &leaseExpiresAt,
		NamespaceID:    "ns_alpha01",
		Resource:       operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"},
		CallerService:  "afscp-api",
		CorrelationID:  "corr-alpha",
		AuthorizedActor: operations.Actor{
			Type: "system",
			ID:   "svc-alpha",
		},
		CreatedAt:    now.Add(-time.Hour),
		InputSummary: map[string]any{"namespace_id": "ns_alpha01", "token": "input-secret-token"},
	}
	namespace := resources.Namespace{
		ID:        "ns_alpha01",
		Status:    resources.NamespaceStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	update := fake.record
	update.State = operations.OperationStateSucceeded
	update.Phase = "namespace_upsert_committed"
	event := audit.NewEvent(audit.Event{
		EventID:         "audit-namespace",
		Type:            audit.EventTypeNamespaceUpsert,
		Time:            now,
		OperationID:     "op_namespace",
		Resource:        audit.Resource{Type: "namespace", ID: "ns_alpha01", Path: "/metadata?token=audit-path-secret"},
		Outcome:         audit.OutcomeSucceeded,
		Reason:          "namespace committed token=audit-reason-secret",
		CallerService:   "afscp-api",
		CorrelationID:   "corr-alpha",
		AuthorizedActor: audit.Actor{Type: "system", ID: "svc-alpha"},
		Details:         map[string]any{"authorization": "Bearer audit-detail-secret"},
	})

	gotNamespace, gotOperation, err := fake.CommitNamespaceUpsertWithLease(context.Background(), namespace, update.SanitizedForPersistence(), "worker-a", now, event)
	if err != nil {
		t.Fatalf("commit namespace upsert with lease: %v", err)
	}
	if gotNamespace.ID != "ns_alpha01" || gotNamespace.Status != resources.NamespaceStatusActive {
		t.Fatalf("namespace = %#v, want active ns_alpha01", gotNamespace)
	}
	if gotOperation.State != operations.OperationStateSucceeded || gotOperation.LeaseOwner != "" || len(fake.auditEvents) != 1 {
		t.Fatalf("operation/audit = %#v/%#v, want terminal operation and one audit", gotOperation, fake.auditEvents)
	}
	rendered := strings.ToLower(toStoreContractString(fake.record.InputSummary) + " " + toStoreContractString(fake.auditEvents[0]))
	for _, forbidden := range []string{"input-secret-token", "audit-path-secret", "audit-reason-secret", "audit-detail-secret"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("namespace commit leaked %q in operation/audit: %#v %#v", forbidden, fake.record, fake.auditEvents[0])
		}
	}
}

func TestNamespaceUpsertOperationCommitStoreContractRejectsMismatchedOperationBeforeCommit(t *testing.T) {
	fake := &fakeOperationStore{}
	var _ NamespaceUpsertOperationCommitStore = fake

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	leaseExpiresAt := now.Add(30 * time.Minute)
	fake.record = operations.OperationRecord{
		ID:             "op_namespace",
		Type:           operations.OperationNamespaceUpsert,
		State:          operations.OperationStateRunning,
		LeaseOwner:     "worker-a",
		LeaseExpiresAt: &leaseExpiresAt,
		NamespaceID:    "ns_alpha01",
		Resource:       operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"},
		CallerService:  "afscp-api",
		CorrelationID:  "corr-alpha",
		AuthorizedActor: operations.Actor{
			Type: "system",
			ID:   "svc-alpha",
		},
		CreatedAt: now.Add(-time.Hour),
	}
	namespace := resources.Namespace{ID: "ns_alpha01", Status: resources.NamespaceStatusActive, CreatedAt: now, UpdatedAt: now}
	update := fake.record
	update.Type = operations.OperationRepoCreate
	update.State = operations.OperationStateSucceeded

	_, _, err := fake.CommitNamespaceUpsertWithLease(context.Background(), namespace, update.SanitizedForPersistence(), "worker-a", now, audit.NewEvent(audit.Event{
		EventID:         "audit-namespace",
		Type:            audit.EventTypeNamespaceUpsert,
		Time:            now,
		OperationID:     "op_namespace",
		Resource:        audit.Resource{Type: "namespace", ID: "ns_alpha01"},
		Outcome:         audit.OutcomeSucceeded,
		CallerService:   "afscp-api",
		CorrelationID:   "corr-alpha",
		AuthorizedActor: audit.Actor{Type: "system", ID: "svc-alpha"},
	}))
	if err == nil {
		t.Fatal("CommitNamespaceUpsertWithLease succeeded for non-namespace operation, want error")
	}
	if len(fake.auditEvents) != 0 {
		t.Fatalf("audit events = %#v, want none for rejected request", fake.auditEvents)
	}

	fake.record.Type = operations.OperationRepoCreate
	update = operations.OperationRecord{
		ID:              "op_namespace",
		Type:            operations.OperationNamespaceUpsert,
		State:           operations.OperationStateSucceeded,
		LeaseOwner:      "worker-a",
		LeaseExpiresAt:  &leaseExpiresAt,
		NamespaceID:     "ns_alpha01",
		Resource:        operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"},
		CallerService:   "afscp-api",
		CorrelationID:   "corr-alpha",
		AuthorizedActor: operations.Actor{Type: "system", ID: "svc-alpha"},
		CreatedAt:       now.Add(-time.Hour),
	}
	_, _, err = fake.CommitNamespaceUpsertWithLease(context.Background(), namespace, update.SanitizedForPersistence(), "worker-a", now, audit.NewEvent(audit.Event{
		EventID:         "audit-namespace",
		Type:            audit.EventTypeNamespaceUpsert,
		Time:            now,
		OperationID:     "op_namespace",
		Resource:        audit.Resource{Type: "namespace", ID: "ns_alpha01"},
		Outcome:         audit.OutcomeSucceeded,
		CallerService:   "afscp-api",
		CorrelationID:   "corr-alpha",
		AuthorizedActor: audit.Actor{Type: "system", ID: "svc-alpha"},
	}))
	if !errors.Is(err, operations.ErrLeaseUnavailable) {
		t.Fatalf("stored mismatch error = %v, want ErrLeaseUnavailable", err)
	}

	fake.record = update
	fake.record.ID = "op_other"
	_, _, err = fake.CommitNamespaceUpsertWithLease(context.Background(), namespace, update.SanitizedForPersistence(), "worker-a", now, audit.NewEvent(audit.Event{
		EventID:         "audit-namespace",
		Type:            audit.EventTypeNamespaceUpsert,
		Time:            now,
		OperationID:     "op_namespace",
		Resource:        audit.Resource{Type: "namespace", ID: "ns_alpha01"},
		Outcome:         audit.OutcomeSucceeded,
		CallerService:   "afscp-api",
		CorrelationID:   "corr-alpha",
		AuthorizedActor: audit.Actor{Type: "system", ID: "svc-alpha"},
	}))
	if !errors.Is(err, operations.ErrLeaseUnavailable) {
		t.Fatalf("stored id mismatch error = %v, want ErrLeaseUnavailable", err)
	}
}

func TestOperationRecoveryReaderContractListsReadOnlyCandidates(t *testing.T) {
	fake := &fakeOperationStore{}
	var _ OperationRecoveryReader = fake

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	expiredLease := now.Add(-time.Minute)
	liveLease := now.Add(time.Minute)
	fake.operations = []operations.OperationRecord{
		{ID: "op_queued", State: operations.OperationStateQueued, CreatedAt: now.Add(-4 * time.Minute)},
		{ID: "op_running_expired", State: operations.OperationStateRunning, LeaseOwner: "worker-a", LeaseExpiresAt: &expiredLease, CreatedAt: now.Add(-3 * time.Minute)},
		{ID: "op_cancel_expired", State: operations.OperationStateCancelRequested, LeaseOwner: "worker-a", LeaseExpiresAt: &expiredLease, CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "op_running_live", State: operations.OperationStateRunning, LeaseOwner: "worker-a", LeaseExpiresAt: &liveLease, CreatedAt: now.Add(-time.Minute)},
		{ID: "op_succeeded", State: operations.OperationStateSucceeded, CreatedAt: now},
	}

	candidates, err := fake.ListOperationsForRecovery(context.Background(), now, 10)
	if err != nil {
		t.Fatalf("list operations for recovery: %v", err)
	}
	got := operationIDs(candidates)
	want := []string{"op_queued", "op_running_expired", "op_cancel_expired"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
}

func TestIdempotencyStoreContractRequiresAtomicCreateOrReuseBoundary(t *testing.T) {
	fake := &fakeIdempotencyStore{}
	var _ IdempotencyStore = fake

	scope := operations.NewIdempotencyScope("afscp-api", "ns_alpha", operations.OperationRepoCreate, "client-key-1")
	spec := operations.QueuedOperationSpec{
		OperationID:     "op_alpha",
		Scope:           scope,
		RequestHash:     operations.RequestHash("sha256:abc"),
		Phase:           "allocate_repo_path",
		CorrelationID:   "corr-1",
		CallerService:   "afscp-api",
		AuthorizedActor: operations.Actor{Type: "system", ID: "svc-1"},
		Resource:        operations.ResourceRef{Type: "repo", ID: "repo_project"},
		NamespaceID:     "ns_alpha",
		RepoID:          "repo_project",
		CreatedAt:       time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC),
	}

	resolution, err := fake.CreateOrReuseOperation(context.Background(), spec)
	if err != nil {
		t.Fatalf("create or reuse operation: %v", err)
	}
	if resolution.Existing || resolution.Reused {
		t.Fatalf("new operation should not be reported as reused: %#v", resolution)
	}
	if resolution.Operation.ID != "op_alpha" {
		t.Fatalf("operation ID = %q, want op_alpha", resolution.Operation.ID)
	}
	if got, want := fake.constraintKey, scope.ConstraintKey(); got != want {
		t.Fatalf("constraint key = %#v, want %#v", got, want)
	}
}

func TestRepoFenceStoreContractCoversDurableReadCreateReleaseBoundary(t *testing.T) {
	fake := &fakeRepoFenceStore{}

	var _ RepoFenceReader = fake
	var _ RepoFenceWriter = fake
	var _ RepoFenceStore = fake

	fence := fences.Fence{
		ID:                "fence_alpha",
		RepoID:            "repo_alpha",
		Kind:              fences.KindWriterSession,
		HolderOperationID: "op_alpha",
		Status:            fences.StatusActive,
		ExpiresAt:         time.Date(2026, 5, 5, 12, 30, 0, 0, time.UTC),
		CreatedAt:         time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC),
	}

	if err := fake.CreateRepoFence(context.Background(), fence); err != nil {
		t.Fatalf("create repo fence: %v", err)
	}
	held, err := fake.ListHeldRepoFences(context.Background(), "repo_alpha")
	if err != nil {
		t.Fatalf("list held repo fences: %v", err)
	}
	if len(held) != 1 || held[0].ID != "fence_alpha" {
		t.Fatalf("held fences = %#v, want fence_alpha", held)
	}
	if err := fake.ReleaseRepoFence(context.Background(), "repo_alpha", "fence_alpha"); err != nil {
		t.Fatalf("release repo fence: %v", err)
	}
	held, err = fake.ListHeldRepoFences(context.Background(), "repo_alpha")
	if err != nil {
		t.Fatalf("list after release: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("held fences after release = %#v, want none", held)
	}
}

func TestRepoRecoveryInspectionReaderContractIsReadOnly(t *testing.T) {
	fake := &fakeRepoRecoveryInspectionReader{
		repos: []resources.Repo{
			{
				ID:                  "repo_alpha01",
				NamespaceID:         "ns_alpha01",
				VolumeID:            "vol_shared01",
				JVSRepoID:           "jvs-alpha",
				Kind:                resources.RepoKindRepo,
				Status:              resources.RepoStatusArchiving,
				ControlVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/control",
				PayloadVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/payload",
				Lifecycle: resources.RepoLifecycle{
					Status:                   resources.RepoStatusArchiving,
					LastLifecycleOperationID: "op_archive01",
				},
				CreatedAt: time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC),
			},
		},
		fences: []fences.Fence{
			{
				ID:                "fence_alpha",
				RepoID:            "repo_alpha01",
				Kind:              fences.KindLifecycle,
				HolderOperationID: "op_archive01",
				Status:            fences.StatusActive,
				ExpiresAt:         time.Date(2026, 5, 5, 12, 30, 0, 0, time.UTC),
				CreatedAt:         time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC),
				UpdatedAt:         time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC),
			},
		},
	}
	var _ RepoRecoveryInspectionReader = fake

	repo, err := fake.GetRepo(context.Background(), "repo_alpha01")
	if err != nil {
		t.Fatalf("get repo through inspection reader: %v", err)
	}
	if repo.ID != "repo_alpha01" {
		t.Fatalf("repo = %#v, want repo_alpha01", repo)
	}

	repos, err := fake.ListReposForRecoveryInspection(context.Background())
	if err != nil {
		t.Fatalf("list repos for recovery inspection: %v", err)
	}
	if len(repos) != 1 || repos[0].Status != resources.RepoStatusArchiving {
		t.Fatalf("candidate repos = %#v, want archiving repo", repos)
	}

	held, err := fake.ListAllHeldRepoFences(context.Background())
	if err != nil {
		t.Fatalf("list all held repo fences: %v", err)
	}
	if len(held) != 1 || held[0].Kind != fences.KindLifecycle {
		t.Fatalf("held fences = %#v, want lifecycle fence", held)
	}
}

func TestResourceStoresContractCoverControlPlaneMetadataOnly(t *testing.T) {
	fake := &fakeResourceStore{}

	var _ VolumeStore = fake
	var _ NamespaceStore = fake
	var _ NamespaceVolumeBindingStore = fake
	var _ RepoReader = fake
	var _ RepoWriter = fake
	var _ RepoStore = fake

	now := time.Date(2026, 5, 5, 14, 0, 0, 0, time.UTC)
	volume := resources.Volume{
		ID:             "vol_shared01",
		Backend:        resources.VolumeBackendJuiceFS,
		IsolationClass: resources.VolumeIsolationShared,
		Status:         resources.VolumeStatusActive,
		Capabilities: map[string]any{
			"webdav_export":             true,
			"workload_mount":            true,
			"jvs_external_control_root": true,
			"directory_quota":           false,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := fake.UpsertVolume(context.Background(), volume); err != nil {
		t.Fatalf("upsert volume: %v", err)
	}
	volumes, err := fake.ListActiveVolumes(context.Background())
	if err != nil {
		t.Fatalf("list active volumes: %v", err)
	}
	if len(volumes) != 1 || volumes[0].ID != "vol_shared01" {
		t.Fatalf("active volumes = %#v, want vol_shared01", volumes)
	}

	namespace := resources.Namespace{ID: "ns_alpha01", Status: resources.NamespaceStatusActive, CreatedAt: now, UpdatedAt: now}
	if err := fake.UpsertNamespace(context.Background(), namespace); err != nil {
		t.Fatalf("upsert namespace: %v", err)
	}
	disabled, err := fake.DisableNamespace(context.Background(), "ns_alpha01", "test hold")
	if err != nil {
		t.Fatalf("disable namespace: %v", err)
	}
	if disabled.Status != resources.NamespaceStatusDisabled || disabled.DisabledAt == nil {
		t.Fatalf("disabled namespace = %#v", disabled)
	}

	binding := resources.NamespaceVolumeBinding{
		NamespaceID:       "ns_alpha01",
		DefaultVolumeID:   "vol_shared01",
		AllowedCallers:    []resources.AllowedCaller{{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin}}},
		ExportPolicy:      map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600)},
		LifecyclePolicy:   map[string]any{"tombstone_retention_seconds": float64(604800), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
		MountPolicy:       map[string]any{"workload_mount_enabled": true, "workload_mount_requires_jvs_external_control_root": true, "allow_privileged_workload": false},
		TemplatePolicy:    map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
		Status:            resources.NamespaceStatusActive,
		QuotaBytesDefault: 0,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := fake.PutNamespaceVolumeBinding(context.Background(), binding); err != nil {
		t.Fatalf("put binding: %v", err)
	}
	gotBinding, err := fake.GetNamespaceVolumeBinding(context.Background(), "ns_alpha01")
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if gotBinding.DefaultVolumeID != "vol_shared01" || gotBinding.AllowedCallers[0].Roles[0] != resources.CallerRoleRepoAdmin {
		t.Fatalf("binding = %#v, want default volume and caller role", gotBinding)
	}

	repo := resources.Repo{
		ID:                  "repo_alpha01",
		NamespaceID:         "ns_alpha01",
		VolumeID:            "vol_shared01",
		JVSRepoID:           "jvs-alpha",
		Kind:                resources.RepoKindRepo,
		Status:              resources.RepoStatusActive,
		ControlVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/control",
		PayloadVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/payload",
		Lifecycle:           resources.RepoLifecycle{Status: resources.RepoStatusActive},
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := fake.CreateRepo(context.Background(), repo); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	repos, err := fake.ListReposByNamespace(context.Background(), "ns_alpha01")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].VolumeID != "vol_shared01" || repos[0].JVSRepoID != "jvs-alpha" {
		t.Fatalf("repos = %#v, want recorded identities", repos)
	}
	updated, err := fake.UpdateRepoLifecycle(context.Background(), "repo_alpha01", resources.RepoLifecycle{
		Status:                   resources.RepoStatusTombstoned,
		RetentionExpiresAt:       ptrTimeForStoreContract(now.Add(24 * time.Hour)),
		LastLifecycleOperationID: "op_delete01",
		PreDeleteStatus:          resources.RepoStatusActive,
	})
	if err != nil {
		t.Fatalf("update repo lifecycle: %v", err)
	}
	if updated.ID != "repo_alpha01" || updated.Status != resources.RepoStatusTombstoned || updated.VolumeID != "vol_shared01" {
		t.Fatalf("updated repo = %#v, want immutable ids and tombstoned status", updated)
	}
}

func TestOperationWriterContractAcceptsOnlySanitizedPersistenceRecords(t *testing.T) {
	fake := &fakeOperationStore{}
	scope := operations.NewIdempotencyScope("afscp-api", "ns_alpha", operations.OperationExportCreate, "client-key-1")
	record, err := operations.NewQueuedOperationRecord(operations.QueuedOperationSpec{
		OperationID:         "op_safe_write",
		Scope:               scope,
		RequestHash:         operations.RequestHash("sha256:safe-write"),
		Phase:               "queued",
		CorrelationID:       "corr-safe-write",
		CallerService:       "afscp-api",
		AuthorizedActor:     operations.Actor{Type: "system", ID: "svc-1"},
		Resource:            operations.ResourceRef{Type: "export", ID: "export_project"},
		NamespaceID:         "ns_alpha",
		ExportID:            "export_project",
		ExternalResourceIDs: map[string]string{"jvs_repo_id": "jvs-store-secret"},
		InputSummary: map[string]any{
			"command": "export --token store-token-secret",
		},
		CreatedAt: time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("new queued operation record: %v", err)
	}
	if !strings.Contains(record.InputSummary["command"].(string), "store-token-secret") {
		t.Fatalf("test setup lost raw secret: %#v", record.InputSummary)
	}

	if err := fake.CreateOperation(context.Background(), record.SanitizedForPersistence()); err != nil {
		t.Fatalf("create operation: %v", err)
	}

	if got := fake.record.InputSummary["command"].(string); strings.Contains(got, "store-token-secret") {
		t.Fatalf("store write received unsanitized input summary: %q", got)
	}
	if got := fake.record.ExternalResourceIDs["jvs_repo_id"]; got != observability.Redacted {
		t.Fatalf("external resource ID = %q, want redacted", got)
	}
	if !fake.record.Redaction.Redacted {
		t.Fatalf("store write record missing redaction report: %#v", fake.record.Redaction)
	}
}

func TestAuditSinkContractAcceptsAppendOnlyAuditEvents(t *testing.T) {
	fake := &fakeAuditSink{}
	var _ AuditSink = fake

	event := audit.NewEvent(audit.Event{
		EventID:         "audit-1",
		Type:            audit.EventTypeExportCreate,
		Time:            time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC),
		CallerService:   "afscp-api",
		AuthorizedActor: audit.Actor{Type: "system", ID: "svc-1"},
		CorrelationID:   "corr-1",
		OperationID:     "op_alpha",
		Resource:        audit.Resource{Type: "repo", ID: "repo_project", Path: "/payload --token audit-path-token"},
		Outcome:         audit.OutcomeSucceeded,
		Reason:          "operation queued token=audit-reason-token",
		Details:         map[string]any{"message": "Authorization: Bearer audit-detail-token"},
	})

	if err := fake.AppendAuditEvent(context.Background(), event); err != nil {
		t.Fatalf("append audit event: %v", err)
	}
	if len(fake.events) != 1 || fake.events[0].EventID != "audit-1" {
		t.Fatalf("events = %#v, want appended audit-1", fake.events)
	}
	rendered := strings.ToLower(fake.events[0].Reason + " " + fake.events[0].Resource.Path + " " + fake.events[0].Details["message"].(string))
	for _, forbidden := range []string{"audit-path-token", "audit-reason-token", "audit-detail-token"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("audit sink event leaked %q in %#v", forbidden, fake.events[0])
		}
	}
}

func TestAuditOutboxDeliveryStoreContractCoversDBOnlyStateAdapter(t *testing.T) {
	fake := &fakeAuditOutboxDeliveryStore{}
	var _ AuditOutboxDeliveryStore = fake

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := audit.OutboxRecord{
		EventID:         "audit-1",
		EventType:       audit.EventTypeRepoCreate,
		EventTime:       now.Add(-time.Minute),
		PayloadJSON:     []byte(`{"event_id":"audit-1"}`),
		Status:          audit.OutboxStatusPending,
		DeliveryAttempt: 0,
		CreatedAt:       now.Add(-time.Minute),
		UpdatedAt:       now.Add(-time.Minute),
	}
	fake.records = []audit.OutboxRecord{record}

	due, err := fake.ListDueAuditOutboxRecords(context.Background(), now, 10)
	if err != nil {
		t.Fatalf("list due audit outbox records: %v", err)
	}
	if len(due) != 1 || due[0].EventID != "audit-1" {
		t.Fatalf("due records = %#v, want audit-1", due)
	}

	claimed, err := fake.ClaimDueAuditOutboxRecords(context.Background(), "deliverer-1", now, 10)
	if err != nil {
		t.Fatalf("claim due audit outbox records: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Status != audit.OutboxStatusDelivering || claimed[0].DeliveryAttempt != 1 {
		t.Fatalf("claimed records = %#v, want delivering attempt 1", claimed)
	}
	if fake.owner != "deliverer-1" {
		t.Fatalf("owner = %q, want validated owner", fake.owner)
	}

	recovered, err := fake.RecoverStaleAuditOutboxRecords(context.Background(), "deliverer-1", 30*time.Second, 10, audit.DeliveryFailure{
		MaxAttempts: 3,
		Backoff:     time.Minute,
		LastError:   "stale token=contract-secret",
		Now:         now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("recover stale audit outbox records: %v", err)
	}
	if len(recovered) != 1 || recovered[0].Status != audit.OutboxStatusRetryWait || strings.Contains(recovered[0].LastError, "contract-secret") {
		t.Fatalf("recovered records = %#v, want retry_wait with redacted error", recovered)
	}

	fake.records[0].Status = audit.OutboxStatusDelivering
	fake.records[0].UpdatedAt = now
	fake.records[0].LastError = ""
	if err := fake.MarkAuditOutboxDelivered(context.Background(), "audit-1", now.Add(time.Minute)); err != nil {
		t.Fatalf("mark audit outbox delivered: %v", err)
	}
	if fake.records[0].Status != audit.OutboxStatusDelivered || fake.records[0].DeliveredAt == nil {
		t.Fatalf("delivered record = %#v", fake.records[0])
	}

	fake.records[0].Status = audit.OutboxStatusDelivering
	fake.records[0].DeliveredAt = nil
	if err := fake.MarkAuditOutboxDeliveryFailed(context.Background(), "audit-1", audit.DeliveryFailure{
		MaxAttempts: 2,
		Backoff:     time.Minute,
		LastError:   "token=contract-secret",
		Now:         now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("mark audit outbox delivery failed: %v", err)
	}
	if fake.records[0].Status != audit.OutboxStatusRetryWait || strings.Contains(fake.records[0].LastError, "contract-secret") {
		t.Fatalf("failed record = %#v, want retry_wait with redacted error", fake.records[0])
	}
}

type fakeOperationStore struct {
	record      operations.OperationRecord
	operations  []operations.OperationRecord
	auditEvents []audit.Event
}

type fakeExportAccessStore struct {
	session exportaccess.Session
}

func (fake *fakeExportAccessStore) CreateOrReuseExport(_ context.Context, request exportaccess.CreateRequest) (exportaccess.CreateResult, error) {
	fake.session = request.Session
	return exportaccess.CreateResult{Session: fake.session, Operation: request.Operation}, nil
}

func (fake *fakeExportAccessStore) GetExportSession(_ context.Context, exportID string) (exportaccess.Session, error) {
	session := fake.session
	session.ID = exportID
	return session, nil
}

func (fake *fakeExportAccessStore) RevokeExport(_ context.Context, request exportaccess.RevokeRequest) (exportaccess.RevokeResult, error) {
	fake.session.ID = request.ExportID
	fake.session.NamespaceID = request.NamespaceID
	fake.session.Status = sessionstate.ExportStatusRevoking
	return exportaccess.RevokeResult{Session: fake.session, Operation: request.Operation}, nil
}

func (fake *fakeExportAccessStore) GetExportGatewayCredential(_ context.Context, exportID string) (exportaccess.GatewayCredential, error) {
	session := fake.session
	session.ID = exportID
	return exportaccess.GatewayCredential{Session: session}, nil
}

func (fake *fakeExportAccessStore) RecordExportAccess(_ context.Context, _ string, _ time.Time) error {
	return nil
}

func (fake *fakeExportAccessStore) RecordExportRuntimeObservation(_ context.Context, observation exportaccess.RuntimeObservation) (exportaccess.Session, error) {
	fake.session.ID = observation.ExportID
	fake.session.ActiveRequestCount += observation.ActiveRequestDelta
	fake.session.ActiveWriteCount += observation.ActiveWriteDelta
	return fake.session, nil
}

func (fake *fakeExportAccessStore) ListExportSessionsForTerminalReconcile(_ context.Context, _ time.Time, _ int) ([]exportaccess.Session, error) {
	if fake.session.ID == "" {
		return nil, nil
	}
	return []exportaccess.Session{fake.session}, nil
}

func (fake *fakeExportAccessStore) ReconcileExportSessionTerminal(_ context.Context, request exportaccess.ReconcileRequest) (exportaccess.ReconcileResult, error) {
	fake.session.ID = request.ExportID
	fake.session.NamespaceID = request.NamespaceID
	fake.session.Status = request.TargetStatus
	fake.session.ActiveRequestCount = request.ActiveRequestCount
	fake.session.ActiveWriteCount = request.ActiveWriteCount
	fake.session.TerminalObservedAt = &request.ObservedAt
	return exportaccess.ReconcileResult{Session: fake.session, Operation: request.Operation}, nil
}

func (fake *fakeOperationStore) GetOperation(_ context.Context, operationID string) (operations.OperationRecord, error) {
	if fake.record.ID != operationID {
		return operations.OperationRecord{}, nil
	}
	return fake.record, nil
}

func (fake *fakeOperationStore) CreateOperation(_ context.Context, record operations.SanitizedOperationRecord) error {
	fake.record = record.Record()
	return nil
}

func (fake *fakeOperationStore) UpdateOperation(_ context.Context, record operations.SanitizedOperationRecord) error {
	fake.record = record.Record()
	return nil
}

func (fake *fakeOperationStore) AcquireOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	if fake.record.ID != operationID {
		return operations.OperationRecord{}, nil
	}
	decision := operations.AcquireLease(fake.record, request)
	if !decision.Allowed {
		return operations.OperationRecord{}, decision.Error
	}
	fake.record = decision.Record
	return fake.record, nil
}

func (fake *fakeOperationStore) RenewOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	if fake.record.ID != operationID {
		return operations.OperationRecord{}, nil
	}
	decision := operations.RenewLease(fake.record, request)
	if !decision.Allowed {
		return operations.OperationRecord{}, decision.Error
	}
	fake.record = decision.Record
	return fake.record, nil
}

func (fake *fakeOperationStore) UpdateOperationWithLease(_ context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time) (operations.OperationRecord, error) {
	if fake.record.ID != record.Record().ID {
		return operations.OperationRecord{}, nil
	}
	if fake.record.State != operations.OperationStateRunning || fake.record.LeaseOwner != strings.TrimSpace(owner) || fake.record.LeaseExpiresAt == nil || !fake.record.LeaseExpiresAt.After(now) {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	updated := record.Record()
	updated.Attempt = fake.record.Attempt
	if updated.State == operations.OperationStateRunning {
		updated.LeaseOwner = fake.record.LeaseOwner
		updated.LeaseExpiresAt = fake.record.LeaseExpiresAt
	} else {
		updated.LeaseOwner = ""
		updated.LeaseExpiresAt = nil
	}
	fake.record = updated.SanitizedForPersistence().Record()
	return fake.record, nil
}

func (fake *fakeOperationStore) CommitOperationWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	if strings.TrimSpace(event.OperationID) == "" || event.OperationID != record.Record().ID {
		return operations.OperationRecord{}, audit.ErrInvalidOutboxRequest
	}
	updated, err := fake.UpdateOperationWithLease(ctx, record, owner, now)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	fake.auditEvents = append(fake.auditEvents, event.Sanitized())
	return updated, nil
}

func (fake *fakeOperationStore) CommitNamespaceUpsertWithLease(ctx context.Context, namespace resources.Namespace, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Namespace, operations.OperationRecord, error) {
	if err := namespace.Validate(); err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, err
	}
	operation := record.Record()
	if namespace.Status != resources.NamespaceStatusActive ||
		operation.Type != operations.OperationNamespaceUpsert ||
		operation.State != operations.OperationStateSucceeded ||
		operation.NamespaceID == "" ||
		operation.NamespaceID != namespace.ID ||
		operation.Resource.Type != "namespace" ||
		operation.Resource.ID == "" ||
		operation.Resource.ID != namespace.ID ||
		event.Outcome != audit.OutcomeSucceeded ||
		event.Resource.Type != "namespace" ||
		event.Resource.ID != namespace.ID ||
		(strings.TrimSpace(event.Resource.NamespaceID) != "" && event.Resource.NamespaceID != namespace.ID) ||
		event.CallerService != operation.CallerService ||
		event.CorrelationID != operation.CorrelationID ||
		event.AuthorizedActor.Type != operation.AuthorizedActor.Type ||
		event.AuthorizedActor.ID != operation.AuthorizedActor.ID {
		return resources.Namespace{}, operations.OperationRecord{}, operations.ErrInvalidLeaseRequest
	}
	if fake.record.ID != operation.ID ||
		fake.record.Type != operations.OperationNamespaceUpsert ||
		fake.record.NamespaceID != namespace.ID ||
		fake.record.Resource.Type != "namespace" ||
		fake.record.Resource.ID != namespace.ID {
		return resources.Namespace{}, operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	updated, err := fake.CommitOperationWithLease(ctx, record, owner, now, event)
	if err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, err
	}
	return namespace, updated, nil
}

func (fake *fakeOperationStore) ListOperationsForRecovery(_ context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	var out []operations.OperationRecord
	for _, record := range fake.operations {
		if len(out) >= limit {
			break
		}
		switch record.State {
		case operations.OperationStateQueued, operations.OperationStateOperatorInterventionRequired:
			out = append(out, record.Sanitized())
		case operations.OperationStateRunning:
			if record.LeaseExpiresAt == nil || strings.TrimSpace(record.LeaseOwner) == "" || !record.LeaseExpiresAt.After(now) {
				out = append(out, record.Sanitized())
			}
		case operations.OperationStateCancelRequested:
			if record.LeaseExpiresAt == nil || !record.LeaseExpiresAt.After(now) {
				out = append(out, record.Sanitized())
			}
		}
	}
	return out, nil
}

func operationIDs(records []operations.OperationRecord) []string {
	out := make([]string, len(records))
	for idx, record := range records {
		out[idx] = record.ID
	}
	return out
}

type fakeIdempotencyStore struct {
	constraintKey operations.IdempotencyConstraintKey
}

func (fake *fakeIdempotencyStore) CreateOrReuseOperation(_ context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	fake.constraintKey = spec.Scope.ConstraintKey()

	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}

	return operations.IdempotencyResolution{
		Operation: record.SanitizedForPersistence().Record(),
	}, nil
}

type fakeAuditSink struct {
	events []audit.Event
}

func (fake *fakeAuditSink) AppendAuditEvent(_ context.Context, event audit.Event) error {
	fake.events = append(fake.events, event)
	return nil
}

type fakeAuditOutboxDeliveryStore struct {
	records []audit.OutboxRecord
	owner   string
}

func (fake *fakeAuditOutboxDeliveryStore) ListDueAuditOutboxRecords(_ context.Context, now time.Time, limit int) ([]audit.OutboxRecord, error) {
	var due []audit.OutboxRecord
	for _, record := range fake.records {
		if len(due) >= limit {
			break
		}
		if record.Status == audit.OutboxStatusPending ||
			(record.Status == audit.OutboxStatusRetryWait && record.NextRetryAt != nil && !record.NextRetryAt.After(now)) {
			due = append(due, record)
		}
	}
	return due, nil
}

func (fake *fakeAuditOutboxDeliveryStore) ClaimDueAuditOutboxRecords(ctx context.Context, owner string, now time.Time, limit int) ([]audit.OutboxRecord, error) {
	fake.owner = strings.TrimSpace(owner)
	due, err := fake.ListDueAuditOutboxRecords(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	for idx := range fake.records {
		for dueIdx := range due {
			if fake.records[idx].EventID == due[dueIdx].EventID {
				updated, err := audit.MarkDelivering(fake.records[idx], fake.owner, now)
				if err != nil {
					return nil, err
				}
				fake.records[idx] = updated
				due[dueIdx] = updated
			}
		}
	}
	return due, nil
}

func (fake *fakeAuditOutboxDeliveryStore) RecoverStaleAuditOutboxRecords(_ context.Context, owner string, staleThreshold time.Duration, limit int, failure audit.DeliveryFailure) ([]audit.OutboxRecord, error) {
	fake.owner = strings.TrimSpace(owner)
	if fake.owner == "" || staleThreshold <= 0 || limit <= 0 {
		return nil, audit.ErrInvalidOutboxRequest
	}
	staleBefore := failure.Now.Add(-staleThreshold)
	var recovered []audit.OutboxRecord
	for idx := range fake.records {
		if len(recovered) >= limit {
			break
		}
		if fake.records[idx].Status != audit.OutboxStatusDelivering || fake.records[idx].UpdatedAt.After(staleBefore) {
			continue
		}
		updated, err := audit.MarkDeliveryFailed(fake.records[idx], failure)
		if err != nil {
			return nil, err
		}
		fake.records[idx] = updated
		recovered = append(recovered, updated)
	}
	return recovered, nil
}

func (fake *fakeAuditOutboxDeliveryStore) MarkAuditOutboxDelivered(_ context.Context, eventID string, now time.Time) error {
	for idx := range fake.records {
		if fake.records[idx].EventID == eventID {
			updated, err := audit.MarkDelivered(fake.records[idx], now)
			if err != nil {
				return err
			}
			fake.records[idx] = updated
		}
	}
	return nil
}

func (fake *fakeAuditOutboxDeliveryStore) MarkAuditOutboxDeliveryFailed(_ context.Context, eventID string, failure audit.DeliveryFailure) error {
	for idx := range fake.records {
		if fake.records[idx].EventID == eventID {
			updated, err := audit.MarkDeliveryFailed(fake.records[idx], failure)
			if err != nil {
				return err
			}
			fake.records[idx] = updated
		}
	}
	return nil
}

type fakeRepoFenceStore struct {
	fences []fences.Fence
}

func (fake *fakeRepoFenceStore) ListHeldRepoFences(_ context.Context, repoID string) ([]fences.Fence, error) {
	var held []fences.Fence
	for _, fence := range fake.fences {
		if fence.RepoID == repoID && fence.Held() {
			held = append(held, fence)
		}
	}
	return held, nil
}

func (fake *fakeRepoFenceStore) CreateRepoFence(_ context.Context, fence fences.Fence) error {
	fake.fences = append(fake.fences, fence)
	return nil
}

func (fake *fakeRepoFenceStore) ReleaseRepoFence(_ context.Context, repoID, fenceID string) error {
	now := time.Date(2026, 5, 5, 13, 0, 0, 0, time.UTC)
	for idx := range fake.fences {
		if fake.fences[idx].RepoID == repoID && fake.fences[idx].ID == fenceID && fake.fences[idx].Held() {
			fake.fences[idx].Status = fences.StatusReleased
			fake.fences[idx].ReleasedAt = &now
			fake.fences[idx].UpdatedAt = now
		}
	}
	return nil
}

type fakeRepoRecoveryInspectionReader struct {
	repos  []resources.Repo
	fences []fences.Fence
}

func (fake *fakeRepoRecoveryInspectionReader) GetRepo(_ context.Context, repoID string) (resources.Repo, error) {
	for _, repo := range fake.repos {
		if repo.ID == repoID {
			return repo, nil
		}
	}
	return resources.Repo{}, nil
}

func (fake *fakeRepoRecoveryInspectionReader) ListReposForRecoveryInspection(_ context.Context) ([]resources.Repo, error) {
	out := make([]resources.Repo, len(fake.repos))
	copy(out, fake.repos)
	return out, nil
}

func (fake *fakeRepoRecoveryInspectionReader) ListAllHeldRepoFences(_ context.Context) ([]fences.Fence, error) {
	out := make([]fences.Fence, len(fake.fences))
	copy(out, fake.fences)
	return out, nil
}

type fakeRestorePlanStore struct {
	plans map[string]restoreplan.Plan
}

func (fake *fakeRestorePlanStore) CreatePendingRestorePlan(_ context.Context, plan restoreplan.Plan) error {
	if plan.Status != restoreplan.StatusPending {
		return errors.New("restore plan must be pending")
	}
	if err := plan.Validate(); err != nil {
		return err
	}
	if fake.plans == nil {
		fake.plans = map[string]restoreplan.Plan{}
	}
	fake.plans[plan.ID] = plan
	return nil
}

func (fake *fakeRestorePlanStore) GetRestorePlanByPreviewOperation(_ context.Context, previewOperationID string) (restoreplan.Plan, error) {
	for _, plan := range fake.plans {
		if plan.PreviewOperationID == previewOperationID {
			return plan, nil
		}
	}
	return restoreplan.Plan{}, errors.New("restore plan not found")
}

func (fake *fakeRestorePlanStore) GetActiveRestorePlanByRepo(_ context.Context, repoID string) (restoreplan.Plan, error) {
	for _, plan := range fake.plans {
		if plan.RepoID == repoID && plan.Active() {
			return plan, nil
		}
	}
	return restoreplan.Plan{}, errors.New("active restore plan not found")
}

func (fake *fakeRestorePlanStore) TransitionRestorePlanStatus(_ context.Context, restorePlanID string, from, to restoreplan.Status, now time.Time) (restoreplan.Plan, error) {
	plan, ok := fake.plans[restorePlanID]
	if !ok || plan.Status != from {
		return restoreplan.Plan{}, errors.New("restore plan transition conflict")
	}
	if !restoreplan.ValidTransition(from, to) {
		return restoreplan.Plan{}, errors.New("invalid restore plan transition")
	}
	plan.Status = to
	plan.UpdatedAt = now
	fake.plans[restorePlanID] = plan
	return plan, nil
}

type fakeRestorePreviewOperationStore struct {
	fakeRestorePlanStore
	record      operations.OperationRecord
	auditEvents []audit.Event
}

func (fake *fakeRestorePreviewOperationStore) ListRestorePreviewOperationsForRecovery(_ context.Context, _ time.Time, _ int) ([]operations.OperationRecord, error) {
	return []operations.OperationRecord{fake.record.Sanitized()}, nil
}

func (fake *fakeRestorePreviewOperationStore) AcquireRestorePreviewOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	if fake.record.ID != operationID || fake.record.Type != operations.OperationRestorePreview {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	decision := operations.AcquireLease(fake.record, request)
	if !decision.Allowed {
		return operations.OperationRecord{}, decision.Error
	}
	fake.record = decision.Record
	return fake.record, nil
}

func (fake *fakeRestorePreviewOperationStore) UpdateRestorePreviewPreflightWithLease(_ context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time) (operations.OperationRecord, error) {
	update := record.Record()
	if fake.record.ID != update.ID || fake.record.State != operations.OperationStateRunning || fake.record.Phase != operations.OperationPhaseRestorePreviewValidate || fake.record.LeaseOwner != strings.TrimSpace(owner) || fake.record.LeaseExpiresAt == nil || !fake.record.LeaseExpiresAt.After(now) {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if !restorePreviewContractPreflightMarker(update) {
		return operations.OperationRecord{}, operations.ErrInvalidLeaseRequest
	}
	update.LeaseOwner = fake.record.LeaseOwner
	update.LeaseExpiresAt = fake.record.LeaseExpiresAt
	fake.record = update
	return fake.record, nil
}

func (fake *fakeRestorePreviewOperationStore) CommitRestorePreviewSucceededWithLease(_ context.Context, plan restoreplan.Plan, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	update := record.Record()
	if fake.record.ID != update.ID ||
		fake.record.Type != operations.OperationRestorePreview ||
		fake.record.State != operations.OperationStateRunning ||
		fake.record.Phase != operations.OperationPhaseRestorePreviewPreflightIdle ||
		fake.record.LeaseOwner != strings.TrimSpace(owner) ||
		fake.record.LeaseExpiresAt == nil ||
		!fake.record.LeaseExpiresAt.After(now) ||
		update.State != operations.OperationStateSucceeded ||
		update.Phase != operations.OperationPhaseRestorePreviewCommitted ||
		plan.Status != restoreplan.StatusPending ||
		plan.PreviewOperationID != update.ID ||
		plan.NamespaceID != update.NamespaceID ||
		plan.RepoID != update.RepoID ||
		event.OperationID != update.ID ||
		event.Type != audit.EventTypeRestorePreview ||
		event.Outcome != audit.OutcomeSucceeded {
		return restoreplan.Plan{}, operations.OperationRecord{}, operations.ErrInvalidLeaseRequest
	}
	if err := plan.Validate(); err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	update.LeaseOwner = ""
	update.LeaseExpiresAt = nil
	fake.record = update
	if fake.plans == nil {
		fake.plans = map[string]restoreplan.Plan{}
	}
	fake.plans[plan.ID] = plan
	fake.auditEvents = append(fake.auditEvents, event.Sanitized())
	return plan, update, nil
}

func (fake *fakeRestorePreviewOperationStore) CommitRestorePreviewFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	update := record.Record()
	if fake.record.ID != update.ID ||
		fake.record.Type != operations.OperationRestorePreview ||
		fake.record.State != operations.OperationStateRunning ||
		(fake.record.Phase != operations.OperationPhaseRestorePreviewValidate && fake.record.Phase != operations.OperationPhaseRestorePreviewPreflightIdle) ||
		fake.record.LeaseOwner != strings.TrimSpace(owner) ||
		fake.record.LeaseExpiresAt == nil ||
		!fake.record.LeaseExpiresAt.After(now) ||
		(update.State != operations.OperationStateFailed && update.State != operations.OperationStateOperatorInterventionRequired) ||
		event.OperationID != update.ID ||
		event.Type != audit.EventTypeRestorePreview ||
		event.Outcome != audit.OutcomeFailed {
		return operations.OperationRecord{}, operations.ErrInvalidLeaseRequest
	}
	update.LeaseOwner = ""
	update.LeaseExpiresAt = nil
	fake.record = update
	fake.auditEvents = append(fake.auditEvents, event.Sanitized())
	return update, nil
}

func (fake *fakeRestorePreviewOperationStore) GetRepoInNamespace(context.Context, string, string) (resources.Repo, error) {
	return resources.Repo{}, nil
}

func (fake *fakeRestorePreviewOperationStore) GetNamespace(context.Context, string) (resources.Namespace, error) {
	return resources.Namespace{}, nil
}

func (fake *fakeRestorePreviewOperationStore) GetNamespaceVolumeBinding(context.Context, string) (resources.NamespaceVolumeBinding, error) {
	return resources.NamespaceVolumeBinding{}, nil
}

func (fake *fakeRestorePreviewOperationStore) GetVolume(context.Context, string) (resources.Volume, error) {
	return resources.Volume{}, nil
}

func (fake *fakeRestorePreviewOperationStore) ListHeldRepoFences(context.Context, string) ([]fences.Fence, error) {
	return nil, nil
}

type fakeRestoreRunOperationStore struct {
	record           operations.OperationRecord
	previewOperation operations.OperationRecord
	plan             restoreplan.Plan
	fence            fences.Fence
	auditEvents      []audit.Event
}

func (fake *fakeRestoreRunOperationStore) ListRestoreRunOperationsForRecovery(_ context.Context, _ time.Time, _ int) ([]operations.OperationRecord, error) {
	return []operations.OperationRecord{fake.record.Sanitized()}, nil
}

func (fake *fakeRestoreRunOperationStore) AcquireRestoreRunOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	if fake.record.ID != operationID || fake.record.Type != operations.OperationRestoreRun || fake.plan.PreviewOperationID != fake.record.InputSummary["preview_operation_id"] {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	decision := operations.AcquireLease(fake.record, request)
	if !decision.Allowed {
		return operations.OperationRecord{}, decision.Error
	}
	fake.record = decision.Record
	return fake.record, nil
}

func (fake *fakeRestoreRunOperationStore) MarkRestoreRunWriterFencedWithLease(_ context.Context, fence fences.Fence, record operations.SanitizedOperationRecord, owner string, now time.Time) (fences.Fence, operations.OperationRecord, error) {
	update := record.Record()
	if fake.record.ID != update.ID ||
		fake.record.Type != operations.OperationRestoreRun ||
		fake.record.Phase != operations.OperationPhaseRestoreRunValidate ||
		fake.record.LeaseOwner != strings.TrimSpace(owner) ||
		fake.record.LeaseExpiresAt == nil ||
		!fake.record.LeaseExpiresAt.After(now) ||
		update.Phase != operations.OperationPhaseRestoreRunWriterFenced ||
		update.SessionFenceID != fence.ID ||
		fence.Kind != fences.KindWriterSession ||
		fence.HolderOperationID != update.ID ||
		fence.RepoID != update.RepoID {
		return fences.Fence{}, operations.OperationRecord{}, operations.ErrInvalidLeaseRequest
	}
	fake.fence = fence
	fake.record = update
	return fence, update, nil
}

func (fake *fakeRestoreRunOperationStore) MarkRestoreRunConsumingWithLease(_ context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time) (restoreplan.Plan, operations.OperationRecord, error) {
	update := record.Record()
	if fake.record.ID != update.ID ||
		fake.record.Phase != operations.OperationPhaseRestoreRunWriterFenced ||
		fake.record.LeaseOwner != strings.TrimSpace(owner) ||
		fake.record.LeaseExpiresAt == nil ||
		!fake.record.LeaseExpiresAt.After(now) ||
		fake.fence.ID != update.SessionFenceID ||
		fake.fence.Status != fences.StatusActive ||
		fake.plan.Status != restoreplan.StatusPending ||
		fake.previewOperation.ID != update.InputSummary["preview_operation_id"] ||
		fake.previewOperation.Type != operations.OperationRestorePreview ||
		fake.previewOperation.State != operations.OperationStateSucceeded ||
		update.Phase != operations.OperationPhaseRestoreRunConsuming {
		return restoreplan.Plan{}, operations.OperationRecord{}, operations.ErrInvalidLeaseRequest
	}
	fake.plan.Status = restoreplan.StatusConsuming
	fake.plan.UpdatedAt = now
	fake.record = update
	return fake.plan, update, nil
}

func (fake *fakeRestoreRunOperationStore) CommitRestoreRunSucceededWithLease(_ context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	update := record.Record()
	if fake.record.ID != update.ID ||
		fake.record.Phase != operations.OperationPhaseRestoreRunConsuming ||
		fake.record.LeaseOwner != strings.TrimSpace(owner) ||
		fake.record.LeaseExpiresAt == nil ||
		!fake.record.LeaseExpiresAt.After(now) ||
		fake.plan.Status != restoreplan.StatusConsuming ||
		fake.fence.ID != update.SessionFenceID ||
		fake.fence.Status != fences.StatusActive ||
		update.State != operations.OperationStateSucceeded ||
		update.Phase != operations.OperationPhaseRestoreRunCommitted ||
		event.OperationID != update.ID ||
		event.Type != audit.EventTypeRestoreRun ||
		event.Outcome != audit.OutcomeSucceeded {
		return restoreplan.Plan{}, operations.OperationRecord{}, operations.ErrInvalidLeaseRequest
	}
	fake.plan.Status = restoreplan.StatusConsumed
	fake.plan.UpdatedAt = now
	fake.fence.Status = fences.StatusReleased
	fake.fence.ReleasedAt = &now
	fake.fence.UpdatedAt = now
	update.LeaseOwner = ""
	update.LeaseExpiresAt = nil
	fake.record = update
	fake.auditEvents = append(fake.auditEvents, event.Sanitized())
	return fake.plan, update, nil
}

func (fake *fakeRestoreRunOperationStore) CommitRestoreRunFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	update := record.Record()
	if fake.record.ID != update.ID ||
		fake.record.LeaseOwner != strings.TrimSpace(owner) ||
		fake.record.LeaseExpiresAt == nil ||
		!fake.record.LeaseExpiresAt.After(now) ||
		(update.State != operations.OperationStateFailed && update.State != operations.OperationStateOperatorInterventionRequired) ||
		event.OperationID != update.ID ||
		event.Type != audit.EventTypeRestoreRun ||
		event.Outcome != audit.OutcomeFailed {
		return operations.OperationRecord{}, operations.ErrInvalidLeaseRequest
	}
	if fake.record.Phase == operations.OperationPhaseRestoreRunWriterFenced {
		fake.fence.Status = fences.StatusReleased
		fake.fence.ReleasedAt = &now
		fake.fence.UpdatedAt = now
	}
	if fake.record.Phase == operations.OperationPhaseRestoreRunConsuming {
		fake.plan.Status = restoreplan.StatusOperatorInterventionRequired
		fake.plan.UpdatedAt = now
	}
	update.LeaseOwner = ""
	update.LeaseExpiresAt = nil
	fake.record = update
	fake.auditEvents = append(fake.auditEvents, event.Sanitized())
	return update, nil
}

func (fake *fakeRestoreRunOperationStore) GetOperation(_ context.Context, operationID string) (operations.OperationRecord, error) {
	if fake.previewOperation.ID == operationID {
		return fake.previewOperation, nil
	}
	if fake.record.ID == operationID {
		return fake.record, nil
	}
	return operations.OperationRecord{}, errors.New("operation not found")
}

func (fake *fakeRestoreRunOperationStore) GetRestorePlanByPreviewOperation(_ context.Context, previewOperationID string) (restoreplan.Plan, error) {
	if fake.plan.PreviewOperationID == previewOperationID {
		return fake.plan, nil
	}
	return restoreplan.Plan{}, errors.New("restore plan not found")
}

func (fake *fakeRestoreRunOperationStore) GetActiveRestorePlanByRepo(_ context.Context, repoID string) (restoreplan.Plan, error) {
	if fake.plan.RepoID == repoID && fake.plan.Active() {
		return fake.plan, nil
	}
	return restoreplan.Plan{}, errors.New("active restore plan not found")
}

func (fake *fakeRestoreRunOperationStore) GetRepoInNamespace(context.Context, string, string) (resources.Repo, error) {
	return resources.Repo{}, nil
}

func (fake *fakeRestoreRunOperationStore) GetNamespace(context.Context, string) (resources.Namespace, error) {
	return resources.Namespace{}, nil
}

func (fake *fakeRestoreRunOperationStore) GetNamespaceVolumeBinding(context.Context, string) (resources.NamespaceVolumeBinding, error) {
	return resources.NamespaceVolumeBinding{}, nil
}

func (fake *fakeRestoreRunOperationStore) GetVolume(context.Context, string) (resources.Volume, error) {
	return resources.Volume{}, nil
}

func (fake *fakeRestoreRunOperationStore) ListHeldRepoFences(context.Context, string) ([]fences.Fence, error) {
	return nil, nil
}

func (fake *fakeRestoreRunOperationStore) ListExportSessionsByRepo(context.Context, string) ([]sessionstate.ExportSession, error) {
	return nil, nil
}

func (fake *fakeRestoreRunOperationStore) ListWorkloadMountBindingsByRepo(context.Context, string) ([]sessionstate.WorkloadMountBinding, error) {
	return nil, nil
}

func restorePreviewContractPreflightMarker(record operations.OperationRecord) bool {
	verification, ok := record.VerificationResult.(map[string]any)
	if !ok {
		return false
	}
	captured, _ := verification["preflight_recovery_status_captured"].(bool)
	state, _ := verification["preflight_restore_state"].(string)
	blocking, _ := verification["preflight_blocking"].(bool)
	return captured && state == "idle" && !blocking
}

type fakeResourceStore struct {
	volumes    map[string]resources.Volume
	namespaces map[string]resources.Namespace
	bindings   map[string]resources.NamespaceVolumeBinding
	repos      map[string]resources.Repo
}

func (fake *fakeResourceStore) UpsertVolume(_ context.Context, volume resources.Volume) error {
	if fake.volumes == nil {
		fake.volumes = map[string]resources.Volume{}
	}
	fake.volumes[volume.ID] = volume
	return nil
}

func (fake *fakeResourceStore) GetVolume(_ context.Context, volumeID string) (resources.Volume, error) {
	return fake.volumes[volumeID], nil
}

func (fake *fakeResourceStore) ListActiveVolumes(_ context.Context) ([]resources.Volume, error) {
	var out []resources.Volume
	for _, volume := range fake.volumes {
		if volume.Status == resources.VolumeStatusActive {
			out = append(out, volume)
		}
	}
	return out, nil
}

func (fake *fakeResourceStore) UpsertNamespace(_ context.Context, namespace resources.Namespace) error {
	if fake.namespaces == nil {
		fake.namespaces = map[string]resources.Namespace{}
	}
	existing := fake.namespaces[namespace.ID]
	if existing.Status == resources.NamespaceStatusDisabled {
		namespace = existing
	}
	fake.namespaces[namespace.ID] = namespace
	return nil
}

func (fake *fakeResourceStore) DisableNamespace(_ context.Context, namespaceID, reason string) (resources.Namespace, error) {
	if fake.namespaces == nil {
		fake.namespaces = map[string]resources.Namespace{}
	}
	namespace := fake.namespaces[namespaceID]
	if namespace.ID == "" {
		namespace.ID = namespaceID
	}
	if namespace.Status != resources.NamespaceStatusDisabled {
		now := time.Date(2026, 5, 5, 14, 30, 0, 0, time.UTC)
		namespace.Status = resources.NamespaceStatusDisabled
		namespace.DisabledReason = reason
		namespace.DisabledAt = &now
		namespace.UpdatedAt = now
	}
	fake.namespaces[namespaceID] = namespace
	return namespace, nil
}

func (fake *fakeResourceStore) GetNamespace(_ context.Context, namespaceID string) (resources.Namespace, error) {
	return fake.namespaces[namespaceID], nil
}

func (fake *fakeResourceStore) PutNamespaceVolumeBinding(_ context.Context, binding resources.NamespaceVolumeBinding) error {
	if fake.bindings == nil {
		fake.bindings = map[string]resources.NamespaceVolumeBinding{}
	}
	fake.bindings[binding.NamespaceID] = binding
	return nil
}

func (fake *fakeResourceStore) GetNamespaceVolumeBinding(_ context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error) {
	return fake.bindings[namespaceID], nil
}

func (fake *fakeResourceStore) CreateRepo(_ context.Context, repo resources.Repo) error {
	if fake.repos == nil {
		fake.repos = map[string]resources.Repo{}
	}
	fake.repos[repo.ID] = repo
	return nil
}

func (fake *fakeResourceStore) GetRepo(_ context.Context, repoID string) (resources.Repo, error) {
	return fake.repos[repoID], nil
}

func (fake *fakeResourceStore) GetRepoInNamespace(_ context.Context, namespaceID, repoID string) (resources.Repo, error) {
	repo := fake.repos[repoID]
	if repo.NamespaceID != namespaceID {
		return resources.Repo{}, nil
	}
	return repo, nil
}

func (fake *fakeResourceStore) ListReposByNamespace(_ context.Context, namespaceID string) ([]resources.Repo, error) {
	var out []resources.Repo
	for _, repo := range fake.repos {
		if repo.NamespaceID == namespaceID {
			out = append(out, repo)
		}
	}
	return out, nil
}

func (fake *fakeResourceStore) UpdateRepoLifecycle(_ context.Context, repoID string, lifecycle resources.RepoLifecycle) (resources.Repo, error) {
	repo := fake.repos[repoID]
	repo.Status = lifecycle.Status
	repo.Lifecycle = lifecycle
	fake.repos[repoID] = repo
	return repo, nil
}

func ptrTimeForStoreContract(value time.Time) *time.Time { return &value }

func toStoreContractString(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
