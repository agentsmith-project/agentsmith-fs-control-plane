package namespacebindingexec

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestExecutorSupportsOnlyNamespaceVolumeBindingPutValidatePhase(t *testing.T) {
	executor := newTestExecutor(t, &fakeCommitStore{}, bindingExecNow(), func() string { return "evt_binding" })
	tests := []struct {
		name      string
		record    operations.OperationRecord
		plan      recovery.RecoveryPlan
		supported bool
		reason    string
	}{
		{name: "binding put validate", record: bindingExecRecord("op_supported"), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, supported: true},
		{name: "binding put retry", record: bindingExecRecord("op_retry"), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry}, supported: true},
		{name: "binding put reclaim", record: bindingExecRecord("op_reclaim"), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionReclaim}, supported: true},
		{name: "wrong type", record: func() operations.OperationRecord {
			record := bindingExecRecord("op_wrong_type")
			record.Type = operations.OperationNamespaceUpsert
			return record
		}(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, reason: "unsupported_namespace_volume_binding_put_operation"},
		{name: "wrong phase", record: func() operations.OperationRecord {
			record := bindingExecRecord("op_wrong_phase")
			record.Phase = "other"
			return record
		}(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, reason: "unsupported_namespace_volume_binding_put_phase"},
		{name: "wrong action", record: bindingExecRecord("op_wrong_action"), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionFinalizeCancellation}, reason: "unsupported_namespace_volume_binding_put_recovery_action"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			support := executor.SupportsOperationRecovery(context.Background(), tt.record, tt.plan)
			if support.Supported != tt.supported {
				t.Fatalf("Supported = %v, want %v", support.Supported, tt.supported)
			}
			if tt.reason != "" && support.Reason != tt.reason {
				t.Fatalf("Reason = %q, want %q", support.Reason, tt.reason)
			}
		})
	}
}

func TestExecutorClaimRetryReclaimCommitBindingOperationAndAuditAtomically(t *testing.T) {
	for _, action := range []recovery.RecoveryAction{recovery.RecoveryActionClaimable, recovery.RecoveryActionRetry, recovery.RecoveryActionReclaim} {
		t.Run(string(action), func(t *testing.T) {
			now := bindingExecNow()
			store := &fakeCommitStore{}
			executor := newTestExecutor(t, store, now, func() string { return "evt_binding_" + string(action) })
			record := bindingExecRecord("op_binding_" + string(action))
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
			if call.binding.NamespaceID != record.NamespaceID || call.binding.DefaultVolumeID != "vol_123" || call.binding.Status != resources.NamespaceStatusActive {
				t.Fatalf("binding = %#v, want active ns/vol", call.binding)
			}
			if !call.binding.CreatedAt.Equal(record.CreatedAt) || !call.binding.UpdatedAt.Equal(now) {
				t.Fatalf("binding times = %v/%v, want record created/now", call.binding.CreatedAt, call.binding.UpdatedAt)
			}
			operation := call.record.Record()
			if operation.ID != record.ID || operation.Type != operations.OperationNamespaceVolumeBindingPut || operation.State != operations.OperationStateSucceeded || operation.Phase != operations.OperationPhaseNamespaceVolumeBindingPutCommitted {
				t.Fatalf("operation = %#v, want binding succeeded committed", operation)
			}
			if operation.NamespaceID != record.NamespaceID || operation.Resource != record.Resource {
				t.Fatalf("operation namespace/resource = %q/%#v", operation.NamespaceID, operation.Resource)
			}
			event := call.event
			if event.EventID == "" || event.Type != audit.EventTypeNamespaceVolumeBindingPut || event.Outcome != audit.OutcomeSucceeded {
				t.Fatalf("event = %#v, want binding succeeded audit", event)
			}
			if event.OperationID != operation.ID || event.Resource.Type != "namespace_volume_binding" || event.Resource.ID != record.NamespaceID || event.Resource.NamespaceID != record.NamespaceID {
				t.Fatalf("event operation/resource = %q/%#v", event.OperationID, event.Resource)
			}
			if event.CallerService != operation.CallerService || event.CorrelationID != operation.CorrelationID {
				t.Fatalf("event caller/correlation = %q/%q", event.CallerService, event.CorrelationID)
			}
		})
	}
}

