package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func TestMarkTemplateCreateWriterFencedWithLeaseLocksRepoBeforeWriterFence(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := templateCreateOperationRecord(now, operations.OperationStateRunning, operations.OperationPhaseTemplateCreateWriterFenced)
	record.SessionFenceID = "fence_template01"
	fence := fences.Fence{ID: "fence_template01", RepoID: record.RepoID, Kind: fences.KindWriterSession, HolderOperationID: record.ID, Status: fences.StatusActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
	exec := &fakeExecutor{row: fakeRow{values: append(repoFenceRowValues(fence), operationRowValues(record)...)}}
	st := &Store{exec: exec}

	_, _, err := st.MarkTemplateCreateWriterFencedWithLease(context.Background(), fence, record.SanitizedForPersistence(), "worker-a", now)
	if err != nil {
		t.Fatalf("MarkTemplateCreateWriterFencedWithLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'template_create'",
		"phase = 'validate_template_create'",
		"locked_repo AS",
		"FROM repos, eligible_operation",
		"FOR UPDATE",
		"held_lifecycle_fence AS",
		"repo_fences.repo_id = locked_repo.repo_id",
		"active_writer_fence AS",
		"repo_fences.repo_id = locked_repo.repo_id",
		"inserted_writer_fence AS",
		"FROM eligible_operation, locked_repo",
		"ON CONFLICT (repo_id, fence_kind) WHERE released_at IS NULL DO NOTHING",
	)
}

func templateCreateOperationRecord(now time.Time, state operations.OperationState, phase string) operations.OperationRecord {
	lease := now.Add(time.Minute)
	started := now.Add(-time.Minute)
	return operations.OperationRecord{
		ID:                  "op_template01",
		Type:                operations.OperationTemplateCreate,
		State:               state,
		Phase:               phase,
		LeaseOwner:          "worker-a",
		LeaseExpiresAt:      &lease,
		Attempt:             1,
		IdempotencyScope:    operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationTemplateCreate, "idem_template").String(),
		IdempotencyKey:      "idem_template",
		RequestHash:         "sha256:template-create",
		CorrelationID:       "corr-template",
		CallerService:       "product-caller",
		AuthorizedActor:     operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:            operations.ResourceRef{Type: "repo_template", ID: "tmpl_base01"},
		NamespaceID:         "ns_alpha01",
		RepoID:              "repo_alpha01",
		TemplateID:          "tmpl_base01",
		InputSummary:        map[string]any{"source_repo_id": "repo_alpha01", "target_template_id": "tmpl_base01", "clone_history_mode": "main"},
		ExternalResourceIDs: map[string]string{},
		StartedAt:           &started,
		CreatedAt:           now.Add(-time.Hour),
	}
}
