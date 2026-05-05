package namespaceexec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

func TestExecutorSupportsOnlyNamespaceUpsertValidatePhase(t *testing.T) {
	executor := newTestExecutor(t, &fakeNamespaceCommitStore{}, namespaceExecNow(), func() string { return "evt_namespace" })

	tests := []struct {
		name      string
		record    operations.OperationRecord
		plan      recovery.RecoveryPlan
		supported bool
		reason    string
	}{
		{
			name:      "namespace upsert validate",
			record:    namespaceExecRecord("op_supported"),
			plan:      recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable},
			supported: true,
		},
		{
			name:      "namespace upsert retry",
			record:    namespaceExecRecord("op_retry"),
			plan:      recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry},
			supported: true,
		},
		{
			name:      "namespace upsert reclaim",
			record:    namespaceExecRecord("op_reclaim"),
			plan:      recovery.RecoveryPlan{Action: recovery.RecoveryActionReclaim},
			supported: true,
		},
		{
			name: "wrong type",
			record: func() operations.OperationRecord {
				record := namespaceExecRecord("op_wrong_type")
				record.Type = operations.OperationRepoCreate
				return record
			}(),
			plan:      recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable},
			supported: false,
			reason:    "unsupported_namespace_upsert_operation",
		},
		{
			name: "wrong phase",
			record: func() operations.OperationRecord {
				record := namespaceExecRecord("op_wrong_phase")
				record.Phase = "allocate_namespace"
				return record
			}(),
			plan:      recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable},
			supported: false,
			reason:    "unsupported_namespace_upsert_phase",
		},
		{
			name:      "wrong action",
			record:    namespaceExecRecord("op_wrong_action"),
			plan:      recovery.RecoveryPlan{Action: recovery.RecoveryActionFinalizeCancellation},
			supported: false,
			reason:    "unsupported_namespace_upsert_recovery_action",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			support := executor.SupportsOperationRecovery(context.Background(), tt.record, tt.plan)
			if support.Supported != tt.supported {
				t.Fatalf("Supported = %v, want %v: %#v", support.Supported, tt.supported, support)
			}
			if tt.reason != "" && support.Reason != tt.reason {
				t.Fatalf("Reason = %q, want %q", support.Reason, tt.reason)
			}
		})
	}
}

func TestExecutorRejectsInvalidConfigAndNilExecutorBeforeStoreCalls(t *testing.T) {
	now := namespaceExecNow()
	validGenerator := func() string { return "evt_namespace" }
	tests := []struct {
		name   string
		config Config
	}{
		{name: "nil store", config: Config{Owner: "worker-a", Now: now, AuditEventID: validGenerator}},
		{name: "blank owner", config: Config{CommitStore: &fakeNamespaceCommitStore{}, Owner: " \t", Now: now, AuditEventID: validGenerator}},
		{name: "zero now without clock", config: Config{CommitStore: &fakeNamespaceCommitStore{}, Owner: "worker-a", AuditEventID: validGenerator}},
		{name: "nil audit id generator", config: Config{CommitStore: &fakeNamespaceCommitStore{}, Owner: "worker-a", Now: now}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _ := tt.config.CommitStore.(*fakeNamespaceCommitStore)
			executor, err := NewExecutor(tt.config)
			if err == nil {
				t.Fatal("NewExecutor succeeded, want config error")
			}
			if executor != nil {
				t.Fatalf("executor = %#v, want nil", executor)
			}
			if store != nil && store.commitCalls != 0 {
				t.Fatalf("commit calls = %d, want 0", store.commitCalls)
			}
		})
	}

	var nilExecutor *Executor
	store := &fakeNamespaceCommitStore{}
	if nilExecutor.SupportsOperationRecovery(context.Background(), namespaceExecRecord("op_nil"), recovery.RecoveryPlan{}).Supported {
		t.Fatal("nil executor support = true, want false")
	}
	err := nilExecutor.ExecuteOperationRecovery(context.Background(), namespaceExecRecord("op_nil"), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if err == nil {
		t.Fatal("nil executor ExecuteOperationRecovery succeeded, want error")
	}
	if store.commitCalls != 0 {
		t.Fatalf("commit calls = %d, want 0", store.commitCalls)
	}
}

