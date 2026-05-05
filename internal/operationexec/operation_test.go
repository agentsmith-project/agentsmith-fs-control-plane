package operationexec

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
)

func TestNewExecutorRejectsInvalidConfigAndRegistrations(t *testing.T) {
	now := operationExecTestNow()
	validStore := &fakeCommitStore{}
	validHandler := HandlerFunc(func(context.Context, operations.OperationRecord, recovery.RecoveryPlan, Committer) error { return nil })
	tests := []struct {
		name   string
		config Config
	}{
		{name: "nil store", config: Config{Owner: "worker", Now: now, Registrations: []Registration{{OperationType: operations.OperationRepoCreate, Phase: "queued", Handler: validHandler}}}},
		{name: "blank owner", config: Config{CommitStore: validStore, Owner: " \t", Now: now, Registrations: []Registration{{OperationType: operations.OperationRepoCreate, Phase: "queued", Handler: validHandler}}}},
		{name: "zero now without clock", config: Config{CommitStore: validStore, Owner: "worker", Registrations: []Registration{{OperationType: operations.OperationRepoCreate, Phase: "queued", Handler: validHandler}}}},
		{name: "blank type", config: Config{CommitStore: validStore, Owner: "worker", Now: now, Registrations: []Registration{{Phase: "queued", Handler: validHandler}}}},
		{name: "unknown type", config: Config{CommitStore: validStore, Owner: "worker", Now: now, Registrations: []Registration{{OperationType: operations.OperationType("unknown_operation"), Phase: "queued", Handler: validHandler}}}},
		{name: "blank phase", config: Config{CommitStore: validStore, Owner: "worker", Now: now, Registrations: []Registration{{OperationType: operations.OperationRepoCreate, Phase: " ", Handler: validHandler}}}},
		{name: "nil handler", config: Config{CommitStore: validStore, Owner: "worker", Now: now, Registrations: []Registration{{OperationType: operations.OperationRepoCreate, Phase: "queued"}}}},
		{name: "duplicate", config: Config{CommitStore: validStore, Owner: "worker", Now: now, Registrations: []Registration{
			{OperationType: operations.OperationRepoCreate, Phase: "queued", Handler: validHandler},
			{OperationType: operations.OperationRepoCreate, Phase: " queued ", Handler: validHandler},
		}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor, err := NewExecutor(tt.config)
			if err == nil {
				t.Fatalf("NewExecutor = %#v, nil error; want invalid config error", executor)
			}
			if validStore.calls != 0 {
				t.Fatalf("commit store calls = %d, want 0", validStore.calls)
			}
		})
	}
}

func TestExecutorRegistrationSupportsTypePhaseForClaimRetryAndReclaimPlans(t *testing.T) {
	handlerErr := errors.New("handler observed plan")
	store := &fakeCommitStore{}
	handler := &recordingHandler{returnErr: handlerErr}
	executor := newTestExecutor(t, store, Registration{OperationType: operations.OperationRepoCreate, Phase: "allocate", Handler: handler})
	record := operationExecRecord("op_supported", operations.OperationRepoCreate, "allocate")
	plans := []recovery.RecoveryPlan{
		{Action: recovery.RecoveryActionClaimable},
		{Action: recovery.RecoveryActionRetry},
		{Action: recovery.RecoveryActionReclaim},
	}

	for _, plan := range plans {
		support := executor.SupportsOperationRecovery(context.Background(), record, plan)
		if !support.Supported {
			t.Fatalf("SupportsOperationRecovery(%s) = %#v, want supported by type/phase", plan.Action, support)
		}
		err := executor.ExecuteOperationRecovery(context.Background(), record, plan)
		if !errors.Is(err, handlerErr) {
			t.Fatalf("ExecuteOperationRecovery(%s) error = %v, want handler error", plan.Action, err)
		}
		if handler.plan.Action != plan.Action {
			t.Fatalf("handler plan action = %s, want %s", handler.plan.Action, plan.Action)
		}
	}
	if handler.calls != len(plans) || store.calls != 0 {
		t.Fatalf("handler/store calls = %d/%d, want %d/0", handler.calls, store.calls, len(plans))
	}
}

