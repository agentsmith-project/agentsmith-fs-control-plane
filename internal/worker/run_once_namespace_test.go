package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/namespaceexec"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestRunOnceWithNamespaceOperationRecoveryRunnerClaimsNamespaceUpsert(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := workerNamespaceOperationRecord(now)
	store := &workerNamespaceRecoveryStore{records: map[string]operations.OperationRecord{record.ID: record}}
	executor, err := namespaceexec.NewExecutor(namespaceexec.Config{
		CommitStore:  store,
		Owner:        "worker-a",
		Now:          now,
		AuditEventID: func() string { return "evt_namespace" },
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	operationRunner := recovery.NewOperationCoordinator(recovery.OperationConfig{
		Reader:        store,
		LeaseStore:    store,
		Executor:      executor,
		Owner:         "worker-a",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
	})

	result, err := New(Config{OperationRecovery: operationRunner}).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Claimed != 1 || summary.Failed != 0 || summary.Unsupported != 0 {
		t.Fatalf("summary = %#v, want claimed=1 failed=0 unsupported=0", summary)
	}
	if store.namespace.ID != "ns_alpha01" || store.operation.ID != "op_namespace" || len(store.auditEvents) != 1 {
		t.Fatalf("atomic commit = namespace %#v operation %#v audit %#v", store.namespace, store.operation, store.auditEvents)
	}
}

func workerNamespaceOperationRecord(now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:               "op_namespace",
		Type:             operations.OperationNamespaceUpsert,
		State:            operations.OperationStateQueued,
		Phase:            operations.OperationPhaseNamespaceUpsertValidate,
		IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationNamespaceUpsert, "idem_namespace").String(),
		IdempotencyKey:   "idem_namespace",
		RequestHash:      operations.RequestHash("sha256:namespace"),
		CorrelationID:    "corr-alpha",
		CallerService:    "product-caller",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"},
		NamespaceID:      "ns_alpha01",
		CreatedAt:        now.Add(-time.Hour),
	}
}

type workerNamespaceRecoveryStore struct {
	records     map[string]operations.OperationRecord
	namespace   resources.Namespace
	operation   operations.OperationRecord
	auditEvents []audit.Event
}

func (store *workerNamespaceRecoveryStore) ListOperationsForRecovery(_ context.Context, _ time.Time, limit int) ([]operations.OperationRecord, error) {
	var out []operations.OperationRecord
	for _, record := range store.records {
		if len(out) >= limit {
			break
		}
		if record.State == operations.OperationStateQueued {
			out = append(out, record)
		}
	}
	return out, nil
}

func (store *workerNamespaceRecoveryStore) AcquireOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	record, ok := store.records[operationID]
	if !ok {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	decision := operations.AcquireLease(record, request)
	if !decision.Allowed {
		return operations.OperationRecord{}, decision.Error
	}
	store.records[operationID] = decision.Record
	return decision.Record, nil
}

func (store *workerNamespaceRecoveryStore) RenewOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected renew call")
}

func (store *workerNamespaceRecoveryStore) UpdateOperationWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected ordinary operation update")
}

func (store *workerNamespaceRecoveryStore) CommitNamespaceUpsertWithLease(_ context.Context, namespace resources.Namespace, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (resources.Namespace, operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.namespace = namespace
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return namespace, operation, nil
}
