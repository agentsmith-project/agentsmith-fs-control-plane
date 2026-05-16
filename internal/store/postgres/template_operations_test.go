package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
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
	if strings.Contains(exec.query, "restore_plans") || strings.Contains(exec.query, "active_restore_plan") {
		t.Fatalf("template writer fence SQL must not inspect restore plans: %s", exec.query)
	}
}

func TestAcquireTemplateCreateOperationLeaseSerializesEarlierLifecycleAndJVSMutations(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := templateCreateOperationRecord(now, operations.OperationStateRunning, operations.OperationPhaseTemplateCreateValidate)
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.AcquireTemplateCreateOperationLease(context.Background(), record.ID, operations.LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now})
	if err != nil {
		t.Fatalf("AcquireTemplateCreateOperationLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'template_create'",
		"phase IN ('validate_template_create','template_create_writer_fenced')",
		"earlier_jvs_mutation AS",
		"o.operation_id <> e.operation_id",
		"o.operation_type IN ('save_point_create', 'restore', 'template_create', 'template_clone')",
		"earlier_repo_lifecycle AS",
		"o.operation_id <> e.operation_id",
		"o.operation_type IN ('repo_archive', 'repo_restore_archived', 'repo_delete', 'repo_restore_tombstoned', 'repo_purge')",
		"operation_state NOT IN ('succeeded','failed','cancelled')",
		"NOT EXISTS (SELECT 1 FROM earlier_jvs_mutation)",
		"NOT EXISTS (SELECT 1 FROM earlier_repo_lifecycle)",
	)
	for _, forbidden := range []string{"restore_plans", "active_restore_plan"} {
		if strings.Contains(exec.query, forbidden) {
			t.Fatalf("template create acquire SQL must not inspect %q: %s", forbidden, exec.query)
		}
	}
}

func TestTemplateCloneJVSGateScopeUsesTargetRepoOnly(t *testing.T) {
	if got := repoJVSMutationOperationTypeSQLList(); !strings.Contains(got, "'template_clone'") {
		t.Fatalf("mutation operation list = %s, want template_clone covered", got)
	}
	assertSQLContainsInOrder(t, templateCloneOperationCreateOrReuseSQL(),
		"operation_type = 'template_clone'",
		"AND NOT EXISTS (SELECT 1 FROM repos WHERE repo_id = $18)",
	)
	assertSQLContainsInOrder(t, templateCloneOperationAcquireLeaseSQL(),
		"operation_type = 'template_clone'",
		"phase = 'validate_template_clone'",
	)
	if strings.Contains(templateCloneOperationAcquireLeaseSQL(), "template_id =") {
		t.Fatalf("template clone acquire should not serialize source template JVS state because clone mutates target repo only: %s", templateCloneOperationAcquireLeaseSQL())
	}
}

func TestCommitTemplateCreateFailedWithLeaseReleasesWriterFenceOnPostFenceFailure(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := templateCreateOperationRecord(now, operations.OperationStateFailed, operations.OperationPhaseTemplateCreateWriterFenced)
	record.SessionFenceID = "fence_template01"
	record.Error = &operations.OperationError{
		Code:          "TEMPLATE_CREATE_FAILED",
		Message:       "template create failed after writer fence",
		Retryable:     true,
		CorrelationID: record.CorrelationID,
		OperationID:   record.ID,
	}
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.CommitTemplateCreateFailedWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, templateAudit(record, audit.EventTypeTemplateCreate, audit.OutcomeFailed, now))
	if err != nil {
		t.Fatalf("CommitTemplateCreateFailedWithLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"held_writer_fence AS",
		"repo_fences.fence_id = $21",
		"released_writer_fence AS",
		"WHERE $1 = 'failed'",
		"updated_operation AS",
		"EXISTS (SELECT 1 FROM released_writer_fence)",
	)
}