func TestExecutorUnsupportedTypePhaseDoesNotCallHandlerOrStore(t *testing.T) {
	store := &fakeCommitStore{}
	handler := &recordingHandler{}
	executor := newTestExecutor(t, store, Registration{OperationType: operations.OperationRepoCreate, Phase: "allocate", Handler: handler})
	record := operationExecRecord("op_unsupported", operations.OperationRepoArchive, "allocate")
	plan := recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}

	support := executor.SupportsOperationRecovery(context.Background(), record, plan)
	if support.Supported || support.Reason != "unsupported_operation_recovery_handler" {
		t.Fatalf("support = %#v, want unsupported handler reason", support)
	}
	err := executor.ExecuteOperationRecovery(context.Background(), record, plan)
	if !errors.Is(err, ErrUnsupportedOperationRecovery) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want ErrUnsupportedOperationRecovery", err)
	}
	if handler.calls != 0 || store.calls != 0 {
		t.Fatalf("handler/store calls = %d/%d, want 0", handler.calls, store.calls)
	}
}

func TestExecutorSupportedTypePhaseCallsHandlerWithContextRecordAndPlan(t *testing.T) {
	ctx := context.WithValue(context.Background(), operationExecContextKey("ctx"), "value")
	store := &fakeCommitStore{}
	handler := &recordingHandler{returnErr: errors.New("stop before commit")}
	executor := newTestExecutor(t, store, Registration{OperationType: operations.OperationRepoCreate, Phase: "allocate", Handler: handler})
	record := operationExecRecord("op_supported", operations.OperationRepoCreate, "allocate")
	plan := recovery.RecoveryPlan{Action: recovery.RecoveryActionReclaim, Reason: "expired_lease"}

	support := executor.SupportsOperationRecovery(ctx, record, plan)
	if !support.Supported || support.Reason != "" {
		t.Fatalf("support = %#v, want supported", support)
	}
	err := executor.ExecuteOperationRecovery(ctx, record, plan)
	if !errors.Is(err, handler.returnErr) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want handler error", err)
	}
	if handler.calls != 1 || handler.ctx != ctx || handler.record.ID != record.ID || handler.plan.Action != plan.Action {
		t.Fatalf("handler call = calls %d ctx %v record %#v plan %#v", handler.calls, handler.ctx == ctx, handler.record, handler.plan)
	}
}

func TestExecutorCommitterCommitsSanitizedRecordWithOwnerNowAndAuditEvent(t *testing.T) {
	ctx := context.WithValue(context.Background(), operationExecContextKey("ctx"), "value")
	now := operationExecTestNow()
	store := &fakeCommitStore{returnRecord: operations.OperationRecord{ID: "op_commit", State: operations.OperationStateSucceeded, Phase: "done"}}
	updated := operationExecRecord("op_commit", operations.OperationRepoCreate, "done")
	updated.State = operations.OperationStateSucceeded
	updated.InputSummary = map[string]any{"token": "secret-token"}
	event := audit.Event{EventID: "audit-1", Type: audit.EventTypeRepoCreate, Time: now, OperationID: "op_commit", Outcome: audit.OutcomeSucceeded}
	handler := HandlerFunc(func(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan, committer Committer) error {
		got, err := committer.Commit(ctx, updated.SanitizedForPersistence(), event)
		if err != nil {
			return err
		}
		if got.ID != "op_commit" || got.Phase != "done" {
			t.Fatalf("commit returned %#v, want store result", got)
		}
		return nil
	})
	executor := newTestExecutorWithConfig(t, Config{
		CommitStore:   store,
		Owner:         " worker-1 ",
		Now:           now,
		Registrations: []Registration{{OperationType: operations.OperationRepoCreate, Phase: "allocate", Handler: handler}},
	})

	err := executor.ExecuteOperationRecovery(ctx, operationExecRecord("op_commit", operations.OperationRepoCreate, "allocate"), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.calls != 1 || store.ctx != ctx || store.owner != "worker-1" || !store.now.Equal(now) || store.event.OperationID != "op_commit" {
		t.Fatalf("store call = calls %d ctx %v owner %q now %v event %#v", store.calls, store.ctx == ctx, store.owner, store.now, store.event)
	}
	if got := store.record.Record(); got.InputSummary["token"] == "secret-token" {
		t.Fatalf("commit received unsanitized record: %#v", got.InputSummary)
	}
}

func TestExecutorCommitterRejectsMismatchedOperationIDBeforeStoreCall(t *testing.T) {
	store := &fakeCommitStore{}
	handler := HandlerFunc(func(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan, committer Committer) error {
		other := operationExecRecord("op_b", operations.OperationRepoCreate, "done")
		_, err := committer.Commit(ctx, other.SanitizedForPersistence(), audit.Event{EventID: "audit-1", Type: audit.EventTypeRepoCreate, OperationID: "op_b"})
		return err
	})
	executor := newTestExecutor(t, store, Registration{OperationType: operations.OperationRepoCreate, Phase: "allocate", Handler: handler})

	err := executor.ExecuteOperationRecovery(context.Background(), operationExecRecord("op_a", operations.OperationRepoCreate, "allocate"), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, ErrOperationCommitIDMismatch) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want ErrOperationCommitIDMismatch", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0 before mismatched operation commit", store.calls)
	}
}

