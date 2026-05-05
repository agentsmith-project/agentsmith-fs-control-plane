package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
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
	record operations.OperationRecord
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