func TestExecutorCommitsFailedOperationWhenDefaultVolumeIsMissing(t *testing.T) {
	now := bindingExecNow()
	store := &fakeCommitStore{volumeErr: sql.ErrNoRows}
	executor := newTestExecutor(t, store, now, func() string { return "evt_binding_failed" })
	record := bindingExecRecord("op_binding_missing_volume")
	record.State = operations.OperationStateRunning
	expiresAt := now.Add(5 * time.Minute)
	record.LeaseOwner = "worker-a"
	record.LeaseExpiresAt = &expiresAt
	record.Attempt = 2

	err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.commitCalls != 0 {
		t.Fatalf("success commit calls = %d, want 0", store.commitCalls)
	}
	if store.failedCalls != 1 {
		t.Fatalf("failure commit calls = %d, want 1", store.failedCalls)
	}
	call := store.lastFailedCall
	if call.owner != "worker-a" || !call.now.Equal(now) {
		t.Fatalf("failure owner/now = %q/%v, want worker-a/%v", call.owner, call.now, now)
	}
	operation := call.record.Record()
	if operation.ID != record.ID || operation.State != operations.OperationStateFailed || operation.Phase != operations.OperationPhaseNamespaceVolumeBindingPutValidate {
		t.Fatalf("failed operation = %#v, want failed validate operation", operation)
	}
	if operation.Error == nil || operation.Error.Code != "NAMESPACE_VOLUME_BINDING_VOLUME_NOT_ACTIVE" || operation.Error.Retryable {
		t.Fatalf("operation error = %#v, want non-retryable missing volume error", operation.Error)
	}
	if operation.Error.Details["default_volume_id"] != "vol_123" || operation.Error.Details["namespace_id"] != record.NamespaceID {
		t.Fatalf("operation error details = %#v, want namespace/default volume ids", operation.Error.Details)
	}
	if operation.FinishedAt == nil || !operation.FinishedAt.Equal(now) {
		t.Fatalf("finished_at = %v, want %v", operation.FinishedAt, now)
	}
	if call.event.Type != audit.EventTypeNamespaceVolumeBindingPut || call.event.Outcome != audit.OutcomeFailed || call.event.Reason != "namespace_volume_binding_put_failed" {
		t.Fatalf("failure audit = %#v, want failed namespace binding audit", call.event)
	}
	if call.event.OperationID != record.ID || call.event.Resource.ID != record.NamespaceID || call.event.Details["default_volume_id"] != "vol_123" {
		t.Fatalf("failure audit linkage/details = %#v/%#v, want operation and default volume", call.event.Resource, call.event.Details)
	}
}