func TestExecutorCommitterRejectsMismatchedAuditEventTypeBeforeStoreCall(t *testing.T) {
	store := &fakeCommitStore{}
	handler := HandlerFunc(func(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan, committer Committer) error {
		_, err := committer.Commit(ctx, record.SanitizedForPersistence(), audit.Event{EventID: "audit-1", Type: audit.EventTypeRepoArchive, OperationID: record.ID})
		return err
	})
	executor := newTestExecutor(t, store, Registration{OperationType: operations.OperationRepoCreate, Phase: "allocate", Handler: handler})

	err := executor.ExecuteOperationRecovery(context.Background(), operationExecRecord("op_event_mismatch", operations.OperationRepoCreate, "allocate"), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, ErrOperationCommitEventTypeMismatch) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want ErrOperationCommitEventTypeMismatch", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0 before mismatched audit event type", store.calls)
	}
}

func TestExecutorErrorsWhenHandlerReturnsNilWithoutCommit(t *testing.T) {
	store := &fakeCommitStore{}
	executor := newTestExecutor(t, store, Registration{
		OperationType: operations.OperationRepoCreate,
		Phase:         "allocate",
		Handler:       HandlerFunc(func(context.Context, operations.OperationRecord, recovery.RecoveryPlan, Committer) error { return nil }),
	})

	err := executor.ExecuteOperationRecovery(context.Background(), operationExecRecord("op_no_commit", operations.OperationRepoCreate, "allocate"), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if err == nil || !strings.Contains(err.Error(), "durable commit") {
		t.Fatalf("ExecuteOperationRecovery error = %v, want missing commit error", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestExecutorReturnsContractViolationWhenHandlerErrorsAfterDurableCommit(t *testing.T) {
	handlerErr := errors.New("handler did work after commit")
	store := &fakeCommitStore{}
	handler := HandlerFunc(func(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan, committer Committer) error {
		if _, err := committer.Commit(ctx, record.SanitizedForPersistence(), audit.Event{EventID: "audit-1", Type: audit.EventTypeRepoCreate, OperationID: record.ID}); err != nil {
			return err
		}
		return handlerErr
	})
	executor := newTestExecutor(t, store, Registration{OperationType: operations.OperationRepoCreate, Phase: "allocate", Handler: handler})

	err := executor.ExecuteOperationRecovery(context.Background(), operationExecRecord("op_commit_then_error", operations.OperationRepoCreate, "allocate"), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, ErrOperationHandlerAfterCommit) || !errors.Is(err, handlerErr) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want handler-after-commit contract violation wrapping handler error", err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want durable commit already happened once", store.calls)
	}
}

func TestExecutorClockIsReadForEachExecuteCommitTime(t *testing.T) {
	first := operationExecTestNow()
	second := first.Add(time.Minute)
	times := []time.Time{first, second}
	store := &fakeCommitStore{}
	handler := HandlerFunc(func(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan, committer Committer) error {
		_, err := committer.Commit(ctx, record.SanitizedForPersistence(), audit.Event{EventID: "audit-1", Type: audit.EventTypeRepoCreate, OperationID: record.ID})
		return err
	})
	executor := newTestExecutorWithConfig(t, Config{
		CommitStore: store,
		Owner:       "worker-1",
		Now:         first.Add(-time.Hour),
		Clock: func() time.Time {
			now := times[0]
			times = times[1:]
			return now
		},
		Registrations: []Registration{{OperationType: operations.OperationRepoCreate, Phase: "allocate", Handler: handler}},
	})

	if err := executor.ExecuteOperationRecovery(context.Background(), operationExecRecord("op_one", operations.OperationRepoCreate, "allocate"), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("first ExecuteOperationRecovery: %v", err)
	}
	if !store.now.Equal(first) {
		t.Fatalf("first commit time = %v, want clock time %v", store.now, first)
	}
	if err := executor.ExecuteOperationRecovery(context.Background(), operationExecRecord("op_two", operations.OperationRepoCreate, "allocate"), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("second ExecuteOperationRecovery: %v", err)
	}
	if !store.now.Equal(second) {
		t.Fatalf("second commit time = %v, want refreshed clock time %v", store.now, second)
	}
	if store.calls != 2 {
		t.Fatalf("store calls = %d, want 2", store.calls)
	}
}

func TestExecutorReturnsCommitFailure(t *testing.T) {
	commitErr := errors.New("commit failed")
	store := &fakeCommitStore{err: commitErr}
	handler := HandlerFunc(func(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan, committer Committer) error {
		_, err := committer.Commit(ctx, record.SanitizedForPersistence(), audit.Event{EventID: "audit-1", Type: audit.EventTypeRepoCreate, OperationID: record.ID, Outcome: audit.OutcomeFailed})
		return err
	})
	executor := newTestExecutor(t, store, Registration{OperationType: operations.OperationRepoCreate, Phase: "allocate", Handler: handler})

	err := executor.ExecuteOperationRecovery(context.Background(), operationExecRecord("op_commit_fail", operations.OperationRepoCreate, "allocate"), recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry})
	if !errors.Is(err, commitErr) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want commit error", err)
	}
}