func TestExecutorClaimRetryReclaimCommitNamespaceOperationAndAuditAtomically(t *testing.T) {
	for _, action := range []recovery.RecoveryAction{recovery.RecoveryActionClaimable, recovery.RecoveryActionRetry, recovery.RecoveryActionReclaim} {
		t.Run(string(action), func(t *testing.T) {
			now := namespaceExecNow()
			store := &fakeNamespaceCommitStore{}
			executor := newTestExecutor(t, store, now, func() string { return "evt_namespace_" + string(action) })
			record := namespaceExecRecord("op_namespace_" + string(action))
			record.State = operations.OperationStateRunning
			expiresAt := now.Add(5 * time.Minute)
			record.LeaseOwner = "worker-a"
			record.LeaseExpiresAt = &expiresAt
			record.Attempt = 2

			err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: action})
			if err != nil {
				t.Fatalf("ExecuteOperationRecovery: %v", err)
			}
			if store.commitCalls != 1 {
				t.Fatalf("commit calls = %d, want 1", store.commitCalls)
			}

			call := store.lastCall
			if call.owner != "worker-a" || !call.now.Equal(now) {
				t.Fatalf("owner/now = %q/%v, want worker-a/%v", call.owner, call.now, now)
			}
			if call.namespace.ID != record.NamespaceID || call.namespace.Status != resources.NamespaceStatusActive {
				t.Fatalf("namespace = %#v, want active %s", call.namespace, record.NamespaceID)
			}
			if !call.namespace.CreatedAt.Equal(record.CreatedAt) || !call.namespace.UpdatedAt.Equal(now) {
				t.Fatalf("namespace times = %v/%v, want created record time and updated now", call.namespace.CreatedAt, call.namespace.UpdatedAt)
			}

			operation := call.record.Record()
			if operation.ID != record.ID || operation.Type != operations.OperationNamespaceUpsert {
				t.Fatalf("operation id/type = %q/%q, want %q/namespace_upsert", operation.ID, operation.Type, record.ID)
			}
			if operation.State != operations.OperationStateSucceeded || operation.Phase != operations.OperationPhaseNamespaceUpsertCommitted {
				t.Fatalf("operation state/phase = %q/%q, want succeeded/%s", operation.State, operation.Phase, operations.OperationPhaseNamespaceUpsertCommitted)
			}
			if operation.NamespaceID != record.NamespaceID || operation.Resource != record.Resource {
				t.Fatalf("operation namespace/resource = %q/%#v, want %q/%#v", operation.NamespaceID, operation.Resource, record.NamespaceID, record.Resource)
			}
			if operation.CallerService != record.CallerService || operation.CorrelationID != record.CorrelationID || operation.AuthorizedActor != record.AuthorizedActor {
				t.Fatalf("operation caller/correlation/actor = %q/%q/%#v, want %q/%q/%#v", operation.CallerService, operation.CorrelationID, operation.AuthorizedActor, record.CallerService, record.CorrelationID, record.AuthorizedActor)
			}
			if operation.FinishedAt == nil || !operation.FinishedAt.Equal(now) {
				t.Fatalf("operation finished_at = %v, want %v", operation.FinishedAt, now)
			}

			event := call.event
			if event.EventID == "" || event.Type != audit.EventTypeNamespaceUpsert || event.Outcome != audit.OutcomeSucceeded {
				t.Fatalf("event id/type/outcome = %q/%q/%q, want id namespace_upsert succeeded", event.EventID, event.Type, event.Outcome)
			}
			if event.OperationID != operation.ID || event.Resource.Type != "namespace" || event.Resource.ID != record.NamespaceID || event.Resource.NamespaceID != record.NamespaceID {
				t.Fatalf("event operation/resource = %q/%#v", event.OperationID, event.Resource)
			}
			if event.CallerService != operation.CallerService || event.CorrelationID != operation.CorrelationID {
				t.Fatalf("event caller/correlation = %q/%q, want %q/%q", event.CallerService, event.CorrelationID, operation.CallerService, operation.CorrelationID)
			}
			if event.AuthorizedActor.Type != operation.AuthorizedActor.Type || event.AuthorizedActor.ID != operation.AuthorizedActor.ID {
				t.Fatalf("event actor = %#v, want %#v", event.AuthorizedActor, operation.AuthorizedActor)
			}
		})
	}
}