func TestExecutorRejectsInvalidRecordPlanTimeEventIDAndSummaryBeforeStoreCalls(t *testing.T) {
	now := bindingExecNow()
	baseRecord := bindingExecRecord("op_invalid")
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
		{name: "unsupported plan", record: baseRecord, plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionFinalizeCancellation}, now: now, eventID: func() string { return "evt_binding" }},
		{name: "unsupported type", record: func() operations.OperationRecord {
			record := baseRecord
			record.Type = operations.OperationRepoCreate
			return record
		}(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_binding" }},
		{name: "unsupported phase", record: func() operations.OperationRecord { record := baseRecord; record.Phase = "other"; return record }(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_binding" }},
		{name: "resource mismatch", record: func() operations.OperationRecord {
			record := baseRecord
			record.Resource.ID = "ns_other01"
			return record
		}(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_binding" }},
		{name: "missing caller", record: func() operations.OperationRecord { record := baseRecord; record.CallerService = ""; return record }(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_binding" }},
		{name: "missing actor", record: func() operations.OperationRecord { record := baseRecord; record.AuthorizedActor.ID = ""; return record }(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_binding" }},
		{name: "zero now from clock", record: baseRecord, plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, clock: func() time.Time { return time.Time{} }, eventID: func() string { return "evt_binding" }},
		{name: "blank event id", record: baseRecord, plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return " \t" }},
		{name: "missing summary", record: func() operations.OperationRecord { record := baseRecord; record.InputSummary = nil; return record }(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_binding" }},
		{name: "summary namespace mismatch", record: func() operations.OperationRecord {
			record := baseRecord
			record.InputSummary["namespace_id"] = "ns_other01"
			return record
		}(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_binding" }},
		{name: "secret-like summary rejected", record: func() operations.OperationRecord {
			record := baseRecord
			record.InputSummary["export_policy"].(map[string]any)["credential_ref"] = "secret-token"
			return record
		}(), plan: recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}, now: now, eventID: func() string { return "evt_binding" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeCommitStore{}
			executor, err := NewExecutor(Config{CommitStore: store, Owner: "worker-a", Now: tt.now, Clock: tt.clock, AuditEventID: tt.eventID})
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
			if strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("error leaked secret summary: %v", err)
			}
		})
	}
}

func TestExecutorPropagatesStoreErrors(t *testing.T) {
	now := bindingExecNow()
	record := bindingExecRecord("op_error")
	record.State = operations.OperationStateRunning
	expiresAt := now.Add(5 * time.Minute)
	record.LeaseOwner = "worker-a"
	record.LeaseExpiresAt = &expiresAt

	for _, storeErr := range []error{operations.ErrLeaseUnavailable, errors.New("database unavailable")} {
		t.Run(storeErr.Error(), func(t *testing.T) {
			store := &fakeCommitStore{err: storeErr}
			executor := newTestExecutor(t, store, now, func() string { return "evt_binding" })

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

func newTestExecutor(t *testing.T, store *fakeCommitStore, now time.Time, eventID func() string) *Executor {
	t.Helper()
	executor, err := NewExecutor(Config{CommitStore: store, Owner: "worker-a", Now: now, AuditEventID: eventID})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	return executor
}

func bindingExecRecord(operationID string) operations.OperationRecord {
	now := bindingExecNow()
	return operations.OperationRecord{
		ID:               operationID,
		Type:             operations.OperationNamespaceVolumeBindingPut,
		State:            operations.OperationStateQueued,
		Phase:            operations.OperationPhaseNamespaceVolumeBindingPutValidate,
		IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationNamespaceVolumeBindingPut, "idem_binding").String(),
		IdempotencyKey:   "idem_binding",
		RequestHash:      operations.RequestHash("sha256:binding"),
		CorrelationID:    "corr-alpha",
		CallerService:    "product-caller",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "namespace_volume_binding", ID: "ns_alpha01"},
		NamespaceID:      "ns_alpha01",
		InputSummary:     bindingInputSummaryForTest("ns_alpha01"),
		CreatedAt:        now.Add(-time.Hour),
	}
}

func bindingInputSummaryForTest(namespaceID string) map[string]any {
	return map[string]any{
		"namespace_id":        namespaceID,
		"default_volume_id":   "vol_123",
		"allowed_callers":     []any{map[string]any{"caller_service": "product-caller", "roles": []any{"repo_admin", "operation_inspector"}}},
		"quota_bytes_default": float64(4096),
		"export_policy":       map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600)},
		"lifecycle_policy":    map[string]any{"tombstone_retention_seconds": float64(604800), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
		"mount_policy":        map[string]any{"workload_mount_enabled": true, "workload_mount_requires_external_control_root": true, "allow_privileged_workload": false},
		"template_policy":     map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
		"status":              "active",
	}
}

func bindingExecNow() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}

type fakeCommitStore struct {
	commitCalls    int
	lastCall       bindingCommitCall
	err            error
	namespace      resources.Namespace
	namespaceErr   error
	volume         resources.Volume
	volumeErr      error
	failedCalls    int
	lastFailedCall bindingFailureCall
	failedErr      error
}

type bindingCommitCall struct {
	binding resources.NamespaceVolumeBinding
	record  operations.SanitizedOperationRecord
	owner   string
	now     time.Time
	event   audit.Event
}

type bindingFailureCall struct {
	record operations.SanitizedOperationRecord
	owner  string
	now    time.Time
	event  audit.Event
}

func (store *fakeCommitStore) GetNamespace(_ context.Context, namespaceID string) (resources.Namespace, error) {
	if store.namespaceErr != nil {
		return resources.Namespace{}, store.namespaceErr
	}
	namespace := store.namespace
	if namespace.ID == "" {
		namespace = activeNamespaceForBindingTest(namespaceID)
	}
	if namespace.ID != namespaceID {
		return resources.Namespace{}, sql.ErrNoRows
	}
	return namespace, nil
}

func (store *fakeCommitStore) GetVolume(_ context.Context, volumeID string) (resources.Volume, error) {
	if store.volumeErr != nil {
		return resources.Volume{}, store.volumeErr
	}
	volume := store.volume
	if volume.ID == "" {
		volume = activeVolumeForBindingTest(volumeID)
	}
	if volume.ID != volumeID {
		return resources.Volume{}, sql.ErrNoRows
	}
	return volume, nil
}

func (store *fakeCommitStore) CommitNamespaceVolumeBindingPutWithLease(_ context.Context, binding resources.NamespaceVolumeBinding, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.NamespaceVolumeBinding, operations.OperationRecord, error) {
	store.commitCalls++
	store.lastCall = bindingCommitCall{binding: binding, record: record, owner: owner, now: now, event: event}
	if store.err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, store.err
	}
	return binding, record.Record(), nil
}

func (store *fakeCommitStore) CommitNamespaceVolumeBindingPutFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	store.failedCalls++
	store.lastFailedCall = bindingFailureCall{record: record, owner: owner, now: now, event: event}
	if store.failedErr != nil {
		return operations.OperationRecord{}, store.failedErr
	}
	return record.Record(), nil
}

func activeNamespaceForBindingTest(namespaceID string) resources.Namespace {
	now := bindingExecNow()
	return resources.Namespace{ID: namespaceID, Status: resources.NamespaceStatusActive, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
}

func activeVolumeForBindingTest(volumeID string) resources.Volume {
	now := bindingExecNow()
	return resources.Volume{
		ID:             volumeID,
		Backend:        resources.VolumeBackendJuiceFS,
		IsolationClass: resources.VolumeIsolationShared,
		Status:         resources.VolumeStatusActive,
		Capabilities:   map[string]any{"webdav_export": true, "workload_mount": true, "jvs_external_control_root": true, "directory_quota": false},
		CreatedAt:      now.Add(-time.Hour),
		UpdatedAt:      now,
	}
}