func TestCommitTemplateCreateSucceededWithLeaseRequiresWriterFenceBeforeRepoInsert(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := templateCreateOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseTemplateCreateCommitted)
	record.SessionFenceID = "fence_template01"
	template := templateCreateRepo(now)
	exec := &fakeExecutor{row: fakeRow{values: append(repoRowValues(template), operationRowValues(record)...)}}
	st := &Store{exec: exec}

	_, _, err := st.CommitTemplateCreateSucceededWithLease(context.Background(), template, record.RepoID, "1778487604491-0f57855a", "main", record.SanitizedForPersistence(), "worker-a", now, templateAudit(record, audit.EventTypeTemplateCreate, audit.OutcomeSucceeded, now))
	if err != nil {
		t.Fatalf("CommitTemplateCreateSucceededWithLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"template_create_prerequisites AS",
		"held_writer_fence AS",
		"released_writer_fence AS",
		"), inserted_repo AS (",
		"FROM template_create_prerequisites, released_writer_fence",
		"updated_operation AS",
		"FROM eligible_operation, inserted_repo, released_writer_fence",
		"inserted_audit AS",
		"FROM updated_operation, inserted_repo, released_writer_fence",
	)
	if strings.Index(exec.query, "released_writer_fence AS") > strings.Index(exec.query, "), inserted_repo AS (") {
		t.Fatalf("template create success SQL inserts repo before confirming/releasing writer fence: %s", exec.query)
	}
	if !strings.Contains(exec.query, "repo_fences.fence_id = $38") || !strings.Contains(exec.query, "repo_fences.status = 'active'") {
		t.Fatalf("template create success SQL must require the operation-owned active writer fence before insert: %s", exec.query)
	}
}

func TestCommitTemplateCreateSucceededWithLeaseNoRowsFailsClosedBeforeRepoInsertWhenWriterFenceMissing(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := templateCreateOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseTemplateCreateCommitted)
	record.SessionFenceID = "fence_template01"
	template := templateCreateRepo(now)
	exec := &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}
	st := &Store{exec: exec}

	_, _, err := st.CommitTemplateCreateSucceededWithLease(context.Background(), template, record.RepoID, "1778487604491-0f57855a", "main", record.SanitizedForPersistence(), "worker-a", now, templateAudit(record, audit.EventTypeTemplateCreate, audit.OutcomeSucceeded, now))
	if !errors.Is(err, operations.ErrLeaseUnavailable) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("CommitTemplateCreateSucceededWithLease error = %v, want ErrLeaseUnavailable/sql.ErrNoRows", err)
	}
	assertSQLContainsInOrder(t, exec.query,
		"held_writer_fence AS",
		"released_writer_fence AS",
		"), inserted_repo AS (",
		"FROM template_create_prerequisites, released_writer_fence",
	)
}

func TestCommitTemplateCloneSucceededWithLeaseUsesContiguousAuditPlaceholders(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := templateCloneOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseTemplateCloneCommitted)
	repo := templateCloneRepo(now)
	exec := &fakeExecutor{row: fakeRow{values: append(repoRowValues(repo), operationRowValues(record)...)}}
	st := &Store{exec: exec}

	_, _, err := st.CommitTemplateCloneSucceededWithLease(context.Background(), repo, record.SanitizedForPersistence(), "worker-a", now, templateAudit(record, audit.EventTypeTemplateClone, audit.OutcomeSucceeded, now))
	if err != nil {
		t.Fatalf("CommitTemplateCloneSucceededWithLease: %v", err)
	}

	auditStart := len(operationLeaseFencedUpdateArgsForTest(t, record, "worker-a", now)) + 7 + len(repoColumns) + 1
	assertSQLContainsInOrder(t, exec.query,
		"operation_type = 'template_clone'",
		"phase = 'validate_template_clone'",
		"), inserted_audit AS (",
		"SELECT "+placeholders(auditStart, len(auditOutboxColumns)),
	)
	for _, forbidden := range []string{"$46", "$47", "$48", "$49"} {
		if strings.Contains(sqlBetween(t, exec.query, "), inserted_audit AS (", ") SELECT "), forbidden) {
			t.Fatalf("template clone audit placeholders must be contiguous from $35; found %s in query: %s", forbidden, exec.query)
		}
	}
	wantArgs := auditStart - 1 + len(auditOutboxColumns)
	if len(exec.args) != wantArgs {
		t.Fatalf("arg count = %d, want %d without unused provenance/fence gaps", len(exec.args), wantArgs)
	}
	if exec.args[auditStart-1] != "audit_template" {
		t.Fatalf("audit arg start = %#v, want audit event id at placeholder $%d", exec.args[auditStart-1], auditStart)
	}
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