func TestExecutorRejectsInvalidRecordPlanTimeAndEventIDBeforeStoreCalls(t *testing.T) {
	now := namespaceExecNow()
	baseRecord := namespaceExecRecord("op_invalid")
	baseRecord.State = operations.OperationStateRunning
	expiresAt := now.Add(5 * time.Minute)
	baseRecord.LeaseOwner = "worker-a"
	baseRecord.LeaseExpiresAt = &expiresAt

	tests := []struct {
		name    string
		record  operations.OperationRecord
		plan    recovery.RecoveryPlan
		now     time.Time
		clock   func() time.Time
		eventID func() string
	}{
		{name: "unsupported plan", record: baseRecord, plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionFinalizeCancellation}, now: now, eventID: func() string { return "evt_namespace" }},
		{name: "unsupported type", record: func() operations.OperationRecord {
			record := baseRecord
			record.Type = operations.OperationRepoCreate
			return record
		}(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_namespace" }},
		{name: "unsupported phase", record: func() operations.OperationRecord { record := baseRecord; record.Phase = "other"; return record }(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_namespace" }},
		{name: "missing namespace id", record: func() operations.OperationRecord { record := baseRecord; record.NamespaceID = ""; return record }(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_namespace" }},
		{name: "resource mismatch", record: func() operations.OperationRecord {
			record := baseRecord
			record.Resource.ID = "ns_beta01"
			return record
		}(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_namespace" }},
		{name: "missing resource type", record: func() operations.OperationRecord { record := baseRecord; record.Resource.Type = ""; return record }(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_namespace" }},
		{name: "missing caller", record: func() operations.OperationRecord { record := baseRecord; record.CallerService = " "; return record }(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_namespace" }},
		{name: "missing actor type", record: func() operations.OperationRecord {
			record := baseRecord
			record.AuthorizedActor.Type = ""
			return record
		}(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_namespace" }},
		{name: "missing actor id", record: func() operations.OperationRecord { record := baseRecord; record.AuthorizedActor.ID = ""; return record }(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_namespace" }},
		{name: "missing correlation", record: func() operations.OperationRecord { record := baseRecord; record.CorrelationID = ""; return record }(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_namespace" }},
		{name: "missing running lease owner", record: func() operations.OperationRecord { record := baseRecord; record.LeaseOwner = ""; return record }(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_namespace" }},
		{name: "zero now from clock", record: baseRecord, plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, clock: func() time.Time { return time.Time{} }, eventID: func() string { return "evt_namespace" }},
		{name: "blank event id", record: baseRecord, plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return " \t" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeNamespaceCommitStore{}
			executor, err := NewExecutor(Config{
				CommitStore:  store,
				Owner:        "worker-a",
				Now:          tt.now,
				Clock:        tt.clock,
				AuditEventID: tt.eventID,
			})
			if err != nil {
				t.Fatalf("NewExecutor: %v", err)
			}

			err = executor.ExecuteOperationRecovery(context.Background(), tt.record, tt.plan)
			if err == nil {
				t.Fatal("ExecuteOperationRecovery succeeded, want fail-closed error")
			}
			if store.commitCalls != 0 {
				t.Fatalf("commit calls = %d, want 0", store.commitCalls)
			}
		})
	}
}

func TestExecutorPropagatesStoreErrors(t *testing.T) {
	now := namespaceExecNow()
	record := namespaceExecRecord("op_error")
	record.State = operations.OperationStateRunning
	expiresAt := now.Add(5 * time.Minute)
	record.LeaseOwner = "worker-a"
	record.LeaseExpiresAt = &expiresAt

	for _, storeErr := range []error{operations.ErrLeaseUnavailable, errors.New("database unavailable")} {
		t.Run(storeErr.Error(), func(t *testing.T) {
			store := &fakeNamespaceCommitStore{err: storeErr}
			executor := newTestExecutor(t, store, now, func() string { return "evt_namespace" })

			err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
			if !errors.Is(err, storeErr) {
				t.Fatalf("ExecuteOperationRecovery error = %v, want %v", err, storeErr)
			}
			if store.commitCalls != 1 {
				t.Fatalf("commit calls = %d, want 1", store.commitCalls)
			}
		})
	}
}

func TestOperationCoordinatorRunsNamespaceUpsertClaimRetryAndReclaim(t *testing.T) {
	now := namespaceExecNow()
	expired := now.Add(-time.Minute)

	tests := []struct {
		name          string
		record        operations.OperationRecord
		wantClaimed   int
		wantReclaimed int
		wantAction    recovery.RecoveryAction
	}{
		{
			name: "queued first claim",
			record: func() operations.OperationRecord {
				record := namespaceExecRecord("op_namespace_claim")
				record.State = operations.OperationStateQueued
				record.Attempt = 0
				record.LeaseOwner = ""
				record.LeaseExpiresAt = nil
				return record
			}(),
			wantClaimed: 1,
			wantAction:  recovery.RecoveryActionClaimable,
		},
		{
			name: "queued retry",
			record: func() operations.OperationRecord {
				record := namespaceExecRecord("op_namespace_retry")
				record.State = operations.OperationStateQueued
				record.Attempt = 2
				record.LeaseOwner = ""
				record.LeaseExpiresAt = nil
				return record
			}(),
			wantClaimed: 1,
			wantAction:  recovery.RecoveryActionRetry,
		},
		{
			name: "expired running reclaim",
			record: func() operations.OperationRecord {
				record := namespaceExecRecord("op_namespace_reclaim")
				record.State = operations.OperationStateRunning
				record.LeaseOwner = "worker-old"
				record.LeaseExpiresAt = &expired
				return record
			}(),
			wantReclaimed: 1,
			wantAction:    recovery.RecoveryActionReclaim,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeNamespaceRecoveryStore{records: map[string]operations.OperationRecord{tt.record.ID: tt.record}}
			executor := newTestExecutor(t, store, now, func() string { return "evt_" + tt.record.ID })
			coordinator := recovery.NewOperationCoordinator(recovery.OperationConfig{
				Reader:        store,
				LeaseStore:    store,
				Executor:      executor,
				Owner:         "worker-a",
				LeaseDuration: time.Minute,
				Limit:         10,
				Now:           now,
			})

			result, err := coordinator.RunOnce(context.Background())
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if result.Claimed != tt.wantClaimed || result.Reclaimed != tt.wantReclaimed || result.Failed != 0 || result.Unsupported != 0 {
				t.Fatalf("result = %#v, want claimed=%d reclaimed=%d failed=0 unsupported=0", result, tt.wantClaimed, tt.wantReclaimed)
			}
			if len(result.Results) != 1 || result.Results[0].Action != tt.wantAction {
				t.Fatalf("result items = %#v, want one %s", result.Results, tt.wantAction)
			}
			if store.commitCalls != 1 || store.operation.ID != tt.record.ID || store.namespace.ID != tt.record.NamespaceID || len(store.auditEvents) != 1 {
				t.Fatalf("atomic commit = calls %d namespace %#v operation %#v audit %#v", store.commitCalls, store.namespace, store.operation, store.auditEvents)
			}
			if store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseNamespaceUpsertCommitted {
				t.Fatalf("operation terminal = %#v, want succeeded committed", store.operation)
			}
		})
	}
}

func newTestExecutor(t *testing.T, commitStore store.NamespaceUpsertOperationCommitStore, now time.Time, eventID func() string) *Executor {
	t.Helper()
	executor, err := NewExecutor(Config{
		CommitStore:  commitStore,
		Owner:        "worker-a",
		Now:          now,
		AuditEventID: eventID,
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	return executor
}

func namespaceExecRecord(operationID string) operations.OperationRecord {
	now := namespaceExecNow().Add(-time.Hour)
	return operations.OperationRecord{
		ID:               operationID,
		Type:             operations.OperationNamespaceUpsert,
		State:            operations.OperationStateRunning,
		Phase:            operations.OperationPhaseNamespaceUpsertValidate,
		Attempt:          1,
		IdempotencyScope: operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationNamespaceUpsert, "idem_namespace").String(),
		IdempotencyKey:   "idem_namespace",
		RequestHash:      operations.RequestHash("sha256:namespace"),
		CorrelationID:    "corr-alpha",
		CallerService:    "agentsmith-api",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"},
		NamespaceID:      "ns_alpha01",
		InputSummary:     map[string]any{"namespace_id": "ns_alpha01"},
		CreatedAt:        now,
	}
}

func namespaceExecNow() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}

type fakeNamespaceCommitStore struct {
	commitCalls int
	lastCall    namespaceCommitCall
	err         error
}

type namespaceCommitCall struct {
	namespace resources.Namespace
	record    operations.SanitizedOperationRecord
	owner     string
	now       time.Time
	event     audit.Event
}

func (store *fakeNamespaceCommitStore) CommitNamespaceUpsertWithLease(ctx context.Context, namespace resources.Namespace, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Namespace, operations.OperationRecord, error) {
	store.commitCalls++
	store.lastCall = namespaceCommitCall{namespace: namespace, record: record, owner: owner, now: now, event: event}
	if store.err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, store.err
	}
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	return namespace, operation, nil
}

type fakeNamespaceRecoveryStore struct {
	fakeNamespaceCommitStore
	records     map[string]operations.OperationRecord
	namespace   resources.Namespace
	operation   operations.OperationRecord
	auditEvents []audit.Event
}

func (store *fakeNamespaceRecoveryStore) ListOperationsForRecovery(_ context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	var out []operations.OperationRecord
	for _, record := range store.records {
		if len(out) >= limit {
			break
		}
		if record.State == operations.OperationStateQueued {
			out = append(out, record)
			continue
		}
		if record.State == operations.OperationStateRunning && record.LeaseExpiresAt != nil && !record.LeaseExpiresAt.After(now) {
			out = append(out, record)
		}
	}
	return out, nil
}

func (store *fakeNamespaceRecoveryStore) AcquireOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
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

func (store *fakeNamespaceRecoveryStore) RenewOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected renew call")
}

func (store *fakeNamespaceRecoveryStore) UpdateOperationWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected ordinary operation update")
}

func (store *fakeNamespaceRecoveryStore) CommitNamespaceUpsertWithLease(ctx context.Context, namespace resources.Namespace, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Namespace, operations.OperationRecord, error) {
	store.fakeNamespaceCommitStore.CommitNamespaceUpsertWithLease(ctx, namespace, record, owner, now, event)
	if store.err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, store.err
	}
	if strings.TrimSpace(event.OperationID) == "" || event.OperationID != record.Record().ID {
		return resources.Namespace{}, operations.OperationRecord{}, audit.ErrInvalidOutboxRequest
	}
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.namespace = namespace
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return namespace, operation, nil
}
