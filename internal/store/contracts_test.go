package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
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