func TestExecutorRejectsDuplicateCommit(t *testing.T) {
	store := &fakeCommitStore{}
	handler := HandlerFunc(func(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan, committer Committer) error {
		if _, err := committer.Commit(ctx, record.SanitizedForPersistence(), audit.Event{EventID: "audit-1", Type: audit.EventTypeRepoCreate, OperationID: record.ID}); err != nil {
			return err
		}
		_, err := committer.Commit(ctx, record.SanitizedForPersistence(), audit.Event{EventID: "audit-2", Type: audit.EventTypeRepoCreate, OperationID: record.ID})
		return err
	})
	executor := newTestExecutor(t, store, Registration{OperationType: operations.OperationRepoCreate, Phase: "allocate", Handler: handler})

	err := executor.ExecuteOperationRecovery(context.Background(), operationExecRecord("op_duplicate_commit", operations.OperationRepoCreate, "allocate"), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, ErrOperationAlreadyCommitted) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want duplicate commit error", err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want one durable commit", store.calls)
	}
}

func TestOperationExecPackageDoesNotImportWorkerOrStorageIntegrations(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package files: %v", err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s imports: %v", name, err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, forbidden := range []string{"/cmd", "/jvs", "/webdav", "/mount", "/storage"} {
				if strings.Contains(path, forbidden) {
					t.Fatalf("%s imports forbidden integration package %q", name, path)
				}
			}
		}
	}
}

type operationExecContextKey string

type recordingHandler struct {
	calls     int
	ctx       context.Context
	record    operations.OperationRecord
	plan      recovery.RecoveryPlan
	returnErr error
}

func (handler *recordingHandler) HandleOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan, _ Committer) error {
	handler.calls++
	handler.ctx = ctx
	handler.record = record
	handler.plan = plan
	return handler.returnErr
}

type fakeCommitStore struct {
	calls        int
	ctx          context.Context
	record       operations.SanitizedOperationRecord
	owner        string
	now          time.Time
	event        audit.Event
	err          error
	returnRecord operations.OperationRecord
}

func (store *fakeCommitStore) CommitOperationWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	store.calls++
	store.ctx = ctx
	store.record = record
	store.owner = owner
	store.now = now
	store.event = event
	if store.err != nil {
		return operations.OperationRecord{}, store.err
	}
	if store.returnRecord.ID != "" {
		return store.returnRecord, nil
	}
	return record.Record(), nil
}

func newTestExecutor(t *testing.T, store *fakeCommitStore, registration Registration) *Executor {
	t.Helper()
	return newTestExecutorWithConfig(t, Config{
		CommitStore:   store,
		Owner:         "worker-1",
		Now:           operationExecTestNow(),
		Registrations: []Registration{registration},
	})
}

func newTestExecutorWithConfig(t *testing.T, config Config) *Executor {
	t.Helper()
	executor, err := NewExecutor(config)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	return executor
}

func operationExecRecord(id string, typ operations.OperationType, phase string) operations.OperationRecord {
	return operations.OperationRecord{
		ID:           id,
		Type:         typ,
		State:        operations.OperationStateRunning,
		Phase:        phase,
		InputSummary: map[string]any{"safe": "value"},
	}
}

func operationExecTestNow() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}
