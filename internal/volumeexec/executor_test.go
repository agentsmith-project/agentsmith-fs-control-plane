package volumeexec

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestExecutorCommitsVolumeOperationAndAudit(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	store := &fakeVolumeCommitStore{}
	executor, err := NewExecutor(Config{CommitStore: store, Owner: "worker-a", Now: now, AuditEventID: func() string { return "evt_volume" }})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	record := volumeRecord(now)
	record.State = operations.OperationStateRunning
	record.LeaseOwner = "worker-a"
	expires := now.Add(time.Minute)
	record.LeaseExpiresAt = &expires

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.calls != 1 || store.volume.ID != "vol_123" {
		t.Fatalf("commit = %d volume %#v", store.calls, store.volume)
	}
	operation := store.record.Record()
	if operation.State != operations.OperationStateSucceeded || operation.Phase != operations.OperationPhaseVolumeEnsureCommitted {
		t.Fatalf("operation = %#v, want succeeded committed", operation)
	}
	if store.event.Type != audit.EventTypeVolumeEnsure || store.event.Outcome != audit.OutcomeSucceeded || store.event.Resource.ID != "vol_123" {
		t.Fatalf("event = %#v, want volume ensure succeeded", store.event)
	}
}

func TestExecutorRejectsWrongTypePhaseAndSummaryBeforeStore(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		edit func(*operations.OperationRecord)
	}{
		{name: "wrong type", edit: func(record *operations.OperationRecord) { record.Type = operations.OperationRepoCreate }},
		{name: "wrong phase", edit: func(record *operations.OperationRecord) { record.Phase = "other" }},
		{name: "namespace-bound operation", edit: func(record *operations.OperationRecord) { record.NamespaceID = "ns_alpha01" }},
		{name: "resource mismatch", edit: func(record *operations.OperationRecord) { record.Resource.ID = "vol_other" }},
		{name: "secret summary", edit: func(record *operations.OperationRecord) {
			record.InputSummary["capabilities"].(map[string]any)["metadata_url"] = "secret"
		}},
		{name: "top-level credential ref", edit: func(record *operations.OperationRecord) {
			record.InputSummary["credential_ref"] = "secret://volume"
		}},
		{name: "top-level raw path", edit: func(record *operations.OperationRecord) {
			record.InputSummary["raw_path"] = "/secret/raw"
		}},
		{name: "top-level unknown field", edit: func(record *operations.OperationRecord) {
			record.InputSummary["unexpected"] = "secret-value"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeVolumeCommitStore{}
			executor, _ := NewExecutor(Config{CommitStore: store, Owner: "worker-a", Now: now, AuditEventID: func() string { return "evt_volume" }})
			record := volumeRecord(now)
			record.State = operations.OperationStateRunning
			record.LeaseOwner = "worker-a"
			expires := now.Add(time.Minute)
			record.LeaseExpiresAt = &expires
			tt.edit(&record)
			err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
			if err == nil {
				t.Fatal("ExecuteOperationRecovery succeeded, want error")
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/secret/raw") {
				t.Fatalf("error leaked sensitive input summary value: %v", err)
			}
			if store.calls != 0 {
				t.Fatalf("commit calls = %d, want 0", store.calls)
			}
		})
	}
}

func volumeRecord(now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:               "op_volume",
		Type:             operations.OperationVolumeEnsure,
		State:            operations.OperationStateQueued,
		Phase:            operations.OperationPhaseVolumeEnsureValidate,
		IdempotencyScope: operations.NewIdempotencyScope("agentsmith-api", "", operations.OperationVolumeEnsure, "idem_volume").String(),
		IdempotencyKey:   "idem_volume",
		RequestHash:      "sha256:volume",
		CorrelationID:    "corr-volume",
		CallerService:    "agentsmith-api",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-volume"},
		Resource:         operations.ResourceRef{Type: "volume", ID: "vol_123"},
		InputSummary: map[string]any{
			"volume_id":       "vol_123",
			"backend":         "juicefs",
			"isolation_class": "shared",
			"status":          "active",
			"capabilities":    map[string]any{"webdav_export": true, "workload_mount": true, "jvs_external_control_root": true, "directory_quota": false},
		},
		CreatedAt: now.Add(-time.Hour),
	}
}

type fakeVolumeCommitStore struct {
	calls  int
	volume resources.Volume
	record operations.SanitizedOperationRecord
	event  audit.Event
}

func (store *fakeVolumeCommitStore) CommitVolumeEnsureWithLease(_ context.Context, volume resources.Volume, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (resources.Volume, operations.OperationRecord, error) {
	store.calls++
	store.volume = volume
	store.record = record
	store.event = event
	return volume, record.Record(), nil
}