func templateCloneOperationRecord(now time.Time, state operations.OperationState, phase string) operations.OperationRecord {
	record := templateCreateOperationRecord(now, state, phase)
	record.ID = "op_template_clone"
	record.Type = operations.OperationTemplateClone
	record.IdempotencyScope = operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationTemplateClone, "idem_template_clone").String()
	record.IdempotencyKey = "idem_template_clone"
	record.RequestHash = "sha256:template-clone"
	record.Resource = operations.ResourceRef{Type: "repo", ID: "repo_flib_6a17b13c99a3"}
	record.RepoID = "repo_flib_6a17b13c99a3"
	record.TemplateID = "tmpl_tftpl_a92e7bbee4db41d6b91905e0"
	record.InputSummary = map[string]any{"template_id": record.TemplateID, "target_repo_id": record.RepoID, "clone_history_mode": "main"}
	return record
}

func templateCreateRepo(now time.Time) resources.Repo {
	return resources.Repo{
		ID:                  "tmpl_base01",
		NamespaceID:         "ns_alpha01",
		VolumeID:            "vol_123",
		JVSRepoID:           "jvs_tmpl_base01",
		Kind:                resources.RepoKindTemplate,
		Status:              resources.RepoStatusActive,
		ControlVolumeSubdir: "afscp/namespaces/ns_alpha01/templates/tmpl_base01/control",
		PayloadVolumeSubdir: "afscp/namespaces/ns_alpha01/templates/tmpl_base01/payload",
		Lifecycle:           resources.RepoLifecycle{Status: resources.RepoStatusActive, LastLifecycleOperationID: "op_template01"},
		CreatedAt:           now.Add(-time.Hour),
		UpdatedAt:           now,
	}
}

func templateCloneRepo(now time.Time) resources.Repo {
	return resources.Repo{
		ID:                  "repo_flib_6a17b13c99a3",
		NamespaceID:         "ns_alpha01",
		VolumeID:            "vol_123",
		JVSRepoID:           "jvs_repo_clone_alpha",
		Kind:                resources.RepoKindRepo,
		Status:              resources.RepoStatusActive,
		ControlVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_flib_6a17b13c99a3/control",
		PayloadVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_flib_6a17b13c99a3/payload",
		Lifecycle:           resources.RepoLifecycle{Status: resources.RepoStatusActive, LastLifecycleOperationID: "op_template_clone"},
		CreatedAt:           now.Add(-time.Hour),
		UpdatedAt:           now,
	}
}

func templateAudit(record operations.OperationRecord, typ audit.EventType, outcome audit.Outcome, now time.Time) audit.Event {
	return audit.NewEvent(audit.Event{
		EventID:         "audit_template",
		Type:            typ,
		Time:            now,
		CallerService:   record.CallerService,
		AuthorizedActor: audit.Actor{Type: record.AuthorizedActor.Type, ID: record.AuthorizedActor.ID},
		CorrelationID:   record.CorrelationID,
		OperationID:     record.ID,
		Resource:        audit.Resource{Type: record.Resource.Type, ID: record.Resource.ID, NamespaceID: record.NamespaceID},
		Outcome:         outcome,
		Reason:          "template_commit",
	})
}
